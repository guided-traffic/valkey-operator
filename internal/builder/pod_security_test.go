package builder

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	psaapi "k8s.io/pod-security-admission/api"
	psapolicy "k8s.io/pod-security-admission/policy"
	"k8s.io/utils/ptr"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// docs/adr/0032-generated-pods-run-rootless.md, tested with the checks the API
// server's PodSecurity admission itself runs (k8s.io/pod-security-admission/policy),
// not with a restatement of them. Every rendered template must pass "restricted";
// the data template with the migration repair must pass "baseline" and fail
// "restricted" -- both sides, so a check that can never fail cannot pass for one.

// podSecurityMatrix spans every switch that adds, removes or changes a container:
// topology x TLS x auth x metrics x persistence.
func podSecurityMatrix() []*vkov1.Valkey {
	topologies := []struct {
		name  string
		apply func(*vkov1.Valkey)
	}{
		{"standalone", func(v *vkov1.Valkey) { v.Spec.Replicas = 1 }},
		{"replicas3", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 }},
		{"sentinel", func(v *vkov1.Valkey) {
			v.Spec.Replicas = 3
			v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		}},
	}
	persistence := []*vkov1.PersistenceSpec{
		nil,
		{Enabled: true, Mode: vkov1.PersistenceModeRDB},
		{Enabled: true, Mode: vkov1.PersistenceModeAOF},
	}

	out := make([]*vkov1.Valkey, 0, len(topologies)*2*2*2*len(persistence))
	for _, topo := range topologies {
		for _, tls := range []bool{false, true} {
			for _, auth := range []bool{false, true} {
				for _, metrics := range []bool{false, true} {
					for i, p := range persistence {
						name := fmt.Sprintf("%s-tls%v-auth%v-metrics%v-p%d", topo.name, tls, auth, metrics, i)
						out = append(out, newTestValkey(name, func(v *vkov1.Valkey) {
							topo.apply(v)
							if tls {
								v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
							}
							if auth {
								v.Spec.Auth = &vkov1.AuthSpec{SecretName: "creds", SecretPasswordKey: "password"}
							}
							if metrics {
								v.Spec.Metrics = &vkov1.MetricsSpec{Enabled: true}
							}
							v.Spec.Persistence = p
						}))
					}
				}
			}
		}
	}
	return out
}

// renderedPodSpecs returns every pod template the operator renders for v, by name.
func renderedPodSpecs(v *vkov1.Valkey) map[string]*corev1.PodTemplateSpec {
	out := map[string]*corev1.PodTemplateSpec{
		"data":     &BuildStatefulSet(v, testOperatorImage).Spec.Template,
		"observer": &BuildObserverDeployment(v, testOperatorImage).Spec.Template,
	}
	if v.IsSentinelEnabled() {
		out["sentinel"] = &BuildSentinelStatefulSet(v).Spec.Template
	}
	return out
}

func psaEvaluate(t *testing.T, level psaapi.Level, tmpl *corev1.PodTemplateSpec) psapolicy.AggregateCheckResult {
	t.Helper()
	evaluator, err := psapolicy.NewEvaluator(psapolicy.DefaultChecks(), nil)
	require.NoError(t, err)
	return psapolicy.AggregateCheckResults(evaluator.EvaluatePod(
		psaapi.LevelVersion{Level: level, Version: psaapi.LatestVersion()}, &tmpl.ObjectMeta, &tmpl.Spec))
}

// TestPodSecurity_EveryRenderedTemplateIsRestricted is the ADR 0032 D1 guard.
//
// Revert check: dropping the applyValkeyPodSecurity call from buildPodSpec (or
// applyObserverPodSecurity from the observer builder) fails every row of that
// template with "allowPrivilegeEscalation != false, unrestricted capabilities,
// runAsNonRoot != true, seccompProfile".
func TestPodSecurity_EveryRenderedTemplateIsRestricted(t *testing.T) {
	for _, v := range podSecurityMatrix() {
		for kind, tmpl := range renderedPodSpecs(v) {
			t.Run(v.Name+"/"+kind, func(t *testing.T) {
				result := psaEvaluate(t, psaapi.LevelRestricted, tmpl)
				assert.True(t, result.Allowed, "%s: %s", result.ForbiddenReason(), result.ForbiddenDetail())
			})
		}
	}
}

