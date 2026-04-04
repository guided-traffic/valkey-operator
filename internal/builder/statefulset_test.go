package builder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

const testOperatorImage = "ghcr.io/guided-traffic/valkey-operator:test"

// --- BuildStatefulSet ---

func TestBuildStatefulSet_Standalone(t *testing.T) {
	v := newTestValkey("test")

	sts := BuildStatefulSet(v, testOperatorImage)

	assert.Equal(t, "test", sts.Name)
	assert.Equal(t, "default", sts.Namespace)
	assert.Equal(t, int32(1), *sts.Spec.Replicas)
	assert.Equal(t, "test-headless", sts.Spec.ServiceName)
	assert.Equal(t, appsv1.OnDeleteStatefulSetStrategyType, sts.Spec.UpdateStrategy.Type)

	// Selector.
	assert.Equal(t, "test", sts.Spec.Selector.MatchLabels["app.kubernetes.io/instance"])
	assert.Equal(t, "valkey", sts.Spec.Selector.MatchLabels["app.kubernetes.io/component"])

	// Pod template labels.
	assert.Equal(t, "valkey", sts.Spec.Template.Labels["app.kubernetes.io/component"])
	assert.Equal(t, "test", sts.Spec.Template.Labels["app.kubernetes.io/instance"])
	assert.Equal(t, "8.0", sts.Spec.Template.Labels["app.kubernetes.io/version"])

	// Container.
	require.Len(t, sts.Spec.Template.Spec.Containers, 2)
	container := sts.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "valkey", container.Name)
	assert.Equal(t, "valkey/valkey:8.0", container.Image)

	// Sidecar container.
	sidecar := sts.Spec.Template.Spec.Containers[1]
	assert.Equal(t, SidecarContainerName, sidecar.Name)
	assert.Equal(t, testOperatorImage, sidecar.Image)

	// Command.
	assert.Equal(t, []string{"valkey-server", "/etc/valkey/valkey.conf"}, container.Command)

	// Ports.
	require.Len(t, container.Ports, 1)
	assert.Equal(t, int32(ValkeyPort), container.Ports[0].ContainerPort)
	assert.Equal(t, "valkey", container.Ports[0].Name)

	// Probes.
	assert.NotNil(t, container.ReadinessProbe)
	assert.NotNil(t, container.LivenessProbe)
	assert.Equal(t, []string{"valkey-cli", "ping"}, container.ReadinessProbe.Exec.Command)
	assert.Equal(t, []string{"valkey-cli", "ping"}, container.LivenessProbe.Exec.Command)

	// Volumes — config + emptyDir data (no persistence).
	require.Len(t, sts.Spec.Template.Spec.Volumes, 2)
	assert.Equal(t, ConfigVolumeName, sts.Spec.Template.Spec.Volumes[0].Name)
	assert.Equal(t, DataVolumeName, sts.Spec.Template.Spec.Volumes[1].Name)
	assert.NotNil(t, sts.Spec.Template.Spec.Volumes[0].ConfigMap)
	assert.NotNil(t, sts.Spec.Template.Spec.Volumes[1].EmptyDir)

	// No PVC templates.
	assert.Empty(t, sts.Spec.VolumeClaimTemplates)

	// Volume mounts.
	require.Len(t, container.VolumeMounts, 2)
	assert.Equal(t, ConfigMountPath, container.VolumeMounts[0].MountPath)
	assert.True(t, container.VolumeMounts[0].ReadOnly)
	assert.Equal(t, DataDir, container.VolumeMounts[1].MountPath)
}

func TestBuildStatefulSet_WithPersistence(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Persistence = &vkov1.PersistenceSpec{
			Enabled:      true,
			Mode:         vkov1.PersistenceModeRDB,
			StorageClass: "fast-ssd",
			Size:         resource.MustParse("10Gi"),
		}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	// Should have PVC template.
	require.Len(t, sts.Spec.VolumeClaimTemplates, 1)
	pvc := sts.Spec.VolumeClaimTemplates[0]
	assert.Equal(t, DataVolumeName, pvc.Name)
	assert.Equal(t, resource.MustParse("10Gi"), pvc.Spec.Resources.Requests[corev1.ResourceStorage])
	require.NotNil(t, pvc.Spec.StorageClassName)
	assert.Equal(t, "fast-ssd", *pvc.Spec.StorageClassName)
	assert.Contains(t, pvc.Spec.AccessModes, corev1.ReadWriteOnce)

	// Should NOT have emptyDir data volume (PVC takes over).
	for _, vol := range sts.Spec.Template.Spec.Volumes {
		assert.NotEqual(t, DataVolumeName, vol.Name, "data volume should come from PVC, not inline")
	}
}

func TestBuildStatefulSet_WithPersistence_DefaultStorageClass(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Persistence = &vkov1.PersistenceSpec{
			Enabled: true,
			Mode:    vkov1.PersistenceModeAOF,
			Size:    resource.MustParse("5Gi"),
		}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.Len(t, sts.Spec.VolumeClaimTemplates, 1)
	assert.Nil(t, sts.Spec.VolumeClaimTemplates[0].Spec.StorageClassName, "empty StorageClass should be nil (default)")
}

func TestBuildStatefulSet_WithResources(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Resources = corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("250m"),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
		}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	container := sts.Spec.Template.Spec.Containers[0]
	assert.Equal(t, resource.MustParse("500m"), container.Resources.Limits[corev1.ResourceCPU])
	assert.Equal(t, resource.MustParse("512Mi"), container.Resources.Limits[corev1.ResourceMemory])
	assert.Equal(t, resource.MustParse("250m"), container.Resources.Requests[corev1.ResourceCPU])
	assert.Equal(t, resource.MustParse("256Mi"), container.Resources.Requests[corev1.ResourceMemory])
}

func TestBuildStatefulSet_WithPodLabelsAndAnnotations(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.PodLabels = map[string]string{
			"custom-label": "custom-value",
		}
		v.Spec.PodAnnotations = map[string]string{
			"example.com/annotation": "true",
		}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	// User labels merged with operator labels.
	assert.Equal(t, "custom-value", sts.Spec.Template.Labels["custom-label"])
	assert.Equal(t, "valkey", sts.Spec.Template.Labels["app.kubernetes.io/component"])

	// Annotations.
	assert.Equal(t, "true", sts.Spec.Template.Annotations["example.com/annotation"])
}

func TestBuildStatefulSet_MultipleReplicas(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	assert.Equal(t, int32(3), *sts.Spec.Replicas)

	// Multi-replica without Sentinel must have an init container.
	require.Len(t, sts.Spec.Template.Spec.InitContainers, 1)
	initC := sts.Spec.Template.Spec.InitContainers[0]
	assert.Equal(t, "init-config-selector", initC.Name)

	// Must have replica-config and writable-config volumes.
	volNames := make([]string, 0, len(sts.Spec.Template.Spec.Volumes))
	for _, vol := range sts.Spec.Template.Spec.Volumes {
		volNames = append(volNames, vol.Name)
	}
	assert.Contains(t, volNames, ReplicaConfigVolumeName)
	assert.Contains(t, volNames, WritableConfigVolumeName)

	// Valkey container must use the writable config mount.
	valkeyContainer := sts.Spec.Template.Spec.Containers[0]
	assert.Equal(t, ValkeyContainerName, valkeyContainer.Name)
	foundWritableMount := false
	for _, vm := range valkeyContainer.VolumeMounts {
		if vm.Name == WritableConfigVolumeName {
			assert.Equal(t, WritableConfigMountPath, vm.MountPath)
			assert.False(t, vm.ReadOnly)
			foundWritableMount = true
		}
	}
	assert.True(t, foundWritableMount, "valkey container must mount writable config volume")
}

