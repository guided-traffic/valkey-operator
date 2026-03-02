package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// --- Unit Tests for Rolling Update Helper Functions ---

func TestDetectImageChange_NoChange(t *testing.T) {
	sts := &appsv1.StatefulSet{
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Image: "valkey/valkey:8.0"},
					},
				},
			},
		},
	}
	assert.False(t, detectImageChange("valkey/valkey:8.0", sts))
}

func TestDetectImageChange_WithChange(t *testing.T) {
	sts := &appsv1.StatefulSet{
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Image: "valkey/valkey:8.0"},
					},
				},
			},
		},
	}
	assert.True(t, detectImageChange("valkey/valkey:9.0", sts))
}

func TestDetectImageChange_EmptyContainers(t *testing.T) {
	sts := &appsv1.StatefulSet{
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{},
			},
		},
	}
	assert.False(t, detectImageChange("valkey/valkey:9.0", sts))
}

func TestPodNeedsUpdate_NoUpdate(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
			},
		},
	}
	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0", ""))
}

func TestPodNeedsUpdate_NeedsUpdate(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.ValkeyContainerName, Image: "valkey/valkey:8.0"},
			},
		},
	}
	assert.True(t, podNeedsUpdate(pod, "valkey/valkey:9.0", ""))
}

func TestPodNeedsUpdate_EmptyContainers(t *testing.T) {
	pod := &corev1.Pod{}
	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0", ""))
}

func TestPodNeedsUpdate_SidecarNeedsUpdate(t *testing.T) {
	const newSidecar = "ghcr.io/guided-traffic/valkey-operator:v2.0"
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
				{Name: builder.SidecarContainerName, Image: "ghcr.io/guided-traffic/valkey-operator:v1.0"},
			},
		},
	}
	// Valkey image matches, but sidecar image changed → needs update.
	assert.True(t, podNeedsUpdate(pod, "valkey/valkey:9.0", newSidecar))
}

func TestPodNeedsUpdate_SidecarUpToDate(t *testing.T) {
	const sidecar = "ghcr.io/guided-traffic/valkey-operator:v2.0"
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
				{Name: builder.SidecarContainerName, Image: sidecar},
			},
		},
	}
	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0", sidecar))
}

func TestPodNeedsUpdate_EmptySidecarImage_SkipsSidecarCheck(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
				{Name: builder.SidecarContainerName, Image: "ghcr.io/guided-traffic/valkey-operator:v1.0"},
			},
		},
	}
	// Empty desiredSidecarImage → sidecar check skipped.
	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0", ""))
}

// --- sidecarImageFromSts ---

func TestSidecarImageFromSts_Found(t *testing.T) {
	sts := &appsv1.StatefulSet{
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
						{Name: builder.SidecarContainerName, Image: "ghcr.io/guided-traffic/valkey-operator:v2.0"},
					},
				},
			},
		},
	}
	assert.Equal(t, "ghcr.io/guided-traffic/valkey-operator:v2.0", sidecarImageFromSts(sts))
}

func TestSidecarImageFromSts_NotFound(t *testing.T) {
	sts := &appsv1.StatefulSet{
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
					},
				},
			},
		},
	}
	assert.Equal(t, "", sidecarImageFromSts(sts))
}

func TestIsPodReady_Ready(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}
	assert.True(t, isPodReady(pod))
}

func TestIsPodReady_NotReady(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionFalse},
			},
		},
	}
	assert.False(t, isPodReady(pod))
}

func TestIsPodReady_NoConditions(t *testing.T) {
	pod := &corev1.Pod{}
	assert.False(t, isPodReady(pod))
}

// --- Rolling Update Result Tests ---

func TestRollingUpdateResult_Defaults(t *testing.T) {
	result := RollingUpdateResult{}
	assert.False(t, result.NeedsRequeue)
	assert.False(t, result.Completed)
	assert.Nil(t, result.Error)
	assert.Equal(t, time.Duration(0), result.RequeueAfter)
}

// --- Integration Tests with Fake Client ---

// createPodForSts creates a pod that looks like it belongs to the given StatefulSet.
func createPodForSts(v *vkov1.Valkey, ordinal int, image string, ready bool) *corev1.Pod {
	stsName := common.StatefulSetName(v, common.ComponentValkey)
	podName := fmt.Sprintf("%s-%d", stsName, ordinal)

	conditions := []corev1.PodCondition{}
	if ready {
		conditions = append(conditions, corev1.PodCondition{
			Type:   corev1.PodReady,
			Status: corev1.ConditionTrue,
		})
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: v.Namespace,
			Labels: map[string]string{
				common.LabelInstance:  v.Name,
				common.LabelComponent: common.ComponentValkey,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "valkey",
					Image: image,
				},
			},
		},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: conditions,
		},
	}
}

func TestCheckAndHandleRollingUpdate_NoUpdateNeeded(t *testing.T) {
	v := newTestValkey("test", "default")
	pod0 := createPodForSts(v, 0, "valkey/valkey:8.0", true)

	r, _ := newTestReconciler(v, pod0)
	reconcileOnce(t, r, "test", "default")

	result := r.checkAndHandleRollingUpdate(context.Background(), v)
	assert.False(t, result.NeedsRequeue)
	assert.False(t, result.Completed)
	assert.Nil(t, result.Error)
}

func TestCheckAndHandleRollingUpdate_StandaloneImageChange(t *testing.T) {
	// Scenario: Valkey spec has been updated to image 9.0, but the running pod still has 8.0.
	// We first reconcile with 8.0, then update the spec to 9.0 and verify the next reconcile
	// triggers a rolling update.
	v := newTestValkey("test", "default")
	pod0 := createPodForSts(v, 0, "valkey/valkey:8.0", true)

	r, c := newTestReconciler(v, pod0)
	reconcileOnce(t, r, "test", "default")

	// Now update the spec image to 9.0.
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, v))
	v.Spec.Image = "valkey/valkey:9.0"
	require.NoError(t, c.Update(context.Background(), v))

	// The next reconcile should detect the image change and trigger rolling update.
	result := reconcileOnce(t, r, "test", "default")

	// Should requeue because of the rolling update.
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)

	// Pod should have been deleted.
	pod := &corev1.Pod{}
	err := c.Get(context.Background(), types.NamespacedName{Name: "test-0", Namespace: "default"}, pod)
	assert.Error(t, err, "Pod should have been deleted for rolling update")
}

func TestCheckAndHandleRollingUpdate_StatefulSetNotFound(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	// Don't reconcile — StatefulSet doesn't exist yet.
	result := r.checkAndHandleRollingUpdate(context.Background(), v)
	assert.False(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)
}

func TestHandleStandaloneRollingUpdate_AllUpdated(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Image = "valkey/valkey:9.0"
	})
	pod0 := createPodForSts(v, 0, "valkey/valkey:9.0", true)

	r, _ := newTestReconciler(v, pod0)
	reconcileOnce(t, r, "test", "default")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, sts))

	result := r.handleStandaloneRollingUpdate(context.Background(), v, sts)
	assert.True(t, result.Completed)
	assert.False(t, result.NeedsRequeue)
}

