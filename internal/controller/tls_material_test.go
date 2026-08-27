package controller

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// --- fixtures ---

func certManagerValkey(name string, opts ...func(*vkov1.Valkey)) *vkov1.Valkey {
	return newTestValkey(name, "default", append([]func(*vkov1.Valkey){func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "cluster-ca"},
			},
		}
	}}, opts...)...)
}

func tlsSecret(name, ns, revision string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Data: map[string][]byte{
			"ca.crt":  []byte("ca-" + revision),
			"tls.crt": []byte("cert-" + revision),
			"tls.key": []byte("key-" + revision),
		},
	}
}

// tlsCarrierContainer is the container of a tier that records the fingerprint:
// the sidecar on the data tier, the sentinel container on the Sentinel tier.
func tlsCarrierContainer(component string) string {
	if component == common.ComponentSentinel {
		return builder.SentinelContainerName
	}
	return builder.SidecarContainerName
}

// tlsCarrierSpec builds the minimal pod spec that records hash the way the
// reconciler does -- as env on the tier's carrier container. An empty hash yields
// a spec with no record, which is what a pod created before the mechanism shipped
// looks like.
func tlsCarrierSpec(component, hash string) corev1.PodSpec {
	container := corev1.Container{Name: tlsCarrierContainer(component)}
	if hash != "" {
		container.Env = []corev1.EnvVar{{Name: builder.TLSMaterialHashEnvName, Value: hash}}
	}
	return corev1.PodSpec{Containers: []corev1.Container{container}}
}

// tlsTierSts builds an owned StatefulSet for a tier, carrying the fingerprint the
// reconciler would have stamped.
func tlsTierSts(v *vkov1.Valkey, component string, replicas int32, hash string) *appsv1.StatefulSet {
	name := common.StatefulSetName(v, component)
	yes := true
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: v.Namespace,
			UID:       stsUIDFor(name),
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: vkov1.GroupVersion.String(),
				Kind:       "Valkey",
				Name:       v.Name,
				UID:        v.UID,
				Controller: &yes,
			}},
		},
		Spec: appsv1.StatefulSetSpec{Replicas: &replicas},
	}
	sts.Spec.Template.Spec = tlsCarrierSpec(component, hash)
	return sts
}

// tlsTierPod builds a pod of a tier carrying the given fingerprint in its spec.
// An empty hash leaves no record, which is what every pod created before this
// mechanism shipped looks like.
func tlsTierPod(v *vkov1.Valkey, component string, ordinal int, hash string) *corev1.Pod {
	name := fmt.Sprintf("%s-%d", common.StatefulSetName(v, component), ordinal)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: v.Namespace},
		Spec:       tlsCarrierSpec(component, hash),
	}
	ownedByTestSts(v, component, pod)
	return pod
}

// tlsTierPodLegacy builds a pod that records the fingerprint the way pods created
// before 2026-08-27 do: in the annotation, with no env anywhere. It is the
// population the fallback read in RecordedTLSMaterialHash exists for.
func tlsTierPodLegacy(v *vkov1.Valkey, component string, ordinal int, hash string) *corev1.Pod {
	name := fmt.Sprintf("%s-%d", common.StatefulSetName(v, component), ordinal)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   v.Namespace,
			Annotations: map[string]string{builder.AnnotationTLSMaterialHash: hash},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: tlsCarrierContainer(component)}}},
	}
	ownedByTestSts(v, component, pod)
	return pod
}

// --- stampTLSMaterialHash ---

// recordedOn returns the fingerprint the pod template carries, read the way every
// consumer reads it.
func recordedOn(sts *appsv1.StatefulSet) string {
	return builder.RecordedTLSMaterialHash(&sts.Spec.Template.Spec, sts.Spec.Template.Annotations)
}

func TestStampTLSMaterialHash_WritesTheFingerprintOfTheMountedSecret(t *testing.T) {
	v := certManagerValkey("test")
	secret := tlsSecret(builder.ValkeyTLSSecretName(v), v.Namespace, "1")
	r, _ := newTestReconciler(v, secret)

	sts := builder.BuildStatefulSet(v, "operator:test")
	r.stampTLSMaterialHash(context.Background(), v, sts,
		builder.ValkeyTLSSecretName(v), builder.SidecarContainerName)

	assert.Equal(t, builder.ComputeTLSMaterialHash(secret), recordedOn(sts))
}

