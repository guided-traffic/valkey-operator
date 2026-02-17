package builder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// --- SentinelConfigMapName ---

func TestSentinelConfigMapName(t *testing.T) {
	v := newTestValkey("my-valkey")
	assert.Equal(t, "my-valkey-sentinel-config", SentinelConfigMapName(v))
}

// --- SentinelMonitorName ---

func TestSentinelMonitorName(t *testing.T) {
	v := newTestValkey("my-cluster")
	assert.Equal(t, "my-cluster", SentinelMonitorName(v))
}

// --- GenerateSentinelConf ---

func TestGenerateSentinelConf_Default(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	conf := GenerateSentinelConf(v)

	assert.Contains(t, conf, "port 26379")
	assert.Contains(t, conf, "dir /data")
	assert.Contains(t, conf, "sentinel monitor test")
	assert.Contains(t, conf, "6379 2") // port + quorum (3/2 + 1 = 2)
	assert.Contains(t, conf, "sentinel down-after-milliseconds test 5000")
	assert.Contains(t, conf, "sentinel failover-timeout test 60000")
	assert.Contains(t, conf, "sentinel parallel-syncs test 1")
	assert.Contains(t, conf, "sentinel resolve-hostnames yes")
	assert.Contains(t, conf, "sentinel announce-hostnames yes")
}

func TestGenerateSentinelConf_Quorum5Replicas(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 5,
		}
	})

	conf := GenerateSentinelConf(v)

	// Quorum for 5 sentinels: 5/2 + 1 = 3
	assert.Contains(t, conf, "6379 3")
}

func TestGenerateSentinelConf_WithAuth(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
	})

	conf := GenerateSentinelConf(v)

	assert.Contains(t, conf, "# Auth")
	assert.Contains(t, conf, "sentinel auth-pass")
}

func TestGenerateSentinelConf_MasterAddress(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "prod"
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	conf := GenerateSentinelConf(v)

	// Master address should be the DNS name of pod-0.
	assert.Contains(t, conf, "test-0.test-headless.prod.svc.cluster.local")
}

// --- BuildSentinelConfigMap ---

func TestBuildSentinelConfigMap(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	cm := BuildSentinelConfigMap(v)

	assert.Equal(t, "test-sentinel-config", cm.Name)
	assert.Equal(t, "default", cm.Namespace)
	assert.Contains(t, cm.Data, SentinelConfigKey)
	assert.NotEmpty(t, cm.Data[SentinelConfigKey])

	// Labels should have sentinel component.
	assert.Equal(t, "sentinel", cm.Labels["app.kubernetes.io/component"])
	assert.Equal(t, "test", cm.Labels["app.kubernetes.io/instance"])
}

// --- BuildSentinelStatefulSet ---

func TestBuildSentinelStatefulSet_Default(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	sts := BuildSentinelStatefulSet(v)

	assert.Equal(t, "test-sentinel", sts.Name)
	assert.Equal(t, "default", sts.Namespace)
	assert.Equal(t, int32(3), *sts.Spec.Replicas)
	assert.Equal(t, "test-sentinel-headless", sts.Spec.ServiceName)

	// Labels.
	assert.Equal(t, "sentinel", sts.Labels["app.kubernetes.io/component"])

	// Pod template.
	assert.Equal(t, "sentinel", sts.Spec.Template.Labels["app.kubernetes.io/component"])

	// Container.
	require.Len(t, sts.Spec.Template.Spec.Containers, 1)
	container := sts.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "sentinel", container.Name)
	assert.Equal(t, "valkey/valkey:8.0", container.Image)
	assert.Equal(t, []string{"valkey-sentinel", SentinelConfigMountPath + "/" + SentinelConfigKey}, container.Command)

	// Ports.
	require.Len(t, container.Ports, 1)
	assert.Equal(t, int32(26379), container.Ports[0].ContainerPort)
	assert.Equal(t, "sentinel", container.Ports[0].Name)

	// Probes.
	assert.NotNil(t, container.ReadinessProbe)
	assert.NotNil(t, container.LivenessProbe)
	assert.NotNil(t, container.ReadinessProbe.TCPSocket)
}