func TestHandleStandaloneRollingUpdate_PodNotReady(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Image = "valkey/valkey:9.0"
	})
	// Pod has old image but is not ready — should wait.
	pod0 := createPodForSts(v, 0, "valkey/valkey:8.0", false)

	r, _ := newTestReconciler(v, pod0)
	reconcileOnce(t, r, "test", "default")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, sts))

	result := r.handleStandaloneRollingUpdate(context.Background(), v, sts)
	assert.True(t, result.NeedsRequeue, "Should requeue waiting for pod to become ready")
	assert.False(t, result.Completed)
}

func TestHandleStandaloneRollingUpdate_DeletesPodWithOldImage(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Image = "valkey/valkey:9.0"
	})
	pod0 := createPodForSts(v, 0, "valkey/valkey:8.0", true)

	r, c := newTestReconciler(v, pod0)
	reconcileOnce(t, r, "test", "default")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, sts))

	result := r.handleStandaloneRollingUpdate(context.Background(), v, sts)
	assert.True(t, result.NeedsRequeue)

	// Pod should have been deleted.
	pod := &corev1.Pod{}
	err := c.Get(context.Background(), types.NamespacedName{Name: "test-0", Namespace: "default"}, pod)
	assert.Error(t, err, "Pod should have been deleted")
}

func TestHandleRollingUpdate_HA_AllPodsAlreadyUpdated(t *testing.T) {
	v := newTestValkey("ha-test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:9.0"
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	// All pods have new image and are ready.
	pod0 := createPodForSts(v, 0, "valkey/valkey:9.0", true)
	pod1 := createPodForSts(v, 1, "valkey/valkey:9.0", true)
	pod2 := createPodForSts(v, 2, "valkey/valkey:9.0", true)

	r, _ := newTestReconciler(v, pod0, pod1, pod2)
	reconcileOnce(t, r, "ha-test", "default")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ha-test", Namespace: "default"}, sts))

	result := r.handleRollingUpdate(context.Background(), v, sts)
	assert.True(t, result.Completed)
	assert.False(t, result.NeedsRequeue)
}

func TestHandleRollingUpdate_HA_DeletesReplicaFirst(t *testing.T) {
	v := newTestValkey("ha-test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:9.0"
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	// All pods have old image. Master role detection will fail (no actual Valkey),
	// so the controller will treat them as non-master (replicas).
	pod0 := createPodForSts(v, 0, "valkey/valkey:8.0", true)
	pod1 := createPodForSts(v, 1, "valkey/valkey:8.0", true)
	pod2 := createPodForSts(v, 2, "valkey/valkey:8.0", true)

	r, c := newTestReconciler(v, pod0, pod1, pod2)
	reconcileOnce(t, r, "ha-test", "default")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ha-test", Namespace: "default"}, sts))

	result := r.handleRollingUpdate(context.Background(), v, sts)
	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)

	// At least one pod should have been deleted.
	deletedCount := 0
	for i := 0; i < 3; i++ {
		pod := &corev1.Pod{}
		podName := fmt.Sprintf("ha-test-%d", i)
		err := c.Get(context.Background(), types.NamespacedName{Name: podName, Namespace: "default"}, pod)
		if err != nil {
			deletedCount++
		}
	}
	assert.Equal(t, 1, deletedCount, "Exactly one pod should be deleted per iteration")
}

func TestHandleRollingUpdate_HA_WaitsForNotReadyPod(t *testing.T) {
	v := newTestValkey("ha-test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:9.0"
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	// Pod-0 has new image but not ready yet (was just replaced).
	pod0 := createPodForSts(v, 0, "valkey/valkey:9.0", false)
	pod1 := createPodForSts(v, 1, "valkey/valkey:8.0", true)
	pod2 := createPodForSts(v, 2, "valkey/valkey:8.0", true)

	r, _ := newTestReconciler(v, pod0, pod1, pod2)
	reconcileOnce(t, r, "ha-test", "default")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ha-test", Namespace: "default"}, sts))

	result := r.handleRollingUpdate(context.Background(), v, sts)

	// Should wait for the not-ready pod with new image, then proceed to delete next old-image pod.
	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)
}

func TestHandleRollingUpdate_HA_PartiallyUpdated(t *testing.T) {
	v := newTestValkey("ha-test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:9.0"
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	// Pod-0 updated and ready, pod-1 and pod-2 still old.
	pod0 := createPodForSts(v, 0, "valkey/valkey:9.0", true)
	pod1 := createPodForSts(v, 1, "valkey/valkey:8.0", true)
	pod2 := createPodForSts(v, 2, "valkey/valkey:8.0", true)

	r, c := newTestReconciler(v, pod0, pod1, pod2)
	reconcileOnce(t, r, "ha-test", "default")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ha-test", Namespace: "default"}, sts))

	result := r.handleRollingUpdate(context.Background(), v, sts)
	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)

	// One of the old-image pods should have been deleted.
	deletedOld := 0
	for i := 1; i < 3; i++ {
		pod := &corev1.Pod{}
		podName := fmt.Sprintf("ha-test-%d", i)
		err := c.Get(context.Background(), types.NamespacedName{Name: podName, Namespace: "default"}, pod)
		if err != nil {
			deletedOld++
		}
	}
	assert.Equal(t, 1, deletedOld, "Should delete exactly one old-image replica per step")
}

func TestRollingUpdatePhaseString(t *testing.T) {
	tests := []struct {
		updated int
		total   int
		want    string
	}{
		{0, 3, "Rolling Update 0/3"},
		{1, 3, "Rolling Update 1/3"},
		{2, 3, "Rolling Update 2/3"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d/%d", tt.updated, tt.total), func(t *testing.T) {
			phase := fmt.Sprintf("%s %d/%d", vkov1.ValkeyPhaseRollingUpdate, tt.updated, tt.total)
			assert.Equal(t, tt.want, phase)
		})
	}
}

// --- Reconciler-Level Rolling Update Integration Tests ---

func TestReconcile_RollingUpdate_StandaloneNoRequeueWhenNoChange(t *testing.T) {
	v := newTestValkey("test", "default")
	pod0 := createPodForSts(v, 0, "valkey/valkey:8.0", true)

	r, _ := newTestReconciler(v, pod0)
	result := reconcileOnce(t, r, "test", "default")

	// No requeue needed — no image change.
	assert.Equal(t, time.Duration(0), result.RequeueAfter)
}

func TestReconcile_RollingUpdate_StandaloneRequeuesOnImageChange(t *testing.T) {
	v := newTestValkey("test", "default")
	pod0 := createPodForSts(v, 0, "valkey/valkey:8.0", true)

	r, c := newTestReconciler(v, pod0)
	reconcileOnce(t, r, "test", "default")

	// Update spec to new image.
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, v))
	v.Spec.Image = "valkey/valkey:9.0"
	require.NoError(t, c.Update(context.Background(), v))

	result := reconcileOnce(t, r, "test", "default")

	// Should requeue for rolling update.
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
}