// The record goes into the pod spec and never into pod metadata: metadata is what
// a compromised container can patch (ADR 0031).
func TestStampTLSMaterialHash_RecordsInTheSpecAndNotInMetadata(t *testing.T) {
	v := certManagerValkey("test")
	secret := tlsSecret(builder.ValkeyTLSSecretName(v), v.Namespace, "1")
	r, _ := newTestReconciler(v, secret)

	sts := builder.BuildStatefulSet(v, "operator:test")
	r.stampTLSMaterialHash(context.Background(), v, sts,
		builder.ValkeyTLSSecretName(v), builder.SidecarContainerName)

	assert.NotContains(t, sts.Spec.Template.Annotations, builder.AnnotationTLSMaterialHash,
		"the annotation is superseded and must not be written any more")

	carriers := map[string]string{}
	for _, c := range sts.Spec.Template.Spec.Containers {
		for _, env := range c.Env {
			if env.Name == builder.TLSMaterialHashEnvName {
				carriers[c.Name] = env.Value
			}
		}
	}
	assert.Equal(t,
		map[string]string{builder.SidecarContainerName: builder.ComputeTLSMaterialHash(secret)},
		carriers, "exactly the carrier container records it")
}

// A second stamp replaces the value rather than appending a second entry -- a
// duplicated env var is a pod the API server rejects.
func TestStampTLSMaterialHash_RotationReplacesTheRecord(t *testing.T) {
	v := certManagerValkey("test")
	sts := builder.BuildStatefulSet(v, "operator:test")

	builder.StampTLSMaterialHash(sts, builder.SidecarContainerName, "aaaa")
	builder.StampTLSMaterialHash(sts, builder.SidecarContainerName, "bbbb")

	count := 0
	for _, c := range sts.Spec.Template.Spec.Containers {
		for _, env := range c.Env {
			if env.Name == builder.TLSMaterialHashEnvName {
				count++
			}
		}
	}
	assert.Equal(t, 1, count)
	assert.Equal(t, "bbbb", recordedOn(sts))
}

func TestStampTLSMaterialHash_SkipsClustersWithoutTLS(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v, tlsSecret("test-tls", "default", "1"))

	sts := builder.BuildStatefulSet(v, "operator:test")
	r.stampTLSMaterialHash(context.Background(), v, sts, "test-tls", builder.SidecarContainerName)

	assert.Empty(t, recordedOn(sts))
}

// cert-manager has not issued yet. Leaving the record off is what keeps the
// StatefulSet writable in that window, and a pod without the record is never
// restarted for one.
func TestStampTLSMaterialHash_AbsentSecretLeavesNoRecord(t *testing.T) {
	v := certManagerValkey("test")
	r, _ := newTestReconciler(v)

	sts := builder.BuildStatefulSet(v, "operator:test")
	r.stampTLSMaterialHash(context.Background(), v, sts,
		builder.ValkeyTLSSecretName(v), builder.SidecarContainerName)

	assert.Empty(t, recordedOn(sts))
}

// --- the Secret watch ---

func TestSecretConcernsValkey(t *testing.T) {
	plain := newTestValkey("plain", "default", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{SecretName: "auth-secret"}
	})
	certManager := certManagerValkey("cm")
	unified := certManagerValkey("uni", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true}
		v.Spec.TLS.UnifiedCertificate = true
	})
	provided := newTestValkey("prov", "default", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true, SecretName: "my-own-tls"}
	})

	tests := []struct {
		name   string
		v      *vkov1.Valkey
		secret string
		want   bool
	}{
		{"auth secret still matches", plain, "auth-secret", true},
		{"unrelated secret on a non-TLS cluster", plain, "cm-tls", false},
		{"cert-manager valkey secret", certManager, "cm-tls", true},
		{"cert-manager sentinel secret", certManager, "cm-sentinel-tls", true},
		{"unified mode maps both tiers onto one secret", unified, "uni-tls", true},
		{"unified mode does not react to the legacy name", unified, "uni-sentinel-tls", false},
		{"user-provided secret", provided, "my-own-tls", true},
		{"a stranger's secret", certManager, "some-other-secret", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, secretConcernsValkey(tt.v, tt.secret))
		})
	}
}

// --- podTLSMaterialHashChanged, the upgrade-neutrality guard ---

