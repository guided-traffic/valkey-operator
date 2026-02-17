//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/controller"
)

func TestStandaloneMode_Integration(t *testing.T) {
	log.SetLogger(zap.New(zap.UseDevMode(true)))

	// Start envtest.
	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{"../../config/crd/bases"},
	}

	cfg, err := testEnv.Start()
	require.NoError(t, err, "failed to start envtest")
	defer func() {
		require.NoError(t, testEnv.Stop())
	}()

	// Register schemes.
	require.NoError(t, vkov1.AddToScheme(scheme.Scheme))
	require.NoError(t, appsv1.AddToScheme(scheme.Scheme))

	// Set up manager and controller.
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	require.NoError(t, err)

	reconciler := &controller.ValkeyReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	require.NoError(t, reconciler.SetupWithManager(mgr))

	// Start manager in background.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		require.NoError(t, mgr.Start(ctx))
	}()

	// Wait for cache to sync.
	require.True(t, mgr.GetCache().WaitForCacheSync(ctx))

	k8sClient := mgr.GetClient()

	t.Run("deploy standalone valkey", func(t *testing.T) {
		v := &vkov1.Valkey{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "standalone-test",
				Namespace: "default",
			},
			Spec: vkov1.ValkeySpec{
				Replicas: 1,
				Image:    "valkey/valkey:8.0",
			},
		}

		// Create the Valkey resource.
		require.NoError(t, k8sClient.Create(ctx, v))

		// Wait for reconciliation to create resources.
		require.Eventually(t, func() bool {
			cm := &corev1.ConfigMap{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: builder.ConfigMapName(v), Namespace: "default",
			}, cm)
			return err == nil
		}, 10*time.Second, 250*time.Millisecond, "ConfigMap should be created")

		require.Eventually(t, func() bool {
			svc := &corev1.Service{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "standalone-test-headless", Namespace: "default",
			}, svc)
			return err == nil
		}, 10*time.Second, 250*time.Millisecond, "Headless Service should be created")

		require.Eventually(t, func() bool {
			svc := &corev1.Service{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "standalone-test", Namespace: "default",
			}, svc)
			return err == nil
		}, 10*time.Second, 250*time.Millisecond, "Client Service should be created")

		require.Eventually(t, func() bool {
			sts := &appsv1.StatefulSet{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "standalone-test", Namespace: "default",
			}, sts)
			return err == nil
		}, 10*time.Second, 250*time.Millisecond, "StatefulSet should be created")

		// Verify StatefulSet configuration.
		sts := &appsv1.StatefulSet{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "standalone-test", Namespace: "default",
		}, sts))

		assert.Equal(t, int32(1), *sts.Spec.Replicas)
		assert.Equal(t, "valkey/valkey:8.0", sts.Spec.Template.Spec.Containers[0].Image)

		// Verify ConfigMap content.
		cm := &corev1.ConfigMap{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "standalone-test-config", Namespace: "default",
		}, cm))
		assert.Contains(t, cm.Data["valkey.conf"], "bind 0.0.0.0")
		assert.Contains(t, cm.Data["valkey.conf"], "port 6379")

		// Verify owner references (garbage collection).
		assert.Len(t, sts.OwnerReferences, 1)
		assert.Equal(t, "standalone-test", sts.OwnerReferences[0].Name)

		// Verify status is set.
		require.Eventually(t, func() bool {
			updated := &vkov1.Valkey{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "standalone-test", Namespace: "default",
			}, updated)
			return err == nil && updated.Status.Phase != ""
		}, 10*time.Second, 250*time.Millisecond, "Status phase should be set")
	})

	t.Run("delete standalone valkey", func(t *testing.T) {
		v := &vkov1.Valkey{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "standalone-test",
				Namespace: "default",
			},
		}
		require.NoError(t, k8sClient.Delete(ctx, v))

		// The owner reference mechanism in envtest should cascade delete.
		require.Eventually(t, func() bool {
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "standalone-test", Namespace: "default",
			}, &vkov1.Valkey{})
			return err != nil
		}, 10*time.Second, 250*time.Millisecond, "Valkey resource should be deleted")
	})

	t.Run("deploy standalone with persistence", func(t *testing.T) {
		v := &vkov1.Valkey{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "persistent-test",
				Namespace: "default",
			},
			Spec: vkov1.ValkeySpec{
				Replicas: 1,
				Image:    "valkey/valkey:8.0",
				Persistence: &vkov1.PersistenceSpec{
					Enabled: true,
					Mode:    vkov1.PersistenceModeRDB,
				},
			},
		}

		require.NoError(t, k8sClient.Create(ctx, v))

		// Wait for StatefulSet creation.
		require.Eventually(t, func() bool {
			sts := &appsv1.StatefulSet{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "persistent-test", Namespace: "default",
			}, sts)
			return err == nil
		}, 10*time.Second, 250*time.Millisecond, "StatefulSet should be created")

		// Verify PVC template.
		sts := &appsv1.StatefulSet{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "persistent-test", Namespace: "default",
		}, sts))

		require.Len(t, sts.Spec.VolumeClaimTemplates, 1, "should have PVC template")
		assert.Equal(t, "data", sts.Spec.VolumeClaimTemplates[0].Name)

		// ConfigMap should contain RDB save directives.
		cm := &corev1.ConfigMap{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "persistent-test-config", Namespace: "default",
		}, cm))
		assert.Contains(t, cm.Data["valkey.conf"], "save 900 1")

		// Cleanup.
		require.NoError(t, k8sClient.Delete(ctx, v, client.GracePeriodSeconds(0)))
	})
}
