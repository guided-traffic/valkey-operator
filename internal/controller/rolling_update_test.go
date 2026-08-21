package controller

import (
	"context"
	"crypto/tls"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/health"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
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
	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "", "", nil))
}

func TestPodNeedsUpdate_NeedsUpdate(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.ValkeyContainerName, Image: "valkey/valkey:8.0"},
			},
		},
	}
	assert.True(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "", "", nil))
}

func TestPodNeedsUpdate_EmptyContainers(t *testing.T) {
	pod := &corev1.Pod{}
	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "", "", nil))
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
	assert.True(t, podNeedsUpdate(pod, "valkey/valkey:9.0", newSidecar, "", "", nil))
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
	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0", sidecar, "", "", nil))
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
	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "", "", nil))
}

// --- podNeedsUpdate: config hash checks ---

func TestPodNeedsUpdate_ConfigHashMismatch_TriggersUpdate(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				builder.AnnotationConfigHash: "deadbeef",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
			},
		},
	}
	// Pod has a different hash → allowUnencrypted was toggled → needs update.
	assert.True(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "cafebabe", "", nil))
}

func TestPodNeedsUpdate_ConfigHashMatch_NoUpdate(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				builder.AnnotationConfigHash: "cafebabe",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
			},
		},
	}
	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "cafebabe", "", nil))
}

func TestPodNeedsUpdate_NoConfigHashAnnotation_SkipsCheck(t *testing.T) {
	// Pod created before config-hash feature — no annotation present.
	// Must NOT trigger a rolling update to avoid disrupting existing clusters.
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
			},
		},
	}
	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "cafebabe", "", nil))
}

func TestPodNeedsUpdate_EmptyDesiredConfigHash_SkipsCheck(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				builder.AnnotationConfigHash: "oldhash",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
			},
		},
	}
	// Empty desired hash → skip config hash check entirely.
	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "", "", nil))
}

// --- podNeedsUpdate: pod spec hash checks ---

func TestPodNeedsUpdate_PodSpecHashMismatch_TriggersUpdate(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				builder.AnnotationPodSpecHash: "aabbccdd",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
			},
		},
	}
	// Pod has a different pod spec hash → resources changed → needs update.
	assert.True(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "", "11223344", nil))
}

func TestPodNeedsUpdate_PodSpecHashMatch_NoUpdate(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				builder.AnnotationPodSpecHash: "aabbccdd",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
			},
		},
	}
	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "", "aabbccdd", nil))
}

func TestPodNeedsUpdate_NoPodSpecHashAnnotation_SkipsCheck(t *testing.T) {
	// Pod created before pod-spec-hash feature — no annotation present.
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
			},
		},
	}
	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "", "aabbccdd", nil))
}

func TestPodNeedsUpdate_EmptyDesiredPodSpecHash_SkipsCheck(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				builder.AnnotationPodSpecHash: "oldhash",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
			},
		},
	}
	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "", "", nil))
}

// --- podNeedsUpdate: resource fallback for pods without hash annotation ---

func TestPodNeedsUpdate_NoAnnotation_ResourceChanged_TriggersUpdate(t *testing.T) {
	// Pod created before pod-spec-hash feature, but actual resources differ
	// from the desired containers.  The fallback comparison must detect this.
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  builder.ValkeyContainerName,
					Image: "valkey/valkey:9.0",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("250m"),
						},
					},
				},
			},
		},
	}
	desiredContainers := []corev1.Container{
		{
			Name:  builder.ValkeyContainerName,
			Image: "valkey/valkey:9.0",
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("500m"),
				},
			},
		},
	}
	assert.True(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "", "newhash", desiredContainers))
}

func TestPodNeedsUpdate_NoAnnotation_ResourceUnchanged_NoUpdate(t *testing.T) {
	// Pod created before pod-spec-hash feature — resources match the desired spec.
	// Must NOT trigger a rolling update (operator upgrade scenario).
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  builder.ValkeyContainerName,
					Image: "valkey/valkey:9.0",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("250m"),
						},
					},
				},
			},
		},
	}
	desiredContainers := []corev1.Container{
		{
			Name:  builder.ValkeyContainerName,
			Image: "valkey/valkey:9.0",
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("250m"),
				},
			},
		},
	}
	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "", "newhash", desiredContainers))
}

func TestPodNeedsUpdate_NoAnnotation_LimitsChanged_TriggersUpdate(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  builder.ValkeyContainerName,
					Image: "valkey/valkey:9.0",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("256Mi"),
						},
					},
				},
			},
		},
	}
	desiredContainers := []corev1.Container{
		{
			Name:  builder.ValkeyContainerName,
			Image: "valkey/valkey:9.0",
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
			},
		},
	}
	assert.True(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "", "newhash", desiredContainers))
}

func TestPodNeedsUpdate_WithAnnotation_IgnoresContainersFallback(t *testing.T) {
	// When the annotation IS present, the container comparison is skipped;
	// the hash check alone determines whether an update is needed.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				builder.AnnotationPodSpecHash: "samehash",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  builder.ValkeyContainerName,
					Image: "valkey/valkey:9.0",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("250m"),
						},
					},
				},
			},
		},
	}
	desiredContainers := []corev1.Container{
		{
			Name:  builder.ValkeyContainerName,
			Image: "valkey/valkey:9.0",
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("500m"),
				},
			},
		},
	}
	// Hash matches → no update, even though resources differ.
	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "", "samehash", desiredContainers))
}

func TestPodNeedsUpdate_NoAnnotation_NilDesiredContainers_SkipsFallback(t *testing.T) {
	// Nil desiredContainers → fallback comparison is skipped (backward compat).
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  builder.ValkeyContainerName,
					Image: "valkey/valkey:9.0",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("250m"),
						},
					},
				},
			},
		},
	}
	assert.False(t, podNeedsUpdate(pod, "valkey/valkey:9.0", "", "", "newhash", nil))
}

// --- containersResourceChanged ---

func TestContainersResourceChanged_RequestsDiffer(t *testing.T) {
	actual := []corev1.Container{{
		Name: builder.ValkeyContainerName,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m")},
		},
	}}
	desired := []corev1.Container{{
		Name: builder.ValkeyContainerName,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")},
		},
	}}
	assert.True(t, containersResourceChanged(actual, desired))
}

func TestContainersResourceChanged_LimitsDiffer(t *testing.T) {
	actual := []corev1.Container{{
		Name: builder.ValkeyContainerName,
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("256Mi")},
		},
	}}
	desired := []corev1.Container{{
		Name: builder.ValkeyContainerName,
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi")},
		},
	}}
	assert.True(t, containersResourceChanged(actual, desired))
}

func TestContainersResourceChanged_Equal(t *testing.T) {
	actual := []corev1.Container{{
		Name: builder.ValkeyContainerName,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m")},
			Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("256Mi")},
		},
	}}
	desired := []corev1.Container{{
		Name: builder.ValkeyContainerName,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m")},
			Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("256Mi")},
		},
	}}
	assert.False(t, containersResourceChanged(actual, desired))
}

func TestContainersResourceChanged_NilSlices(t *testing.T) {
	assert.False(t, containersResourceChanged(nil, nil))
}

func TestContainersResourceChanged_UnknownContainerSkipped(t *testing.T) {
	actual := []corev1.Container{{
		Name: "unknown",
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
		},
	}}
	desired := []corev1.Container{{
		Name: builder.ValkeyContainerName,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")},
		},
	}}
	// "unknown" is not in desired → skipped; no match found → false
	assert.False(t, containersResourceChanged(actual, desired))
}

func TestContainersResourceChanged_RequestsAddedInDesired(t *testing.T) {
	actual := []corev1.Container{{
		Name: builder.ValkeyContainerName,
	}}
	desired := []corev1.Container{{
		Name: builder.ValkeyContainerName,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m")},
		},
	}}
	assert.True(t, containersResourceChanged(actual, desired))
}

// --- countReplacedPods ---

func TestCountReplacedPods(t *testing.T) {
	pods := []podState{
		{name: "p-0", needsUpdate: true, ready: true},
		{name: "p-1", needsUpdate: false, ready: true},
		{name: "p-2", needsUpdate: false, ready: false}, // replaced but not ready
	}
	// countUpdatedPods only counts ready + !needsUpdate → 1
	assert.Equal(t, 1, countUpdatedPods(pods))
	// countReplacedPods ignores readiness → 2
	assert.Equal(t, 2, countReplacedPods(pods))
}

// --- sentinel podNeedsUpdate: resource fallback ---

func TestSentinelPodNeedsUpdate_NoPodAnnotations_ResourceChanged_TriggersUpdate(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{
				Name:  builder.SentinelContainerName,
				Image: "valkey/valkey:9.0",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
				},
			},
		}},
	}
	template := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				builder.AnnotationPodSpecHash: "newhash",
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{
				Name:  builder.SentinelContainerName,
				Image: "valkey/valkey:9.0",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m")},
				},
			},
		}},
	}
	assert.True(t, sentinelPodNeedsUpdate(pod, template))
}

func TestSentinelPodNeedsUpdate_NoPodAnnotations_ResourceUnchanged_NoUpdate(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{
				Name:  builder.SentinelContainerName,
				Image: "valkey/valkey:9.0",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
				},
			},
		}},
	}
	template := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				builder.AnnotationPodSpecHash: "newhash",
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{
				Name:  builder.SentinelContainerName,
				Image: "valkey/valkey:9.0",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
				},
			},
		}},
	}
	assert.False(t, sentinelPodNeedsUpdate(pod, template))
}

// --- podSpecHashFromSts ---

func TestPodSpecHashFromSts_Found(t *testing.T) {
	sts := &appsv1.StatefulSet{
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						builder.AnnotationPodSpecHash: "abcd1234",
					},
				},
			},
		},
	}
	assert.Equal(t, "abcd1234", podSpecHashFromSts(sts))
}

func TestPodSpecHashFromSts_NotFound(t *testing.T) {
	sts := &appsv1.StatefulSet{
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{},
		},
	}
	assert.Equal(t, "", podSpecHashFromSts(sts))
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
// readyStatefulSetFor returns a data StatefulSet that already reports `replicas`
// created and ready pods.
//
// The fake client runs no statefulset-controller, so a StatefulSet the reconcile
// creates itself stays at status.replicas = 0 no matter how many pods the test
// seeded. That state means "short of pods" to the reconciler and makes it requeue
// for the nudge, which drowns out assertions about rolling-update requeues. Seeding
// the status to match the pods a test creates keeps the fixture honest.
func readyStatefulSetFor(v *vkov1.Valkey, replicas int32) *appsv1.StatefulSet {
	desired := replicas
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      common.StatefulSetName(v, common.ComponentValkey),
			Namespace: v.Namespace,
		},
		Spec: appsv1.StatefulSetSpec{Replicas: &desired},
		Status: appsv1.StatefulSetStatus{
			Replicas:      replicas,
			ReadyReplicas: replicas,
		},
	}
}

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
	// Provide replication info so the sync check on the already-replaced pod-0 passes.
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{
				Role:                 "slave",
				MasterSyncInProgress: false,
				MasterLinkStatus:     "up",
			}, nil
		},
	}
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

	r, _ := newTestReconciler(v, pod0, readyStatefulSetFor(v, 1))
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
	// When only the sidecar image changed on a TRUE standalone (replicas=1),
	// the pod must NOT be deleted. The update is deferred to the next natural
	// pod restart.
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
	assert.NoError(t, err, "Pod must NOT be deleted for a sidecar-only change in true standalone mode")
}

