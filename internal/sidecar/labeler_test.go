package sidecar

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/guided-traffic/valkey-operator/internal/common"
)

// --- Mock implementations ---

type mockRoleDetector struct {
	role string
	err  error
}

func (m *mockRoleDetector) DetectRole() (string, error) {
	return m.role, m.err
}

type mockPodPatcher struct {
	patches           []patchRecord
	annotationPatches []annotationPatchRecord
	err               error
	annotationErr     error
	// onAnnotation runs inside PatchAnnotation, before it returns. Tests use it
	// to observe what has and has not happened yet at stamping time.
	onAnnotation func()
}

type patchRecord struct {
	namespace  string
	name       string
	labelKey   string
	labelValue string
}

type annotationPatchRecord struct {
	namespace string
	name      string
	key       string
	value     string
}

func (m *mockPodPatcher) PatchLabel(_ context.Context, namespace, name, labelKey, labelValue string) error {
	m.patches = append(m.patches, patchRecord{
		namespace:  namespace,
		name:       name,
		labelKey:   labelKey,
		labelValue: labelValue,
	})
	return m.err
}

func (m *mockPodPatcher) PatchAnnotation(_ context.Context, namespace, name, key, value string) error {
	m.annotationPatches = append(m.annotationPatches, annotationPatchRecord{
		namespace: namespace,
		name:      name,
		key:       key,
		value:     value,
	})
	if m.onAnnotation != nil {
		m.onAnnotation()
	}
	return m.annotationErr
}

// --- Tests ---

func TestLabeler_DetectsAndPatchesMasterRole(t *testing.T) {
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}
	health := NewHealthServer(":0")
	labeler := NewLabelerWithDeps(detector, patcher, "pod-0", "default", 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	labeler.Run(ctx, health)

	// Should have patched the label once (role didn't change after that).
	require.Len(t, patcher.patches, 1)
	assert.Equal(t, "default", patcher.patches[0].namespace)
	assert.Equal(t, "pod-0", patcher.patches[0].name)
	assert.Equal(t, common.LabelInstanceRole, patcher.patches[0].labelKey)
	assert.Equal(t, common.RoleMaster, patcher.patches[0].labelValue)

	// Health should be ready.
	assert.True(t, health.IsReady())
}

