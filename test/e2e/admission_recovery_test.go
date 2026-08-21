//go:build e2e

package e2e

// Tests in this file cover recovery after a transient admission-webhook
// rejection of pod creation.
//
// Incident this guards against (infra-d, 2026-08-19): a node drain evicted the
// single-replica Kyverno admission controller together with all three Valkey
// data pods. For ~90 s a failurePolicy=Fail webhook had no endpoints, so every
// pod create was rejected API-side. The statefulset-controller then sat out its
// exponential workqueue backoff — 5 min 29 s measured — while the webhook had
// already been healthy for five of those minutes. Nothing woke it: the
// StatefulSet object was never written (no spec drift) and with zero pods there
// were no pod events either.
//
// The operator's answer is the nudge annotation (see nudgeShortStatefulSets):
// while a StatefulSet reports fewer pods than requested, the operator bumps
// vko.gtrfc.com/nudge on it, which produces an informer event and an immediate
// statefulset-controller sync.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
)

// nudgeAnnotationKey is the annotation the operator bumps to force a
// statefulset-controller resync (internal/builder.AnnotationNudge).
const nudgeAnnotationKey = "vko.gtrfc.com/nudge"

// blackholeWebhookName is the webhook name the API server quotes in its
// rejection message; T4 asserts the CR condition carries exactly this string.
const blackholeWebhookName = "blackhole.e2e.vko.gtrfc.com"

// admissionBlockHold is how long pod creation stays blocked. It is long enough
// for the statefulset-controller's exponential backoff (5 ms · 2^n) to grow past
// the recovery deadline asserted below, so a pass is normally the nudge's doing
// and not the controller's own retry. Measured runs show that is a tendency, not
// a guarantee — see the test header on why the deterministic guard is a unit test.
const admissionBlockHold = 90 * time.Second

// admissionRecoveryDeadline is the time budget for all data pods to be recreated
// after the webhook is removed. The nudge fires at most nudgeGracePeriod (10 s)
// plus NudgeInterval (20 s) after the block clears; the remainder is scheduling
// headroom on a loaded Kind cluster. In the incident the same step took 5 min 29 s.
const admissionRecoveryDeadline = 60 * time.Second

// nudgeEventTimeout is how long the StatefulSetNudged Event may take to appear.
// The nudge itself is already observed on the StatefulSet by the subtest before
// it, so this budget only covers the recorder's asynchronous broadcast.
const nudgeEventTimeout = 60 * time.Second

// blockPodCreation installs a fail-closed webhook that rejects CREATE pods in
// the given namespace, as the incident's Kyverno webhook did.
func (tc *testClients) blockPodCreation(t *testing.T, namespace, name string) func() {
	t.Helper()
	return tc.blockCoreResourceCreation(t, namespace, name, "pods")
}

// blockCoreResourceCreation installs a fail-closed MutatingWebhookConfiguration
// that matches CREATE of the given core/v1 resources in the given namespace only
// and points at a Service with no endpoints, reproducing the incident's
// "no endpoints available for service" rejection. The namespaceSelector is
// mandatory: an unscoped fail-closed webhook would also block Kind system pods
// and every parallel e2e test.
//
// Returns a function that removes the webhook (idempotent).
func (tc *testClients) blockCoreResourceCreation(t *testing.T, namespace, name string, resources ...string) func() {
	t.Helper()
	return tc.blockResourceOperations(t, namespace, name, "", "v1",
		[]admissionregistrationv1.OperationType{admissionregistrationv1.Create}, resources...)
}