func TestHandleStandaloneRollingUpdate_SidecarOnlyChange_MultiReplica_DeletesPod(t *testing.T) {
	// When only the sidecar image changed but the cluster has multiple replicas
	// (replicas > 1), the pod MUST be deleted because there is redundancy.
	const newSidecar = "operator:v2.0"
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Image = "valkey/valkey:9.0"
		v.Spec.Replicas = 3
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
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-1", Namespace: "default"},
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
	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-2", Namespace: "default"},
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

	r, c := newTestReconciler(v, pod0, pod1, pod2)
	reconcileOnce(t, r, "test", "default")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, sts))
	for i, cont := range sts.Spec.Template.Spec.Containers {
		if cont.Name == builder.SidecarContainerName {
			sts.Spec.Template.Spec.Containers[i].Image = newSidecar
		}
	}

	result := r.handleStandaloneRollingUpdate(context.Background(), v, sts)

	// Must trigger a rolling update (requeue), not defer.
	assert.True(t, result.NeedsRequeue, "Multi-replica sidecar-only change must trigger requeue")
	assert.Nil(t, result.Error)

	// First pod (test-0) should have been deleted.
	pod := &corev1.Pod{}
	err := c.Get(context.Background(), types.NamespacedName{Name: "test-0", Namespace: "default"}, pod)
	assert.Error(t, err, "Pod must be deleted for sidecar-only change with multi-replica cluster")
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

func TestSentinelPodNeedsUpdate_PodSpecHashMismatch_TriggersUpdate(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				builder.AnnotationPodSpecHash: "oldhash",
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: builder.SentinelContainerName, Image: "valkey/valkey:9.0"},
		}},
	}
	template := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				builder.AnnotationPodSpecHash: "newhash",
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: builder.SentinelContainerName, Image: "valkey/valkey:9.0"},
		}},
	}
	assert.True(t, sentinelPodNeedsUpdate(pod, template))
}

func TestSentinelPodNeedsUpdate_PodSpecHashMatch_NoUpdate(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				builder.AnnotationPodSpecHash: "samehash",
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: builder.SentinelContainerName, Image: "valkey/valkey:9.0"},
		}},
	}
	template := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				builder.AnnotationPodSpecHash: "samehash",
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: builder.SentinelContainerName, Image: "valkey/valkey:9.0"},
		}},
	}
	assert.False(t, sentinelPodNeedsUpdate(pod, template))
}

func TestSentinelPodNeedsUpdate_ConfigHashMismatch_TriggersUpdate(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				builder.AnnotationConfigHash: "oldcfg",
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: builder.SentinelContainerName, Image: "valkey/valkey:9.0"},
		}},
	}
	template := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				builder.AnnotationConfigHash: "newcfg",
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: builder.SentinelContainerName, Image: "valkey/valkey:9.0"},
		}},
	}
	assert.True(t, sentinelPodNeedsUpdate(pod, template))
}

func TestSentinelPodNeedsUpdate_NoPodAnnotations_SkipsHashCheck(t *testing.T) {
	// Sentinel pod without annotations (old operator) — skip hash checks.
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: builder.SentinelContainerName, Image: "valkey/valkey:9.0"},
		}},
	}
	template := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				builder.AnnotationPodSpecHash: "newhash",
				builder.AnnotationConfigHash:  "newcfg",
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: builder.SentinelContainerName, Image: "valkey/valkey:9.0"},
		}},
	}
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

// --- ADR 0001 D7: a Sentinel rolling-update error must lead to a retry ---

// TestReconcileWorkload_RetriesAfterSentinelRollingUpdateError pins the ADR 0001 D7 fix.
// The Sentinel rolling-update error path used to return (ctrl.Result{}, true) with
// no error and no RequeueAfter, so the pass ended without any retry — and since a
// status write does not re-trigger the CR watch (GenerationChangedPredicate),
// reconciliation stalled until some unrelated owned-object event arrived.
func TestReconcileWorkload_RetriesAfterSentinelRollingUpdateError(t *testing.T) {
	v, objs := sentinelQuorumWaitFixture(3)
	failingPod := fmt.Sprintf("%s-0", common.StatefulSetName(v, common.ComponentSentinel))
	failSentinelPodGet := interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey,
			obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Pod); ok && key.Name == failingPod {
				return apierrors.NewInternalError(fmt.Errorf("etcd unavailable"))
			}
			return c.Get(ctx, key, obj, opts...)
		},
	}
	r, _ := newInterceptedReconciler(failSentinelPodGet, objs...)

	result, err := r.reconcileWorkload(context.Background(), v)

	require.Error(t, err,
		"a Sentinel rolling-update error must reach the caller so the rate limiter retries the pass")
	assert.Contains(t, err.Error(), failingPod)
	assert.Equal(t, vkov1.ValkeyPhaseError, v.Status.Phase,
		"the error must also be visible on the CR, not only in the retry")
	assert.Zero(t, result.RequeueAfter,
		"the retry comes from the returned error; an extra RequeueAfter would bypass the rate limiter")
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

// rollingUpdateTestCACert is a self-signed CA certificate for testing TLS config
// in rolling-update sentinel port-selection tests.
const rollingUpdateTestCACert = `-----BEGIN CERTIFICATE-----
MIIBejCCAR+gAwIBAgIUS2/Z6nko0KrjmZ0isXIKpnW9gaMwCgYIKoZIzj0EAwIw
EjEQMA4GA1UECgwHQWNtZSBDbzAeFw0yNjAyMTkxNTI1NThaFw0zNjAyMTcxNTI1
NThaMBIxEDAOBgNVBAoMB0FjbWUgQ28wWTATBgcqhkjOPQIBBggqhkjOPQMBBwNC
AARIjEAmZv4pCmau7ruKl2JZHwl2MjolHJYy7lxhkLw7TWfj8iX7Fxnhlz0BXZqP
oF7ek0Fxvw7p60NYXxWjwkxZo1MwUTAdBgNVHQ4EFgQU48l9XI8AgN3399I9KLB1
D7y4XccwHwYDVR0jBBgwFoAU48l9XI8AgN3399I9KLB1D7y4XccwDwYDVR0TAQH/
BAUwAwEB/zAKBggqhkjOPQQDAgNJADBGAiEA/M1Mw9nDZg7HKX8NxL+GZy8KSvOp
HZpATeWHjH8TsQ0CIQCkTrqAe9DpBTdPlF6f9kyUkVLtXiMjb6KTTH9m8x3Zzg==
-----END CERTIFICATE-----`

// newTestSentinelTLSSecret builds a fake TLS secret with a valid CA certificate
// so that buildTLSConfig returns a non-nil *tls.Config for sentinel connections.
func newTestSentinelTLSSecret(secretName, ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: ns,
		},
		Data: map[string][]byte{
			"ca.crt": []byte(rollingUpdateTestCACert),
		},
	}
}

// TestTriggerSentinelFailover_TLS_UsesTLSPort verifies that when TLS is enabled for
// the Valkey cluster, triggerSentinelFailover connects to the sentinel TLS port
// (36379) instead of the plain-text port (26379).
//
// Regression test for: operator stuck in failover-reset loop because TLS client
// was dialing the plain port (26379), causing TLS handshake failures reported as
// "unexpected error" rather than connection refused/timeout.
func TestTriggerSentinelFailover_TLS_UsesTLSPort(t *testing.T) {
	v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 1}
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "test-issuer"},
			},
		}
	})

	secretName := builder.SentinelTLSSecretName(v)
	tlsSecret := newTestSentinelTLSSecret(secretName, "default")
	r, _ := newTestReconciler(v, tlsSecret)

	err := r.triggerSentinelFailover(context.Background(), v)

	// The function must fail (no real sentinel running), but the error must
	// reference the TLS port 36379, proving the correct port was selected.
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf(":%d", builder.SentinelTLSPort),
		"sentinel failover with TLS enabled must target the TLS port (%d)", builder.SentinelTLSPort)
	assert.NotContains(t, err.Error(), fmt.Sprintf(":%d", builder.SentinelPort),
		"sentinel failover with TLS enabled must NOT target the plain-text port (%d)", builder.SentinelPort)
}

// perPodMockChecker is an InstanceChecker mock that returns per-pod replication info.
// This allows unit tests that require specific replication states per pod.
type perPodMockChecker struct {
	infos map[string]*valkeyclient.ReplicationInfo
}

func (m *perPodMockChecker) PingPod(_ context.Context, _ *vkov1.Valkey, _ string) error {
	return nil
}

func (m *perPodMockChecker) CheckCluster(_ context.Context, v *vkov1.Valkey) *health.ClusterState {
	return &health.ClusterState{
		MasterPod:     fmt.Sprintf("%s-0", v.Name),
		ReadyReplicas: v.Spec.Replicas - 1,
		TotalReplicas: v.Spec.Replicas - 1,
		AllSynced:     true,
	}
}

func (m *perPodMockChecker) GetReplicationInfo(_ context.Context, _ *vkov1.Valkey, podName string) (*valkeyclient.ReplicationInfo, error) {
	if info, ok := m.infos[podName]; ok {
		return info, nil
	}
	return nil, fmt.Errorf("mock: no replication info for %s", podName)
}

