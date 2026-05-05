package sidecar

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// ValkeyClientFactory creates Valkey clients for arbitrary addresses.
// This interface enables mocking in tests.
type ValkeyClientFactory interface {
	NewClient(addr string) ValkeyCommander
}

// ValkeyCommander provides the Valkey commands needed by the drain handler
// for sentinel and manual failover.
type ValkeyCommander interface {
	InfoReplication() (*valkeyclient.ReplicationInfo, error)
	SentinelFailover(name string) error
	ReplicaOf(host, port string) error
	Ping() error
}

// drainLog is the logger interface used by drain handler methods (compatible with logr.Logger).
type drainLog interface {
	Info(msg string, keysAndValues ...interface{})
	Error(err error, msg string, keysAndValues ...interface{})
}

// DrainHandler handles graceful failover on SIGTERM.
// When the pod is the master, it patches the label to "draining", triggers
// a failover (via Sentinel or manually), and waits for the role to change.
type DrainHandler struct {
	detector              RoleDetector
	patcher               PodPatcher
	clientFactory         ValkeyClientFactory
	sentinelClientFactory ValkeyClientFactory // for sentinel connections (may differ in auth)
	podName               string
	podNamespace          string
	sentinelEnabled       bool
	sentinelMonitor       string
	sentinelAddrs         []string
	headlessSvc           string
	replicas              int
	valkeyPort            string
}

// NewDrainHandlerWithDeps creates a DrainHandler with injected dependencies (for testing).
func NewDrainHandlerWithDeps(
	detector RoleDetector,
	patcher PodPatcher,
	clientFactory ValkeyClientFactory,
	sentinelClientFactory ValkeyClientFactory,
	podName, podNamespace string,
	sentinelEnabled bool,
	sentinelMonitor string,
	sentinelAddrs []string,
	headlessSvc string,
	replicas int,
	valkeyPort string,
) *DrainHandler {
	if sentinelClientFactory == nil {
		sentinelClientFactory = clientFactory
	}
	return &DrainHandler{
		detector:              detector,
		patcher:               patcher,
		clientFactory:         clientFactory,
		sentinelClientFactory: sentinelClientFactory,
		podName:               podName,
		podNamespace:          podNamespace,
		sentinelEnabled:       sentinelEnabled,
		sentinelMonitor:       sentinelMonitor,
		sentinelAddrs:         sentinelAddrs,
		headlessSvc:           headlessSvc,
		replicas:              replicas,
		valkeyPort:            valkeyPort,
	}
}

// Handle processes a SIGTERM signal. It detects the current role and triggers
// a graceful failover if the pod is the master. Returns nil when safe to exit.
func (d *DrainHandler) Handle(ctx context.Context) error {
	log := ctrl.Log.WithName("sidecar").WithName("drain")

	role, err := d.detector.DetectRole()
	if err != nil {
		log.Error(err, "failed to detect role during drain, exiting immediately")
		return nil
	}

	log.Info("drain handler invoked", "role", role)

	if role != common.RoleMaster {
		log.Info("not master, exiting immediately", "role", role)
		return nil
	}

	// 1. Patch label to "draining" to remove pod from -rw Service.
	if patchErr := d.patcher.PatchLabel(ctx, d.podNamespace, d.podName,
		common.LabelInstanceRole, common.RoleDraining); patchErr != nil {
		log.Error(patchErr, "failed to patch draining label, continuing with failover")
	} else {
		log.Info("label set to draining")
	}

	// 2. Trigger failover based on sentinel configuration.
	if d.sentinelEnabled {
		return d.sentinelFailover(ctx, log)
	}
	return d.manualFailover(ctx, log)
}

