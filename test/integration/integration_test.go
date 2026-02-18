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

	t.Run("deploy HA cluster with sentinel", func(t *testing.T) {
		v := &vkov1.Valkey{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ha-test",
				Namespace: "default",
			},
			Spec: vkov1.ValkeySpec{
				Replicas: 3,
				Image:    "valkey/valkey:8.0",
				Sentinel: &vkov1.SentinelSpec{
					Enabled:  true,
					Replicas: 3,
					PodLabels: map[string]string{
						"app": "sentinel",
					},
				},
			},
		}

		require.NoError(t, k8sClient.Create(ctx, v))

		// Wait for Valkey StatefulSet.
		require.Eventually(t, func() bool {
			sts := &appsv1.StatefulSet{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "ha-test", Namespace: "default",
			}, sts)
			return err == nil
		}, 10*time.Second, 250*time.Millisecond, "Valkey StatefulSet should be created")

		// Wait for Sentinel StatefulSet.
		require.Eventually(t, func() bool {
			sts := &appsv1.StatefulSet{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "ha-test-sentinel", Namespace: "default",
			}, sts)
			return err == nil
		}, 10*time.Second, 250*time.Millisecond, "Sentinel StatefulSet should be created")

		// Verify Valkey StatefulSet.
		valkeySts := &appsv1.StatefulSet{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "ha-test", Namespace: "default",
		}, valkeySts))
		assert.Equal(t, int32(3), *valkeySts.Spec.Replicas)
		// HA mode should have init container for config selection.
		require.Len(t, valkeySts.Spec.Template.Spec.InitContainers, 1)
		assert.Equal(t, "init-config-selector", valkeySts.Spec.Template.Spec.InitContainers[0].Name)

		// Verify Sentinel StatefulSet.
		sentinelSts := &appsv1.StatefulSet{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "ha-test-sentinel", Namespace: "default",
		}, sentinelSts))
		assert.Equal(t, int32(3), *sentinelSts.Spec.Replicas)
		assert.Equal(t, "ha-test-sentinel-headless", sentinelSts.Spec.ServiceName)
		assert.Equal(t, "sentinel", sentinelSts.Spec.Template.Labels["app.kubernetes.io/component"])
		assert.Equal(t, "sentinel", sentinelSts.Spec.Template.Labels["app"])

		// Verify ConfigMaps.
		masterCm := &corev1.ConfigMap{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "ha-test-config", Namespace: "default",
		}, masterCm))
		assert.NotContains(t, masterCm.Data["valkey.conf"], "replicaof")
		assert.Contains(t, masterCm.Data["valkey.conf"], "replica-serve-stale-data yes")

		replicaCm := &corev1.ConfigMap{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "ha-test-replica-config", Namespace: "default",
		}, replicaCm))
		assert.Contains(t, replicaCm.Data["valkey.conf"], "replicaof")

		sentinelCm := &corev1.ConfigMap{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "ha-test-sentinel-config", Namespace: "default",
		}, sentinelCm))
		assert.Contains(t, sentinelCm.Data["sentinel.conf"], "sentinel monitor ha-test")

		// Verify Services.
		headlessSvc := &corev1.Service{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "ha-test-headless", Namespace: "default",
		}, headlessSvc))

		sentinelSvc := &corev1.Service{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "ha-test-sentinel-headless", Namespace: "default",
		}, sentinelSvc))
		assert.Equal(t, int32(26379), sentinelSvc.Spec.Ports[0].Port)

		// Verify owner references on all sentinel resources.
		assert.Len(t, sentinelSts.OwnerReferences, 1)
		assert.Equal(t, "ha-test", sentinelSts.OwnerReferences[0].Name)
		assert.Len(t, sentinelCm.OwnerReferences, 1)
		assert.Len(t, sentinelSvc.OwnerReferences, 1)

		// Verify status.
		require.Eventually(t, func() bool {
			updated := &vkov1.Valkey{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "ha-test", Namespace: "default",
			}, updated)
			return err == nil && updated.Status.Phase != ""
		}, 10*time.Second, 250*time.Millisecond, "Status phase should be set")

		// Cleanup.
		require.NoError(t, k8sClient.Delete(ctx, v, client.GracePeriodSeconds(0)))
	})

	t.Run("deploy standalone with auth", func(t *testing.T) {
		// Create the auth Secret first.
		authSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-auth-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"password": []byte("mysecretpassword"),
			},
		}
		require.NoError(t, k8sClient.Create(ctx, authSecret))

		v := &vkov1.Valkey{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "auth-test",
				Namespace: "default",
			},
			Spec: vkov1.ValkeySpec{
				Replicas: 1,
				Image:    "valkey/valkey:8.0",
				Auth: &vkov1.AuthSpec{
					SecretName:        "test-auth-secret",
					SecretPasswordKey: "password",
				},
			},
		}

		require.NoError(t, k8sClient.Create(ctx, v))

		// Wait for StatefulSet creation.
		require.Eventually(t, func() bool {
			sts := &appsv1.StatefulSet{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "auth-test", Namespace: "default",
			}, sts)
			return err == nil
		}, 10*time.Second, 250*time.Millisecond, "StatefulSet should be created")

		// Verify StatefulSet has auth env var.
		sts := &appsv1.StatefulSet{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "auth-test", Namespace: "default",
		}, sts))

		container := sts.Spec.Template.Spec.Containers[0]

		// Should have VALKEY_PASSWORD env var from the Secret.
		require.Len(t, container.Env, 1)
		assert.Equal(t, builder.AuthSecretEnvName, container.Env[0].Name)
		require.NotNil(t, container.Env[0].ValueFrom)
		require.NotNil(t, container.Env[0].ValueFrom.SecretKeyRef)
		assert.Equal(t, "test-auth-secret", container.Env[0].ValueFrom.SecretKeyRef.Name)
		assert.Equal(t, "password", container.Env[0].ValueFrom.SecretKeyRef.Key)

		// Command should include --requirepass and --masterauth flags.
		assert.Equal(t, "sh", container.Command[0])
		assert.Contains(t, container.Command[2], "--requirepass")
		assert.Contains(t, container.Command[2], "--masterauth")

		// Probes should use auth.
		require.NotNil(t, container.ReadinessProbe)
		require.NotNil(t, container.ReadinessProbe.Exec)
		assert.Equal(t, "sh", container.ReadinessProbe.Exec.Command[0])
		assert.Contains(t, container.ReadinessProbe.Exec.Command[2], "-a")

		// ConfigMap should NOT contain the password.
		cm := &corev1.ConfigMap{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "auth-test-config", Namespace: "default",
		}, cm))
		assert.NotContains(t, cm.Data["valkey.conf"], "mysecretpassword")
		assert.Contains(t, cm.Data["valkey.conf"], "# Auth")

		// Cleanup.
		require.NoError(t, k8sClient.Delete(ctx, v, client.GracePeriodSeconds(0)))
		require.NoError(t, k8sClient.Delete(ctx, authSecret))
	})

	t.Run("deploy HA cluster with auth", func(t *testing.T) {
		// Create the auth Secret first.
		authSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ha-auth-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"password": []byte("hapassword"),
			},
		}
		require.NoError(t, k8sClient.Create(ctx, authSecret))

		v := &vkov1.Valkey{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ha-auth-test",
				Namespace: "default",
			},
			Spec: vkov1.ValkeySpec{
				Replicas: 3,
				Image:    "valkey/valkey:8.0",
				Auth: &vkov1.AuthSpec{
					SecretName:        "ha-auth-secret",
					SecretPasswordKey: "password",
				},
				Sentinel: &vkov1.SentinelSpec{
					Enabled:  true,
					Replicas: 3,
				},
			},
		}

		require.NoError(t, k8sClient.Create(ctx, v))

		// Wait for Valkey StatefulSet.
		require.Eventually(t, func() bool {
			sts := &appsv1.StatefulSet{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "ha-auth-test", Namespace: "default",
			}, sts)
			return err == nil
		}, 10*time.Second, 250*time.Millisecond, "Valkey StatefulSet should be created")

		// Wait for Sentinel StatefulSet.
		require.Eventually(t, func() bool {
			sts := &appsv1.StatefulSet{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: "ha-auth-test-sentinel", Namespace: "default",
			}, sts)
			return err == nil
		}, 10*time.Second, 250*time.Millisecond, "Sentinel StatefulSet should be created")

		// Verify Valkey StatefulSet auth.
		valkeySts := &appsv1.StatefulSet{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "ha-auth-test", Namespace: "default",
		}, valkeySts))

		container := valkeySts.Spec.Template.Spec.Containers[0]
		require.Len(t, container.Env, 1)
		assert.Equal(t, builder.AuthSecretEnvName, container.Env[0].Name)
		assert.Contains(t, container.Command[2], "--requirepass")
		assert.Contains(t, container.Command[2], "--masterauth")

		// Verify Sentinel StatefulSet auth.
		sentinelSts := &appsv1.StatefulSet{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "ha-auth-test-sentinel", Namespace: "default",
		}, sentinelSts))

		// Init container should have the auth env var.
		require.Len(t, sentinelSts.Spec.Template.Spec.InitContainers, 1)
		initContainer := sentinelSts.Spec.Template.Spec.InitContainers[0]
		require.Len(t, initContainer.Env, 1)
		assert.Equal(t, builder.AuthSecretEnvName, initContainer.Env[0].Name)
		// Init container should use sed to replace the placeholder.
		assert.Contains(t, initContainer.Command[2], "sed")

		// Verify Sentinel ConfigMap has auth placeholder.
		sentinelCm := &corev1.ConfigMap{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "ha-auth-test-sentinel-config", Namespace: "default",
		}, sentinelCm))
		assert.Contains(t, sentinelCm.Data["sentinel.conf"], "sentinel auth-pass ha-auth-test %VALKEY_PASSWORD%")
		assert.NotContains(t, sentinelCm.Data["sentinel.conf"], "hapassword")

		// Cleanup.
		require.NoError(t, k8sClient.Delete(ctx, v, client.GracePeriodSeconds(0)))
		require.NoError(t, k8sClient.Delete(ctx, authSecret))
	})
}