// TestPodSecurity_TheRepairIsBaselineButNotRestricted is the other side: the
// migration repair runs as uid 0 with CAP_CHOWN, which "baseline" admits and
// "restricted" must refuse. It is also the positive control that the evaluator
// wired here can deny anything at all.
func TestPodSecurity_TheRepairIsBaselineButNotRestricted(t *testing.T) {
	for _, v := range podSecurityMatrix() {
		if !v.IsPersistenceEnabled() {
			continue
		}
		t.Run(v.Name, func(t *testing.T) {
			sts := BuildStatefulSet(v, testOperatorImage)
			WithDataOwnershipRepair(sts)

			baseline := psaEvaluate(t, psaapi.LevelBaseline, &sts.Spec.Template)
			assert.True(t, baseline.Allowed, "%s: %s", baseline.ForbiddenReason(), baseline.ForbiddenDetail())

			restricted := psaEvaluate(t, psaapi.LevelRestricted, &sts.Spec.Template)
			assert.False(t, restricted.Allowed, "the repair runs as root and adds CHOWN; restricted must refuse it")
		})
	}
}

// TestPodSecurity_EvaluatorRefusesTheLegacyShape is the positive control against
// the shape every cluster ran before ADR 0032: the same template with the posture
// stripped must fail restricted, or the matrix above proves nothing.
func TestPodSecurity_EvaluatorRefusesTheLegacyShape(t *testing.T) {
	v := newTestValkey("legacy", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	tmpl := BuildStatefulSet(v, testOperatorImage).Spec.Template
	tmpl.Spec.SecurityContext = nil
	for i := range tmpl.Spec.Containers {
		tmpl.Spec.Containers[i].SecurityContext = nil
	}
	for i := range tmpl.Spec.InitContainers {
		tmpl.Spec.InitContainers[i].SecurityContext = nil
	}

	assert.False(t, psaEvaluate(t, psaapi.LevelRestricted, &tmpl).Allowed)
}

// TestPodSecurity_EveryContainerHasTheFullPosture covers what Pod Security does not
// require: a read-only root filesystem, and drop ALL on containers PSS would let
// keep NET_BIND_SERVICE. The walk is what guarantees it, so it is asserted on every
// container of every template -- the ones a builder added later included.
func TestPodSecurity_EveryContainerHasTheFullPosture(t *testing.T) {
	for _, v := range podSecurityMatrix() {
		for kind, tmpl := range renderedPodSpecs(v) {
			all := append(append([]corev1.Container{}, tmpl.Spec.InitContainers...), tmpl.Spec.Containers...)
			require.NotEmpty(t, all)
			for _, c := range all {
				sc := c.SecurityContext
				require.NotNil(t, sc, "%s/%s/%s has no securityContext", v.Name, kind, c.Name)
				assert.Equal(t, ptr.To(false), sc.AllowPrivilegeEscalation, "%s/%s/%s", v.Name, kind, c.Name)
				assert.Equal(t, ptr.To(true), sc.ReadOnlyRootFilesystem, "%s/%s/%s", v.Name, kind, c.Name)
				require.NotNil(t, sc.Capabilities, "%s/%s/%s", v.Name, kind, c.Name)
				assert.Equal(t, []corev1.Capability{"ALL"}, sc.Capabilities.Drop, "%s/%s/%s", v.Name, kind, c.Name)
				assert.Empty(t, sc.Capabilities.Add, "%s/%s/%s", v.Name, kind, c.Name)
			}
		}
	}
}

// TestPodSecurity_DataAndSentinelPodsRunAsTheValkeyUser pins the identity: uid,
// gid and fsGroup 999 on both tiers, fsGroupChangePolicy deliberately unset
// (Always), RuntimeDefault seccomp.
func TestPodSecurity_DataAndSentinelPodsRunAsTheValkeyUser(t *testing.T) {
	v := newTestValkey("ident", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	for kind, tmpl := range renderedPodSpecs(v) {
		sc := tmpl.Spec.SecurityContext
		require.NotNil(t, sc, kind)
		assert.Equal(t, ptr.To(true), sc.RunAsNonRoot, kind)
		require.NotNil(t, sc.SeccompProfile, kind)
		assert.Equal(t, corev1.SeccompProfileTypeRuntimeDefault, sc.SeccompProfile.Type, kind)
		if kind == "observer" {
			assert.Nil(t, sc.RunAsUser, "the observer keeps its image's numeric nonroot user")
			assert.Nil(t, sc.FSGroup, "the observer mounts no data volume")
			continue
		}
		assert.Equal(t, ptr.To(ValkeyUID), sc.RunAsUser, kind)
		assert.Equal(t, ptr.To(ValkeyGID), sc.RunAsGroup, kind)
		assert.Equal(t, ptr.To(ValkeyGID), sc.FSGroup, kind)
		assert.Nil(t, sc.FSGroupChangePolicy, "%s: OnRootMismatch would skip files a later root writer left", kind)
	}
}

// TestPodSecurity_PreflightOnlyWithPersistence: every persistent data pod runs the
// pre-flight first, and a pod without persistence (fresh emptyDir) does not.
func TestPodSecurity_PreflightOnlyWithPersistence(t *testing.T) {
	for _, v := range podSecurityMatrix() {
		inits := BuildStatefulSet(v, testOperatorImage).Spec.Template.Spec.InitContainers
		if !v.IsPersistenceEnabled() {
			for _, c := range inits {
				assert.NotEqual(t, DataWritableCheckContainerName, c.Name, v.Name)
			}
			continue
		}
		require.NotEmpty(t, inits, v.Name)
		assert.Equal(t, DataWritableCheckContainerName, inits[0].Name, "%s: the pre-flight runs first", v.Name)
		assert.Equal(t, v.Spec.Image, inits[0].Image)
		assert.Equal(t, corev1.TerminationMessageFallbackToLogsOnError, inits[0].TerminationMessagePolicy)
	}
	for _, v := range podSecurityMatrix() {
		if !v.IsSentinelEnabled() {
			continue
		}
		for _, c := range BuildSentinelStatefulSet(v).Spec.Template.Spec.InitContainers {
			assert.NotEqual(t, DataWritableCheckContainerName, c.Name, "Sentinel volumes are emptyDirs")
		}
	}
}

// TestPodSecurity_ValkeyContainerStatesItsWorkingDir: without persistence the
// config has no dir directive, so the working directory is where a replica's
// full-sync RDB lands; with a read-only root it has to be the data mount.
func TestPodSecurity_ValkeyContainerStatesItsWorkingDir(t *testing.T) {
	v := newTestValkey("wd", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	for _, c := range BuildStatefulSet(v, testOperatorImage).Spec.Template.Spec.Containers {
		if c.Name == ValkeyContainerName {
			assert.Equal(t, DataDir, c.WorkingDir)
			return
		}
	}
	t.Fatal("no valkey container")
}

// --- the migration repair (ADR 0032 D2) --------------------------------------------

// TestWithDataOwnershipRepair_IsFirstAndIdempotent: the repair runs before the
// pre-flight it exists to satisfy, as root with CHOWN only, and inserting it twice
// is the same as once.
func TestWithDataOwnershipRepair_IsFirstAndIdempotent(t *testing.T) {
	v := newTestValkey("repair", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Persistence = &vkov1.PersistenceSpec{Enabled: true}
	})
	sts := BuildStatefulSet(v, testOperatorImage)
	require.False(t, HasDataOwnershipRepair(&sts.Spec.Template.Spec))

	WithDataOwnershipRepair(sts)
	WithDataOwnershipRepair(sts)

	inits := sts.Spec.Template.Spec.InitContainers
	require.True(t, HasDataOwnershipRepair(&sts.Spec.Template.Spec))
	require.GreaterOrEqual(t, len(inits), 2)
	assert.Equal(t, DataOwnershipRepairContainerName, inits[0].Name)
	assert.Equal(t, DataWritableCheckContainerName, inits[1].Name)
	count := 0
	for _, c := range inits {
		if c.Name == DataOwnershipRepairContainerName {
			count++
		}
	}
	assert.Equal(t, 1, count)

	repair := inits[0]
	assert.Equal(t, v.Spec.Image, repair.Image, "the repair runs the Valkey image, which ships find and chown")
	sc := repair.SecurityContext
	require.NotNil(t, sc)
	assert.Equal(t, ptr.To(int64(0)), sc.RunAsUser)
	assert.Equal(t, ptr.To(false), sc.RunAsNonRoot)
	assert.Equal(t, ptr.To(false), sc.AllowPrivilegeEscalation)
	assert.Equal(t, ptr.To(true), sc.ReadOnlyRootFilesystem)
	assert.Equal(t, []corev1.Capability{"ALL"}, sc.Capabilities.Drop)
	assert.Equal(t, []corev1.Capability{"CHOWN"}, sc.Capabilities.Add)
}

// TestWithDataOwnershipRepair_IsHashNeutral is the hash test of ADR 0032 D2: the
// repair comes and goes without a roll because the pod-spec hash never sees it,
// while the posture itself is inside the hash and rolls every cluster once.
//
// Revert check: moving applyValkeyPodSecurity out of buildPodSpec (after the hash)
// fails the posture assertion; inserting the repair inside buildPodSpec fails the
// neutrality assertion.
func TestWithDataOwnershipRepair_IsHashNeutral(t *testing.T) {
	v := newTestValkey("hash", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Persistence = &vkov1.PersistenceSpec{Enabled: true}
	})
	sts := BuildStatefulSet(v, testOperatorImage)
	annotated := sts.Spec.Template.Annotations[AnnotationPodSpecHash]
	require.NotEmpty(t, annotated)
	require.Equal(t, annotated, hashPodSpec(t, sts.Spec.Template.Spec),
		"precondition: the annotation is the hash of the template spec as built")

	WithDataOwnershipRepair(sts)
	assert.Equal(t, annotated, sts.Spec.Template.Annotations[AnnotationPodSpecHash])
	assert.Equal(t, annotated, ComputePodSpecHash(v, testOperatorImage),
		"the hash a later pass computes is the one without the repair")
	assert.NotEqual(t, annotated, hashPodSpec(t, sts.Spec.Template.Spec),
		"the template with the repair hashes differently: the annotation cannot have seen it")

	stripped := BuildStatefulSet(v, testOperatorImage).Spec.Template.Spec
	stripped.SecurityContext = nil
	assert.NotEqual(t, annotated, hashPodSpec(t, stripped),
		"the posture is inside the hash: it rolls every existing cluster once")
}

func hashPodSpec(t *testing.T, spec corev1.PodSpec) string {
	t.Helper()
	return podSpecDigest(spec)
}

// --- change detection -----------------------------------------------------------

func TestPodSpecChanged_SecurityContextSubsetSemantics(t *testing.T) {
	v := newTestValkey("diff", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Persistence = &vkov1.PersistenceSpec{Enabled: true}
	})
	desired := BuildStatefulSet(v, testOperatorImage).Spec.Template.Spec

	for _, tc := range []struct {
		name    string
		mutate  func(*corev1.PodSpec)
		changed bool
	}{
		{"identical", func(*corev1.PodSpec) {}, false},
		{"pod securityContext missing (a legacy template)", func(s *corev1.PodSpec) { s.SecurityContext = nil }, true},
		{"runAsUser differs", func(s *corev1.PodSpec) { s.SecurityContext.RunAsUser = ptr.To(int64(1000)) }, true},
		{"seccomp missing", func(s *corev1.PodSpec) { s.SecurityContext.SeccompProfile = nil }, true},
		{"a field the operator does not set is added", func(s *corev1.PodSpec) {
			p := corev1.FSGroupChangeOnRootMismatch
			s.SecurityContext.FSGroupChangePolicy = &p
		}, false},
		{"container securityContext missing", func(s *corev1.PodSpec) { s.Containers[0].SecurityContext = nil }, true},
		{"container root filesystem writable", func(s *corev1.PodSpec) {
			s.Containers[1].SecurityContext.ReadOnlyRootFilesystem = ptr.To(false)
		}, true},
		{"container adds a capability", func(s *corev1.PodSpec) {
			s.Containers[0].SecurityContext.Capabilities.Add = []corev1.Capability{"NET_RAW"}
		}, true},
		{"container drops less", func(s *corev1.PodSpec) {
			s.Containers[0].SecurityContext.Capabilities.Drop = nil
		}, true},
		{"init container escalates", func(s *corev1.PodSpec) {
			s.InitContainers[0].SecurityContext.AllowPrivilegeEscalation = ptr.To(true)
		}, true},
		{"container carries an extra field the operator does not set", func(s *corev1.PodSpec) {
			s.Containers[0].SecurityContext.ProcMount = ptr.To(corev1.DefaultProcMount)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := *desired.DeepCopy()
			tc.mutate(&current)
			assert.Equal(t, tc.changed, podSpecChanged(desired, current))
		})
	}
}