func TestBuildStatefulSet_SingleReplica_NoInitContainer(t *testing.T) {
	v := newTestValkey("test")

	sts := BuildStatefulSet(v, testOperatorImage)

	// Single replica (standalone) must NOT have an init container.
	assert.Empty(t, sts.Spec.Template.Spec.InitContainers)
}

func TestBuildStatefulSet_ConfigMapReference(t *testing.T) {
	v := newTestValkey("my-cluster")

	sts := BuildStatefulSet(v, testOperatorImage)

	configVol := sts.Spec.Template.Spec.Volumes[0]
	assert.Equal(t, ConfigVolumeName, configVol.Name)
	require.NotNil(t, configVol.ConfigMap)
	assert.Equal(t, "my-cluster-config", configVol.ConfigMap.Name)
}

// --- StatefulSetHasChanged ---

func TestStatefulSetHasChanged_NoChange(t *testing.T) {
	v := newTestValkey("test")
	desired := BuildStatefulSet(v, testOperatorImage)

	// Clone as current.
	current := desired.DeepCopy()

	assert.False(t, StatefulSetHasChanged(desired, current))
}

func TestStatefulSetHasChanged_ReplicaChange(t *testing.T) {
	v := newTestValkey("test")
	desired := BuildStatefulSet(v, testOperatorImage)
	current := desired.DeepCopy()

	newReplicas := int32(3)
	desired.Spec.Replicas = &newReplicas

	assert.True(t, StatefulSetHasChanged(desired, current))
}

func TestStatefulSetHasChanged_ImageChange(t *testing.T) {
	v := newTestValkey("test")
	desired := BuildStatefulSet(v, testOperatorImage)
	current := desired.DeepCopy()

	desired.Spec.Template.Spec.Containers[0].Image = "valkey/valkey:9.0"

	assert.True(t, StatefulSetHasChanged(desired, current))
}

func TestStatefulSetHasChanged_LabelChange(t *testing.T) {
	v := newTestValkey("test")
	desired := BuildStatefulSet(v, testOperatorImage)
	current := desired.DeepCopy()

	desired.Spec.Template.Labels["new-label"] = "value"

	assert.True(t, StatefulSetHasChanged(desired, current))
}

func TestStatefulSetHasChanged_AnnotationChange(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.PodAnnotations = map[string]string{"a": "1"}
	})
	desired := BuildStatefulSet(v, testOperatorImage)
	current := desired.DeepCopy()

	desired.Spec.Template.Annotations["b"] = "2"

	assert.True(t, StatefulSetHasChanged(desired, current))
}

func TestStatefulSetHasChanged_ResourceChange(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Resources = corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("500m"),
			},
		}
	})
	desired := BuildStatefulSet(v, testOperatorImage)
	current := desired.DeepCopy()

	desired.Spec.Template.Spec.Containers[0].Resources.Limits[corev1.ResourceCPU] = resource.MustParse("1000m")

	assert.True(t, StatefulSetHasChanged(desired, current))
}

// --- ServicePort / ProbeCommand ---

func TestServicePort_Default(t *testing.T) {
	v := newTestValkey("test")
	assert.Equal(t, int32(6379), ServicePort(v))
}

func TestServicePort_TLS(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})
	assert.Equal(t, int32(16379), ServicePort(v))
}

func TestProbeCommand_Default(t *testing.T) {
	v := newTestValkey("test")
	cmd := ProbeCommand(v)
	assert.Equal(t, []string{"valkey-cli", "ping"}, cmd)
}

func TestProbeCommand_TLS(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})
	cmd := ProbeCommand(v)
	assert.Contains(t, cmd, "--tls")
	assert.Contains(t, cmd, "ping")
}

// --- StatefulSet Labels ---

func TestBuildStatefulSet_LabelsOnStatefulSetItself(t *testing.T) {
	v := newTestValkey("test")

	sts := BuildStatefulSet(v, testOperatorImage)

	assert.Equal(t, "valkey", sts.Labels["app.kubernetes.io/component"])
	assert.Equal(t, "test", sts.Labels["app.kubernetes.io/instance"])
	assert.Equal(t, "vko.gtrfc.com", sts.Labels["app.kubernetes.io/managed-by"])
	assert.Equal(t, "8.0", sts.Labels["app.kubernetes.io/version"])
}

// --- Edge Cases ---

func TestBuildStatefulSet_EmptyPodLabels(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.PodLabels = map[string]string{}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	// Should still have operator labels.
	assert.Equal(t, "valkey", sts.Spec.Template.Labels["app.kubernetes.io/component"])
}

// --- buildVolumeClaimTemplates ---

func TestBuildVolumeClaimTemplates_DefaultSize(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Persistence = &vkov1.PersistenceSpec{
			Enabled: true,
			Mode:    vkov1.PersistenceModeRDB,
			// Size intentionally left as zero value.
		}
	})

	pvcs := buildVolumeClaimTemplates(v)

	require.Len(t, pvcs, 1)
	assert.Equal(t, resource.MustParse("1Gi"), pvcs[0].Spec.Resources.Requests[corev1.ResourceStorage])
}

// --- Standalone vs HA selector ---

func TestBuildStatefulSet_SelectorMatchesService(t *testing.T) {
	v := newTestValkey("test")

	sts := BuildStatefulSet(v, testOperatorImage)
	svc := BuildHeadlessService(v)

	// StatefulSet selector must match Service selector for DNS to work.
	for k, val := range sts.Spec.Selector.MatchLabels {
		assert.Equal(t, val, svc.Spec.Selector[k], "selector label %s must match between StatefulSet and Service", k)
	}
}

// --- ParallelPodManagement ---

func TestBuildStatefulSet_ParallelPodManagement(t *testing.T) {
	v := newTestValkey("test")

	sts := BuildStatefulSet(v, testOperatorImage)

	assert.Equal(t, appsv1.ParallelPodManagement, sts.Spec.PodManagementPolicy)
}

// --- OnDelete UpdateStrategy ---

func TestBuildStatefulSet_OnDeleteUpdateStrategy(t *testing.T) {
	v := newTestValkey("test")

	sts := BuildStatefulSet(v, testOperatorImage)

	assert.Equal(t, appsv1.OnDeleteStatefulSetStrategyType, sts.Spec.UpdateStrategy.Type,
		"operator manages pod-by-pod rollout, so StatefulSet must use OnDelete strategy")
}

// --- DesiredServicePort ---

func TestDesiredServicePort_Default(t *testing.T) {
	v := newTestValkey("test")
	port := DesiredServicePort(v)
	assert.Equal(t, int32(6379), port.Port)
	assert.Equal(t, "valkey", port.Name)
}

func TestDesiredServicePort_TLS(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})
	port := DesiredServicePort(v)
	assert.Equal(t, int32(16379), port.Port)
}

// helper to build a StatefulSet with a given image then "deploy" it as current
func buildCurrentSTS(name, image string) *appsv1.StatefulSet {
	replicas := int32(1)
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "valkey"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "valkey", Image: image},
					},
				},
			},
		},
	}
}

func TestStatefulSetHasChanged_SameImage(t *testing.T) {
	a := buildCurrentSTS("test", "valkey:8.0")
	b := a.DeepCopy()

	assert.False(t, StatefulSetHasChanged(a, b))
}

// --- Auth Tests ---