// blockResourceOperations is the general form of blockCoreResourceCreation: it
// blocks the given operations on the given apiGroup/apiVersion resources in one
// namespace. T2 uses it for UPDATE on apps/v1 statefulsets.
func (tc *testClients) blockResourceOperations(t *testing.T, namespace, name, apiGroup, apiVersion string,
	operations []admissionregistrationv1.OperationType, resources ...string) func() {
	t.Helper()
	ctx := context.Background()

	// A Service without endpoints: the API server resolves it, finds no backend
	// and — with failurePolicy Fail — rejects the request.
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "admission-blackhole", Namespace: namespace},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "admission-blackhole-no-such-pod"},
			Ports:    []corev1.ServicePort{{Port: 443, TargetPort: intstr.FromInt32(8443)}},
		},
	}
	tc.ensureServiceCreated(t, namespace, svc)

	failurePolicy := admissionregistrationv1.Fail
	sideEffects := admissionregistrationv1.SideEffectClassNone
	scope := admissionregistrationv1.NamespacedScope
	path := "/mutate"
	port := int32(443)
	timeout := int32(1)

	webhook := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionregistrationv1.MutatingWebhook{{
			Name: blackholeWebhookName,
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				Service: &admissionregistrationv1.ServiceReference{
					Name:      svc.Name,
					Namespace: namespace,
					Path:      &path,
					Port:      &port,
				},
			},
			Rules: []admissionregistrationv1.RuleWithOperations{{
				Operations: operations,
				Rule: admissionregistrationv1.Rule{
					APIGroups:   []string{apiGroup},
					APIVersions: []string{apiVersion},
					Resources:   resources,
					Scope:       &scope,
				},
			}},
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"kubernetes.io/metadata.name": namespace},
			},
			FailurePolicy:           &failurePolicy,
			SideEffects:             &sideEffects,
			TimeoutSeconds:          &timeout,
			AdmissionReviewVersions: []string{"v1"},
		}},
	}

	_ = tc.kube.AdmissionregistrationV1().MutatingWebhookConfigurations().
		Delete(ctx, name, metav1.DeleteOptions{})
	_, err := tc.kube.AdmissionregistrationV1().MutatingWebhookConfigurations().
		Create(ctx, webhook, metav1.CreateOptions{})
	require.NoError(t, err, "Failed to install blocking webhook %s", name)
	t.Logf("Installed fail-closed webhook %s blocking %v %v in namespace %s", name, operations, resources, namespace)

	removed := false
	// The flag is set only after the delete succeeded, so a transient API error
	// on the first call leaves the deferred second call able to retry instead of
	// short-circuiting and leaking the cluster-scoped webhook configuration.
	return func() {
		if removed {
			return
		}
		err := tc.kube.AdmissionregistrationV1().MutatingWebhookConfigurations().
			Delete(ctx, name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			t.Logf("Failed to remove blocking webhook %s: %v", name, err)
			return
		}
		removed = true
		t.Logf("Removed blocking webhook %s", name)
	}
}

// waitForStatefulSetCreatedPods waits until a StatefulSet reports the expected
// number of created (not necessarily ready) pods in status.replicas.
func (tc *testClients) waitForStatefulSetCreatedPods(ctx context.Context, namespace, name string, replicas int32, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		sts, err := tc.kube.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return sts.Status.Replicas >= replicas, nil
	})
}

