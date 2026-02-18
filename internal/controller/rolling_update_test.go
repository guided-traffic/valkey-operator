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
				{Image: "valkey/valkey:9.0"},
			},
		},
	}
	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0"))
}

func TestPodNeedsUpdate_NeedsUpdate(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Image: "valkey/valkey:8.0"},
			},
		},
	}
	assert.True(t, podNeedsUpdate(pod, "valkey/valkey:9.0"))
}

func TestPodNeedsUpdate_EmptyContainers(t *testing.T) {
	pod := &corev1.Pod{}
	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0"))
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

	assert.True(t, r.hasMinWaitElapsed(v, failoverResetMinWait),
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

	assert.False(t, r.hasMinWaitElapsed(v, failoverResetMinWait),
		"Should return false for a recent timestamp")
}

func TestHasMinWaitElapsed_OldTimestamp(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	oldTime := time.Now().UTC().Add(-failoverResetMinWait - time.Minute)
	v.Annotations = map[string]string{
		annotationFailoverTimestamp: oldTime.Format(time.RFC3339),
	}

	assert.True(t, r.hasMinWaitElapsed(v, failoverResetMinWait),
		"Should return true for an old timestamp")
}

func TestHasMinWaitElapsed_CorruptedTimestamp(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	v.Annotations = map[string]string{
		annotationFailoverTimestamp: "invalid-timestamp",
	}

	assert.True(t, r.hasMinWaitElapsed(v, failoverResetMinWait),
		"Should return true for corrupted timestamp to allow progress")
}