func TestBuildStatefulSet_WithAuth(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	container := sts.Spec.Template.Spec.Containers[0]

	// Container should have env var from auth Secret.
	require.Len(t, container.Env, 1)
	assert.Equal(t, AuthSecretEnvName, container.Env[0].Name)
	require.NotNil(t, container.Env[0].ValueFrom)
	require.NotNil(t, container.Env[0].ValueFrom.SecretKeyRef)
	assert.Equal(t, "my-secret", container.Env[0].ValueFrom.SecretKeyRef.Name)
	assert.Equal(t, "password", container.Env[0].ValueFrom.SecretKeyRef.Key)

	// Command should use shell to expand env var for auth flags.
	assert.Equal(t, "sh", container.Command[0])
	assert.Equal(t, "-c", container.Command[1])
	assert.Contains(t, container.Command[2], "--requirepass")
	assert.Contains(t, container.Command[2], "--masterauth")
	assert.Contains(t, container.Command[2], "$VALKEY_PASSWORD")
}

func TestBuildStatefulSet_WithAuthCustomKey(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "custom-auth",
			SecretPasswordKey: "redis-pass",
		}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	container := sts.Spec.Template.Spec.Containers[0]

	require.Len(t, container.Env, 1)
	assert.Equal(t, "custom-auth", container.Env[0].ValueFrom.SecretKeyRef.Name)
	assert.Equal(t, "redis-pass", container.Env[0].ValueFrom.SecretKeyRef.Key)
}

func TestBuildStatefulSet_WithoutAuth_NoEnvVars(t *testing.T) {
	v := newTestValkey("test")

	sts := BuildStatefulSet(v, testOperatorImage)

	container := sts.Spec.Template.Spec.Containers[0]

	// No env vars should be present.
	assert.Empty(t, container.Env)

	// Command should be direct valkey-server (no shell wrapper).
	assert.Equal(t, "valkey-server", container.Command[0])
}

func TestBuildStatefulSet_WithAuthAndTLS(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	container := sts.Spec.Template.Spec.Containers[0]

	// Should have auth env var.
	require.Len(t, container.Env, 1)
	assert.Equal(t, AuthSecretEnvName, container.Env[0].Name)

	// Command should include auth.
	assert.Contains(t, container.Command[2], "--requirepass")

	// TLS volumes should still be present.
	hasVolume := false
	for _, vol := range sts.Spec.Template.Spec.Volumes {
		if vol.Name == TLSVolumeName {
			hasVolume = true
		}
	}
	assert.True(t, hasVolume, "TLS volume should be present alongside auth")
}

func TestBuildStatefulSet_WithAuth_HAMode(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	container := sts.Spec.Template.Spec.Containers[0]

	// Should have auth env var.
	require.Len(t, container.Env, 1)
	assert.Equal(t, AuthSecretEnvName, container.Env[0].Name)

	// In HA mode, should use writable config path.
	assert.Contains(t, container.Command[2], WritableConfigMountPath)
	assert.Contains(t, container.Command[2], "--requirepass")
	assert.Contains(t, container.Command[2], "--masterauth")
}

// --- Probe Command Auth ---

func TestProbeCommand_Auth(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
	})

	cmd := ProbeCommand(v)

	// Should use shell to expand env var.
	assert.Equal(t, "sh", cmd[0])
	assert.Equal(t, "-c", cmd[1])
	assert.Contains(t, cmd[2], "-a")
	assert.Contains(t, cmd[2], "$VALKEY_PASSWORD")
	assert.Contains(t, cmd[2], "ping")
}

func TestProbeCommand_AuthWithTLS(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})

	cmd := ProbeCommand(v)

	assert.Equal(t, "sh", cmd[0])
	assert.Equal(t, "-c", cmd[1])
	assert.Contains(t, cmd[2], "--tls")
	assert.Contains(t, cmd[2], "-a")
	assert.Contains(t, cmd[2], "$VALKEY_PASSWORD")
	assert.Contains(t, cmd[2], "ping")
	assert.Contains(t, cmd[2], "16379")
}

// --- StatefulSetHasChanged Auth ---

func TestStatefulSetHasChanged_EnvVarAdded(t *testing.T) {
	v := newTestValkey("test")
	desired := BuildStatefulSet(v, testOperatorImage)
	current := desired.DeepCopy()

	// Add auth to desired (simulating auth being enabled).
	desired.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{
		{
			Name: AuthSecretEnvName,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "my-secret"},
					Key:                  "password",
				},
			},
		},
	}

	assert.True(t, StatefulSetHasChanged(desired, current))
}

func TestStatefulSetHasChanged_CommandChanged(t *testing.T) {
	v := newTestValkey("test")
	desired := BuildStatefulSet(v, testOperatorImage)
	current := desired.DeepCopy()

	// Simulate command change (auth flags added).
	desired.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "exec valkey-server /etc/valkey/valkey.conf --requirepass \"$VALKEY_PASSWORD\""}

	assert.True(t, StatefulSetHasChanged(desired, current))
}

func TestStatefulSetHasChanged_SidecarImageChange(t *testing.T) {
	v := newTestValkey("test")
	desired := BuildStatefulSet(v, testOperatorImage)
	current := desired.DeepCopy()

	// Simulate operator upgrade: sidecar image in current pod spec is old.
	current.Spec.Template.Spec.Containers[1].Image = "ghcr.io/guided-traffic/valkey-operator:v0.9"

	assert.True(t, StatefulSetHasChanged(desired, current))
}

func TestStatefulSetHasChanged_SidecarImageNoChange(t *testing.T) {
	v := newTestValkey("test")
	desired := BuildStatefulSet(v, testOperatorImage)
	current := desired.DeepCopy()

	assert.False(t, StatefulSetHasChanged(desired, current))
}

func TestStatefulSetHasChanged_SidecarArgsChange(t *testing.T) {
	v := newTestValkey("test")
	desired := BuildStatefulSet(v, testOperatorImage)
	current := desired.DeepCopy()

	// Simulate sidecar args change (e.g., new flag added in newer operator).
	current.Spec.Template.Spec.Containers[1].Args = []string{"sidecar", "--obsolete-flag"}

	assert.True(t, StatefulSetHasChanged(desired, current))
}

func TestStatefulSetHasChanged_InitContainerImageChange(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	desired := BuildStatefulSet(v, testOperatorImage)
	current := desired.DeepCopy()

	if len(desired.Spec.Template.Spec.InitContainers) == 0 {
		t.Skip("no init containers in this configuration")
	}

	// Simulate init container image change between operator versions.
	current.Spec.Template.Spec.InitContainers[0].Image = "old-operator:v0.1"

	assert.True(t, StatefulSetHasChanged(desired, current))
}

func TestStatefulSetHasChanged_ExtraInitContainer(t *testing.T) {
	v := newTestValkey("test")
	desired := BuildStatefulSet(v, testOperatorImage)
	current := desired.DeepCopy()

	// Simulate an init container being added in the new desired spec.
	desired.Spec.Template.Spec.InitContainers = []corev1.Container{
		{Name: "init-extra", Image: "busybox:latest"},
	}

	assert.True(t, StatefulSetHasChanged(desired, current))
}

func TestStatefulSetHasChanged_VolumeAdded(t *testing.T) {
	v := newTestValkey("test")
	desired := BuildStatefulSet(v, testOperatorImage)
	current := desired.DeepCopy()

	// Simulate a new volume added by a newer operator version.
	desired.Spec.Template.Spec.Volumes = append(desired.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: "new-volume",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	})

	assert.True(t, StatefulSetHasChanged(desired, current))
}

func TestStatefulSetHasChanged_VolumeConfigMapChanged(t *testing.T) {
	v := newTestValkey("test")
	desired := BuildStatefulSet(v, testOperatorImage)
	current := desired.DeepCopy()

	// Simulate the ConfigMap backing a volume being renamed between operator versions.
	for i, vol := range current.Spec.Template.Spec.Volumes {
		if vol.ConfigMap != nil {
			current.Spec.Template.Spec.Volumes[i].ConfigMap.Name = "old-configmap-name"
			break
		}
	}

	assert.True(t, StatefulSetHasChanged(desired, current))
}

