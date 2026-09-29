package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

// The tests in this file pin the image input of both tiers
// (docs/adr/0007-failover-aware-rolling-update.md, D2): a pod is outdated when any
// container or init container runs an image other than the one the persisted
// template gives the container of the same name. Until this rule the data tier
// compared the valkey and the sidecar image only, so an exporter or init-container
// image that differed from the template was never replaced.
//
// Mutation checks: dropping podImagesDrifted from podOutdated fails
// TestPodOutdated_ComparesEveryContainerAndInitContainerImage and
// TestCheckAndHandleRollingUpdate_ReplacesAStandalonePodWithAnExporterImageDrift;
// dropping the init-container half of podImagesDrifted fails the init rows of
// TestPodImagesDrifted, the init subtest of the podOutdated test and
// TestSentinelPodNeedsUpdate_ComparesInitContainerImages.

const driftedImage = "registry.example/not-the-template:1"

// podWithTemplateInits is podFromStsTemplate plus the template's init containers:
// a pod the statefulset-controller builds carries both lists.
func podWithTemplateInits(v *vkov1.Valkey, sts *appsv1.StatefulSet, ordinal int) *corev1.Pod {
	pod := podFromStsTemplate(v, sts, ordinal)
	for _, c := range sts.Spec.Template.Spec.InitContainers {
		pod.Spec.InitContainers = append(pod.Spec.InitContainers, corev1.Container{Name: c.Name, Image: c.Image})
	}
	return pod
}

// setImage replaces the image of the named container in either list and fails the
// test when the pod has no container of that name, so a fixture that drifted from
// the builder cannot pass as "no drift found".
func setImage(t *testing.T, pod *corev1.Pod, name, image string) {
	t.Helper()
	for _, list := range [][]corev1.Container{pod.Spec.InitContainers, pod.Spec.Containers} {
		for i := range list {
			if list[i].Name == name {
				list[i].Image = image
				return
			}
		}
	}
	t.Fatalf("pod %s has no container %q", pod.Name, name)
}

func TestPodImagesDrifted(t *testing.T) {
	tmpl := &corev1.PodSpec{
		InitContainers: []corev1.Container{{Name: "init-config-selector", Image: "valkey/valkey:9.0"}},
		Containers: []corev1.Container{
			{Name: builder.ValkeyContainerName, Image: "valkey/valkey:9.0"},
			{Name: builder.SidecarContainerName, Image: desiredSidecar},
			{Name: builder.ExporterContainerName, Image: "exporter:1"},
		},
	}
	for _, tc := range []struct {
		name   string
		mutate func(*corev1.PodSpec)
		want   bool
	}{
		{name: "a pod built from the template", want: false},
		{
			name:   "the exporter image differs",
			mutate: func(s *corev1.PodSpec) { s.Containers[2].Image = driftedImage },
			want:   true,
		},
		{
			name:   "the valkey image differs",
			mutate: func(s *corev1.PodSpec) { s.Containers[0].Image = driftedImage },
			want:   true,
		},
		{
			name:   "an init container image differs",
			mutate: func(s *corev1.PodSpec) { s.InitContainers[0].Image = driftedImage },
			want:   true,
		},
		{
			name: "a container the template does not carry is skipped",
			mutate: func(s *corev1.PodSpec) {
				s.InitContainers = append(s.InitContainers,
					corev1.Container{Name: builder.DataOwnershipRepairContainerName, Image: driftedImage})
			},
			want: false,
		},
		{
			name:   "a container the pod does not carry is skipped",
			mutate: func(s *corev1.PodSpec) { s.Containers = s.Containers[:2] },
			want:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := tmpl.DeepCopy()
			if tc.mutate != nil {
				tc.mutate(spec)
			}
			assert.Equal(t, tc.want, podImagesDrifted(&corev1.Pod{Spec: *spec}, tmpl))
		})
	}
}

// metricsCluster is a persistent multi-replica cluster with the exporter on, so
// its data template carries the exporter and two init containers
// (check-data-writable, init-config-selector).
func metricsCluster(name string, replicas int32) (*vkov1.Valkey, *appsv1.StatefulSet) {
	v := newTestValkey(name, "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = replicas
		v.Spec.Persistence = &vkov1.PersistenceSpec{Enabled: true}
		v.Spec.Metrics = &vkov1.MetricsSpec{Enabled: true}
	})
	return v, stsForValkey(v)
}

