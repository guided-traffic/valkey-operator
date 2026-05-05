package builder

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
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
	return generateSentinelConf(v, true)
}

// GenerateSentinelConfForHash generates sentinel.conf without the AnnotationKnownMaster
// override. Use this when computing the config hash for pod update detection.
// The AnnotationKnownMaster changes during rolling-update failovers (it is set by
// persistKnownMaster) and must NOT affect the hash — including it would cause all
// pods to appear outdated immediately after a failover, triggering an infinite
// restart loop.
func GenerateSentinelConfForHash(v *vkov1.Valkey) string {
	return generateSentinelConf(v, false)
}

// generateSentinelConf is the internal implementation shared by GenerateSentinelConf
// and GenerateSentinelConfForHash.
func generateSentinelConf(v *vkov1.Valkey, useKnownMaster bool) string {
	var lines []string

	masterAddr := MasterAddress(v)
	if useKnownMaster && v.Annotations != nil {
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
		// Sentinel TLS port is always SentinelTLSPort (36379 = SentinelPort + 10000).
		// When allowUnencrypted is true, keep the plaintext port open alongside TLS.
		// Otherwise disable plaintext entirely (port 0).
		plaintextPort := "port 0"
		if v.IsSentinelUnencryptedAllowed() {
			plaintextPort = fmt.Sprintf("port %d", SentinelPort)
		}
		lines = append(lines,
			"# Sentinel configuration",
			plaintextPort,
			fmt.Sprintf("tls-port %d", SentinelTLSPort),
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
	// When DisableAuth is set on the Sentinel spec, requirepass is omitted so that clients
	// can connect to Sentinel without authentication — but auth-pass is still emitted so
	// that Sentinel can authenticate with password-protected Valkey nodes.
	if v.IsAuthEnabled() {
		if v.IsSentinelAuthDisabled() {
			lines = append(lines,
				"# Auth (sentinel client auth disabled, auth-pass only)",
				fmt.Sprintf("sentinel auth-pass %s %%VALKEY_PASSWORD%%", monitorName),
				"",
			)
		} else {
			lines = append(lines,
				"# Auth",
				"requirepass %VALKEY_PASSWORD%",
				fmt.Sprintf("sentinel auth-pass %s %%VALKEY_PASSWORD%%", monitorName),
				"",
			)
		}
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

	// Always inject the config hash annotation so that config changes
	// (e.g. allowUnencrypted toggle) are visible to the rolling update logic.
	if podAnnotations == nil {
		podAnnotations = make(map[string]string)
	}
	podAnnotations[AnnotationConfigHash] = ComputeConfigHash(v)
	podAnnotations[AnnotationPodSpecHash] = ComputeSentinelPodSpecHash(v)

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

	// Build the init container command.
	// 1. Copy the read-only ConfigMap config to the writable volume.
	// 2. If auth is enabled, replace the password placeholder with the actual password.
	// 3. Validate the configured master by probing Valkey pods with the ROLE command.
	//    If the configured master is not reachable or is not actually a master,
	//    scan all known Valkey pods to find the real master and rewrite sentinel.conf.
	//    This prevents a stale master entry after a simultaneous restart of all pods.
	initCommand := buildSentinelInitCommand(v)

	initVolumeMounts := []corev1.VolumeMount{
		{
			Name:      "sentinel-config-readonly",
			MountPath: "/etc/sentinel-readonly",
			ReadOnly:  true,
		},
		{
			Name:      SentinelConfigVolumeName,
			MountPath: SentinelConfigMountPath,
		},
	}
	// The init container needs TLS certs to probe Valkey nodes when TLS is enabled.
	if v.IsTLSEnabled() {
		initVolumeMounts = append(initVolumeMounts, corev1.VolumeMount{
			Name:      TLSVolumeName,
			MountPath: TLSMountPath,
			ReadOnly:  true,
		})
	}

	initContainer := corev1.Container{
		Name:  "init-sentinel-config",
		Image: v.Spec.Image,
		Command: []string{
			"sh", "-c",
			initCommand,
		},
		VolumeMounts: initVolumeMounts,
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
		ServiceAccountName: DefaultServiceAccountName,
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
	// Use the TLS port (36379) when TLS is enabled — Sentinel listens on tls-port SentinelTLSPort.
	var port string
	if v.IsTLSEnabled() {
		port = fmt.Sprintf("%d", SentinelTLSPort)
	} else {
		port = fmt.Sprintf("%d", SentinelPort)
	}

	// When auth is enabled AND sentinel auth is not disabled, the probe must authenticate.
	sentinelNeedsAuth := v.IsAuthEnabled() && !v.IsSentinelAuthDisabled()

	if sentinelNeedsAuth {
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
			ValkeyCLIBinary,
			ValkeyTLSFlag,
			ValkeyCACertFlag, TLSMountPath + "/ca.crt",
			"-p", port,
			ValkeyPingCommand,
		}
	}

	return []string{ValkeyCLIBinary, "-p", port, ValkeyPingCommand}
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

	// Build probe handler. When TLS is enabled or sentinel requires auth, use an exec
	// probe with valkey-cli so that the probe speaks the correct protocol. A bare
	// tcpSocket probe against a TLS-only port causes the Sentinel to log continuous
	// "SSL routines::unexpected eof while reading" errors because kubelet
	// opens the TCP connection without performing a TLS handshake.
	// When sentinel auth is disabled, the probe still needs exec for TLS but not for auth.
	sentinelNeedsAuth := v.IsAuthEnabled() && !v.IsSentinelAuthDisabled()
	var probeHandler corev1.ProbeHandler
	if v.IsTLSEnabled() || sentinelNeedsAuth {
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

	// Determine the container ports.
	// When TLS is enabled, the primary named port is the TLS port (36379).
	// When allowUnencrypted is also set, a second named port (sentinel-plain)
	// is exposed for services and network policies that target plaintext clients.
	sentinelContainerPort := int32(SentinelPort)
	if v.IsTLSEnabled() {
		sentinelContainerPort = SentinelTLSPort
	}
	sentinelContainerPorts := []corev1.ContainerPort{
		{
			Name:          SentinelContainerName,
			ContainerPort: sentinelContainerPort,
			Protocol:      corev1.ProtocolTCP,
		},
	}
	if v.IsSentinelUnencryptedAllowed() {
		sentinelContainerPorts = append(sentinelContainerPorts, corev1.ContainerPort{
			Name:          "sentinel-plain",
			ContainerPort: SentinelPort,
			Protocol:      corev1.ProtocolTCP,
		})
	}

	container := corev1.Container{
		Name:  SentinelContainerName,
		Image: v.Spec.Image,
		Command: []string{
			"valkey-sentinel",
			SentinelConfigMountPath + "/" + SentinelConfigKey,
		},
		Ports:        sentinelContainerPorts,
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
	// is enabled AND sentinel auth is not disabled, so the probe command can
	// reference $VALKEY_PASSWORD. When sentinel auth is disabled, the probe
	// does not authenticate and the env var is not needed in the main container.
	if v.IsAuthEnabled() && !v.IsSentinelAuthDisabled() {
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

// ComputeSentinelPodSpecHash returns a short hex digest of the sentinel pod spec
// built for this Valkey CR. Works identically to ComputePodSpecHash but for
// sentinel pods.
func ComputeSentinelPodSpecHash(v *vkov1.Valkey) string {
	spec := buildSentinelPodSpec(v)
	data, _ := json.Marshal(spec)
	h := fnv.New32a()
	_, _ = h.Write(data)
	return fmt.Sprintf("%08x", h.Sum32())
}

// buildSentinelInitCommand constructs the shell script for the init-sentinel-config
// init container. The script:
//  1. Copies the read-only ConfigMap sentinel.conf to the writable volume.
//  2. Replaces the %VALKEY_PASSWORD% placeholder (when auth is enabled).
//  3. Validates the configured master by probing Valkey pods via the ROLE command.
//     If the configured master is unreachable or not actually a master, the script
//     scans all known Valkey pods to discover the real master and rewrites the
//     "sentinel monitor" line in sentinel.conf. This prevents stale master entries
//     after a simultaneous restart of all pods in the namespace.
func buildSentinelInitCommand(v *vkov1.Valkey) string {
	// Port used to connect to Valkey nodes for ROLE check.
	valkeyPort := ValkeyPort
	if v.IsTLSEnabled() {
		valkeyPort = TLSPort
	}

	// CLI flags for connecting to Valkey nodes.
	cliTLSFlags := ""
	if v.IsTLSEnabled() {
		cliTLSFlags = fmt.Sprintf("--tls --cacert %s/ca.crt", TLSMountPath)
	}
	cliAuthFlags := ""
	if v.IsAuthEnabled() {
		cliAuthFlags = fmt.Sprintf("-a \"$%s\" --no-auth-warning", AuthSecretEnvName)
	}

	valkeySTSName := common.StatefulSetName(v, common.ComponentValkey)
	valkeyHeadless := common.HeadlessServiceName(v, common.ComponentValkey)

	return fmt.Sprintf(
		`# Step 1: Copy read-only config to writable volume.
cp /etc/sentinel-readonly/%[1]s %[2]s/%[1]s
%[3]s
# Step 2: Validate the configured master.
# Extract the current master host from the sentinel monitor line.
CONFIGURED_MASTER=$(grep "^sentinel monitor" %[2]s/%[1]s | awk '{print $4}')
MONITOR_NAME=$(grep "^sentinel monitor" %[2]s/%[1]s | awk '{print $3}')
MONITOR_PORT=$(grep "^sentinel monitor" %[2]s/%[1]s | awk '{print $5}')
MONITOR_QUORUM=$(grep "^sentinel monitor" %[2]s/%[1]s | awk '{print $6}')

echo "Configured master: $CONFIGURED_MASTER (port=$MONITOR_PORT, quorum=$MONITOR_QUORUM)"

# Check if the configured master is actually a master.
MASTER_OK=false
ROLE_RESULT=$(timeout 3 valkey-cli %[4]s %[5]s -h "$CONFIGURED_MASTER" -p %[6]d ROLE 2>/dev/null | head -1)
if [ "$ROLE_RESULT" = "master" ]; then
  echo "Configured master $CONFIGURED_MASTER confirmed as master"
  MASTER_OK=true
fi

if [ "$MASTER_OK" = "false" ]; then
  echo "Configured master $CONFIGURED_MASTER is not reachable or not a master (role=$ROLE_RESULT), scanning pods..."
  ACTUAL_MASTER=""
  VALKEY_HEADLESS="%[7]s.%[8]s.svc.cluster.local"

  for i in $(seq 0 %[9]d); do
    POD_HOST="%[10]s-${i}.${VALKEY_HEADLESS}"
    ROLE_RESULT=$(timeout 3 valkey-cli %[4]s %[5]s -h "$POD_HOST" -p %[6]d ROLE 2>/dev/null | head -1)
    echo "  Pod $POD_HOST role=$ROLE_RESULT"
    if [ "$ROLE_RESULT" = "master" ]; then
      ACTUAL_MASTER="$POD_HOST"
      echo "Found actual master: $ACTUAL_MASTER"
      break
    fi
  done

  if [ -n "$ACTUAL_MASTER" ]; then
    # Rewrite the sentinel monitor line with the correct master.
    sed -i "s|^sentinel monitor $MONITOR_NAME .*|sentinel monitor $MONITOR_NAME $ACTUAL_MASTER $MONITOR_PORT $MONITOR_QUORUM|" %[2]s/%[1]s
    echo "Updated sentinel.conf to point to $ACTUAL_MASTER"
  else
    echo "No master found among Valkey pods (cold start). Using configured master."
  fi
fi`,
		SentinelConfigKey,       // 1: sentinel.conf filename
		SentinelConfigMountPath, // 2: writable config path /etc/sentinel
		buildSentinelSedStep(v), // 3: sed replacement (empty string if no auth)
		cliTLSFlags,             // 4: TLS flags for valkey-cli
		cliAuthFlags,            // 5: auth flags for valkey-cli
		valkeyPort,              // 6: Valkey port to probe
		valkeyHeadless,          // 7: Valkey headless service name
		v.Namespace,             // 8: namespace
		v.Spec.Replicas-1,       // 9: max pod ordinal (replicas - 1)
		valkeySTSName,           // 10: Valkey StatefulSet name
	)
}

// buildSentinelSedStep returns the sed command that replaces the password
// placeholder, or an empty string when auth is not enabled.
func buildSentinelSedStep(v *vkov1.Valkey) string {
	if !v.IsAuthEnabled() {
		return ""
	}
	return fmt.Sprintf(
		`sed -i "s|%%VALKEY_PASSWORD%%|$%s|g" %s/%s`,
		AuthSecretEnvName, SentinelConfigMountPath, SentinelConfigKey,
	)
}
