package builder

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

const (
	// ComponentObserver is the component value for observer instances.
	ComponentObserver = "observer"

	// ObserverHealthPort is the port on which the observer health endpoint listens.
	ObserverHealthPort = 8084
)

// ObserverDeploymentName returns the name for the observer Deployment.
func ObserverDeploymentName(v *vkov1.Valkey) string {
	return fmt.Sprintf("%s-observer", v.Name)
}

// ObserverServiceAccountName returns the name of the ServiceAccount used by the
// observer Deployment. The observer makes no Kubernetes API call at all, so this
// ServiceAccount is bound to no Role and no RoleBinding — it exists to keep the
// observer out of the sidecar's grant (ADR 0012 D8 step 2).
func ObserverServiceAccountName(v *vkov1.Valkey) string {
	return fmt.Sprintf("%s-observer", v.Name)
}

// BuildObserverServiceAccount builds the Role-less ServiceAccount for the observer.
// Nothing binds a Role to it: the observer imports no Kubernetes client, and until
// this ServiceAccount existed it ran under the sidecar's, which grants pods patch.
func BuildObserverServiceAccount(v *vkov1.Valkey) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ObserverServiceAccountName(v),
			Namespace: v.Namespace,
			Labels:    ObserverLabels(v),
		},
	}
}

// ObserverLabels returns the labels for observer resources.
func ObserverLabels(v *vkov1.Valkey) map[string]string {
	return map[string]string{
		common.LabelComponent: ComponentObserver,
		common.LabelInstance:  v.Name,
		common.LabelManagedBy: common.ManagedBy,
		common.LabelName:      ValkeyContainerName,
		common.LabelCluster:   v.Name,
	}
}

// ObserverSelectorLabels returns the minimal label set for observer selectors.
func ObserverSelectorLabels(v *vkov1.Valkey) map[string]string {
	return map[string]string{
		common.LabelComponent: ComponentObserver,
		common.LabelCluster:   v.Name,
	}
}

// BuildObserverDeployment builds the Deployment for the observer.
func BuildObserverDeployment(v *vkov1.Valkey, operatorImage string) *appsv1.Deployment {
	labels := ObserverLabels(v)
	selectorLabels := ObserverSelectorLabels(v)
	replicas := int32(1)

	args := buildObserverArgs(v)
	resources := v.GetObserverResources()

	containers := []corev1.Container{
		{
			Name:      "observer",
			Image:     operatorImage,
			Command:   []string{ManagerBinary},
			Args:      append([]string{"observer"}, args...),
			Resources: resources,
			Ports: []corev1.ContainerPort{
				{
					Name:          "health",
					ContainerPort: ObserverHealthPort,
					Protocol:      corev1.ProtocolTCP,
				},
			},
			Env:          buildObserverEnv(v),
			VolumeMounts: buildObserverVolumeMounts(v),
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: "/readyz",
						Port: intstr.FromInt32(ObserverHealthPort),
					},
				},
				PeriodSeconds:    2,
				FailureThreshold: 1,
				SuccessThreshold: 1,
			},
			LivenessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: "/healthz",
						Port: intstr.FromInt32(ObserverHealthPort),
					},
				},
				PeriodSeconds:    10,
				FailureThreshold: 3,
			},
		},
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ObserverDeploymentName(v),
			Namespace: v.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: selectorLabels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: ObserverServiceAccountName(v),
					// The observer never calls the Kubernetes API, so it gets no
					// token either: an unmounted token cannot be stolen out of a
					// compromised observer pod (ADR 0012 D8 step 2).
					AutomountServiceAccountToken: ptr.To(false),
					Containers:                   containers,
					Volumes:                      buildObserverVolumes(v),
				},
			},
		},
	}
}