// TestE2E_AdmissionRejection_StatefulSetNudgeRecovery is scenario T1 of the
// admission-gap ticket.
//
// Step "all data pods return shortly after the webhook is removed" is a forward
// assertion, not the regression guard for the nudge: against an operator without
// the nudge its outcome is a coin flip, because the statefulset-controller may
// happen to retry inside the deadline on its own (measured on the same unfixed
// binary: pass at 15 s under load, fail at 60 s isolated). With the nudge in
// place the step is deterministic — bounded by nudgeGracePeriod + NudgeInterval.
//
// The deterministic guard for the nudge is the unit test
// TestReconcileWorkload_RequeuesWhileShortOfPods (internal/controller/nudge_test.go).
func TestE2E_AdmissionRejection_StatefulSetNudgeRecovery(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ctx := context.Background()

	// Start of the window the operator-log assertion below inspects. Restricting
	// it keeps the assertion honest on long-lived clusters whose operator log
	// still carries rejections from before the RBAC fix was deployed.
	testStart := time.Now()

	ns := "e2e-admission-nudge"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "nudge-test"
	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(3),
		"image":    "valkey/valkey:8.0",
		"sentinel": map[string]interface{}{
			"enabled":  true,
			"replicas": int64(3),
		},
	})

	t.Log("Creating HA Valkey CR with Sentinel")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	// Block pod creation the way the incident did, then evict all data pods.
	removeWebhook := tc.blockPodCreation(t, ns, "e2e-nudge-blackhole")
	defer removeWebhook()

	for i := 0; i < 3; i++ {
		tc.deletePod(t, ns, fmt.Sprintf("%s-%d", name, i))
	}
	t.Log("Deleted all three data pods while pod creation is rejected")

	t.Run("pod creation stays rejected and the CR leaves OK", func(t *testing.T) {
		err := wait.PollUntilContextTimeout(ctx, 2*time.Second, 60*time.Second, true, func(ctx context.Context) (bool, error) {
			sts, err := tc.kube.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			return sts.Status.Replicas == 0, nil
		})
		require.NoError(t, err, "data StatefulSet should report zero created pods while the webhook rejects creates")

		// Poll the phase instead of reading it once: the StatefulSet poll above
		// exits on the same watch event the operator still has to process, so a
		// single read races the operator writing the status.
		lastPhase := "<none>"
		err = wait.PollUntilContextTimeout(ctx, 2*time.Second, 60*time.Second, true, func(ctx context.Context) (bool, error) {
			cr, err := tc.dynamic.Resource(valkeyGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			phase, found, err := unstructured.NestedString(cr.Object, "status", "phase")
			if err != nil || !found {
				return false, nil
			}
			lastPhase = phase
			return phase != "OK", nil
		})
		require.NoError(t, err, "CR must leave phase OK with zero data pods (last observed phase: %s)", lastPhase)
		t.Logf("CR left phase OK, now reporting %s", lastPhase)
	})

	t.Run("operator nudges the StatefulSet instead of waiting", func(t *testing.T) {
		err := wait.PollUntilContextTimeout(ctx, 2*time.Second, 60*time.Second, true, func(ctx context.Context) (bool, error) {
			sts, err := tc.kube.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			return sts.Annotations[nudgeAnnotationKey] != "", nil
		})
		require.NoError(t, err, "operator must bump %s while the StatefulSet is short of pods", nudgeAnnotationKey)
	})

	// NA12 regression guard: the operator records through events.k8s.io/v1, so a
	// ClusterRole granting only core-group events silently discards every Event
	// ("Server rejected event" in the operator log, nothing on the CR). The nudge
	// above must therefore be re-observable as an Event on the Valkey CR. The
	// recorder broadcasts asynchronously, hence the poll.
	t.Run("nudge Event is visible on the Valkey CR", func(t *testing.T) {
		tc.waitForValkeyEvent(t, ns, name, "StatefulSetNudged", nudgeEventTimeout,
			"a StatefulSetNudged Event must appear on Valkey %s/%s; if it never does, the operator RBAC is missing create/patch on events.k8s.io", ns, name)
	})

	// Hold the block long enough for the statefulset-controller's own backoff to
	// exceed the recovery deadline asserted below.
	t.Logf("Holding the admission block for %v so the statefulset-controller backoff grows", admissionBlockHold)
	time.Sleep(admissionBlockHold)

	t.Run("all data pods return shortly after the webhook is removed", func(t *testing.T) {
		start := time.Now()
		removeWebhook()

		err := tc.waitForStatefulSetCreatedPods(ctx, ns, name, 3, admissionRecoveryDeadline)
		elapsed := time.Since(start)
		require.NoError(t, err,
			"data pods must be recreated within %v of the webhook being removed (took longer; in the incident the statefulset-controller needed 5m29s)",
			admissionRecoveryDeadline)
		t.Logf("All three data pods recreated %v after the webhook was removed", elapsed)
	})

	t.Run("cluster returns to OK", func(t *testing.T) {
		tc.waitForStatefulSetReady(t, ns, name, 3)
		tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")
	})

	// Second half of the NA12 guard: no Event write may be denied by RBAC. The
	// assertion is scoped to forbidden-rejections because a namespace being torn
	// down by a parallel test can legitimately reject an Event create without any
	// RBAC involvement, and to this test's time window so pre-fix history in a
	// long-lived operator pod's log cannot fail a cluster that is already fixed.
	t.Run("operator log has no RBAC-rejected events", func(t *testing.T) {
		pods, err := tc.kube.CoreV1().Pods("valkey-operator-system").List(ctx, metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=valkey-operator",
		})
		require.NoError(t, err)
		require.NotEmpty(t, pods.Items, "no operator pods found in valkey-operator-system")

		for _, pod := range pods.Items {
			logs := tc.getPodLogsSince(t, "valkey-operator-system", pod.Name, testStart)
			for _, line := range strings.Split(logs, "\n") {
				if strings.Contains(line, "Server rejected event") && strings.Contains(line, "forbidden") {
					t.Errorf("operator pod %s logged an RBAC-rejected event: %s", pod.Name, line)
				}
			}
		}
	})
}

