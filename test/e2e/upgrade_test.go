//go:build e2e

package e2e

// Tests in this file verify operator upgrade behaviour: resource annotation
// tracking (Phase 4), RBAC drift recovery (Phase 3), and sentinel quorum
// preservation during a rolling update (Phase 2).
//
// These tests simulate an upgrade by inspecting managed resources and
// triggering drift — no second operator binary or real Helm upgrade is
// required. They run in isolated namespaces and are fully parallel with
// all other E2E tests.

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/guided-traffic/valkey-operator/test/testimages"
)

// annotationOperatorVersion is the annotation set by the operator on every
// managed resource after Phase 4. Defined here so the test file does not
// import the internal builder package.
const annotationOperatorVersion = "vko.gtrfc.com/operator-version"

// TestE2E_Upgrade_OperatorVersionAnnotation verifies that the operator sets the
// vko.gtrfc.com/operator-version annotation on all managed resources when a
// cluster is deployed, and that it re-applies the annotation whenever it is
// removed (simulating resources that were last touched by an older operator).
func TestE2E_Upgrade_OperatorVersionAnnotation(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-upg-ann"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "ann-test"
	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(1),
		"image":    testimages.Default(),
	})

	t.Log("Creating standalone Valkey CR")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 1)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	t.Run("StatefulSet has operator-version annotation", func(t *testing.T) {
		sts := tc.getStatefulSet(t, ns, name)
		ann := sts.Annotations[annotationOperatorVersion]
		assert.NotEmpty(t, ann,
			"StatefulSet should carry the %s annotation", annotationOperatorVersion)
		t.Logf("StatefulSet annotation %s=%s", annotationOperatorVersion, ann)
	})

	t.Run("Headless service has operator-version annotation", func(t *testing.T) {
		svc := tc.getService(t, ns, fmt.Sprintf("%s-headless", name))
		assert.NotEmpty(t, svc.Annotations[annotationOperatorVersion],
			"Headless service should carry the operator-version annotation")
	})

	t.Run("ConfigMap has operator-version annotation", func(t *testing.T) {
		cm := tc.getConfigMap(t, ns, fmt.Sprintf("%s-config", name))
		assert.NotEmpty(t, cm.Annotations[annotationOperatorVersion],
			"ConfigMap should carry the operator-version annotation")
	})

	t.Run("status.operatorVersion is set", func(t *testing.T) {
		// Use Eventually instead of an instant assertion: the operator writes
		// phase and operatorVersion in the same status update, but under load
		// a concurrent reconcile can briefly overwrite the status with a phase
		// update that happens to read the object before the OperatorVersion write
		// was flushed. Polling here makes the test robust against this race.
		ctx := context.Background()
		var operatorVersion string
		require.Eventually(t, func() bool {
			v, err := tc.dynamic.Resource(valkeyGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return false
			}
			ov, _, _ := unstructuredNestedString(v.Object, "status", "operatorVersion")
			operatorVersion = ov
			return ov != ""
		}, testTimeout, pollInterval, "status.operatorVersion should be set after deployment")
		t.Logf("status.operatorVersion=%s", operatorVersion)
	})

	t.Run("Operator re-applies removed annotation", func(t *testing.T) {
		ctx := context.Background()
		svcName := fmt.Sprintf("%s-headless", name)

		// Remove the operator-version annotation from the headless service,
		// simulating a resource that was last reconciled by an older operator
		// version which did not set the annotation yet.
		patch := []byte(`[{"op":"remove","path":"/metadata/annotations/vko.gtrfc.com~1operator-version"}]`)
		_, err := tc.kube.CoreV1().Services(ns).Patch(
			ctx, svcName, types.JSONPatchType, patch, metav1.PatchOptions{})
		require.NoError(t, err, "Failed to remove annotation from service %s", svcName)

		// Trigger a reconcile by patching the Valkey CR with a harmless annotation.
		tc.triggerReconcile(t, ns, name)

		// The operator should detect the missing annotation via OperatorVersionChanged()
		// and update the service within one reconcile loop.
		require.Eventually(t, func() bool {
			svc, err := tc.kube.CoreV1().Services(ns).Get(ctx, svcName, metav1.GetOptions{})
			if err != nil {
				return false
			}
			return svc.Annotations[annotationOperatorVersion] != ""
		}, testTimeout, pollInterval,
			"Operator should re-apply %s annotation on service %s", annotationOperatorVersion, svcName)

		t.Logf("Operator successfully re-applied annotation on %s", svcName)
	})
}

