//go:build e2e

package e2e

// This file covers what the volumeClaimTemplates guard is worth on a running
// cluster: enabling spec.persistence on a cluster created without it must be
// refused, and refused *visibly* -- no pod rolled, no claim created, the CR naming
// the reason -- and putting the spec back must clear it at no cost.
//
// It deliberately stops there. An earlier version of this file walked the
// orphan-delete migration that ADR 0020 D1 documents and that this operator's Event
// used to recommend. Running it for the first time on 2026-08-23 (Kind, Kubernetes
// 1.36) is what showed the recommendation to be wrong: the statefulset-controller
// adopts the orphaned pods and then wedges trying to attach the new claim to them,
// and clearing that wedge by hand cost the dataset. Both measurements, with their
// reproductions, are docs/adr/0023-volume-claim-templates-are-immutable.md D6. A
// test that walked that path would either fail forever or, worse, enshrine a
// procedure nobody should follow -- so the migration stays a documented finding
// until the two defects behind it are decided.
//
// What survives here is the half that is this operator's own code and that no unit
// test can reach: a real API server, real pods, a real dataset, and the assertion
// that a refused pass leaves all three alone. Every wait is bounded and dumps the
// cluster state it was waiting for (ADR 0017 D25, D45).

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/guided-traffic/valkey-operator/test/testimages"
)

const (
	// migrationBlockedWindow is how long the refusal is held under observation
	// before the "nothing was written" assertions run.
	//
	// It is not a bound on anything the operator does -- the ReconcileBlocked
	// condition already proves the operator saw the spec change and decided. It is
	// the window in which a *wrong* operator would act: without the guard,
	// reconcileStatefulSet copies the new pod template onto the live object (it
	// never writes volumeClaimTemplates, so the API server accepts the update),
	// StatefulSetHasChanged fires and the rolling update starts deleting pods whose
	// replacements can no longer be created -- the template mounts "data" and
	// neither an emptyDir nor a claim backs it any more. 30 s is several reconcile
	// passes plus the first pod delete.
	migrationBlockedWindow = 30 * time.Second

	// migrationConditionTimeout bounds the waits for a status condition or an
	// Event to appear. Both are written in the pass that refuses, so this is
	// scheduling and broadcast headroom on a loaded Kind cluster, not a design
	// bound.
	migrationConditionTimeout = 2 * time.Minute

	// migrationPort is the plaintext Valkey port. This scenario runs without TLS
	// and without auth so that the storage migration is the only variable.
	migrationPort = 6379
)

// migrationSeedData is written to the master before persistence is enabled. It has
// to survive the whole migration: the cluster holds it in memory only until the
// last pod is replaced, so every step that loses it is a step that lost a dataset.
var migrationSeedData = map[string]string{
	"persist-mig:key1": "written-before-persistence",
	"persist-mig:key2": "must-survive-the-recreate",
}

