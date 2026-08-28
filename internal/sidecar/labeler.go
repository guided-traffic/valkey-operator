package sidecar

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/tlsmaterial"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

const (
	// valkeyRoleSlave is the replication role string returned by Valkey for replica nodes.
	valkeyRoleSlave = "slave"
)

// RoleDetector detects the current Valkey replication role.
// This interface allows mocking in tests.
type RoleDetector interface {
	// DetectRole returns "master", "replica", or an error.
	DetectRole() (string, error)
}

// PodPatcher patches metadata on a pod.
// This interface allows mocking in tests.
type PodPatcher interface {
	// PatchLabel patches the given label key to the given value on the pod.
	PatchLabel(ctx context.Context, namespace, name, labelKey, labelValue string) error
	// PatchAnnotation patches the given annotation key to the given value on the pod.
	// The pod may be a peer, not the local one - the drain handler stamps the pod
	// it promoted.
	PatchAnnotation(ctx context.Context, namespace, name, key, value string) error
	// IsTerminating reports whether the named pod carries a DeletionTimestamp.
	// The drain handler consults it before promoting a peer: a terminating pod
	// answers role queries for the whole of its termination and then returns on
	// an empty volume, so promoting it forwards the drain window into nothing
	// (ADR 0028 D5a, measured in CI 2026-08-27). An error means "unknown", and
	// the caller treats unknown as alive -- refusing candidates on ignorance
	// would turn an API blip into a failed drain.
	IsTerminating(ctx context.Context, namespace, name string) (bool, error)
}

// SentinelMasterQuerier queries Sentinel for the current master address.
// This interface allows mocking in tests.
type SentinelMasterQuerier interface {
	// GetMasterAddress returns the hostname/FQDN of the current master as known by Sentinel.
	GetMasterAddress(monitor string) (string, error)
}

// Labeler polls the local Valkey instance and patches the pod's role label.
type Labeler struct {
	detector     RoleDetector
	patcher      PodPatcher
	podName      string
	podNamespace string
	pollInterval time.Duration
	lastRole     string

	// Sentinel cross-check (defense in depth against split-brain).
	sentinelQuerier SentinelMasterQuerier
	sentinelMonitor string
	myFQDN          string
}

// NewLabelerWithDeps creates a Labeler with injected dependencies. The live
// wiring is runSidecar (run.go), which builds the detector and patcher once and
// shares them with the drain handler — which is why there is no constructor here
// that builds its own: a second wiring path with its own detector/patcher pair
// was dead code that could drift from the reachable one unnoticed.
func NewLabelerWithDeps(detector RoleDetector, patcher PodPatcher, podName, podNamespace string, pollInterval time.Duration) *Labeler {
	return &Labeler{
		detector:     detector,
		patcher:      patcher,
		podName:      podName,
		podNamespace: podNamespace,
		pollInterval: pollInterval,
	}
}

// SetSentinelCrossCheck enables Sentinel cross-checking for the labeler.
// When enabled, the labeler verifies with Sentinel before labeling a pod as master.
// If Sentinel reports a different master, the pod is labeled as replica instead.
func (l *Labeler) SetSentinelCrossCheck(querier SentinelMasterQuerier, monitor, myFQDN string) {
	l.sentinelQuerier = querier
	l.sentinelMonitor = monitor
	l.myFQDN = myFQDN
}

// Run starts the polling loop. It blocks until the context is done.
// It notifies the health server once a role is successfully detected.
func (l *Labeler) Run(ctx context.Context, health *HealthServer) {
	logger := ctrl.Log.WithName("sidecar").WithName("labeler")
	ticker := time.NewTicker(l.pollInterval)
	defer ticker.Stop()

	// Do an immediate first poll.
	l.poll(ctx, logger, health)

	for {
		select {
		case <-ctx.Done():
			logger.Info("context cancelled, stopping labeler")
			return
		case <-ticker.C:
			l.poll(ctx, logger, health)
		}
	}
}