func TestStatefulSetHasChanged_ServiceAccountNameChange(t *testing.T) {
	v := newTestValkey("test")
	desired := BuildStatefulSet(v, testOperatorImage)
	current := desired.DeepCopy()

	current.Spec.Template.Spec.ServiceAccountName = "old-service-account"

	assert.True(t, StatefulSetHasChanged(desired, current))
}

func TestStatefulSetHasChanged_TerminationGracePeriodChange(t *testing.T) {
	v := newTestValkey("test")
	desired := BuildStatefulSet(v, testOperatorImage)
	current := desired.DeepCopy()

	oldGrace := int64(30)
	current.Spec.Template.Spec.TerminationGracePeriodSeconds = &oldGrace

	assert.True(t, StatefulSetHasChanged(desired, current))
}

func TestStatefulSetHasChanged_VolumeMountAdded(t *testing.T) {
	v := newTestValkey("test")
	desired := BuildStatefulSet(v, testOperatorImage)
	current := desired.DeepCopy()

	// Simulate a new volume mount added to the sidecar in a newer operator version.
	desired.Spec.Template.Spec.Containers[1].VolumeMounts = append(
		desired.Spec.Template.Spec.Containers[1].VolumeMounts,
		corev1.VolumeMount{Name: "new-mount", MountPath: "/new"},
	)

	assert.True(t, StatefulSetHasChanged(desired, current))
}

// --- Sidecar Container Tests ---

func TestBuildStatefulSet_SidecarContainer(t *testing.T) {
	v := newTestValkey("test")

	sts := BuildStatefulSet(v, testOperatorImage)

	require.Len(t, sts.Spec.Template.Spec.Containers, 2)
	sidecar := sts.Spec.Template.Spec.Containers[1]

	assert.Equal(t, SidecarContainerName, sidecar.Name)
	assert.Equal(t, testOperatorImage, sidecar.Image)
	assert.Equal(t, []string{"./manager"}, sidecar.Command)
	assert.Contains(t, sidecar.Args, "sidecar")
	assert.Contains(t, sidecar.Args, "--poll-interval=1s")

	// Drain-related args: headless service and replicas always present.
	assert.Contains(t, sidecar.Args, "--headless-svc=test-headless.default.svc.cluster.local")
	assert.Contains(t, sidecar.Args, "--replicas=1")

	// Sentinel args should NOT be present for standalone.
	for _, arg := range sidecar.Args {
		assert.NotContains(t, arg, "--sentinel-enabled")
		assert.NotContains(t, arg, "--sentinel-monitor")
		assert.NotContains(t, arg, "--sentinel-addrs")
	}

	// Ports.
	require.Len(t, sidecar.Ports, 1)
	assert.Equal(t, int32(SidecarHealthPort), sidecar.Ports[0].ContainerPort)
	assert.Equal(t, "health", sidecar.Ports[0].Name)

	// Probes.
	require.NotNil(t, sidecar.ReadinessProbe)
	require.NotNil(t, sidecar.ReadinessProbe.HTTPGet)
	assert.Equal(t, "/readyz", sidecar.ReadinessProbe.HTTPGet.Path)
	require.NotNil(t, sidecar.LivenessProbe)
	require.NotNil(t, sidecar.LivenessProbe.HTTPGet)
	assert.Equal(t, "/healthz", sidecar.LivenessProbe.HTTPGet.Path)

	// Downward API env vars.
	hasPodName := false
	hasPodNamespace := false
	for _, env := range sidecar.Env {
		if env.Name == "POD_NAME" && env.ValueFrom != nil && env.ValueFrom.FieldRef != nil {
			hasPodName = true
			assert.Equal(t, "metadata.name", env.ValueFrom.FieldRef.FieldPath)
		}
		if env.Name == "POD_NAMESPACE" && env.ValueFrom != nil && env.ValueFrom.FieldRef != nil {
			hasPodNamespace = true
			assert.Equal(t, "metadata.namespace", env.ValueFrom.FieldRef.FieldPath)
		}
	}
	assert.True(t, hasPodName, "sidecar must have POD_NAME from Downward API")
	assert.True(t, hasPodNamespace, "sidecar must have POD_NAMESPACE from Downward API")

	// No TLS volume mounts in non-TLS mode.
	assert.Empty(t, sidecar.VolumeMounts)
}

func TestBuildStatefulSet_SidecarWithTLS(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	sidecar := sts.Spec.Template.Spec.Containers[1]
	assert.Equal(t, SidecarContainerName, sidecar.Name)

	// Should have TLS flags.
	assert.Contains(t, sidecar.Args, "--tls-enabled=true")
	assert.Contains(t, sidecar.Args, "--tls-ca-cert=/tls/ca.crt")
	assert.Contains(t, sidecar.Args, "--tls-cert=/tls/tls.crt")
	assert.Contains(t, sidecar.Args, "--tls-key=/tls/tls.key")

	// Should have TLS volume mount.
	require.Len(t, sidecar.VolumeMounts, 1)
	assert.Equal(t, TLSVolumeName, sidecar.VolumeMounts[0].Name)
	assert.Equal(t, TLSMountPath, sidecar.VolumeMounts[0].MountPath)
	assert.True(t, sidecar.VolumeMounts[0].ReadOnly)
}

func TestBuildStatefulSet_SidecarWithAuth(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	sidecar := sts.Spec.Template.Spec.Containers[1]

	// Sidecar should have auth env var.
	hasAuthEnv := false
	for _, env := range sidecar.Env {
		if env.Name == AuthSecretEnvName && env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil {
			hasAuthEnv = true
			assert.Equal(t, "my-secret", env.ValueFrom.SecretKeyRef.Name)
			assert.Equal(t, "password", env.ValueFrom.SecretKeyRef.Key)
		}
	}
	assert.True(t, hasAuthEnv, "sidecar must have VALKEY_PASSWORD env var from Secret")
}

func TestBuildStatefulSet_SidecarImageDefault(t *testing.T) {
	v := newTestValkey("test")

	sts := BuildStatefulSet(v, "")

	sidecar := sts.Spec.Template.Spec.Containers[1]
	assert.Equal(t, "ghcr.io/guided-traffic/valkey-operator:latest", sidecar.Image,
		"when operatorImage is empty, should use default image")
}

func TestBuildStatefulSet_ServiceAccountName(t *testing.T) {
	v := newTestValkey("my-valkey")

	sts := BuildStatefulSet(v, testOperatorImage)

	assert.Equal(t, "my-valkey-sidecar", sts.Spec.Template.Spec.ServiceAccountName)
}

func TestBuildStatefulSet_TerminationGracePeriod(t *testing.T) {
	v := newTestValkey("test")

	sts := BuildStatefulSet(v, testOperatorImage)

	require.NotNil(t, sts.Spec.Template.Spec.TerminationGracePeriodSeconds)
	assert.Equal(t, int64(75), *sts.Spec.Template.Spec.TerminationGracePeriodSeconds)
}

func TestBuildStatefulSet_SidecarSentinelArgs(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	sidecar := sts.Spec.Template.Spec.Containers[1]

	assert.Contains(t, sidecar.Args, "--sentinel-enabled=true")
	assert.Contains(t, sidecar.Args, "--sentinel-monitor=test")
	assert.Contains(t, sidecar.Args, "--replicas=3")
	assert.Contains(t, sidecar.Args, "--headless-svc=test-headless.default.svc.cluster.local")

	// Verify sentinel addresses are present.
	hasSentinelAddrs := false
	for _, arg := range sidecar.Args {
		if len(arg) > 16 && arg[:16] == "--sentinel-addrs" {
			hasSentinelAddrs = true
			assert.Contains(t, arg, "test-sentinel-0.test-sentinel-headless.default.svc.cluster.local:26379")
			assert.Contains(t, arg, "test-sentinel-1.test-sentinel-headless.default.svc.cluster.local:26379")
			assert.Contains(t, arg, "test-sentinel-2.test-sentinel-headless.default.svc.cluster.local:26379")
		}
	}
	assert.True(t, hasSentinelAddrs, "sidecar must have --sentinel-addrs arg")
}