func TestPodTLSMaterialHashChanged(t *testing.T) {
	withHash := func(hash string) *corev1.Pod {
		return &corev1.Pod{Spec: tlsCarrierSpec(common.ComponentValkey, hash)}
	}

	assert.False(t, podTLSMaterialHashChanged(withHash("aaaa"), ""),
		"no fingerprint on the template means nothing to compare against")
	assert.False(t, podTLSMaterialHashChanged(withHash(""), "aaaa"),
		"a pod created before the operator wrote fingerprints must never be restarted for one")
	assert.False(t, podTLSMaterialHashChanged(withHash("aaaa"), "aaaa"))
	assert.True(t, podTLSMaterialHashChanged(withHash("aaaa"), "bbbb"))
}

// --- the carrier move, and the migration it has to survive (ADR 0031) ---

func TestPodTLSMaterialHashChanged_ReadsTheSpecAndFallsBackToTheAnnotation(t *testing.T) {
	legacy := tlsTierPodLegacy(certManagerValkey("test"), common.ComponentValkey, 0, "aaaa")

	assert.True(t, podTLSMaterialHashChanged(legacy, "bbbb"),
		"a pod that predates the move stays measured; without the fallback a rotation "+
			"would neither replace it nor report it")
	assert.False(t, podTLSMaterialHashChanged(legacy, "aaaa"))
}

// The forgery the move exists to stop, at the level the rolling update reads it.
// Patching the annotation onto a pod that carries the env changes nothing,
// because the env is what is read -- and env is not one of the pod spec fields
// the API server lets an update change, so this patch cannot happen at all on a
// real cluster.
func TestPodTLSMaterialHashChanged_TheSpecWinsOverAForgedAnnotation(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{builder.AnnotationTLSMaterialHash: "desired"},
		},
		Spec: tlsCarrierSpec(common.ComponentValkey, "stale"),
	}

	assert.True(t, podTLSMaterialHashChanged(pod, "desired"),
		"the pod runs stale material and says so in its spec; the annotation is inert")
}

// The other half of the deletion attack: with the carrier in metadata, a merge
// patch setting the key to null made the pod unmeasured. Deleting an env var is
// not a patch the API server accepts, so the same move has no effect.
func TestPodTLSMaterialHashChanged_DeletingTheAnnotationNoLongerHidesAPod(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}},
		Spec:       tlsCarrierSpec(common.ComponentValkey, "stale"),
	}

	assert.True(t, podTLSMaterialHashChanged(pod, "desired"))
}

// The whole point of the mechanism, at the level the rolling update reads it: a
// rotated Secret makes every pod of the tier outdated.
func TestPodNeedsUpdate_RotatedTLSMaterialSchedulesTheReplacement(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
			{Name: builder.SidecarContainerName, Env: []corev1.EnvVar{
				{Name: builder.TLSMaterialHashEnvName, Value: "old"},
			}},
		}},
	}

	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "", "", "old", nil))
	assert.True(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "", "", "new", nil))
}

func TestSentinelPodNeedsUpdate_RotatedTLSMaterialSchedulesTheReplacement(t *testing.T) {
	template := corev1.PodTemplateSpec{Spec: tlsCarrierSpec(common.ComponentSentinel, "new")}
	pod := &corev1.Pod{Spec: tlsCarrierSpec(common.ComponentSentinel, "old")}

	assert.True(t, sentinelPodNeedsUpdate(pod, template))

	pod.Spec = tlsCarrierSpec(common.ComponentSentinel, "new")
	assert.False(t, sentinelPodNeedsUpdate(pod, template))

	pod.Spec = tlsCarrierSpec(common.ComponentSentinel, "")
	assert.False(t, sentinelPodNeedsUpdate(pod, template),
		"a Sentinel pod created before this mechanism is unmeasured, not stale")
}

// The Sentinel tier is the population the annotation fallback was added for: it
// carries no sidecar, so a plain operator upgrade never rolls it and its pods
// keep the superseded carrier until something else does (ADR 0005 D11).
func TestSentinelPodNeedsUpdate_APodFromBeforeTheMoveStaysMeasured(t *testing.T) {
	template := corev1.PodTemplateSpec{Spec: tlsCarrierSpec(common.ComponentSentinel, "new")}
	pod := tlsTierPodLegacy(certManagerValkey("test"), common.ComponentSentinel, 0, "old")

	assert.True(t, sentinelPodNeedsUpdate(pod, template))
}