func TestLabeler_DetectsAndPatchesReplicaRole(t *testing.T) {
	detector := &mockRoleDetector{role: common.RoleReplica}
	patcher := &mockPodPatcher{}
	health := NewHealthServer(":0")
	labeler := NewLabelerWithDeps(detector, patcher, "pod-1", "test-ns", 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	labeler.Run(ctx, health)

	require.Len(t, patcher.patches, 1)
	assert.Equal(t, "test-ns", patcher.patches[0].namespace)
	assert.Equal(t, "pod-1", patcher.patches[0].name)
	assert.Equal(t, common.RoleReplica, patcher.patches[0].labelValue)
}

func TestLabeler_SkipsPatchWhenRoleUnchanged(t *testing.T) {
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}
	health := NewHealthServer(":0")
	labeler := NewLabelerWithDeps(detector, patcher, "pod-0", "default", 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	labeler.Run(ctx, health)

	// Multiple polls happened, but only one patch (initial detection).
	assert.Len(t, patcher.patches, 1)
}

func TestLabeler_PatchesAgainOnRoleChange(t *testing.T) {
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}
	health := NewHealthServer(":0")
	labeler := NewLabelerWithDeps(detector, patcher, "pod-0", "default", 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	// Start polling in background.
	done := make(chan struct{})
	go func() {
		labeler.Run(ctx, health)
		close(done)
	}()

	// Wait for initial detection.
	time.Sleep(100 * time.Millisecond)

	// Simulate role change.
	detector.role = common.RoleReplica

	// Wait for the change to be detected.
	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done

	// Should have two patches: master -> replica.
	require.GreaterOrEqual(t, len(patcher.patches), 2)
	assert.Equal(t, common.RoleMaster, patcher.patches[0].labelValue)
	assert.Equal(t, common.RoleReplica, patcher.patches[1].labelValue)
}

func TestLabeler_HandlesDetectionError(t *testing.T) {
	detector := &mockRoleDetector{err: errors.New("connection refused")}
	patcher := &mockPodPatcher{}
	health := NewHealthServer(":0")
	labeler := NewLabelerWithDeps(detector, patcher, "pod-0", "default", 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	labeler.Run(ctx, health)

	// No patches should have been made.
	assert.Empty(t, patcher.patches)

	// Health should NOT be ready.
	assert.False(t, health.IsReady())
}

func TestLabeler_HandlesPatchError(t *testing.T) {
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{err: errors.New("forbidden")}
	health := NewHealthServer(":0")
	labeler := NewLabelerWithDeps(detector, patcher, "pod-0", "default", 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	labeler.Run(ctx, health)

	// Should have attempted the patch but lastRole should not be updated.
	// The patcher records the attempt even though it returned an error.
	assert.NotEmpty(t, patcher.patches)

	// Health should be ready (role was detected even though patch failed).
	assert.True(t, health.IsReady())
}

func TestLabeler_NilHealth(t *testing.T) {
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}
	labeler := NewLabelerWithDeps(detector, patcher, "pod-0", "default", 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	// Should not panic even with nil health server.
	labeler.Run(ctx, nil)

	require.Len(t, patcher.patches, 1)
}

// --- Mock SentinelMasterQuerier ---

type mockSentinelQuerier struct {
	masterAddr string
	err        error
}

func (m *mockSentinelQuerier) GetMasterAddress(_ string) (string, error) {
	return m.masterAddr, m.err
}

// --- Sentinel cross-check tests ---

func TestLabeler_SentinelCrossCheck_MasterAgreed(t *testing.T) {
	// Local Valkey says master, Sentinel agrees → label as master.
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}
	health := NewHealthServer(":0")
	labeler := NewLabelerWithDeps(detector, patcher, "pod-0", "default", 100*time.Millisecond)
	labeler.SetSentinelCrossCheck(
		&mockSentinelQuerier{masterAddr: "pod-0.headless.default.svc.cluster.local"},
		"mymonitor",
		"pod-0.headless.default.svc.cluster.local",
	)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	labeler.Run(ctx, health)

	require.Len(t, patcher.patches, 1)
	assert.Equal(t, common.RoleMaster, patcher.patches[0].labelValue)
	assert.True(t, health.IsReady())
}

func TestLabeler_SentinelCrossCheck_MasterDisagreed(t *testing.T) {
	// Local Valkey says master, but Sentinel says different pod is master → label as replica.
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}
	health := NewHealthServer(":0")
	labeler := NewLabelerWithDeps(detector, patcher, "pod-0", "default", 100*time.Millisecond)
	labeler.SetSentinelCrossCheck(
		&mockSentinelQuerier{masterAddr: "pod-1.headless.default.svc.cluster.local"},
		"mymonitor",
		"pod-0.headless.default.svc.cluster.local",
	)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	labeler.Run(ctx, health)

	require.Len(t, patcher.patches, 1)
	assert.Equal(t, common.RoleReplica, patcher.patches[0].labelValue)
	assert.True(t, health.IsReady())
}

func TestLabeler_SentinelCrossCheck_SentinelUnreachable(t *testing.T) {
	// Local Valkey says master, Sentinel is unreachable → trust local, label as master.
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}
	health := NewHealthServer(":0")
	labeler := NewLabelerWithDeps(detector, patcher, "pod-0", "default", 100*time.Millisecond)
	labeler.SetSentinelCrossCheck(
		&mockSentinelQuerier{err: errors.New("all sentinels unreachable")},
		"mymonitor",
		"pod-0.headless.default.svc.cluster.local",
	)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	labeler.Run(ctx, health)

	require.Len(t, patcher.patches, 1)
	assert.Equal(t, common.RoleMaster, patcher.patches[0].labelValue)
}

func TestLabeler_SentinelCrossCheck_ReplicaNoCheck(t *testing.T) {
	// Local Valkey says replica → no Sentinel cross-check needed.
	detector := &mockRoleDetector{role: common.RoleReplica}
	patcher := &mockPodPatcher{}
	health := NewHealthServer(":0")
	labeler := NewLabelerWithDeps(detector, patcher, "pod-1", "default", 100*time.Millisecond)
	// Even with a querier that would say pod-0 is master, replica should remain replica.
	labeler.SetSentinelCrossCheck(
		&mockSentinelQuerier{masterAddr: "pod-0.headless.default.svc.cluster.local"},
		"mymonitor",
		"pod-1.headless.default.svc.cluster.local",
	)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	labeler.Run(ctx, health)

	require.Len(t, patcher.patches, 1)
	assert.Equal(t, common.RoleReplica, patcher.patches[0].labelValue)
}