func TestBuildSentinelAddrList(t *testing.T) {
	v := newTestValkey("my-cluster", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	addrs := buildSentinelAddrList(v)

	expected := "my-cluster-sentinel-0.my-cluster-sentinel-headless.default.svc.cluster.local:26379," +
		"my-cluster-sentinel-1.my-cluster-sentinel-headless.default.svc.cluster.local:26379," +
		"my-cluster-sentinel-2.my-cluster-sentinel-headless.default.svc.cluster.local:26379"
	assert.Equal(t, expected, addrs)
}

// --- containerPortsEqual ---

func TestContainerPortsEqual_SamePorts(t *testing.T) {
	a := []corev1.ContainerPort{{Name: "valkey", ContainerPort: 6379, Protocol: corev1.ProtocolTCP}}
	b := []corev1.ContainerPort{{Name: "valkey", ContainerPort: 6379, Protocol: corev1.ProtocolTCP}}
	assert.True(t, containerPortsEqual(a, b))
}

func TestContainerPortsEqual_Empty(t *testing.T) {
	assert.True(t, containerPortsEqual(nil, nil))
	assert.True(t, containerPortsEqual([]corev1.ContainerPort{}, []corev1.ContainerPort{}))
}

func TestContainerPortsEqual_DifferentCount(t *testing.T) {
	a := []corev1.ContainerPort{
		{Name: "valkey", ContainerPort: 16379, Protocol: corev1.ProtocolTCP},
		{Name: "valkey-plain", ContainerPort: 6379, Protocol: corev1.ProtocolTCP},
	}
	b := []corev1.ContainerPort{{Name: "valkey", ContainerPort: 16379, Protocol: corev1.ProtocolTCP}}
	assert.False(t, containerPortsEqual(a, b))
}

func TestContainerPortsEqual_DifferentPortNumber(t *testing.T) {
	a := []corev1.ContainerPort{{Name: "valkey", ContainerPort: 16379, Protocol: corev1.ProtocolTCP}}
	b := []corev1.ContainerPort{{Name: "valkey", ContainerPort: 6379, Protocol: corev1.ProtocolTCP}}
	assert.False(t, containerPortsEqual(a, b))
}

func TestContainerPortsEqual_DifferentName(t *testing.T) {
	a := []corev1.ContainerPort{{Name: "valkey", ContainerPort: 6379, Protocol: corev1.ProtocolTCP}}
	b := []corev1.ContainerPort{{Name: "valkey-plain", ContainerPort: 6379, Protocol: corev1.ProtocolTCP}}
	assert.False(t, containerPortsEqual(a, b))
}

func TestContainerPortsEqual_OrderIndependent(t *testing.T) {
	a := []corev1.ContainerPort{
		{Name: "valkey", ContainerPort: 16379, Protocol: corev1.ProtocolTCP},
		{Name: "valkey-plain", ContainerPort: 6379, Protocol: corev1.ProtocolTCP},
	}
	b := []corev1.ContainerPort{
		{Name: "valkey-plain", ContainerPort: 6379, Protocol: corev1.ProtocolTCP},
		{Name: "valkey", ContainerPort: 16379, Protocol: corev1.ProtocolTCP},
	}
	assert.True(t, containerPortsEqual(a, b))
}

// --- containerChanged: port detection ---

func TestContainerChanged_PortChangeTriggersDiff(t *testing.T) {
	desired := corev1.Container{
		Name:  "valkey",
		Image: "valkey/valkey:8.0",
		Ports: []corev1.ContainerPort{
			{Name: "valkey", ContainerPort: 16379, Protocol: corev1.ProtocolTCP},
			{Name: "valkey-plain", ContainerPort: 6379, Protocol: corev1.ProtocolTCP},
		},
	}
	current := corev1.Container{
		Name:  "valkey",
		Image: "valkey/valkey:8.0",
		Ports: []corev1.ContainerPort{
			{Name: "valkey", ContainerPort: 16379, Protocol: corev1.ProtocolTCP},
		},
	}
	assert.True(t, containerChanged(desired, current), "adding a plaintext port must be detected as a change")
}

func TestContainerChanged_SamePortsNoChange(t *testing.T) {
	ports := []corev1.ContainerPort{{Name: "valkey", ContainerPort: 6379, Protocol: corev1.ProtocolTCP}}
	c := corev1.Container{Name: "valkey", Image: "valkey/valkey:8.0", Ports: ports}
	assert.False(t, containerChanged(c, c))
}

// --- StatefulSetHasChanged: port change detection ---

func TestStatefulSetHasChanged_PortChange(t *testing.T) {
	replicas := int32(3)
	desired := &appsv1.StatefulSet{
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "valkey",
							Image: "valkey/valkey:8.0",
							Ports: []corev1.ContainerPort{
								{Name: "valkey", ContainerPort: 16379, Protocol: corev1.ProtocolTCP},
								{Name: "valkey-plain", ContainerPort: 6379, Protocol: corev1.ProtocolTCP},
							},
						},
					},
				},
			},
		},
	}
	current := &appsv1.StatefulSet{
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "valkey",
							Image: "valkey/valkey:8.0",
							Ports: []corev1.ContainerPort{
								{Name: "valkey", ContainerPort: 16379, Protocol: corev1.ProtocolTCP},
							},
						},
					},
				},
			},
		},
	}
	assert.True(t, StatefulSetHasChanged(desired, current), "adding valkey-plain port must be detected")
}

func TestStatefulSetHasChanged_NoPortChange(t *testing.T) {
	replicas := int32(1)
	ports := []corev1.ContainerPort{{Name: "valkey", ContainerPort: 6379, Protocol: corev1.ProtocolTCP}}
	spec := appsv1.StatefulSetSpec{
		Replicas: &replicas,
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "valkey", Image: "valkey/valkey:8.0", Ports: ports}},
			},
		},
	}
	sts := &appsv1.StatefulSet{Spec: spec}
	assert.False(t, StatefulSetHasChanged(sts, sts))
}

// --- BuildStatefulSet: config hash annotation ---

func TestBuildStatefulSet_InjectsConfigHashAnnotation(t *testing.T) {
	v := newTestValkey("test")

	sts := BuildStatefulSet(v, testOperatorImage)

	hash, ok := sts.Spec.Template.Annotations[AnnotationConfigHash]
	assert.True(t, ok, "pod template must carry the config hash annotation")
	assert.NotEmpty(t, hash, "config hash must not be empty")
}

func TestBuildStatefulSet_ConfigHashAnnotationChangesWithSpec(t *testing.T) {
	// Without TLS.
	v1 := newTestValkey("test")
	sts1 := BuildStatefulSet(v1, testOperatorImage)

	// With TLS → different config → different hash.
	v2 := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled:    true,
			SecretName: "tls-secret",
		}
	})
	sts2 := BuildStatefulSet(v2, testOperatorImage)

	hash1 := sts1.Spec.Template.Annotations[AnnotationConfigHash]
	hash2 := sts2.Spec.Template.Annotations[AnnotationConfigHash]
	assert.NotEqual(t, hash1, hash2, "config hash must differ when TLS is toggled")
}