// --- reportTLSMaterialStale ---

func staleCondition(t *testing.T, r *ValkeyReconciler, v *vkov1.Valkey) *metav1.Condition {
	t.Helper()
	fresh := &vkov1.Valkey{}
	require.NoError(t, r.Get(context.Background(),
		types.NamespacedName{Name: v.Name, Namespace: v.Namespace}, fresh))
	return meta.FindStatusCondition(fresh.Status.Conditions, vkov1.ConditionTypeTLSMaterialStale)
}

func TestReportTLSMaterialStale_CurrentPodsClearTheCondition(t *testing.T) {
	v := certManagerValkey("test")
	secret := tlsSecret(builder.ValkeyTLSSecretName(v), v.Namespace, "1")
	hash := builder.ComputeTLSMaterialHash(secret)

	r, _ := newTestReconciler(v, secret,
		tlsTierSts(v, common.ComponentValkey, 3, hash),
		tlsTierPod(v, common.ComponentValkey, 0, hash),
		tlsTierPod(v, common.ComponentValkey, 1, hash),
		tlsTierPod(v, common.ComponentValkey, 2, hash),
	)

	require.NoError(t, r.reportTLSMaterialStale(context.Background(), v))

	cond := staleCondition(t, r, v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, vkov1.ReasonTLSMaterialCurrent, cond.Reason)
}

func TestReportTLSMaterialStale_NamesThePodsHoldingThePreviousMaterial(t *testing.T) {
	v := certManagerValkey("test")
	secret := tlsSecret(builder.ValkeyTLSSecretName(v), v.Namespace, "2")
	hash := builder.ComputeTLSMaterialHash(secret)
	previous := builder.ComputeTLSMaterialHash(tlsSecret(builder.ValkeyTLSSecretName(v), v.Namespace, "1"))

	r, _ := newTestReconciler(v, secret,
		tlsTierSts(v, common.ComponentValkey, 3, hash),
		tlsTierPod(v, common.ComponentValkey, 0, hash),
		tlsTierPod(v, common.ComponentValkey, 1, previous),
		tlsTierPod(v, common.ComponentValkey, 2, previous),
	)

	require.NoError(t, r.reportTLSMaterialStale(context.Background(), v))

	cond := staleCondition(t, r, v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonTLSMaterialRollPending, cond.Reason)
	assert.Contains(t, cond.Message, "test-1")
	assert.Contains(t, cond.Message, "test-2")
	assert.NotContains(t, cond.Message, "test-0 ")
}