// TestCheckFinalizationTopology_StalledCascadedReplication_Proceeds verifies that
// when finalization is stalled and connectedSlaves < expectedReplicas due to
// cascaded replication, checkFinalizationTopology calls forceReplicaConnections
// to break the chain and returns nil (proceeds with finalization).
//
// Root cause: after a failover, a replica can end up connected to the old master
// (now itself a replica) instead of the new master — a cascaded chain:
//
//	new-master → old-master-pod → cascaded-replica
//
// The cascaded replica does not appear in the new master's INFO replication, so
// after SENTINEL REMOVE+MONITOR sentinel cannot discover or fix it. Without
// forceReplicaConnections the cluster is permanently stuck in "Syncing: 1/2".
func TestCheckFinalizationTopology_StalledCascadedReplication_Proceeds(t *testing.T) {
	v := newTestValkey("ha-cascaded", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:9.0"
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	// Finalization has been stalled for longer than finalizationStallTimeout.
	stalledTime := time.Now().UTC().Add(-finalizationStallTimeout - time.Minute)
	v.Annotations = map[string]string{
		annotationRollingUpdateState:    stateReplacingMaster,
		annotationFinalizationTimestamp: stalledTime.Format(time.RFC3339),
	}

	// All pods have the new image. Pod-1 is master (after failover from pod-0).
	pod0 := createPodForSts(v, 0, "valkey/valkey:9.0", true)
	pod1 := createPodForSts(v, 1, "valkey/valkey:9.0", true)
	pod2 := createPodForSts(v, 2, "valkey/valkey:9.0", true)
	pod1.Labels[common.LabelInstanceRole] = common.RoleMaster

	// Pod-1 is master with only 1 connected slave. Pod-2 is the cascaded replica
	// (connected to pod-0, not pod-1) and does not appear in pod-1's slave list.
	mockChecker := &perPodMockChecker{
		infos: map[string]*valkeyclient.ReplicationInfo{
			"ha-cascaded-1": {Role: common.RoleMaster, ConnectedSlaves: 1},
			"ha-cascaded-0": {Role: common.RoleReplica},
			"ha-cascaded-2": {Role: common.RoleReplica},
		},
	}

	r, _ := newTestReconciler(v, pod0, pod1, pod2)
	r.InstanceChecker = mockChecker
	reconcileOnce(t, r, "ha-cascaded", "default")

	pods := []podState{
		{name: "ha-cascaded-0", pod: pod0, exists: true, ready: true, needsUpdate: false, isMaster: false},
		{name: "ha-cascaded-1", pod: pod1, exists: true, ready: true, needsUpdate: false, isMaster: true},
		{name: "ha-cascaded-2", pod: pod2, exists: true, ready: true, needsUpdate: false, isMaster: false},
	}

	// checkFinalizationTopology must return nil (proceed with partial sync) even
	// with only 1/2 replicas directly connected, because the stall path now calls
	// forceReplicaConnections to break the cascaded chain before resetting sentinel.
	result := r.checkFinalizationTopology(context.Background(), v, pods)
	assert.Nil(t, result,
		"Should proceed with finalization when stalled, even with cascaded replication (1/2 replicas direct)")
}

// TestSyncSentinelWithMaster_StalledGetReplicationInfoFails_ProceedsWithMasterAddr
// verifies that when GetReplicationInfo fails during a stalled finalization,
// syncSentinelWithMaster uses the identified master pod's address (not an empty
// string that would fall back to pod-0, which may not be the master).
func TestSyncSentinelWithMaster_StalledGetReplicationInfoFails_ProceedsWithMasterAddr(t *testing.T) {
	v := newTestValkey("ha-errstall", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:9.0"
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	stalledTime := time.Now().UTC().Add(-finalizationStallTimeout - time.Minute)
	v.Annotations = map[string]string{
		annotationFinalizationTimestamp: stalledTime.Format(time.RFC3339),
	}

	// Pod-2 is master (not pod-0 — verifies the fix avoids the pod-0 fallback).
	masterPS := podState{
		name:     "ha-errstall-2",
		isMaster: true,
		exists:   true,
		ready:    true,
	}

	// Default mockInstanceChecker returns an error for GetReplicationInfo,
	// simulating a connectivity failure during a stalled finalization.
	r, _ := newTestReconciler(v)

	// syncSentinelWithMaster must return nil (proceed) in the stalled error path.
	// Before the fix it used resetSentinelState("") which defaults to pod-0;
	// after the fix it uses the correct master address from masterPS.name.
	result := r.syncSentinelWithMaster(context.Background(), v, masterPS, 2,
		r.getInstanceChecker(), true)
	assert.Nil(t, result,
		"Should return nil in stalled error path to continue finalization with correct master address")
}

// TestTriggerSentinelFailover_NoTLS_UsesPlainPort verifies that when TLS is disabled,
// triggerSentinelFailover targets the plain-text sentinel port (26379).
func TestTriggerSentinelFailover_NoTLS_UsesPlainPort(t *testing.T) {
	v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 1}
		// No TLS spec → plain-text mode.
	})

	r, _ := newTestReconciler(v)

	err := r.triggerSentinelFailover(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf(":%d", builder.SentinelPort),
		"sentinel failover without TLS must target the plain-text port (%d)", builder.SentinelPort)
}

// TestTriggerSentinelFailover_DisableAuth_NoAuthSent verifies that when
// sentinel.disableAuth is true, the operator does NOT send AUTH to sentinel.
// Regression test for: operator stuck in failover loop because AUTH was sent
// to sentinel that has no password configured.
func TestTriggerSentinelFailover_DisableAuth_NoAuthSent(t *testing.T) {
	v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:     true,
			Replicas:    1,
			DisableAuth: true,
		}
	})

	authSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "my-secret", Namespace: "default"},
		Data:       map[string][]byte{"password": []byte("supersecret")},
	}
	r, _ := newTestReconciler(v, authSecret)

	err := r.triggerSentinelFailover(context.Background(), v)

	// The function will fail (no real sentinel), but the error must NOT contain
	// "AUTH failed" — it should be a connection error instead, proving no AUTH was sent.
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "AUTH failed",
		"sentinel failover with disableAuth must not send AUTH to sentinel")
}

// TestIsSentinelAwareOfReplicas_DisableAuth verifies that isSentinelAwareOfReplicas
// respects disableAuth when connecting to sentinel.
func TestIsSentinelAwareOfReplicas_DisableAuth(t *testing.T) {
	v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:     true,
			Replicas:    3,
			DisableAuth: true,
		}
	})

	authSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "my-secret", Namespace: "default"},
		Data:       map[string][]byte{"password": []byte("supersecret")},
	}
	r, _ := newTestReconciler(v, authSecret)

	// All sentinels unreachable → returns true (optimistic).
	// The important thing is it doesn't panic or behave differently with disableAuth.
	result := r.isSentinelAwareOfReplicas(context.Background(), v, 2)
	assert.True(t, result, "Should return true when all sentinels are unreachable")
}

// --- Edge Case Tests for Private Helper Functions ---

func TestPodImageChanged_NoContainers(t *testing.T) {
	pod := &corev1.Pod{}
	assert.False(t, podImageChanged(pod, "valkey:9.0", "sidecar:2.0"))
}

func TestPodImageChanged_UnknownContainerIgnored(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "unknown-container", Image: "some-image:1.0"},
			},
		},
	}
	assert.False(t, podImageChanged(pod, "valkey:9.0", "sidecar:2.0"))
}

func TestPodImageChanged_ValkeyMatchesSidecarDiffers(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.ValkeyContainerName, Image: "valkey:9.0"},
				{Name: builder.SidecarContainerName, Image: "sidecar:1.0"},
			},
		},
	}
	assert.True(t, podImageChanged(pod, "valkey:9.0", "sidecar:2.0"))
}

func TestPodImageChanged_BothEmpty(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.ValkeyContainerName, Image: "valkey:8.0"},
				{Name: builder.SidecarContainerName, Image: "sidecar:1.0"},
			},
		},
	}
	// Empty desired images → skip checks.
	assert.False(t, podImageChanged(pod, "", ""))
}

func TestPodAnnotationHashChanged_NilAnnotations(t *testing.T) {
	pod := &corev1.Pod{} // Annotations is nil.
	assert.False(t, podAnnotationHashChanged(pod, "cafebabe"))
}

func TestPodAnnotationHashChanged_EmptyDesiredHash(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{builder.AnnotationConfigHash: "oldhash"},
		},
	}
	assert.False(t, podAnnotationHashChanged(pod, ""))
}

func TestPodAnnotationHashChanged_PodHashEmptyDesiredNonEmpty(t *testing.T) {
	// Pod was created before the feature (no annotation) — should not trigger update.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{},
		},
	}
	assert.False(t, podAnnotationHashChanged(pod, "newhash"))
}

func TestPodAnnotationHashChanged_SameHash(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{builder.AnnotationConfigHash: "aabbccdd"},
		},
	}
	assert.False(t, podAnnotationHashChanged(pod, "aabbccdd"))
}

func TestPodAnnotationHashChanged_DifferentHash(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{builder.AnnotationConfigHash: "oldhash"},
		},
	}
	assert.True(t, podAnnotationHashChanged(pod, "newhash"))
}

// --- podSpecHashChanged edge cases ---

func TestPodSpecHashChanged_EmptyDesiredHash(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{builder.AnnotationPodSpecHash: "something"},
		},
	}
	assert.False(t, podSpecHashChanged(pod, "", nil))
}

func TestPodSpecHashChanged_PodHasAnnotation_Match(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{builder.AnnotationPodSpecHash: "aabb"},
		},
	}
	assert.False(t, podSpecHashChanged(pod, "aabb", nil))
}

func TestPodSpecHashChanged_PodHasAnnotation_Mismatch(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{builder.AnnotationPodSpecHash: "aabb"},
		},
	}
	assert.True(t, podSpecHashChanged(pod, "ccdd", nil))
}

func TestPodSpecHashChanged_NoAnnotation_NilContainers(t *testing.T) {
	pod := &corev1.Pod{} // No annotations, no containers.
	assert.False(t, podSpecHashChanged(pod, "newhash", nil))
}

func TestPodSpecHashChanged_NoAnnotation_ResourcesMatch(t *testing.T) {
	containers := []corev1.Container{{
		Name: builder.ValkeyContainerName,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m")},
		},
	}}
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{Containers: containers},
	}
	assert.False(t, podSpecHashChanged(pod, "newhash", containers))
}

func TestPodSpecHashChanged_NoAnnotation_ResourcesDiffer(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: builder.ValkeyContainerName,
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
				},
			}},
		},
	}
	desired := []corev1.Container{{
		Name: builder.ValkeyContainerName,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")},
		},
	}}
	assert.True(t, podSpecHashChanged(pod, "newhash", desired))
}

// --- containersResourceChanged edge cases ---

func TestContainersResourceChanged_BothEmpty(t *testing.T) {
	assert.False(t, containersResourceChanged([]corev1.Container{}, []corev1.Container{}))
}

func TestContainersResourceChanged_ActualHasNoMatch(t *testing.T) {
	actual := []corev1.Container{{Name: "foo"}}
	desired := []corev1.Container{{Name: "bar"}}
	assert.False(t, containersResourceChanged(actual, desired))
}

func TestContainersResourceChanged_LimitsRemovedInDesired(t *testing.T) {
	actual := []corev1.Container{{
		Name: builder.ValkeyContainerName,
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi")},
		},
	}}
	desired := []corev1.Container{{
		Name:      builder.ValkeyContainerName,
		Resources: corev1.ResourceRequirements{},
	}}
	assert.True(t, containersResourceChanged(actual, desired))
}

func TestContainersResourceChanged_MultipleContainers_OnlyFirstDiffers(t *testing.T) {
	actual := []corev1.Container{
		{
			Name: builder.ValkeyContainerName,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
			},
		},
		{
			Name: builder.SidecarContainerName,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m")},
			},
		},
	}
	desired := []corev1.Container{
		{
			Name: builder.ValkeyContainerName,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")},
			},
		},
		{
			Name: builder.SidecarContainerName,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m")},
			},
		},
	}
	assert.True(t, containersResourceChanged(actual, desired))
}

func TestContainersResourceChanged_EquivalentQuantities(t *testing.T) {
	// 1000m CPU == 1 CPU — Quantity.Cmp handles this.
	actual := []corev1.Container{{
		Name: builder.ValkeyContainerName,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1000m")},
		},
	}}
	desired := []corev1.Container{{
		Name: builder.ValkeyContainerName,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")},
		},
	}}
	assert.False(t, containersResourceChanged(actual, desired))
}

// --- resourceListEqual edge cases ---

func TestResourceListEqual_BothNil(t *testing.T) {
	assert.True(t, resourceListEqual(nil, nil))
}

func TestResourceListEqual_OneNilOneEmpty(t *testing.T) {
	assert.True(t, resourceListEqual(nil, corev1.ResourceList{}))
}

func TestResourceListEqual_DifferentKeys(t *testing.T) {
	a := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}
	b := corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("128Mi")}
	assert.False(t, resourceListEqual(a, b))
}

func TestResourceListEqual_SameKeysDifferentValues(t *testing.T) {
	a := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}
	b := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m")}
	assert.False(t, resourceListEqual(a, b))
}