func TestReconcile_RollingUpdate_HARequeuesOnImageChange(t *testing.T) {
	v := newTestValkey("ha-test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})
	pod0 := createPodForSts(v, 0, "valkey/valkey:8.0", true)
	pod1 := createPodForSts(v, 1, "valkey/valkey:8.0", true)
	pod2 := createPodForSts(v, 2, "valkey/valkey:8.0", true)

	r, c := newTestReconciler(v, pod0, pod1, pod2)
	reconcileOnce(t, r, "ha-test", "default")

	// Update spec to new image.
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "ha-test", Namespace: "default"}, v))
	v.Spec.Image = "valkey/valkey:9.0"
	require.NoError(t, c.Update(context.Background(), v))

	result := reconcileOnce(t, r, "ha-test", "default")

	// Should requeue for rolling update.
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
}

func TestReconcile_RollingUpdate_StatefulSetTemplateIsUpdated(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	// Change image in spec.
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, v))
	v.Spec.Image = "valkey/valkey:9.0"
	require.NoError(t, c.Update(context.Background(), v))

	reconcileOnce(t, r, "test", "default")

	// StatefulSet template should have been updated to the new image.
	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, sts))
	assert.Equal(t, "valkey/valkey:9.0", sts.Spec.Template.Spec.Containers[0].Image)
}

func TestReconcile_RollingUpdate_OnDeleteStrategy(t *testing.T) {
	v := newTestValkey("test", "default")
	r, c := newTestReconciler(v)

	reconcileOnce(t, r, "test", "default")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, sts))

	// Verify the StatefulSet uses OnDelete update strategy.
	assert.Equal(t, appsv1.OnDeleteStatefulSetStrategyType, sts.Spec.UpdateStrategy.Type,
		"StatefulSet should use OnDelete strategy so the operator controls pod replacement")
}

// --- Failover Timestamp and State Tests ---

func TestSetFailoverTimestamp(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	err := r.setFailoverTimestamp(context.Background(), v)
	require.NoError(t, err)

	assert.NotEmpty(t, v.Annotations[annotationFailoverTimestamp])

	// Verify the timestamp is valid RFC3339.
	ts, err := time.Parse(time.RFC3339, v.Annotations[annotationFailoverTimestamp])
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().UTC(), ts, 5*time.Second)
}

func TestIsFailoverTimedOut_NotSet(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	assert.False(t, r.isFailoverTimedOut(v), "Should not be timed out when no timestamp is set")
}

func TestIsFailoverTimedOut_RecentTimestamp(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	v.Annotations = map[string]string{
		annotationFailoverTimestamp: time.Now().UTC().Format(time.RFC3339),
	}

	assert.False(t, r.isFailoverTimedOut(v), "Should not be timed out for a recent timestamp")
}

func TestIsFailoverTimedOut_OldTimestamp(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	oldTime := time.Now().UTC().Add(-failoverRetryTimeout - time.Minute)
	v.Annotations = map[string]string{
		annotationFailoverTimestamp: oldTime.Format(time.RFC3339),
	}

	assert.True(t, r.isFailoverTimedOut(v), "Should be timed out for an old timestamp")
}

func TestIsFailoverTimedOut_CorruptedTimestamp(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	v.Annotations = map[string]string{
		annotationFailoverTimestamp: "invalid-timestamp",
	}

	assert.True(t, r.isFailoverTimedOut(v), "Should treat corrupted timestamp as timed out")
}

func TestClearRollingUpdateState_CleansUpFailoverTimestamp(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	v.Annotations = map[string]string{
		annotationRollingUpdateState: stateFailoverTriggered,
		annotationFailoverTimestamp:  time.Now().UTC().Format(time.RFC3339),
	}
	require.NoError(t, r.Update(context.Background(), v))

	err := r.clearRollingUpdateState(context.Background(), v)
	require.NoError(t, err)

	assert.Empty(t, v.Annotations[annotationRollingUpdateState])
	assert.Empty(t, v.Annotations[annotationFailoverTimestamp])
}

func TestClearRollingUpdateState_NothingToClean(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	err := r.clearRollingUpdateState(context.Background(), v)
	require.NoError(t, err)
}

// --- waitForWriteSync ---

func TestWaitWriteSyncClientOverhead_LargerThanWAITTimeout(t *testing.T) {
	// The client timeout must be strictly larger than the WAIT timeout
	// to avoid a race between client deadline and server-side WAIT blocking.
	waitDuration := time.Duration(waitWriteSyncTimeout) * time.Millisecond
	clientTimeout := waitDuration + waitWriteSyncClientOverhead
	assert.Greater(t, clientTimeout, waitDuration,
		"client timeout must exceed server-side WAIT timeout to prevent i/o timeout race")
}

func TestWaitForWriteSync_NoReplicas_ReturnsNil(t *testing.T) {
	v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:9.0"
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	r, _ := newTestReconciler(v)

	// Master is pod 2, replicas are not ready or still need update → numReplicas == 0.
	pods := []podState{
		{name: "ha-0", needsUpdate: true, ready: false},
		{name: "ha-1", needsUpdate: true, ready: false},
		{name: "ha-2", needsUpdate: true, ready: true, isMaster: true},
	}
	masterIdx := 2

	result := r.waitForWriteSync(context.Background(), v, pods, masterIdx)
	assert.Nil(t, result, "Should return nil when no replicas need to acknowledge writes")
}

func TestWaitForWriteSync_ConnectionFails_Requeues(t *testing.T) {
	v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:9.0"
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	r, _ := newTestReconciler(v)

	// Two replicas are ready and updated, master is pod 2.
	pods := []podState{
		{name: "ha-0", needsUpdate: false, ready: true},
		{name: "ha-1", needsUpdate: false, ready: true},
		{name: "ha-2", needsUpdate: true, ready: true, isMaster: true},
	}
	masterIdx := 2

	result := r.waitForWriteSync(context.Background(), v, pods, masterIdx)
	// Connection will fail (no real Valkey server), so it should requeue.
	assert.NotNil(t, result)
	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)
}

