package builder

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

const (
	// SentinelPort is the default Sentinel port.
	SentinelPort = 26379

	// SentinelConfigKey is the key used in the ConfigMap for sentinel configuration.
	SentinelConfigKey = "sentinel.conf"

	// SentinelContainerName is the name of the Sentinel container.
	SentinelContainerName = "sentinel"

	// SentinelConfigVolumeName is the name of the writable sentinel config volume.
	SentinelConfigVolumeName = "sentinel-config"

	// SentinelConfigMountPath is the mount path for the sentinel configuration.
	SentinelConfigMountPath = "/etc/sentinel"

	// SentinelDataDir is the working directory for Sentinel.
	SentinelDataDir = "/data"

	// SentinelQuorum is the default number of Sentinels that need to agree for failover.
	SentinelQuorum = 2

	// SentinelDownAfterMilliseconds is the default time before a master is considered down.
	SentinelDownAfterMilliseconds = 5000

	// SentinelFailoverTimeout is the default failover timeout.
	SentinelFailoverTimeout = 60000

	// SentinelParallelSyncs is the number of replicas that can sync simultaneously after failover.
	SentinelParallelSyncs = 1
)

// SentinelConfigMapName returns the name for the Sentinel ConfigMap.
func SentinelConfigMapName(v *vkov1.Valkey) string {
	return fmt.Sprintf("%s-sentinel-config", v.Name)
}

// SentinelMonitorName returns the name used for the `sentinel monitor` directive.
func SentinelMonitorName(v *vkov1.Valkey) string {
	return v.Name
}

// GenerateSentinelConf generates the sentinel.conf content based on the CRD spec.
func GenerateSentinelConf(v *vkov1.Valkey) string {
	var lines []string

	masterAddr := MasterAddress(v)
	monitorName := SentinelMonitorName(v)

	// Calculate quorum: majority of sentinel replicas.
	quorum := SentinelQuorum
	if v.Spec.Sentinel != nil && v.Spec.Sentinel.Replicas > 0 {
		quorum = int(v.Spec.Sentinel.Replicas/2) + 1
	}

	lines = append(lines,
		"# Sentinel configuration",
		fmt.Sprintf("port %d", SentinelPort),
		fmt.Sprintf("dir %s", SentinelDataDir),
		"",
		"# Monitor configuration",
		fmt.Sprintf("sentinel monitor %s %s %d %d", monitorName, masterAddr, ValkeyPort, quorum),
		fmt.Sprintf("sentinel down-after-milliseconds %s %d", monitorName, SentinelDownAfterMilliseconds),
		fmt.Sprintf("sentinel failover-timeout %s %d", monitorName, SentinelFailoverTimeout),
		fmt.Sprintf("sentinel parallel-syncs %s %d", monitorName, SentinelParallelSyncs),
		"",
		"# Resolve hostnames — needed for Kubernetes DNS-based pod discovery",
		"sentinel resolve-hostnames yes",
		"sentinel announce-hostnames yes",
		"",
	)

	// Auth configuration if enabled.
	if v.IsAuthEnabled() {
		lines = append(lines,
			"# Auth (password will be set via runtime injection)",
			fmt.Sprintf("# sentinel auth-pass %s <password>", monitorName),
			"",
		)
	}

	return strings.Join(lines, "\n")
}

// BuildSentinelConfigMap builds the ConfigMap for Sentinel configuration.
func BuildSentinelConfigMap(v *vkov1.Valkey) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SentinelConfigMapName(v),
			Namespace: v.Namespace,
			Labels:    common.BaseLabels(v, common.ComponentSentinel),
		},
		Data: map[string]string{
			SentinelConfigKey: GenerateSentinelConf(v),
		},
	}
}