func TestBuildSentinelStatefulSet_InitContainer(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	sts := BuildSentinelStatefulSet(v)

	// Init container copies sentinel config to writable volume.
	require.Len(t, sts.Spec.Template.Spec.InitContainers, 1)
	init := sts.Spec.Template.Spec.InitContainers[0]
	assert.Equal(t, "init-sentinel-config", init.Name)
	assert.Equal(t, "valkey/valkey:8.0", init.Image)
	assert.Contains(t, init.Command[2], "cp")
}

func TestBuildSentinelStatefulSet_Volumes(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	sts := BuildSentinelStatefulSet(v)

	volumes := sts.Spec.Template.Spec.Volumes
	require.Len(t, volumes, 3) // readonly config, writable config, data

	assert.Equal(t, "sentinel-config-readonly", volumes[0].Name)
	assert.Equal(t, "test-sentinel-config", volumes[0].ConfigMap.Name)
	assert.Equal(t, SentinelConfigVolumeName, volumes[1].Name)
	assert.NotNil(t, volumes[1].EmptyDir)
	assert.Equal(t, DataVolumeName, volumes[2].Name)
	assert.NotNil(t, volumes[2].EmptyDir)
}

func TestBuildSentinelStatefulSet_CustomPodLabels(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
			PodLabels: map[string]string{
				"app": "sentinel",
			},
		}
	})

	sts := BuildSentinelStatefulSet(v)

	assert.Equal(t, "sentinel", sts.Spec.Template.Labels["app"])
	// Operator labels should still be present.
	assert.Equal(t, "sentinel", sts.Spec.Template.Labels["app.kubernetes.io/component"])
}

func TestBuildSentinelStatefulSet_CustomPodAnnotations(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:    true,
			Replicas:   3,
			PodAnnotations: map[string]string{
				"example.com/sentinel": "true",
			},
		}
	})

	sts := BuildSentinelStatefulSet(v)

	assert.Equal(t, "true", sts.Spec.Template.Annotations["example.com/sentinel"])
}

func TestBuildSentinelStatefulSet_NilAnnotationsWhenEmpty(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	sts := BuildSentinelStatefulSet(v)

	assert.Nil(t, sts.Spec.Template.Annotations)
}

func TestBuildSentinelStatefulSet_RollingUpdateStrategy(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	sts := BuildSentinelStatefulSet(v)

	// Sentinel uses standard rolling update (unlike Valkey which uses OnDelete).
	assert.Equal(t, "RollingUpdate", string(sts.Spec.UpdateStrategy.Type))
}

// --- SentinelStatefulSetHasChanged ---

func TestSentinelStatefulSetHasChanged_NoChange(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	desired := BuildSentinelStatefulSet(v)
	current := desired.DeepCopy()

	assert.False(t, SentinelStatefulSetHasChanged(desired, current))
}

func TestSentinelStatefulSetHasChanged_ReplicaChange(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	desired := BuildSentinelStatefulSet(v)
	current := desired.DeepCopy()

	// Simulate current having different replicas.
	oldReplicas := int32(5)
	current.Spec.Replicas = &oldReplicas

	assert.True(t, SentinelStatefulSetHasChanged(desired, current))
}

func TestSentinelStatefulSetHasChanged_ImageChange(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	desired := BuildSentinelStatefulSet(v)
	current := desired.DeepCopy()

	current.Spec.Template.Spec.Containers[0].Image = "valkey/valkey:7.0"

	assert.True(t, SentinelStatefulSetHasChanged(desired, current))
}

func TestSentinelStatefulSetHasChanged_LabelChange(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	desired := BuildSentinelStatefulSet(v)
	current := desired.DeepCopy()

	current.Spec.Template.Labels["extra"] = "label"

	assert.True(t, SentinelStatefulSetHasChanged(desired, current))
}

// --- MasterAddress ---

func TestMasterAddress(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "prod"
	})

	addr := MasterAddress(v)
	assert.Equal(t, "test-0.test-headless.prod.svc.cluster.local", addr)
}

