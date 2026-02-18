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

// --- BuildStatefulSet ---

func TestBuildStatefulSet_Standalone(t *testing.T) {
	v := newTestValkey("test")

	sts := BuildStatefulSet(v)

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
	require.Len(t, sts.Spec.Template.Spec.Containers, 1)
	container := sts.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "valkey", container.Name)
	assert.Equal(t, "valkey/valkey:8.0", container.Image)

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

	sts := BuildStatefulSet(v)

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

	sts := BuildStatefulSet(v)

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

	sts := BuildStatefulSet(v)

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

	sts := BuildStatefulSet(v)

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

	sts := BuildStatefulSet(v)

	assert.Equal(t, int32(3), *sts.Spec.Replicas)
}

func TestBuildStatefulSet_ConfigMapReference(t *testing.T) {
	v := newTestValkey("my-cluster")

	sts := BuildStatefulSet(v)

	configVol := sts.Spec.Template.Spec.Volumes[0]
	assert.Equal(t, ConfigVolumeName, configVol.Name)
	require.NotNil(t, configVol.ConfigMap)
	assert.Equal(t, "my-cluster-config", configVol.ConfigMap.Name)
}

// --- StatefulSetHasChanged ---

func TestStatefulSetHasChanged_NoChange(t *testing.T) {
	v := newTestValkey("test")
	desired := BuildStatefulSet(v)

	// Clone as current.
	current := desired.DeepCopy()

	assert.False(t, StatefulSetHasChanged(desired, current))
}

func TestStatefulSetHasChanged_ReplicaChange(t *testing.T) {
	v := newTestValkey("test")
	desired := BuildStatefulSet(v)
	current := desired.DeepCopy()

	newReplicas := int32(3)
	desired.Spec.Replicas = &newReplicas

	assert.True(t, StatefulSetHasChanged(desired, current))
}

func TestStatefulSetHasChanged_ImageChange(t *testing.T) {
	v := newTestValkey("test")
	desired := BuildStatefulSet(v)
	current := desired.DeepCopy()

	desired.Spec.Template.Spec.Containers[0].Image = "valkey/valkey:9.0"

	assert.True(t, StatefulSetHasChanged(desired, current))
}

func TestStatefulSetHasChanged_LabelChange(t *testing.T) {
	v := newTestValkey("test")
	desired := BuildStatefulSet(v)
	current := desired.DeepCopy()

	desired.Spec.Template.Labels["new-label"] = "value"

	assert.True(t, StatefulSetHasChanged(desired, current))
}

func TestStatefulSetHasChanged_AnnotationChange(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.PodAnnotations = map[string]string{"a": "1"}
	})
	desired := BuildStatefulSet(v)
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
	desired := BuildStatefulSet(v)
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

	sts := BuildStatefulSet(v)

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

	sts := BuildStatefulSet(v)

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

	sts := BuildStatefulSet(v)
	svc := BuildHeadlessService(v)

	// StatefulSet selector must match Service selector for DNS to work.
	for k, val := range sts.Spec.Selector.MatchLabels {
		assert.Equal(t, val, svc.Spec.Selector[k], "selector label %s must match between StatefulSet and Service", k)
	}
}

// --- ParallelPodManagement ---

func TestBuildStatefulSet_ParallelPodManagement(t *testing.T) {
	v := newTestValkey("test")

	sts := BuildStatefulSet(v)

	assert.Equal(t, appsv1.ParallelPodManagement, sts.Spec.PodManagementPolicy)
}

// --- OnDelete UpdateStrategy ---

func TestBuildStatefulSet_OnDeleteUpdateStrategy(t *testing.T) {
	v := newTestValkey("test")

	sts := BuildStatefulSet(v)

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

	sts := BuildStatefulSet(v)

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

	sts := BuildStatefulSet(v)

	container := sts.Spec.Template.Spec.Containers[0]

	require.Len(t, container.Env, 1)
	assert.Equal(t, "custom-auth", container.Env[0].ValueFrom.SecretKeyRef.Name)
	assert.Equal(t, "redis-pass", container.Env[0].ValueFrom.SecretKeyRef.Key)
}

func TestBuildStatefulSet_WithoutAuth_NoEnvVars(t *testing.T) {
	v := newTestValkey("test")

	sts := BuildStatefulSet(v)

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

	sts := BuildStatefulSet(v)

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

	sts := BuildStatefulSet(v)

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
	desired := BuildStatefulSet(v)
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
	desired := BuildStatefulSet(v)
	current := desired.DeepCopy()

	// Simulate command change (auth flags added).
	desired.Spec.Template.Spec.Containers[0].Command = []string{"sh", "-c", "exec valkey-server /etc/valkey/valkey.conf --requirepass \"$VALKEY_PASSWORD\""}

	assert.True(t, StatefulSetHasChanged(desired, current))
}