// TestE2E_Persistence_EnableIsRefusedOnARunningCluster enables spec.persistence on
// a running three-replica cluster and asserts the operator refuses it without
// touching anything.
//
// It fails against an operator without the guard, and not subtly: without it
// reconcileStatefulSet writes the new pod template (the API server accepts that
// write, because volumeClaimTemplates are never submitted), the drift check fires,
// and the rolling update starts deleting pods whose replacements the
// statefulset-controller can no longer create -- the template mounts "data" with
// neither an emptyDir nor a claim behind it. The visible damage is what this test
// looks for: a missing pod, a moved StatefulSet generation, a lost dataset.
func TestE2E_Persistence_EnableIsRefusedOnARunningCluster(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ctx := context.Background()

	ns := "e2e-persistence-migration"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "persist-mig"
	const replicas = 3

	t.Log("Creating a 3-replica Valkey CR without persistence and without Sentinel")
	tc.createValkey(t, ns, buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(replicas),
		"image":    testimages.Default(),
	}))
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, replicas)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	master := tc.findMasterPod(t, ns, name, replicas)
	require.NotEmpty(t, master, "no master pod answered; the cluster never formed")
	tc.waitForConnectedReplicas(t, ns, master, migrationPort, replicas-1)
	t.Logf("Master before the migration: %s", master)

	t.Run("data written before the migration reaches the replicas", func(t *testing.T) {
		tc.valkeyMSET(t, ns, master, migrationPort, migrationSeedData)

		for _, pod := range otherDataPods(name, replicas, master) {
			tc.waitForReplicaSynced(t, ns, pod, migrationPort)
			for key, want := range migrationSeedData {
				got := tc.valkeyExec(t, ns, pod, migrationPort, "GET", key)
				assert.Equal(t, want, got, "replica %s must hold %s", pod, key)
			}
		}
	})

	// Everything the refusal must leave untouched, read before the spec changes.
	uidsBefore := tc.dataPodUIDs(t, ns, name, replicas)
	stsBefore := tc.getStatefulSet(t, ns, name)
	require.Empty(t, stsBefore.Spec.VolumeClaimTemplates,
		"precondition: the cluster under test must start without volumeClaimTemplates")
	require.Equal(t, "emptyDir", dataVolumeSource(stsBefore.Spec.Template.Spec.Volumes),
		"precondition: without persistence the pod template backs %q with an emptyDir",
		dataVolumeName)
	require.Empty(t, tc.claimNames(t, ns),
		"precondition: a cluster without persistence has no PersistentVolumeClaims")

	t.Log("Enabling spec.persistence on the running cluster")
	tc.patchValkeySpec(t, ns, name, map[string]interface{}{
		"persistence.enabled": true,
		"persistence.size":    "1Gi",
	})

	t.Run("the operator refuses and names the recreate on the CR", func(t *testing.T) {
		blocked := tc.waitForValkeyCondition(t, ns, name,
			"ReconcileBlocked", "True", migrationConditionTimeout)
		assert.Equal(t, "RecreateRequired", blocked["reason"],
			"an immutable-claims refusal must be distinguishable from a foreign object or a rejected write")

		storage := tc.waitForValkeyCondition(t, ns, name,
			"StorageSpecNotApplied", "True", migrationConditionTimeout)
		assert.Equal(t, "RecreateRequired", storage["reason"],
			"a toggled persistence flag is the structural conflict, not the parameter one")
		message, _ := storage["message"].(string)
		assert.Contains(t, message, name,
			"the condition must name the StatefulSet that is stuck")
		assert.Contains(t, message, "volumeClaimTemplates",
			"the condition must say why the operator will not converge it")

		tc.waitForValkeyEvent(t, ns, name, "StatefulSetRecreateRequired", migrationConditionTimeout,
			"a StatefulSetRecreateRequired Event must appear on Valkey %s/%s; it is the surface that "+
				"carries the migration procedure", ns, name)
	})

	t.Run("nothing is written and no pod is rolled while it refuses", func(t *testing.T) {
		t.Logf("Holding %v so a wrong operator has time to act", migrationBlockedWindow)
		time.Sleep(migrationBlockedWindow)

		sts := tc.getStatefulSet(t, ns, name)
		assert.Equal(t, stsBefore.UID, sts.UID, "the StatefulSet must be the same object")
		assert.Equal(t, stsBefore.Generation, sts.Generation,
			"a refused pass must not write the StatefulSet spec at all (generation moved)")
		assert.Empty(t, sts.Spec.VolumeClaimTemplates,
			"volumeClaimTemplates are immutable; the operator must not have gained any")
		assert.Equal(t, "emptyDir", dataVolumeSource(sts.Spec.Template.Spec.Volumes),
			"the pod template must still back %q with the emptyDir the running pods use", dataVolumeName)

		// The pods, one by one rather than through a map comparison, because a
		// missing pod is the pre-fix outcome and deserves to say so itself.
		for i := 0; i < replicas; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			pod, err := tc.kube.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{})
			if !assert.NoError(t, err,
				"pod %s must still exist: a refused persistence change may not roll a pod, and a pod "+
					"rolled here could not be replaced at all -- the written template would mount %q with "+
					"neither an emptyDir nor a claim behind it", podName, dataVolumeName) {
				continue
			}
			assert.Equal(t, uidsBefore[podName], string(pod.UID), "pod %s must not have been replaced", podName)
			assert.True(t, podIsReady(pod), "pod %s must stay ready", podName)
		}

		assert.Empty(t, tc.claimNames(t, ns),
			"no PersistentVolumeClaim may appear while the operator refuses the update")

		got := tc.valkeyExec(t, ns, master, migrationPort, "GET", "persist-mig:key1")
		assert.Equal(t, migrationSeedData["persist-mig:key1"], got,
			"the running cluster must be untouched by the refusal")

		if t.Failed() {
			t.Log(tc.migrationForensics(t, ns, name, replicas))
		}
	})

	// --- the free way out ----------------------------------------------------
	//
	// Reverting the spec is the one remediation this operator can stand behind:
	// it needs no maintenance window, touches no pod, and is what the Event tells
	// a user who did not mean to change storage. Applying the change instead needs
	// the StatefulSet recreated by hand, which is a rebuild rather than a rolling
	// change and is not walked here -- ADR 0023 D6 records why.

	t.Log("Reverting spec.persistence")
	tc.patchValkeySpec(t, ns, name, map[string]interface{}{
		"persistence.enabled": false,
	})

	t.Run("reverting the spec clears the block without touching the cluster", func(t *testing.T) {
		blocked := tc.waitForValkeyCondition(t, ns, name,
			"ReconcileBlocked", "False", migrationConditionTimeout)
		assert.Equal(t, "ReconcileSucceeded", blocked["reason"])

		// The condition exists on this CR because the refusal set it True, so it has
		// to be cleared rather than merely absent.
		storage := tc.waitForValkeyCondition(t, ns, name,
			"StorageSpecNotApplied", "False", migrationConditionTimeout)
		assert.Equal(t, "StorageSpecApplied", storage["reason"],
			"once the live claims match spec.persistence again the condition must resolve")

		tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")

		for i := 0; i < replicas; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			pod, err := tc.kube.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{})
			if !assert.NoError(t, err, "pod %s must still exist after the revert", podName) {
				continue
			}
			assert.Equal(t, uidsBefore[podName], string(pod.UID),
				"reverting a change the operator never applied must not roll pod %s", podName)
		}
		assert.Empty(t, tc.claimNames(t, ns),
			"no PersistentVolumeClaim may be left behind by a change that never took effect")

		for key, want := range migrationSeedData {
			got := tc.valkeyExec(t, ns, master, migrationPort, "GET", key)
			assert.Equal(t, want, got, "key %s must be untouched by the whole episode", key)
		}

		if t.Failed() {
			t.Log(tc.migrationForensics(t, ns, name, replicas))
		}
	})
}