func TestResourceListEqual_SameKeysEqualValues(t *testing.T) {
	a := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("250m"),
		corev1.ResourceMemory: resource.MustParse("256Mi"),
	}
	b := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("250m"),
		corev1.ResourceMemory: resource.MustParse("256Mi"),
	}
	assert.True(t, resourceListEqual(a, b))
}

func TestResourceListEqual_DifferentLengths(t *testing.T) {
	a := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}
	b := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("100m"),
		corev1.ResourceMemory: resource.MustParse("256Mi"),
	}
	assert.False(t, resourceListEqual(a, b))
}

// --- isPodReady edge cases ---

func TestIsPodReady_MultipleConditions(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
				{Type: corev1.PodReady, Status: corev1.ConditionFalse},
				{Type: corev1.ContainersReady, Status: corev1.ConditionTrue},
			},
		},
	}
	assert.False(t, isPodReady(pod), "PodReady is False, even though other conditions are True")
}

func TestIsPodReady_ReadyConditionMissing(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
			},
		},
	}
	assert.False(t, isPodReady(pod), "Should return false when Ready condition is missing")
}

// --- sidecarImageFromSts edge cases ---

func TestSidecarImageFromSts_MultipleContainers(t *testing.T) {
	sts := &appsv1.StatefulSet{
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
						{Name: "unrelated", Image: "unrelated:1.0"},
						{Name: builder.SidecarContainerName, Image: "sidecar:2.0"},
					},
				},
			},
		},
	}
	assert.Equal(t, "sidecar:2.0", sidecarImageFromSts(sts))
}

func TestSidecarImageFromSts_EmptyContainers(t *testing.T) {
	sts := &appsv1.StatefulSet{
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{},
		},
	}
	assert.Equal(t, "", sidecarImageFromSts(sts))
}

// --- detectImageChange edge cases ---

func TestDetectImageChange_MultipleContainers_FirstMatches(t *testing.T) {
	sts := &appsv1.StatefulSet{
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Image: "valkey/valkey:8.0"},
						{Image: "sidecar:1.0"},
					},
				},
			},
		},
	}
	// detectImageChange only checks first container.
	assert.False(t, detectImageChange("valkey/valkey:8.0", sts))
}

func TestDetectImageChange_EmptyDesired(t *testing.T) {
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
	assert.True(t, detectImageChange("", sts))
}

// --- sentinelPodNeedsUpdate edge cases ---

func TestSentinelPodNeedsUpdate_ImageMatch_NoAnnotations(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.SentinelContainerName, Image: "valkey/valkey:9.0"},
			},
		},
	}
	template := corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.SentinelContainerName, Image: "valkey/valkey:9.0"},
			},
		},
	}
	assert.False(t, sentinelPodNeedsUpdate(pod, template))
}

func TestSentinelPodNeedsUpdate_ImageDiffers(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.SentinelContainerName, Image: "valkey/valkey:8.0"},
			},
		},
	}
	template := corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.SentinelContainerName, Image: "valkey/valkey:9.0"},
			},
		},
	}
	assert.True(t, sentinelPodNeedsUpdate(pod, template))
}

func TestSentinelPodNeedsUpdate_ConfigHashMismatch(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				builder.AnnotationConfigHash: "oldhash",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.SentinelContainerName, Image: "valkey/valkey:9.0"},
			},
		},
	}
	template := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				builder.AnnotationConfigHash: "newhash",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.SentinelContainerName, Image: "valkey/valkey:9.0"},
			},
		},
	}
	assert.True(t, sentinelPodNeedsUpdate(pod, template))
}

func TestSentinelPodNeedsUpdate_ConfigHashMatch(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				builder.AnnotationConfigHash: "samehash",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.SentinelContainerName, Image: "valkey/valkey:9.0"},
			},
		},
	}
	template := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				builder.AnnotationConfigHash: "samehash",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: builder.SentinelContainerName, Image: "valkey/valkey:9.0"},
			},
		},
	}
	assert.False(t, sentinelPodNeedsUpdate(pod, template))
}

// --- countUpdatedPods / countReplacedPods edge cases ---

func TestCountUpdatedPods_AllUpdatedAndReady(t *testing.T) {
	pods := []podState{
		{needsUpdate: false, ready: true},
		{needsUpdate: false, ready: true},
		{needsUpdate: false, ready: true},
	}
	assert.Equal(t, 3, countUpdatedPods(pods))
}

func TestCountUpdatedPods_NoneUpdated(t *testing.T) {
	pods := []podState{
		{needsUpdate: true, ready: true},
		{needsUpdate: true, ready: true},
	}
	assert.Equal(t, 0, countUpdatedPods(pods))
}

func TestCountUpdatedPods_UpdatedButNotReady(t *testing.T) {
	pods := []podState{
		{needsUpdate: false, ready: false},
		{needsUpdate: false, ready: false},
	}
	assert.Equal(t, 0, countUpdatedPods(pods))
}

func TestCountUpdatedPods_EmptySlice(t *testing.T) {
	assert.Equal(t, 0, countUpdatedPods(nil))
	assert.Equal(t, 0, countUpdatedPods([]podState{}))
}

func TestCountReplacedPods_AllReplaced(t *testing.T) {
	pods := []podState{
		{needsUpdate: false, ready: true},
		{needsUpdate: false, ready: false},
	}
	assert.Equal(t, 2, countReplacedPods(pods))
}

func TestCountReplacedPods_NoneReplaced(t *testing.T) {
	pods := []podState{
		{needsUpdate: true, ready: true},
		{needsUpdate: true, ready: false},
	}
	assert.Equal(t, 0, countReplacedPods(pods))
}

func TestCountReplacedPods_EmptySlice(t *testing.T) {
	assert.Equal(t, 0, countReplacedPods(nil))
	assert.Equal(t, 0, countReplacedPods([]podState{}))
}

// --- podSpecHashFromSts edge cases ---

func TestPodSpecHashFromSts_NilAnnotations(t *testing.T) {
	sts := &appsv1.StatefulSet{
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: nil,
				},
			},
		},
	}
	assert.Equal(t, "", podSpecHashFromSts(sts))
}

func TestPodSpecHashFromSts_EmptyAnnotations(t *testing.T) {
	sts := &appsv1.StatefulSet{
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
		},
	}
	assert.Equal(t, "", podSpecHashFromSts(sts))
}

// --- Multi-replica without Sentinel rolling update tests ---

func TestFindPromotionCandidate_FindsReadyUpdatedReplica(t *testing.T) {
	pods := []podState{
		{name: "pod-0", exists: true, ready: true, needsUpdate: true, isMaster: true},
		{name: "pod-1", exists: true, ready: true, needsUpdate: false, isMaster: false},
		{name: "pod-2", exists: true, ready: true, needsUpdate: false, isMaster: false},
	}
	idx := findPromotionCandidate(pods, 0)
	assert.Equal(t, 1, idx)
}

func TestFindPromotionCandidate_SkipsMaster(t *testing.T) {
	pods := []podState{
		{name: "pod-0", exists: true, ready: true, needsUpdate: false, isMaster: true},
		{name: "pod-1", exists: true, ready: true, needsUpdate: true, isMaster: false},
		{name: "pod-2", exists: true, ready: true, needsUpdate: false, isMaster: false},
	}
	idx := findPromotionCandidate(pods, 0)
	assert.Equal(t, 2, idx)
}

func TestFindPromotionCandidate_NoCandidates(t *testing.T) {
	pods := []podState{
		{name: "pod-0", exists: true, ready: true, needsUpdate: true, isMaster: true},
		{name: "pod-1", exists: true, ready: true, needsUpdate: true, isMaster: false},
		{name: "pod-2", exists: true, ready: false, needsUpdate: false, isMaster: false},
	}
	idx := findPromotionCandidate(pods, 0)
	assert.Equal(t, -1, idx)
}

func TestFindPromotionCandidate_SkipsNotExisting(t *testing.T) {
	pods := []podState{
		{name: "pod-0", exists: true, ready: true, needsUpdate: true, isMaster: true},
		{name: "pod-1", exists: false, ready: false, needsUpdate: false, isMaster: false},
		{name: "pod-2", exists: true, ready: true, needsUpdate: false, isMaster: false},
	}
	idx := findPromotionCandidate(pods, 0)
	assert.Equal(t, 2, idx)
}

func TestMultiReplicaRollingUpdate_SkipsMasterFirst(t *testing.T) {
	// Scenario: 3-replica cluster without Sentinel, all pods need updating.
	// Pod-0 is master. The rolling update should delete a replica first, not the master.
	v := newTestValkey("mr", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:9.0"
	})

	pod0 := createPodForSts(v, 0, "valkey/valkey:8.0", true)
	pod0.Labels[common.LabelInstanceRole] = common.RoleMaster
	pod1 := createPodForSts(v, 1, "valkey/valkey:8.0", true)
	pod1.Labels[common.LabelInstanceRole] = common.RoleReplica
	pod2 := createPodForSts(v, 2, "valkey/valkey:8.0", true)
	pod2.Labels[common.LabelInstanceRole] = common.RoleReplica

	r, c := newTestReconciler(v, pod0, pod1, pod2)
	reconcileOnce(t, r, "mr", "default")

	// Now update the spec image to 9.0.
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "mr", Namespace: "default"}, v))
	v.Spec.Image = "valkey/valkey:9.0"
	require.NoError(t, c.Update(context.Background(), v))

	// Reconcile — should delete a replica, not the master.
	reconcileOnce(t, r, "mr", "default")

	// Pod-0 (master) should still exist.
	masterPod := &corev1.Pod{}
	err := c.Get(context.Background(), types.NamespacedName{Name: "mr-0", Namespace: "default"}, masterPod)
	assert.NoError(t, err, "Master pod should NOT be deleted first")

	// At least one replica should be deleted.
	replicaExists := 0
	for i := 1; i <= 2; i++ {
		p := &corev1.Pod{}
		if err := c.Get(context.Background(), types.NamespacedName{Name: fmt.Sprintf("mr-%d", i), Namespace: "default"}, p); err == nil {
			replicaExists++
		}
	}
	assert.Less(t, replicaExists, 2, "At least one replica should have been deleted")
}

func TestMultiReplicaRollingUpdate_RoutesToMultiReplicaHandler(t *testing.T) {
	// Verify that multi-replica without sentinel routes to handleMultiReplicaRollingUpdate
	// (not handleStandaloneRollingUpdate) by checking that master pod survives the first reconcile.
	v := newTestValkey("route-test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 2
		v.Spec.Image = "valkey/valkey:9.0"
	})

	pod0 := createPodForSts(v, 0, "valkey/valkey:8.0", true)
	pod0.Labels[common.LabelInstanceRole] = common.RoleMaster
	pod1 := createPodForSts(v, 1, "valkey/valkey:8.0", true)
	pod1.Labels[common.LabelInstanceRole] = common.RoleReplica

	r, c := newTestReconciler(v, pod0, pod1)
	reconcileOnce(t, r, "route-test", "default")

	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "route-test", Namespace: "default"}, v))
	v.Spec.Image = "valkey/valkey:9.0"
	require.NoError(t, c.Update(context.Background(), v))

	reconcileOnce(t, r, "route-test", "default")

	// Master pod-0 should still exist.
	masterPod := &corev1.Pod{}
	err := c.Get(context.Background(), types.NamespacedName{Name: "route-test-0", Namespace: "default"}, masterPod)
	assert.NoError(t, err, "Master pod should still exist when using multi-replica handler")

	// Replica pod-1 should have been deleted.
	replicaPod := &corev1.Pod{}
	err = c.Get(context.Background(), types.NamespacedName{Name: "route-test-1", Namespace: "default"}, replicaPod)
	assert.Error(t, err, "Replica pod should be deleted first")
}