func TestBuildStatefulSet_PreservesUserAnnotationsAlongsideConfigHash(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.PodAnnotations = map[string]string{"custom/ann": "value"}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	assert.Equal(t, "value", sts.Spec.Template.Annotations["custom/ann"])
	assert.NotEmpty(t, sts.Spec.Template.Annotations[AnnotationConfigHash])
}

// --- BuildStatefulSet: pod spec hash annotation ---

func TestBuildStatefulSet_InjectsPodSpecHashAnnotation(t *testing.T) {
	v := newTestValkey("test")

	sts := BuildStatefulSet(v, testOperatorImage)

	hash, ok := sts.Spec.Template.Annotations[AnnotationPodSpecHash]
	assert.True(t, ok, "pod template must carry the pod spec hash annotation")
	assert.NotEmpty(t, hash, "pod spec hash must not be empty")
}

func TestBuildStatefulSet_PodSpecHashChangesWithResources(t *testing.T) {
	v1 := newTestValkey("test")
	sts1 := BuildStatefulSet(v1, testOperatorImage)

	v2 := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Resources = corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
		}
	})
	sts2 := BuildStatefulSet(v2, testOperatorImage)

	hash1 := sts1.Spec.Template.Annotations[AnnotationPodSpecHash]
	hash2 := sts2.Spec.Template.Annotations[AnnotationPodSpecHash]
	assert.NotEqual(t, hash1, hash2, "pod spec hash must differ when resources change")
}

func TestBuildStatefulSet_PodSpecHashStableForSameSpec(t *testing.T) {
	v := newTestValkey("test")
	sts1 := BuildStatefulSet(v, testOperatorImage)
	sts2 := BuildStatefulSet(v, testOperatorImage)

	hash1 := sts1.Spec.Template.Annotations[AnnotationPodSpecHash]
	hash2 := sts2.Spec.Template.Annotations[AnnotationPodSpecHash]
	assert.Equal(t, hash1, hash2, "pod spec hash must be stable for the same spec")
}

func TestComputePodSpecHash_ChangesWithOperatorImage(t *testing.T) {
	v := newTestValkey("test")
	hash1 := ComputePodSpecHash(v, "operator:v1.0")
	hash2 := ComputePodSpecHash(v, "operator:v2.0")
	assert.NotEqual(t, hash1, hash2, "pod spec hash must change when operator image changes")
}

// --- Valkey container ports ---

// TestBuildStatefulSet_TLSOnly_SinglePort16379 verifies that when TLS is enabled
// the Valkey container exposes only the TLS port (16379) named "valkey".
func TestBuildStatefulSet_TLSOnly_SinglePort16379(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})

	sts := BuildStatefulSet(v, testOperatorImage)
	c := sts.Spec.Template.Spec.Containers[0]

	require.Len(t, c.Ports, 1, "TLS-only should expose exactly one port")
	assert.Equal(t, "valkey", c.Ports[0].Name)
	assert.Equal(t, int32(TLSPort), c.Ports[0].ContainerPort,
		"TLS port must be 16379")
}

// TestBuildStatefulSet_TLSAndAllowUnencrypted_DualPorts verifies that when both
// TLS and allowUnencrypted are enabled, the Valkey container exposes two named
// ports: "valkey" (16379) and "valkey-plain" (6379).
func TestBuildStatefulSet_TLSAndAllowUnencrypted_DualPorts(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true, AllowUnencrypted: true}
	})

	sts := BuildStatefulSet(v, testOperatorImage)
	c := sts.Spec.Template.Spec.Containers[0]

	require.Len(t, c.Ports, 2, "TLS+allowUnencrypted should expose two ports")

	portsByName := make(map[string]int32)
	for _, p := range c.Ports {
		portsByName[p.Name] = p.ContainerPort
	}

	assert.Equal(t, int32(TLSPort), portsByName["valkey"],
		"Named port 'valkey' must be TLS port 16379")
	assert.Equal(t, int32(ValkeyPort), portsByName["valkey-plain"],
		"Named port 'valkey-plain' must be plaintext port 6379")
}

// --- buildSentinelAddrList with TLS ---