func TestHandlePostFailover_RequeuesWhenNoNewMaster(t *testing.T) {
	v := newTestValkey("ha-post", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:9.0"
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	// Set recent failover timestamp — should not retry yet.
	v.Annotations = map[string]string{
		annotationRollingUpdateState: stateFailoverTriggered,
		annotationFailoverTimestamp:  time.Now().UTC().Format(time.RFC3339),
	}

	// Pod-0 and Pod-1 have the new image, Pod-2 still has the old one (the master being replaced).
	// No actual Valkey running, so no master is detected by GetReplicationInfo.
	pod0 := createPodForSts(v, 0, "valkey/valkey:9.0", true)
	pod1 := createPodForSts(v, 1, "valkey/valkey:9.0", true)
	pod2 := createPodForSts(v, 2, "valkey/valkey:8.0", true)

	// Create a StatefulSet manually to avoid going through reconcileOnce.
	replicas := int32(3)
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ha-post",
			Namespace: "default",
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "ha-post"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "ha-post"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "valkey", Image: "valkey/valkey:9.0"},
					},
				},
			},
		},
	}

	r, _ := newTestReconciler(v, pod0, pod1, pod2, sts)

	pods, masterIdx, err := r.collectPodStates(context.Background(), v, sts)
	require.NoError(t, err)

	// handlePostFailover should requeue because no pod reports role=master
	// (no actual Valkey is running in unit tests).
	result := r.handlePostFailover(context.Background(), v, pods, masterIdx)
	assert.True(t, result.NeedsRequeue, "Should requeue when no new master is found")
	assert.Nil(t, result.Error)
}

func TestHandleRollingUpdate_HA_ClearsStaleStateOnNewRollingUpdate(t *testing.T) {
	// Simulate a scenario where a first rolling update completed (8.0→8.1)
	// and left stale state annotations, then a second update (8.1→8.0)
	// starts immediately. All pods have the old desired image (8.1) and
	// the state is failover-triggered from the first update.
	v := newTestValkey("ha-test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:8.0" // Desired image for the second rolling update.
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	// Stale annotations from the first rolling update.
	v.Annotations = map[string]string{
		annotationRollingUpdateState: stateFailoverTriggered,
		annotationFailoverTimestamp:  time.Now().UTC().Add(-time.Minute).Format(time.RFC3339),
	}

	// All pods run 8.1 (old image from first rolling update), all need update to 8.0.
	pod0 := createPodForSts(v, 0, "valkey/valkey:8.1", true)
	pod1 := createPodForSts(v, 1, "valkey/valkey:8.1", true)
	pod2 := createPodForSts(v, 2, "valkey/valkey:8.1", true)

	r, c := newTestReconciler(v, pod0, pod1, pod2)
	reconcileOnce(t, r, "ha-test", "default")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ha-test", Namespace: "default"}, sts))

	result := r.handleRollingUpdate(context.Background(), v, sts)

	// Should NOT be stuck in handlePostFailover. Instead, the stale state should
	// be cleared and a replica should be deleted (starting the new rolling update fresh).
	assert.True(t, result.NeedsRequeue, "Should requeue to continue rolling update")
	assert.Nil(t, result.Error)

	// Verify stale state was cleared.
	updatedV := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "ha-test", Namespace: "default"}, updatedV))
	_, hasState := updatedV.Annotations[annotationRollingUpdateState]
	// State should either be cleared or set to replacing-replicas (new update started).
	if hasState {
		assert.Equal(t, stateReplacingReplicas, updatedV.Annotations[annotationRollingUpdateState],
			"State should be replacing-replicas for the new rolling update, not stale failover state")
	}

	// At least one pod should have been deleted (replica replacement started).
	deletedCount := 0
	for i := 0; i < 3; i++ {
		pod := &corev1.Pod{}
		podName := fmt.Sprintf("ha-test-%d", i)
		err := c.Get(context.Background(), types.NamespacedName{Name: podName, Namespace: "default"}, pod)
		if err != nil {
			deletedCount++
		}
	}
	assert.Equal(t, 1, deletedCount, "Should delete one replica pod after clearing stale state")
}

func TestHandleRollingUpdate_HA_DoesNotClearValidState(t *testing.T) {
	// When some pods are already updated (updatedCount > 0) and state is
	// failover-triggered, it's a valid ongoing update — do NOT clear.
	v := newTestValkey("ha-test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:9.0"
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	// State annotation from the current rolling update.
	v.Annotations = map[string]string{
		annotationRollingUpdateState: stateFailoverTriggered,
		annotationFailoverTimestamp:  time.Now().UTC().Format(time.RFC3339),
	}

	// Pod 1 and 2 already updated to 9.0 (updated + ready), pod 0 still on 8.0 (master).
	pod0 := createPodForSts(v, 0, "valkey/valkey:8.0", true)
	pod1 := createPodForSts(v, 1, "valkey/valkey:9.0", true)
	pod2 := createPodForSts(v, 2, "valkey/valkey:9.0", true)

	r, c := newTestReconciler(v, pod0, pod1, pod2)
	reconcileOnce(t, r, "ha-test", "default")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ha-test", Namespace: "default"}, sts))

	result := r.handleRollingUpdate(context.Background(), v, sts)

	// Should enter handlePostFailover (state is valid).
	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)

	// Verify state was NOT cleared (it's a valid failover-triggered state).
	updatedV := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "ha-test", Namespace: "default"}, updatedV))
	assert.Contains(t, updatedV.Annotations, annotationRollingUpdateState,
		"Valid rolling update state should not be cleared")
}

func TestHasMinWaitElapsed_NoTimestamp(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	assert.True(t, r.hasMinWaitElapsed(v),
		"Should return true when no timestamp is set")
}

func TestCheckAndHandleRollingUpdate_FinalizesStuckState(t *testing.T) {
	// Scenario: A rolling update completed (all pods have the desired image)
	// but finalizeRollingUpdate was never called, leaving state annotations behind.
	// checkAndHandleRollingUpdate must detect this and still enter the handler
	// instead of returning early with an empty result.
	//
	// We use a standalone Valkey (no sentinel) to avoid the master topology check
	// in finalizeRollingUpdate which requires a live cluster. The standalone handler
	// will see all pods updated and return Completed: true.
	v := newTestValkey("standalone-test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 1
		v.Spec.Image = "valkey/valkey:8.1"
		// No sentinel — standalone mode.
	})

	// Stale state from a completed rolling update.
	v.Annotations = map[string]string{
		annotationRollingUpdateState: stateReplacingReplicas,
	}

	// The single pod already has the desired image (8.1) and is ready.
	pod0 := createPodForSts(v, 0, "valkey/valkey:8.1", true)

	r, _ := newTestReconciler(v, pod0)
	reconcileOnce(t, r, "standalone-test", "default")

	result := r.checkAndHandleRollingUpdate(context.Background(), v)

	// The handler should have been called and returned Completed.
	// Without the fix, checkAndHandleRollingUpdate would return an empty result
	// because needsRollingUpdate is false, leaving the state stuck.
	assert.True(t, result.Completed, "Rolling update should be marked as completed")
	assert.Nil(t, result.Error)
}

func TestCheckAndHandleRollingUpdate_NoStateNoRollingUpdate(t *testing.T) {
	// When no pods need updating and no state annotation exists,
	// checkAndHandleRollingUpdate should return an empty result (no action needed).
	v := newTestValkey("standalone-test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 1
		v.Spec.Image = "valkey/valkey:8.1"
	})

	pod0 := createPodForSts(v, 0, "valkey/valkey:8.1", true)

	r, _ := newTestReconciler(v, pod0)
	reconcileOnce(t, r, "standalone-test", "default")

	result := r.checkAndHandleRollingUpdate(context.Background(), v)

	assert.False(t, result.NeedsRequeue, "Should not need requeue")
	assert.False(t, result.Completed, "Should not be completed (no rolling update)")
	assert.Nil(t, result.Error)
}

