package builder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// findContainer returns a pointer to the container with the given name, or nil.
func findContainer(pod corev1.PodSpec, name string) *corev1.Container {
	for i := range pod.Containers {
		if pod.Containers[i].Name == name {
			return &pod.Containers[i]
		}
	}
	return nil
}

// envValue returns the plain value of the named env var (empty if absent or valueFrom).
func envValue(c *corev1.Container, name string) string {
	for _, e := range c.Env {
		if e.Name == name {
			return e.Value
		}
	}
	return ""
}

func hasSecretEnv(c *corev1.Container, name string) bool {
	for _, e := range c.Env {
		if e.Name == name && e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
			return true
		}
	}
	return false
}

// --- exporter sidecar ---

func TestBuildStatefulSet_NoExporterWhenDisabled(t *testing.T) {
	v := newTestValkey("test")
	sts := BuildStatefulSet(v, testOperatorImage)
	assert.Nil(t, findContainer(sts.Spec.Template.Spec, ExporterContainerName),
		"exporter container must be absent when metrics disabled")
}

func TestBuildStatefulSet_ExporterWhenEnabled(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Metrics = &vkov1.MetricsSpec{Enabled: true}
	})
	sts := BuildStatefulSet(v, testOperatorImage)

	exp := findContainer(sts.Spec.Template.Spec, ExporterContainerName)
	require.NotNil(t, exp, "exporter container must be present when metrics enabled")
	assert.Equal(t, vkov1.DefaultMetricsExporterImage, exp.Image)
	assert.Equal(t, "redis://localhost:6379", envValue(exp, "REDIS_ADDR"))

	require.Len(t, exp.Ports, 1)
	assert.Equal(t, ExporterPortName, exp.Ports[0].Name)
	assert.Equal(t, vkov1.DefaultMetricsExporterPort, exp.Ports[0].ContainerPort)

	// The exporter must never gate pod readiness.
	assert.Nil(t, exp.ReadinessProbe, "exporter must have no readiness probe")
}

func TestBuildExporterContainer_CustomImagePortResources(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Metrics = &vkov1.MetricsSpec{
			Enabled:   true,
			Image:     "my/exporter:9.9",
			Port:      19121,
			ExtraArgs: []string{"--check-keys=*"},
		}
	})
	c := buildExporterContainer(v)
	assert.Equal(t, "my/exporter:9.9", c.Image)
	assert.Equal(t, int32(19121), c.Ports[0].ContainerPort)
	assert.Equal(t, ":19121", envValue(&c, "REDIS_EXPORTER_WEB_LISTEN_ADDRESS"))
	assert.Contains(t, c.Args, "--check-keys=*")
}

func TestBuildExporterContainer_Auth(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Metrics = &vkov1.MetricsSpec{Enabled: true}
		v.Spec.Auth = &vkov1.AuthSpec{SecretName: "sec", SecretPasswordKey: "password"}
	})
	c := buildExporterContainer(v)
	assert.True(t, hasSecretEnv(&c, "REDIS_PASSWORD"), "REDIS_PASSWORD must come from the auth Secret")
}

func TestBuildExporterContainer_TLS(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Metrics = &vkov1.MetricsSpec{Enabled: true}
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})
	c := buildExporterContainer(v)
	assert.Equal(t, "rediss://localhost:16379", envValue(&c, "REDIS_ADDR"))
	assert.Equal(t, "true", envValue(&c, "REDIS_EXPORTER_SKIP_TLS_VERIFICATION"))
	assert.Equal(t, TLSMountPath+"/ca.crt", envValue(&c, "REDIS_EXPORTER_TLS_CA_CERT_FILE"))

	var mounted bool
	for _, m := range c.VolumeMounts {
		if m.Name == TLSVolumeName && m.MountPath == TLSMountPath {
			mounted = true
		}
	}
	assert.True(t, mounted, "exporter must mount the TLS volume when TLS enabled")
}

// --- metrics Service ---

func TestBuildMetricsService(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Metrics = &vkov1.MetricsSpec{
			Enabled: true,
			Service: &vkov1.MetricsServiceSpec{Labels: map[string]string{"team": "sre"}},
		}
	})
	svc := BuildMetricsService(v)

	assert.Equal(t, "test-metrics", svc.Name)
	assert.Equal(t, "default", svc.Namespace)
	assert.Equal(t, "true", svc.Labels[MetricsServiceLabel], "metrics marker label must be set")
	assert.Equal(t, "sre", svc.Labels["team"], "user labels must be merged")

	// Selector must target all Valkey pods (not role-restricted).
	assert.Equal(t, common.SelectorLabels(v, common.ComponentValkey), svc.Spec.Selector)

	require.Len(t, svc.Spec.Ports, 1)
	assert.Equal(t, ExporterPortName, svc.Spec.Ports[0].Name)
	assert.Equal(t, vkov1.DefaultMetricsExporterPort, svc.Spec.Ports[0].Port)
	assert.Equal(t, ExporterPortName, svc.Spec.Ports[0].TargetPort.StrVal)
}

// --- ServiceMonitor ---

