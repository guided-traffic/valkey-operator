package controller

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// The tests in this file pin ADR 0007 D2: while the StatefulSet write is rejected (an
// admission webhook, a quota, any write failure), the aggregate-reconcile design
// still runs the workload pass. The rolling update must then compare pods
// against the *persisted* template, not against the CR, or it deletes pods that
// the statefulset-controller recreates from the still-old template — once per
// requeue, for as long as the write stays blocked.

const testOperatorImage = "ghcr.io/guided-traffic/valkey-operator:test"

// stsForValkey builds the StatefulSet the operator would persist for v.
func stsForValkey(v *vkov1.Valkey) *appsv1.StatefulSet {
	sts := builder.BuildStatefulSet(v, testOperatorImage)
	replicas := v.Spec.Replicas
	sts.Spec.Replicas = &replicas
	sts.Status.Replicas = replicas
	sts.Status.ReadyReplicas = replicas
	// The ADR 0020 guards treat an un-owned StatefulSet as absent.
	controllerRefTo(v, sts)
	return sts
}

// podFromStsTemplate builds a ready pod exactly as the statefulset-controller
// would create it from sts.Spec.Template: same container images, same template
// annotations. A pod built this way is by definition up to date with respect to
// the persisted template.
func podFromStsTemplate(v *vkov1.Valkey, sts *appsv1.StatefulSet, ordinal int) *corev1.Pod {
	annotations := map[string]string{}
	for k, val := range sts.Spec.Template.Annotations {
		annotations[k] = val
	}

	containers := make([]corev1.Container, 0, len(sts.Spec.Template.Spec.Containers))
	for _, c := range sts.Spec.Template.Spec.Containers {
		containers = append(containers, corev1.Container{
			Name:      c.Name,
			Image:     c.Image,
			Resources: *c.Resources.DeepCopy(),
		})
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        fmt.Sprintf("%s-%d", sts.Name, ordinal),
			Namespace:   v.Namespace,
			Annotations: annotations,
			Labels: map[string]string{
				common.LabelInstance:  v.Name,
				common.LabelComponent: common.ComponentValkey,
			},
		},
		Spec: corev1.PodSpec{Containers: containers},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
}

func podExists(t *testing.T, c client.Client, name string) bool {
	t.Helper()
	pod := &corev1.Pod{}
	err := c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, pod)
	return err == nil
}

// TestCheckAndHandleRollingUpdate_NoUpdateWhenImageWriteBlocked is the ADR 0007 D2
// regression guard: the CR asks for a new image, the StatefulSet still carries
// the old one because its Update was rejected, and the pod matches the
// StatefulSet. Deleting that pod would achieve nothing — it comes back on the
// same old template.
func TestCheckAndHandleRollingUpdate_NoUpdateWhenImageWriteBlocked(t *testing.T) {
	old := newTestValkey("test", "default")
	sts := stsForValkey(old)
	pod0 := podFromStsTemplate(old, sts, 0)

	// The CR the operator reconciles asks for 9.0; the persisted template does not.
	desired := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Image = "valkey/valkey:9.0"
	})

	r, c := newTestReconciler(desired, sts, pod0)

	result := r.checkAndHandleRollingUpdate(context.Background(), desired)

	assert.Nil(t, result.Error)
	assert.False(t, result.NeedsRequeue,
		"no rolling update may start against a template that was never persisted")
	assert.True(t, podExists(t, c, pod0.Name),
		"the pod must not be deleted while the StatefulSet still carries the old image")
}

// TestCheckAndHandleRollingUpdate_NoUpdateWhenConfigWriteBlocked is the same
// guard for the second CR-derived input, the config hash.
func TestCheckAndHandleRollingUpdate_NoUpdateWhenConfigWriteBlocked(t *testing.T) {
	old := newTestValkey("test", "default")
	sts := stsForValkey(old)
	pod0 := podFromStsTemplate(old, sts, 0)

	// Enabling auth changes the generated Valkey config, hence the config hash.
	desired := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{SecretName: "valkey-auth", SecretPasswordKey: "password"}
	})
	require.NotEqual(t, builder.ComputeConfigHash(old), builder.ComputeConfigHash(desired),
		"the fixture must actually change the config hash, otherwise the test proves nothing")

	r, c := newTestReconciler(desired, sts, pod0)

	result := r.checkAndHandleRollingUpdate(context.Background(), desired)

	assert.Nil(t, result.Error)
	assert.False(t, result.NeedsRequeue,
		"no rolling update may start against a config hash that was never persisted")
	assert.True(t, podExists(t, c, pod0.Name),
		"the pod must not be deleted while the StatefulSet still carries the old config hash")
}