func TestFinalizeMultiReplicaRollingUpdate_ClearsState(t *testing.T) {
	v := newTestValkey("finalize-mr", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Annotations = map[string]string{
			annotationRollingUpdateState: stateReplacingReplicas,
		}
	})
	r, _ := newTestReconciler(v)

	pods := []podState{
		{name: "pod-0", exists: true, ready: true, needsUpdate: false, isMaster: true},
		{name: "pod-1", exists: true, ready: true, needsUpdate: false, isMaster: false},
		{name: "pod-2", exists: true, ready: true, needsUpdate: false, isMaster: false},
	}

	result := r.finalizeMultiReplicaRollingUpdate(context.Background(), v, pods)
	assert.True(t, result.Completed)
	assert.Nil(t, result.Error)
}

func TestFinalizeMultiReplicaRollingUpdate_WaitsForTopologyRestoration(t *testing.T) {
	v := newTestValkey("finalize-wait", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Annotations = map[string]string{
			annotationRollingUpdateState: stateRestoringTopology,
		}
	})
	r, _ := newTestReconciler(v)

	pods := []podState{}
	result := r.finalizeMultiReplicaRollingUpdate(context.Background(), v, pods)
	// State-machine transitions (stateRestoringTopology, stateManualFailover, etc.)
	// are now dispatched directly in handleMultiReplicaRollingUpdate before reaching
	// finalizeMultiReplicaRollingUpdate. If finalize is called with a leftover state,
	// it clears the state and completes.
	assert.True(t, result.Completed)
	assert.Nil(t, result.Error)
}

// --- Graceful Rolling Update Unit Tests ---

func TestSortReplicaCandidates_YoungestFirst(t *testing.T) {
	now := time.Now()
	pods := []podState{
		{name: "pod-0", exists: true, needsUpdate: true, isMaster: false,
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(now.Add(-10 * time.Minute))}}},
		{name: "pod-1", exists: true, needsUpdate: true, isMaster: false,
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(now.Add(-2 * time.Minute))}}},
		{name: "pod-2", exists: true, needsUpdate: true, isMaster: false,
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(now.Add(-5 * time.Minute))}}},
	}

	candidates := sortReplicaCandidates(pods)

	require.Len(t, candidates, 3)
	assert.Equal(t, "pod-1", candidates[0].name, "Youngest pod should be first")
	assert.Equal(t, "pod-2", candidates[1].name, "Middle-aged pod should be second")
	assert.Equal(t, "pod-0", candidates[2].name, "Oldest pod should be last")
}

func TestSortReplicaCandidates_NonExistentFirst(t *testing.T) {
	now := time.Now()
	pods := []podState{
		{name: "pod-0", exists: true, needsUpdate: true, isMaster: false,
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(now)}}},
		{name: "pod-1", exists: false, needsUpdate: true, isMaster: false},
		{name: "pod-2", exists: true, needsUpdate: true, isMaster: false,
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(now.Add(-5 * time.Minute))}}},
	}

	candidates := sortReplicaCandidates(pods)

	require.Len(t, candidates, 3)
	assert.Equal(t, "pod-1", candidates[0].name, "Non-existent pod should be first")
}

func TestSortReplicaCandidates_ExcludesMaster(t *testing.T) {
	now := time.Now()
	pods := []podState{
		{name: "pod-0", exists: true, needsUpdate: true, isMaster: true,
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(now)}}},
		{name: "pod-1", exists: true, needsUpdate: true, isMaster: false,
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(now)}}},
	}

	candidates := sortReplicaCandidates(pods)

	require.Len(t, candidates, 1)
	assert.Equal(t, "pod-1", candidates[0].name, "Only non-master pods should be candidates")
}

func TestSortReplicaCandidates_ExcludesAlreadyUpdated(t *testing.T) {
	now := time.Now()
	pods := []podState{
		{name: "pod-0", exists: true, needsUpdate: false, isMaster: false,
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(now)}}},
		{name: "pod-1", exists: true, needsUpdate: true, isMaster: false,
			pod: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(now)}}},
	}

	candidates := sortReplicaCandidates(pods)

	require.Len(t, candidates, 1)
	assert.Equal(t, "pod-1", candidates[0].name, "Only pods needing update should be candidates")
}

func TestVerifyReplacedReplicasSynced_AllSynced(t *testing.T) {
	v := newTestValkey("sync-test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})
	r, _ := newTestReconciler(v)
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{
				Role:                 "slave",
				MasterSyncInProgress: false,
				MasterLinkStatus:     "up",
			}, nil
		},
	}

	pods := []podState{
		{name: "sync-test-0", exists: true, ready: true, needsUpdate: false, isMaster: true},
		{name: "sync-test-1", exists: true, ready: true, needsUpdate: false, isMaster: false},
		{name: "sync-test-2", exists: true, ready: true, needsUpdate: true, isMaster: false},
	}

	result := r.verifyReplacedReplicasSynced(context.Background(), v, pods)
	assert.Nil(t, result, "Should return nil when all replaced replicas are synced")
}

func TestVerifyReplacedReplicasSynced_SyncInProgress(t *testing.T) {
	v := newTestValkey("sync-test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})
	r, _ := newTestReconciler(v)
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{
				Role:                 "slave",
				MasterSyncInProgress: true,
				MasterLinkStatus:     "up",
			}, nil
		},
	}

	pods := []podState{
		{name: "sync-test-0", exists: true, ready: true, needsUpdate: false, isMaster: true},
		{name: "sync-test-1", exists: true, ready: true, needsUpdate: false, isMaster: false},
		{name: "sync-test-2", exists: true, ready: true, needsUpdate: true, isMaster: false},
	}

	result := r.verifyReplacedReplicasSynced(context.Background(), v, pods)
	require.NotNil(t, result, "Should return requeue when sync is in progress")
	assert.True(t, result.NeedsRequeue)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
}

func TestVerifyReplacedReplicasSynced_SkipsMasterAndPendingPods(t *testing.T) {
	v := newTestValkey("sync-test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})
	r, _ := newTestReconciler(v)
	// Default mock returns error — should not matter because all pods are skipped.

	pods := []podState{
		{name: "sync-test-0", exists: true, ready: true, needsUpdate: false, isMaster: true},    // master: skip
		{name: "sync-test-1", exists: true, ready: true, needsUpdate: true, isMaster: false},    // needs update: skip
		{name: "sync-test-2", exists: false, ready: false, needsUpdate: false, isMaster: false}, // not exists: skip
	}

	result := r.verifyReplacedReplicasSynced(context.Background(), v, pods)
	assert.Nil(t, result, "Should return nil when no pods qualify for sync check")
}

func TestVerifyReplacedReplicasSynced_NotReadyBlocksNextDeletion(t *testing.T) {
	v := newTestValkey("sync-notready", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})
	r, _ := newTestReconciler(v)

	pods := []podState{
		{name: "sync-notready-0", exists: true, ready: true, needsUpdate: false, isMaster: true},
		// Pod-1 was replaced (needsUpdate=false) but is not yet ready — must block.
		{name: "sync-notready-1", exists: true, ready: false, needsUpdate: false, isMaster: false},
		{name: "sync-notready-2", exists: true, ready: true, needsUpdate: true, isMaster: false},
	}

	result := r.verifyReplacedReplicasSynced(context.Background(), v, pods)
	require.NotNil(t, result, "Should return requeue when a replaced pod is not yet ready")
	assert.True(t, result.NeedsRequeue)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
}

func TestVerifyReplacedReplicasSynced_ErrorRequeues(t *testing.T) {
	v := newTestValkey("sync-err", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})
	r, _ := newTestReconciler(v)
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	pods := []podState{
		{name: "sync-err-0", exists: true, ready: true, needsUpdate: false, isMaster: true},
		{name: "sync-err-1", exists: true, ready: true, needsUpdate: false, isMaster: false},
		{name: "sync-err-2", exists: true, ready: true, needsUpdate: true, isMaster: false},
	}

	result := r.verifyReplacedReplicasSynced(context.Background(), v, pods)
	require.NotNil(t, result, "Should return requeue when GetReplicationInfo fails")
	assert.True(t, result.NeedsRequeue)
	assert.Equal(t, rollingUpdateRequeueDelay, result.RequeueAfter)
	// Verify that the sync-wait-started annotation was set.
	assert.NotEmpty(t, v.Annotations[annotationSyncWaitStarted])
}

func TestVerifyReplacedReplicasSynced_TimeoutPausesUpdate(t *testing.T) {
	v := newTestValkey("sync-timeout", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		// Set the sync-wait-started annotation to a time in the past (>5 min default timeout).
		v.Annotations = map[string]string{
			annotationSyncWaitStarted: time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339),
		}
	})
	r, _ := newTestReconciler(v)
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	pods := []podState{
		{name: "sync-timeout-0", exists: true, ready: true, needsUpdate: false, isMaster: true},
		{name: "sync-timeout-1", exists: true, ready: true, needsUpdate: false, isMaster: false},
		{name: "sync-timeout-2", exists: true, ready: true, needsUpdate: true, isMaster: false},
	}

	result := r.verifyReplacedReplicasSynced(context.Background(), v, pods)
	require.NotNil(t, result, "Should return a pause result on timeout")
	// Paused: no requeue (operator waits for spec change).
	assert.False(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)
}

func TestSyncWaitTimestamp_SetAndCheck(t *testing.T) {
	v := newTestValkey("ts-test", "default")
	r, c := newTestReconciler(v)
	reconcileOnce(t, r, "ts-test", "default")
	// The stored copy, so the arming write below actually lands — ensureWaitBound
	// takes a rejected annotation back off the object (ADR 0010 D7/D8).
	v = crGet(t, c, "ts-test")

	// Initially not timed out.
	assert.False(t, r.isSyncWaitTimedOut(v))

	// Set a timestamp.
	r.ensureSyncWaitTimestamp(context.Background(), v)
	assert.NotEmpty(t, v.Annotations[annotationSyncWaitStarted])

	// Should not be timed out immediately.
	assert.False(t, r.isSyncWaitTimedOut(v))

	// Simulate a timestamp in the past beyond the default 5 min timeout.
	v.Annotations[annotationSyncWaitStarted] = time.Now().Add(-6 * time.Minute).UTC().Format(time.RFC3339)
	assert.True(t, r.isSyncWaitTimedOut(v))

	// Clear the timestamp.
	r.clearSyncWaitTimestamp(context.Background(), v)
	_, exists := v.Annotations[annotationSyncWaitStarted]
	assert.False(t, exists, "Sync wait annotation should be removed")
}

func TestSyncWaitTimestamp_CorruptedTreatedAsTimedOut(t *testing.T) {
	v := newTestValkey("ts-corrupt", "default", func(v *vkov1.Valkey) {
		v.Annotations = map[string]string{
			annotationSyncWaitStarted: "not-a-valid-timestamp",
		}
	})
	r, _ := newTestReconciler(v)

	assert.True(t, r.isSyncWaitTimedOut(v), "Corrupted timestamp should be treated as timed out")
}

