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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
)

// nudgeAnnotationKey is the annotation the operator bumps to force a
// statefulset-controller resync (internal/builder.AnnotationNudge).
const nudgeAnnotationKey = "vko.gtrfc.com/nudge"

// admissionBlockHold is how long pod creation stays blocked. It must be long
// enough for the statefulset-controller's exponential backoff (5 ms · 2^n) to
// grow well past the recovery deadline asserted below — otherwise the test would
// pass on the statefulset-controller's own retry and guard nothing.
const admissionBlockHold = 90 * time.Second

// admissionRecoveryDeadline is the time budget for all data pods to be recreated
// after the webhook is removed. The nudge fires at most nudgeGracePeriod (10 s)
// plus NudgeInterval (20 s) after the block clears; the remainder is scheduling
// headroom on a loaded Kind cluster. In the incident the same step took 5 min 29 s.
const admissionRecoveryDeadline = 60 * time.Second

// blockPodCreation installs a fail-closed MutatingWebhookConfiguration that
// matches CREATE pods in the given namespace only and points at a Service with
// no endpoints, reproducing the incident's
// "no endpoints available for service" rejection. The namespaceSelector is
// mandatory: an unscoped fail-closed webhook would also block Kind system pods
// and every parallel e2e test.
//
// Returns a function that removes the webhook (idempotent).
func (tc *testClients) blockPodCreation(t *testing.T, namespace, name string) func() {
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
			Name: "blackhole.e2e.vko.gtrfc.com",
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				Service: &admissionregistrationv1.ServiceReference{
					Name:      svc.Name,
					Namespace: namespace,
					Path:      &path,
					Port:      &port,
				},
			},
			Rules: []admissionregistrationv1.RuleWithOperations{{
				Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create},
				Rule: admissionregistrationv1.Rule{
					APIGroups:   []string{""},
					APIVersions: []string{"v1"},
					Resources:   []string{"pods"},
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
	t.Logf("Installed fail-closed webhook %s blocking CREATE pods in namespace %s", name, namespace)

	removed := false
	return func() {
		if removed {
			return
		}
		removed = true
		err := tc.kube.AdmissionregistrationV1().MutatingWebhookConfigurations().
			Delete(ctx, name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			t.Logf("Failed to remove blocking webhook %s: %v", name, err)
			return
		}
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
// admission-gap ticket. Step "pods return quickly after the webhook is removed"
// is the regression guard: without the nudge the recovery is bounded only by the
// statefulset-controller's backoff, which is minutes deep by then.
func TestE2E_AdmissionRejection_StatefulSetNudgeRecovery(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ctx := context.Background()

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

		phase, _ := tc.getValkeyStatus(t, ns, name)["phase"].(string)
		assert.NotEqual(t, "OK", phase, "CR must not report OK with zero data pods")
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
}
