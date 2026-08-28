//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

// A certificate rotation has to reach the pod template without anyone touching
// the CR, and that claim can only be settled here.
//
// The CR watch carries GenerationChangedPredicate, so a Secret rewritten by
// cert-manager is the *only* thing that can start this pass: no spec changes, no
// owned object changes, and the operator's own status writes are dropped by the
// predicate. What is under test is therefore the watch, the mapping function and
// the annotation stamp working together against a real work queue -- a unit test
// can prove each of the three and none of the wiring, which is exactly how the
// missing TLS half of findValkeyForSecret survived: every part of the operator
// was correct and nothing ever enqueued.
//
// envtest runs no kubelet, so no pod exists here. That is fine for this test and
// is why it asserts on the StatefulSet template rather than on a roll: the
// rolling update is ordinary machinery driven by a template change, and the
// template change is the part this mechanism adds.
//
// Since 2026-08-27 the record lives in the pod spec rather than in the template
// annotations (ADR 0031), and the API server is the authority on the half that
// makes the move worth making: env is not one of the fields a pod update may
// change. templateFingerprint reads it the way every consumer does.

const (
	tlsRotationInterval = 250 * time.Millisecond
	tlsRotationTimeout  = 60 * time.Second
)

// templateFingerprint reads the fingerprint off a StatefulSet template the way
// the rolling update and the staleness report read it.
func templateFingerprint(sts *appsv1.StatefulSet) string {
	return builder.RecordedTLSMaterialHash(&sts.Spec.Template.Spec, sts.Spec.Template.Annotations)
}

func tlsMaterialSecret(name, revision string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Data: map[string][]byte{
			builder.TLSCACertKey:     []byte("ca-" + revision),
			builder.TLSCertKey:       []byte("cert-" + revision),
			builder.TLSPrivateKeyKey: []byte("key-" + revision),
		},
	}
}

func TestTLSMaterialRotation_ReachesThePodTemplate_Integration(t *testing.T) {
	ctx := testCtx
	crName := "tls-rotation-test"
	secretName := crName + "-provided-tls"
	key := types.NamespacedName{Name: crName, Namespace: "default"}
	secretKey := types.NamespacedName{Name: secretName, Namespace: "default"}

	secret := tlsMaterialSecret(secretName, "1")
	require.NoError(t, k8sClient.Create(ctx, secret))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, secret) })

	v := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: "default"},
		Spec: vkov1.ValkeySpec{
			Replicas: 1,
			Image:    "valkey/valkey:8.0",
			// A user-provided Secret rather than cert-manager: envtest has no
			// cert-manager CRDs, and the mechanism is identical either way -- both
			// resolve through builder.ValkeyTLSSecretName.
			TLS: &vkov1.TLSSpec{Enabled: true, SecretName: secretName},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, v))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, v) })

	waitForStatefulSet(t, crName, tlsRotationTimeout)

	var before string
	awaitTLSRotation(t, "the initial fingerprint must be stamped onto the pod template",
		func(ctx context.Context) (bool, string) {
			sts := &appsv1.StatefulSet{}
			if err := k8sClient.Get(ctx, key, sts); err != nil {
				return false, err.Error()
			}
			before = templateFingerprint(sts)
			return before != "", fmt.Sprintf("containers %v", sts.Spec.Template.Spec.Containers)
		})

	stored := &corev1.Secret{}
	require.NoError(t, k8sClient.Get(ctx, secretKey, stored))
	assert.Equal(t, builder.ComputeTLSMaterialHash(stored), before,
		"the stamped value must be the fingerprint of the Secret the pods mount")

	t.Run("rotating the Secret alone changes the pod template", func(t *testing.T) {
		require.NoError(t, wait.PollUntilContextTimeout(ctx, tlsRotationInterval, tlsRotationTimeout, true,
			func(ctx context.Context) (bool, error) {
				current := &corev1.Secret{}
				if err := k8sClient.Get(ctx, secretKey, current); err != nil {
					return false, nil
				}
				current.Data[builder.TLSCertKey] = []byte("cert-2")
				current.Data[builder.TLSPrivateKeyKey] = []byte("key-2")
				return k8sClient.Update(ctx, current) == nil, nil
			}), "the rotation write itself must land")

		awaitTLSRotation(t,
			"nothing but the Secret watch can start this pass, so a changed fingerprint proves the wiring",
			func(ctx context.Context) (bool, string) {
				sts := &appsv1.StatefulSet{}
				if err := k8sClient.Get(ctx, key, sts); err != nil {
					return false, err.Error()
				}
				got := templateFingerprint(sts)
				return got != "" && got != before, "fingerprint " + got
			})

		rotated := &corev1.Secret{}
		require.NoError(t, k8sClient.Get(ctx, secretKey, rotated))
		sts := &appsv1.StatefulSet{}
		require.NoError(t, k8sClient.Get(ctx, key, sts))
		assert.Equal(t, builder.ComputeTLSMaterialHash(rotated), templateFingerprint(sts))
	})

	t.Run("a cluster with no pods carries the condition as False", func(t *testing.T) {
		// envtest has no kubelet: there is a StatefulSet and no pods, so nothing is
		// stale and the level says so. The value of asserting it here is the other
		// half -- that the evaluator runs at all on a TLS cluster, from a reconcile
		// step rather than from the workload pass.
		awaitTLSRotation(t, "the TLSMaterialStale level must be measured on every TLS cluster",
			func(ctx context.Context) (bool, string) {
				current := &vkov1.Valkey{}
				if err := k8sClient.Get(ctx, key, current); err != nil {
					return false, err.Error()
				}
				cond := meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeTLSMaterialStale)
				if cond == nil {
					return false, "condition absent"
				}
				return cond.Status == metav1.ConditionFalse, "status " + string(cond.Status)
			})
	})
}

