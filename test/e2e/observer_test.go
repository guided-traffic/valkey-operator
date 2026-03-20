//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestE2E_Observer tests the observer deployment lifecycle.
func TestE2E_Observer(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-observer"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	t.Run("observer deployment with HA cluster", func(t *testing.T) {
		name := "obs-ha"
		valkey := buildValkeyObject(name, ns, map[string]interface{}{
			"replicas": int64(3),
			"image":    "valkey/valkey:8.0",
			"sentinel": map[string]interface{}{
				"enabled":  true,
				"replicas": int64(3),
			},
			"observer": map[string]interface{}{
				"enabled": true,
			},
		})

		t.Log("Creating HA Valkey with observer")
		tc.createValkey(t, ns, valkey)
		defer tc.deleteValkey(t, ns, name)

		t.Log("Waiting for Valkey StatefulSet to be ready")
		tc.waitForStatefulSetReady(t, ns, name, 3)

		t.Log("Waiting for Sentinel StatefulSet to be ready")
		tc.waitForStatefulSetReady(t, ns, fmt.Sprintf("%s-sentinel", name), 3)

		t.Log("Waiting for observer Deployment")
		tc.waitForDeploymentReady(t, ns, fmt.Sprintf("%s-observer", name))

		t.Run("observer deployment has correct labels", func(t *testing.T) {
			deploy, err := tc.kube.AppsV1().Deployments(ns).Get(
				context.Background(), fmt.Sprintf("%s-observer", name), metav1.GetOptions{})
			require.NoError(t, err)

			assert.Equal(t, "observer", deploy.Labels["app.kubernetes.io/component"])
			assert.Equal(t, name, deploy.Labels["app.kubernetes.io/instance"])
			assert.Equal(t, "vko.gtrfc.com", deploy.Labels["app.kubernetes.io/managed-by"])
		})

		t.Run("observer container runs with correct command", func(t *testing.T) {
			deploy, err := tc.kube.AppsV1().Deployments(ns).Get(
				context.Background(), fmt.Sprintf("%s-observer", name), metav1.GetOptions{})
			require.NoError(t, err)

			require.Len(t, deploy.Spec.Template.Spec.Containers, 1)
			c := deploy.Spec.Template.Spec.Containers[0]
			assert.Equal(t, "observer", c.Name)
			assert.Equal(t, []string{"/manager", "observer"}, c.Command)

			// Verify sentinel args are present.
			hasSentinelArg := false
			for _, arg := range c.Args {
				if arg == "--sentinel-enabled=true" {
					hasSentinelArg = true
					break
				}
			}
			assert.True(t, hasSentinelArg, "observer should have sentinel args in HA mode")
		})

		t.Run("observer has probes configured", func(t *testing.T) {
			deploy, err := tc.kube.AppsV1().Deployments(ns).Get(
				context.Background(), fmt.Sprintf("%s-observer", name), metav1.GetOptions{})
			require.NoError(t, err)

			c := deploy.Spec.Template.Spec.Containers[0]
			require.NotNil(t, c.ReadinessProbe, "observer should have readiness probe")
			assert.Equal(t, "/readyz", c.ReadinessProbe.HTTPGet.Path)
			require.NotNil(t, c.LivenessProbe, "observer should have liveness probe")
			assert.Equal(t, "/healthz", c.LivenessProbe.HTTPGet.Path)
		})

		t.Run("Valkey status shows phase OK", func(t *testing.T) {
			tc.waitForValkeyPhase(t, ns, name, "OK")
		})
	})

	t.Run("observer not created for standalone without observer spec", func(t *testing.T) {
		name := "obs-standalone"
		valkey := buildValkeyObject(name, ns, map[string]interface{}{
			"replicas": int64(1),
			"image":    "valkey/valkey:8.0",
		})

		tc.createValkey(t, ns, valkey)
		defer tc.deleteValkey(t, ns, name)

		tc.waitForStatefulSetReady(t, ns, name, 1)

		// Observer deployment should not be created.
		_, err := tc.kube.AppsV1().Deployments(ns).Get(
			context.Background(), fmt.Sprintf("%s-observer", name), metav1.GetOptions{})
		assert.True(t, apierrors.IsNotFound(err), "observer deployment should not exist for standalone without observer spec")
	})
}


