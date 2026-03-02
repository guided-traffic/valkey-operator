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

	// AnnotationKnownMaster is the annotation key used to persist the post-failover
	// master address on the Valkey CR. When present, GenerateSentinelConf uses this
	// address instead of the default pod-0 address. This ensures that if a sentinel
	// pod restarts after a failover, it reads the correct master from the ConfigMap
	// rather than falling back to the stale pod-0 default.
	AnnotationKnownMaster = "vko.gtrfc.com/known-master"

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
// If the Valkey CR carries the AnnotationKnownMaster annotation (set by the
// operator after a successful sentinel failover), that address is used as the
// sentinel monitor target instead of the default pod-0 DNS address. This ensures
// that sentinel pods which restart after a rolling-update failover immediately
// connect to the actual current master rather than a stale pod-0 replica.
func GenerateSentinelConf(v *vkov1.Valkey) string {
	var lines []string

	masterAddr := MasterAddress(v)
	if v.Annotations != nil {
		if override, ok := v.Annotations[AnnotationKnownMaster]; ok && override != "" {
			masterAddr = override
		}
	}
	monitorName := SentinelMonitorName(v)

	// Calculate quorum: majority of sentinel replicas.
	quorum := SentinelQuorum
	if v.Spec.Sentinel != nil && v.Spec.Sentinel.Replicas > 0 {
		quorum = int(v.Spec.Sentinel.Replicas/2) + 1
	}

	// Use TLS port for monitoring when TLS is enabled.
	monitorPort := ValkeyPort
	if v.IsTLSEnabled() {
		monitorPort = TLSPort
	}

	// Sentinel port configuration.
	if v.IsTLSEnabled() {
		lines = append(lines,
			"# Sentinel configuration",
			"port 0",
			fmt.Sprintf("tls-port %d", SentinelPort),
			fmt.Sprintf("dir %s", SentinelDataDir),
			"",
			"# TLS configuration",
			"tls-cert-file /tls/tls.crt",
			"tls-key-file /tls/tls.key",
			"tls-ca-cert-file /tls/ca.crt",
			"tls-replication yes",
			"tls-auth-clients optional",
			"",
		)
	} else {
		lines = append(lines,
			"# Sentinel configuration",
			fmt.Sprintf("port %d", SentinelPort),
			fmt.Sprintf("dir %s", SentinelDataDir),
			"",
		)
	}

	lines = append(lines,
		"# Monitor configuration",
		fmt.Sprintf("sentinel monitor %s %s %d %d", monitorName, masterAddr, monitorPort, quorum),
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
	// The sentinel init container will replace %VALKEY_PASSWORD% with the actual password.
	// requirepass protects the Sentinel process itself — without it, Sentinel rejects any
	// AUTH command with "ERR AUTH called without any password configured for the default user",
	// which causes the operator health checker to permanently fail Sentinel connectivity checks.
	// sentinel auth-pass is the separate credential Sentinel uses to connect to Valkey nodes.
	if v.IsAuthEnabled() {
		lines = append(lines,
			"# Auth",
			"requirepass %VALKEY_PASSWORD%",
			fmt.Sprintf("sentinel auth-pass %s %%VALKEY_PASSWORD%%", monitorName),
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
				// Use OnDelete so the operator controls pod-by-pod rollout
				// with sentinel quorum verification before each deletion.
				Type: appsv1.OnDeleteStatefulSetStrategyType,
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
	volumes := []corev1.Volume{
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
	}

	// Add TLS volume if TLS is enabled.
	if v.IsTLSEnabled() {
		volumes = append(volumes, corev1.Volume{
			Name: TLSVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: SentinelTLSSecretName(v),
				},
			},
		})
	}

	// Build the init container command. If auth is enabled, replace the password placeholder.
	initCommand := fmt.Sprintf("cp /etc/sentinel-readonly/%s %s/%s", SentinelConfigKey, SentinelConfigMountPath, SentinelConfigKey)
	if v.IsAuthEnabled() {
		// After copying, replace the placeholder with the actual password from the env var.
		initCommand = fmt.Sprintf(
			"cp /etc/sentinel-readonly/%s %s/%s && sed -i \"s|%%VALKEY_PASSWORD%%|$%s|g\" %s/%s",
			SentinelConfigKey, SentinelConfigMountPath, SentinelConfigKey,
			AuthSecretEnvName, SentinelConfigMountPath, SentinelConfigKey,
		)
	}

	initContainer := corev1.Container{
		Name:  "init-sentinel-config",
		Image: v.Spec.Image,
		Command: []string{
			"sh", "-c",
			initCommand,
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
	}

	// Inject auth env var into the init container if auth is enabled.
	if v.IsAuthEnabled() {
		initContainer.Env = append(initContainer.Env, corev1.EnvVar{
			Name: AuthSecretEnvName,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: v.Spec.Auth.SecretName,
					},
					Key: v.Spec.Auth.SecretPasswordKey,
				},
			},
		})
	}

	return corev1.PodSpec{
		ServiceAccountName: "default",
		// Init container copies the sentinel config to a writable volume.
		// Sentinel needs to rewrite its config file at runtime.
		InitContainers: []corev1.Container{
			initContainer,
		},
		Containers: []corev1.Container{
			buildSentinelContainer(v),
		},
		Volumes: volumes,
	}
}