func TestHasMinWaitElapsed_RecentTimestamp(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	v.Annotations = map[string]string{
		annotationFailoverTimestamp: time.Now().UTC().Format(time.RFC3339),
	}

	assert.False(t, r.hasMinWaitElapsed(v),
		"Should return false for a recent timestamp")
}

func TestHasMinWaitElapsed_OldTimestamp(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	oldTime := time.Now().UTC().Add(-failoverResetMinWait - time.Minute)
	v.Annotations = map[string]string{
		annotationFailoverTimestamp: oldTime.Format(time.RFC3339),
	}

	assert.True(t, r.hasMinWaitElapsed(v),
		"Should return true for an old timestamp")
}

func TestHasMinWaitElapsed_CorruptedTimestamp(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	v.Annotations = map[string]string{
		annotationFailoverTimestamp: "invalid-timestamp",
	}

	assert.True(t, r.hasMinWaitElapsed(v),
		"Should return true for corrupted timestamp to allow progress")
}

// --- isSentinelAwareOfReplicas Tests ---

func TestIsSentinelAwareOfReplicas_AllUnreachable(t *testing.T) {
	// When no real sentinel exists (unit test), all connections fail.
	// Should return true to proceed optimistically.
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})
	r, _ := newTestReconciler(v)

	assert.True(t, r.isSentinelAwareOfReplicas(context.Background(), v, 2),
		"Should return true when all sentinels are unreachable (optimistic)")
}

func TestIsSentinelAwareOfReplicas_DefaultSentinelReplicas(t *testing.T) {
	// When sentinel spec is nil, should use default 3 replicas.
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})
	r, _ := newTestReconciler(v)

	assert.True(t, r.isSentinelAwareOfReplicas(context.Background(), v, 2),
		"Should return true with default sentinel replicas and all unreachable")
}

// --- getReconnectResetCount / incrementReconnectResetCount / clearReconnectResetCount ---

func TestGetReconnectResetCount_NoAnnotation(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)
	assert.Equal(t, 0, r.getReconnectResetCount(v))
}

func TestGetReconnectResetCount_NilAnnotations(t *testing.T) {
	v := newTestValkey("test", "default")
	v.Annotations = nil
	r, _ := newTestReconciler(v)
	assert.Equal(t, 0, r.getReconnectResetCount(v))
}

func TestGetReconnectResetCount_ValidCount(t *testing.T) {
	v := newTestValkey("test", "default")
	v.Annotations = map[string]string{
		annotationReconnectResetCount: "2",
	}
	r, _ := newTestReconciler(v)
	assert.Equal(t, 2, r.getReconnectResetCount(v))
}

func TestGetReconnectResetCount_CorruptedAnnotation(t *testing.T) {
	v := newTestValkey("test", "default")
	v.Annotations = map[string]string{
		annotationReconnectResetCount: "not-a-number",
	}
	r, _ := newTestReconciler(v)
	assert.Equal(t, 0, r.getReconnectResetCount(v))
}

func TestIncrementReconnectResetCount_StoresBothAnnotations(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	require.NoError(t, r.Update(context.Background(), v))

	err := r.incrementReconnectResetCount(context.Background(), v, 1)
	require.NoError(t, err)

	assert.Equal(t, "1", v.Annotations[annotationReconnectResetCount])
	assert.NotEmpty(t, v.Annotations[annotationFailoverTimestamp],
		"incrementReconnectResetCount should also set the failover timestamp")
}

func TestClearReconnectResetCount_RemovesAnnotation(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	v.Annotations = map[string]string{
		annotationReconnectResetCount: "3",
	}
	require.NoError(t, r.Update(context.Background(), v))

	err := r.clearReconnectResetCount(context.Background(), v)
	require.NoError(t, err)
	assert.Empty(t, v.Annotations[annotationReconnectResetCount])
}

func TestClearReconnectResetCount_NoopWhenAbsent(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	// Should not error when annotation is already absent.
	err := r.clearReconnectResetCount(context.Background(), v)
	assert.NoError(t, err)
}

func TestClearRollingUpdateState_AlsoClearsResetCount(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	v.Annotations = map[string]string{
		annotationRollingUpdateState:  stateFailoverTriggered,
		annotationFailoverTimestamp:   time.Now().UTC().Format(time.RFC3339),
		annotationReconnectResetCount: "2",
	}
	require.NoError(t, r.Update(context.Background(), v))

	err := r.clearRollingUpdateState(context.Background(), v)
	require.NoError(t, err)

	assert.Empty(t, v.Annotations[annotationRollingUpdateState])
	assert.Empty(t, v.Annotations[annotationFailoverTimestamp])
	assert.Empty(t, v.Annotations[annotationReconnectResetCount])
}

func TestClearRollingUpdateState_AlsoClearsFinalizationTimestamp(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	v.Annotations = map[string]string{
		annotationRollingUpdateState:    stateReplacingMaster,
		annotationFailoverTimestamp:     time.Now().UTC().Format(time.RFC3339),
		annotationFinalizationTimestamp: time.Now().UTC().Format(time.RFC3339),
	}
	require.NoError(t, r.Update(context.Background(), v))

	err := r.clearRollingUpdateState(context.Background(), v)
	require.NoError(t, err)

	assert.Empty(t, v.Annotations[annotationRollingUpdateState])
	assert.Empty(t, v.Annotations[annotationFailoverTimestamp])
	assert.Empty(t, v.Annotations[annotationFinalizationTimestamp])
}

func TestIsFinalizationStalled_FalseWhenAnnotationAbsent(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	assert.False(t, r.isFinalizationStalled(v))
}

func TestIsFinalizationStalled_FalseWhenRecent(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	v.Annotations = map[string]string{
		annotationFinalizationTimestamp: time.Now().UTC().Format(time.RFC3339),
	}

	assert.False(t, r.isFinalizationStalled(v))
}

func TestIsFinalizationStalled_TrueWhenOld(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	old := time.Now().Add(-(finalizationStallTimeout + 10*time.Second))
	v.Annotations = map[string]string{
		annotationFinalizationTimestamp: old.UTC().Format(time.RFC3339),
	}

	assert.True(t, r.isFinalizationStalled(v))
}

func TestIsFinalizationStalled_TrueWhenCorrupted(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	v.Annotations = map[string]string{
		annotationFinalizationTimestamp: "not-a-timestamp",
	}

	assert.True(t, r.isFinalizationStalled(v))
}

func TestEnsureFinalizationTimestamp_SetsOnce(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	// First call must set the annotation.
	r.ensureFinalizationTimestamp(context.Background(), v)
	first := v.Annotations[annotationFinalizationTimestamp]
	assert.NotEmpty(t, first)

	// Second call must not overwrite.
	r.ensureFinalizationTimestamp(context.Background(), v)
	second := v.Annotations[annotationFinalizationTimestamp]
	assert.Equal(t, first, second, "timestamp must not be overwritten on second call")
}

