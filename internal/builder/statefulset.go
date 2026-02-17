package builder

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

const (
	// ValkeyContainerName is the name of the main Valkey container.
	ValkeyContainerName = "valkey"

	// ConfigVolumeName is the name of the volume for the master Valkey configuration (readonly).
	ConfigVolumeName = "config"

	// ReplicaConfigVolumeName is the name of the volume for the replica configuration (readonly, HA mode).
	ReplicaConfigVolumeName = "replica-config"

	// WritableConfigVolumeName is the name of the writable config volume (HA mode, populated by init container).
	WritableConfigVolumeName = "writable-config"

	// DataVolumeName is the name of the volume for persistent data.
	DataVolumeName = "data"

	// ConfigMountPath is the mount path for the master Valkey configuration (readonly).
	ConfigMountPath = "/etc/valkey"

	// ReplicaConfigMountPath is the mount path for the replica configuration (readonly, HA mode).
	ReplicaConfigMountPath = "/etc/valkey-replica"

	// WritableConfigMountPath is the mount path for the writable config (HA mode).
	WritableConfigMountPath = "/etc/valkey-active"
)

// BuildStatefulSet builds the StatefulSet for Valkey instances.
func BuildStatefulSet(v *vkov1.Valkey) *appsv1.StatefulSet {
	labels := common.BaseLabels(v, common.ComponentValkey)
	selectorLabels := common.SelectorLabels(v, common.ComponentValkey)
	podLabels := common.MergeLabels(labels, v.Spec.PodLabels)

	// Only set annotations if there are user-defined ones.
	var podAnnotations map[string]string
	if len(v.Spec.PodAnnotations) > 0 {
		podAnnotations = common.MergeAnnotations(v.Spec.PodAnnotations)
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      common.StatefulSetName(v, common.ComponentValkey),
			Namespace: v.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:            &v.Spec.Replicas,
			ServiceName:         common.HeadlessServiceName(v, common.ComponentValkey),
			PodManagementPolicy: appsv1.ParallelPodManagement,
			Selector: &metav1.LabelSelector{
				MatchLabels: selectorLabels,
			},
			// Disable default rolling update — operator handles pod-by-pod rollout.
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.OnDeleteStatefulSetStrategyType,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      podLabels,
					Annotations: podAnnotations,
				},
				Spec: buildPodSpec(v),
			},
		},
	}

	// Add PVC template if persistence is enabled.
	if v.IsPersistenceEnabled() {
		sts.Spec.VolumeClaimTemplates = buildVolumeClaimTemplates(v)
	}

	return sts
}

// buildPodSpec constructs the PodSpec for Valkey pods.
func buildPodSpec(v *vkov1.Valkey) corev1.PodSpec {
	volumes := []corev1.Volume{
		{
			Name: ConfigVolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: ConfigMapName(v),
					},
				},
			},
		},
	}

	var initContainers []corev1.Container

	// In HA mode, use an init container to select the right config (master vs replica).
	if v.IsSentinelEnabled() {
		// Add replica config volume.
		volumes = append(volumes, corev1.Volume{
			Name: ReplicaConfigVolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: ReplicaConfigMapName(v),
					},
				},
			},
		})

		// Add writable config volume (init container will copy the right config here).
		volumes = append(volumes, corev1.Volume{
			Name: WritableConfigVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})

		// Init container selects master or replica config based on pod ordinal.
		initContainers = append(initContainers, corev1.Container{
			Name:  "init-config-selector",
			Image: v.Spec.Image,
			Command: []string{
				"sh", "-c",
				// Pod-0 is the initial master; all others are replicas.
				fmt.Sprintf(
					`ORDINAL=$(echo $HOSTNAME | rev | cut -d'-' -f1 | rev)
if [ "$ORDINAL" = "0" ]; then
  cp %s/%s %s/%s
else
  cp %s/%s %s/%s
fi`,
					ConfigMountPath, ValkeyConfigKey, WritableConfigMountPath, ValkeyConfigKey,
					ReplicaConfigMountPath, ValkeyConfigKey, WritableConfigMountPath, ValkeyConfigKey,
				),
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      ConfigVolumeName,
					MountPath: ConfigMountPath,
					ReadOnly:  true,
				},
				{
					Name:      ReplicaConfigVolumeName,
					MountPath: ReplicaConfigMountPath,
					ReadOnly:  true,
				},
				{
					Name:      WritableConfigVolumeName,
					MountPath: WritableConfigMountPath,
				},
			},
		})
	}

	// If persistence is NOT enabled, use an emptyDir for data.
	if !v.IsPersistenceEnabled() {
		volumes = append(volumes, corev1.Volume{
			Name: DataVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})
	}

	spec := corev1.PodSpec{
		ServiceAccountName: "default",
		Containers: []corev1.Container{
			buildValkeyContainer(v),
		},
		Volumes: volumes,
	}

	if len(initContainers) > 0 {
		spec.InitContainers = initContainers
	}

	return spec
}

// configMountForContainer returns the config mount path used by the valkey container.
// In HA mode, this is the writable config directory (populated by init container).
// In standalone mode, this is the readonly ConfigMap mount.
func configMountForContainer(v *vkov1.Valkey) string {
	if v.IsSentinelEnabled() {
		return WritableConfigMountPath
	}
	return ConfigMountPath
}

// configVolumeNameForContainer returns the volume name to mount for the valkey config.
func configVolumeNameForContainer(v *vkov1.Valkey) string {
	if v.IsSentinelEnabled() {
		return WritableConfigVolumeName
	}
	return ConfigVolumeName
}

