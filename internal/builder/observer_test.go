package builder

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// --- ObserverDeploymentName ---

func TestObserverDeploymentName(t *testing.T) {
	v := newTestValkey("my-valkey")
	assert.Equal(t, "my-valkey-observer", ObserverDeploymentName(v))
}

// --- ObserverLabels ---

func TestObserverLabels(t *testing.T) {
	v := newTestValkey("test")
	labels := ObserverLabels(v)

	assert.Equal(t, ComponentObserver, labels[common.LabelComponent])
	assert.Equal(t, "test", labels[common.LabelInstance])
	assert.Equal(t, common.ManagedBy, labels[common.LabelManagedBy])
	assert.Equal(t, "valkey", labels[common.LabelName])
	assert.Equal(t, "test", labels[common.LabelCluster])
}

// --- ObserverSelectorLabels ---

func TestObserverSelectorLabels(t *testing.T) {
	v := newTestValkey("test")
	labels := ObserverSelectorLabels(v)

	assert.Len(t, labels, 2)
	assert.Equal(t, ComponentObserver, labels[common.LabelComponent])
	assert.Equal(t, "test", labels[common.LabelCluster])
}

// --- BuildObserverDeployment ---

func TestBuildObserverDeployment_Standalone(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})

	deploy := BuildObserverDeployment(v, testOperatorImage)

	assert.Equal(t, "test-observer", deploy.Name)
	assert.Equal(t, "default", deploy.Namespace)
	assert.Equal(t, int32(1), *deploy.Spec.Replicas)

	// Labels.
	assert.Equal(t, ComponentObserver, deploy.Labels[common.LabelComponent])
	assert.Equal(t, "test", deploy.Labels[common.LabelInstance])

	// Selector.
	assert.Equal(t, ComponentObserver, deploy.Spec.Selector.MatchLabels[common.LabelComponent])
	assert.Equal(t, "test", deploy.Spec.Selector.MatchLabels[common.LabelCluster])

	// Container.
	require.Len(t, deploy.Spec.Template.Spec.Containers, 1)
	c := deploy.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "observer", c.Name)
	assert.Equal(t, testOperatorImage, c.Image)
	assert.Equal(t, []string{"./manager"}, c.Command)
	assert.Equal(t, "observer", c.Args[0], "first arg should be observer subcommand")

	// Ports.
	require.Len(t, c.Ports, 1)
	assert.Equal(t, int32(ObserverHealthPort), c.Ports[0].ContainerPort)
	assert.Equal(t, "health", c.Ports[0].Name)

	// Probes.
	require.NotNil(t, c.ReadinessProbe)
	assert.Equal(t, "/readyz", c.ReadinessProbe.HTTPGet.Path)
	assert.Equal(t, intstr.FromInt32(ObserverHealthPort), c.ReadinessProbe.HTTPGet.Port)
	require.NotNil(t, c.LivenessProbe)
	assert.Equal(t, "/healthz", c.LivenessProbe.HTTPGet.Path)

	// Default resources.
	assert.Equal(t, resource.MustParse("50m"), c.Resources.Requests[corev1.ResourceCPU])
	assert.Equal(t, resource.MustParse("64Mi"), c.Resources.Requests[corev1.ResourceMemory])
	assert.Empty(t, c.Resources.Limits)

	// No TLS volumes.
	assert.Empty(t, c.VolumeMounts)
	assert.Empty(t, deploy.Spec.Template.Spec.Volumes)

	// ServiceAccount.
	assert.Equal(t, "test-sidecar", deploy.Spec.Template.Spec.ServiceAccountName)
}