func TestLabeler_SentinelCrossCheck_DisagreedThenResolved(t *testing.T) {
	// Start as master with Sentinel disagreeing (labeled replica),
	// then Valkey role changes to actual replica → no extra patch since already replica.
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}
	health := NewHealthServer(":0")
	labeler := NewLabelerWithDeps(detector, patcher, "pod-0", "default", 50*time.Millisecond)
	labeler.SetSentinelCrossCheck(
		&mockSentinelQuerier{masterAddr: "pod-1.headless.default.svc.cluster.local"},
		"mymonitor",
		"pod-0.headless.default.svc.cluster.local",
	)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		labeler.Run(ctx, health)
		close(done)
	}()

	// Wait for initial cross-check to label as replica.
	time.Sleep(100 * time.Millisecond)

	// Now the actual Valkey role changes to replica (operator demoted us).
	detector.role = common.RoleReplica

	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done

	// First patch: master overridden to replica by cross-check.
	require.GreaterOrEqual(t, len(patcher.patches), 1)
	assert.Equal(t, common.RoleReplica, patcher.patches[0].labelValue)
	// No second patch since role didn't change (still replica).
	assert.Len(t, patcher.patches, 1)
}

func TestLabeler_SentinelCrossCheck_NotConfigured(t *testing.T) {
	// No sentinel querier set → behaves like before, master stays master.
	detector := &mockRoleDetector{role: common.RoleMaster}
	patcher := &mockPodPatcher{}
	health := NewHealthServer(":0")
	labeler := NewLabelerWithDeps(detector, patcher, "pod-0", "default", 100*time.Millisecond)
	// No SetSentinelCrossCheck call.

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	labeler.Run(ctx, health)

	require.Len(t, patcher.patches, 1)
	assert.Equal(t, common.RoleMaster, patcher.patches[0].labelValue)
}

// --- buildSidecarTLSConfig ---

func TestBuildSidecarTLSConfig_NoMaterialStillPinsMinVersion(t *testing.T) {
	cfg, err := buildSidecarTLSConfig(Config{TLSEnabled: true})

	require.NoError(t, err)
	assert.Equal(t, uint16(tls.VersionTLS12), cfg.MinVersion)
	assert.Nil(t, cfg.RootCAs)
	assert.Empty(t, cfg.Certificates)
}

func TestBuildSidecarTLSConfig_LoadsCAAndClientKeyPair(t *testing.T) {
	certs := generateTestCerts(t)

	cfg, err := buildSidecarTLSConfig(Config{
		TLSEnabled: true,
		TLSCACert:  certs.caPath,
		TLSCert:    certs.certPath,
		TLSKey:     certs.keyPath,
	})

	require.NoError(t, err)
	require.NotNil(t, cfg.RootCAs)
	require.Len(t, cfg.Certificates, 1)
}

func TestBuildSidecarTLSConfig_MissingCAFile(t *testing.T) {
	_, err := buildSidecarTLSConfig(Config{TLSEnabled: true, TLSCACert: filepath.Join(t.TempDir(), "absent.crt")})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading CA cert")
}

func TestBuildSidecarTLSConfig_UnparseableCA(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ca.crt")
	require.NoError(t, os.WriteFile(path, []byte("not a certificate"), 0o600))

	_, err := buildSidecarTLSConfig(Config{TLSEnabled: true, TLSCACert: path})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse CA cert")
}

func TestBuildSidecarTLSConfig_BrokenClientKeyPair(t *testing.T) {
	certs := generateTestCerts(t)
	broken := filepath.Join(certs.dir, "broken.key")
	require.NoError(t, os.WriteFile(broken, []byte("not a key"), 0o600))

	_, err := buildSidecarTLSConfig(Config{TLSEnabled: true, TLSCert: certs.certPath, TLSKey: broken})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "loading client certificate")
}

// --- valkeyRoleDetector ---

func TestValkeyRoleDetector_TranslatesRoles(t *testing.T) {
	tests := []struct {
		reported string
		want     string
	}{
		{"master", common.RoleMaster},
		{"slave", common.RoleReplica},
		{"sentinel", "sentinel"},
	}

	for _, tt := range tests {
		t.Run(tt.reported, func(t *testing.T) {
			addr := fakeValkeyServer(t, nil, func([]string) string {
				return infoReplicationReply(tt.reported)
			})

			detector, err := newValkeyRoleDetector(Config{ValkeyAddr: addr})
			require.NoError(t, err)

			role, err := detector.DetectRole()
			require.NoError(t, err)
			assert.Equal(t, tt.want, role)
		})
	}
}

