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
	// ComponentObserver is the component value for observer instances.
	ComponentObserver = "observer"

	// ObserverHealthPort is the port on which the observer health endpoint listens.
	ObserverHealthPort = 8084
)

// ObserverDeploymentName returns the name for the observer Deployment.
func ObserverDeploymentName(v *vkov1.Valkey) string {
	return fmt.Sprintf("%s-observer", v.Name)
}

// ObserverLabels returns the labels for observer resources.
func ObserverLabels(v *vkov1.Valkey) map[string]string {
	return map[string]string{
		common.LabelComponent: ComponentObserver,
		common.LabelInstance:  v.Name,
		common.LabelManagedBy: common.ManagedBy,
		common.LabelName:      "valkey",
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
			Command:   []string{"./manager"},
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
					ServiceAccountName: fmt.Sprintf("%s-sidecar", v.Name),
					Containers:         containers,
					Volumes:            buildObserverVolumes(v),
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
		args = append(args, "--tls-enabled=true")
		if v.IsObserverMTLSActive() {
			args = append(args,
				fmt.Sprintf("--tls-ca-cert=%s/ca.crt", TLSMountPath),
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
			Name: "POD_NAMESPACE",
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
	if !v.IsTLSEnabled() || !v.IsObserverMTLSActive() {
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
	if !v.IsTLSEnabled() || !v.IsObserverMTLSActive() {
		return nil
	}

	secretName := observerTLSSecretName(v)
	return []corev1.Volume{
		{
			Name: TLSVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: secretName,
				},
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

// ObserverDeploymentHasChanged returns true if the desired observer
// Deployment differs from the current one in meaningful ways.
func ObserverDeploymentHasChanged(desired, current *appsv1.Deployment) bool {
	if *desired.Spec.Replicas != *current.Spec.Replicas {
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