// TestBuildSentinelAddrList_TLS_UsesTLSPort36379 verifies that when TLS is
// enabled, the sentinel addr list uses port 36379 (SentinelTLSPort) so that
// the sidecar drain handler connects to Sentinel over TLS.
func TestBuildSentinelAddrList_TLS_UsesTLSPort36379(t *testing.T) {
	v := newTestValkey("my-cluster", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	addrs := buildSentinelAddrList(v)

	expected := "my-cluster-sentinel-0.my-cluster-sentinel-headless.default.svc.cluster.local:36379," +
		"my-cluster-sentinel-1.my-cluster-sentinel-headless.default.svc.cluster.local:36379," +
		"my-cluster-sentinel-2.my-cluster-sentinel-headless.default.svc.cluster.local:36379"
	assert.Equal(t, expected, addrs,
		"With TLS enabled, sentinel addr list must target port 36379 (SentinelTLSPort)")
}

// --- Init container Sentinel port ---

// TestBuildStatefulSet_HA_InitContainer_TLS_UsesTLSPort36379 verifies that the
// HA init container queries Sentinel over TLS using port 36379 when TLS is enabled.
func TestBuildStatefulSet_HA_InitContainer_TLS_UsesTLSPort36379(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.Len(t, sts.Spec.Template.Spec.InitContainers, 1, "HA should have init container")
	initCmd := sts.Spec.Template.Spec.InitContainers[0].Command
	require.Len(t, initCmd, 3, "init container command should be [sh -c ...]")

	script := initCmd[2]
	assert.Contains(t, script, "36379",
		"Init container script must query Sentinel on TLS port 36379 when TLS is enabled")
	assert.Contains(t, script, "--tls",
		"Init container script must use --tls flag when TLS is enabled")
	assert.Contains(t, script, "--cacert",
		"Init container script must use --cacert flag when TLS is enabled")
}

// TestBuildStatefulSet_HA_InitContainer_NoTLS_UsesPlainPort26379 verifies that
// without TLS the init container queries Sentinel on plain port 26379.
func TestBuildStatefulSet_HA_InitContainer_NoTLS_UsesPlainPort26379(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.Len(t, sts.Spec.Template.Spec.InitContainers, 1, "HA should have init container")
	script := sts.Spec.Template.Spec.InitContainers[0].Command[2]

	assert.Contains(t, script, "26379",
		"Init container script must query Sentinel on plain port 26379 when no TLS")
	assert.NotContains(t, script, "--tls",
		"Init container script must NOT use --tls flag when TLS is disabled")
}

// TestBuildStatefulSet_HA_InitContainer_Auth_PassesCredentials verifies that when
// auth is enabled the init container script passes -a "$VALKEY_PASSWORD" to
// valkey-cli so Sentinel does not return "NOAUTH Authentication required."
// and the VALKEY_PASSWORD env var is injected into the init container.
func TestBuildStatefulSet_HA_InitContainer_Auth_PassesCredentials(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.Len(t, sts.Spec.Template.Spec.InitContainers, 1, "HA should have init container")
	init := sts.Spec.Template.Spec.InitContainers[0]
	script := init.Command[2]

	assert.Contains(t, script, "-a \"$VALKEY_PASSWORD\"",
		"Init container script must pass -a flag with password when auth is enabled")
	assert.Contains(t, script, "--no-auth-warning",
		"Init container script must suppress auth warning")
	assert.Contains(t, script, "NOAUTH",
		"Init container script must guard against NOAUTH error responses from Sentinel")

	// The VALKEY_PASSWORD env var must be injected so the shell can expand it.
	require.Len(t, init.Env, 1, "Init container must have VALKEY_PASSWORD env var")
	assert.Equal(t, AuthSecretEnvName, init.Env[0].Name)
	assert.Equal(t, "my-secret", init.Env[0].ValueFrom.SecretKeyRef.Name)
	assert.Equal(t, "password", init.Env[0].ValueFrom.SecretKeyRef.Key)
}

// TestBuildStatefulSet_HA_InitContainer_NoAuth_NoCredentials verifies that when
// auth is disabled the init container script does not include auth flags.
func TestBuildStatefulSet_HA_InitContainer_NoAuth_NoCredentials(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.Len(t, sts.Spec.Template.Spec.InitContainers, 1, "HA should have init container")
	init := sts.Spec.Template.Spec.InitContainers[0]
	script := init.Command[2]

	assert.NotContains(t, script, "-a ",
		"Init container script must NOT include -a flag when auth is disabled")
	assert.Empty(t, init.Env,
		"Init container must have no env vars when auth is disabled")
}

// TestBuildStatefulSet_HA_InitContainer_AuthDisabled_NoCredentials verifies that when
// auth is enabled but sentinel disableAuth is true, the init container script does NOT
// include auth flags for Sentinel queries (since Sentinel has no requirepass).
func TestBuildStatefulSet_HA_InitContainer_AuthDisabled_NoCredentials(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
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

	sts := BuildStatefulSet(v, testOperatorImage)

	require.Len(t, sts.Spec.Template.Spec.InitContainers, 1)
	init := sts.Spec.Template.Spec.InitContainers[0]
	script := init.Command[2]

	assert.NotContains(t, script, "-a ",
		"Init container script must NOT include -a flag when sentinel auth is disabled")

	// The VALKEY_PASSWORD env var must still be injected because the main Valkey
	// container still needs it for --requirepass/--masterauth.
}

// TestBuildStatefulSet_Sidecar_SentinelDisableAuth verifies that the sidecar container
// receives --sentinel-disable-auth=true when sentinel disableAuth is enabled.
func TestBuildStatefulSet_Sidecar_SentinelDisableAuth(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
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

	sts := BuildStatefulSet(v, testOperatorImage)

	var sidecar *corev1.Container
	for i := range sts.Spec.Template.Spec.Containers {
		if sts.Spec.Template.Spec.Containers[i].Name == SidecarContainerName {
			sidecar = &sts.Spec.Template.Spec.Containers[i]
			break
		}
	}
	require.NotNil(t, sidecar, "sidecar container must exist")

	hasDisableAuth := false
	for _, arg := range sidecar.Args {
		if arg == "--sentinel-disable-auth=true" {
			hasDisableAuth = true
			break
		}
	}
	assert.True(t, hasDisableAuth, "sidecar must have --sentinel-disable-auth=true arg")
}

// TestBuildStatefulSet_Sidecar_NoSentinelDisableAuth verifies that --sentinel-disable-auth
// is NOT present when sentinel disableAuth is false (default).
func TestBuildStatefulSet_Sidecar_NoSentinelDisableAuth(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	var sidecar *corev1.Container
	for i := range sts.Spec.Template.Spec.Containers {
		if sts.Spec.Template.Spec.Containers[i].Name == SidecarContainerName {
			sidecar = &sts.Spec.Template.Spec.Containers[i]
			break
		}
	}
	require.NotNil(t, sidecar, "sidecar container must exist")

	for _, arg := range sidecar.Args {
		assert.NotContains(t, arg, "sentinel-disable-auth",
			"sidecar must NOT have --sentinel-disable-auth when not set")
	}
}

// TestBuildInitContainerVolumeMounts_TLS_MountsTLSVolume verifies that the init
// container mounts the TLS secret volume when TLS is enabled.
func TestBuildInitContainerVolumeMounts_TLS_MountsTLSVolume(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})

	mounts := buildInitContainerVolumeMounts(v)

	mountNames := make([]string, 0, len(mounts))
	for _, m := range mounts {
		mountNames = append(mountNames, m.Name)
	}
	assert.Contains(t, mountNames, TLSVolumeName,
		"Init container mounts must include TLS volume when TLS is enabled")
}

// TestBuildInitContainerVolumeMounts_NoTLS_NoTLSMount verifies that without TLS
// the init container does not mount a TLS volume.
func TestBuildInitContainerVolumeMounts_NoTLS_NoTLSMount(t *testing.T) {
	v := newTestValkey("test")

	mounts := buildInitContainerVolumeMounts(v)

	for _, m := range mounts {
		assert.NotEqual(t, TLSVolumeName, m.Name,
			"Init container must not mount TLS volume when TLS is disabled")
	}
}

// --- replica-announce-ip / replica-announce-port ---

func TestBuildStatefulSet_HA_InitContainer_InjectsReplicaAnnounceIP(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
	script := sts.Spec.Template.Spec.InitContainers[0].Command[2]
	assert.Contains(t, script, `replica-announce-ip $MY_HOST`,
		"Sentinel init container must inject replica-announce-ip")
}

func TestBuildStatefulSet_HA_InitContainer_InjectsReplicaAnnouncePort_TLS(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
	script := sts.Spec.Template.Spec.InitContainers[0].Command[2]
	assert.Contains(t, script, "replica-announce-port 16379",
		"Sentinel init container must announce TLS port when TLS is enabled")
}

func TestBuildStatefulSet_HA_InitContainer_InjectsReplicaAnnouncePort_Plain(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
	script := sts.Spec.Template.Spec.InitContainers[0].Command[2]
	assert.Contains(t, script, "replica-announce-port 6379",
		"Sentinel init container must announce plain port when TLS is disabled")
}

func TestBuildStatefulSet_MultiReplica_InitContainer_InjectsReplicaAnnounceIP(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
	script := sts.Spec.Template.Spec.InitContainers[0].Command[2]
	assert.Contains(t, script, `replica-announce-ip $MY_HOST`,
		"Non-Sentinel init container must inject replica-announce-ip")
	assert.Contains(t, script, "replica-announce-port 6379",
		"Non-Sentinel init container must announce plain port")
}

func TestBuildStatefulSet_MultiReplica_InitContainer_InjectsReplicaAnnouncePort_TLS(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
	script := sts.Spec.Template.Spec.InitContainers[0].Command[2]
	assert.Contains(t, script, "replica-announce-port 16379",
		"Non-Sentinel init container must announce TLS port when TLS is enabled")
}

func TestBuildStatefulSet_Standalone_NoReplicaAnnounce(t *testing.T) {
	v := newTestValkey("test")

	sts := BuildStatefulSet(v, testOperatorImage)

	// Standalone has no init container — no replica-announce-ip injection.
	assert.Empty(t, sts.Spec.Template.Spec.InitContainers,
		"Standalone must not have init containers")
}