func TestValkeyRoleDetector_AuthenticatesWhenPasswordIsSet(t *testing.T) {
	var mu sync.Mutex
	var seen [][]string
	addr := fakeValkeyServer(t, nil, func(args []string) string {
		mu.Lock()
		seen = append(seen, args)
		mu.Unlock()
		if args[0] == "AUTH" {
			return "+OK\r\n"
		}
		return infoReplicationReply("master")
	})

	detector, err := newValkeyRoleDetector(Config{ValkeyAddr: addr, Password: "s3cret"})
	require.NoError(t, err)

	role, err := detector.DetectRole()
	require.NoError(t, err)
	assert.Equal(t, common.RoleMaster, role)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seen, 2)
	assert.Equal(t, []string{"AUTH", "s3cret"}, seen[0])
	assert.Equal(t, "INFO", seen[1][0])
}

func TestValkeyRoleDetector_TLSVariantsReachTheServer(t *testing.T) {
	certs := generateTestCerts(t)

	for _, password := range []string{"", "s3cret"} {
		t.Run("password="+password, func(t *testing.T) {
			addr := fakeValkeyServer(t, certs.serverTLSConfig(t), func(args []string) string {
				if args[0] == "AUTH" {
					return "+OK\r\n"
				}
				return infoReplicationReply("slave")
			})

			detector, err := newValkeyRoleDetector(Config{
				ValkeyAddr: addr,
				TLSEnabled: true,
				TLSCACert:  certs.caPath,
				Password:   password,
			})
			require.NoError(t, err)

			role, err := detector.DetectRole()
			require.NoError(t, err)
			assert.Equal(t, common.RoleReplica, role)
		})
	}
}

func TestValkeyRoleDetector_TLSConfigError(t *testing.T) {
	_, err := newValkeyRoleDetector(Config{TLSEnabled: true, TLSCACert: filepath.Join(t.TempDir(), "absent.crt")})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "building TLS config")
}

func TestValkeyRoleDetector_UnreachableServer(t *testing.T) {
	detector, err := newValkeyRoleDetector(Config{ValkeyAddr: "127.0.0.1:1"})
	require.NoError(t, err)

	_, err = detector.DetectRole()
	assert.Error(t, err)
}

// --- kubernetesPodPatcher ---

func TestKubernetesPodPatcher_PatchesLabelWithoutTouchingOtherMetadata(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-1",
			Namespace:   "default",
			Labels:      map[string]string{"keep": "me"},
			Annotations: map[string]string{"vko.gtrfc.com/config-hash": "abc"},
		},
	}
	client := k8sfake.NewSimpleClientset(pod)
	patcher := &kubernetesPodPatcher{clientset: client}

	require.NoError(t, patcher.PatchLabel(context.Background(), "default", "test-1",
		common.LabelInstanceRole, common.RoleMaster))

	got, err := client.CoreV1().Pods("default").Get(context.Background(), "test-1", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, common.RoleMaster, got.Labels[common.LabelInstanceRole])
	assert.Equal(t, "me", got.Labels["keep"])
	assert.Equal(t, "abc", got.Annotations["vko.gtrfc.com/config-hash"])
}

func TestKubernetesPodPatcher_PatchesAnnotationAdditively(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-1",
			Namespace:   "default",
			Labels:      map[string]string{common.LabelInstanceRole: common.RoleReplica},
			Annotations: map[string]string{"vko.gtrfc.com/pod-spec-hash": "xyz"},
		},
	}
	client := k8sfake.NewSimpleClientset(pod)
	patcher := &kubernetesPodPatcher{clientset: client}

	require.NoError(t, patcher.PatchAnnotation(context.Background(), "default", "test-1",
		common.AnnotationDrainPromotedAt, "2026-01-01T00:00:00Z"))

	got, err := client.CoreV1().Pods("default").Get(context.Background(), "test-1", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "2026-01-01T00:00:00Z", got.Annotations[common.AnnotationDrainPromotedAt])
	// The operator-owned hashes must survive: a whole-map replace would make the
	// pod look out of date to the rolling update.
	assert.Equal(t, "xyz", got.Annotations["vko.gtrfc.com/pod-spec-hash"])
	assert.Equal(t, common.RoleReplica, got.Labels[common.LabelInstanceRole])
}

func TestKubernetesPodPatcher_ReportsPatchFailure(t *testing.T) {
	patcher := &kubernetesPodPatcher{clientset: k8sfake.NewSimpleClientset()}

	err := patcher.PatchAnnotation(context.Background(), "default", "missing",
		common.AnnotationDrainPromotedAt, "2026-01-01T00:00:00Z")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "patching pod default/missing")
}

func TestNewKubernetesPodPatcher_OutsideClusterFails(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")

	_, err := newKubernetesPodPatcher()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting in-cluster config")
}

// --- sentinelMasterQuerier ---