// A cluster without TLS must never gain the condition, which is the upgrade-
// neutrality half of the level: the evaluator handles the non-TLS case itself
// (it only ever retracts a standing True there, T24(d)), so nothing is written
// rather than a False being stamped across the fleet.
func TestTLSMaterialStale_NonTLSClusterIsNeverMeasured_Integration(t *testing.T) {
	ctx := testCtx
	crName := "tls-rotation-plain"
	key := types.NamespacedName{Name: crName, Namespace: "default"}

	v := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: "default"},
		Spec:       vkov1.ValkeySpec{Replicas: 1, Image: "valkey/valkey:8.0"},
	}
	require.NoError(t, k8sClient.Create(ctx, v))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, v) })

	sts := waitForStatefulSet(t, crName, tlsRotationTimeout)
	assert.Empty(t, templateFingerprint(sts),
		"a cluster without TLS mounts no material and must carry no fingerprint")

	current := &vkov1.Valkey{}
	require.NoError(t, k8sClient.Get(ctx, key, current))
	assert.Nil(t, meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeTLSMaterialStale))
}

// The create gate of ADR 0030 D12, against a real API server and the real work
// queue: a TLS cluster whose Secret does not exist yet gets no StatefulSet, and
// the StatefulSet that appears once the Secret does carries the fingerprint from
// birth. What is under test beyond the unit tier is the progress wiring -- the
// Secret create is the only event between "refused" and "created", so the
// StatefulSet appearing proves the watch (or the bounded recheck) re-enters the
// pass without anyone touching the CR.
func TestTLSMaterialGate_TheStatefulSetWaitsForTheSecret_Integration(t *testing.T) {
	ctx := testCtx
	crName := "tls-gate-test"
	secretName := crName + "-provided-tls"
	key := types.NamespacedName{Name: crName, Namespace: "default"}

	v := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: "default"},
		Spec: vkov1.ValkeySpec{
			Replicas: 1,
			Image:    "valkey/valkey:8.0",
			TLS:      &vkov1.TLSSpec{Enabled: true, SecretName: secretName},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, v))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, v) })

	// The refusal holds while the Secret is missing. Three seconds of settled
	// absence is the strongest "never" an integration test can afford.
	require.Never(t, func() bool {
		sts := &appsv1.StatefulSet{}
		return k8sClient.Get(ctx, key, sts) == nil
	}, 3*time.Second, tlsRotationInterval,
		"no StatefulSet may be created before its template can be armed")

	secret := tlsMaterialSecret(secretName, "1")
	require.NoError(t, k8sClient.Create(ctx, secret))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, secret) })

	awaitTLSRotation(t, "the StatefulSet must appear once the Secret exists, armed from birth",
		func(ctx context.Context) (bool, string) {
			sts := &appsv1.StatefulSet{}
			if err := k8sClient.Get(ctx, key, sts); err != nil {
				return false, err.Error()
			}
			return templateFingerprint(sts) == builder.ComputeTLSMaterialHash(secret),
				"fingerprint " + templateFingerprint(sts)
		})
}

