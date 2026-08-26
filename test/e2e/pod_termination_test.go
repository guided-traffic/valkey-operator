//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/guided-traffic/valkey-operator/test/testimages"
)

// TestE2E_RollingUpdate_NoSecondDeleteWhileAPodTerminates is the field half of
// docs/adr/0026-a-pod-being-deleted-is-not-available.md.
//
// The rule it proves is the one no unit test can: kubelet keeps a terminating
// pod's PodReady condition True until the pod object is gone, so every "is this
// pod healthy" guard of the rolling update used to see a healthy pod where one
// was on its way out. Only a real kubelet produces that state.
//
// The scenario is the one an adversarial re-review found and the first version of
// the fix would have failed: an **already-replaced** pod is deleted mid-roll (the
// shape a chaos schedule, an eviction or a node drain produces), and the roll must
// hold rather than delete its next candidate on top of it.
//
// The assertion is a sampler running for the whole roll: at no observed moment do
// two pods of the data tier carry a DeletionTimestamp at the same time, **except
// where this test itself caused the overlap**.
//
// That exception is not a loophole, it is the whole difficulty of the scenario.
// The test injects on the same event the operator reacts to -- a replaced pod
// becoming Ready is what unblocks the roll's next delete and is also what makes a
// victim available here -- so whoever wins that race decides who deleted second.
// When the operator deleted first, into a quiet tier, and this test then deleted on
// top of it, the operator did exactly what the invariant asks and the overlap is
// the test's. The sampler is told which pod the test deleted and what was already
// terminating at that instant, and excuses only that combination; everything else,
// including any pod the operator puts on its way out *after* the injection, is a
// violation. Measured in CI before this attribution existed: the operator deleted
// its next candidate 0.6 s before the injection and was blamed for the result.
func TestE2E_RollingUpdate_NoSecondDeleteWhileAPodTerminates(t *testing.T) {
	t.Parallel()

	for _, topology := range []struct {
		name     string
		sentinel bool
	}{
		{name: "sentinel", sentinel: true},
		{name: "no-sentinel", sentinel: false},
	} {
		t.Run(topology.name, func(t *testing.T) {
			t.Parallel()
			runNoSecondDeleteScenario(t, topology.name, topology.sentinel)
		})
	}
}

