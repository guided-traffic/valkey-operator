package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

func metricsEnabled(v *vkov1.Valkey) {
	v.Spec.Metrics = &vkov1.MetricsSpec{
		Enabled:        true,
		ServiceMonitor: &vkov1.ServiceMonitorSpec{Enabled: true},
	}
}

func getServiceMonitor(ctx context.Context, r *ValkeyReconciler, name, ns string) error {
	sm := &unstructured.Unstructured{}
	sm.SetGroupVersionKind(builder.ServiceMonitorGVK())
	return r.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, sm)
}

// TestReconcileMetrics_CreatesServiceAndServiceMonitor verifies that enabling
// metrics + serviceMonitor creates both the metrics Service and the ServiceMonitor.
func TestReconcileMetrics_CreatesServiceAndServiceMonitor(t *testing.T) {
	v := newTestValkey("test", "default", metricsEnabled)
	r, c := newTestReconciler(v)
	ctx := context.Background()

	require.NoError(t, r.reconcileMetrics(ctx, v))

	svc := &corev1.Service{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "test-metrics", Namespace: "default"}, svc))
	require.Len(t, svc.Spec.Ports, 1)
	assert.Equal(t, builder.ExporterPortName, svc.Spec.Ports[0].Name)
	assert.Equal(t, "true", svc.Labels[builder.MetricsServiceLabel])

	err := getServiceMonitor(ctx, r, "test-metrics", "default")
	require.NoError(t, err, "ServiceMonitor must be created")
}

// TestReconcileMetrics_ServiceOnlyWhenServiceMonitorDisabled verifies the Service
// is created but no ServiceMonitor when serviceMonitor is disabled.
func TestReconcileMetrics_ServiceOnlyWhenServiceMonitorDisabled(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Metrics = &vkov1.MetricsSpec{Enabled: true}
	})
	r, c := newTestReconciler(v)
	ctx := context.Background()

	require.NoError(t, r.reconcileMetrics(ctx, v))

	svc := &corev1.Service{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "test-metrics", Namespace: "default"}, svc))

	err := getServiceMonitor(ctx, r, "test-metrics", "default")
	assert.True(t, apierrors.IsNotFound(err), "no ServiceMonitor expected when disabled")
}

// TestReconcileMetrics_CleanupWhenDisabled verifies both resources are removed
// when metrics are turned off.
func TestReconcileMetrics_CleanupWhenDisabled(t *testing.T) {
	v := newTestValkey("test", "default", metricsEnabled)
	r, c := newTestReconciler(v)
	ctx := context.Background()

	// First create.
	require.NoError(t, r.reconcileMetrics(ctx, v))
	svc := &corev1.Service{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "test-metrics", Namespace: "default"}, svc))

	// Now disable and reconcile again.
	v.Spec.Metrics.Enabled = false
	require.NoError(t, r.reconcileMetrics(ctx, v))

	err := c.Get(ctx, types.NamespacedName{Name: "test-metrics", Namespace: "default"}, svc)
	assert.True(t, apierrors.IsNotFound(err), "metrics Service must be deleted when disabled")

	err = getServiceMonitor(ctx, r, "test-metrics", "default")
	assert.True(t, apierrors.IsNotFound(err), "ServiceMonitor must be deleted when disabled")
}

// TestReconcile_EnablesExporterSidecar verifies a full Reconcile injects the
// exporter container into the StatefulSet and creates the metrics Service.
func TestReconcile_EnablesExporterSidecar(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Metrics = &vkov1.MetricsSpec{Enabled: true}
	})
	r, c := newTestReconciler(v)
	ctx := context.Background()

	reconcileOnce(t, r, "test", "default")

	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "test", Namespace: "default"}, sts))
	var found bool
	for _, cont := range sts.Spec.Template.Spec.Containers {
		if cont.Name == builder.ExporterContainerName {
			found = true
		}
	}
	assert.True(t, found, "exporter container must be present in the StatefulSet")

	svc := &corev1.Service{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "test-metrics", Namespace: "default"}, svc))
}
