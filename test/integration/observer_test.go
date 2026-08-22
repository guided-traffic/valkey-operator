//go:build integration

package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

func TestObserver_Integration(t *testing.T) {
	ctx := testCtx

	t.Run("observer deployment created when enabled", func(t *testing.T) {
		v := &vkov1.Valkey{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "obs-test",
				Namespace: "default",
			},
			Spec: vkov1.ValkeySpec{
				Replicas: 1,
				Image:    "valkey/valkey:8.0",
				Observer: &vkov1.ObserverSpec{Enabled: true},
			},
		}

		require.NoError(t, k8sClient.Create(ctx, v))

		// Wait for observer Deployment to be created.
		require.Eventually(t, func() bool {
			deploy := &appsv1.Deployment{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: builder.ObserverDeploymentName(v), Namespace: "default",
			}, deploy)
			return err == nil
		}, 10*time.Second, 250*time.Millisecond, "Observer Deployment should be created")

		// Verify observer Deployment configuration.
		deploy := &appsv1.Deployment{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "obs-test-observer", Namespace: "default",
		}, deploy))

		assert.Equal(t, int32(1), *deploy.Spec.Replicas)
		require.Len(t, deploy.Spec.Template.Spec.Containers, 1)
		assert.Equal(t, "observer", deploy.Spec.Template.Spec.Containers[0].Name)

		// Verify Labels.
		assert.Equal(t, builder.ComponentObserver, deploy.Labels["app.kubernetes.io/component"])
		assert.Equal(t, "obs-test", deploy.Labels["app.kubernetes.io/instance"])

		// Verify selector labels.
		assert.Equal(t, builder.ComponentObserver, deploy.Spec.Selector.MatchLabels["app.kubernetes.io/component"])

		// Verify owner reference (garbage collection).
		require.Len(t, deploy.OwnerReferences, 1)
		assert.Equal(t, "obs-test", deploy.OwnerReferences[0].Name)

		// The observer runs under its own ServiceAccount and mounts no token
		// (ADR 0012 D8 step 2). Sharing the sidecar ServiceAccount gave a process
		// that makes no Kubernetes API call a mounted token for pods patch.
		assert.Equal(t, "obs-test-observer", deploy.Spec.Template.Spec.ServiceAccountName)
		require.NotNil(t, deploy.Spec.Template.Spec.AutomountServiceAccountToken)
		assert.False(t, *deploy.Spec.Template.Spec.AutomountServiceAccountToken)

		sa := &corev1.ServiceAccount{}
		require.Eventually(t, func() bool {
			return k8sClient.Get(ctx, types.NamespacedName{
				Name: "obs-test-observer", Namespace: "default",
			}, sa) == nil
		}, 10*time.Second, 250*time.Millisecond, "Observer ServiceAccount should be created")
		require.Len(t, sa.OwnerReferences, 1)
		assert.Equal(t, "obs-test", sa.OwnerReferences[0].Name)

		// Role-less by construction: nothing in the namespace may bind a Role to it.
		bindings := &rbacv1.RoleBindingList{}
		require.NoError(t, k8sClient.List(ctx, bindings, client.InNamespace("default")))
		for i := range bindings.Items {
			for _, subject := range bindings.Items[i].Subjects {
				assert.False(t, subject.Kind == "ServiceAccount" && subject.Name == "obs-test-observer",
					"the observer ServiceAccount must be bound to no Role at all")
			}
		}

		// Cleanup.
		require.NoError(t, k8sClient.Delete(ctx, v, client.GracePeriodSeconds(0)))
	})

	t.Run("observer deployment not created when disabled", func(t *testing.T) {
		v := &vkov1.Valkey{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "obs-disabled-test",
				Namespace: "default",
			},
			Spec: vkov1.ValkeySpec{
				Replicas: 1,
				Image:    "valkey/valkey:8.0",
			},
		}

		require.NoError(t, k8sClient.Create(ctx, v))

		// Wait for StatefulSet to be created (ensures reconcile ran).
		require.Eventually(t, func() bool {
			sts := &appsv1.StatefulSet{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "obs-disabled-test", Namespace: "default",
			}, sts)
			return err == nil
		}, 10*time.Second, 250*time.Millisecond, "StatefulSet should be created first")

		// Observer Deployment should not exist.
		deploy := &appsv1.Deployment{}
		err := k8sClient.Get(ctx, types.NamespacedName{
			Name: "obs-disabled-test-observer", Namespace: "default",
		}, deploy)
		assert.Error(t, err, "Observer Deployment should not exist when observer is disabled")

		// Cleanup.
		require.NoError(t, k8sClient.Delete(ctx, v, client.GracePeriodSeconds(0)))
	})

	t.Run("observer deployment cleaned up on disable", func(t *testing.T) {
		v := &vkov1.Valkey{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "obs-cleanup-test",
				Namespace: "default",
			},
			Spec: vkov1.ValkeySpec{
				Replicas: 1,
				Image:    "valkey/valkey:8.0",
				Observer: &vkov1.ObserverSpec{Enabled: true},
			},
		}

		require.NoError(t, k8sClient.Create(ctx, v))

		// Wait for observer Deployment.
		require.Eventually(t, func() bool {
			deploy := &appsv1.Deployment{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "obs-cleanup-test-observer", Namespace: "default",
			}, deploy)
			return err == nil
		}, 10*time.Second, 250*time.Millisecond, "Observer Deployment should be created")

		// Disable observer.
		updated := &vkov1.Valkey{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "obs-cleanup-test", Namespace: "default",
		}, updated))
		updated.Spec.Observer.Enabled = false
		require.NoError(t, k8sClient.Update(ctx, updated))

		// Wait for deployment to be deleted.
		require.Eventually(t, func() bool {
			deploy := &appsv1.Deployment{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "obs-cleanup-test-observer", Namespace: "default",
			}, deploy)
			return err != nil
		}, 10*time.Second, 250*time.Millisecond, "Observer Deployment should be deleted after disabling")

		// The ServiceAccount goes with it: the operator owns it, so the ownership
		// guard in cleanupObserverServiceAccount lets this delete through.
		require.Eventually(t, func() bool {
			sa := &corev1.ServiceAccount{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "obs-cleanup-test-observer", Namespace: "default",
			}, sa)
			return err != nil
		}, 10*time.Second, 250*time.Millisecond, "Observer ServiceAccount should be deleted after disabling")

		// Cleanup.
		require.NoError(t, k8sClient.Delete(ctx, v, client.GracePeriodSeconds(0)))
	})

	t.Run("observer deployment with HA sentinel", func(t *testing.T) {
		v := &vkov1.Valkey{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "obs-ha-test",
				Namespace: "default",
			},
			Spec: vkov1.ValkeySpec{
				Replicas: 3,
				Image:    "valkey/valkey:8.0",
				Sentinel: &vkov1.SentinelSpec{Enabled: true, Replicas: 3},
				Observer: &vkov1.ObserverSpec{Enabled: true},
			},
		}

		require.NoError(t, k8sClient.Create(ctx, v))

		// Wait for observer Deployment.
		require.Eventually(t, func() bool {
			deploy := &appsv1.Deployment{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "obs-ha-test-observer", Namespace: "default",
			}, deploy)
			return err == nil
		}, 10*time.Second, 250*time.Millisecond, "Observer Deployment should be created in HA mode")

		deploy := &appsv1.Deployment{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "obs-ha-test-observer", Namespace: "default",
		}, deploy))

		// Verify sentinel args are present.
		args := deploy.Spec.Template.Spec.Containers[0].Args
		hasSentinelArg := false
		for _, arg := range args {
			if arg == "--sentinel-enabled=true" {
				hasSentinelArg = true
				break
			}
		}
		assert.True(t, hasSentinelArg, "observer should have --sentinel-enabled=true arg in HA mode")

		// Cleanup.
		require.NoError(t, k8sClient.Delete(ctx, v, client.GracePeriodSeconds(0)))
	})
}