func TestBuildObserverDeployment_Args_Standalone(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})

	deploy := BuildObserverDeployment(v, testOperatorImage)
	args := deploy.Spec.Template.Spec.Containers[0].Args

	assert.Contains(t, args, "--namespace=default")
	assert.Contains(t, args, "--cluster-name=test")
	assert.Contains(t, args, "--health-addr=:8084")
	assert.Contains(t, args, "--poll-interval=2s")
	assert.Contains(t, args, "--replicas=1")
	assert.Contains(t, args, "--observer-db=15")
	assert.Contains(t, args, fmt.Sprintf("--valkey-headless-svc=%s.default.svc.cluster.local",
		common.HeadlessServiceName(v, common.ComponentValkey)))

	// No Sentinel or TLS args.
	for _, arg := range args {
		assert.NotContains(t, arg, "--sentinel-enabled")
		assert.NotContains(t, arg, "--tls-enabled")
	}
}

func TestBuildObserverDeployment_WithTLS(t *testing.T) {
	// TLS enabled, but both mTLS=false (default): CA cert mounted, no client cert args.
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "ca"},
			},
		}
	})

	deploy := BuildObserverDeployment(v, testOperatorImage)
	c := deploy.Spec.Template.Spec.Containers[0]

	// TLS enabled flag present, mTLS flags at default false.
	assert.Contains(t, c.Args, "--tls-enabled=true")
	assert.Contains(t, c.Args, "--valkey-mtls=false")
	assert.Contains(t, c.Args, "--sentinel-mtls=false")

	// CA cert path arg present for server verification.
	assert.Contains(t, c.Args, fmt.Sprintf("--tls-ca-cert=%s/ca.crt", TLSMountPath))

	// No client cert path args when mTLS is inactive.
	for _, arg := range c.Args {
		assert.NotContains(t, arg, "--tls-cert")
		assert.NotContains(t, arg, "--tls-key")
	}

	// TLS volume and mount present (needed for CA cert).
	require.Len(t, c.VolumeMounts, 1)
	assert.Equal(t, TLSVolumeName, c.VolumeMounts[0].Name)
	assert.Equal(t, TLSMountPath, c.VolumeMounts[0].MountPath)
	assert.True(t, c.VolumeMounts[0].ReadOnly)

	// Volume projects only ca.crt when mTLS is inactive.
	require.Len(t, deploy.Spec.Template.Spec.Volumes, 1)
	assert.Equal(t, TLSVolumeName, deploy.Spec.Template.Spec.Volumes[0].Name)
	secretVol := deploy.Spec.Template.Spec.Volumes[0].Secret
	require.NotNil(t, secretVol.Items)
	require.Len(t, secretVol.Items, 1)
	assert.Equal(t, "ca.crt", secretVol.Items[0].Key)
	assert.Equal(t, "ca.crt", secretVol.Items[0].Path)
}

func TestBuildObserverDeployment_WithTLS_WithMTLS(t *testing.T) {
	// TLS enabled + Valkey mTLS=true: cert must be mounted and cert path args present.
	tr := true
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{
			Enabled: true,
			MTLS:    &vkov1.ObserverMTLSSpec{Valkey: &tr},
		}
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "ca"},
			},
		}
	})

	deploy := BuildObserverDeployment(v, testOperatorImage)
	c := deploy.Spec.Template.Spec.Containers[0]

	// Cert path args present.
	assert.Contains(t, c.Args, "--tls-enabled=true")
	assert.Contains(t, c.Args, fmt.Sprintf("--tls-ca-cert=%s/ca.crt", TLSMountPath))
	assert.Contains(t, c.Args, fmt.Sprintf("--tls-cert=%s/tls.crt", TLSMountPath))
	assert.Contains(t, c.Args, fmt.Sprintf("--tls-key=%s/tls.key", TLSMountPath))
	assert.Contains(t, c.Args, "--valkey-mtls=true")

	// TLS volume mounted.
	require.Len(t, c.VolumeMounts, 1)
	assert.Equal(t, TLSVolumeName, c.VolumeMounts[0].Name)
	assert.Equal(t, TLSMountPath, c.VolumeMounts[0].MountPath)
	assert.True(t, c.VolumeMounts[0].ReadOnly)

	// TLS volume present — full secret, no Items projection with mTLS.
	require.Len(t, deploy.Spec.Template.Spec.Volumes, 1)
	assert.Equal(t, TLSVolumeName, deploy.Spec.Template.Spec.Volumes[0].Name)
	secretVol := deploy.Spec.Template.Spec.Volumes[0].Secret
	assert.Nil(t, secretVol.Items)
}