// The Sentinel tier is measured too, against its own Secret.
func TestReportTLSMaterialStale_CoversTheSentinelTier(t *testing.T) {
	v := certManagerValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	dataSecret := tlsSecret(builder.ValkeyTLSSecretName(v), v.Namespace, "2")
	sentinelSecret := tlsSecret(builder.SentinelTLSSecretName(v), v.Namespace, "2")
	dataHash := builder.ComputeTLSMaterialHash(dataSecret)
	sentinelHash := builder.ComputeTLSMaterialHash(sentinelSecret)

	r, _ := newTestReconciler(v, dataSecret, sentinelSecret,
		tlsTierSts(v, common.ComponentValkey, 1, dataHash),
		tlsTierPod(v, common.ComponentValkey, 0, dataHash),
		tlsTierSts(v, common.ComponentSentinel, 1, sentinelHash),
		tlsTierPod(v, common.ComponentSentinel, 0, "stale"),
	)

	require.NoError(t, r.reportTLSMaterialStale(context.Background(), v))

	cond := staleCondition(t, r, v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Contains(t, cond.Message, "test-sentinel-0")
	assert.Contains(t, cond.Message, common.ComponentSentinel)
}

// The upgrade that ships this mechanism must not light up the fleet.
func TestReportTLSMaterialStale_PodsWithoutTheAnnotationAreUnmeasured(t *testing.T) {
	v := certManagerValkey("test")
	secret := tlsSecret(builder.ValkeyTLSSecretName(v), v.Namespace, "1")
	hash := builder.ComputeTLSMaterialHash(secret)

	r, _ := newTestReconciler(v, secret,
		tlsTierSts(v, common.ComponentValkey, 2, hash),
		tlsTierPod(v, common.ComponentValkey, 0, ""),
		tlsTierPod(v, common.ComponentValkey, 1, ""),
	)

	require.NoError(t, r.reportTLSMaterialStale(context.Background(), v))

	cond := staleCondition(t, r, v)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
}

// A Secret that cannot be read is "not measured", never "everything is current":
// overwriting a True on a failed Get would clear the one signal an operator acts on.
func TestReportTLSMaterialStale_AbsentSecretWritesNothing(t *testing.T) {
	v := certManagerValkey("test")
	r, _ := newTestReconciler(v,
		tlsTierSts(v, common.ComponentValkey, 1, "whatever"),
		tlsTierPod(v, common.ComponentValkey, 0, "whatever"),
	)

	require.NoError(t, r.reportTLSMaterialStale(context.Background(), v))

	assert.Nil(t, staleCondition(t, r, v))
}

// A StatefulSet this Valkey does not control is treated as absent, and this
// function stays quiet about it -- reconcileStatefulSet is the one reporter.
func TestReportTLSMaterialStale_ForeignStatefulSetIsTreatedAsAbsent(t *testing.T) {
	v := certManagerValkey("test")
	secret := tlsSecret(builder.ValkeyTLSSecretName(v), v.Namespace, "1")
	foreign := tlsTierSts(v, common.ComponentValkey, 1, "whatever")
	foreign.OwnerReferences = nil

	r, _ := newTestReconciler(v, secret, foreign)

	require.NoError(t, r.reportTLSMaterialStale(context.Background(), v))

	assert.Nil(t, staleCondition(t, r, v))
}

// The guard on the stored condition: a second pass over unchanged state must not
// write again. Without it the operator re-Gets and re-evaluates the CR once per
// pass on every TLS cluster, forever, for a verdict that changes twice per
// rotation.
func TestReportTLSMaterialStale_RepeatedPassesDoNotRewriteTheCondition(t *testing.T) {
	v := certManagerValkey("test")
	secret := tlsSecret(builder.ValkeyTLSSecretName(v), v.Namespace, "1")
	hash := builder.ComputeTLSMaterialHash(secret)

	r, _ := newTestReconciler(v, secret,
		tlsTierSts(v, common.ComponentValkey, 1, hash),
		tlsTierPod(v, common.ComponentValkey, 0, hash),
	)

	require.NoError(t, r.reportTLSMaterialStale(context.Background(), v))
	first := staleCondition(t, r, v)
	require.NotNil(t, first)

	require.NoError(t, r.reportTLSMaterialStale(context.Background(), v))
	second := staleCondition(t, r, v)
	require.NotNil(t, second)

	assert.Equal(t, first.LastTransitionTime, second.LastTransitionTime,
		"an unchanged verdict must not be rewritten")
}

func TestReportTLSMaterialStale_NonTLSClusterIsNeverMeasured(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	require.NoError(t, r.reportTLSMaterialStale(context.Background(), v))

	assert.Nil(t, staleCondition(t, r, v))
}

// --- the full path, from a rotated Secret to a changed pod template ---

func TestReconcileStatefulSet_RotationChangesThePodTemplateFingerprint(t *testing.T) {
	v := certManagerValkey("test")
	secret := tlsSecret(builder.ValkeyTLSSecretName(v), v.Namespace, "1")
	r, c := newTestReconciler(v, secret)
	ctx := context.Background()

	require.NoError(t, r.reconcileStatefulSet(ctx, v))

	sts := &appsv1.StatefulSet{}
	stsKey := types.NamespacedName{Name: common.StatefulSetName(v, common.ComponentValkey), Namespace: v.Namespace}
	require.NoError(t, c.Get(ctx, stsKey, sts))
	before := recordedOn(sts)
	require.NotEmpty(t, before)

	// cert-manager rotates the certificate in place.
	rotated := &corev1.Secret{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace}, rotated))
	rotated.Data["tls.crt"] = []byte("cert-2")
	rotated.Data["tls.key"] = []byte("key-2")
	require.NoError(t, c.Update(ctx, rotated))

	require.NoError(t, r.reconcileStatefulSet(ctx, v))

	require.NoError(t, c.Get(ctx, stsKey, sts))
	after := recordedOn(sts)
	assert.NotEqual(t, before, after, "the rotation must reach the pod template")
	assert.Equal(t, after, tlsMaterialHashFromSts(sts))
}