// TestE2E_AdmissionRejection_ReconcileBlockedCondition is scenario T4 of the
// admission-gap ticket: while a fail-closed webhook rejects a write the operator
// owns, the CR must name the webhook instead of leaving the user with a bare
// "Error" phase and operator logs to read.
//
// The webhook here blocks CREATE configmaps, not pods: the operator writes the
// ConfigMap itself, whereas pod creation is the statefulset-controller's job and
// never reaches the operator's error path (that failure mode is T1 above).
func TestE2E_AdmissionRejection_ReconcileBlockedCondition(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)

	ns := "e2e-admission-condition"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	// The root-CA publisher creates kube-root-ca.crt in every new namespace.
	// Wait for it, otherwise the webhook below blocks it and pods cannot mount
	// their service-account CA once the block is lifted.
	tc.waitForConfigMap(t, ns, "kube-root-ca.crt")

	removeWebhook := tc.blockCoreResourceCreation(t, ns, "e2e-condition-blackhole", "configmaps")
	defer removeWebhook()

	name := "condition-test"
	t.Log("Creating Valkey CR while ConfigMap creation is rejected")
	tc.createValkey(t, ns, buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(1),
		"image":    "valkey/valkey:8.0",
	}))
	defer tc.deleteValkey(t, ns, name)

	t.Run("CR names the rejecting webhook", func(t *testing.T) {
		cond := tc.waitForValkeyCondition(t, ns, name, "ReconcileBlocked", "True", 90*time.Second)
		assert.Equal(t, "AdmissionWebhookDenied", cond["reason"],
			"an admission rejection must be distinguishable from an ordinary write failure")
		message, _ := cond["message"].(string)
		assert.Contains(t, message, blackholeWebhookName,
			"the condition message must carry the webhook name")
	})

	t.Run("condition clears once the block is gone", func(t *testing.T) {
		removeWebhook()
		cond := tc.waitForValkeyCondition(t, ns, name, "ReconcileBlocked", "False", 120*time.Second)
		assert.Equal(t, "ReconcileSucceeded", cond["reason"])
		tc.waitForValkeyPhase(t, ns, name, "OK")
	})
}