func TestBuildObserverDeployment_WithSentinel(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	deploy := BuildObserverDeployment(v, testOperatorImage)
	args := deploy.Spec.Template.Spec.Containers[0].Args

	assert.Contains(t, args, "--sentinel-enabled=true")
	assert.Contains(t, args, fmt.Sprintf("--sentinel-monitor=%s", SentinelMonitorName(v)))
	assert.Contains(t, args, "--replicas=3")

	// Sentinel addresses should be present.
	found := false
	for _, arg := range args {
		if len(arg) > 16 && arg[:16] == "--sentinel-addrs" {
			found = true
			break
		}
	}
	assert.True(t, found, "should contain --sentinel-addrs argument")
}

func TestBuildObserverDeployment_WithSentinelDisableAuth(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
		v.Spec.Auth = &vkov1.AuthSpec{SecretName: "my-secret", SecretPasswordKey: "password"}
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3, DisableAuth: true}
	})

	deploy := BuildObserverDeployment(v, testOperatorImage)
	args := deploy.Spec.Template.Spec.Containers[0].Args

	assert.Contains(t, args, "--sentinel-disable-auth=true")
}

func TestBuildObserverDeployment_WithAuth(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
		v.Spec.Auth = &vkov1.AuthSpec{SecretName: "my-secret", SecretPasswordKey: "password"}
	})

	deploy := BuildObserverDeployment(v, testOperatorImage)
	envVars := deploy.Spec.Template.Spec.Containers[0].Env

	// Should have POD_NAMESPACE + AUTH env var.
	require.Len(t, envVars, 2)
	assert.Equal(t, AuthSecretEnvName, envVars[1].Name)
	assert.Equal(t, "my-secret", envVars[1].ValueFrom.SecretKeyRef.Name)
	assert.Equal(t, "password", envVars[1].ValueFrom.SecretKeyRef.Key)
}

func TestBuildObserverDeployment_CustomDB(t *testing.T) {
	db := 5
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true, DB: &db}
	})

	deploy := BuildObserverDeployment(v, testOperatorImage)
	args := deploy.Spec.Template.Spec.Containers[0].Args

	assert.Contains(t, args, "--observer-db=5")
}

func TestBuildObserverDeployment_CustomResources(t *testing.T) {
	customRes := &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("200m"),
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		},
	}
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true, Resources: customRes}
	})

	deploy := BuildObserverDeployment(v, testOperatorImage)
	c := deploy.Spec.Template.Spec.Containers[0]

	assert.Equal(t, resource.MustParse("200m"), c.Resources.Requests[corev1.ResourceCPU])
	assert.Equal(t, resource.MustParse("256Mi"), c.Resources.Requests[corev1.ResourceMemory])
}

func TestBuildObserverDeployment_Namespace(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "production"
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})

	deploy := BuildObserverDeployment(v, testOperatorImage)
	assert.Equal(t, "production", deploy.Namespace)
}

// --- ObserverDeploymentHasChanged ---

func TestObserverDeploymentHasChanged_Identical(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})
	a := BuildObserverDeployment(v, testOperatorImage)
	b := BuildObserverDeployment(v, testOperatorImage)

	assert.False(t, ObserverDeploymentHasChanged(a, b))
}

func TestObserverDeploymentHasChanged_DifferentImage(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})
	a := BuildObserverDeployment(v, testOperatorImage)
	b := BuildObserverDeployment(v, "other-image:v2")

	assert.True(t, ObserverDeploymentHasChanged(a, b))
}