// --- sentinel awareness stall detection ---

func TestIsSentinelAwarenessStalled_FalseWhenAnnotationAbsent(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	assert.False(t, r.isSentinelAwarenessStalled(v))
}

func TestIsSentinelAwarenessStalled_FalseWhenRecent(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	v.Annotations = map[string]string{
		annotationSentinelAwarenessStarted: time.Now().UTC().Format(time.RFC3339),
	}

	assert.False(t, r.isSentinelAwarenessStalled(v))
}

func TestIsSentinelAwarenessStalled_TrueWhenOld(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	old := time.Now().Add(-(sentinelAwarenessTimeout + 10*time.Second))
	v.Annotations = map[string]string{
		annotationSentinelAwarenessStarted: old.UTC().Format(time.RFC3339),
	}

	assert.True(t, r.isSentinelAwarenessStalled(v))
}

func TestIsSentinelAwarenessStalled_TrueWhenCorrupted(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	v.Annotations = map[string]string{
		annotationSentinelAwarenessStarted: "not-a-valid-timestamp",
	}

	assert.True(t, r.isSentinelAwarenessStalled(v))
}

func TestEnsureSentinelAwarenessTimestamp_SetsOnce(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	// First call must set the annotation.
	r.ensureSentinelAwarenessTimestamp(context.Background(), v)
	first := v.Annotations[annotationSentinelAwarenessStarted]
	assert.NotEmpty(t, first)

	// Second call must not overwrite.
	r.ensureSentinelAwarenessTimestamp(context.Background(), v)
	second := v.Annotations[annotationSentinelAwarenessStarted]
	assert.Equal(t, first, second, "timestamp must not be overwritten on second call")
}

func TestClearSentinelAwarenessTimestamp_RemovesAnnotation(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	v.Annotations = map[string]string{
		annotationSentinelAwarenessStarted: time.Now().UTC().Format(time.RFC3339),
		annotationRollingUpdateState:       stateFailoverTriggered,
	}

	r.clearSentinelAwarenessTimestamp(v)

	assert.Empty(t, v.Annotations[annotationSentinelAwarenessStarted])
	// Other annotations must remain.
	assert.Equal(t, stateFailoverTriggered, v.Annotations[annotationRollingUpdateState])
}

func TestClearRollingUpdateState_AlsoClearsSentinelAwarenessTimestamp(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	v.Annotations = map[string]string{
		annotationRollingUpdateState:       stateFailoverTriggered,
		annotationFailoverTimestamp:        time.Now().UTC().Format(time.RFC3339),
		annotationSentinelAwarenessStarted: time.Now().UTC().Format(time.RFC3339),
	}
	require.NoError(t, r.Update(context.Background(), v))

	err := r.clearRollingUpdateState(context.Background(), v)
	require.NoError(t, err)

	assert.Empty(t, v.Annotations[annotationRollingUpdateState])
	assert.Empty(t, v.Annotations[annotationFailoverTimestamp])
	assert.Empty(t, v.Annotations[annotationSentinelAwarenessStarted])
}

// --- handleMasterWithNoReplicas ---

func TestHandleMasterWithNoReplicas_WaitsWhenNotTimedOut(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	r, _ := newTestReconciler(v)

	// Set a fresh timestamp so the timeout has NOT elapsed yet.
	v.Annotations = map[string]string{
		annotationFailoverTimestamp: time.Now().UTC().Format(time.RFC3339),
	}

	ps := podState{name: "test-1"}
	result := r.handleMasterWithNoReplicas(context.Background(), v, ps, nil)

	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
}

// ============================================================
// Phase 2: Graceful Sidecar Rolling Update — New Tests
// ============================================================

// --- isSidecarOnlyChange ---

func TestIsSidecarOnlyChange_OnlySidecarChanged(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
		{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
		{Name: builder.SidecarContainerName, Image: "operator:v1.0"},
	}}}
	assert.True(t, isSidecarOnlyChange(pod, "valkey/valkey:9.0", "operator:v2.0"))
}

func TestIsSidecarOnlyChange_BothImagesChanged(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
		{Name: builder.ValkeyContainerName, Image: "valkey/valkey:8.0"},
		{Name: builder.SidecarContainerName, Image: "operator:v1.0"},
	}}}
	// Valkey image also changed → not sidecar-only.
	assert.False(t, isSidecarOnlyChange(pod, "valkey/valkey:9.0", "operator:v2.0"))
}

func TestIsSidecarOnlyChange_OnlyValkeyChanged(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
		{Name: builder.ValkeyContainerName, Image: "valkey/valkey:8.0"},
		{Name: builder.SidecarContainerName, Image: "operator:v2.0"},
	}}}
	// Only valkey changed, sidecar is already up to date → not sidecar-only.
	assert.False(t, isSidecarOnlyChange(pod, "valkey/valkey:9.0", "operator:v2.0"))
}

func TestIsSidecarOnlyChange_EmptySidecarImage(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
		{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
		{Name: builder.SidecarContainerName, Image: "operator:v1.0"},
	}}}
	// Empty desiredSidecarImage → sidecar check is skipped entirely.
	assert.False(t, isSidecarOnlyChange(pod, "valkey/valkey:9.0", ""))
}

func TestIsSidecarOnlyChange_NoSidecarContainer(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
		{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
	}}}
	// No sidecar container → cannot be a sidecar-only change.
	assert.False(t, isSidecarOnlyChange(pod, "valkey/valkey:9.0", "operator:v2.0"))
}

func TestIsSidecarOnlyChange_EmptyPod(t *testing.T) {
	pod := &corev1.Pod{}
	assert.False(t, isSidecarOnlyChange(pod, "valkey/valkey:9.0", "operator:v2.0"))
}

func TestIsSidecarOnlyChange_NothingChanged(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
		{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
		{Name: builder.SidecarContainerName, Image: "operator:v2.0"},
	}}}
	// Both images are already at desired versions.
	assert.False(t, isSidecarOnlyChange(pod, "valkey/valkey:9.0", "operator:v2.0"))
}

// --- handleStandaloneRollingUpdate — sidecar-only deferred update ---

