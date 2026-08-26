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

const (
	tlsRotationInterval = 250 * time.Millisecond
	tlsRotationTimeout  = 60 * time.Second
)

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
			before = sts.Spec.Template.Annotations[builder.AnnotationTLSMaterialHash]
			return before != "", fmt.Sprintf("annotations %v", sts.Spec.Template.Annotations)
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
				got := sts.Spec.Template.Annotations[builder.AnnotationTLSMaterialHash]
				return got != "" && got != before, "fingerprint " + got
			})

		rotated := &corev1.Secret{}
		require.NoError(t, k8sClient.Get(ctx, secretKey, rotated))
		sts := &appsv1.StatefulSet{}
		require.NoError(t, k8sClient.Get(ctx, key, sts))
		assert.Equal(t, builder.ComputeTLSMaterialHash(rotated),
			sts.Spec.Template.Annotations[builder.AnnotationTLSMaterialHash])
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
// neutrality half of the level: the reconcile step is gated on IsTLSEnabled, so
// nothing is written rather than a False being stamped across the fleet.
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
	assert.NotContains(t, sts.Spec.Template.Annotations, builder.AnnotationTLSMaterialHash,
		"a cluster without TLS mounts no material and must carry no fingerprint")

	current := &vkov1.Valkey{}
	require.NoError(t, k8sClient.Get(ctx, key, current))
	assert.Nil(t, meta.FindStatusCondition(current.Status.Conditions, vkov1.ConditionTypeTLSMaterialStale))
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