func TestBuildServiceMonitor(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Metrics = &vkov1.MetricsSpec{
			Enabled: true,
			ServiceMonitor: &vkov1.ServiceMonitorSpec{
				Enabled:       true,
				Interval:      "15s",
				ScrapeTimeout: "10s",
				Labels:        map[string]string{"release": "prometheus"},
			},
		}
	})
	sm := BuildServiceMonitor(v)

	assert.Equal(t, ServiceMonitorGVK(), sm.GroupVersionKind())
	assert.Equal(t, "test-metrics", sm.GetName())
	assert.Equal(t, "default", sm.GetNamespace())
	assert.Equal(t, "prometheus", sm.GetLabels()["release"])

	spec, ok := sm.Object["spec"].(map[string]interface{})
	require.True(t, ok)

	// Selector must include the metrics marker label so only the metrics Service matches.
	sel := spec["selector"].(map[string]interface{})["matchLabels"].(map[string]interface{})
	assert.Equal(t, "true", sel[MetricsServiceLabel])

	endpoints := spec["endpoints"].([]interface{})
	require.Len(t, endpoints, 1)
	ep := endpoints[0].(map[string]interface{})
	assert.Equal(t, ExporterPortName, ep["port"])
	assert.Equal(t, "15s", ep["interval"])
	assert.Equal(t, "10s", ep["scrapeTimeout"])
	assert.Equal(t, "/metrics", ep["path"])
}

func TestBuildServiceMonitor_DefaultInterval(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Metrics = &vkov1.MetricsSpec{
			Enabled:        true,
			ServiceMonitor: &vkov1.ServiceMonitorSpec{Enabled: true},
		}
	})
	sm := BuildServiceMonitor(v)
	spec := sm.Object["spec"].(map[string]interface{})
	ep := spec["endpoints"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, vkov1.DefaultMetricsScrapeInterval, ep["interval"])
	_, hasTimeout := ep["scrapeTimeout"]
	assert.False(t, hasTimeout, "scrapeTimeout must be omitted when unset")
}

// spec.metrics.extraArgs is the only way to reach exporter flags the operator does
// not model itself (--check-keys, --script). They have to land on the container as
// arguments and nowhere else: putting them in Command would replace the image
// entrypoint, and dropping them silently would leave the user with an exporter that
// looks configured and is not.
func TestBuildExporterContainer_ExtraArgs(t *testing.T) {
	extra := []string{"--check-keys=queue:*", "--is-cluster=false"}
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Metrics = &vkov1.MetricsSpec{Enabled: true, ExtraArgs: extra}
	})

	exp := findContainer(BuildStatefulSet(v, testOperatorImage).Spec.Template.Spec, ExporterContainerName)
	require.NotNil(t, exp)
	assert.Equal(t, extra, exp.Args, "every configured extra flag must reach the exporter")
	assert.Empty(t, exp.Command, "extra args must not replace the image entrypoint")
}

// Without extraArgs the container carries no Args at all, so the image default
// command line stays in force. A builder that always appended (an empty slice, a
// stray separator) would change the exporter invocation for every user who never
// asked for it.
func TestBuildExporterContainer_NoArgsWithoutExtraArgs(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Metrics = &vkov1.MetricsSpec{Enabled: true}
	})

	exp := findContainer(BuildStatefulSet(v, testOperatorImage).Spec.Template.Spec, ExporterContainerName)
	require.NotNil(t, exp)
	assert.Nil(t, exp.Args)
}

// The ServiceMonitor is created with an owner reference instead of
// controllerutil.SetControllerReference, because it is an unstructured object of a
// CRD the operator has no typed dependency on. The reference is what makes
// Kubernetes garbage-collect the ServiceMonitor when the Valkey CR is deleted, so
// every field it is matched on has to be right: a wrong Kind or UID leaves the
// ServiceMonitor behind, scraping a Service that no longer exists.
func TestServiceMonitorOwnerRef(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.UID = "0f9c1e2a-7b3d-4c5e-8f10-2a3b4c5d6e7f"
	})

	ref := ServiceMonitorOwnerRef(v)

	assert.Equal(t, vkov1.GroupVersion.String(), ref.APIVersion)
	assert.Equal(t, "Valkey", ref.Kind)
	assert.Equal(t, "test", ref.Name)
	assert.Equal(t, v.UID, ref.UID, "garbage collection matches on the UID, not the name")
}

// spec.metrics.resources is the only limit on the exporter sidecar. Without it the
// container runs unbounded next to Valkey in the same pod, and on a node under
// memory pressure an unbounded sidecar makes the whole pod a better eviction
// candidate -- taking the data instance with it.
func TestBuildExporterContainer_Resources(t *testing.T) {
	want := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("10m"),
			corev1.ResourceMemory: resource.MustParse("32Mi"),
		},
	}
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Metrics = &vkov1.MetricsSpec{Enabled: true, Resources: want.DeepCopy()}
	})

	exp := findContainer(BuildStatefulSet(v, testOperatorImage).Spec.Template.Spec, ExporterContainerName)
	require.NotNil(t, exp)
	assert.True(t, want.Limits[corev1.ResourceMemory].Equal(exp.Resources.Limits[corev1.ResourceMemory]),
		"the configured memory limit must reach the exporter container")
	assert.True(t, want.Requests[corev1.ResourceCPU].Equal(exp.Resources.Requests[corev1.ResourceCPU]),
		"the configured CPU request must reach the exporter container")
}

// Without spec.metrics.resources the exporter carries no requirements at all,
// rather than a zero-valued block that would pin it to a BestEffort-looking spec
// the user never wrote.
func TestBuildExporterContainer_NoResourcesByDefault(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Metrics = &vkov1.MetricsSpec{Enabled: true}
	})

	exp := findContainer(BuildStatefulSet(v, testOperatorImage).Spec.Template.Spec, ExporterContainerName)
	require.NotNil(t, exp)
	assert.Empty(t, exp.Resources.Limits)
	assert.Empty(t, exp.Resources.Requests)
}