func TestPodOutdated_ComparesEveryContainerAndInitContainerImage(t *testing.T) {
	v, sts := metricsCluster("drift", 3)
	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers, "fixture: the template must carry init containers")

	t.Run("a pod built from the template is current", func(t *testing.T) {
		assert.False(t, podOutdated(podWithTemplateInits(v, sts, 1), sts))
	})
	t.Run("the exporter image differs", func(t *testing.T) {
		pod := podWithTemplateInits(v, sts, 1)
		setImage(t, pod, builder.ExporterContainerName, driftedImage)
		assert.True(t, podOutdated(pod, sts))
	})
	for _, init := range sts.Spec.Template.Spec.InitContainers {
		t.Run("the init container "+init.Name+" image differs", func(t *testing.T) {
			pod := podWithTemplateInits(v, sts, 1)
			setImage(t, pod, init.Name, driftedImage)
			assert.True(t, podOutdated(pod, sts))
		})
	}
	t.Run("the ownership repair is not an image drift", func(t *testing.T) {
		repaired := sts.DeepCopy()
		builder.WithDataOwnershipRepair(repaired)
		pod := podWithTemplateInits(v, repaired, 1)
		require.True(t, builder.HasDataOwnershipRepair(&pod.Spec))
		assert.False(t, podOutdated(pod, repaired), "a pod built while the template carries the repair is current")
		// Once the template drops it the pod is outdated, but on its own question
		// (podCarriesRetiredRepair), not as an image drift.
		assert.False(t, podImagesDrifted(pod, &sts.Spec.Template.Spec))
		assert.True(t, podOutdated(pod, sts))
	})
}

func TestSentinelPodNeedsUpdate_ComparesInitContainerImages(t *testing.T) {
	v := newTestValkey("drift", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	sts := builder.BuildSentinelStatefulSet(v)
	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers, "fixture: the Sentinel template must carry an init container")

	build := func() *corev1.Pod {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name:        sts.Name + "-0",
			Annotations: map[string]string{},
		}}
		for k, val := range sts.Spec.Template.Annotations {
			pod.Annotations[k] = val
		}
		for _, c := range sts.Spec.Template.Spec.InitContainers {
			pod.Spec.InitContainers = append(pod.Spec.InitContainers, corev1.Container{Name: c.Name, Image: c.Image})
		}
		for _, c := range sts.Spec.Template.Spec.Containers {
			pod.Spec.Containers = append(pod.Spec.Containers,
				corev1.Container{Name: c.Name, Image: c.Image, Resources: *c.Resources.DeepCopy()})
		}
		return pod
	}

	assert.False(t, sentinelPodNeedsUpdate(build(), sts.Spec.Template), "a pod built from the template is current")
	for _, init := range sts.Spec.Template.Spec.InitContainers {
		pod := build()
		setImage(t, pod, init.Name, driftedImage)
		assert.True(t, sentinelPodNeedsUpdate(pod, sts.Spec.Template), "init container %s", init.Name)
	}
	pod := build()
	setImage(t, pod, builder.SentinelContainerName, driftedImage)
	assert.True(t, sentinelPodNeedsUpdate(pod, sts.Spec.Template), "the sentinel container is still compared")
}

// A single rootless persistent pod whose exporter image alone differs is replaced
// through the ordinary dispatch: the entry scan finds it outdated, and the
// single-pod deferral holds an exporter change only on a pod without a volume
// (ADR 0018 D11).
func TestCheckAndHandleRollingUpdate_ReplacesAStandalonePodWithAnExporterImageDrift(t *testing.T) {
	v, sts := metricsCluster("single", 1)
	pod := podWithTemplateInits(v, sts, 0)
	setImage(t, pod, builder.ExporterContainerName, driftedImage)
	r, c := newTestReconciler(v, sts, pod)

	result := r.checkAndHandleRollingUpdate(context.Background(), crGet(t, c, "single"))
	require.NoError(t, result.Error)
	assert.True(t, result.NeedsRequeue)
	assert.False(t, podExists(t, c, "single-0"), "the pod is replaced onto the template's images")
}

// The former single-pod residual: when the sidecar image differs as well, the change
// read as sidecar-only to the deferral and the pod kept its exporter. A differing
// exporter is decided by persistence since ADR 0018 D11, so this persistent pod is
// replaced; the non-persistent case is held and reported as ExporterOutdated
// (TestCheckAndHandleRollingUpdate_DefersAnOutdatedExporterOnANonPersistentPodAndReportsIt).
func TestCheckAndHandleRollingUpdate_ReplacesAPersistentStandalonePodWithAnExporterAndSidecarDrift(t *testing.T) {
	v, sts := metricsCluster("single", 1)
	pod := podWithTemplateInits(v, sts, 0)
	setImage(t, pod, builder.ExporterContainerName, driftedImage)
	setImage(t, pod, builder.SidecarContainerName, olderSidecarImage)
	r, c := newTestReconciler(v, sts, pod)

	result := r.checkAndHandleRollingUpdate(context.Background(), crGet(t, c, "single"))
	require.NoError(t, result.Error)
	assert.True(t, result.NeedsRequeue)
	assert.False(t, podExists(t, c, "single-0"), "the exporter update replaces a persistent single pod")
}