func TestBuildStatefulSet_HA_InitContainer_ReplicaAnnounceUsesCorrectFQDN(t *testing.T) {
	v := newTestValkey("myapp", func(v *vkov1.Valkey) {
		v.Namespace = "production"
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
	script := sts.Spec.Template.Spec.InitContainers[0].Command[2]
	// MY_HOST must use the correct headless service and namespace.
	assert.Contains(t, script, `MY_HOST="$HOSTNAME.myapp-headless.production.svc.cluster.local"`,
		"MY_HOST must use correct headless service name and namespace")
}

// --- Init container retry loop and known-master fallback (Phase 2: Prevention Hardening) ---

// TestBuildStatefulSet_HA_InitContainer_RetryLoop verifies that the init container
// script contains a retry loop with exponential backoff for Sentinel queries.
func TestBuildStatefulSet_HA_InitContainer_RetryLoop(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
	script := sts.Spec.Template.Spec.InitContainers[0].Command[2]

	assert.Contains(t, script, "MAX_WAIT=30",
		"Init container must retry Sentinel queries for up to 30 seconds")
	assert.Contains(t, script, "SLEEP=$((SLEEP * 2))",
		"Init container must use exponential backoff")
	assert.Contains(t, script, "break 2",
		"Init container must break out of both loops when Sentinel responds")
	assert.Contains(t, script, `while [ "$WAITED" -lt "$MAX_WAIT" ]`,
		"Init container must have a retry while-loop")
}

// TestBuildStatefulSet_HA_InitContainer_KnownMasterFallback verifies that the init
// container reads the known master from the replica ConfigMap when Sentinel is
// unavailable, before falling back to ordinal-based selection.
func TestBuildStatefulSet_HA_InitContainer_KnownMasterFallback(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
	script := sts.Spec.Template.Spec.InitContainers[0].Command[2]

	// Phase 2 fallback: read known master from replica ConfigMap.
	assert.Contains(t, script, "grep '^replicaof '",
		"Init container must parse replica ConfigMap for known master address")
	assert.Contains(t, script, "REPLICA_CONF_MASTER",
		"Init container must store the parsed master address")
	assert.Contains(t, script, "known master from replica config",
		"Init container must log when using replica config fallback")
}

// TestBuildStatefulSet_HA_InitContainer_OrdinalFallbackIsLastResort verifies that
// the ordinal-based fallback is the last resort, only used when both Sentinel and
// the replica ConfigMap fail to provide a master address.
func TestBuildStatefulSet_HA_InitContainer_OrdinalFallbackIsLastResort(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
	script := sts.Spec.Template.Spec.InitContainers[0].Command[2]

	assert.Contains(t, script, "All discovery methods exhausted",
		"Ordinal fallback must clearly indicate it is the last resort")
}

// TestBuildStatefulSet_HA_InitContainer_DynamicSentinelIndices verifies that the
// init container script uses the correct sentinel pod indices based on the
// sentinel replica count, instead of hardcoding "0 1 2".
func TestBuildStatefulSet_HA_InitContainer_DynamicSentinelIndices(t *testing.T) {
	tests := []struct {
		name     string
		replicas int32
		expected string
	}{
		{"3 sentinels", 3, "for i in 0 1 2; do"},
		{"5 sentinels", 5, "for i in 0 1 2 3 4; do"},
		{"1 sentinel", 1, "for i in 0; do"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newTestValkey("test", func(v *vkov1.Valkey) {
				v.Spec.Replicas = 3
				v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: tt.replicas}
			})

			sts := BuildStatefulSet(v, testOperatorImage)

			require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
			script := sts.Spec.Template.Spec.InitContainers[0].Command[2]
			assert.Contains(t, script, tt.expected,
				"Init container must iterate over correct sentinel indices")
		})
	}
}

// TestSentinelPodIndices verifies the sentinel pod indices helper.
func TestSentinelPodIndices(t *testing.T) {
	assert.Equal(t, "0 1 2", sentinelPodIndices(3))
	assert.Equal(t, "0 1 2 3 4", sentinelPodIndices(5))
	assert.Equal(t, "0", sentinelPodIndices(1))
}

// --- Non-Sentinel multi-replica master discovery init container ---

func TestBuildStatefulSet_MultiReplica_InitContainer_MasterDiscovery(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
	script := sts.Spec.Template.Spec.InitContainers[0].Command[2]

	assert.Contains(t, script, "Master discovery for non-Sentinel replication",
		"Init container must use master discovery")
	assert.Contains(t, script, "INFO replication",
		"Init container must query peer pods for replication info")
	assert.Contains(t, script, "connected_slaves",
		"Init container must check connected_slaves to find the real master")
	assert.Contains(t, script, "Discovered existing master",
		"Init container must log when a master is discovered")
}

func TestBuildStatefulSet_MultiReplica_InitContainer_OrdinalFallback(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
	script := sts.Spec.Template.Spec.InitContainers[0].Command[2]

	assert.Contains(t, script, "No existing master discovered, using ordinal-based config",
		"Init container must fall back to ordinal-based config when no master found")
}

func TestBuildStatefulSet_MultiReplica_InitContainer_RetryLoop(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
	script := sts.Spec.Template.Spec.InitContainers[0].Command[2]

	assert.Contains(t, script, "MAX_WAIT=15",
		"Init container must retry peer queries for up to 15 seconds")
	assert.Contains(t, script, "SLEEP=$((SLEEP * 2))",
		"Init container must use exponential backoff")
	assert.Contains(t, script, "break 2",
		"Init container must break out of both loops when master discovered")
}

func TestBuildStatefulSet_MultiReplica_InitContainer_UsesCorrectFQDN(t *testing.T) {
	v := newTestValkey("myapp", func(v *vkov1.Valkey) {
		v.Namespace = "production"
		v.Spec.Replicas = 3
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
	script := sts.Spec.Template.Spec.InitContainers[0].Command[2]

	assert.Contains(t, script, `HEADLESS="myapp-headless.production.svc.cluster.local"`,
		"Init container must use correct headless service name and namespace")
	assert.Contains(t, script, `STS_NAME="myapp"`,
		"Init container must use correct StatefulSet name")
	assert.Contains(t, script, "REPLICAS=3",
		"Init container must use correct replica count")
}

func TestBuildStatefulSet_MultiReplica_InitContainer_TLSFlags(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
	script := sts.Spec.Template.Spec.InitContainers[0].Command[2]

	assert.Contains(t, script, "--tls --cert /tls/tls.crt --key /tls/tls.key --cacert /tls/ca.crt",
		"Init container must include TLS flags when TLS is enabled")
	assert.Contains(t, script, "PORT=16379",
		"Init container must use TLS port when TLS is enabled")
}

func TestBuildStatefulSet_MultiReplica_InitContainer_NoTLSFlags(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
	script := sts.Spec.Template.Spec.InitContainers[0].Command[2]

	assert.NotContains(t, script, "--tls",
		"Init container must not include TLS flags when TLS is disabled")
	assert.Contains(t, script, "PORT=6379",
		"Init container must use plain port when TLS is disabled")
}

func TestBuildStatefulSet_MultiReplica_InitContainer_AuthEnvVar(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
	initC := sts.Spec.Template.Spec.InitContainers[0]

	// Auth env var must be injected.
	var hasAuthEnv bool
	for _, env := range initC.Env {
		if env.Name == AuthSecretEnvName {
			require.NotNil(t, env.ValueFrom)
			require.NotNil(t, env.ValueFrom.SecretKeyRef)
			assert.Equal(t, "my-secret", env.ValueFrom.SecretKeyRef.Name)
			assert.Equal(t, "password", env.ValueFrom.SecretKeyRef.Key)
			hasAuthEnv = true
		}
	}
	assert.True(t, hasAuthEnv, "Init container must have VALKEY_PASSWORD env var when auth enabled")

	// Script must include auth flags.
	script := initC.Command[2]
	assert.Contains(t, script, "--no-auth-warning",
		"Init container must use auth flags for valkey-cli when auth enabled")
}

func TestBuildStatefulSet_MultiReplica_InitContainer_NoAuthEnvVar(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
	initC := sts.Spec.Template.Spec.InitContainers[0]

	assert.Empty(t, initC.Env,
		"Init container must not have env vars when auth is disabled")

	script := initC.Command[2]
	assert.NotContains(t, script, "no-auth-warning",
		"Init container must not use auth flags when auth is disabled")
}

func TestBuildStatefulSet_MultiReplica_InitContainer_SkipsSelf(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})

	sts := BuildStatefulSet(v, testOperatorImage)

	require.NotEmpty(t, sts.Spec.Template.Spec.InitContainers)
	script := sts.Spec.Template.Spec.InitContainers[0].Command[2]

	assert.Contains(t, script, `if [ "$PEER" = "$MY_HOST" ]; then`,
		"Init container must skip querying itself")
}