// sentinelFailover triggers failover via one of the known Sentinel instances.
func (d *DrainHandler) sentinelFailover(ctx context.Context, log drainLog) error {
	var lastErr error
	for _, addr := range d.sentinelAddrs {
		client := d.sentinelClientFactory.NewClient(addr)
		if err := client.SentinelFailover(d.sentinelMonitor); err != nil {
			log.Error(err, "sentinel failover command failed", "sentinel", addr)
			lastErr = err
			continue
		}
		log.Info("sentinel failover triggered", "sentinel", addr, "monitor", d.sentinelMonitor)
		return d.waitForRoleChange(ctx, log)
	}
	return fmt.Errorf("all sentinels failed to trigger failover: %w", lastErr)
}

// manualFailover promotes a synced replica to master without Sentinel.
// It discovers replicas via headless DNS, picks a synced one, promotes it,
// and reconfigures the remaining replicas.
func (d *DrainHandler) manualFailover(ctx context.Context, log drainLog) error {
	replicaAddr, err := d.findSyncedReplica(log)
	if err != nil {
		return fmt.Errorf("finding synced replica: %w", err)
	}

	replicaHost, _ := splitHostPort(replicaAddr)
	log.Info("promoting replica to master", "addr", replicaAddr)

	// Promote the chosen replica.
	client := d.clientFactory.NewClient(replicaAddr)
	if err := client.ReplicaOf("NO", "ONE"); err != nil {
		return fmt.Errorf("promoting replica %s: %w", replicaAddr, err)
	}

	// Reconfigure remaining replicas to follow the new master.
	d.reconfigureReplicas(replicaHost, replicaAddr, log)

	return d.waitForRoleChange(ctx, log)
}

// findSyncedReplica discovers replica pods via headless DNS and returns the
// address of the first replica that is fully synced with the master.
func (d *DrainHandler) findSyncedReplica(log drainLog) (string, error) {
	addrs := d.buildReplicaAddrs()
	for _, addr := range addrs {
		client := d.clientFactory.NewClient(addr)
		info, err := client.InfoReplication()
		if err != nil {
			log.Error(err, "failed to query replica", "addr", addr)
			continue
		}
		if isSyncedReplica(info) {
			return addr, nil
		}
	}
	return "", fmt.Errorf("no synced replica found among %d candidates", len(addrs))
}

// isSyncedReplica returns true if the replication info indicates a fully synced replica.
func isSyncedReplica(info *valkeyclient.ReplicationInfo) bool {
	return info.Role == valkeyRoleSlave && info.MasterLinkStatus == "up" && !info.MasterSyncInProgress
}

// reconfigureReplicas sends REPLICAOF to all remaining replicas so they follow
// the newly promoted master.
func (d *DrainHandler) reconfigureReplicas(newMasterHost, newMasterAddr string, log drainLog) {
	addrs := d.buildReplicaAddrs()
	for _, addr := range addrs {
		if addr == newMasterAddr {
			continue
		}
		client := d.clientFactory.NewClient(addr)
		if err := client.ReplicaOf(newMasterHost, d.valkeyPort); err != nil {
			log.Error(err, "failed to reconfigure replica", "addr", addr, "newMaster", newMasterHost)
		}
	}
}

// waitForRoleChange polls the local Valkey until this pod is no longer the master.
// If Valkey becomes unreachable (connection refused), the failover is considered
// complete — the process is already shutting down.
func (d *DrainHandler) waitForRoleChange(ctx context.Context, log drainLog) error {
	log.Info("waiting for local role to change from master")
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for failover completion: %w", ctx.Err())
		case <-ticker.C:
			role, err := d.detector.DetectRole()
			if err != nil {
				if isConnectionRefused(err) {
					log.Info("local Valkey unreachable, assuming failover complete")
					return nil
				}
				log.Error(err, "role detection failed during wait")
				continue
			}
			if role != common.RoleMaster {
				log.Info("role changed, failover successful", "newRole", role)
				return nil
			}
		}
	}
}

// isConnectionRefused returns true when the error indicates that the target
// is not accepting connections — i.e. the local Valkey process has already
// shut down.  Both the standard Go net error ("connection refused") and the
// custom valkey-client message ("… refused …") are matched.
func isConnectionRefused(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") || strings.Contains(msg, "refused")
}