// buildValkeyContainer builds the main Valkey container spec.
func buildValkeyContainer(v *vkov1.Valkey) corev1.Container {
	cfgMount := configMountForContainer(v)
	cfgVolume := configVolumeNameForContainer(v)

	volumeMounts := []corev1.VolumeMount{
		{
			Name:      cfgVolume,
			MountPath: cfgMount,
			ReadOnly:  !v.IsSentinelEnabled(), // Writable in HA mode (init container writes here).
		},
		{
			Name:      DataVolumeName,
			MountPath: DataDir,
		},
	}

	container := corev1.Container{
		Name:  ValkeyContainerName,
		Image: v.Spec.Image,
		Command: []string{
			"valkey-server",
			cfgMount + "/" + ValkeyConfigKey,
		},
		Ports: []corev1.ContainerPort{
			{
				Name:          "valkey",
				ContainerPort: ValkeyPort,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		VolumeMounts: volumeMounts,
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"valkey-cli", "ping"},
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       5,
			TimeoutSeconds:      3,
			SuccessThreshold:    1,
			FailureThreshold:    3,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"valkey-cli", "ping"},
				},
			},
			InitialDelaySeconds: 15,
			PeriodSeconds:       10,
			TimeoutSeconds:      5,
			SuccessThreshold:    1,
			FailureThreshold:    5,
		},
		Resources: v.Spec.Resources,
	}

	return container
}

// buildVolumeClaimTemplates creates PVC templates for persistent storage.
func buildVolumeClaimTemplates(v *vkov1.Valkey) []corev1.PersistentVolumeClaim {
	storageSize := resource.MustParse("1Gi")
	if v.Spec.Persistence != nil && !v.Spec.Persistence.Size.IsZero() {
		storageSize = v.Spec.Persistence.Size
	}

	pvc := corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:   DataVolumeName,
			Labels: common.BaseLabels(v, common.ComponentValkey),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: storageSize,
				},
			},
		},
	}

	// Set StorageClass if specified.
	if v.Spec.Persistence != nil && v.Spec.Persistence.StorageClass != "" {
		sc := v.Spec.Persistence.StorageClass
		pvc.Spec.StorageClassName = &sc
	}

	return []corev1.PersistentVolumeClaim{pvc}
}

// StatefulSetHasChanged returns true if the live StatefulSet differs from the desired spec
// in ways that require an update (image, replicas, resources, config).
func StatefulSetHasChanged(desired, current *appsv1.StatefulSet) bool {
	// Check replicas.
	if desired.Spec.Replicas != nil && current.Spec.Replicas != nil {
		if *desired.Spec.Replicas != *current.Spec.Replicas {
			return true
		}
	}

	// Check container image.
	if len(desired.Spec.Template.Spec.Containers) > 0 && len(current.Spec.Template.Spec.Containers) > 0 {
		if desired.Spec.Template.Spec.Containers[0].Image != current.Spec.Template.Spec.Containers[0].Image {
			return true
		}
	}

	// Check labels on pod template.
	desiredLabels := desired.Spec.Template.Labels
	currentLabels := current.Spec.Template.Labels
	if len(desiredLabels) != len(currentLabels) {
		return true
	}
	for k, v := range desiredLabels {
		if currentLabels[k] != v {
			return true
		}
	}

	// Check annotations on pod template.
	desiredAnnotations := desired.Spec.Template.Annotations
	currentAnnotations := current.Spec.Template.Annotations
	if len(desiredAnnotations) != len(currentAnnotations) {
		return true
	}
	for k, v := range desiredAnnotations {
		if currentAnnotations[k] != v {
			return true
		}
	}

	// Check resource requirements.
	if len(desired.Spec.Template.Spec.Containers) > 0 && len(current.Spec.Template.Spec.Containers) > 0 {
		dRes := desired.Spec.Template.Spec.Containers[0].Resources
		cRes := current.Spec.Template.Spec.Containers[0].Resources

		if resourceListChanged(dRes.Requests, cRes.Requests) || resourceListChanged(dRes.Limits, cRes.Limits) {
			return true
		}
	}

	return false
}

// resourceListChanged returns true if two resource lists differ.
func resourceListChanged(a, b corev1.ResourceList) bool {
	if len(a) != len(b) {
		return true
	}
	for key, aVal := range a {
		bVal, ok := b[key]
		if !ok || aVal.Cmp(bVal) != 0 {
			return true
		}
	}
	return false
}

// ServicePort returns the Valkey client port, accounting for TLS configuration.
func ServicePort(v *vkov1.Valkey) int32 {
	if v.IsTLSEnabled() {
		return int32(ValkeyPort + 10000)
	}
	return int32(ValkeyPort)
}

// ProbeCommand returns the probe command, accounting for TLS.
func ProbeCommand(v *vkov1.Valkey) []string {
	if v.IsTLSEnabled() {
		return []string{
			"valkey-cli",
			"--tls",
			"--cert", "/tls/tls.crt",
			"--key", "/tls/tls.key",
			"--cacert", "/tls/ca.crt",
			"-p", "16379",
			"ping",
		}
	}
	return []string{"valkey-cli", "ping"}
}

// DesiredServicePort returns the port spec for Services, accounting for TLS.
func DesiredServicePort(v *vkov1.Valkey) corev1.ServicePort {
	return corev1.ServicePort{
		Name:       "valkey",
		Port:       ServicePort(v),
		TargetPort: intstr.FromString("valkey"),
		Protocol:   corev1.ProtocolTCP,
	}
}
