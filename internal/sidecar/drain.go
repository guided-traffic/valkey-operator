package sidecar

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/tlsmaterial"
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

	// signalDir is where the drain-complete marker is written. Empty means the
	// mount path the operator uses (common.DrainSignalMountPath); tests point it at
	// a temporary directory.
	signalDir string
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

	// The Valkey container waits on this marker before it exits, so every way out
	// of this function has to release it -- including the panic path, which is why
	// it is deferred here rather than called at each return.
	defer d.signalDrainComplete(log)

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
//
// A peer that already answers role:master ends the failover before it starts:
// the topology has a master, so this drain has nothing to promote and nothing to
// stamp. See findSyncedReplica for why that case is not hypothetical.
func (d *DrainHandler) manualFailover(ctx context.Context, log drainLog) error {
	replicaAddr, masterPresent, err := d.findSyncedReplica(ctx, log)
	if err != nil {
		return fmt.Errorf("finding synced replica: %w", err)
	}
	if masterPresent {
		log.Info("a peer already answers role:master, skipping promotion")
		return d.waitForRoleChange(ctx, log)
	}

	replicaHost, _ := splitHostPort(replicaAddr)
	log.Info("promoting replica to master", "addr", replicaAddr)

	// Promote the chosen replica.
	client := d.clientFactory.NewClient(replicaAddr)
	if err := client.ReplicaOf("NO", "ONE"); err != nil {
		return fmt.Errorf("promoting replica %s: %w", replicaAddr, err)
	}

	// Record the promotion before anything else. Everything below is
	// best-effort, the promotion above is not, and the operator needs the
	// record as early as possible.
	d.stampPromotion(ctx, replicaHost, log)

	// Reconfigure remaining replicas to follow the new master.
	d.reconfigureReplicas(replicaHost, replicaAddr, log)

	return d.waitForRoleChange(ctx, log)
}

// stampPromotion records on the promoted pod that a drain handler - not the
// operator - performed this promotion. The sidecar has no access to the Valkey
// CR, so this pod annotation is the only trace the operator can later find of a
// promotion it did not perform itself.
//
// Non-Sentinel path only, on purpose. With Sentinel, Sentinel is the promotion
// authority and the operator's steady-state split-brain check never runs for
// Sentinel clusters, so a stamp written there would be evidence nobody reads.
//
// Best-effort by construction: the promotion has already happened and cannot be
// undone, and this pod is terminating, so a failed patch must never abort the
// drain. Losing the stamp is a degradation, never a corruption - the operator
// then falls back to the structural rule (a labeled master with ordinal > 0 that
// the replica ConfigMap does not name cannot have elected itself), or refuses to
// resolve and records a Warning Event.
//
// The stamp lives on the Pod object, so it survives a container restart but not a
// delete-recreate: a promoted pod that loses its node before the operator adopts
// the promotion comes back without the annotation. That residual is inherent to
// where the record is kept, and it degrades the same way a failed patch does -
// the operator falls back to the structural rule, or refuses to resolve.
func (d *DrainHandler) stampPromotion(ctx context.Context, promotedHost string, log drainLog) {
	podName, ok := d.podNameFromHost(promotedHost)
	if !ok {
		log.Error(fmt.Errorf("host %q does not belong to headless service %q", promotedHost, d.headlessSvc),
			"cannot derive the promoted pod name, skipping the promotion stamp")
		return
	}

	stamp := time.Now().UTC().Format(time.RFC3339)
	if err := d.patcher.PatchAnnotation(ctx, d.podNamespace, podName,
		common.AnnotationDrainPromotedAt, stamp); err != nil {
		log.Error(err, "failed to stamp the promoted pod, the operator will have to infer this promotion",
			"promoted", podName)
		return
	}
	log.Info("stamped the promoted pod", "promoted", podName, "at", stamp)
}

// podNameFromHost extracts the pod name from a headless-service host of the form
// "<pod>.<headlessSvc>" - the exact shape buildReplicaAddrs produces, where
// headlessSvc is the "<name>-headless.<namespace>.svc.cluster.local" FQDN the
// operator renders from common.HeadlessServiceName. The suffix is matched against
// the configured headless Service instead of just cutting at the first dot, so an
// address that does not come from this StatefulSet yields no pod name at all.
// Reports false when the name cannot be derived.
func (d *DrainHandler) podNameFromHost(host string) (string, bool) {
	if d.headlessSvc == "" {
		return "", false
	}
	name, found := strings.CutSuffix(host, "."+d.headlessSvc)
	if !found || name == "" || strings.Contains(name, ".") {
		return "", false
	}
	return name, true
}