func runNoSecondDeleteScenario(t *testing.T, suffix string, sentinel bool) {
	t.Helper()
	tc := newTestClients(t)
	ns := "e2e-term-" + suffix
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	name := "term-" + suffix
	initialImage := testimages.UpgradeFrom
	updatedImage := testimages.UpgradeTo

	spec := map[string]interface{}{
		"replicas": int64(3),
		"image":    initialImage,
	}
	if sentinel {
		spec["sentinel"] = map[string]interface{}{"enabled": true, "replicas": int64(3)}
	}

	tc.createValkey(t, ns, buildValkeyObject(name, ns, spec))
	defer tc.deleteValkey(t, ns, name)

	tc.waitForStatefulSetReady(t, ns, name, 3)
	if sentinel {
		tc.waitForStatefulSetReady(t, ns, fmt.Sprintf("%s-sentinel", name), 3)
	}
	tc.waitForValkeyPhase(t, ns, name, "OK")

	masterPod := tc.findMasterPod(t, ns, name, 3)
	tc.waitForConnectedReplicas(t, ns, masterPod, 6379, 2)
	if sentinel {
		tc.waitForSentinelSlaves(t, ns, name, 2)
	}

	tc.valkeyMSET(t, ns, masterPod, 6379, map[string]string{
		"term:key1": "must-survive-the-chaos-delete",
	})
	tc.waitForConnectedReplicas(t, ns, masterPod, 6379, 2)

	// Sample the whole roll, starting before the image change so nothing between
	// the two is missed.
	sampler := newTerminationSampler(t, tc, ns, name, 3)
	sampler.start()

	t.Log("Triggering the rolling update")
	tc.updateValkeyImage(t, ns, name, updatedImage)

	// The re-review shape: wait until the roll has replaced at least one pod but
	// has not finished, then delete that already-replaced pod. That pod keeps
	// answering Ready for its whole termination, which is exactly what used to let
	// verifyReplacedReplicasSynced wave the next delete through.
	if victim := tc.waitForAReplacedPodMidRoll(t, ns, name, 3, updatedImage, initialImage); victim != "" {
		// A fresh read immediately before the delete, and it is what makes the
		// assertion attributable at all. The test injects on the same trigger the
		// operator reacts to -- a replaced pod becoming Ready unblocks both -- so the
		// operator may have deleted its next candidate a fraction of a second
		// earlier. A pod already on its way out here was deleted by the operator
		// *before* us, and the overlap that follows is ours, not the operator's.
		already := tc.terminatingDataPods(t, ns, name, 3)
		t.Logf("Deleting the already-replaced pod %s mid-roll (already terminating: %v)", victim, already)
		// Armed before the delete, not after: the sampler ticks every 250 ms and must
		// not classify the very first sample of our own deletion as the operator's.
		sampler.attributeTo(victim, already)
		err := tc.kube.CoreV1().Pods(ns).Delete(context.Background(), victim, metav1.DeleteOptions{})
		require.NoError(t, err, "deleting %s", victim)
	} else {
		t.Log("The roll finished before a mid-roll victim could be picked; the sampler still covers it")
	}

	tc.waitForAllPodsImage(t, ns, name, 3, updatedImage)
	tc.waitForStatefulSetReady(t, ns, name, 3)
	tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")

	sampler.stop()

	t.Run("No two data pods terminated at once", func(t *testing.T) {
		overlaps := sampler.overlaps()
		assert.Empty(t, overlaps,
			"the operator must never delete a pod of a tier while another pod of that tier terminates")
		t.Logf("Sampled %d times, saw %d distinct terminating pods, excused %d overlaps this test caused itself",
			sampler.samples(), sampler.distinctTerminating(), sampler.excusedSamples())
	})

	t.Run("The roll finished and the data survived", func(t *testing.T) {
		newMaster := tc.findMasterPod(t, ns, name, 3)
		assert.Equal(t, "must-survive-the-chaos-delete",
			tc.valkeyExec(t, ns, newMaster, 6379, "GET", "term:key1"))
		tc.waitForConnectedReplicas(t, ns, newMaster, 6379, 2)
	})

	t.Run("The stall condition is not standing", func(t *testing.T) {
		// PodTerminationStalled is the marker for a pod that outlived its graceful
		// deadline by more than podTerminationOverrun. A healthy roll, even one with
		// a chaos delete in it, never reaches that.
		status := tc.getValkeyStatus(t, ns, name)
		assert.Equal(t, "False", conditionStatus(status, "PodTerminationStalled"),
			"a clean roll must not report a stuck termination")
	})

	t.Run("The chaos delete raised no Warning", func(t *testing.T) {
		// The new waits report through logs, the phase string and conditions only.
		// requireNoWarningEvents fails on any Warning of any reason regarding the CR
		// (ADR 0025 D7).
		tc.requireNoWarningEvents(t, ns, name)
	})
}

// conditionStatus reads one condition's status out of an unstructured CR status,
// answering "False" when the condition is absent -- a cluster that never stalled
// does not carry the condition at all, and that is the same verdict.
func conditionStatus(status map[string]interface{}, condType string) string {
	conditions, ok := status["conditions"].([]interface{})
	if !ok {
		return "False"
	}
	for _, raw := range conditions {
		cond, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if cond["type"] == condType {
			if s, ok := cond["status"].(string); ok {
				return s
			}
		}
	}
	return "False"
}

// waitForAReplacedPodMidRoll returns the name of a pod that is already on the new
// image while at least one other pod is still on the old one -- i.e. the roll is
// genuinely in flight. It returns "" when the roll completed before such a moment
// was observed, which is a legitimate outcome on a fast cluster and not a failure:
// the sampler covers the roll either way.
func (tc *testClients) waitForAReplacedPodMidRoll(t *testing.T, namespace, name string,
	replicas int, newImage, oldImage string) string {
	t.Helper()

	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		replaced, outdated := "", false
		for i := 0; i < replicas; i++ {
			podName := fmt.Sprintf("%s-%d", name, i)
			pod, err := tc.kube.CoreV1().Pods(namespace).Get(
				context.Background(), podName, metav1.GetOptions{})
			if err != nil || pod.DeletionTimestamp != nil || len(pod.Spec.Containers) == 0 {
				continue
			}
			switch pod.Spec.Containers[0].Image {
			case newImage:
				// Never the master. Deleting the pod the roll has just promoted is a
				// different scenario with a different set of legitimate outcomes; this
				// test is about an already-replaced *replica*, which is the shape a
				// chaos schedule produces most of the time and the one the first
				// version of the fix would have failed.
				if isPodReadyForTest(pod) && replaced == "" &&
					pod.Labels["vko.gtrfc.com/instanceRole"] != "master" {
					replaced = podName
				}
			case oldImage:
				outdated = true
			}
		}
		if replaced != "" && outdated {
			return replaced
		}
		if replaced != "" && !outdated {
			return "" // every pod is on the new image already
		}
		time.Sleep(500 * time.Millisecond)
	}
	return ""
}