// awaitTLSRotation polls check until it reports true, failing with the last
// observation it saw.
func awaitTLSRotation(t *testing.T, what string, check func(context.Context) (bool, string)) {
	t.Helper()

	var last string
	err := wait.PollUntilContextTimeout(testCtx, tlsRotationInterval, tlsRotationTimeout, true,
		func(ctx context.Context) (bool, error) {
			ok, observed := check(ctx)
			last = observed
			return ok, nil
		})
	require.NoErrorf(t, err, "%s (last observed: %s)", what, last)
}

// The whole reason the fingerprint left pod metadata: the API server refuses a
// change to pod env and accepts any change to pod annotations. That is a claim
// about kube-apiserver, so it is settled here and nowhere else.
//
// The pod is created directly rather than by a StatefulSet. envtest runs no
// kubelet, so it stays Pending forever -- which is all this test needs, because
// admission and validation have already run by then.
func TestTLSMaterialCarrier_TheAPIServerRefusesToChangeIt_Integration(t *testing.T) {
	ctx := testCtx
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "tls-carrier-immutability",
			Namespace:   "default",
			Annotations: map[string]string{builder.AnnotationTLSMaterialHash: "aaaa"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  builder.SidecarContainerName,
				Image: "example.invalid/sidecar:test",
				Env:   []corev1.EnvVar{{Name: builder.TLSMaterialHashEnvName, Value: "aaaa"}},
			}},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, pod))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, pod) })

	t.Run("the superseded annotation carrier is forgeable", func(t *testing.T) {
		patch := []byte(`{"metadata":{"annotations":{"vko.gtrfc.com/tls-material-hash":"forged"}}}`)
		require.NoError(t, k8sClient.Patch(ctx, pod.DeepCopy(), rawMergePatch(patch)),
			"anything holding pods: patch can set the annotation to any value")

		nulled := []byte(`{"metadata":{"annotations":{"vko.gtrfc.com/tls-material-hash":null}}}`)
		require.NoError(t, k8sClient.Patch(ctx, pod.DeepCopy(), rawMergePatch(nulled)),
			"and can delete it, which is the cheaper attack: a pod with no record is unmeasured")
	})

	t.Run("the spec carrier is not", func(t *testing.T) {
		patch := []byte(`{"spec":{"containers":[{"name":"sidecar","env":[{"name":"VKO_TLS_MATERIAL_HASH","value":"forged"}]}]}}`)
		err := k8sClient.Patch(ctx, pod.DeepCopy(), rawMergePatch(patch))
		require.Error(t, err, "env is not one of the pod spec fields an update may change")
		assert.Contains(t, err.Error(), "spec: Forbidden")
	})

	t.Run("what survived is what the operator wrote", func(t *testing.T) {
		stored := &corev1.Pod{}
		require.NoError(t, k8sClient.Get(ctx,
			types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, stored))

		assert.Equal(t, "aaaa",
			builder.RecordedTLSMaterialHash(&stored.Spec, stored.Annotations),
			"the annotation was forged and then deleted; the record read is the one in the spec")
	})
}

// rawMergePatch wraps a literal JSON merge patch for the typed client.
func rawMergePatch(data []byte) client.Patch {
	return client.RawPatch(types.MergePatchType, data)
}