// TestCheckAndHandleRollingUpdate_StartsOnceTemplatePersisted is the positive
// control. Without it, a helper that always reported "no drift" would pass every
// test above while disabling rolling updates outright.
func TestCheckAndHandleRollingUpdate_StartsOnceTemplatePersisted(t *testing.T) {
	old := newTestValkey("test", "default")
	oldSts := stsForValkey(old)
	pod0 := podFromStsTemplate(old, oldSts, 0)

	// The write went through this time: the StatefulSet carries 9.0, the pod
	// still runs 8.0.
	desired := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Image = "valkey/valkey:9.0"
	})
	newSts := stsForValkey(desired)

	r, c := newTestReconciler(desired, newSts, pod0)

	result := r.checkAndHandleRollingUpdate(context.Background(), desired)

	assert.Nil(t, result.Error)
	assert.True(t, result.NeedsRequeue, "a persisted image change must start the rolling update")
	assert.False(t, podExists(t, c, pod0.Name),
		"the outdated pod must be deleted once the template it targets exists")
}

// TestCollectPodStates_IgnoresUnpersistedImageChange covers the multi-replica
// path, which reaches podNeedsUpdate through collectPodStates rather than
// through the gate in checkAndHandleRollingUpdate.
func TestCollectPodStates_IgnoresUnpersistedImageChange(t *testing.T) {
	old := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	sts := stsForValkey(old)

	objs := []client.Object{sts}
	desired := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:9.0"
	})
	for i := 0; i < 3; i++ {
		objs = append(objs, podFromStsTemplate(old, sts, i))
	}
	objs = append(objs, desired)

	r, _ := newTestReconciler(objs...)

	pods, _, err := r.collectPodStates(context.Background(), desired, sts)
	require.NoError(t, err)
	require.Len(t, pods, 3)
	for _, ps := range pods {
		assert.False(t, ps.needsUpdate,
			"pod %s matches the persisted template and must not be marked for replacement", ps.name)
	}
}

// TestReconcile_BlockedStatefulSetWriteDoesNotDeletePods drives the same defect
// through a full Reconcile pass, the way the incident produced it: the
// StatefulSet Update is rejected, reconcileResources reports the error, and the
// workload pass still runs.
func TestReconcile_BlockedStatefulSetWriteDoesNotDeletePods(t *testing.T) {
	v := newTestValkey("test", "default")

	rejectStsUpdates := interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object,
			opts ...client.UpdateOption) error {
			if sts, ok := obj.(*appsv1.StatefulSet); ok &&
				sts.Name == common.StatefulSetName(v, common.ComponentValkey) {
				return webhookUnreachableError()
			}
			return c.Update(ctx, obj, opts...)
		},
	}
	r, c := newInterceptedReconciler(rejectStsUpdates, v)
	ctx := context.Background()

	// First pass creates the StatefulSet (Create is not intercepted).
	_, err := r.Reconcile(ctx, ctrl.Request{
		NamespacedName: types.NamespacedName{Name: v.Name, Namespace: v.Namespace},
	})
	require.NoError(t, err)

	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "test", Namespace: "default"}, sts))
	pod0 := podFromStsTemplate(v, sts, 0)
	require.NoError(t, c.Create(ctx, pod0))

	// Bump the image; every StatefulSet Update from here on is rejected.
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "test", Namespace: "default"}, v))
	v.Spec.Image = "valkey/valkey:9.0"
	require.NoError(t, c.Update(ctx, v))

	// Several passes, as the 10 s requeue would produce.
	for i := 0; i < 3; i++ {
		_, err = r.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: v.Name, Namespace: v.Namespace},
		})
		require.Error(t, err, "the rejected StatefulSet write must surface as a reconcile error")
		assert.True(t, podExists(t, c, pod0.Name),
			"pass %d deleted the pod although the new template was never persisted", i+1)
	}
}
