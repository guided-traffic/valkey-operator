//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	klabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/controller"
)

// TestSidecarServicesRouting_Integration verifies that the operator creates
// the correct role-aware services with proper selectors for sidecar-based routing,
// legacy service cleanup, sidecar container injection, RBAC, and standalone mode.
func TestSidecarServicesRouting_Integration(t *testing.T) {
	log.SetLogger(zap.New(zap.UseDevMode(true)))

	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{"../../config/crd/bases"},
	}

	cfg, err := testEnv.Start()
	require.NoError(t, err, "failed to start envtest")
	defer func() {
		require.NoError(t, testEnv.Stop())
	}()

	require.NoError(t, vkov1.AddToScheme(scheme.Scheme))
	require.NoError(t, appsv1.AddToScheme(scheme.Scheme))
	require.NoError(t, rbacv1.AddToScheme(scheme.Scheme))

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	require.NoError(t, err)

	reconciler := &controller.ValkeyReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}
	require.NoError(t, reconciler.SetupWithManager(mgr))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		require.NoError(t, mgr.Start(ctx))
	}()

	require.True(t, mgr.GetCache().WaitForCacheSync(ctx))
	k8sClient := mgr.GetClient()

	// Helper: wait for a service to appear.
	waitForServiceCreation := func(t *testing.T, name string) {
		t.Helper()
		require.Eventually(t, func() bool {
			svc := &corev1.Service{}
			return k8sClient.Get(ctx, types.NamespacedName{
				Name: name, Namespace: "default",
			}, svc) == nil
		}, 10*time.Second, 250*time.Millisecond, "Service %s should be created", name)
	}

	// --- Create the HA Valkey used by multiple sub-tests ---
	haValkey := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sc-ha-svc",
			Namespace: "default",
		},
		Spec: vkov1.ValkeySpec{
			Replicas: 3,
			Image:    "valkey/valkey:8.0",
			Sentinel: &vkov1.SentinelSpec{
				Enabled:  true,
				Replicas: 3,
			},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, haValkey))
	defer func() {
		_ = k8sClient.Delete(ctx, haValkey, client.GracePeriodSeconds(0))
	}()

	// Wait for all four services to exist.
	waitForServiceCreation(t, "sc-ha-svc-rw")
	waitForServiceCreation(t, "sc-ha-svc-r")
	waitForServiceCreation(t, "sc-ha-svc-all")
	waitForServiceCreation(t, "sc-ha-svc-headless")

	// ========================================================================
	// Service selector tests
	// ========================================================================

	t.Run("HA rw service selects master pods only", func(t *testing.T) {
		rwSvc := &corev1.Service{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "sc-ha-svc-rw", Namespace: "default",
		}, rwSvc))
		assert.Equal(t, common.RoleMaster, rwSvc.Spec.Selector[common.LabelInstanceRole])
		assert.Equal(t, common.ManagedBy, rwSvc.Spec.Selector[common.LabelManagedBy])
		assert.Equal(t, "sc-ha-svc", rwSvc.Spec.Selector[common.LabelInstance])
	})

	t.Run("HA readonly service selects replica pods only", func(t *testing.T) {
		rSvc := &corev1.Service{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "sc-ha-svc-r", Namespace: "default",
		}, rSvc))
		assert.Equal(t, common.RoleReplica, rSvc.Spec.Selector[common.LabelInstanceRole])
	})

	t.Run("HA all service has no role filter", func(t *testing.T) {
		allSvc := &corev1.Service{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "sc-ha-svc-all", Namespace: "default",
		}, allSvc))
		_, hasRole := allSvc.Spec.Selector[common.LabelInstanceRole]
		assert.False(t, hasRole, "-all service must not filter by role")
	})

	t.Run("headless service has no role filter", func(t *testing.T) {
		svc := &corev1.Service{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "sc-ha-svc-headless", Namespace: "default",
		}, svc))
		assert.Equal(t, corev1.ClusterIPNone, svc.Spec.ClusterIP)
		_, hasRole := svc.Spec.Selector[common.LabelInstanceRole]
		assert.False(t, hasRole, "headless service must not filter by role")
	})

	// ========================================================================
	// Programmatic selector matching
	// ========================================================================

	t.Run("RW selector matches only master-labeled pods", func(t *testing.T) {
		rwSvc := &corev1.Service{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "sc-ha-svc-rw", Namespace: "default",
		}, rwSvc))
		selector := klabels.SelectorFromSet(rwSvc.Spec.Selector)

		cases := []struct {
			name  string
			role  string
			match bool
		}{
			{"master pod", common.RoleMaster, true},
			{"replica pod", common.RoleReplica, false},
			{"draining pod", common.RoleDraining, false},
			{"pod without role", "", false},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				labels := map[string]string{
					common.LabelInstance:  "sc-ha-svc",
					common.LabelManagedBy: common.ManagedBy,
					common.LabelComponent: common.ComponentValkey,
				}
				if tc.role != "" {
					labels[common.LabelInstanceRole] = tc.role
				}
				assert.Equal(t, tc.match, selector.Matches(klabels.Set(labels)),
					"selector match for %s", tc.name)
			})
		}
	})

	t.Run("ReadOnly selector matches only replica-labeled pods", func(t *testing.T) {
		rSvc := &corev1.Service{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "sc-ha-svc-r", Namespace: "default",
		}, rSvc))
		selector := klabels.SelectorFromSet(rSvc.Spec.Selector)

		cases := []struct {
			name  string
			role  string
			match bool
		}{
			{"master pod", common.RoleMaster, false},
			{"replica pod", common.RoleReplica, true},
			{"draining pod", common.RoleDraining, false},
			{"pod without role", "", false},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				labels := map[string]string{
					common.LabelInstance:  "sc-ha-svc",
					common.LabelManagedBy: common.ManagedBy,
					common.LabelComponent: common.ComponentValkey,
				}
				if tc.role != "" {
					labels[common.LabelInstanceRole] = tc.role
				}
				assert.Equal(t, tc.match, selector.Matches(klabels.Set(labels)),
					"selector match for %s", tc.name)
			})
		}
	})

	t.Run("All selector matches pods regardless of role", func(t *testing.T) {
		allSvc := &corev1.Service{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "sc-ha-svc-all", Namespace: "default",
		}, allSvc))
		selector := klabels.SelectorFromSet(allSvc.Spec.Selector)

		for _, role := range []string{common.RoleMaster, common.RoleReplica, common.RoleDraining, ""} {
			labels := map[string]string{
				common.LabelInstance:  "sc-ha-svc",
				common.LabelManagedBy: common.ManagedBy,
				common.LabelComponent: common.ComponentValkey,
			}
			if role != "" {
				labels[common.LabelInstanceRole] = role
			}
			assert.True(t, selector.Matches(klabels.Set(labels)),
				"-all selector should match pods with role=%q", role)
		}
	})

	// ========================================================================
	// StatefulSet verification
	// ========================================================================

	t.Run("StatefulSet has sidecar container with correct probes", func(t *testing.T) {
		sts := &appsv1.StatefulSet{}
		require.Eventually(t, func() bool {
			return k8sClient.Get(ctx, types.NamespacedName{
				Name: "sc-ha-svc", Namespace: "default",
			}, sts) == nil
		}, 10*time.Second, 250*time.Millisecond)

		var sidecar *corev1.Container
		for i := range sts.Spec.Template.Spec.Containers {
			if sts.Spec.Template.Spec.Containers[i].Name == builder.SidecarContainerName {
				sidecar = &sts.Spec.Template.Spec.Containers[i]
				break
			}
		}
		require.NotNil(t, sidecar, "Sidecar container must be present in pod spec")

		// Verify env vars.
		envNames := make(map[string]bool)
		for _, env := range sidecar.Env {
			envNames[env.Name] = true
		}
		assert.True(t, envNames["POD_NAME"], "Sidecar should have POD_NAME env var")
		assert.True(t, envNames["POD_NAMESPACE"], "Sidecar should have POD_NAMESPACE env var")

		// Verify readiness probe.
		require.NotNil(t, sidecar.ReadinessProbe)
		require.NotNil(t, sidecar.ReadinessProbe.HTTPGet)
		assert.Equal(t, "/readyz", sidecar.ReadinessProbe.HTTPGet.Path)
		assert.Equal(t, int32(builder.SidecarHealthPort), sidecar.ReadinessProbe.HTTPGet.Port.IntVal)

		// Verify liveness probe.
		require.NotNil(t, sidecar.LivenessProbe)
		require.NotNil(t, sidecar.LivenessProbe.HTTPGet)
		assert.Equal(t, "/healthz", sidecar.LivenessProbe.HTTPGet.Path)
	})

	t.Run("StatefulSet terminationGracePeriodSeconds is 75", func(t *testing.T) {
		sts := &appsv1.StatefulSet{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "sc-ha-svc", Namespace: "default",
		}, sts))
		require.NotNil(t, sts.Spec.Template.Spec.TerminationGracePeriodSeconds)
		assert.Equal(t, int64(75), *sts.Spec.Template.Spec.TerminationGracePeriodSeconds,
			"terminationGracePeriodSeconds should be 75 for graceful failover")
	})

	t.Run("sidecar args include sentinel configuration for HA", func(t *testing.T) {
		sts := &appsv1.StatefulSet{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "sc-ha-svc", Namespace: "default",
		}, sts))

		var sidecar *corev1.Container
		for i := range sts.Spec.Template.Spec.Containers {
			if sts.Spec.Template.Spec.Containers[i].Name == builder.SidecarContainerName {
				sidecar = &sts.Spec.Template.Spec.Containers[i]
				break
			}
		}
		require.NotNil(t, sidecar)

		argsStr := fmt.Sprintf("%v", sidecar.Args)
		assert.Contains(t, argsStr, "--sentinel-enabled=true")
		assert.Contains(t, argsStr, "--sentinel-monitor=")
		assert.Contains(t, argsStr, "--headless-svc=")
		assert.Contains(t, argsStr, "--replicas=3")
	})

	t.Run("pod serviceAccountName is sidecar SA", func(t *testing.T) {
		sts := &appsv1.StatefulSet{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "sc-ha-svc", Namespace: "default",
		}, sts))
		assert.Equal(t, "sc-ha-svc-sidecar", sts.Spec.Template.Spec.ServiceAccountName)
	})

	// ========================================================================
	// Sidecar RBAC
	// ========================================================================

	t.Run("sidecar RBAC resources are created with correct configuration", func(t *testing.T) {
		// ServiceAccount.
		sa := &corev1.ServiceAccount{}
		require.Eventually(t, func() bool {
			return k8sClient.Get(ctx, types.NamespacedName{
				Name: "sc-ha-svc-sidecar", Namespace: "default",
			}, sa) == nil
		}, 10*time.Second, 250*time.Millisecond, "Sidecar ServiceAccount should exist")

		// Role.
		role := &rbacv1.Role{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "sc-ha-svc-sidecar", Namespace: "default",
		}, role))
		require.Len(t, role.Rules, 1)
		assert.Contains(t, role.Rules[0].Verbs, "patch")
		assert.Contains(t, role.Rules[0].Verbs, "get")
		assert.Contains(t, role.Rules[0].Verbs, "list")
		assert.Contains(t, role.Rules[0].Resources, "pods")

		// RoleBinding.
		rb := &rbacv1.RoleBinding{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "sc-ha-svc-sidecar", Namespace: "default",
		}, rb))
		assert.Equal(t, "sc-ha-svc-sidecar", rb.RoleRef.Name)
		assert.Equal(t, "Role", rb.RoleRef.Kind)
		require.Len(t, rb.Subjects, 1)
		assert.Equal(t, "ServiceAccount", rb.Subjects[0].Kind)
		assert.Equal(t, "sc-ha-svc-sidecar", rb.Subjects[0].Name)

		// Verify owner references for garbage collection.
		assert.Len(t, sa.OwnerReferences, 1,
			"ServiceAccount should have owner reference to Valkey CR")
		assert.Len(t, role.OwnerReferences, 1,
			"Role should have owner reference to Valkey CR")
		assert.Len(t, rb.OwnerReferences, 1,
			"RoleBinding should have owner reference to Valkey CR")
	})

	// ========================================================================
	// Legacy service cleanup
	// ========================================================================

	t.Run("legacy services are cleaned up on reconcile", func(t *testing.T) {
		name := "sc-legacy-test"
		v := &vkov1.Valkey{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
			},
			Spec: vkov1.ValkeySpec{
				Replicas: 3,
				Image:    "valkey/valkey:8.0",
				Sentinel: &vkov1.SentinelSpec{
					Enabled:  true,
					Replicas: 3,
				},
			},
		}
		require.NoError(t, k8sClient.Create(ctx, v))
		defer func() {
			_ = k8sClient.Delete(ctx, v, client.GracePeriodSeconds(0))
		}()

		// Wait for initial reconcile.
		waitForServiceCreation(t, name+"-rw")

		// Get the Valkey CR UID for owner references.
		updatedV := &vkov1.Valkey{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: name, Namespace: "default",
		}, updatedV))

		isController := true
		blockOwnerDeletion := true
		ownerRef := metav1.OwnerReference{
			APIVersion:         "vko.gtrfc.com/v1",
			Kind:               "Valkey",
			Name:               name,
			UID:                updatedV.UID,
			Controller:         &isController,
			BlockOwnerDeletion: &blockOwnerDeletion,
		}

		// Create legacy client service (<name>).
		legacySvc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:            name,
				Namespace:       "default",
				OwnerReferences: []metav1.OwnerReference{ownerRef},
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{{
					Name:     "valkey",
					Port:     6379,
					Protocol: corev1.ProtocolTCP,
				}},
			},
		}
		require.NoError(t, k8sClient.Create(ctx, legacySvc))

		// Create legacy read service (<name>-read).
		legacyReadSvc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:            name + "-read",
				Namespace:       "default",
				OwnerReferences: []metav1.OwnerReference{ownerRef},
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{{
					Name:     "valkey",
					Port:     6379,
					Protocol: corev1.ProtocolTCP,
				}},
			},
		}
		require.NoError(t, k8sClient.Create(ctx, legacyReadSvc))

		// The controller's Owns() watch detects the new owned services and triggers reconcile.
		// During reconcile, deleteLegacyServices deletes them.
		require.Eventually(t, func() bool {
			svc := &corev1.Service{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: name, Namespace: "default",
			}, svc)
			return apierrors.IsNotFound(err)
		}, 10*time.Second, 250*time.Millisecond, "Legacy client service should be deleted")

		require.Eventually(t, func() bool {
			svc := &corev1.Service{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: name + "-read", Namespace: "default",
			}, svc)
			return apierrors.IsNotFound(err)
		}, 10*time.Second, 250*time.Millisecond, "Legacy read service should be deleted")

		// New services should survive legacy cleanup.
		svc := &corev1.Service{}
		assert.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: name + "-rw", Namespace: "default",
		}, svc), "-rw service should survive legacy cleanup")
		assert.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: name + "-r", Namespace: "default",
		}, svc), "-r service should survive legacy cleanup")
		assert.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: name + "-all", Namespace: "default",
		}, svc), "-all service should survive legacy cleanup")
	})

	// ========================================================================
	// Standalone mode
	// ========================================================================

	t.Run("standalone creates only rw and headless services", func(t *testing.T) {
		v := &vkov1.Valkey{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "sc-standalone",
				Namespace: "default",
			},
			Spec: vkov1.ValkeySpec{
				Replicas: 1,
				Image:    "valkey/valkey:8.0",
			},
		}
		require.NoError(t, k8sClient.Create(ctx, v))
		defer func() {
			_ = k8sClient.Delete(ctx, v, client.GracePeriodSeconds(0))
		}()

		// -rw and -headless should be created.
		waitForServiceCreation(t, "sc-standalone-rw")
		waitForServiceCreation(t, "sc-standalone-headless")

		// Give controller time to potentially create extra services.
		time.Sleep(2 * time.Second)

		// -all and -r must NOT exist for standalone.
		svc := &corev1.Service{}
		err := k8sClient.Get(ctx, types.NamespacedName{
			Name: "sc-standalone-all", Namespace: "default",
		}, svc)
		assert.True(t, apierrors.IsNotFound(err),
			"-all service must not exist for standalone (replicas=1)")

		err = k8sClient.Get(ctx, types.NamespacedName{
			Name: "sc-standalone-r", Namespace: "default",
		}, svc)
		assert.True(t, apierrors.IsNotFound(err),
			"-r service must not exist for standalone (replicas=1)")

		// -rw should select master.
		rwSvc := &corev1.Service{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "sc-standalone-rw", Namespace: "default",
		}, rwSvc))
		assert.Equal(t, common.RoleMaster, rwSvc.Spec.Selector[common.LabelInstanceRole])
	})

	t.Run("standalone sidecar does not have sentinel flags", func(t *testing.T) {
		sts := &appsv1.StatefulSet{}
		require.Eventually(t, func() bool {
			return k8sClient.Get(ctx, types.NamespacedName{
				Name: "sc-standalone", Namespace: "default",
			}, sts) == nil
		}, 10*time.Second, 250*time.Millisecond)

		var sidecar *corev1.Container
		for i := range sts.Spec.Template.Spec.Containers {
			if sts.Spec.Template.Spec.Containers[i].Name == builder.SidecarContainerName {
				sidecar = &sts.Spec.Template.Spec.Containers[i]
				break
			}
		}
		require.NotNil(t, sidecar)

		argsStr := fmt.Sprintf("%v", sidecar.Args)
		assert.NotContains(t, argsStr, "--sentinel-enabled=true",
			"Standalone sidecar should not have sentinel-enabled flag")
		assert.Contains(t, argsStr, "--replicas=1",
			"Standalone sidecar should have replicas=1")
	})

	t.Run("standalone StatefulSet also has 75s grace period", func(t *testing.T) {
		sts := &appsv1.StatefulSet{}
		require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{
			Name: "sc-standalone", Namespace: "default",
		}, sts))
		require.NotNil(t, sts.Spec.Template.Spec.TerminationGracePeriodSeconds)
		assert.Equal(t, int64(75), *sts.Spec.Template.Spec.TerminationGracePeriodSeconds)
	})
}