// terminatingDataPods returns the data pods that already carry a DeletionTimestamp,
// read in one pass so the answer is a single point in time.
func (tc *testClients) terminatingDataPods(t *testing.T, namespace, name string, replicas int) []string {
	t.Helper()

	var terminating []string
	for i := 0; i < replicas; i++ {
		podName := fmt.Sprintf("%s-%d", name, i)
		pod, err := tc.kube.CoreV1().Pods(namespace).Get(
			context.Background(), podName, metav1.GetOptions{})
		if err != nil || pod.DeletionTimestamp == nil {
			continue
		}
		terminating = append(terminating, podName)
	}
	return terminating
}

func isPodReadyForTest(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// terminationSampler polls the data tier and records every moment at which more
// than one of its pods carried a DeletionTimestamp.
//
// Sampling rather than watching is deliberate: a Watch would report the same
// overlap as two separate events and leave the test to reconstruct the interval,
// while what the invariant is about is a *state* -- two pods of one tier on their
// way out at the same time.
//
// The invariant is about the **operator's** deletes, and this test deletes a pod
// itself. Those two are told apart by attributeTo: the pod the test deleted, plus
// the pods that were already terminating when it did. An overlap made only of
// those was created by the test piling onto a delete the operator had already
// issued -- the operator was holding, not deleting -- and it is not a violation.
// Any other overlap is.
//
// Attribution is needed rather than avoidable because the test injects on the very
// event that unblocks the operator: a replaced pod becoming Ready is what lets the
// roll take its next candidate, and it is also what makes a victim available here.
// Waiting for a quiet tier instead would mean waiting for a window the operator
// closes within the same second, and the injection would mostly not happen at all.
type terminationSampler struct {
	t         *testing.T
	tc        *testClients
	namespace string
	name      string
	replicas  int

	stopCh chan struct{}
	done   chan struct{}

	mu            sync.Mutex
	sampleCount   int
	seen          map[string]struct{}
	overlapEvents []string
	excusedCount  int

	// victim is the pod this test deleted, empty until it did.
	victim string
	// deletedBeforeVictim are the pods the operator had already put on their way
	// out at that moment.
	deletedBeforeVictim map[string]struct{}
}

func newTerminationSampler(t *testing.T, tc *testClients, namespace, name string, replicas int) *terminationSampler {
	return &terminationSampler{
		t: t, tc: tc, namespace: namespace, name: name, replicas: replicas,
		stopCh:              make(chan struct{}),
		done:                make(chan struct{}),
		seen:                map[string]struct{}{},
		deletedBeforeVictim: map[string]struct{}{},
	}
}

// attributeTo records the delete this test issued and what was already terminating
// when it issued it, so overlaps the test caused can be told from the ones the
// operator caused.
func (s *terminationSampler) attributeTo(victim string, alreadyTerminating []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.victim = victim
	for _, p := range alreadyTerminating {
		s.deletedBeforeVictim[p] = struct{}{}
	}
}

// causedByThisTest reports whether an observed overlap is one the test made. It is
// true only when the test's own victim is in it and every other pod was already
// terminating before the test deleted -- i.e. the operator issued its delete
// first, into a quiet tier, and this test then deleted on top of it.
//
// The caller holds s.mu.
func (s *terminationSampler) causedByThisTest(terminating []string) bool {
	if s.victim == "" {
		return false
	}
	sawVictim := false
	for _, p := range terminating {
		if p == s.victim {
			sawVictim = true
			continue
		}
		if _, before := s.deletedBeforeVictim[p]; !before {
			return false
		}
	}
	return sawVictim
}

func (s *terminationSampler) start() {
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-s.stopCh:
				return
			case <-ticker.C:
				s.sample()
			}
		}
	}()
}

func (s *terminationSampler) sample() {
	var terminating []string
	for i := 0; i < s.replicas; i++ {
		podName := fmt.Sprintf("%s-%d", s.name, i)
		pod, err := s.tc.kube.CoreV1().Pods(s.namespace).Get(
			context.Background(), podName, metav1.GetOptions{})
		if err != nil || pod.DeletionTimestamp == nil {
			continue
		}
		terminating = append(terminating, podName)
	}

	s.record(terminating)
}