// BuildSentinelStatefulSet builds the StatefulSet for Sentinel instances.
func BuildSentinelStatefulSet(v *vkov1.Valkey) *appsv1.StatefulSet {
	labels := common.BaseLabels(v, common.ComponentSentinel)
	selectorLabels := common.SelectorLabels(v, common.ComponentSentinel)

	// Merge base labels with sentinel-specific user labels.
	var sentinelPodLabels map[string]string
	if v.Spec.Sentinel != nil {
		sentinelPodLabels = common.MergeLabels(labels, v.Spec.Sentinel.PodLabels)
	} else {
		sentinelPodLabels = labels
	}

	// Only set annotations if there are user-defined sentinel ones.
	var podAnnotations map[string]string
	if v.Spec.Sentinel != nil && len(v.Spec.Sentinel.PodAnnotations) > 0 {
		podAnnotations = common.MergeAnnotations(v.Spec.Sentinel.PodAnnotations)
	}

	replicas := int32(3)
	if v.Spec.Sentinel != nil && v.Spec.Sentinel.Replicas > 0 {
		replicas = v.Spec.Sentinel.Replicas
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      common.StatefulSetName(v, common.ComponentSentinel),
			Namespace: v.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:            &replicas,
			ServiceName:         common.HeadlessServiceName(v, common.ComponentSentinel),
			PodManagementPolicy: appsv1.ParallelPodManagement,
			Selector: &metav1.LabelSelector{
				MatchLabels: selectorLabels,
			},
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.RollingUpdateStatefulSetStrategyType,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      sentinelPodLabels,
					Annotations: podAnnotations,
				},
				Spec: buildSentinelPodSpec(v),
			},
		},
	}

	return sts
}

// buildSentinelPodSpec builds the PodSpec for Sentinel pods.
func buildSentinelPodSpec(v *vkov1.Valkey) corev1.PodSpec {
	return corev1.PodSpec{
		ServiceAccountName: "default",
		// Init container copies the sentinel config to a writable volume.
		// Sentinel needs to rewrite its config file at runtime.
		InitContainers: []corev1.Container{
			{
				Name:  "init-sentinel-config",
				Image: v.Spec.Image,
				Command: []string{
					"sh", "-c",
					fmt.Sprintf("cp /etc/sentinel-readonly/%s %s/%s", SentinelConfigKey, SentinelConfigMountPath, SentinelConfigKey),
				},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "sentinel-config-readonly",
						MountPath: "/etc/sentinel-readonly",
						ReadOnly:  true,
					},
					{
						Name:      SentinelConfigVolumeName,
						MountPath: SentinelConfigMountPath,
					},
				},
			},
		},
		Containers: []corev1.Container{
			buildSentinelContainer(v),
		},
		Volumes: []corev1.Volume{
			{
				Name: "sentinel-config-readonly",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: SentinelConfigMapName(v),
						},
					},
				},
			},
			{
				Name: SentinelConfigVolumeName,
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: DataVolumeName,
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		},
	}
}

// buildSentinelContainer builds the Sentinel container spec.
func buildSentinelContainer(v *vkov1.Valkey) corev1.Container {
	return corev1.Container{
		Name:  SentinelContainerName,
		Image: v.Spec.Image,
		Command: []string{
			"valkey-sentinel",
			SentinelConfigMountPath + "/" + SentinelConfigKey,
		},
		Ports: []corev1.ContainerPort{
			{
				Name:          "sentinel",
				ContainerPort: SentinelPort,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      SentinelConfigVolumeName,
				MountPath: SentinelConfigMountPath,
			},
			{
				Name:      DataVolumeName,
				MountPath: SentinelDataDir,
			},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt32(SentinelPort),
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
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt32(SentinelPort),
				},
			},
			InitialDelaySeconds: 15,
			PeriodSeconds:       10,
			TimeoutSeconds:      5,
			SuccessThreshold:    1,
			FailureThreshold:    5,
		},
	}
}

// SentinelStatefulSetHasChanged returns true if the live Sentinel StatefulSet differs from desired.
func SentinelStatefulSetHasChanged(desired, current *appsv1.StatefulSet) bool {
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

	return false
}
