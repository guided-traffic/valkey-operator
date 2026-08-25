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
// two pods of the data tier carry a DeletionTimestamp at the same time.
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
		t.Logf("Deleting the already-replaced pod %s mid-roll", victim)
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
		t.Logf("Sampled %d times, saw %d distinct terminating pods",
			sampler.samples(), sampler.distinctTerminating())
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
}

func newTerminationSampler(t *testing.T, tc *testClients, namespace, name string, replicas int) *terminationSampler {
	return &terminationSampler{
		t: t, tc: tc, namespace: namespace, name: name, replicas: replicas,
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
		seen:   map[string]struct{}{},
	}
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

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sampleCount++
	for _, p := range terminating {
		s.seen[p] = struct{}{}
	}
	if len(terminating) > 1 {
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