func TestObserverDeploymentHasChanged_DifferentArgs(t *testing.T) {
	v1 := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})
	v2 := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 5
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})
	a := BuildObserverDeployment(v1, testOperatorImage)
	b := BuildObserverDeployment(v2, testOperatorImage)

	assert.True(t, ObserverDeploymentHasChanged(a, b))
}

func TestObserverDeploymentHasChanged_DifferentResources(t *testing.T) {
	v1 := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})
	customRes := &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("500m"),
		},
	}
	v2 := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true, Resources: customRes}
	})
	a := BuildObserverDeployment(v1, testOperatorImage)
	b := BuildObserverDeployment(v2, testOperatorImage)

	assert.True(t, ObserverDeploymentHasChanged(a, b))
}

// --- Observer with TLS secret (not cert-manager) ---

func TestBuildObserverDeployment_WithTLSSecret(t *testing.T) {
	// TLS secret + mTLS active: secret must be mounted as the source volume.
	tr := true
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{
			Enabled: true,
			MTLS:    &vkov1.ObserverMTLSSpec{Valkey: &tr},
		}
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled:    true,
			SecretName: "my-tls-secret",
		}
	})

	deploy := BuildObserverDeployment(v, testOperatorImage)
	require.Len(t, deploy.Spec.Template.Spec.Volumes, 1)
	assert.Equal(t, "my-tls-secret", deploy.Spec.Template.Spec.Volumes[0].Secret.SecretName)
}

// --- Observer mTLS args ---

func TestBuildObserverDeployment_WithTLS_DefaultMTLSArgs(t *testing.T) {
	// No MTLS spec = defaults: both mTLS false, but CA cert + volume always mounted for server verification.
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "ca"},
			},
		}
	})

	deploy := BuildObserverDeployment(v, testOperatorImage)
	c := deploy.Spec.Template.Spec.Containers[0]

	assert.Contains(t, c.Args, "--valkey-mtls=false")
	assert.Contains(t, c.Args, "--sentinel-mtls=false")
	assert.Contains(t, c.Args, fmt.Sprintf("--tls-ca-cert=%s/ca.crt", TLSMountPath))

	// No client cert args when mTLS is inactive.
	for _, arg := range c.Args {
		assert.NotContains(t, arg, "--tls-cert")
		assert.NotContains(t, arg, "--tls-key")
	}

	// TLS volume and mount present for CA cert.
	require.Len(t, c.VolumeMounts, 1)
	assert.Equal(t, TLSVolumeName, c.VolumeMounts[0].Name)
	require.Len(t, deploy.Spec.Template.Spec.Volumes, 1)
	assert.Equal(t, TLSVolumeName, deploy.Spec.Template.Spec.Volumes[0].Name)
	// Only ca.crt projected.
	secretVol := deploy.Spec.Template.Spec.Volumes[0].Secret
	require.NotNil(t, secretVol.Items)
	require.Len(t, secretVol.Items, 1)
	assert.Equal(t, "ca.crt", secretVol.Items[0].Key)
}

func TestBuildObserverDeployment_WithTLS_ExplicitMTLSArgs(t *testing.T) {
	// Explicit: Valkey=false, Sentinel=true — mTLS active via sentinel → cert is mounted.
	f := false
	tr := true
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{
			Enabled: true,
			MTLS:    &vkov1.ObserverMTLSSpec{Valkey: &f, Sentinel: &tr},
		}
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "ca"},
			},
		}
	})

	deploy := BuildObserverDeployment(v, testOperatorImage)
	c := deploy.Spec.Template.Spec.Containers[0]

	assert.Contains(t, c.Args, "--valkey-mtls=false")
	assert.Contains(t, c.Args, "--sentinel-mtls=true")
	assert.Contains(t, c.Args, fmt.Sprintf("--tls-ca-cert=%s/ca.crt", TLSMountPath))
	assert.Contains(t, c.Args, fmt.Sprintf("--tls-cert=%s/tls.crt", TLSMountPath))
	assert.Contains(t, c.Args, fmt.Sprintf("--tls-key=%s/tls.key", TLSMountPath))
	require.Len(t, c.VolumeMounts, 1)
	assert.Equal(t, TLSVolumeName, c.VolumeMounts[0].Name)
	require.Len(t, deploy.Spec.Template.Spec.Volumes, 1)
	assert.Equal(t, TLSVolumeName, deploy.Spec.Template.Spec.Volumes[0].Name)
}