// The repair's own CHOWN is not an addition the comparison refuses: desired adds
// it, so the live template may carry it.
func TestPodSpecChanged_RepairTemplateIsStable(t *testing.T) {
	v := newTestValkey("diff-repair", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Persistence = &vkov1.PersistenceSpec{Enabled: true}
	})
	sts := BuildStatefulSet(v, testOperatorImage)
	WithDataOwnershipRepair(sts)
	assert.False(t, podSpecChanged(sts.Spec.Template.Spec, *sts.Spec.Template.Spec.DeepCopy()))

	without := BuildStatefulSet(v, testOperatorImage)
	assert.True(t, podSpecChanged(sts.Spec.Template.Spec, without.Spec.Template.Spec),
		"adding or removing the repair is a template change the operator writes")
}

// Without its own comparison line an existing observer Deployment would never
// receive a securityContext: it carries no pod-spec hash.
//
// Revert check: deleting either securityContext line from
// ObserverDeploymentHasChanged fails the matching row.
func TestObserverDeploymentHasChanged_SecurityContext(t *testing.T) {
	v := newTestValkey("obs")
	desired := BuildObserverDeployment(v, testOperatorImage)

	legacyPod := desired.DeepCopy()
	legacyPod.Spec.Template.Spec.SecurityContext = nil
	assert.True(t, ObserverDeploymentHasChanged(desired, legacyPod), "pod-level posture missing")

	legacyContainer := desired.DeepCopy()
	legacyContainer.Spec.Template.Spec.Containers[0].SecurityContext = nil
	assert.True(t, ObserverDeploymentHasChanged(desired, legacyContainer), "container posture missing")

	assert.False(t, ObserverDeploymentHasChanged(desired, desired.DeepCopy()))
}