// TestE2E_Upgrade_RBACDrift verifies that the operator detects and repairs RBAC
// drift: when the sidecar Role is deleted, the operator recreates it on the next
// reconcile loop. This covers the Phase 3 RBAC reconciliation fix.
func TestE2E_Upgrade_RBACDrift(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-upg-rbac"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "rbac-test"
	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(1),
		"image":    testimages.Default(),
	})

	t.Log("Creating standalone Valkey CR")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 1)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	roleName := fmt.Sprintf("%s-sidecar", name)

	t.Run("Role is created with rules", func(t *testing.T) {
		ctx := context.Background()
		role, err := tc.kube.RbacV1().Roles(ns).Get(ctx, roleName, metav1.GetOptions{})
		require.NoError(t, err, "Role %s should exist", roleName)
		assert.NotEmpty(t, role.Rules, "Role should have at least one rule")
		t.Logf("Role %s has %d rules", roleName, len(role.Rules))

		// The grant is patch on this cluster's own data pods, nothing wider
		// (ADR 0012 D8 step 3). That the sidecar still does its job under it is
		// proven by the labeling assertions in sidecar_test.go, which run against
		// this same Role on a real cluster.
		require.Len(t, role.Rules, 1)
		assert.Equal(t, []string{"get", "patch"}, role.Rules[0].Verbs)
		assert.Equal(t, []string{fmt.Sprintf("%s-0", name)}, role.Rules[0].ResourceNames,
			"a single-replica cluster grants patch on pod 0 and no other pod")
	})

	t.Run("RoleBinding references sidecar ServiceAccount", func(t *testing.T) {
		ctx := context.Background()
		rbName := fmt.Sprintf("%s-sidecar", name)
		rb, err := tc.kube.RbacV1().RoleBindings(ns).Get(ctx, rbName, metav1.GetOptions{})
		require.NoError(t, err, "RoleBinding %s should exist", rbName)
		require.NotEmpty(t, rb.Subjects, "RoleBinding should have at least one subject")
		assert.Equal(t, fmt.Sprintf("%s-sidecar", name), rb.Subjects[0].Name,
			"RoleBinding subject should reference the sidecar ServiceAccount")
	})

	t.Run("Operator recreates Role after deletion", func(t *testing.T) {
		ctx := context.Background()

		// Record the UID of the current Role so we can detect a true recreation
		// (new object with different UID) rather than a simple existence check.
		originalRole, err := tc.kube.RbacV1().Roles(ns).Get(ctx, roleName, metav1.GetOptions{})
		require.NoError(t, err, "Role %s should exist before deletion", roleName)
		originalUID := originalRole.UID

		err = tc.kube.RbacV1().Roles(ns).Delete(ctx, roleName, metav1.DeleteOptions{})
		require.NoError(t, err, "Should be able to delete Role %s", roleName)

		// The operator watches owned Roles and may recreate the Role very quickly.
		// We therefore wait until a Role with a *different* UID exists, which
		// proves recreation regardless of how fast the controller reacts.
		require.Eventually(t, func() bool {
			role, err := tc.kube.RbacV1().Roles(ns).Get(ctx, roleName, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				// Role is still absent — reconcile not yet complete.
				return false
			}
			if err != nil {
				return false
			}
			// A different UID means this is a newly created object.
			return role.UID != originalUID
		}, testTimeout, pollInterval, "Operator should recreate Role %s with a new UID", roleName)

		// Newly created Role must have rules.
		role, err := tc.kube.RbacV1().Roles(ns).Get(ctx, roleName, metav1.GetOptions{})
		require.NoError(t, err)
		assert.NotEmpty(t, role.Rules, "Recreated Role should have rules")
		t.Logf("Role %s recreated with %d rules (original UID: %s, new UID: %s)",
			roleName, len(role.Rules), originalUID, role.UID)
	})
}