func TestHandleStandaloneRollingUpdate_SidecarOnlyChange_DeferredNoPodDelete(t *testing.T) {
	// When only the sidecar image changed, the pod must NOT be deleted.
	// The update is deferred to the next natural pod restart.
	const newSidecar = "operator:v2.0"
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Image = "valkey/valkey:9.0"
	})
	// Pod runs the correct valkey image but an outdated sidecar.
	pod0 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-0", Namespace: "default"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
			{Name: builder.SidecarContainerName, Image: "operator:v1.0"},
		}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}

	r, c := newTestReconciler(v, pod0)
	reconcileOnce(t, r, "test", "default")

	// Retrieve the created StatefulSet and patch its sidecar image to simulate an
	// operator upgrade that bumped the desired sidecar to v2.0.
	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, sts))
	for i, cont := range sts.Spec.Template.Spec.Containers {
		if cont.Name == builder.SidecarContainerName {
			sts.Spec.Template.Spec.Containers[i].Image = newSidecar
		}
	}

	result := r.handleStandaloneRollingUpdate(context.Background(), v, sts)

	// Deferred: no requeue, not completed (pending).
	assert.False(t, result.NeedsRequeue, "Sidecar-only change must not trigger requeue")
	assert.False(t, result.Completed, "Not completed while sidecar update is pending")
	assert.Nil(t, result.Error)

	// Pod must NOT have been deleted.
	existingPod := &corev1.Pod{}
	err := c.Get(context.Background(), types.NamespacedName{Name: "test-0", Namespace: "default"}, existingPod)
	assert.NoError(t, err, "Pod must NOT be deleted for a sidecar-only change in standalone mode")
}

func TestHandleStandaloneRollingUpdate_ValkeyImageChange_StillDeletesPod(t *testing.T) {
	// When the valkey image changes, the pod must still be deleted (existing behaviour).
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Image = "valkey/valkey:9.0"
	})
	pod0 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-0", Namespace: "default"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: builder.ValkeyContainerName, Image: "valkey/valkey:8.0"}, // outdated
			{Name: builder.SidecarContainerName, Image: "operator:v2.0"},
		}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}

	r, c := newTestReconciler(v, pod0)
	reconcileOnce(t, r, "test", "default")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, sts))

	result := r.handleStandaloneRollingUpdate(context.Background(), v, sts)

	assert.True(t, result.NeedsRequeue, "Valkey image change must trigger requeue")
	assert.Nil(t, result.Error)

	// Pod should have been deleted.
	pod := &corev1.Pod{}
	err := c.Get(context.Background(), types.NamespacedName{Name: "test-0", Namespace: "default"}, pod)
	assert.Error(t, err, "Pod should be deleted when valkey image changes")
}

// --- sentinelPodNeedsUpdate ---

func TestSentinelPodNeedsUpdate_Outdated(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
		{Name: builder.SentinelContainerName, Image: "valkey/valkey:8.0"},
	}}}
	template := corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{
		{Name: builder.SentinelContainerName, Image: "valkey/valkey:9.0"},
	}}}
	assert.True(t, sentinelPodNeedsUpdate(pod, template))
}

func TestSentinelPodNeedsUpdate_UpToDate(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
		{Name: builder.SentinelContainerName, Image: "valkey/valkey:9.0"},
	}}}
	template := corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{
		{Name: builder.SentinelContainerName, Image: "valkey/valkey:9.0"},
	}}}
	assert.False(t, sentinelPodNeedsUpdate(pod, template))
}

func TestSentinelPodNeedsUpdate_EmptyPod(t *testing.T) {
	pod := &corev1.Pod{}
	template := corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{
		{Name: builder.SentinelContainerName, Image: "valkey/valkey:9.0"},
	}}}
	// Pod has no containers — no mismatch possible.
	assert.False(t, sentinelPodNeedsUpdate(pod, template))
}

func TestSentinelPodNeedsUpdate_EmptyTemplate(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
		{Name: builder.SentinelContainerName, Image: "valkey/valkey:8.0"},
	}}}
	template := corev1.PodTemplateSpec{}
	// Template has no containers — nothing to compare against.
	assert.False(t, sentinelPodNeedsUpdate(pod, template))
}

// --- checkAndHandleSentinelRollingUpdate ---

// createSentinelPod builds a synthetic sentinel pod for use in unit tests.
func createSentinelPod(v *vkov1.Valkey, ordinal int, image string, ready bool) *corev1.Pod {
	stsName := common.StatefulSetName(v, common.ComponentSentinel)
	conditions := []corev1.PodCondition{}
	if ready {
		conditions = append(conditions, corev1.PodCondition{
			Type:   corev1.PodReady,
			Status: corev1.ConditionTrue,
		})
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%d", stsName, ordinal),
			Namespace: v.Namespace,
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: builder.SentinelContainerName, Image: image},
		}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: conditions},
	}
}

// buildTestSentinelSts creates a minimal sentinel StatefulSet whose desired container
// image is sentinelTestNewImage. Used by sentinel rolling update unit tests.
const sentinelTestNewImage = "valkey/valkey:9.0"

func buildTestSentinelSts(v *vkov1.Valkey) *appsv1.StatefulSet {
	stsName := common.StatefulSetName(v, common.ComponentSentinel)
	replicas := v.Spec.Sentinel.Replicas
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: stsName, Namespace: v.Namespace},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{
					{Name: builder.SentinelContainerName, Image: sentinelTestNewImage},
				}},
			},
		},
	}
}

func TestCheckAndHandleSentinelRollingUpdate_AllUpToDate(t *testing.T) {
	v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	const img = "valkey/valkey:9.0"
	sts := buildTestSentinelSts(v)
	p0 := createSentinelPod(v, 0, img, true)
	p1 := createSentinelPod(v, 1, img, true)
	p2 := createSentinelPod(v, 2, img, true)

	r, _ := newTestReconciler(v, sts, p0, p1, p2)

	result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)
	assert.False(t, result.NeedsRequeue, "No requeue when all sentinel pods are up to date")
	assert.Nil(t, result.Error)
}

func TestCheckAndHandleSentinelRollingUpdate_StsNotFound(t *testing.T) {
	v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	r, _ := newTestReconciler(v)

	result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)
	assert.False(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)
}

func TestCheckAndHandleSentinelRollingUpdate_DeletesOutdatedPod(t *testing.T) {
	// All 3 sentinel pods are ready and outdated (old image). Quorum is 2.
	// readyCount=3, readyCount-1=2 >= quorum=2 → delete first outdated pod.
	v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	const oldImg = "valkey/valkey:8.0"
	sts := buildTestSentinelSts(v)
	p0 := createSentinelPod(v, 0, oldImg, true)
	p1 := createSentinelPod(v, 1, oldImg, true)
	p2 := createSentinelPod(v, 2, oldImg, true)

	r, c := newTestReconciler(v, sts, p0, p1, p2)

	result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)
	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)

	// Exactly one sentinel pod should have been deleted.
	stsName := common.StatefulSetName(v, common.ComponentSentinel)
	deleted := 0
	for i := 0; i < 3; i++ {
		pod := &corev1.Pod{}
		err := c.Get(context.Background(), types.NamespacedName{
			Name: fmt.Sprintf("%s-%d", stsName, i), Namespace: "default",
		}, pod)
		if err != nil {
			deleted++
		}
	}
	assert.Equal(t, 1, deleted, "Exactly one sentinel pod should be deleted per step")
}

