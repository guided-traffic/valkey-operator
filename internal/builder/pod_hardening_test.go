package builder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	psaapi "k8s.io/pod-security-admission/api"
	"k8s.io/utils/ptr"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md:
// the hardening every generated pod carries beyond the rootless posture of ADR 0032,
// and the two opt-ins spec.podSecurity adds.

// hardenedValkey enables both opt-ins on a cluster that renders every pod kind.
func hardenedValkey(name string) *vkov1.Valkey {
	return newTestValkey(name, func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Spec.Persistence = &vkov1.PersistenceSpec{Enabled: true, Mode: vkov1.PersistenceModeAOF}
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
		v.Spec.Auth = &vkov1.AuthSpec{SecretName: "creds", SecretPasswordKey: "password"}
		v.Spec.Metrics = &vkov1.MetricsSpec{Enabled: true}
		v.Spec.PodSecurity = &vkov1.PodSecuritySpec{
			SeccompProfile: &vkov1.SeccompProfileSpec{
				Type:             corev1.SeccompProfileTypeLocalhost,
				LocalhostProfile: ptr.To("profiles/valkey.json"),
			},
			UserNamespaces: true,
		}
	})
}

// TestPodHardening_DefaultsOnEveryTemplate is the D2-D4 guard over the whole
// matrix: no Service links, no user namespace unless asked for, none of the host
// namespaces, RuntimeDefault seccomp, and privileged stated false on every
// container -- the walk's, so a container added later inherits it.
//
// Revert check: deleting the applyPodHardening call from applyValkeyPodSecurity
// fails every data and Sentinel row, from applyObserverPodSecurity every observer
// row; deleting Privileged from restrictedContainerSecurityContext fails every
// container.
func TestPodHardening_DefaultsOnEveryTemplate(t *testing.T) {
	for _, v := range podSecurityMatrix() {
		for kind, tmpl := range renderedPodSpecs(v) {
			spec := tmpl.Spec
			assert.Equal(t, ptr.To(false), spec.EnableServiceLinks, "%s/%s", v.Name, kind)
			assert.Nil(t, spec.HostUsers, "%s/%s: the user namespace is opt-in", v.Name, kind)
			assert.False(t, spec.HostNetwork || spec.HostPID || spec.HostIPC, "%s/%s", v.Name, kind)
			require.NotNil(t, spec.SecurityContext, "%s/%s", v.Name, kind)
			assert.Equal(t, &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
				spec.SecurityContext.SeccompProfile, "%s/%s", v.Name, kind)
			for _, c := range append(append([]corev1.Container{}, spec.InitContainers...), spec.Containers...) {
				require.NotNil(t, c.SecurityContext, "%s/%s/%s", v.Name, kind, c.Name)
				assert.Equal(t, ptr.To(false), c.SecurityContext.Privileged, "%s/%s/%s", v.Name, kind, c.Name)
			}
		}
	}
}

// TestPodHardening_OptInsReachEveryPodKind: both opt-ins land on the data, Sentinel
// and observer pod, and the result still passes Pod Security "restricted", which
// allows Localhost profiles and says nothing against hostUsers: false.
//
// Revert check: passing a fixed RuntimeDefault instead of v.GetSeccompProfile() in
// either posture function, or dropping the HostUsers assignment in
// applyPodHardening, fails the matching kind.
func TestPodHardening_OptInsReachEveryPodKind(t *testing.T) {
	v := hardenedValkey("opt")
	templates := renderedPodSpecs(v)
	require.Len(t, templates, 3)
	for kind, tmpl := range templates {
		assert.Equal(t, ptr.To(false), tmpl.Spec.HostUsers, kind)
		assert.Equal(t, &corev1.SeccompProfile{
			Type:             corev1.SeccompProfileTypeLocalhost,
			LocalhostProfile: ptr.To("profiles/valkey.json"),
		}, tmpl.Spec.SecurityContext.SeccompProfile, kind)
		result := psaEvaluate(t, psaapi.LevelRestricted, tmpl)
		assert.True(t, result.Allowed, "%s: %s: %s", kind, result.ForbiddenReason(), result.ForbiddenDetail())
	}

	// The repair container is the one root process; it inherits the pod's profile
	// and user namespace, which is why the ADR requires a Localhost profile to allow
	// its chown.
	sts := BuildStatefulSet(v, testOperatorImage)
	WithDataOwnershipRepair(sts)
	assert.Equal(t, ptr.To(false), sts.Spec.Template.Spec.HostUsers)
	assert.Equal(t, ptr.To(false), sts.Spec.Template.Spec.InitContainers[0].SecurityContext.Privileged)
}