// patchValkeySpec applies fields to the CR spec, retrying on conflict.
func (tc *testClients) patchValkeySpec(t *testing.T, namespace, name string, fields map[string]interface{}) {
	t.Helper()
	ctx := context.Background()

	err := wait.PollUntilContextTimeout(ctx, time.Second, 30*time.Second, true,
		func(ctx context.Context) (bool, error) {
			cr, err := tc.dynamic.Resource(valkeyGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			for path, value := range fields {
				if err := unstructured.SetNestedField(cr.Object, value,
					append([]string{"spec"}, strings.Split(path, ".")...)...); err != nil {
					return false, err
				}
			}
			if _, err := tc.dynamic.Resource(valkeyGVR).Namespace(namespace).
				Update(ctx, cr, metav1.UpdateOptions{}); err != nil {
				if apierrors.IsConflict(err) {
					return false, nil
				}
				return false, err
			}
			return true, nil
		})
	require.NoError(t, err, "Failed to patch Valkey CR %s/%s", namespace, name)
	t.Logf("Patched Valkey %s/%s: %v", namespace, name, fields)
}

// waitForNetworkPolicies waits until at least one NetworkPolicy exists in the namespace.
func (tc *testClients) waitForNetworkPolicies(t *testing.T, namespace string, timeout time.Duration) int {
	t.Helper()

	count := 0
	err := wait.PollUntilContextTimeout(context.Background(), 2*time.Second, timeout, true,
		func(ctx context.Context) (bool, error) {
			list, err := tc.kube.NetworkingV1().NetworkPolicies(namespace).List(ctx, metav1.ListOptions{})
			if err != nil {
				return false, err
			}
			count = len(list.Items)
			return count > 0, nil
		})
	require.NoError(t, err, "no NetworkPolicy was created in namespace %s within %v", namespace, timeout)
	return count
}

// TestE2E_AdmissionRejection_ReconcileContinuesPastRejectedWrite is scenario T2
// of the admission-gap ticket, guarding WP3: while a fail-closed webhook rejects
// UPDATE on statefulsets, the steps behind the StatefulSet write must still run
// and the CR status must keep telling the truth about the data plane.
//
// On 1.10.46 reconcileResources returned on the first failing sub-resource, so a
// single rejected StatefulSet write silenced NetworkPolicies, monitoring and the
// status update for as long as the rejection lasted.
func TestE2E_AdmissionRejection_ReconcileContinuesPastRejectedWrite(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)

	ns := "e2e-admission-continue"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "continue-test"
	t.Log("Creating a healthy single-replica Valkey CR")
	tc.createValkey(t, ns, buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(1),
		"image":    "valkey/valkey:8.0",
	}))
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 1)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	// UPDATE only: the StatefulSet already exists, so this rejects exactly the
	// operator's write of the scaled spec while leaving every CREATE — Services,
	// ConfigMaps, NetworkPolicies — admissible.
	removeWebhook := tc.blockResourceOperations(t, ns, "e2e-continue-blackhole", "apps", "v1",
		[]admissionregistrationv1.OperationType{admissionregistrationv1.Update}, "statefulsets")
	defer removeWebhook()

	// Scale up (needs a StatefulSet UPDATE → rejected) and enable NetworkPolicies
	// (a step behind the StatefulSet → must still run).
	tc.patchValkeySpec(t, ns, name, map[string]interface{}{
		"replicas":              int64(2),
		"networkPolicy.enabled": true,
	})

	t.Run("steps behind the rejected write still run", func(t *testing.T) {
		created := tc.waitForNetworkPolicies(t, ns, 90*time.Second)
		t.Logf("%d NetworkPolicy/-ies created while the StatefulSet update was rejected", created)
	})

	t.Run("CR names the rejected StatefulSet write", func(t *testing.T) {
		cond := tc.waitForValkeyCondition(t, ns, name, "ReconcileBlocked", "True", 90*time.Second)
		assert.Equal(t, "AdmissionWebhookDenied", cond["reason"])
		message, _ := cond["message"].(string)
		assert.Contains(t, message, blackholeWebhookName)
		assert.Contains(t, message, "StatefulSet",
			"the condition must name the failing step, not just the webhook")
	})

	t.Run("status keeps reflecting the data plane", func(t *testing.T) {
		// The StatefulSet never grew, so the running pod is still the truth.
		sts := tc.getStatefulSet(t, ns, name)
		assert.Equal(t, int32(1), *sts.Spec.Replicas,
			"the rejected update must not have reached the StatefulSet")

		status := tc.getValkeyStatus(t, ns, name)
		ready, _ := status["readyReplicas"].(int64)
		assert.Equal(t, int64(1), ready,
			"status must report the pod that is actually running")
	})

	t.Run("cluster converges once the block is gone", func(t *testing.T) {
		removeWebhook()
		tc.waitForValkeyCondition(t, ns, name, "ReconcileBlocked", "False", 120*time.Second)
		tc.waitForStatefulSetReady(t, ns, name, 2)
		tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")
	})
}