func TestCheckAndHandleSentinelRollingUpdate_WaitsWhenQuorumWouldBeLost(t *testing.T) {
	// quorum=2, readyCount=2, readyCount-1=1 < 2 → must wait to avoid quorum loss.
	v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	const oldImg = "valkey/valkey:8.0"
	sts := buildTestSentinelSts(v)
	p0 := createSentinelPod(v, 0, oldImg, true)
	p1 := createSentinelPod(v, 1, oldImg, true)
	p2 := createSentinelPod(v, 2, oldImg, false) // not ready — reduces readyCount to 2

	r, c := newTestReconciler(v, sts, p0, p1, p2)

	result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)
	assert.True(t, result.NeedsRequeue, "Must requeue while waiting to maintain quorum")
	assert.Nil(t, result.Error)

	// No pod should have been deleted.
	stsName := common.StatefulSetName(v, common.ComponentSentinel)
	for i := 0; i < 3; i++ {
		pod := &corev1.Pod{}
		err := c.Get(context.Background(), types.NamespacedName{
			Name: fmt.Sprintf("%s-%d", stsName, i), Namespace: "default",
		}, pod)
		assert.NoError(t, err, "Pod %d must not be deleted when quorum would break", i)
	}
}

func TestCheckAndHandleSentinelRollingUpdate_WaitsForMissingPod(t *testing.T) {
	// Pod-0 was just deleted (being recreated). p1 and p2 are outdated.
	// readyCount = 2 (only p1 and p2), quorum = 2, readyCount-1 = 1 < 2 → must wait.
	// The missing p0 naturally reduces effective readyCount, preventing another deletion.
	v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	const oldImg = "valkey/valkey:8.0"
	sts := buildTestSentinelSts(v)
	// p0 intentionally absent (simulates pod being recreated after deletion).
	p1 := createSentinelPod(v, 1, oldImg, true)
	p2 := createSentinelPod(v, 2, oldImg, true)

	r, c := newTestReconciler(v, sts, p1, p2)

	result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)
	// readyCount=2, firstOutdatedPod=p1, readyCount-1=1 < quorum=2 → wait.
	assert.True(t, result.NeedsRequeue, "Must requeue: quorum would be lost if we delete another pod while p0 is absent")
	assert.Nil(t, result.Error)

	// p1 and p2 must NOT have been deleted.
	stsName := common.StatefulSetName(v, common.ComponentSentinel)
	for _, i := range []int{1, 2} {
		pod := &corev1.Pod{}
		err := c.Get(context.Background(), types.NamespacedName{
			Name: fmt.Sprintf("%s-%d", stsName, i), Namespace: "default",
		}, pod)
		assert.NoError(t, err, "Pod %d must not be deleted while missing pod is being recreated", i)
	}
}

func TestCheckAndHandleSentinelRollingUpdate_NoActionWhenNoPodsExist(t *testing.T) {
	// No pods created yet (initial deployment). Should return empty result — no action.
	v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	sts := buildTestSentinelSts(v)
	// No pods registered in fake client.
	r, _ := newTestReconciler(v, sts)

	result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)
	assert.False(t, result.NeedsRequeue, "No action needed when no sentinel pods exist yet")
	assert.Nil(t, result.Error)
}

func TestCheckAndHandleSentinelRollingUpdate_PartialUpdate_DeletesNextPod(t *testing.T) {
	// p0 already updated (new image, ready), p1 and p2 outdated.
	// readyCount=3 (all ready), readyCount-1=2 >= quorum=2 → delete p1.
	v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	const oldImg = "valkey/valkey:8.0"
	const newImg = "valkey/valkey:9.0"
	sts := buildTestSentinelSts(v)
	p0 := createSentinelPod(v, 0, newImg, true) // already updated
	p1 := createSentinelPod(v, 1, oldImg, true)
	p2 := createSentinelPod(v, 2, oldImg, true)

	r, c := newTestReconciler(v, sts, p0, p1, p2)

	result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)
	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)

	// p1 (first outdated pod) should have been deleted.
	stsName := common.StatefulSetName(v, common.ComponentSentinel)
	pod1 := &corev1.Pod{}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: fmt.Sprintf("%s-1", stsName), Namespace: "default",
	}, pod1)
	assert.Error(t, err, "First outdated sentinel pod should be deleted")

	// p0 and p2 should still exist.
	pod0 := &corev1.Pod{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: fmt.Sprintf("%s-0", stsName), Namespace: "default",
	}, pod0))
	pod2 := &corev1.Pod{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: fmt.Sprintf("%s-2", stsName), Namespace: "default",
	}, pod2))
}

// --- persistKnownMaster ---

// TestPersistKnownMaster_SetsAnnotation verifies that persistKnownMaster writes the
// master address as the AnnotationKnownMaster annotation on the Valkey CR, which is
// later read by builder.GenerateSentinelConf so that any restarted sentinel pod
// immediately monitors the correct post-failover master instead of the default pod-0.
func TestPersistKnownMaster_SetsAnnotation(t *testing.T) {
	v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	r, c := newTestReconciler(v)
	const masterAddr = "ha-1.ha-headless.default.svc.cluster.local"

	r.persistKnownMaster(context.Background(), v, masterAddr)

	// The in-memory object must carry the annotation.
	assert.Equal(t, masterAddr, v.Annotations[builder.AnnotationKnownMaster])

	// The persisted CR must also carry the annotation.
	updated := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "ha", Namespace: "default"}, updated))
	assert.Equal(t, masterAddr, updated.Annotations[builder.AnnotationKnownMaster])
}

// TestPersistKnownMaster_UpdatesAnnotation verifies that a second call with a new
// master address overwrites the previous value (e.g. after a second rolling update).
func TestPersistKnownMaster_UpdatesAnnotation(t *testing.T) {
	v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Annotations = map[string]string{
			builder.AnnotationKnownMaster: "ha-1.ha-headless.default.svc.cluster.local",
		}
	})

	r, c := newTestReconciler(v)
	const newMaster = "ha-2.ha-headless.default.svc.cluster.local"

	r.persistKnownMaster(context.Background(), v, newMaster)

	updated := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "ha", Namespace: "default"}, updated))
	assert.Equal(t, newMaster, updated.Annotations[builder.AnnotationKnownMaster],
		"annotation must be updated to the new master address")
}

// TestPersistKnownMaster_NoOpIfUnchanged verifies that when the annotation already
// holds the correct master address, persistKnownMaster does not issue an API update.
// This is a best-effort check: we confirm the annotation is unchanged and no error occurs.
func TestPersistKnownMaster_NoOpIfUnchanged(t *testing.T) {
	const masterAddr = "ha-1.ha-headless.default.svc.cluster.local"
	v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Annotations = map[string]string{
			builder.AnnotationKnownMaster: masterAddr,
		}
	})

	r, c := newTestReconciler(v)

	// Call with the same address → should be a no-op.
	r.persistKnownMaster(context.Background(), v, masterAddr)

	updated := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "ha", Namespace: "default"}, updated))
	assert.Equal(t, masterAddr, updated.Annotations[builder.AnnotationKnownMaster],
		"annotation must remain unchanged when persistKnownMaster is called with the same address")
}