// findSyncedReplica queries the peers discovered via headless DNS and reports
// two things: the address of the first fully synced replica, and whether any
// peer already answers role:master.
//
// The master report is what keeps this handler from fighting the operator. On a
// rolling update the operator promotes a replica itself and then deletes the old
// master without ever demoting it, so this code runs on a pod that is still
// master while the topology already has a new one. Promoting again would pick a
// third pod - the operator's fresh master is a master, not a synced replica, so
// it is skipped here - point REPLICAOF at the operator's master, and stamp a pod
// nobody promoted. That stamp is read by the operator's steady-state master
// resolution, so a promotion the operator did not perform must not leave one. A
// genuine node drain has no master among its peers and is unaffected.
//
// Every reachable peer is therefore queried before "no master" may be concluded:
// returning at the first synced replica could hand back a promotion target while
// a master sits further down the list.
func (d *DrainHandler) findSyncedReplica(ctx context.Context, log drainLog) (string, bool, error) {
	addrs := d.buildReplicaAddrs()
	synced := ""
	for _, addr := range addrs {
		// A terminating peer is out of the running entirely -- as a promotion
		// candidate and as a "master already present" answer. It keeps answering
		// role queries for the whole of its termination and then returns on an
		// empty volume, so promoting it (or deferring to it) forwards the drain
		// window's writes into nothing. Measured in CI (2026-08-27): the drain of
		// a chaos-killed master promoted the peer the rolling update had deleted
		// in the same second, and the fleet consolidated toward the empty pod it
		// came back as (ADR 0028 D5a). Unknown reads as alive -- an API blip must
		// not fail a drain that has one bounded chance to run.
		if d.peerIsTerminating(ctx, log, addr) {
			continue
		}
		client := d.clientFactory.NewClient(addr)
		info, err := client.InfoReplication()
		if err != nil {
			log.Error(err, "failed to query replica", "addr", addr)
			continue
		}
		if info.Role == common.RoleMaster {
			log.Info("peer already reports role:master", "addr", addr)
			return "", true, nil
		}
		if synced == "" && isSyncedReplica(info) {
			synced = addr
		}
	}
	if synced == "" {
		return "", false, fmt.Errorf("no synced replica found among %d candidates", len(addrs))
	}
	return synced, false, nil
}

// peerIsTerminating reports whether the peer behind addr provably carries a
// DeletionTimestamp. Everything short of that proof -- an address that does not
// parse, a name outside this StatefulSet, an API error -- reads as alive.
func (d *DrainHandler) peerIsTerminating(ctx context.Context, log drainLog, addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	name, ok := d.podNameFromHost(host)
	if !ok {
		return false
	}
	terminating, err := d.patcher.IsTerminating(ctx, d.podNamespace, name)
	if err != nil {
		log.Info("cannot read a promotion candidate; treating it as alive", "pod", name, "error", err.Error())
		return false
	}
	if terminating {
		log.Info("peer is terminating; skipping it as a promotion candidate", "pod", name)
	}
	return terminating
}

// signalDrainComplete tells the Valkey container that it may exit
// (internal/common/drain.go).
//
// A missing directory is the normal case, not a failure: the operator mounts the
// shared volume only where the drain performs a manual failover, so a Sentinel or
// standalone pod has no directory and no preStop hook waiting on it. Checking the
// mount rather than re-deriving the condition keeps the two sides from drifting
// apart -- the mount IS the switch.
//
// A failed write is logged and nothing more. The preStop bound then expires on its
// own, which is slower but not wrong, and there is nothing better to do from a
// process that is itself terminating.
func (d *DrainHandler) signalDrainComplete(log drainLog) {
	dir := d.signalDir
	if dir == "" {
		dir = common.DrainSignalMountPath
	}
	if _, err := os.Stat(dir); err != nil {
		return
	}

	path := filepath.Join(dir, common.DrainCompleteFile)
	content := []byte(time.Now().UTC().Format(time.RFC3339) + "\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		log.Error(err, "could not signal drain completion; the Valkey container will wait out its preStop bound",
			"path", path)
		return
	}
	log.Info("drain complete, released the Valkey container", "path", path)
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
//
// It holds the TLS material source, not a parsed config: the drain promotion of
// ADR 0012 is the one call this factory exists for, it happens once at the very
// end of a pod's life, and a config parsed at process start is exactly the thing
// that is stale by then on a cluster whose certificate rotated.
type realValkeyClientFactory struct {
	password string
	tlsSrc   *tlsmaterial.Reloader
}

// newRealValkeyClientFactory creates a factory from the sidecar config.
func newRealValkeyClientFactory(cfg Config) (*realValkeyClientFactory, error) {
	tlsSrc, err := sidecarTLSReloader(cfg)
	if err != nil {
		return nil, fmt.Errorf("building TLS config for drain handler: %w", err)
	}
	return &realValkeyClientFactory{
		password: cfg.Password,
		tlsSrc:   tlsSrc,
	}, nil
}

// NewClient creates a Valkey client for the given address, applying TLS and/or auth as configured.
func (f *realValkeyClientFactory) NewClient(addr string) ValkeyCommander {
	return newValkeyClient(addr, f.tlsSrc, f.password)
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
