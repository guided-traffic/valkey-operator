package builder

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"

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

	// SidecarContainerName is the name of the sidecar container that manages role labels.
	SidecarContainerName = "sidecar"

	// SidecarHealthPort is the port on which the sidecar readiness endpoint listens.
	SidecarHealthPort = 8082

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

	// AuthSecretEnvName is the environment variable name used to inject the Valkey password.
	AuthSecretEnvName = "VALKEY_PASSWORD"
)

// BuildStatefulSet builds the StatefulSet for Valkey instances.
// operatorImage is the container image of the operator, used for the sidecar container.
func BuildStatefulSet(v *vkov1.Valkey, operatorImage string) *appsv1.StatefulSet {
	labels := common.BaseLabels(v, common.ComponentValkey)
	selectorLabels := common.SelectorLabels(v, common.ComponentValkey)
	podLabels := common.MergeLabels(labels, v.Spec.PodLabels)

	// Only set annotations if there are user-defined ones.
	var podAnnotations map[string]string
	if len(v.Spec.PodAnnotations) > 0 {
		podAnnotations = common.MergeAnnotations(v.Spec.PodAnnotations)
	}

	// Always inject the config hash annotation so that config changes
	// (e.g. allowUnencrypted toggle) are visible to the rolling update logic.
	if podAnnotations == nil {
		podAnnotations = make(map[string]string)
	}
	podAnnotations[AnnotationConfigHash] = ComputeConfigHash(v)
	podAnnotations[AnnotationPodSpecHash] = ComputePodSpecHash(v, operatorImage)

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
				Spec: buildPodSpec(v, operatorImage),
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
func buildPodSpec(v *vkov1.Valkey, operatorImage string) corev1.PodSpec {
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

		// Init container queries Sentinel for the actual master.
		// On first boot (Sentinel not yet available), falls back to ordinal-based logic.
		// This prevents data loss during rolling updates when master has moved from pod-0.
		sentinelHeadless := common.HeadlessServiceName(v, common.ComponentSentinel)
		monitorName := SentinelMonitorName(v)
		replicationPort := ValkeyPort
		if v.IsTLSEnabled() {
			replicationPort = TLSPort
		}

		// Determine sentinel query port and optional TLS flags for valkey-cli.
		// When TLS is enabled, Sentinel listens on SentinelTLSPort (36379) and
		// the init container must use TLS flags to connect.
		sentinelQueryPort := SentinelPort
		cliTLSFlags := ""
		if v.IsTLSEnabled() {
			sentinelQueryPort = SentinelTLSPort
			cliTLSFlags = fmt.Sprintf("--tls --cacert %s/ca.crt", TLSMountPath)
		}

		// When auth is enabled AND sentinel auth is not disabled, Sentinel requires
		// a password (requirepass). The init container must authenticate before issuing
		// SENTINEL commands, otherwise Sentinel returns "NOAUTH Authentication required."
		// which gets mistakenly written as the replicaof hostname, causing Valkey to crash.
		// When sentinel auth IS disabled, Sentinel does not have requirepass, so no auth flags.
		cliAuthFlags := ""
		if v.IsAuthEnabled() && !v.IsSentinelAuthDisabled() {
			cliAuthFlags = fmt.Sprintf("-a \"$%s\" --no-auth-warning", AuthSecretEnvName)
		}

		// Generate sentinel pod indices (e.g. "0 1 2" for 3 replicas) for the
		// init container's retry loop. This adapts to non-standard sentinel replica counts.
		sentinelIndices := sentinelPodIndices(v.Spec.Sentinel.Replicas)

		initContainer := corev1.Container{
			Name:  "init-config-selector",
			Image: v.Spec.Image,
			Command: []string{
				"sh", "-c",
				fmt.Sprintf(
					`# Query Sentinel for the actual master address.
# This is critical for rolling updates: after failover, the master may not be pod-0.
# Uses a retry loop with exponential backoff to handle concurrent pod/sentinel restarts.
SENTINEL_HOST="%[1]s.%[2]s.svc.cluster.local"
MONITOR="%[3]s"
MY_HOST="$HOSTNAME.%[4]s.%[2]s.svc.cluster.local"
MASTER_ADDR=""

# Phase 1: Retry Sentinel queries with exponential backoff (up to 30s).
# This prevents the race condition where pods and sentinels restart simultaneously
# and the pod falls through to ordinal fallback before sentinel is ready.
MAX_WAIT=30
WAITED=0
SLEEP=1
while [ "$WAITED" -lt "$MAX_WAIT" ] && [ -z "$MASTER_ADDR" ]; do
  for i in %[14]s; do
    SHOST="%[5]s-${i}.${SENTINEL_HOST}"
    RESULT=$(timeout 3 valkey-cli %[12]s %[13]s -h "$SHOST" -p %[6]d SENTINEL get-master-addr-by-name "$MONITOR" 2>/dev/null)
    if [ -n "$RESULT" ]; then
      CANDIDATE=$(echo "$RESULT" | head -1)
      # Guard against Sentinel returning an error string (e.g. NOAUTH, ERR, WRONGPASS)
      # instead of a hostname — those must never end up in a replicaof directive.
      if echo "$CANDIDATE" | grep -qE "^(NOAUTH|ERR |WRONGPASS|-)"; then
        echo "Sentinel $SHOST returned error: $CANDIDATE — skipping"
        continue
      fi
      MASTER_ADDR="$CANDIDATE"
      echo "Sentinel returned master: $MASTER_ADDR"
      break 2
    fi
  done
  echo "No Sentinel responded (waited ${WAITED}s/${MAX_WAIT}s), retrying in ${SLEEP}s..."
  sleep $SLEEP
  WAITED=$((WAITED + SLEEP))
  SLEEP=$((SLEEP * 2))
  [ "$SLEEP" -gt 8 ] && SLEEP=8
done

# Phase 2: If Sentinel is still unavailable, use the known master from the replica
# ConfigMap as a fallback. The operator updates this ConfigMap with the actual master
# address (via the known-master annotation), so it reflects post-failover state.
if [ -z "$MASTER_ADDR" ]; then
  REPLICA_CONF_MASTER=$(grep '^replicaof ' %[11]s/%[8]s 2>/dev/null | awk '{print $2}')
  if [ -n "$REPLICA_CONF_MASTER" ]; then
    echo "Sentinel unavailable, using known master from replica config: $REPLICA_CONF_MASTER"
    MASTER_ADDR="$REPLICA_CONF_MASTER"
  fi
fi

if [ -n "$MASTER_ADDR" ]; then
  # Master address resolved (from Sentinel or replica ConfigMap).
  if echo "$MASTER_ADDR" | grep -q "$MY_HOST"; then
    echo "This pod IS the master, using master config"
    cp %[7]s/%[8]s %[9]s/%[8]s
  else
    echo "This pod is a replica, master=$MASTER_ADDR"
    cp %[7]s/%[8]s %[9]s/%[8]s
    echo "" >> %[9]s/%[8]s
    echo "# Replication (configured by init container via Sentinel/ConfigMap discovery)" >> %[9]s/%[8]s
    echo "replicaof $MASTER_ADDR %[10]d" >> %[9]s/%[8]s
  fi
else
  # Phase 3: Last resort — ordinal-based fallback (fresh installation only).
  ORDINAL=$(echo $HOSTNAME | rev | cut -d'-' -f1 | rev)
  echo "All discovery methods exhausted, using ordinal-based config (ordinal=$ORDINAL)"
  if [ "$ORDINAL" = "0" ]; then
    cp %[7]s/%[8]s %[9]s/%[8]s
  else
    cp %[11]s/%[8]s %[9]s/%[8]s
  fi
fi

# Announce this pod's FQDN to Sentinel so it uses hostnames instead of IPs.
echo "" >> %[9]s/%[8]s
echo "# Announce hostname for Sentinel discovery (injected by init container)" >> %[9]s/%[8]s
echo "replica-announce-ip $MY_HOST" >> %[9]s/%[8]s
echo "replica-announce-port %[10]d" >> %[9]s/%[8]s`,
					sentinelHeadless, // 1: sentinel headless service
					v.Namespace,      // 2: namespace
					monitorName,      // 3: sentinel monitor name
					common.HeadlessServiceName(v, common.ComponentValkey), // 4: valkey headless service
					common.StatefulSetName(v, common.ComponentSentinel),   // 5: sentinel statefulset name
					sentinelQueryPort,       // 6: sentinel port (TLS-aware)
					ConfigMountPath,         // 7: master config mount
					ValkeyConfigKey,         // 8: config file name
					WritableConfigMountPath, // 9: writable config mount
					replicationPort,         // 10: replication port
					ReplicaConfigMountPath,  // 11: replica config mount
					cliTLSFlags,             // 12: optional TLS flags for valkey-cli
					cliAuthFlags,            // 13: optional auth flags for valkey-cli
					sentinelIndices,         // 14: sentinel pod indices (e.g. "0 1 2")
				),
			},
			VolumeMounts: buildInitContainerVolumeMounts(v),
		}

		// Inject the password env var so the shell script can use $VALKEY_PASSWORD.
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

		initContainers = append(initContainers, initContainer)
	}

	// In multi-replica mode without Sentinel, use a simple ordinal-based init
	// container to select master or replica config. Pod-0 gets the master config,
	// all other pods get the replica config (which contains `replicaof`).
	if v.IsMultiReplicaWithoutSentinel() {
		volumes = append(volumes,
			corev1.Volume{
				Name: ReplicaConfigVolumeName,
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: ReplicaConfigMapName(v),
						},
					},
				},
			},
			corev1.Volume{
				Name: WritableConfigVolumeName,
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		)

		replicationPort := ValkeyPort
		if v.IsTLSEnabled() {
			replicationPort = TLSPort
		}

		initContainer := corev1.Container{
			Name:  "init-config-selector",
			Image: v.Spec.Image,
			Command: []string{
				"sh", "-c",
				fmt.Sprintf(
					`# Ordinal-based config selection for non-Sentinel replication.
MY_HOST="$HOSTNAME.%[5]s.%[6]s.svc.cluster.local"
ORDINAL=$(echo $HOSTNAME | rev | cut -d'-' -f1 | rev)
if [ "$ORDINAL" = "0" ]; then
  echo "Pod ordinal 0 — using master config"
  cp %[1]s/%[3]s %[2]s/%[3]s
else
  echo "Pod ordinal $ORDINAL — using replica config"
  cp %[4]s/%[3]s %[2]s/%[3]s
fi

# Announce this pod's FQDN so replication info shows hostnames instead of IPs.
echo "" >> %[2]s/%[3]s
echo "# Announce hostname for replication discovery (injected by init container)" >> %[2]s/%[3]s
echo "replica-announce-ip $MY_HOST" >> %[2]s/%[3]s
echo "replica-announce-port %[7]d" >> %[2]s/%[3]s`,
					ConfigMountPath,         // 1: master config mount (readonly)
					WritableConfigMountPath, // 2: writable config mount
					ValkeyConfigKey,         // 3: config file name
					ReplicaConfigMountPath,  // 4: replica config mount (readonly)
					common.HeadlessServiceName(v, common.ComponentValkey), // 5: valkey headless service
					v.Namespace,     // 6: namespace
					replicationPort, // 7: replication port
				),
			},
			VolumeMounts: buildInitContainerVolumeMounts(v),
		}
		initContainers = append(initContainers, initContainer)
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

	// Add TLS volume if TLS is enabled.
	if v.IsTLSEnabled() {
		volumes = append(volumes, corev1.Volume{
			Name: TLSVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: ValkeyTLSSecretName(v),
				},
			},
		})
	}

	spec := corev1.PodSpec{
		ServiceAccountName: SidecarServiceAccountName(v),
		Containers: []corev1.Container{
			buildValkeyContainer(v),
			buildSidecarContainer(v, operatorImage),
		},
		Volumes: volumes,
	}

	// Set terminationGracePeriodSeconds to allow time for graceful failover.
	// 75s = 60s failover timeout + 15s buffer.
	terminationGrace := int64(75)
	spec.TerminationGracePeriodSeconds = &terminationGrace

	if len(initContainers) > 0 {
		spec.InitContainers = initContainers
	}

	return spec
}