func TestMasterAddress_DefaultNamespace(t *testing.T) {
	v := newTestValkey("my-valkey")

	addr := MasterAddress(v)
	assert.Equal(t, "my-valkey-0.my-valkey-headless.default.svc.cluster.local", addr)
}

// --- Replication Config ---

func TestGenerateValkeyConf_HAMasterConfig(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	conf := GenerateValkeyConf(v, false) // Master config.

	// Master should have replication section but no replicaof.
	assert.Contains(t, conf, "# Replication")
	assert.NotContains(t, conf, "replicaof")
	assert.Contains(t, conf, "replica-serve-stale-data yes")
	assert.Contains(t, conf, "replica-read-only yes")
	assert.Contains(t, conf, "repl-diskless-sync yes")
}

func TestGenerateValkeyConf_HAReplicaConfig(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "prod"
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	conf := GenerateValkeyConf(v, true) // Replica config.

	// Replica should have replicaof pointing to master.
	assert.Contains(t, conf, "replicaof test-0.test-headless.prod.svc.cluster.local 6379")
	assert.Contains(t, conf, "replica-serve-stale-data yes")
	assert.Contains(t, conf, "replica-read-only yes")
}

func TestGenerateValkeyConf_StandaloneNoReplication(t *testing.T) {
	v := newTestValkey("test")

	conf := GenerateValkeyConf(v, false)

	// Standalone should NOT have replication section.
	assert.NotContains(t, conf, "# Replication")
	assert.NotContains(t, conf, "replicaof")
}

// --- Replica ConfigMap ---

func TestReplicaConfigMapName(t *testing.T) {
	v := newTestValkey("my-valkey")
	assert.Equal(t, "my-valkey-replica-config", ReplicaConfigMapName(v))
}

func TestBuildReplicaConfigMap(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	cm := BuildReplicaConfigMap(v)

	assert.Equal(t, "test-replica-config", cm.Name)
	assert.Contains(t, cm.Data, ValkeyConfigKey)
	assert.Contains(t, cm.Data[ValkeyConfigKey], "replicaof")
}

// --- HA StatefulSet ---

func TestBuildStatefulSet_HAMode(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	sts := BuildStatefulSet(v)

	// Should have init container for config selection.
	require.Len(t, sts.Spec.Template.Spec.InitContainers, 1)
	init := sts.Spec.Template.Spec.InitContainers[0]
	assert.Equal(t, "init-config-selector", init.Name)
	assert.Contains(t, init.Command[2], "ORDINAL")

	// Container should use the writable config mount path.
	container := sts.Spec.Template.Spec.Containers[0]
	assert.Contains(t, container.Command[1], WritableConfigMountPath)

	// Should have master config, replica config, and writable config volumes.
	volumeNames := make([]string, 0)
	for _, vol := range sts.Spec.Template.Spec.Volumes {
		volumeNames = append(volumeNames, vol.Name)
	}
	assert.Contains(t, volumeNames, ConfigVolumeName)
	assert.Contains(t, volumeNames, ReplicaConfigVolumeName)
	assert.Contains(t, volumeNames, WritableConfigVolumeName)
}

func TestBuildStatefulSet_StandaloneNoInitContainer(t *testing.T) {
	v := newTestValkey("test")

	sts := BuildStatefulSet(v)

	// Standalone should NOT have init container.
	assert.Nil(t, sts.Spec.Template.Spec.InitContainers)

	// Container should use the direct config mount path.
	container := sts.Spec.Template.Spec.Containers[0]
	assert.Contains(t, container.Command[1], ConfigMountPath)
	assert.NotContains(t, container.Command[1], WritableConfigMountPath)
}

// --- Read Service ---

func TestBuildReadService(t *testing.T) {
	v := newTestValkey("test")
	svc := BuildReadService(v)

	assert.Equal(t, "test-read", svc.Name)
	assert.Equal(t, "default", svc.Namespace)
	assert.Len(t, svc.Spec.Ports, 1)
	assert.Equal(t, int32(ValkeyPort), svc.Spec.Ports[0].Port)
}