// SentinelProbeCommand returns the exec probe command for a Sentinel container,
// accounting for TLS and auth configuration.
//
// When TLS is enabled the probe uses valkey-cli with TLS flags. The Sentinel
// TLS config uses tls-auth-clients optional, so no client certificate is
// required — only the CA cert is needed for server verification.
// When auth is enabled, the password is read from the VALKEY_PASSWORD env var
// that is injected into the Sentinel container.
func SentinelProbeCommand(v *vkov1.Valkey) []string {
	port := fmt.Sprintf("%d", SentinelPort)

	if v.IsAuthEnabled() {
		var cmdStr string
		if v.IsTLSEnabled() {
			cmdStr = fmt.Sprintf(
				"valkey-cli --tls --cacert %s/ca.crt -p %s -a \"$%s\" ping",
				TLSMountPath, port, AuthSecretEnvName,
			)
		} else {
			cmdStr = fmt.Sprintf(
				"valkey-cli -p %s -a \"$%s\" ping",
				port, AuthSecretEnvName,
			)
		}
		return []string{"sh", "-c", cmdStr}
	}

	if v.IsTLSEnabled() {
		return []string{
			"valkey-cli",
			"--tls",
			"--cacert", TLSMountPath + "/ca.crt",
			"-p", port,
			"ping",
		}
	}

	return []string{"valkey-cli", "-p", port, "ping"}
}

// buildSentinelContainer builds the Sentinel container spec.
func buildSentinelContainer(v *vkov1.Valkey) corev1.Container {
	volumeMounts := []corev1.VolumeMount{
		{
			Name:      SentinelConfigVolumeName,
			MountPath: SentinelConfigMountPath,
		},
		{
			Name:      DataVolumeName,
			MountPath: SentinelDataDir,
		},
	}

	// Mount TLS certificates if TLS is enabled.
	if v.IsTLSEnabled() {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      TLSVolumeName,
			MountPath: TLSMountPath,
			ReadOnly:  true,
		})
	}

	// Build probe handler. When TLS or auth is enabled, use an exec probe with
	// valkey-cli so that the probe speaks the correct protocol. A bare tcpSocket
	// probe against a TLS-only port causes the Sentinel to log continuous
	// "SSL routines::unexpected eof while reading" errors because kubelet
	// opens the TCP connection without performing a TLS handshake.
	var probeHandler corev1.ProbeHandler
	if v.IsTLSEnabled() || v.IsAuthEnabled() {
		probeHandler = corev1.ProbeHandler{
			Exec: &corev1.ExecAction{
				Command: SentinelProbeCommand(v),
			},
		}
	} else {
		probeHandler = corev1.ProbeHandler{
			TCPSocket: &corev1.TCPSocketAction{
				Port: intstr.FromInt32(SentinelPort),
			},
		}
	}

	container := corev1.Container{
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
		VolumeMounts: volumeMounts,
		ReadinessProbe: &corev1.Probe{
			ProbeHandler:        probeHandler,
			InitialDelaySeconds: 5,
			PeriodSeconds:       5,
			TimeoutSeconds:      3,
			SuccessThreshold:    1,
			FailureThreshold:    3,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler:        probeHandler,
			InitialDelaySeconds: 15,
			PeriodSeconds:       10,
			TimeoutSeconds:      5,
			SuccessThreshold:    1,
			FailureThreshold:    5,
		},
	}

	// Inject auth password env var into the main Sentinel container when auth
	// is enabled, so the probe command can reference $VALKEY_PASSWORD.
	if v.IsAuthEnabled() {
		container.Env = append(container.Env, corev1.EnvVar{
			Name: AuthSecretEnvName,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: v.Spec.Auth.SecretName,
					},
					Key: v.Spec.Auth.SecretPasswordKey,
				},
			},
		})
	}

	return container
}

// SentinelStatefulSetHasChanged returns true if the live Sentinel StatefulSet differs from desired.
// It checks replicas and the full pod template spec (containers, init containers, volumes,
// ServiceAccountName, TerminationGracePeriodSeconds, labels, and annotations).
func SentinelStatefulSetHasChanged(desired, current *appsv1.StatefulSet) bool {
	// Check replicas.
	if desired.Spec.Replicas != nil && current.Spec.Replicas != nil {
		if *desired.Spec.Replicas != *current.Spec.Replicas {
			return true
		}
	}
	return podTemplateChanged(desired.Spec.Template, current.Spec.Template)
}