// sentinelPodIndices generates a space-separated list of sentinel pod ordinal
// indices (e.g. "0 1 2" for 3 replicas) for use in the init container's shell
// script. This adapts to non-standard sentinel replica counts.
func sentinelPodIndices(replicas int32) string {
	indices := make([]string, replicas)
	for i := int32(0); i < replicas; i++ {
		indices[i] = fmt.Sprintf("%d", i)
	}
	return strings.Join(indices, " ")
}

// needsInitContainer returns true when the pod spec requires an init container
// to select the correct configuration (master vs replica).
func needsInitContainer(v *vkov1.Valkey) bool {
	return v.IsSentinelEnabled() || v.IsMultiReplicaWithoutSentinel()
}

// configMountForContainer returns the config mount path used by the valkey container.
// In multi-replica mode, this is the writable config directory (populated by init container).
// In standalone mode, this is the readonly ConfigMap mount.
func configMountForContainer(v *vkov1.Valkey) string {
	if needsInitContainer(v) {
		return WritableConfigMountPath
	}
	return ConfigMountPath
}

// configVolumeNameForContainer returns the volume name to mount for the valkey config.
func configVolumeNameForContainer(v *vkov1.Valkey) string {
	if needsInitContainer(v) {
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
			ReadOnly:  !needsInitContainer(v), // Writable when init container populates config.
		},
		{
			Name:      DataVolumeName,
			MountPath: DataDir,
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

	// Determine the container ports.
	// When TLS is enabled the primary named port is the TLS port (16379).
	// When allowUnencrypted is also set, a second named port (valkey-plain) is
	// exposed so that services and network policies can target it by name.
	containerPort := int32(ValkeyPort)
	if v.IsTLSEnabled() {
		containerPort = TLSPort
	}
	valkeyContainerPorts := []corev1.ContainerPort{
		{
			Name:          "valkey",
			ContainerPort: containerPort,
			Protocol:      corev1.ProtocolTCP,
		},
	}
	if v.IsValkeyUnencryptedAllowed() {
		valkeyContainerPorts = append(valkeyContainerPorts, corev1.ContainerPort{
			Name:          "valkey-plain",
			ContainerPort: ValkeyPort,
			Protocol:      corev1.ProtocolTCP,
		})
	}

	// Build command with optional auth arguments.
	cmd := []string{
		"valkey-server",
		cfgMount + "/" + ValkeyConfigKey,
	}
	if v.IsAuthEnabled() {
		// Use shell to expand the environment variable for password injection.
		cmd = []string{
			"sh", "-c",
			fmt.Sprintf("exec valkey-server %s/%s --requirepass \"$%s\" --masterauth \"$%s\"",
				cfgMount, ValkeyConfigKey, AuthSecretEnvName, AuthSecretEnvName),
		}
	}

	container := corev1.Container{
		Name:         ValkeyContainerName,
		Image:        v.Spec.Image,
		Command:      cmd,
		Ports:        valkeyContainerPorts,
		VolumeMounts: volumeMounts,
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: ProbeCommand(v),
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
					Command: ProbeCommand(v),
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

	// Inject auth password from Secret as environment variable.
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

// buildSidecarContainer builds the sidecar container that polls the local Valkey
// instance and patches the pod's instanceRole label.
func buildSidecarContainer(v *vkov1.Valkey, operatorImage string) corev1.Container {
	env := []corev1.EnvVar{
		{
			Name: "POD_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
		{
			Name: "POD_NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.namespace",
				},
			},
		},
	}

	args := []string{
		"sidecar",
		"--poll-interval=1s",
	}

	// Configure Valkey address (use TLS port if TLS is enabled).
	if v.IsTLSEnabled() {
		args = append(args, fmt.Sprintf("--valkey-addr=localhost:%d", TLSPort))
		args = append(args, "--tls-enabled=true")
		args = append(args, "--tls-ca-cert=/tls/ca.crt")
		args = append(args, "--tls-cert=/tls/tls.crt")
		args = append(args, "--tls-key=/tls/tls.key")
	} else {
		args = append(args, fmt.Sprintf("--valkey-addr=localhost:%d", ValkeyPort))
	}

	// Sentinel/failover settings for graceful drain.
	if v.IsSentinelEnabled() {
		args = append(args, "--sentinel-enabled=true")
		args = append(args, fmt.Sprintf("--sentinel-monitor=%s", SentinelMonitorName(v)))
		args = append(args, fmt.Sprintf("--sentinel-addrs=%s", buildSentinelAddrList(v)))
		if v.IsSentinelAuthDisabled() {
			args = append(args, "--sentinel-disable-auth=true")
		}
	}

	// Headless service and replica count for drain handler replica discovery.
	headlessSvc := fmt.Sprintf("%s.%s.svc.cluster.local",
		common.HeadlessServiceName(v, common.ComponentValkey), v.Namespace)
	args = append(args, fmt.Sprintf("--headless-svc=%s", headlessSvc))
	args = append(args, fmt.Sprintf("--replicas=%d", v.Spec.Replicas))

	// Inject auth password if configured.
	if v.IsAuthEnabled() {
		env = append(env, corev1.EnvVar{
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

	var volumeMounts []corev1.VolumeMount

	// Mount TLS certificates if TLS is enabled.
	if v.IsTLSEnabled() {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      TLSVolumeName,
			MountPath: TLSMountPath,
			ReadOnly:  true,
		})
	}

	sidecarImage := operatorImage
	if sidecarImage == "" {
		sidecarImage = "ghcr.io/guided-traffic/valkey-operator:latest"
	}

	return corev1.Container{
		Name:    SidecarContainerName,
		Image:   sidecarImage,
		Command: []string{"./manager"},
		Args:    args,
		Env:     env,
		Ports: []corev1.ContainerPort{
			{
				Name:          "health",
				ContainerPort: SidecarHealthPort,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		VolumeMounts: volumeMounts,
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/readyz",
					Port:   intstr.FromInt32(SidecarHealthPort),
					Scheme: corev1.URISchemeHTTP,
				},
			},
			InitialDelaySeconds: 3,
			PeriodSeconds:       3,
			TimeoutSeconds:      2,
			SuccessThreshold:    1,
			FailureThreshold:    3,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/healthz",
					Port:   intstr.FromInt32(SidecarHealthPort),
					Scheme: corev1.URISchemeHTTP,
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       10,
			TimeoutSeconds:      2,
			SuccessThreshold:    1,
			FailureThreshold:    5,
		},
	}
}

// buildInitContainerVolumeMounts returns the volume mounts for the HA init
// container. When TLS is enabled, the TLS secret volume is included so that
// valkey-cli can connect to Sentinel using TLS.
func buildInitContainerVolumeMounts(v *vkov1.Valkey) []corev1.VolumeMount {
	mounts := []corev1.VolumeMount{
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
	}
	if v.IsTLSEnabled() {
		mounts = append(mounts, corev1.VolumeMount{
			Name:      TLSVolumeName,
			MountPath: TLSMountPath,
			ReadOnly:  true,
		})
	}
	return mounts
}

// buildSentinelAddrList constructs the comma-separated list of sentinel
// pod addresses for the sidecar's drain handler.
// When TLS is enabled, the TLS port (SentinelTLSPort = 36379) is used so that
// the sidecar connects to Sentinel over TLS.
func buildSentinelAddrList(v *vkov1.Valkey) string {
	headless := common.HeadlessServiceName(v, common.ComponentSentinel)
	stsName := common.StatefulSetName(v, common.ComponentSentinel)
	sentinelReplicas := int32(3)
	if v.Spec.Sentinel != nil && v.Spec.Sentinel.Replicas > 0 {
		sentinelReplicas = v.Spec.Sentinel.Replicas
	}

	sentinelPort := SentinelPort
	if v.IsTLSEnabled() {
		sentinelPort = SentinelTLSPort
	}

	addrs := make([]string, 0, sentinelReplicas)
	for i := int32(0); i < sentinelReplicas; i++ {
		addr := fmt.Sprintf("%s-%d.%s.%s.svc.cluster.local:%d",
			stsName, i, headless, v.Namespace, sentinelPort)
		addrs = append(addrs, addr)
	}
	return strings.Join(addrs, ",")
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

// ComputePodSpecHash returns a short hex digest of the pod spec built for
// this Valkey CR. It is embedded in the StatefulSet pod template annotations so
// that any change to the pod specification (resources, probes, volumes, env
// vars, etc.) is detected by the rolling update logic — even though the
// StatefulSet uses the OnDelete update strategy.
func ComputePodSpecHash(v *vkov1.Valkey, operatorImage string) string {
	spec := buildPodSpec(v, operatorImage)
	data, _ := json.Marshal(spec)
	h := fnv.New32a()
	_, _ = h.Write(data)
	return fmt.Sprintf("%08x", h.Sum32())
}

// StatefulSetHasChanged returns true if the live StatefulSet differs from the desired spec
// in ways that require an update (replicas, pod template spec).
func StatefulSetHasChanged(desired, current *appsv1.StatefulSet) bool {
	// Check replicas.
	if desired.Spec.Replicas != nil && current.Spec.Replicas != nil {
		if *desired.Spec.Replicas != *current.Spec.Replicas {
			return true
		}
	}
	return podTemplateChanged(desired.Spec.Template, current.Spec.Template)
}

// podTemplateChanged returns true if the pod template spec has changed in ways
// that require a rolling update (labels, annotations, or pod spec).
func podTemplateChanged(desired, current corev1.PodTemplateSpec) bool {
	if stringMapChanged(desired.Labels, current.Labels) {
		return true
	}
	if stringMapChanged(desired.Annotations, current.Annotations) {
		return true
	}
	return podSpecChanged(desired.Spec, current.Spec)
}

// podSpecChanged returns true if two PodSpecs differ in rolling-update-relevant ways.
// It covers all containers (including sidecar), init containers, volumes,
// ServiceAccountName, and TerminationGracePeriodSeconds.
func podSpecChanged(desired, current corev1.PodSpec) bool {
	if desired.ServiceAccountName != current.ServiceAccountName {
		return true
	}
	if !terminationGracePeriodEqual(desired.TerminationGracePeriodSeconds, current.TerminationGracePeriodSeconds) {
		return true
	}
	if containersChanged(desired.Containers, current.Containers) {
		return true
	}
	if containersChanged(desired.InitContainers, current.InitContainers) {
		return true
	}
	return volumesChanged(desired.Volumes, current.Volumes)
}

// terminationGracePeriodEqual returns true if both pointers refer to equal values
// (or are both nil).
func terminationGracePeriodEqual(a, b *int64) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return *a == *b
}

// containersChanged returns true if the container lists differ in count or in any
// container's name, image, command, args, env, volume mounts, or resource requirements.
func containersChanged(desired, current []corev1.Container) bool {
	if len(desired) != len(current) {
		return true
	}
	for i := range desired {
		if containerChanged(desired[i], current[i]) {
			return true
		}
	}
	return false
}

// containerChanged returns true if two containers differ in rolling-update-relevant fields.
func containerChanged(desired, current corev1.Container) bool {
	if desired.Name != current.Name || desired.Image != current.Image {
		return true
	}
	if !stringSliceEqual(desired.Command, current.Command) || !stringSliceEqual(desired.Args, current.Args) {
		return true
	}
	if !envVarsEqual(desired.Env, current.Env) {
		return true
	}
	if !volumeMountsEqual(desired.VolumeMounts, current.VolumeMounts) {
		return true
	}
	if !containerPortsEqual(desired.Ports, current.Ports) {
		return true
	}
	dRes := desired.Resources
	cRes := current.Resources
	return resourceListChanged(dRes.Requests, cRes.Requests) || resourceListChanged(dRes.Limits, cRes.Limits)
}

// containerPortsEqual returns true when both port slices expose the same set of
// named container ports (matching on Name, ContainerPort, and Protocol).
// Order does not matter.
func containerPortsEqual(a, b []corev1.ContainerPort) bool {
	if len(a) != len(b) {
		return false
	}
	bMap := make(map[string]corev1.ContainerPort, len(b))
	for _, p := range b {
		bMap[p.Name] = p
	}
	for _, pa := range a {
		pb, ok := bMap[pa.Name]
		if !ok {
			return false
		}
		if pa.ContainerPort != pb.ContainerPort || pa.Protocol != pb.Protocol {
			return false
		}
	}
	return true
}

// volumeMountsEqual returns true if two volume mount slices are equal in name,
// mount path, and read-only flag.
func volumeMountsEqual(a, b []corev1.VolumeMount) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].MountPath != b[i].MountPath || a[i].ReadOnly != b[i].ReadOnly {
			return false
		}
	}
	return true
}