func TestGetSyncTimeout_Default(t *testing.T) {
	v := newTestValkey("timeout-default", "default")
	assert.Equal(t, 5*time.Minute, v.GetSyncTimeout())
}

func TestGetSyncTimeout_Custom(t *testing.T) {
	v := newTestValkey("timeout-custom", "default", func(v *vkov1.Valkey) {
		v.Spec.RollingUpdate = &vkov1.RollingUpdateSpec{
			SyncTimeout: &metav1.Duration{Duration: 10 * time.Minute},
		}
	})
	assert.Equal(t, 10*time.Minute, v.GetSyncTimeout())
}

func TestReplaceNextReplica_DeletesYoungestFirst(t *testing.T) {
	v := newTestValkey("youngest", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:9.0"
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	now := time.Now()
	// pod-1 is older than pod-2 — pod-2 should be deleted first.
	pod0 := createPodForSts(v, 0, "valkey/valkey:8.0", true)
	pod0.Labels[common.LabelInstanceRole] = common.RoleMaster
	pod1 := createPodForSts(v, 1, "valkey/valkey:8.0", true)
	pod1.CreationTimestamp = metav1.NewTime(now.Add(-10 * time.Minute))
	pod2 := createPodForSts(v, 2, "valkey/valkey:8.0", true)
	pod2.CreationTimestamp = metav1.NewTime(now.Add(-2 * time.Minute))

	r, c := newTestReconciler(v, pod0, pod1, pod2)
	reconcileOnce(t, r, "youngest", "default")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "youngest", Namespace: "default"}, sts))

	result := r.handleRollingUpdate(context.Background(), v, sts)
	assert.True(t, result.NeedsRequeue)
	assert.Nil(t, result.Error)

	// pod-2 (youngest) should be deleted; pod-1 (oldest) should still exist.
	pod := &corev1.Pod{}
	err := c.Get(context.Background(), types.NamespacedName{Name: "youngest-2", Namespace: "default"}, pod)
	assert.Error(t, err, "Youngest pod (pod-2) should have been deleted first")

	err = c.Get(context.Background(), types.NamespacedName{Name: "youngest-1", Namespace: "default"}, pod)
	assert.NoError(t, err, "Oldest pod (pod-1) should still exist")
}

func TestReplaceNextReplica_WaitsForSyncBeforeDeleting(t *testing.T) {
	v := newTestValkey("sync-wait", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:9.0"
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	// Pod-0 is master (old), pod-1 is already updated and syncing, pod-2 is old.
	pod0 := createPodForSts(v, 0, "valkey/valkey:8.0", true)
	pod0.Labels[common.LabelInstanceRole] = common.RoleMaster
	pod1 := createPodForSts(v, 1, "valkey/valkey:9.0", true)
	pod2 := createPodForSts(v, 2, "valkey/valkey:8.0", true)

	r, c := newTestReconciler(v, pod0, pod1, pod2)
	// pod-1 is still syncing — should block the next deletion.
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			if podName == "sync-wait-1" {
				return &valkeyclient.ReplicationInfo{
					Role:                 "slave",
					MasterSyncInProgress: true,
					MasterLinkStatus:     "up",
				}, nil
			}
			return nil, fmt.Errorf("mock: no info for %s", podName)
		},
	}
	reconcileOnce(t, r, "sync-wait", "default")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "sync-wait", Namespace: "default"}, sts))

	result := r.handleRollingUpdate(context.Background(), v, sts)
	assert.True(t, result.NeedsRequeue, "Should requeue while waiting for sync")
	assert.Nil(t, result.Error)

	// pod-2 should NOT have been deleted because pod-1 hasn't synced yet.
	pod := &corev1.Pod{}
	err := c.Get(context.Background(), types.NamespacedName{Name: "sync-wait-2", Namespace: "default"}, pod)
	assert.NoError(t, err, "Pod-2 should still exist because sync check blocked deletion")
}

// --- Split-Brain Detection and Resolution Tests ---

func TestDetectAndResolveSplitBrain_NoSplitBrain_SingleMaster(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	r, _ := newTestReconciler(v)
	pods := []podState{
		{name: "test-0", isMaster: false, needsUpdate: true, exists: true, ready: true},
		{name: "test-1", isMaster: true, needsUpdate: true, exists: true, ready: true},
		{name: "test-2", isMaster: false, needsUpdate: false, exists: true, ready: true},
	}

	resultPods, masterIdx := r.detectAndResolveSplitBrain(context.Background(), v, pods, 1, "")
	assert.Equal(t, 1, masterIdx, "Master index should remain unchanged")
	assert.True(t, resultPods[1].isMaster, "Pod-1 should still be master")
	assert.False(t, resultPods[0].isMaster, "Pod-0 should still be non-master")
}

func TestDetectAndResolveSplitBrain_NoSplitBrain_NoMaster(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	r, _ := newTestReconciler(v)
	pods := []podState{
		{name: "test-0", isMaster: false, needsUpdate: true, exists: true, ready: true},
		{name: "test-1", isMaster: false, needsUpdate: true, exists: true, ready: true},
		{name: "test-2", isMaster: false, needsUpdate: false, exists: true, ready: true},
	}

	resultPods, masterIdx := r.detectAndResolveSplitBrain(context.Background(), v, pods, -1, "")
	assert.Equal(t, -1, masterIdx, "Master index should remain -1")
	for _, ps := range resultPods {
		assert.False(t, ps.isMaster, "No pod should be master")
	}
}

func TestDetectAndResolveSplitBrain_TwoMasters_FallbackToConnectedSlaves(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	r, _ := newTestReconciler(v)
	// Mock: pod-0 reports master with 0 slaves, pod-1 reports master with 1 slave.
	// Sentinel is unreachable (unit test, no real sentinel), so fallback to slave count.
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			switch podName {
			case "test-0":
				return &valkeyclient.ReplicationInfo{
					Role:            "master",
					ConnectedSlaves: 0,
				}, nil
			case "test-1":
				return &valkeyclient.ReplicationInfo{
					Role:            "master",
					ConnectedSlaves: 1,
				}, nil
			default:
				return &valkeyclient.ReplicationInfo{
					Role:             "slave",
					MasterLinkStatus: "up",
				}, nil
			}
		},
	}

	pods := []podState{
		{name: "test-0", isMaster: true, needsUpdate: true, exists: true, ready: true,
			pod: createPodForSts(v, 0, "valkey/valkey:8.0", true)},
		{name: "test-1", isMaster: true, needsUpdate: true, exists: true, ready: true,
			pod: createPodForSts(v, 1, "valkey/valkey:8.0", true)},
		{name: "test-2", isMaster: false, needsUpdate: false, exists: true, ready: true,
			pod: createPodForSts(v, 2, "valkey/valkey:9.0", true)},
	}

	resultPods, masterIdx := r.detectAndResolveSplitBrain(context.Background(), v, pods, 1, "")
	assert.Equal(t, 1, masterIdx, "Pod-1 should be identified as the real master (more slaves)")
	assert.True(t, resultPods[1].isMaster, "Pod-1 should remain master")
	assert.False(t, resultPods[0].isMaster, "Pod-0 should be demoted (rogue master)")
}

func TestDetectAndResolveSplitBrain_SentinelBasedResolution(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	r, _ := newTestReconciler(v)
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{Role: "master", ConnectedSlaves: 0}, nil
		},
	}

	pods := []podState{
		{name: "test-0", isMaster: true, needsUpdate: true, exists: true, ready: true,
			pod: createPodForSts(v, 0, "valkey/valkey:8.0", true)},
		{name: "test-1", isMaster: true, needsUpdate: true, exists: true, ready: true,
			pod: createPodForSts(v, 1, "valkey/valkey:8.0", true)},
		{name: "test-2", isMaster: false, needsUpdate: false, exists: true, ready: true},
	}

	// Sentinel says test-0 is the real master — should override connected slaves fallback.
	resultPods, masterIdx := r.detectAndResolveSplitBrain(context.Background(), v, pods, 1, "test-0")
	assert.Equal(t, 0, masterIdx, "Sentinel authority should pick test-0 as real master")
	assert.True(t, resultPods[0].isMaster, "test-0 should remain master (sentinel authority)")
	assert.False(t, resultPods[1].isMaster, "test-1 should be demoted (rogue)")
}

func TestDetectAndResolveSplitBrain_ThreeMasters(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	r, _ := newTestReconciler(v)
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			switch podName {
			case "test-0":
				return &valkeyclient.ReplicationInfo{Role: "master", ConnectedSlaves: 0}, nil
			case "test-1":
				return &valkeyclient.ReplicationInfo{Role: "master", ConnectedSlaves: 0}, nil
			case "test-2":
				return &valkeyclient.ReplicationInfo{Role: "master", ConnectedSlaves: 0}, nil
			default:
				return nil, fmt.Errorf("unknown pod")
			}
		},
	}

	pods := []podState{
		{name: "test-0", isMaster: true, needsUpdate: true, exists: true, ready: true,
			pod: createPodForSts(v, 0, "valkey/valkey:8.0", true)},
		{name: "test-1", isMaster: true, needsUpdate: true, exists: true, ready: true,
			pod: createPodForSts(v, 1, "valkey/valkey:8.0", true)},
		{name: "test-2", isMaster: true, needsUpdate: false, exists: true, ready: true,
			pod: createPodForSts(v, 2, "valkey/valkey:9.0", true)},
	}

	resultPods, masterIdx := r.detectAndResolveSplitBrain(context.Background(), v, pods, 2, "")
	assert.GreaterOrEqual(t, masterIdx, 0, "Should have identified a real master")

	masterCount := 0
	for _, ps := range resultPods {
		if ps.isMaster {
			masterCount++
		}
	}
	assert.Equal(t, 1, masterCount, "Exactly one pod should remain master after resolution")
}

func TestDetectAndResolveSplitBrain_ReplicationInfoUnavailable(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	r, _ := newTestReconciler(v)
	// All replication info calls fail — fallback picks the first master index.
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(_ string) (*valkeyclient.ReplicationInfo, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	pods := []podState{
		{name: "test-0", isMaster: true, needsUpdate: true, exists: true, ready: true,
			pod: createPodForSts(v, 0, "valkey/valkey:8.0", true)},
		{name: "test-1", isMaster: true, needsUpdate: true, exists: true, ready: true,
			pod: createPodForSts(v, 1, "valkey/valkey:8.0", true)},
		{name: "test-2", isMaster: false, needsUpdate: false, exists: true, ready: true},
	}

	resultPods, masterIdx := r.detectAndResolveSplitBrain(context.Background(), v, pods, 1, "")
	// When replication info is unavailable, the first master index is chosen.
	assert.GreaterOrEqual(t, masterIdx, 0)

	masterCount := 0
	for _, ps := range resultPods {
		if ps.isMaster {
			masterCount++
		}
	}
	assert.Equal(t, 1, masterCount, "Should resolve to exactly one master")
}

func TestDetectAndResolveSplitBrain_RoguePodNotReady(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	r, _ := newTestReconciler(v)
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			if podName == "test-1" {
				return &valkeyclient.ReplicationInfo{Role: "master", ConnectedSlaves: 1}, nil
			}
			return &valkeyclient.ReplicationInfo{Role: "master", ConnectedSlaves: 0}, nil
		},
	}

	pods := []podState{
		{name: "test-0", isMaster: true, needsUpdate: true, exists: true, ready: false,
			pod: createPodForSts(v, 0, "valkey/valkey:8.0", false)},
		{name: "test-1", isMaster: true, needsUpdate: true, exists: true, ready: true,
			pod: createPodForSts(v, 1, "valkey/valkey:8.0", true)},
		{name: "test-2", isMaster: false, needsUpdate: false, exists: true, ready: true},
	}

	resultPods, masterIdx := r.detectAndResolveSplitBrain(context.Background(), v, pods, 1, "")
	assert.Equal(t, 1, masterIdx, "Pod-1 should be the real master (more slaves)")
	// Pod-0 is marked as non-master even though demoteRogueMaster fails (not ready).
	assert.False(t, resultPods[0].isMaster, "Rogue pod should be marked as non-master")
}