// TestE2E_Upgrade_SentinelQuorumDuringRollingUpdate verifies that during a
// Valkey rolling update the Sentinel quorum (≥2 of 3) is always maintained.
// The sentinel quorum guard in checkAndHandleSentinelRollingUpdate() must only
// remove a Sentinel pod when at least quorum+1 instances are ready.
func TestE2E_Upgrade_SentinelQuorumDuringRollingUpdate(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-upg-quorum"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "quorum-test"
	initialImage := testimages.UpgradeFrom
	updatedImage := testimages.UpgradeTo

	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    initialImage,
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
		},
	})

	t.Log("Creating HA Valkey CR with 3 Valkey + 3 Sentinel pods")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForStatefulSetReady(t, ns, fmt.Sprintf("%s-sentinel", name), 3)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	masterPod := tc.findMasterPod(t, ns, name, 3)
	tc.waitForConnectedReplicas(t, ns, masterPod, 6379, 2)
	tc.waitForSentinelSlaves(t, ns, name, 2)
	t.Logf("Initial master: %s", masterPod)

	// Monitor sentinel pod readiness in the background throughout the rolling
	// update. A quorum violation is counted whenever fewer than 2 of the 3
	// Sentinel pods are Ready at the same time.
	var quorumViolations atomic.Int32
	stopMonitor := make(chan struct{})
	monitorDone := make(chan struct{})

	go func() {
		defer close(monitorDone)
		ctx := context.Background()
		selector := fmt.Sprintf(
			"app.kubernetes.io/instance=%s,app.kubernetes.io/component=sentinel", name)

		for {
			select {
			case <-stopMonitor:
				return
			case <-time.After(2 * time.Second):
				pods, err := tc.kube.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
					LabelSelector: selector,
				})
				if err != nil {
					continue
				}
				readyCount := countReadyPods(pods.Items)
				if readyCount < 2 {
					quorumViolations.Add(1)
					t.Logf("QUORUM VIOLATION: only %d/%d sentinel pods ready", readyCount, 3)
				}
			}
		}
	}()

	// Trigger rolling update.
	t.Log("Updating Valkey image to trigger rolling update")
	tc.updateValkeyImage(t, ns, name, updatedImage)
	tc.waitForAllPodsImage(t, ns, name, 3, updatedImage)
	tc.waitForStatefulSetReady(t, ns, name, 3)
	// Wait for replicas to reconnect to the (possibly new) master before
	// checking the CRD phase. After a sentinel failover the new master may
	// briefly report only 1 connected slave, which keeps the phase at Syncing.
	newMasterPod := tc.findMasterPod(t, ns, name, 3)
	tc.waitForConnectedReplicas(t, ns, newMasterPod, 6379, 2)
	tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")

	// Stop monitor.
	close(stopMonitor)
	<-monitorDone

	t.Run("Sentinel quorum maintained throughout rolling update", func(t *testing.T) {
		violations := quorumViolations.Load()
		t.Logf("Quorum violations observed during rolling update: %d", violations)
		assert.Zero(t, violations,
			"Sentinel quorum (≥2) must never be violated during a rolling update")
	})

	t.Run("All Valkey pods run new image", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			pod := tc.getPod(t, ns, fmt.Sprintf("%s-%d", name, i))
			assert.Equal(t, updatedImage, pod.Spec.Containers[0].Image,
				"Pod %d should run updated image after rolling update", i)
		}
	})

	t.Run("Sentinel is healthy after rolling update", func(t *testing.T) {
		sentinelPod := fmt.Sprintf("%s-sentinel-0", name)
		resp := tc.valkeyExec(t, ns, sentinelPod, 26379, "SENTINEL", "master", name)
		assert.NotEmpty(t, resp, "Sentinel should report a master after rolling update")
		t.Logf("Sentinel master info after rolling update: %s", resp)
	})

	t.Run("Data is accessible after rolling update", func(t *testing.T) {
		newMaster := tc.findMasterPod(t, ns, name, 3)
		resp := tc.valkeyExec(t, ns, newMaster, 6379, "PING")
		assert.Equal(t, "PONG", resp, "Master should respond to PING after update")
	})
}

// --- Helpers specific to upgrade tests ---

// triggerReconcile forces the operator to reconcile the Valkey CR immediately
// by patching a test-only annotation on the CR. The operator reacts to all CR
// update events so this reliably triggers a reconcile loop within seconds.
func (tc *testClients) triggerReconcile(t *testing.T, namespace, name string) {
	t.Helper()
	ctx := context.Background()

	vk, err := tc.dynamic.Resource(valkeyGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err, "Failed to get Valkey CR %s/%s for reconcile trigger", namespace, name)

	annotations := vk.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations["e2e.test/trigger-reconcile"] = time.Now().UTC().Format(time.RFC3339Nano)
	vk.SetAnnotations(annotations)

	_, err = tc.dynamic.Resource(valkeyGVR).Namespace(namespace).Update(ctx, vk, metav1.UpdateOptions{})
	require.NoError(t, err, "Failed to touch Valkey CR %s/%s to trigger reconcile", namespace, name)
}

// countReadyPods returns the number of pods in the slice that have the Ready
// condition set to True.
func countReadyPods(pods []corev1.Pod) int {
	count := 0
	for _, pod := range pods {
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				count++
				break
			}
		}
	}
	return count
}