func buildObserverArgs(v *vkov1.Valkey) []string {
	headlessSvc := fmt.Sprintf("%s.%s.svc.cluster.local",
		common.HeadlessServiceName(v, common.ComponentValkey),
		v.Namespace,
	)

	args := []string{
		fmt.Sprintf("--namespace=%s", v.Namespace),
		fmt.Sprintf("--cluster-name=%s", v.Name),
		"--health-addr=:8084",
		"--poll-interval=2s",
		fmt.Sprintf("--valkey-headless-svc=%s", headlessSvc),
		fmt.Sprintf("--replicas=%d", v.Spec.Replicas),
		fmt.Sprintf("--observer-db=%d", v.GetObserverDB()),
	}

	if v.IsTLSEnabled() {
		args = append(args,
			"--tls-enabled=true",
			fmt.Sprintf("--tls-ca-cert=%s/ca.crt", TLSMountPath),
		)
		if v.IsObserverMTLSActive() {
			args = append(args,
				fmt.Sprintf("--tls-cert=%s/tls.crt", TLSMountPath),
				fmt.Sprintf("--tls-key=%s/tls.key", TLSMountPath),
			)
		}
		args = append(args,
			fmt.Sprintf("--valkey-mtls=%v", v.IsObserverValkeyMTLSEnabled()),
			fmt.Sprintf("--sentinel-mtls=%v", v.IsObserverSentinelMTLSEnabled()),
		)
	}

	if v.IsSentinelEnabled() {
		sentinelAddrs := buildSentinelAddrs(v)
		args = append(args,
			"--sentinel-enabled=true",
			fmt.Sprintf("--sentinel-addrs=%s", sentinelAddrs),
			fmt.Sprintf("--sentinel-monitor=%s", SentinelMonitorName(v)),
		)
		if v.IsSentinelAuthDisabled() {
			args = append(args, "--sentinel-disable-auth=true")
		}
	}

	args = append(args, fmt.Sprintf("--log-level=%s", v.GetObserverLogLevel()))

	uw := v.GetObserverUnreadyWhen()
	args = append(args,
		fmt.Sprintf("--unready-when-master-unreachable=%v", vkov1.UnreadyWhenDefault(uw.MasterUnreachable)),
		fmt.Sprintf("--unready-when-write-test-failure=%v", vkov1.UnreadyWhenDefault(uw.WriteTestFailure)),
		fmt.Sprintf("--unready-when-read-test-failure=%v", vkov1.UnreadyWhenDefault(uw.ReadTestFailure)),
		fmt.Sprintf("--unready-when-replica-sync-failure=%v", vkov1.UnreadyWhenDefault(uw.ReplicaSyncFailure)),
		fmt.Sprintf("--unready-when-replica-read-test-failure=%v", vkov1.UnreadyWhenDefault(uw.ReplicaReadTestFailure)),
		fmt.Sprintf("--unready-when-sentinel-unreachable=%v", vkov1.UnreadyWhenDefault(uw.SentinelUnreachable)),
		fmt.Sprintf("--unready-when-sentinel-quorum-failure=%v", vkov1.UnreadyWhenDefault(uw.SentinelQuorumFailure)),
		fmt.Sprintf("--unready-when-sentinel-master-down=%v", vkov1.UnreadyWhenDefault(uw.SentinelMasterDown)),
		fmt.Sprintf("--unready-when-sentinel-master-hostname-invalid=%v", vkov1.UnreadyWhenDefault(uw.SentinelMasterHostnameInvalid)),
		fmt.Sprintf("--unready-when-sentinel-replica-hostnames-invalid=%v", vkov1.UnreadyWhenDefault(uw.SentinelReplicaHostnamesInvalid)),
	)

	return args
}

// buildSentinelAddrs builds a comma-separated list of sentinel addresses.
func buildSentinelAddrs(v *vkov1.Valkey) string {
	sentinelReplicas := int32(3)
	if v.Spec.Sentinel != nil && v.Spec.Sentinel.Replicas > 0 {
		sentinelReplicas = v.Spec.Sentinel.Replicas
	}

	port := SentinelPort
	if v.IsTLSEnabled() {
		port = SentinelTLSPort
	}

	headlessSvc := common.HeadlessServiceName(v, common.ComponentSentinel)
	var addrs []string
	for i := int32(0); i < sentinelReplicas; i++ {
		addr := fmt.Sprintf("%s-sentinel-%d.%s.%s.svc.cluster.local:%d",
			v.Name, i, headlessSvc, v.Namespace, port)
		addrs = append(addrs, addr)
	}
	return strings.Join(addrs, ",")
}

