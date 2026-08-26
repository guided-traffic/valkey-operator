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
	if hash != "" {
		sts.Spec.Template.Annotations = map[string]string{builder.AnnotationTLSMaterialHash: hash}
	}
	return sts
}

// tlsTierPod builds a pod of a tier carrying the given fingerprint. An empty hash
// leaves the annotation off, which is what every pod created before this
// mechanism shipped looks like.
func tlsTierPod(v *vkov1.Valkey, component string, ordinal int, hash string) *corev1.Pod {
	name := fmt.Sprintf("%s-%d", common.StatefulSetName(v, component), ordinal)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: v.Namespace},
	}
	if hash != "" {
		pod.Annotations = map[string]string{builder.AnnotationTLSMaterialHash: hash}
	}
	ownedByTestSts(v, component, pod)
	return pod
}

// --- stampTLSMaterialHash ---

func TestStampTLSMaterialHash_WritesTheFingerprintOfTheMountedSecret(t *testing.T) {
	v := certManagerValkey("test")
	secret := tlsSecret(builder.ValkeyTLSSecretName(v), v.Namespace, "1")
	r, _ := newTestReconciler(v, secret)

	sts := &appsv1.StatefulSet{}
	r.stampTLSMaterialHash(context.Background(), v, sts, builder.ValkeyTLSSecretName(v))

	assert.Equal(t, builder.ComputeTLSMaterialHash(secret),
		sts.Spec.Template.Annotations[builder.AnnotationTLSMaterialHash])
}

func TestStampTLSMaterialHash_SkipsClustersWithoutTLS(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v, tlsSecret("test-tls", "default", "1"))

	sts := &appsv1.StatefulSet{}
	r.stampTLSMaterialHash(context.Background(), v, sts, "test-tls")

	assert.NotContains(t, sts.Spec.Template.Annotations, builder.AnnotationTLSMaterialHash)
}

// cert-manager has not issued yet. Leaving the annotation off is what keeps the
// StatefulSet writable in that window, and a pod without the annotation is never
// restarted for one.
func TestStampTLSMaterialHash_AbsentSecretLeavesNoAnnotation(t *testing.T) {
	v := certManagerValkey("test")
	r, _ := newTestReconciler(v)

	sts := &appsv1.StatefulSet{}
	r.stampTLSMaterialHash(context.Background(), v, sts, builder.ValkeyTLSSecretName(v))

	assert.NotContains(t, sts.Spec.Template.Annotations, builder.AnnotationTLSMaterialHash)
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
		p := &corev1.Pod{}
		if hash != "" {
			p.Annotations = map[string]string{builder.AnnotationTLSMaterialHash: hash}
		}
		return p
	}

	assert.False(t, podTLSMaterialHashChanged(withHash("aaaa"), ""),
		"no fingerprint on the template means nothing to compare against")
	assert.False(t, podTLSMaterialHashChanged(withHash(""), "aaaa"),
		"a pod created before the operator wrote fingerprints must never be restarted for one")
	assert.False(t, podTLSMaterialHashChanged(withHash("aaaa"), "aaaa"))
	assert.True(t, podTLSMaterialHashChanged(withHash("aaaa"), "bbbb"))
}

// The whole point of the mechanism, at the level the rolling update reads it: a
// rotated Secret makes every pod of the tier outdated.
func TestPodNeedsUpdate_RotatedTLSMaterialSchedulesTheReplacement(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{builder.AnnotationTLSMaterialHash: "old"},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0",
		}}},
	}

	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "", "", "old", nil))
	assert.True(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "", "", "new", nil))
}

func TestSentinelPodNeedsUpdate_RotatedTLSMaterialSchedulesTheReplacement(t *testing.T) {
	template := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{builder.AnnotationTLSMaterialHash: "new"},
		},
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Annotations: map[string]string{builder.AnnotationTLSMaterialHash: "old"},
	}}

	assert.True(t, sentinelPodNeedsUpdate(pod, template))

	pod.Annotations[builder.AnnotationTLSMaterialHash] = "new"
	assert.False(t, sentinelPodNeedsUpdate(pod, template))

	delete(pod.Annotations, builder.AnnotationTLSMaterialHash)
	assert.False(t, sentinelPodNeedsUpdate(pod, template),
		"a Sentinel pod created before this mechanism is unmeasured, not stale")
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
	before := sts.Spec.Template.Annotations[builder.AnnotationTLSMaterialHash]
	require.NotEmpty(t, before)

	// cert-manager rotates the certificate in place.
	rotated := &corev1.Secret{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace}, rotated))
	rotated.Data["tls.crt"] = []byte("cert-2")
	rotated.Data["tls.key"] = []byte("key-2")
	require.NoError(t, c.Update(ctx, rotated))

	require.NoError(t, r.reconcileStatefulSet(ctx, v))

	require.NoError(t, c.Get(ctx, stsKey, sts))
	after := sts.Spec.Template.Annotations[builder.AnnotationTLSMaterialHash]
	assert.NotEqual(t, before, after, "the rotation must reach the pod template")
	assert.Equal(t, after, tlsMaterialHashFromSts(sts))
}