// TestPodHardening_OptInsMoveThePodSpecHashes: the hash is what rolls the pods of an
// OnDelete StatefulSet (ADR 0005 D7), so a toggle that did not move it would change
// the template and never reach a running pod.
func TestPodHardening_OptInsMoveThePodSpecHashes(t *testing.T) {
	base := newTestValkey("hash", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	userns := base.DeepCopy()
	userns.Spec.PodSecurity = &vkov1.PodSecuritySpec{UserNamespaces: true}
	localhost := base.DeepCopy()
	localhost.Spec.PodSecurity = &vkov1.PodSecuritySpec{SeccompProfile: &vkov1.SeccompProfileSpec{
		Type: corev1.SeccompProfileTypeLocalhost, LocalhostProfile: ptr.To("profiles/a.json"),
	}}
	otherProfile := localhost.DeepCopy()
	otherProfile.Spec.PodSecurity.SeccompProfile.LocalhostProfile = ptr.To("profiles/b.json")
	explicitDefault := base.DeepCopy()
	explicitDefault.Spec.PodSecurity = &vkov1.PodSecuritySpec{
		SeccompProfile: &vkov1.SeccompProfileSpec{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}

	for _, hash := range []func(*vkov1.Valkey) string{
		func(v *vkov1.Valkey) string { return ComputePodSpecHash(v, testOperatorImage) },
		ComputeSentinelPodSpecHash,
	} {
		h := hash(base)
		assert.NotEqual(t, h, hash(userns), "userNamespaces")
		assert.NotEqual(t, h, hash(localhost), "Localhost profile")
		assert.NotEqual(t, hash(localhost), hash(otherProfile), "another Localhost profile")
		assert.Equal(t, h, hash(explicitDefault), "an explicit RuntimeDefault is the default: no roll")
	}
}

// TestPodSpecChanged_Hardening: the template comparison converges each field back.
// hostUsers is compared exactly -- the opt-out leaves desired unset, and a subset
// comparison would keep the persisted false forever.
//
// Revert check: dropping podHardeningChanged from podSpecChanged fails the
// hostUsers and service-link rows; replacing ptr.Equal with ptrDiffers fails
// "opt-out"; deleting the Privileged line from containerSecurityContextChanged
// fails both privileged rows.
func TestPodSpecChanged_Hardening(t *testing.T) {
	plain := newTestValkey("hard-diff", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	withUserns := plain.DeepCopy()
	withUserns.Spec.PodSecurity = &vkov1.PodSecuritySpec{UserNamespaces: true}
	withProfile := plain.DeepCopy()
	withProfile.Spec.PodSecurity = &vkov1.PodSecuritySpec{SeccompProfile: &vkov1.SeccompProfileSpec{
		Type: corev1.SeccompProfileTypeLocalhost, LocalhostProfile: ptr.To("profiles/a.json"),
	}}
	spec := func(v *vkov1.Valkey) corev1.PodSpec { return BuildStatefulSet(v, testOperatorImage).Spec.Template.Spec }

	for _, tc := range []struct {
		name             string
		desired, current corev1.PodSpec
		mutate           func(*corev1.PodSpec)
		changed          bool
	}{
		{"identical", spec(plain), spec(plain), nil, false},
		{"opt-in", spec(withUserns), spec(plain), nil, true},
		{"opt-out", spec(plain), spec(withUserns), nil, true},
		{"service links missing (a template before ADR 0033)", spec(plain), spec(plain),
			func(s *corev1.PodSpec) { s.EnableServiceLinks = nil }, true},
		{"service links on", spec(plain), spec(plain),
			func(s *corev1.PodSpec) { s.EnableServiceLinks = ptr.To(true) }, true},
		{"RuntimeDefault to Localhost", spec(withProfile), spec(plain), nil, true},
		{"Localhost back to RuntimeDefault", spec(plain), spec(withProfile), nil, true},
		{"another Localhost profile", spec(withProfile), spec(withProfile), func(s *corev1.PodSpec) {
			s.SecurityContext.SeccompProfile.LocalhostProfile = ptr.To("profiles/b.json")
		}, true},
		{"privileged missing (a template before ADR 0033)", spec(plain), spec(plain),
			func(s *corev1.PodSpec) { s.Containers[0].SecurityContext.Privileged = nil }, true},
		{"privileged on", spec(plain), spec(plain),
			func(s *corev1.PodSpec) { s.InitContainers[0].SecurityContext.Privileged = ptr.To(true) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := *tc.current.DeepCopy()
			if tc.mutate != nil {
				tc.mutate(&current)
			}
			assert.Equal(t, tc.changed, podSpecChanged(tc.desired, current))
		})
	}
}

// TestObserverDeploymentHasChanged_Hardening: the observer carries no pod-spec
// hash, so only its own comparison line moves an existing Deployment.
//
// Revert check: dropping podHardeningChanged from ObserverDeploymentHasChanged
// fails every row but the last.
func TestObserverDeploymentHasChanged_Hardening(t *testing.T) {
	plain := newTestValkey("obs-hard")
	withUserns := plain.DeepCopy()
	withUserns.Spec.PodSecurity = &vkov1.PodSecuritySpec{UserNamespaces: true}
	build := func(v *vkov1.Valkey) *appsv1.Deployment { return BuildObserverDeployment(v, testOperatorImage) }

	assert.True(t, ObserverDeploymentHasChanged(build(withUserns), build(plain)), "opt-in")
	assert.True(t, ObserverDeploymentHasChanged(build(plain), build(withUserns)), "opt-out")
	noLinks := build(plain)
	noLinks.Spec.Template.Spec.EnableServiceLinks = nil
	assert.True(t, ObserverDeploymentHasChanged(build(plain), noLinks), "service links missing")
	assert.False(t, ObserverDeploymentHasChanged(build(withUserns), build(withUserns)))
}

// TestSentinelResources_ReachEveryContainer: spec.sentinel.resources goes to the
// sentinel container and its init container, because a ResourceQuota admits a pod
// only when every container states the values; unset, nothing is stated.
func TestSentinelResources_ReachEveryContainer(t *testing.T) {
	res := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("20m"), corev1.ResourceMemory: resource.MustParse("32Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Mi")},
	}
	v := newTestValkey("sres", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3, Resources: &res}
	})
	spec := BuildSentinelStatefulSet(v).Spec.Template.Spec
	all := append(append([]corev1.Container{}, spec.InitContainers...), spec.Containers...)
	require.Len(t, all, 2)
	for _, c := range all {
		assert.Equal(t, res, c.Resources, c.Name)
	}
	assert.NotEqual(t, ComputeSentinelPodSpecHash(v), func() string {
		w := v.DeepCopy()
		w.Spec.Sentinel.Resources = nil
		return ComputeSentinelPodSpecHash(w)
	}(), "a resources change rolls the Sentinel tier")

	v.Spec.Sentinel.Resources = nil
	for _, c := range BuildSentinelStatefulSet(v).Spec.Template.Spec.Containers {
		assert.Equal(t, corev1.ResourceRequirements{}, c.Resources, "no default: %s", c.Name)
	}
}