func TestGetSentinelMasterPodName_NoSentinel(t *testing.T) {
	v := newTestValkey("test", "default")
	// No sentinel configured.
	r, _ := newTestReconciler(v)

	result := r.getSentinelMasterPodName(context.Background(), v)
	assert.Equal(t, "", result, "Should return empty when sentinel is not enabled")
}

func TestGetSentinelMasterPodName_SentinelUnreachable(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	r, _ := newTestReconciler(v)

	// Sentinels are not reachable in unit tests.
	result := r.getSentinelMasterPodName(context.Background(), v)
	assert.Equal(t, "", result, "Should return empty when sentinels are unreachable")
}

func TestHandleRollingUpdate_HA_SplitBrain_BreaksDeadlock(t *testing.T) {
	// This test reproduces the exact deadlock scenario from the split-brain analysis:
	// - Pod-0 and Pod-1 both report as master (split-brain)
	// - Pod-2 is already updated
	// - Without split-brain detection, replaceNextReplica skips pod-0 (isMaster=true)
	//   and waitForReplicasReady blocks because pod-0 needsUpdate=true
	// - With split-brain detection, the rogue master is demoted and the update proceeds.
	v := newTestValkey("ha-split", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:9.0"
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	// Pod-0: old image, reports master (rogue), 0 connected slaves
	// Pod-1: old image, reports master (real), 1 connected slave
	// Pod-2: new image, ready, reports replica
	pod0 := createPodForSts(v, 0, "valkey/valkey:8.0", true)
	pod0.Labels[common.LabelInstanceRole] = common.RoleMaster
	pod1 := createPodForSts(v, 1, "valkey/valkey:8.0", true)
	pod1.Labels[common.LabelInstanceRole] = common.RoleMaster
	pod2 := createPodForSts(v, 2, "valkey/valkey:9.0", true)

	r, c := newTestReconciler(v, pod0, pod1, pod2)
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			switch podName {
			case "ha-split-0":
				return &valkeyclient.ReplicationInfo{
					Role:            "master",
					ConnectedSlaves: 0,
				}, nil
			case "ha-split-1":
				return &valkeyclient.ReplicationInfo{
					Role:            "master",
					ConnectedSlaves: 1,
				}, nil
			default:
				return &valkeyclient.ReplicationInfo{
					Role:                 "slave",
					MasterSyncInProgress: false,
					MasterLinkStatus:     "up",
				}, nil
			}
		},
	}
	reconcileOnce(t, r, "ha-split", "default")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "ha-split", Namespace: "default"}, sts))

	result := r.handleRollingUpdate(context.Background(), v, sts)
	assert.True(t, result.NeedsRequeue, "Should requeue to continue rolling update")
	assert.Nil(t, result.Error, "Should not error")

	// The rogue master pod (pod-0) should have been deleted because it was
	// demoted to non-master status and became a valid candidate for replaceNextReplica.
	deletedPod0 := false
	pod := &corev1.Pod{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "ha-split-0", Namespace: "default"}, pod); err != nil {
		deletedPod0 = true
	}
	assert.True(t, deletedPod0, "Rogue master pod-0 should be deleted after split-brain resolution")

	// Pod-1 (real master) should still exist.
	err := c.Get(context.Background(), types.NamespacedName{Name: "ha-split-1", Namespace: "default"}, pod)
	assert.NoError(t, err, "Real master pod-1 should still exist")
}

func TestCountMasters_MultipleMasters(t *testing.T) {
	pods := []podState{
		{name: "test-0", isMaster: true},
		{name: "test-1", isMaster: true},
		{name: "test-2", isMaster: false},
	}
	assert.Equal(t, 2, countMasters(pods))
}

func TestCountMasters_NoMasters(t *testing.T) {
	pods := []podState{
		{name: "test-0", isMaster: false},
		{name: "test-1", isMaster: false},
	}
	assert.Equal(t, 0, countMasters(pods))
}

func TestCountMasters_SingleMaster(t *testing.T) {
	pods := []podState{
		{name: "test-0", isMaster: false},
		{name: "test-1", isMaster: true},
		{name: "test-2", isMaster: false},
	}
	assert.Equal(t, 1, countMasters(pods))
}

// --- Split-brain detection in non-sentinel multi-replica path ---

// TestHandleMultiReplicaRollingUpdate_SplitBrainDemotedBeforeUpdate verifies that
// when two pods report "master" in a non-sentinel cluster, the split-brain is detected
// and the rogue master is demoted before the rolling update proceeds. This covers the
// bug where detectAndResolveSplitBrain was only called in the Sentinel path.
//
// Every pod in the fixture is up to date on purpose: detectAndResolveSplitBrain runs at
// the top of handleMultiReplicaRollingUpdate, ahead of the "all pods updated" branch
// that finalizes the update. Moving the call behind that branch would leave the rogue
// master in place, and that regression is what this test pins.
func TestHandleMultiReplicaRollingUpdate_SplitBrainDemotedBeforeUpdate(t *testing.T) {
	// Non-sentinel, 3-replica cluster with split-brain:
	// pod-0 reports master (rogue, 0 slaves), pod-1 reports master (real, 1 slave), pod-2 replica.
	// The pods are built from the persisted StatefulSet template, so the rolling update
	// itself is already complete — the rogue master was left behind by a failed
	// topology restoration.
	v := newTestValkey("mr-split", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:8.0"
	})
	sts := stsForValkey(v)

	pod0 := podFromStsTemplate(v, sts, 0)
	pod0.Labels[common.LabelInstanceRole] = common.RoleMaster
	pod1 := podFromStsTemplate(v, sts, 1)
	pod1.Labels[common.LabelInstanceRole] = common.RoleMaster
	pod2 := podFromStsTemplate(v, sts, 2)

	r, _ := newTestReconciler(v, sts, pod0, pod1, pod2)

	// A server that answers REPLICAOF, so the demotion is exercised for real instead
	// of dying on a refused connection.
	addr := fakeValkeyServer(t)
	var demoted []string
	r.NewValkeyClientFn = func(target, _ string, _ *tls.Config) *valkeyclient.Client {
		demoted = append(demoted, target)
		return valkeyclient.New(addr)
	}
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			switch podName {
			case "mr-split-0":
				return &valkeyclient.ReplicationInfo{Role: "master", ConnectedSlaves: 0}, nil
			case "mr-split-1":
				return &valkeyclient.ReplicationInfo{Role: "master", ConnectedSlaves: 1}, nil
			default:
				return &valkeyclient.ReplicationInfo{
					Role: "slave", MasterLinkStatus: "up",
				}, nil
			}
		},
	}

	result := r.handleMultiReplicaRollingUpdate(context.Background(), v, sts)

	require.Nil(t, result.Error, "Should not return an error")
	assert.True(t, result.Completed, "every pod is up to date, so the update finalizes")

	// The master without connected slaves is the rogue one and the only pod that may
	// be demoted — demoting pod-1 would point the data it serves at an empty master.
	require.Len(t, demoted, 1, "exactly one pod must be demoted")
	assert.Contains(t, demoted[0], "mr-split-0.", "the rogue master must be the demoted pod")
}

// --- Two-phase handleTopologyRestoration ---

// TestHandleTopologyRestoration_Phase1SetsAnnotationAndRequeues verifies that Phase 1
// promotes pod-0, sends REPLICAOF to all other pods, sets the
// annotationTopologyRestoreStarted annotation, and requeues without completing.
func TestHandleTopologyRestoration_Phase1SetsAnnotationAndRequeues(t *testing.T) {
	v := newTestValkey("topo-p1", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:8.0"
	})

	pod0 := createPodForSts(v, 0, "valkey/valkey:8.0", true)
	pod1 := createPodForSts(v, 1, "valkey/valkey:8.0", true)
	pod2 := createPodForSts(v, 2, "valkey/valkey:8.0", true)

	r, c := newTestReconciler(v, pod0, pod1, pod2)
	reconcileOnce(t, r, "topo-p1", "default")

	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "topo-p1", Namespace: "default"}, v))
	v.Annotations = map[string]string{
		annotationRollingUpdateState: stateRestoringTopology,
		annotationPromotedPod:        "topo-p1-1",
	}
	require.NoError(t, c.Update(context.Background(), v))

	// Phase 1 only reaches stateVerifyingTopology once REPLICAOF NO ONE on pod-0
	// succeeded; a refused connection correctly keeps the state at
	// stateRestoringTopology, so the fixture needs a server that answers.
	addr := fakeValkeyServer(t)
	r.NewValkeyClientFn = func(_, _ string, _ *tls.Config) *valkeyclient.Client {
		return valkeyclient.New(addr)
	}

	// pod-0 is fully synced as replica of pod-1 and ready for promotion.
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			if podName == "topo-p1-0" {
				return &valkeyclient.ReplicationInfo{
					Role:                 "slave",
					MasterLinkStatus:     "up",
					MasterSyncInProgress: false,
				}, nil
			}
			return &valkeyclient.ReplicationInfo{Role: "slave", MasterLinkStatus: "up"}, nil
		},
	}

	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "topo-p1", Namespace: "default"}, sts))

	result := r.handleTopologyRestoration(context.Background(), v, sts)

	// Phase 1 should requeue, not complete.
	assert.True(t, result.NeedsRequeue, "Phase 1 should requeue")
	assert.False(t, result.Completed, "Phase 1 should not complete")
	assert.Nil(t, result.Error)

	// After Phase 1, the state must have transitioned to stateVerifyingTopology.
	updated := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "topo-p1", Namespace: "default"}, updated))
	assert.Equal(t, stateVerifyingTopology, updated.Annotations[annotationRollingUpdateState],
		"rolling update state must be stateVerifyingTopology after Phase 1")
}