// sentinelMasterReply renders the SENTINEL MASTER key/value array for the
// monitor "mymaster", naming ip as the current master.
func sentinelMasterReply(ip string) string {
	fields := []string{"name", "mymaster", "ip", ip, "port", "6379", "flags", "master", "num-slaves", "2", "quorum", "2"}
	out := fmt.Sprintf("*%d\r\n", len(fields))
	for _, f := range fields {
		out += respBulk(f)
	}
	return out
}

func TestNewSentinelMasterQuerier_CarriesPasswordUnlessAuthIsDisabled(t *testing.T) {
	q, err := newSentinelMasterQuerier(Config{SentinelAddrs: "a:26379,b:26379", Password: "s3cret"})
	require.NoError(t, err)
	assert.Equal(t, []string{"a:26379", "b:26379"}, q.addrs)
	assert.Equal(t, "s3cret", q.password)
	assert.Nil(t, q.tlsCfg)

	q, err = newSentinelMasterQuerier(Config{SentinelAddrs: "a:26379", Password: "s3cret", SentinelDisableAuth: true})
	require.NoError(t, err)
	assert.Empty(t, q.password, "clients must not AUTH against a Sentinel that does not require it")
}

func TestNewSentinelMasterQuerier_TLSConfigError(t *testing.T) {
	_, err := newSentinelMasterQuerier(Config{TLSEnabled: true, TLSCACert: filepath.Join(t.TempDir(), "absent.crt")})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "building TLS config for sentinel querier")
}

func TestSentinelMasterQuerier_ReturnsMasterIP(t *testing.T) {
	addr := fakeValkeyServer(t, nil, func(args []string) string {
		if args[0] == "AUTH" {
			return "+OK\r\n"
		}
		return sentinelMasterReply("test-1.test-headless.default.svc.cluster.local")
	})

	q, err := newSentinelMasterQuerier(Config{SentinelAddrs: addr, Password: "s3cret"})
	require.NoError(t, err)

	ip, err := q.GetMasterAddress("mymaster")
	require.NoError(t, err)
	assert.Equal(t, "test-1.test-headless.default.svc.cluster.local", ip)
}

func TestSentinelMasterQuerier_SkipsUnreachableSentinels(t *testing.T) {
	addr := fakeValkeyServer(t, nil, func([]string) string {
		return sentinelMasterReply("test-0.test-headless.default.svc.cluster.local")
	})

	q, err := newSentinelMasterQuerier(Config{SentinelAddrs: "127.0.0.1:1," + addr})
	require.NoError(t, err)

	ip, err := q.GetMasterAddress("mymaster")
	require.NoError(t, err)
	assert.Equal(t, "test-0.test-headless.default.svc.cluster.local", ip)
}

func TestSentinelMasterQuerier_TLSVariants(t *testing.T) {
	certs := generateTestCerts(t)

	for _, password := range []string{"", "s3cret"} {
		t.Run("password="+password, func(t *testing.T) {
			addr := fakeValkeyServer(t, certs.serverTLSConfig(t), func(args []string) string {
				if args[0] == "AUTH" {
					return "+OK\r\n"
				}
				return sentinelMasterReply("test-2.test-headless.default.svc.cluster.local")
			})

			q, err := newSentinelMasterQuerier(Config{
				SentinelAddrs: addr,
				TLSEnabled:    true,
				TLSCACert:     certs.caPath,
				Password:      password,
			})
			require.NoError(t, err)

			ip, err := q.GetMasterAddress("mymaster")
			require.NoError(t, err)
			assert.Equal(t, "test-2.test-headless.default.svc.cluster.local", ip)
		})
	}
}

func TestSentinelMasterQuerier_AllUnreachable(t *testing.T) {
	q, err := newSentinelMasterQuerier(Config{SentinelAddrs: "127.0.0.1:1"})
	require.NoError(t, err)

	_, err = q.GetMasterAddress("mymaster")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "all sentinels unreachable")
}

func TestSentinelMasterQuerier_NoAddressesConfigured(t *testing.T) {
	q, err := newSentinelMasterQuerier(Config{})
	require.NoError(t, err)

	_, err = q.GetMasterAddress("mymaster")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no sentinel addresses configured")
}

// --- NewLabeler ---

func TestNewLabeler_TLSConfigError(t *testing.T) {
	_, err := NewLabeler(Config{TLSEnabled: true, TLSCACert: filepath.Join(t.TempDir(), "absent.crt")})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "building TLS config")
}

func TestNewLabeler_NeedsAnInClusterConfig(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")

	_, err := NewLabeler(Config{ValkeyAddr: "127.0.0.1:6379"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting in-cluster config")
}
