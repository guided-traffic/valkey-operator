// Package health provides health checking and cluster state assessment
// for Valkey and Sentinel instances.
package health

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"sort"
	"sync"

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

	// SentinelPeers maps each sentinel pod that answered to the number of other
	// Sentinels it knows. Empty when Sentinel is disabled or no sentinel answered.
	SentinelPeers map[string]int

	// SentinelPeersExpected is how many other Sentinels each of them should know:
	// one less than the configured replica count.
	SentinelPeersExpected int

	// Error holds any error encountered during health check.
	Error error
}

// Checker performs health checks on Valkey and Sentinel instances.
type Checker struct {
	client client.Client

	// NewValkeyClientFn builds the client used for every probe this Checker
	// sends. It is nil in production, where newValkeyClient builds the client
	// itself; tests set it to point the probes at a local responder. Mirrors
	// ValkeyReconciler.NewValkeyClientFn.
	NewValkeyClientFn func(addr, password string, tlsConfig *tls.Config) *valkeyclient.Client
}

// NewChecker creates a new health checker.
func NewChecker(c client.Client) *Checker {
	return &Checker{client: c}
}

// readAuthPassword reads the Valkey auth password from the configured Secret.
// Returns an empty string if authentication is not configured or if the secret
// cannot be read (in which case connections are attempted without auth).
func (h *Checker) readAuthPassword(ctx context.Context, v *vkov1.Valkey) string {
	if !v.IsAuthEnabled() {
		return ""
	}
	secret := &corev1.Secret{}
	if err := h.client.Get(ctx, types.NamespacedName{
		Name:      v.Spec.Auth.SecretName,
		Namespace: v.Namespace,
	}, secret); err != nil {
		return ""
	}
	return string(secret.Data[v.Spec.Auth.SecretPasswordKey])
}

// CheckCluster performs a full health check on the Valkey HA cluster.
func (h *Checker) CheckCluster(ctx context.Context, v *vkov1.Valkey) *ClusterState {
	logger := log.FromContext(ctx)
	state := &ClusterState{
		TotalReplicas: v.Spec.Replicas - 1, // Minus master.
	}

	// Read auth password and TLS config once for all health check connections.
	password := h.readAuthPassword(ctx, v)

	// Build TLS config once for all health check connections.
	tlsConfig, err := h.buildTLSConfig(ctx, v, builder.ValkeyTLSSecretName(v))
	if err != nil {
		logger.Info("Could not build TLS config for health check", "error", err)
		state.Error = fmt.Errorf("TLS config: %w", err)
		return state
	}

	// Find the master by querying each pod.
	masterPod, masterAddr, err := h.findMaster(ctx, v, password, tlsConfig)
	if err != nil {
		logger.Info("Could not find master via INFO replication", "error", err)
		state.Error = err
		return state
	}

	state.MasterPod = masterPod
	state.MasterAddress = masterAddr

	// Check master replication info.
	masterClient := h.newValkeyClient(masterAddr, password, tlsConfig)
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
		observed := h.observeSentinels(ctx, v)
		state.SentinelMonitoring = observed.monitoring()
		state.SentinelPeers = observed.peers
		state.SentinelPeersExpected = observed.expectedPeers
	}

	return state
}

// PingPod sends a PING to a specific Valkey pod.
func (h *Checker) PingPod(ctx context.Context, v *vkov1.Valkey, podName string) error {
	password := h.readAuthPassword(ctx, v)
	addr := valkeyPodAddress(v, podName)

	tlsConfig, err := h.buildTLSConfig(ctx, v, builder.ValkeyTLSSecretName(v))
	if err != nil {
		return fmt.Errorf("TLS config for ping: %w", err)
	}

	c := h.newValkeyClient(addr, password, tlsConfig)
	return c.Ping()
}

// GetReplicationInfo returns the replication info for a specific Valkey pod.
func (h *Checker) GetReplicationInfo(ctx context.Context, v *vkov1.Valkey, podName string) (*valkeyclient.ReplicationInfo, error) {
	password := h.readAuthPassword(ctx, v)
	addr := valkeyPodAddress(v, podName)

	tlsConfig, err := h.buildTLSConfig(ctx, v, builder.ValkeyTLSSecretName(v))
	if err != nil {
		return nil, fmt.Errorf("TLS config for replication info: %w", err)
	}

	c := h.newValkeyClient(addr, password, tlsConfig)
	return c.InfoReplication()
}

// masterCandidate represents a pod reporting role=master during findMaster discovery.
type masterCandidate struct {
	podName         string
	addr            string
	connectedSlaves int
}