// --- the pre-flight script, executed ---------------------------------------------

// runPreflight executes the generated check-data-writable script against a
// temporary directory standing in for the data mount.
func runPreflight(t *testing.T, dataDir string) (string, error) {
	t.Helper()
	script := strings.ReplaceAll(dataWritableCheckScript, DataDir, dataDir)
	out, err := exec.Command("sh", "-c", script).CombinedOutput()
	return string(out), err
}

func TestDataWritableCheck_Executes(t *testing.T) {
	if os.Geteuid() == 0 {
		// Root passes every -w test regardless of the mode bits, so the refusal
		// cases cannot be observed here. `make test-image-tools` runs the same
		// script as uid 999 against the real images.
		t.Skip("permission bits are not enforced for root")
	}

	t.Run("a writable dataset passes", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "dump.rdb"), []byte("x"), 0o644))
		require.NoError(t, os.Mkdir(filepath.Join(dir, "appendonlydir"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "appendonlydir", "appendonly.aof.1.incr.aof"), []byte("x"), 0o644))
		out, err := runPreflight(t, dir)
		assert.NoError(t, err, out)
	})

	t.Run("an empty volume passes", func(t *testing.T) {
		out, err := runPreflight(t, t.TempDir())
		assert.NoError(t, err, out)
	})

	t.Run("an unwritable AOF file fails and names the fix", func(t *testing.T) {
		dir := t.TempDir()
		aof := filepath.Join(dir, "appendonlydir")
		require.NoError(t, os.Mkdir(aof, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(aof, "appendonly.aof.1.incr.aof"), []byte("x"), 0o444))
		out, err := runPreflight(t, dir)
		var exitErr *exec.ExitError
		require.True(t, errors.As(err, &exitErr), "the pod must fail: %s", out)
		assert.Contains(t, out, "appendonly.aof.1.incr.aof")
		assert.Contains(t, out, "chown -R 999:999")
	})

	t.Run("an unwritable data directory fails", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o555))
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		out, err := runPreflight(t, dir)
		assert.Error(t, err, out)
		assert.Contains(t, out, "directory "+dir)
	})

	t.Run("a hidden file is checked too", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".hidden"), []byte("x"), 0o444))
		out, err := runPreflight(t, dir)
		assert.Error(t, err, out)
	})

	t.Run("an unwritable non-regular entry is not the pre-flight's business", func(t *testing.T) {
		dir := t.TempDir()
		lostFound := filepath.Join(dir, "lost+found")
		require.NoError(t, os.Mkdir(lostFound, 0o500))
		t.Cleanup(func() { _ = os.Chmod(lostFound, 0o755) })
		out, err := runPreflight(t, dir)
		assert.NoError(t, err, out)
	})
}

// PodRunsRootless is the migration evidence, and the spec field is the only input.
func TestPodRunsRootless(t *testing.T) {
	assert.False(t, PodRunsRootless(&corev1.Pod{}), "a pod built before ADR 0032 carries no securityContext")
	assert.False(t, PodRunsRootless(&corev1.Pod{Spec: corev1.PodSpec{SecurityContext: &corev1.PodSecurityContext{}}}))
	assert.False(t, PodRunsRootless(&corev1.Pod{Spec: corev1.PodSpec{
		SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: ptr.To(false)}}}))
	assert.True(t, PodRunsRootless(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p"},
		Spec:       corev1.PodSpec{SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: ptr.To(true)}},
	}))
}