// volumesChanged returns true if the volume configurations differ in count, names,
// or source (ConfigMap/Secret names).
func volumesChanged(desired, current []corev1.Volume) bool {
	if len(desired) != len(current) {
		return true
	}
	currentMap := make(map[string]corev1.Volume, len(current))
	for _, v := range current {
		currentMap[v.Name] = v
	}
	for _, d := range desired {
		c, ok := currentMap[d.Name]
		if !ok {
			return true
		}
		if volumeSourceChanged(d.VolumeSource, c.VolumeSource) {
			return true
		}
	}
	return false
}

// volumeSourceChanged returns true if two VolumeSource values differ in
// ConfigMap name or Secret name — the most common changes between operator versions.
func volumeSourceChanged(desired, current corev1.VolumeSource) bool {
	dCM, cCM := desired.ConfigMap, current.ConfigMap
	if (dCM == nil) != (cCM == nil) {
		return true
	}
	if dCM != nil && dCM.Name != cCM.Name {
		return true
	}
	dSec, cSec := desired.Secret, current.Secret
	if (dSec == nil) != (cSec == nil) {
		return true
	}
	if dSec != nil && dSec.SecretName != cSec.SecretName {
		return true
	}
	return false
}

// stringMapChanged returns true if two string maps differ in length or content.
func stringMapChanged(a, b map[string]string) bool {
	if len(a) != len(b) {
		return true
	}
	for k, v := range a {
		if b[k] != v {
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

// envVarsEqual returns true if two env var slices are equal.
func envVarsEqual(a, b []corev1.EnvVar) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name {
			return false
		}
		if a[i].Value != b[i].Value {
			return false
		}
		// Compare SecretKeyRef.
		aRef := a[i].ValueFrom
		bRef := b[i].ValueFrom
		if (aRef == nil) != (bRef == nil) {
			return false
		}
		if aRef != nil && bRef != nil {
			if (aRef.SecretKeyRef == nil) != (bRef.SecretKeyRef == nil) {
				return false
			}
			if aRef.SecretKeyRef != nil && bRef.SecretKeyRef != nil {
				if aRef.SecretKeyRef.Name != bRef.SecretKeyRef.Name || aRef.SecretKeyRef.Key != bRef.SecretKeyRef.Key {
					return false
				}
			}
		}
	}
	return true
}

// stringSliceEqual returns true if two string slices are equal.
func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ServicePort returns the Valkey client port, accounting for TLS configuration.
func ServicePort(v *vkov1.Valkey) int32 {
	if v.IsTLSEnabled() {
		return int32(ValkeyPort + 10000)
	}
	return int32(ValkeyPort)
}

// ProbeCommand returns the probe command, accounting for TLS and auth.
// When auth is enabled, the probe uses a shell command to expand the
// VALKEY_PASSWORD environment variable for the -a flag.
func ProbeCommand(v *vkov1.Valkey) []string {
	if v.IsAuthEnabled() {
		// Use shell to expand the env var for the password.
		var cmdStr string
		if v.IsTLSEnabled() {
			cmdStr = fmt.Sprintf(
				"valkey-cli --tls --cert /tls/tls.crt --key /tls/tls.key --cacert /tls/ca.crt -p %d -a \"$%s\" ping",
				TLSPort, AuthSecretEnvName,
			)
		} else {
			cmdStr = fmt.Sprintf(
				"valkey-cli -a \"$%s\" ping",
				AuthSecretEnvName,
			)
		}
		return []string{"sh", "-c", cmdStr}
	}

	if v.IsTLSEnabled() {
		return []string{
			"valkey-cli",
			"--tls",
			"--cert", "/tls/tls.crt",
			"--key", "/tls/tls.key",
			"--cacert", "/tls/ca.crt",
			"-p", fmt.Sprintf("%d", TLSPort),
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