// dataVolumeName is the name the builder gives the data volume and the claim
// template (internal/builder/statefulset.go, DataVolumeName). The claims the
// statefulset-controller derives from it are named data-<statefulset>-<ordinal>.
const dataVolumeName = "data"

// podIsReady reports whether a pod carries a Ready condition in status True.
//
// It is a twin of podReady in fleet_upgrade_test.go rather than a shared helper:
// that file is behind the second build tag `fleetupgrade` and does not compile
// into the ordinary suite, so a reference to it would fail every normal e2e build.
func podIsReady(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// dataPodUIDs maps pod name to UID for the ordinals of the data StatefulSet, so a
// later comparison can tell a replaced pod from a surviving one. Same relationship
// to podUIDs in fleet_upgrade_test.go as podIsReady has to podReady.
func (tc *testClients) dataPodUIDs(t *testing.T, namespace, stsName string, replicas int) map[string]string {
	t.Helper()

	uids := make(map[string]string, replicas)
	for i := 0; i < replicas; i++ {
		podName := fmt.Sprintf("%s-%d", stsName, i)
		pod, err := tc.kube.CoreV1().Pods(namespace).Get(
			context.Background(), podName, metav1.GetOptions{})
		require.NoError(t, err, "pod %s should exist", podName)
		uids[podName] = string(pod.UID)
	}
	return uids
}

// otherDataPods returns the data pods of a cluster except the named one.
func otherDataPods(stsName string, replicas int, except string) []string {
	var pods []string
	for i := 0; i < replicas; i++ {
		podName := fmt.Sprintf("%s-%d", stsName, i)
		if podName != except {
			pods = append(pods, podName)
		}
	}
	return pods
}

// dataVolumeSource renders how the data volume is backed: "emptyDir",
// "pvc:<claim>", or "none" when the volume list does not declare it at all.
//
// "none" is a real and expected answer for a persistent StatefulSet's pod
// template: the claim template supplies the volume, and the statefulset-controller
// injects it into each generated pod. On a pod object it means the opposite -- a
// mount with nothing behind it.
func dataVolumeSource(volumes []corev1.Volume) string {
	for i := range volumes {
		if volumes[i].Name != dataVolumeName {
			continue
		}
		switch {
		case volumes[i].EmptyDir != nil:
			return "emptyDir"
		case volumes[i].PersistentVolumeClaim != nil:
			return "pvc:" + volumes[i].PersistentVolumeClaim.ClaimName
		default:
			return "other"
		}
	}
	return "none"
}

// claimNames lists the PersistentVolumeClaims in a namespace, sorted.
func (tc *testClients) claimNames(t *testing.T, namespace string) []string {
	t.Helper()

	list, err := tc.kube.CoreV1().PersistentVolumeClaims(namespace).List(
		context.Background(), metav1.ListOptions{})
	require.NoError(t, err, "Failed to list PersistentVolumeClaims in %s", namespace)

	names := make([]string, 0, len(list.Items))
	for i := range list.Items {
		names = append(names, list.Items[i].Name)
	}
	sort.Strings(names)
	return names
}

// ownerSummary renders a controller reference for a log line, tolerating nil.
func ownerSummary(ref *metav1.OwnerReference) string {
	if ref == nil {
		return "<none>"
	}
	return fmt.Sprintf("%s/%s uid=%s", ref.Kind, ref.Name, ref.UID)
}

// migrationForensics dumps everything that distinguishes the failure modes of this
// scenario from one another, at the moment the assertion fails (ADR 0017 D45):
// which StatefulSet object is live and what claims it carries, what the
// statefulset-controller has said about it, and for every ordinal whether the pod
// is an original or a replacement, who controls it and what backs its data volume.
//
// The Events of the StatefulSet are the load-bearing part. A pod the controller
// adopted but cannot bring in line with the claim template shows up there as a
// FailedUpdate naming the rejected pod write, and nothing else in the cluster says
// so.
//
// Every lookup is best-effort: this runs while a test is already failing, and a
// dump that can fail replaces the real message with its own.
func (tc *testClients) migrationForensics(t *testing.T, namespace, stsName string, replicas int) string {
	t.Helper()
	ctx := context.Background()

	var b strings.Builder
	fmt.Fprintf(&b, "migration forensics for %s/%s\n", namespace, stsName)

	sts, err := tc.kube.AppsV1().StatefulSets(namespace).Get(ctx, stsName, metav1.GetOptions{})
	if err != nil {
		fmt.Fprintf(&b, "  statefulset lookup failed: %v\n", err)
	} else {
		fmt.Fprintf(&b, "  statefulset uid=%s generation=%d deletionTimestamp=%v\n",
			sts.UID, sts.Generation, sts.DeletionTimestamp)
		fmt.Fprintf(&b, "    status: replicas=%d ready=%d current=%d updated=%d\n",
			sts.Status.Replicas, sts.Status.ReadyReplicas, sts.Status.CurrentReplicas, sts.Status.UpdatedReplicas)
		fmt.Fprintf(&b, "    template data volume: %s\n", dataVolumeSource(sts.Spec.Template.Spec.Volumes))
		for i := range sts.Spec.VolumeClaimTemplates {
			claim := sts.Spec.VolumeClaimTemplates[i]
			size := claim.Spec.Resources.Requests[corev1.ResourceStorage]
			fmt.Fprintf(&b, "    claim template %s: size=%s class=%v modes=%v\n",
				claim.Name, size.String(), claim.Spec.StorageClassName, claim.Spec.AccessModes)
		}
		if len(sts.Spec.VolumeClaimTemplates) == 0 {
			fmt.Fprintf(&b, "    claim templates: <none>\n")
		}
	}

	fmt.Fprintf(&b, "  statefulset events:\n%s", indentLines(
		tc.eventLines(ctx, namespace, stsName), "    "))

	for i := 0; i < replicas; i++ {
		podName := fmt.Sprintf("%s-%d", stsName, i)
		pod, err := tc.kube.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			fmt.Fprintf(&b, "  pod %s: %v\n", podName, err)
			continue
		}
		fmt.Fprintf(&b, "  pod %s: uid=%s phase=%s ready=%v controller=%s data=%s deletionTimestamp=%v\n",
			podName, pod.UID, pod.Status.Phase, podIsReady(pod), ownerSummary(metav1.GetControllerOf(pod)),
			dataVolumeSource(pod.Spec.Volumes), pod.DeletionTimestamp)
	}

	claims, err := tc.kube.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(&b, "  claim lookup failed: %v\n", err)
	} else {
		fmt.Fprintf(&b, "  claims (%d):\n", len(claims.Items))
		for i := range claims.Items {
			pvc := claims.Items[i]
			size := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
			fmt.Fprintf(&b, "    %s: phase=%s size=%s class=%v\n",
				pvc.Name, pvc.Status.Phase, size.String(), pvc.Spec.StorageClassName)
		}
	}

	// Read the CR directly rather than through getValkeyStatus: that helper is a
	// require, and a forensics dump that can fail the test replaces the failure it
	// was called to explain with its own.
	if cr, err := tc.dynamic.Resource(valkeyGVR).Namespace(namespace).Get(
		ctx, stsName, metav1.GetOptions{}); err != nil {
		fmt.Fprintf(&b, "  valkey lookup failed: %v\n", err)
	} else {
		phase, _, _ := unstructured.NestedString(cr.Object, "status", "phase")
		message, _, _ := unstructured.NestedString(cr.Object, "status", "message")
		fmt.Fprintf(&b, "  valkey status: phase=%q message=%q\n", phase, message)
	}
	for _, condType := range []string{"ReconcileBlocked", "StorageSpecNotApplied"} {
		fmt.Fprintf(&b, "    condition %s: %v\n", condType,
			tc.valkeyStatusCondition(t, namespace, stsName, condType))
	}

	return b.String()
}

// eventLines renders the Events of one object, newest last, or the reason they
// could not be read.
func (tc *testClients) eventLines(ctx context.Context, namespace, objectName string) string {
	events, err := tc.kube.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "involvedObject.name=" + objectName,
	})
	if err != nil {
		return fmt.Sprintf("<events lookup failed: %v>\n", err)
	}
	if len(events.Items) == 0 {
		return "<none>\n"
	}
	var b strings.Builder
	for _, ev := range events.Items {
		fmt.Fprintf(&b, "%v %s/%s (x%d): %s\n", ev.LastTimestamp, ev.Type, ev.Reason, ev.Count, ev.Message)
	}
	return b.String()
}