func TestBuildObserverDeployment_NoTLS_NoMTLSArgs(t *testing.T) {
	// Without TLS, mTLS flags must not appear in args
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})

	deploy := BuildObserverDeployment(v, testOperatorImage)
	args := deploy.Spec.Template.Spec.Containers[0].Args

	for _, arg := range args {
		assert.NotContains(t, arg, "valkey-mtls")
		assert.NotContains(t, arg, "sentinel-mtls")
	}
}

func TestBuildObserverDeployment_LogLevel(t *testing.T) {
	tests := []struct {
		name     string
		level    vkov1.ObserverLogLevel
		expected string
	}{
		{name: "nil level defaults to info", expected: "--log-level=info"},
		{name: "debug level", level: vkov1.ObserverLogLevelDebug, expected: "--log-level=debug"},
		{name: "info level", level: vkov1.ObserverLogLevelInfo, expected: "--log-level=info"},
		{name: "warn level", level: vkov1.ObserverLogLevelWarn, expected: "--log-level=warn"},
		{name: "error level", level: vkov1.ObserverLogLevelError, expected: "--log-level=error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newTestValkey("test", func(v *vkov1.Valkey) {
				v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true, LogLevel: tt.level}
			})
			deploy := BuildObserverDeployment(v, testOperatorImage)
			args := deploy.Spec.Template.Spec.Containers[0].Args
			assert.Contains(t, args, tt.expected)
		})
	}
}

func TestBuildObserverDeployment_UnreadyWhen_Defaults(t *testing.T) {
	// Without any unreadyWhen config, all flags default to true.
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})

	deploy := BuildObserverDeployment(v, testOperatorImage)
	args := deploy.Spec.Template.Spec.Containers[0].Args

	expected := []string{
		"--unready-when-master-unreachable=true",
		"--unready-when-write-test-failure=true",
		"--unready-when-read-test-failure=true",
		"--unready-when-replica-sync-failure=true",
		"--unready-when-replica-read-test-failure=true",
		"--unready-when-sentinel-unreachable=true",
		"--unready-when-sentinel-quorum-failure=true",
		"--unready-when-sentinel-master-down=true",
		"--unready-when-sentinel-master-hostname-invalid=true",
		"--unready-when-sentinel-replica-hostnames-invalid=true",
	}
	for _, e := range expected {
		assert.Contains(t, args, e)
	}
}

func TestBuildObserverDeployment_UnreadyWhen_PartialFalse(t *testing.T) {
	// When some fields are explicitly false, the corresponding flags must be false.
	f := false
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{
			Enabled: true,
			UnreadyWhen: &vkov1.ObserverUnreadyWhenSpec{
				ReplicaSyncFailure:              &f,
				SentinelUnreachable:             &f,
				SentinelReplicaHostnamesInvalid: &f,
			},
		}
	})

	deploy := BuildObserverDeployment(v, testOperatorImage)
	args := deploy.Spec.Template.Spec.Containers[0].Args

	assert.Contains(t, args, "--unready-when-master-unreachable=true")
	assert.Contains(t, args, "--unready-when-replica-sync-failure=false")
	assert.Contains(t, args, "--unready-when-sentinel-unreachable=false")
	assert.Contains(t, args, "--unready-when-sentinel-replica-hostnames-invalid=false")
}
