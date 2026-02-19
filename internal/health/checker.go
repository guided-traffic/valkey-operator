// Package health provides health checking and cluster state assessment
// for Valkey and Sentinel instances.
package health

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// ClusterState represents the observed state of the Valkey HA cluster.
type ClusterState struct {
	// MasterPod is the name of the pod currently acting as master.
	MasterPod string

	// MasterAddress is the address of the master.
	MasterAddress string

	// ReadyReplicas is the number of replicas that are ready and synced.
	ReadyReplicas int32

	// TotalReplicas is the total number of expected replicas (excluding master).
	TotalReplicas int32

	// AllSynced is true when all replicas have completed sync with the master.
	AllSynced bool

	// SentinelMonitoring is true when sentinel instances agree on the master.
	SentinelMonitoring bool

	// Error holds any error encountered during health check.
	Error error
}

// Checker performs health checks on Valkey and Sentinel instances.
type Checker struct {
	client client.Client
}

// NewChecker creates a new health checker.
func NewChecker(c client.Client) *Checker {
	return &Checker{client: c}
}

// CheckCluster performs a full health check on the Valkey HA cluster.
func (h *Checker) CheckCluster(ctx context.Context, v *vkov1.Valkey) *ClusterState {
	logger := log.FromContext(ctx)
	state := &ClusterState{
		TotalReplicas: v.Spec.Replicas - 1, // Minus master.
	}

	// Build TLS config once for all health check connections.
	tlsConfig, err := h.buildTLSConfig(ctx, v, builder.ValkeyTLSSecretName(v))
	if err != nil {
		logger.Info("Could not build TLS config for health check", "error", err)
		state.Error = fmt.Errorf("TLS config: %w", err)
		return state
	}

	// Find the master by querying each pod.
	masterPod, masterAddr, err := h.findMaster(ctx, v, tlsConfig)
	if err != nil {
		logger.Info("Could not find master via INFO replication", "error", err)
		state.Error = err
		return state
	}

	state.MasterPod = masterPod
	state.MasterAddress = masterAddr

	// Check master replication info.
	masterClient := h.newValkeyClient(masterAddr, tlsConfig)
	masterInfo, err := masterClient.InfoReplication()
	if err != nil {
		logger.Info("Could not get master replication info", "pod", masterPod, "error", err)
		state.Error = fmt.Errorf("master replication info: %w", err)
		return state
	}

	// Count ready replicas from master's perspective.
	// #nosec G115 — ConnectedSlaves is bounded by the number of pods in the cluster, safe to convert.
	state.ReadyReplicas = int32(min(masterInfo.ConnectedSlaves, int(state.TotalReplicas)))
	state.AllSynced = !masterInfo.MasterSyncInProgress && state.ReadyReplicas == state.TotalReplicas

	// Check sentinel view if sentinel is enabled.
	if v.IsSentinelEnabled() {
		state.SentinelMonitoring = h.checkSentinel(ctx, v)
	}

	return state
}

// PingPod sends a PING to a specific Valkey pod.
func (h *Checker) PingPod(ctx context.Context, v *vkov1.Valkey, podName string) error {
	port := int(builder.ServicePort(v))
	addr := podAddress(v, podName, port)

	tlsConfig, err := h.buildTLSConfig(ctx, v, builder.ValkeyTLSSecretName(v))
	if err != nil {
		return fmt.Errorf("TLS config for ping: %w", err)
	}

	c := h.newValkeyClient(addr, tlsConfig)
	return c.Ping()
}

// GetReplicationInfo returns the replication info for a specific Valkey pod.
func (h *Checker) GetReplicationInfo(ctx context.Context, v *vkov1.Valkey, podName string) (*valkeyclient.ReplicationInfo, error) {
	port := int(builder.ServicePort(v))
	addr := podAddress(v, podName, port)

	tlsConfig, err := h.buildTLSConfig(ctx, v, builder.ValkeyTLSSecretName(v))
	if err != nil {
		return nil, fmt.Errorf("TLS config for replication info: %w", err)
	}

	c := h.newValkeyClient(addr, tlsConfig)
	return c.InfoReplication()
}