// probeMasterRole asks one pod whether it is the master and returns a candidate
// when it says yes. A pod the API server does not report as Running is not
// dialled at all, and a pod that does not answer is not a candidate.
func (h *Checker) probeMasterRole(
	ctx context.Context, v *vkov1.Valkey, podName, addr string, password string, tlsConfig *tls.Config,
) *masterCandidate {
	pod := &corev1.Pod{}
	err := h.client.Get(ctx, types.NamespacedName{
		Name:      podName,
		Namespace: v.Namespace,
	}, pod)
	if err != nil || pod.Status.Phase != corev1.PodRunning {
		return nil
	}

	c := h.newValkeyClient(addr, password, tlsConfig)
	info, err := c.InfoReplication()
	if err != nil || info.Role != "master" {
		return nil
	}

	return &masterCandidate{podName: podName, addr: addr, connectedSlaves: info.ConnectedSlaves}
}

// findMaster probes all Valkey pods and returns the one reporting role=master.
// When multiple pods report as master (split-brain), a warning is logged and the
// one with the most connected slaves is preferred (it is the active master serving
// real data); ties are broken by the lowest ordinal.
//
// The probes run concurrently. Sequentially they cost up to one client timeout
// (5 s, internal/valkeyclient) per pod, so a cluster whose pods stopped answering
// held the reconcile worker for replicas x 5 s — latency that every other Valkey CR
// inherited through the shared work queue
// (docs/adr/0019-reconcile-concurrency-and-the-cost-of-a-stuck-pass.md).
//
// Concurrency must not make the answer depend on which pod replies first: the
// results are collected into a slice indexed by ordinal, never appended in
// completion order, and the sort below is stable with an explicit ordinal
// tie-break. Before this the tie-break was sort.Slice, which is not stable — with
// two masters reporting the same slave count the winner was already unspecified.
func (h *Checker) findMaster(ctx context.Context, v *vkov1.Valkey, password string, tlsConfig *tls.Config) (string, string, error) {
	logger := log.FromContext(ctx)
	stsName := common.StatefulSetName(v, common.ComponentValkey)

	found := make([]*masterCandidate, v.Spec.Replicas)
	var wg sync.WaitGroup

	for i := int32(0); i < v.Spec.Replicas; i++ {
		wg.Add(1)
		go func(idx int32) {
			defer wg.Done()
			podName := fmt.Sprintf("%s-%d", stsName, idx)
			found[idx] = h.probeMasterRole(ctx, v, podName, valkeyPodAddress(v, podName), password, tlsConfig)
		}(i)
	}
	wg.Wait()

	candidates := make([]masterCandidate, 0, len(found))
	for _, c := range found {
		if c != nil {
			candidates = append(candidates, *c)
		}
	}

	if len(candidates) == 0 {
		return "", "", fmt.Errorf("no master found among %d pods", v.Spec.Replicas)
	}

	if len(candidates) > 1 {
		logger.Info("WARNING: Multiple masters detected (split-brain)",
			"count", len(candidates), "candidates", masterCandidateNames(candidates))
		// Return the one with the most connected slaves — it is the active master
		// serving real replicas and preserving client data. Stable, so equal slave
		// counts keep the ordinal order the slice was built in.
		sort.SliceStable(candidates, func(i, j int) bool {
			return candidates[i].connectedSlaves > candidates[j].connectedSlaves
		})
	}

	return candidates[0].podName, candidates[0].addr, nil
}

// masterCandidateNames returns the pod names from a slice of masterCandidates.
func masterCandidateNames(candidates []masterCandidate) []string {
	names := make([]string, len(candidates))
	for i, c := range candidates {
		names[i] = c.podName
	}
	return names
}

// sentinelObservation is what one pass of SENTINEL MASTER over every sentinel pod
// saw: how many of them report a healthy master, and how many other Sentinels each
// of them knows.
//
// Both answers come from the same reply. Peer drift would otherwise cost a second
// connection per sentinel per reconcile pass for a field that is already on the
// wire.
type sentinelObservation struct {
	// agreeing is the number of sentinels reporting the master with no error flags.
	agreeing int
	// replicas is the configured sentinel replica count.
	replicas int32
	// expectedPeers is how many other Sentinels each of them should know.
	expectedPeers int
	// peers maps a sentinel pod name to its num-other-sentinels. A sentinel that
	// did not answer is absent rather than zero.
	peers map[string]int
}

// monitoring reports whether a majority of the configured sentinels sees a healthy
// master.
func (o sentinelObservation) monitoring() bool {
	return o.agreeing > int(o.replicas/2)
}