// poll performs a single poll-and-patch cycle.
func (l *Labeler) poll(ctx context.Context, logger interface {
	Info(string, ...interface{})
	Error(error, string, ...interface{})
}, health *HealthServer) {
	role, err := l.detector.DetectRole()
	if err != nil {
		logger.Error(err, "failed to detect role")
		return
	}

	// Mark ready once we know the role.
	if health != nil {
		health.SetReady()
	}

	// Cross-check: if local Valkey says "master" but Sentinel disagrees, trust Sentinel.
	// This prevents the rw Service from having two master endpoints during split-brain.
	if role == common.RoleMaster && l.sentinelQuerier != nil {
		sentinelMaster, sqErr := l.sentinelQuerier.GetMasterAddress(l.sentinelMonitor)
		if sqErr == nil && sentinelMaster != l.myFQDN {
			logger.Info("local Valkey reports master but Sentinel disagrees, labeling as replica",
				"sentinelMaster", sentinelMaster, "myFQDN", l.myFQDN)
			role = common.RoleReplica
		}
		// If Sentinel is unreachable (sqErr != nil), trust the local role.
	}

	// Only patch if role changed.
	if role == l.lastRole {
		return
	}

	logger.Info("role changed", "from", l.lastRole, "to", role)

	if err := l.patcher.PatchLabel(ctx, l.podNamespace, l.podName, common.LabelInstanceRole, role); err != nil {
		logger.Error(err, "failed to patch pod label", "role", role)
		return
	}

	l.lastRole = role
}

// --- valkeyRoleDetector ---

// valkeyRoleDetector detects the role by calling INFO REPLICATION on the local Valkey.
//
// It holds the TLS reloader rather than a client, because a client holds a
// *tls.Config and a *tls.Config holds the certificate that was on disk when it
// was built. That is what broke the labeler on every TLS cluster whose pods
// outlived a cert-manager rotation: the detector kept presenting an expired
// client certificate once per second and the server kept rejecting it.
type valkeyRoleDetector struct {
	addr     string
	password string
	tlsSrc   *tlsmaterial.Reloader
}

func newValkeyRoleDetector(cfg Config) (*valkeyRoleDetector, error) {
	tlsSrc, err := sidecarTLSReloader(cfg)
	if err != nil {
		return nil, fmt.Errorf("building TLS config: %w", err)
	}

	return &valkeyRoleDetector{
		addr:     cfg.ValkeyAddr,
		password: cfg.Password,
		tlsSrc:   tlsSrc,
	}, nil
}

// DetectRole queries INFO REPLICATION and returns "master" or "replica".
func (d *valkeyRoleDetector) DetectRole() (string, error) {
	info, err := newValkeyClient(d.addr, d.tlsSrc, d.password).InfoReplication()
	if err != nil {
		return "", err
	}

	switch info.Role {
	case common.RoleMaster:
		return common.RoleMaster, nil
	case valkeyRoleSlave:
		return common.RoleReplica, nil
	default:
		return info.Role, nil
	}
}

// --- kubernetesPodPatcher ---

// kubernetesPodPatcher uses the Kubernetes API to patch pod labels.
type kubernetesPodPatcher struct {
	clientset kubernetes.Interface
}

func newKubernetesPodPatcher() (*kubernetesPodPatcher, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("getting in-cluster config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}

	return &kubernetesPodPatcher{clientset: clientset}, nil
}

// PatchLabel performs a merge patch on the pod to update a label.
func (p *kubernetesPodPatcher) PatchLabel(ctx context.Context, namespace, name, labelKey, labelValue string) error {
	return p.patchMetadata(ctx, namespace, name, "labels", labelKey, labelValue)
}

// PatchAnnotation performs a merge patch on the pod to update an annotation.
func (p *kubernetesPodPatcher) PatchAnnotation(ctx context.Context, namespace, name, key, value string) error {
	return p.patchMetadata(ctx, namespace, name, "annotations", key, value)
}

// IsTerminating reads the pod and reports whether it carries a DeletionTimestamp.
// The read rides the same named-pod grant the patches use (get, added with this
// method -- see SECURITY_ARCHITECTURE.md section 4.2).
func (p *kubernetesPodPatcher) IsTerminating(ctx context.Context, namespace, name string) (bool, error) {
	pod, err := p.clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	return pod.DeletionTimestamp != nil, nil
}

// patchMetadata merge-patches a single key inside metadata.labels or
// metadata.annotations. A merge patch on that map is additive for every other
// key, so it cannot clobber the operator-owned hash annotations on the target
// pod - which a whole-map replace would, making the pod look out of date to the
// rolling update.
func (p *kubernetesPodPatcher) patchMetadata(ctx context.Context, namespace, name, field, key, value string) error {
	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			field: map[string]string{
				key: value,
			},
		},
	}

	patchData, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshalling patch: %w", err)
	}

	_, err = p.clientset.CoreV1().Pods(namespace).Patch(
		ctx,
		name,
		types.MergePatchType,
		patchData,
		metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("patching pod %s/%s: %w", namespace, name, err)
	}

	return nil
}