// buildReplicaAddrs constructs the FQDN addresses for all Valkey pods
// in the StatefulSet, excluding the current pod.
func (d *DrainHandler) buildReplicaAddrs() []string {
	base := podBaseName(d.podName)
	addrs := make([]string, 0, d.replicas)
	for i := 0; i < d.replicas; i++ {
		name := fmt.Sprintf("%s-%d", base, i)
		if name == d.podName {
			continue
		}
		fqdn := fmt.Sprintf("%s.%s:%s", name, d.headlessSvc, d.valkeyPort)
		addrs = append(addrs, fqdn)
	}
	return addrs
}

// podBaseName extracts the StatefulSet base name from a pod name.
// For example, "test-0" returns "test", "my-cluster-1" returns "my-cluster".
func podBaseName(podName string) string {
	idx := strings.LastIndex(podName, "-")
	if idx < 0 {
		return podName
	}
	return podName[:idx]
}

// splitHostPort splits an address into host and port parts.
func splitHostPort(addr string) (string, string) {
	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		return addr, ""
	}
	return addr[:idx], addr[idx+1:]
}

// --- Production ValkeyClientFactory ---

// realValkeyClientFactory creates real Valkey clients with optional TLS and auth.
type realValkeyClientFactory struct {
	password  string
	tlsConfig *tls.Config
}

// newRealValkeyClientFactory creates a factory from the sidecar config.
func newRealValkeyClientFactory(cfg Config) (*realValkeyClientFactory, error) {
	factory := &realValkeyClientFactory{
		password: cfg.Password,
	}
	if cfg.TLSEnabled {
		tlsCfg, err := buildSidecarTLSConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("building TLS config for drain handler: %w", err)
		}
		factory.tlsConfig = tlsCfg
	}
	return factory, nil
}

// NewClient creates a Valkey client for the given address, applying TLS and/or auth as configured.
func (f *realValkeyClientFactory) NewClient(addr string) ValkeyCommander {
	if f.tlsConfig != nil && f.password != "" {
		return valkeyclient.NewTLSWithPassword(addr, f.tlsConfig, f.password)
	}
	if f.tlsConfig != nil {
		return valkeyclient.NewTLS(addr, f.tlsConfig)
	}
	if f.password != "" {
		return valkeyclient.NewWithPassword(addr, f.password)
	}
	return valkeyclient.New(addr)
}

// buildDrainHandler creates a DrainHandler from the sidecar Config with shared dependencies.
func buildDrainHandler(cfg Config, detector RoleDetector, patcher PodPatcher) (*DrainHandler, error) {
	factory, err := newRealValkeyClientFactory(cfg)
	if err != nil {
		return nil, err
	}

	// When sentinel auth is disabled, create a separate factory without password
	// for sentinel connections. Sentinel still connects to Valkey with auth-pass,
	// but clients (including the sidecar) connect to Sentinel without AUTH.
	sentinelFactory := ValkeyClientFactory(factory)
	if cfg.SentinelDisableAuth {
		sentinelCfg := cfg
		sentinelCfg.Password = ""
		sf, err := newRealValkeyClientFactory(sentinelCfg)
		if err != nil {
			return nil, err
		}
		sentinelFactory = sf
	}

	var sentinelAddrs []string
	if cfg.SentinelAddrs != "" {
		sentinelAddrs = strings.Split(cfg.SentinelAddrs, ",")
	}

	_, port := splitHostPort(cfg.ValkeyAddr)
	if port == "" {
		port = "6379"
	}

	return &DrainHandler{
		detector:              detector,
		patcher:               patcher,
		clientFactory:         factory,
		sentinelClientFactory: sentinelFactory,
		podName:               cfg.PodName,
		podNamespace:          cfg.PodNamespace,
		sentinelEnabled:       cfg.SentinelEnabled,
		sentinelMonitor:       cfg.SentinelMonitor,
		sentinelAddrs:         sentinelAddrs,
		headlessSvc:           cfg.HeadlessSvc,
		replicas:              cfg.Replicas,
		valkeyPort:            port,
	}, nil
}