// observeSentinels queries every sentinel pod once and reports what it saw.
func (h *Checker) observeSentinels(ctx context.Context, v *vkov1.Valkey) sentinelObservation {
	logger := log.FromContext(ctx)
	sentinelStsName := common.StatefulSetName(v, common.ComponentSentinel)
	monitorName := builder.SentinelMonitorName(v)

	sentinelReplicas := int32(3)
	if v.Spec.Sentinel != nil && v.Spec.Sentinel.Replicas > 0 {
		sentinelReplicas = v.Spec.Sentinel.Replicas
	}

	// Read auth password for sentinel connections (Sentinel uses the same requirepass).
	// When sentinel auth is disabled, Sentinel does not have requirepass, so no password.
	sentinelPassword := ""
	if !v.IsSentinelAuthDisabled() {
		sentinelPassword = h.readAuthPassword(ctx, v)
	}

	// Build TLS config for sentinel connections (uses sentinel TLS secret).
	observation := sentinelObservation{
		replicas:      sentinelReplicas,
		expectedPeers: int(sentinelReplicas) - 1,
		peers:         map[string]int{},
	}

	tlsConfig, err := h.buildTLSConfig(ctx, v, builder.SentinelTLSSecretName(v))
	if err != nil {
		logger.Info("Could not build TLS config for sentinel health check", "error", err)
		return observation
	}

	for i := int32(0); i < sentinelReplicas; i++ {
		podName := fmt.Sprintf("%s-%d", sentinelStsName, i)
		addr := sentinelPodAddress(v, podName)

		c := h.newValkeyClient(addr, sentinelPassword, tlsConfig)
		masterInfo, err := c.SentinelMaster(monitorName)
		if err != nil {
			logger.V(1).Info("Sentinel not responding", "pod", podName, "error", err)
			continue
		}

		observation.peers[podName] = masterInfo.NumOtherSentinels

		// Sentinel should report the master with "master" flag and no error flags.
		if masterInfo.Flags == "master" {
			observation.agreeing++
		}
	}

	return observation
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

// newValkeyClient creates a valkeyclient.Client with the given TLS and auth settings.
func (h *Checker) newValkeyClient(addr, password string, tlsConfig *tls.Config) *valkeyclient.Client {
	if h.NewValkeyClientFn != nil {
		return h.NewValkeyClientFn(addr, password, tlsConfig)
	}
	if tlsConfig != nil && password != "" {
		return valkeyclient.NewTLSWithPassword(addr, tlsConfig, password)
	}
	if tlsConfig != nil {
		return valkeyclient.NewTLS(addr, tlsConfig)
	}
	if password != "" {
		return valkeyclient.NewWithPassword(addr, password)
	}
	return valkeyclient.New(addr)
}

// valkeyPodAddress returns the address of a data-tier pod: the Valkey headless
// Service and the Valkey client port, chosen in one place so they cannot
// disagree.
//
// The component is never derived from the pod name. It used to be, by testing a
// fixed-offset window of the name against "sentinel", and that guess was wrong in
// two directions at once: a data pod of a CR whose own name ends in "sentinel"
// (`term-no-sentinel-0`) was dialled through the Sentinel headless Service, and a
// Sentinel pod from ordinal 10 upward was dialled through the data one. Both
// resolve to nothing, so the operator went blind to its own pods -- loudly for
// PingPod (phase Error), silently for GetReplicationInfo and findMaster, which
// read an unreachable pod as "not the master".
//
// Every caller of these helpers already knows which tier it is addressing, and
// already picks the matching port. Pairing the two removes the only place that
// had to guess (docs/adr/0029-a-name-is-not-a-component.md, D1, D2).
func valkeyPodAddress(v *vkov1.Valkey, podName string) string {
	return PodAddressForComponent(v, podName, common.ComponentValkey, int(builder.ServicePort(v)))
}

// sentinelPodAddress returns the address of a Sentinel-tier pod: the Sentinel
// headless Service and the Sentinel port, chosen together for the reason
// valkeyPodAddress states.
func sentinelPodAddress(v *vkov1.Valkey, podName string) string {
	return PodAddressForComponent(v, podName, common.ComponentSentinel, sentinelPort(v))
}

// sentinelPort is the port Sentinel listens on: the TLS port when TLS is enabled
// (Sentinel is configured with the tls-port directive), the plaintext port
// otherwise.
func sentinelPort(v *vkov1.Valkey) int {
	if v.IsTLSEnabled() {
		return builder.SentinelTLSPort
	}
	return builder.SentinelPort
}

// PodAddressForComponent returns the FQDN for a pod given an explicit component.
//
// The component and the port belong together -- a Sentinel Service with a Valkey
// port resolves to nothing, and so does the converse. Callers inside this package
// use valkeyPodAddress or sentinelPodAddress, which pair them; a caller that uses
// this function directly owns that pairing itself.
func PodAddressForComponent(v *vkov1.Valkey, podName, component string, port int) string {
	headlessSvc := common.HeadlessServiceName(v, component)
	return fmt.Sprintf("%s.%s.%s.svc.cluster.local:%d",
		podName, headlessSvc, v.Namespace, port)
}