// sidecarTLSReloader builds the TLS material source shared by the three
// collaborators the sidecar constructs at startup: the role detector, the
// Sentinel cross-check querier and the drain handler's client factory. It
// returns nil when TLS is disabled, which every caller reads as "plaintext".
//
// The reloader re-reads the mounted files, so all three keep working across a
// cert-manager rotation instead of holding the material they parsed at process
// start until it expires. Failing here still fails the process: material that
// cannot be read at startup is a misconfiguration, not a rotation.
func sidecarTLSReloader(cfg Config) (*tlsmaterial.Reloader, error) {
	if !cfg.TLSEnabled {
		return nil, nil
	}
	return tlsmaterial.New(cfg.TLSCACert, cfg.TLSCert, cfg.TLSKey)
}

// newValkeyClient builds a client for addr with the material that is on disk
// now. Every caller builds one per command rather than keeping it: the client
// holds no connection -- exec, ExecMulti and ExecGet each dial -- so building it
// per call costs an allocation and is what makes the rotation visible to the
// next handshake.
//
// tlsSrc is nil when TLS is disabled.
func newValkeyClient(addr string, tlsSrc *tlsmaterial.Reloader, password string) *valkeyclient.Client {
	switch {
	case tlsSrc != nil && password != "":
		return valkeyclient.NewTLSWithPassword(addr, tlsSrc.Config(), password)
	case tlsSrc != nil:
		return valkeyclient.NewTLS(addr, tlsSrc.Config())
	case password != "":
		return valkeyclient.NewWithPassword(addr, password)
	default:
		return valkeyclient.New(addr)
	}
}

// --- sentinelMasterQuerier ---

// sentinelMasterQuerier queries multiple Sentinel instances for the current master.
type sentinelMasterQuerier struct {
	addrs    []string
	password string
	tlsSrc   *tlsmaterial.Reloader
}

// newSentinelMasterQuerier creates a production querier from the sidecar config.
func newSentinelMasterQuerier(cfg Config) (*sentinelMasterQuerier, error) {
	q := &sentinelMasterQuerier{}

	if cfg.SentinelAddrs != "" {
		q.addrs = strings.Split(cfg.SentinelAddrs, ",")
	}

	// When sentinel auth is disabled, clients connect to Sentinel without AUTH.
	if !cfg.SentinelDisableAuth {
		q.password = cfg.Password
	}

	tlsSrc, err := sidecarTLSReloader(cfg)
	if err != nil {
		return nil, fmt.Errorf("building TLS config for sentinel querier: %w", err)
	}
	q.tlsSrc = tlsSrc

	return q, nil
}

// GetMasterAddress queries Sentinel instances in order and returns the master hostname.
func (q *sentinelMasterQuerier) GetMasterAddress(monitor string) (string, error) {
	var lastErr error
	for _, addr := range q.addrs {
		info, err := newValkeyClient(addr, q.tlsSrc, q.password).SentinelMaster(monitor)
		if err != nil {
			lastErr = err
			continue
		}
		return info.IP, nil
	}
	if lastErr != nil {
		return "", fmt.Errorf("all sentinels unreachable: %w", lastErr)
	}
	return "", fmt.Errorf("no sentinel addresses configured")
}