// findMaster iterates over all Valkey pods and finds the one reporting role=master.
func (h *Checker) findMaster(ctx context.Context, v *vkov1.Valkey, tlsConfig *tls.Config) (string, string, error) {
	stsName := common.StatefulSetName(v, common.ComponentValkey)
	port := int(builder.ServicePort(v))

	for i := int32(0); i < v.Spec.Replicas; i++ {
		podName := fmt.Sprintf("%s-%d", stsName, i)
		addr := podAddress(v, podName, port)

		// Check if pod is running first.
		pod := &corev1.Pod{}
		err := h.client.Get(ctx, types.NamespacedName{
			Name:      podName,
			Namespace: v.Namespace,
		}, pod)
		if err != nil || pod.Status.Phase != corev1.PodRunning {
			continue
		}

		c := h.newValkeyClient(addr, tlsConfig)
		info, err := c.InfoReplication()
		if err != nil {
			continue
		}

		if info.Role == "master" {
			return podName, addr, nil
		}
	}

	return "", "", fmt.Errorf("no master found among %d pods", v.Spec.Replicas)
}

// checkSentinel checks if sentinel instances are monitoring the cluster correctly.
func (h *Checker) checkSentinel(ctx context.Context, v *vkov1.Valkey) bool {
	logger := log.FromContext(ctx)
	sentinelStsName := common.StatefulSetName(v, common.ComponentSentinel)
	monitorName := builder.SentinelMonitorName(v)

	sentinelReplicas := int32(3)
	if v.Spec.Sentinel != nil && v.Spec.Sentinel.Replicas > 0 {
		sentinelReplicas = v.Spec.Sentinel.Replicas
	}

	// Build TLS config for sentinel connections (uses sentinel TLS secret).
	tlsConfig, err := h.buildTLSConfig(ctx, v, builder.SentinelTLSSecretName(v))
	if err != nil {
		logger.Info("Could not build TLS config for sentinel health check", "error", err)
		return false
	}

	// Sentinel port: always 26379 (TLS or not — sentinel uses tls-port 26379).
	port := builder.SentinelPort

	agreeing := 0
	for i := int32(0); i < sentinelReplicas; i++ {
		podName := fmt.Sprintf("%s-%d", sentinelStsName, i)
		addr := podAddress(v, podName, port)

		c := h.newValkeyClient(addr, tlsConfig)
		masterInfo, err := c.SentinelMaster(monitorName)
		if err != nil {
			logger.V(1).Info("Sentinel not responding", "pod", podName, "error", err)
			continue
		}

		// Sentinel should report the master with "master" flag and no error flags.
		if masterInfo.Flags == "master" {
			agreeing++
		}
	}

	return agreeing > int(sentinelReplicas/2)
}

// buildTLSConfig constructs a tls.Config for connecting to TLS-enabled Valkey/Sentinel pods.
// It reads the CA certificate from the specified Kubernetes Secret.
// Returns nil (no TLS) if TLS is not enabled on the Valkey CR.
func (h *Checker) buildTLSConfig(ctx context.Context, v *vkov1.Valkey, secretName string) (*tls.Config, error) {
	if !v.IsTLSEnabled() {
		return nil, nil
	}

	// Read the TLS secret containing the CA certificate.
	secret := &corev1.Secret{}
	err := h.client.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: v.Namespace,
	}, secret)
	if err != nil {
		return nil, fmt.Errorf("reading TLS secret %s: %w", secretName, err)
	}

	caCert, ok := secret.Data["ca.crt"]
	if !ok {
		return nil, fmt.Errorf("TLS secret %s missing ca.crt", secretName)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA certificate from secret %s", secretName)
	}

	return &tls.Config{
		RootCAs:    certPool,
		MinVersion: tls.VersionTLS12,
	}, nil
}

// newValkeyClient creates a valkeyclient.Client, using TLS if tlsConfig is non-nil.
func (h *Checker) newValkeyClient(addr string, tlsConfig *tls.Config) *valkeyclient.Client {
	if tlsConfig != nil {
		return valkeyclient.NewTLS(addr, tlsConfig)
	}
	return valkeyclient.New(addr)
}

// podAddress returns the FQDN address for a pod using the headless service.
func podAddress(v *vkov1.Valkey, podName string, port int) string {
	component := common.ComponentValkey
	// Detect sentinel pods by name suffix.
	if len(podName) > 9 && podName[len(podName)-10:len(podName)-2] == "sentinel" {
		component = common.ComponentSentinel
	}

	headlessSvc := common.HeadlessServiceName(v, component)
	return fmt.Sprintf("%s.%s.%s.svc.cluster.local:%d",
		podName, headlessSvc, v.Namespace, port)
}

// PodAddressForComponent returns the FQDN for a pod given an explicit component.
func PodAddressForComponent(v *vkov1.Valkey, podName, component string, port int) string {
	headlessSvc := common.HeadlessServiceName(v, component)
	return fmt.Sprintf("%s.%s.%s.svc.cluster.local:%d",
		podName, headlessSvc, v.Namespace, port)
}