// record classifies one observation. Split from sample so the classification can
// be driven directly by TestTerminationSampler_AttributesOverlapsToWhoCausedThem:
// the case that matters is the one a cluster run reaches only by chance, and a
// path that only runs when a race happens to fire is a path nobody has tested.
func (s *terminationSampler) record(terminating []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sampleCount++
	for _, p := range terminating {
		s.seen[p] = struct{}{}
	}
	if len(terminating) > 1 {
		if s.causedByThisTest(terminating) {
			s.excusedCount++
			return
		}
		s.overlapEvents = append(s.overlapEvents,
			fmt.Sprintf("%v terminated simultaneously", terminating))
	}
}

func (s *terminationSampler) stop() {
	close(s.stopCh)
	<-s.done
}

func (s *terminationSampler) overlaps() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.overlapEvents...)
}

func (s *terminationSampler) samples() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sampleCount
}

func (s *terminationSampler) distinctTerminating() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.seen)
}

// excusedSamples is how many overlaps were attributed to this test's own delete.
// Logged rather than asserted on: it is a property of the race between the test
// and the operator, not of the operator.
func (s *terminationSampler) excusedSamples() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.excusedCount
}

// TestTerminationSampler_AttributesOverlapsToWhoCausedThem drives the
// classification the cluster run reaches only by chance. It needs no cluster: the
// sampler is a state machine over pod names, and the whole point of this test is
// that the case which made CI red on 2026-08-26 is now reproduced deterministically
// instead of waiting for the race to fire again.
func TestTerminationSampler_AttributesOverlapsToWhoCausedThem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		victim      string
		already     []string
		observed    []string
		wantOverlap bool
	}{
		{
			// The measured CI failure: the operator deleted term-*-2 into a quiet
			// tier, logged that it was now waiting, and the test deleted term-*-1
			// 0.6 s later. The operator held; the test piled on.
			name:        "the operator deleted first and the test piled on",
			victim:      "term-1",
			already:     []string{"term-2"},
			observed:    []string{"term-1", "term-2"},
			wantOverlap: false,
		},
		{
			// The violation this test exists to catch: the tier was quiet when the
			// test deleted, and the operator then deleted its next candidate on top
			// of a pod that was already on its way out.
			name:        "the operator deleted on top of the injected delete",
			victim:      "term-1",
			already:     nil,
			observed:    []string{"term-1", "term-2"},
			wantOverlap: true,
		},
		{
			name:        "an overlap the test had no part in",
			victim:      "term-1",
			already:     []string{"term-2"},
			observed:    []string{"term-0", "term-2"},
			wantOverlap: true,
		},
		{
			// Three at once is never excusable: at most one of them is ours.
			name:        "a third pod joins an excusable pair",
			victim:      "term-1",
			already:     []string{"term-2"},
			observed:    []string{"term-0", "term-1", "term-2"},
			wantOverlap: true,
		},
		{
			// Before the injection there is nothing to attribute, so every overlap
			// is the operator's.
			name:        "an overlap before the injection",
			victim:      "",
			observed:    []string{"term-1", "term-2"},
			wantOverlap: true,
		},
		{
			name:        "a single terminating pod is not an overlap",
			victim:      "term-1",
			observed:    []string{"term-1"},
			wantOverlap: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTerminationSampler(t, nil, "ns", "term", 3)
			if tt.victim != "" {
				s.attributeTo(tt.victim, tt.already)
			}

			s.record(tt.observed)

			if tt.wantOverlap {
				assert.Len(t, s.overlaps(), 1, "this overlap is the operator's and must be reported")
				assert.Zero(t, s.excusedSamples())
				return
			}
			assert.Empty(t, s.overlaps())
		})
	}
}

// Excusing is per observation, not a switch that disables the assertion once the
// injection happened: a violation after an excused overlap is still reported.
func TestTerminationSampler_KeepsReportingAfterAnExcusedOverlap(t *testing.T) {
	t.Parallel()

	s := newTerminationSampler(t, nil, "ns", "term", 3)
	s.attributeTo("term-1", []string{"term-2"})

	s.record([]string{"term-1", "term-2"}) // ours
	s.record([]string{"term-1", "term-0"}) // the operator deleting on top of ours

	assert.Equal(t, 1, s.excusedSamples())
	assert.Len(t, s.overlaps(), 1)
	assert.Contains(t, s.overlaps()[0], "term-0")
}