func buildObserverEnv(v *vkov1.Valkey) []corev1.EnvVar {
	envVars := []corev1.EnvVar{
		{
			Name: PodNamespaceEnvName,
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.namespace",
				},
			},
		},
	}

	if v.IsAuthEnabled() {
		envVars = append(envVars, corev1.EnvVar{
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

	return envVars
}

func buildObserverVolumeMounts(v *vkov1.Valkey) []corev1.VolumeMount {
	if !v.IsTLSEnabled() {
		return nil
	}
	return []corev1.VolumeMount{
		{
			Name:      TLSVolumeName,
			MountPath: TLSMountPath,
			ReadOnly:  true,
		},
	}
}

func buildObserverVolumes(v *vkov1.Valkey) []corev1.Volume {
	if !v.IsTLSEnabled() {
		return nil
	}

	secretName := observerTLSSecretName(v)
	secretVolume := &corev1.SecretVolumeSource{
		SecretName: secretName,
	}

	// Without mTLS the observer only needs the CA certificate for server
	// verification — do not expose the private key unnecessarily.
	if !v.IsObserverMTLSActive() {
		secretVolume.Items = []corev1.KeyToPath{
			{Key: TLSCACertKey, Path: TLSCACertKey},
		}
	}

	return []corev1.Volume{
		{
			Name: TLSVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: secretVolume,
			},
		},
	}
}

// observerTLSSecretName returns the TLS secret name for the observer.
// It reuses the Valkey TLS secret.
func observerTLSSecretName(v *vkov1.Valkey) string {
	if v.IsTLSSecretProvided() {
		return v.Spec.TLS.SecretName
	}
	return ValkeyCertificateName(v)
}

// observerIdentityChanged reports whether the ServiceAccount the observer pod runs
// under, or whether it mounts that ServiceAccount's token, differs between the two
// pod specs. An unset AutomountServiceAccountToken means "mount it", so it compares
// equal to an explicit true.
func observerIdentityChanged(desired, current *corev1.PodSpec) bool {
	if desired.ServiceAccountName != current.ServiceAccountName {
		return true
	}
	return automountsToken(desired) != automountsToken(current)
}

// automountsToken reports whether the pod spec mounts its ServiceAccount token,
// resolving the nil default (mount) to true.
func automountsToken(spec *corev1.PodSpec) bool {
	return spec.AutomountServiceAccountToken == nil || *spec.AutomountServiceAccountToken
}

// ObserverDeploymentHasChanged returns true if the desired observer
// Deployment differs from the current one in meaningful ways.
func ObserverDeploymentHasChanged(desired, current *appsv1.Deployment) bool {
	if *desired.Spec.Replicas != *current.Spec.Replicas {
		return true
	}

	// The pod identity is part of the comparison: without it an observer created
	// before ADR 0012 D8 step 2 would keep the sidecar ServiceAccount and its
	// mounted token forever, because nothing else about the pod changed.
	if observerIdentityChanged(&desired.Spec.Template.Spec, &current.Spec.Template.Spec) {
		return true
	}

	desiredContainers := desired.Spec.Template.Spec.Containers
	currentContainers := current.Spec.Template.Spec.Containers

	if len(desiredContainers) != len(currentContainers) {
		return true
	}

	if len(desiredContainers) > 0 && len(currentContainers) > 0 {
		if desiredContainers[0].Image != currentContainers[0].Image {
			return true
		}
		if fmt.Sprintf("%v", desiredContainers[0].Args) != fmt.Sprintf("%v", currentContainers[0].Args) {
			return true
		}
		if fmt.Sprintf("%v", desiredContainers[0].Resources) != fmt.Sprintf("%v", currentContainers[0].Resources) {
			return true
		}
	}

	return false
}