// TestVerifyTopologyRestored_CleanTopologyCompletes verifies that when
// stateVerifyingTopology is active and all replicas are connected to pod-0 (no
// rogue masters), verifyTopologyRestored completes and clears all rolling update state.
func TestVerifyTopologyRestored_CleanTopologyCompletes(t *testing.T) {
	v := newTestValkey("topo-p2", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:8.0"
	})

	pod0 := createPodForSts(v, 0, "valkey/valkey:8.0", true)
	pod0.Labels[common.LabelInstanceRole] = common.RoleMaster
	pod1 := createPodForSts(v, 1, "valkey/valkey:8.0", true)
	pod2 := createPodForSts(v, 2, "valkey/valkey:8.0", true)

	r, c := newTestReconciler(v, pod0, pod1, pod2)
	reconcileOnce(t, r, "topo-p2", "default")

	// Set annotations after initial reconcile (simulates mid-rolling-update state).
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "topo-p2", Namespace: "default"}, v))
	v.Annotations = map[string]string{
		annotationRollingUpdateState: stateVerifyingTopology,
	}
	require.NoError(t, c.Update(context.Background(), v))

	// Only pod-0 reports master — clean topology, no split-brain.
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			if podName == "topo-p2-0" {
				return &valkeyclient.ReplicationInfo{Role: "master", ConnectedSlaves: 2}, nil
			}
			return &valkeyclient.ReplicationInfo{Role: "slave", MasterLinkStatus: "up"}, nil
		},
	}

	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "topo-p2", Namespace: "default"}, sts))

	result := r.verifyTopologyRestored(context.Background(), v, sts)

	assert.True(t, result.Completed, "Clean topology should complete")
	assert.Nil(t, result.Error)

	// All rolling update state must be cleared.
	updated := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "topo-p2", Namespace: "default"}, updated))
	assert.Empty(t, updated.Annotations[annotationRollingUpdateState], "rolling update state annotation should be cleared")
}

// TestVerifyTopologyRestored_RogueMasterRetriedThenStall verifies that when a rogue
// master is still present in Phase 2, verifyTopologyRestored retries REPLICAOF
// (via detectAndResolveSplitBrain) and requeues — until finalizationStallTimeout,
// after which it clears state and completes to prevent an indefinite stall.
func TestVerifyTopologyRestored_RogueMasterRetriedThenStall(t *testing.T) {
	v := newTestValkey("topo-stall", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:8.0"
	})

	pod0 := createPodForSts(v, 0, "valkey/valkey:8.0", true)
	pod0.Labels[common.LabelInstanceRole] = common.RoleMaster
	pod1 := createPodForSts(v, 1, "valkey/valkey:8.0", true)
	pod1.Labels[common.LabelInstanceRole] = common.RoleMaster // rogue master
	pod2 := createPodForSts(v, 2, "valkey/valkey:8.0", true)

	r, c := newTestReconciler(v, pod0, pod1, pod2)
	reconcileOnce(t, r, "topo-stall", "default")

	// Set stateVerifyingTopology with a stale finalization timestamp to simulate stall.
	stalledTime := time.Now().UTC().Add(-finalizationStallTimeout - time.Minute)
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "topo-stall", Namespace: "default"}, v))
	v.Annotations = map[string]string{
		annotationRollingUpdateState:    stateVerifyingTopology,
		annotationFinalizationTimestamp: stalledTime.Format(time.RFC3339),
	}
	require.NoError(t, c.Update(context.Background(), v))

	// Pod-0 and pod-1 both report master — split-brain persists.
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			switch podName {
			case "topo-stall-0":
				return &valkeyclient.ReplicationInfo{Role: "master", ConnectedSlaves: 1}, nil
			case "topo-stall-1":
				return &valkeyclient.ReplicationInfo{Role: "master", ConnectedSlaves: 0}, nil
			default:
				return &valkeyclient.ReplicationInfo{Role: "slave", MasterLinkStatus: "up"}, nil
			}
		},
	}

	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "topo-stall", Namespace: "default"}, sts))

	result := r.verifyTopologyRestored(context.Background(), v, sts)

	// After stall timeout, should complete despite rogue master.
	assert.True(t, result.Completed, "Should complete after stall timeout even with rogue master")
	assert.Nil(t, result.Error)

	// All rolling update state must be cleared.
	updated := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "topo-stall", Namespace: "default"}, updated))
	assert.Empty(t, updated.Annotations[annotationRollingUpdateState], "rolling update state should be cleared on stall")
	assert.Empty(t, updated.Annotations[annotationFinalizationTimestamp], "finalization timestamp should be cleared on stall")
}

// TestVerifyTopologyRestored_RequeuesWhenRogueMasterPresent verifies that before the
// stall timeout, verifyTopologyRestored requeues rather than completing when a rogue
// master is still present after a REPLICAOF attempt.
func TestVerifyTopologyRestored_RequeuesWhenRogueMasterPresent(t *testing.T) {
	v := newTestValkey("topo-retry", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:8.0"
	})

	pod0 := createPodForSts(v, 0, "valkey/valkey:8.0", true)
	pod0.Labels[common.LabelInstanceRole] = common.RoleMaster
	pod1 := createPodForSts(v, 1, "valkey/valkey:8.0", true)
	pod1.Labels[common.LabelInstanceRole] = common.RoleMaster // rogue master
	pod2 := createPodForSts(v, 2, "valkey/valkey:8.0", true)

	r, c := newTestReconciler(v, pod0, pod1, pod2)
	reconcileOnce(t, r, "topo-retry", "default")

	// No finalization timestamp → not stalled yet.
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "topo-retry", Namespace: "default"}, v))
	v.Annotations = map[string]string{
		annotationRollingUpdateState: stateVerifyingTopology,
	}
	require.NoError(t, c.Update(context.Background(), v))

	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			switch podName {
			case "topo-retry-0":
				return &valkeyclient.ReplicationInfo{Role: "master", ConnectedSlaves: 1}, nil
			case "topo-retry-1":
				return &valkeyclient.ReplicationInfo{Role: "master", ConnectedSlaves: 0}, nil
			default:
				return &valkeyclient.ReplicationInfo{Role: "slave", MasterLinkStatus: "up"}, nil
			}
		},
	}

	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "topo-retry", Namespace: "default"}, sts))

	result := r.verifyTopologyRestored(context.Background(), v, sts)

	// Before stall timeout: should requeue, not complete.
	assert.False(t, result.Completed, "Should not complete while rogue master present and not stalled")
	assert.True(t, result.NeedsRequeue, "Should requeue to retry")
	assert.Nil(t, result.Error)
}

// TestIsFinalizationStalled_UsedForTopologyVerification verifies that the existing
// isFinalizationStalled helper correctly identifies a stalled topology verification —
// no separate helper is needed since the same annotation and timeout are reused.
func TestIsFinalizationStalled_UsedForTopologyVerification(t *testing.T) {
	r, _ := newTestReconciler()
	v := newTestValkey("ts", "default")

	// No annotation → not stalled.
	assert.False(t, r.isFinalizationStalled(v))

	// Fresh annotation → not stalled.
	v.Annotations = map[string]string{
		annotationFinalizationTimestamp: time.Now().UTC().Format(time.RFC3339),
	}
	assert.False(t, r.isFinalizationStalled(v))

	// Old annotation → stalled.
	v.Annotations[annotationFinalizationTimestamp] = time.Now().UTC().
		Add(-finalizationStallTimeout - time.Second).Format(time.RFC3339)
	assert.True(t, r.isFinalizationStalled(v))
}

// --- findPromotionCandidate excludes pod-0 ---

// TestFindPromotionCandidate_SkipsPod0 verifies that pod-0 is never selected as the
// temporary master even when the current master is a higher-ordinal pod. Promoting
// pod-0 would cause handlePostManualFailover to send REPLICAOF <pod-0> to pod-0 itself
// (self-loop), leaving master_link_status permanently "down".
func TestFindPromotionCandidate_SkipsPod0(t *testing.T) {
	// masterIdx=2 (pod-2 is master); pod-0 and pod-1 are updated replicas.
	pods := []podState{
		{name: "test-0", isMaster: false, needsUpdate: false, ready: true, exists: true},
		{name: "test-1", isMaster: false, needsUpdate: false, ready: true, exists: true},
		{name: "test-2", isMaster: true, needsUpdate: true, ready: true, exists: true},
	}
	candidate := findPromotionCandidate(pods, 2)
	assert.Equal(t, 1, candidate, "Should pick pod-1, never pod-0")
}

func TestFindPromotionCandidate_Pod0IsOnlyCandidate_ReturnsNegative(t *testing.T) {
	// Only pod-0 is available as a non-master updated replica → no valid candidate.
	pods := []podState{
		{name: "test-0", isMaster: false, needsUpdate: false, ready: true, exists: true},
		{name: "test-1", isMaster: true, needsUpdate: true, ready: true, exists: true},
	}
	candidate := findPromotionCandidate(pods, 1)
	assert.Equal(t, -1, candidate, "Should return -1 when only pod-0 is available")
}

// --- Self-loop recovery in handleTopologyRestoration ---

// TestHandleTopologyRestoration_SelfLoopRecovery verifies that when the promoted pod
// annotation equals pod-0 (the self-loop bug), handleTopologyRestoration skips the
// sync-wait and directly promotes pod-0 via REPLICAOF NO ONE, then transitions to
// stateVerifyingTopology. Without this guard the operator waits forever for
// master_link_status=up on a self-referencing REPLICAOF.
func TestHandleTopologyRestoration_SelfLoopRecovery(t *testing.T) {
	v := newTestValkey("selfloop", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Image = "valkey/valkey:8.0"
	})

	pod0 := createPodForSts(v, 0, "valkey/valkey:8.0", true)
	pod1 := createPodForSts(v, 1, "valkey/valkey:8.0", true)
	pod2 := createPodForSts(v, 2, "valkey/valkey:8.0", true)

	r, c := newTestReconciler(v, pod0, pod1, pod2)
	reconcileOnce(t, r, "selfloop", "default")

	// Simulate the self-loop state: promoted pod == pod-0.
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "selfloop", Namespace: "default"}, v))
	v.Annotations = map[string]string{
		annotationRollingUpdateState: stateRestoringTopology,
		annotationPromotedPod:        "selfloop-0", // self-loop: pod-0 was promoted
	}
	require.NoError(t, c.Update(context.Background(), v))

	// Phase 1 only reaches stateVerifyingTopology once REPLICAOF NO ONE on pod-0
	// succeeded; a refused connection correctly keeps the state at
	// stateRestoringTopology, so the fixture needs a server that answers.
	addr := fakeValkeyServer(t)
	r.NewValkeyClientFn = func(_, _ string, _ *tls.Config) *valkeyclient.Client {
		return valkeyclient.New(addr)
	}

	// GetReplicationInfo returns role=slave, link=down (the stuck self-loop state).
	r.InstanceChecker = &mockInstanceChecker{
		replicationInfoFn: func(podName string) (*valkeyclient.ReplicationInfo, error) {
			return &valkeyclient.ReplicationInfo{
				Role:             "slave",
				MasterLinkStatus: "down",
			}, nil
		},
	}

	sts := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(context.Background(), types.NamespacedName{Name: "selfloop", Namespace: "default"}, sts))

	result := r.handleTopologyRestoration(context.Background(), v, sts)

	// Must requeue (transition to stateVerifyingTopology), not complete, not error.
	assert.True(t, result.NeedsRequeue, "Should requeue after self-loop recovery")
	assert.False(t, result.Completed, "Should not complete in Phase 1")
	assert.Nil(t, result.Error)

	// State must have advanced to stateVerifyingTopology.
	updated := &vkov1.Valkey{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "selfloop", Namespace: "default"}, updated))
	assert.Equal(t, stateVerifyingTopology, updated.Annotations[annotationRollingUpdateState],
		"State should advance to stateVerifyingTopology after self-loop recovery")
}
