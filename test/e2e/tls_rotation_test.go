//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/guided-traffic/valkey-operator/test/testimages"
)

// Certificate rotation had zero end-to-end coverage until this file, and it is
// the one mechanism in the operator where every cheaper tier is structurally
// blind to the failure.
//
// The bug ADR 0030 was written for is a *process* still presenting material its
// mount no longer holds. A unit test sees the builder, an envtest sees the
// StatefulSet template, and neither runs a kubelet, a Valkey server or a TLS
// handshake. Only this tier can watch a Secret change underneath running pods and
// then check that the pods were actually replaced -- and, since 2026-08-27, that
// they came back with the token scoped to the sidecar (ADR 0012 D8 step 4) and the
// fingerprint recorded in the pod spec (ADR 0031).
//
// The rotation is forced by deleting the Secret: cert-manager owns it, notices it
// is gone and reissues within seconds, with a fresh private key because the old
// one went with the Secret. That is the real trigger the mechanism exists for, and
// it costs no hand-rolled certificate authority. The pods survive the gap --
// kubelet keeps the last payload it successfully wrote to a Secret volume -- which
// is worth exercising in itself.
//
// Every name below is a literal on purpose, as everywhere else in this tier. A
// test that read the operator's own constants would agree with a renamed carrier
// instead of catching it.

const (
	// tlsRotationRollTimeout bounds the whole roll: three pods replaced one at a
	// time, each waiting for replication to re-establish, with one controlled
	// failover.
	tlsRotationRollTimeout = 15 * time.Minute

	// tlsMaterialHashEnv is the pod-spec carrier of the TLS material fingerprint.
	tlsMaterialHashEnv = "VKO_TLS_MATERIAL_HASH"

	// tlsMaterialHashAnnotation is the superseded metadata carrier. No pod the
	// operator writes may carry it any more.
	tlsMaterialHashAnnotation = "vko.gtrfc.com/tls-material-hash"

	// saTokenMountPath is where a mounted ServiceAccount token appears.
	saTokenMountPath = "/var/run/secrets/kubernetes.io/serviceaccount" // #nosec G101 -- path, not a credential

	// instanceRoleLabel is the sidecar's single Kubernetes API write.
	instanceRoleLabel = "vko.gtrfc.com/instanceRole"
)

// recordedMaterialHash reads the fingerprint off a pod the way the operator does:
// out of the pod spec, which no principal can patch.
func recordedMaterialHash(pod *corev1.Pod) string {
	for _, c := range pod.Spec.Containers {
		for _, env := range c.Env {
			if env.Name == tlsMaterialHashEnv {
				return env.Value
			}
		}
	}
	return ""
}

// TestE2E_TLS_CertificateRotation_RollsTheFleet proves the full chain on a live
// cluster: cert-manager rewrites the Secret, the operator notices the content
// change, every data pod is replaced, the dataset survives, and the staleness
// report clears itself.
func TestE2E_TLS_CertificateRotation_RollsTheFleet(t *testing.T) {
	t.Parallel()
	tc := newTestClients(t)
	ns := "e2e-tls-rotation"
	cleanup := tc.createNamespace(t, ns)
	defer cleanup()

	const (
		name     = "tls-rotation"
		replicas = 3
	)
	secretName := fmt.Sprintf("%s-tls", name)

	valkey := buildValkeyObject(name, ns, map[string]interface{}{
		"replicas": int64(replicas),
		"image":    testimages.Default(),
		"tls":      tlsSpec(),
	})

	t.Log("Creating a 3-replica TLS cluster")
	tc.createValkey(t, ns, valkey)
	defer tc.deleteValkey(t, ns, name)

	tc.waitForCertificateReady(t, ns, secretName)
	tc.waitForSecret(t, ns, secretName)
	tc.waitForStatefulSetReady(t, ns, name, replicas)
	tc.waitForValkeyPhase(t, ns, name, "OK")

	// The pods of a freshly created TLS cluster used to carry no fingerprint at
	// all -- measured by an earlier version of this test, filed as T27: the
	// StatefulSet was created before cert-manager issued, so the first pods were
	// built from a record-less template and the presence rule exempted them from
	// every rotation forever. ADR 0030 D12 closed it by refusing to create the
	// StatefulSet until the material can be fingerprinted, so the pods a fresh
	// cluster boots are armed from birth -- which is exactly what the subtest
	// below asserts, with no help from the test.
	tc.awaitTemplateFingerprint(t, ns, name)

	t.Run("the pods of a fresh cluster are armed from birth", func(t *testing.T) {
		template := templateMaterialHash(tc.getStatefulSet(t, ns, name).Spec.Template.Spec.Containers)
		require.NotEmpty(t, template)
		for _, pod := range tc.listComponentPods(t, ns, name, "valkey") {
			assert.Equal(t, template, recordedMaterialHash(&pod),
				"pod %s was created by the StatefulSet and must already carry the record (ADR 0030 D12)", pod.Name)
		}
	})

	master := tc.masterPodName(t, ns, name)
	tc.valkeyTLSExec(t, ns, master, tlsValkeyPort, "SET", "rotation-canary", "before")

	before := tc.dataPodMaterialRecords(t, ns, name, replicas)
	beforeCert := string(tc.getSecret(t, ns, secretName).Data["tls.crt"])
	t.Logf("Recorded fingerprints before the rotation: %v", before.hashes)
	require.Len(t, before.hashes, replicas)

	t.Run("every data pod records the fingerprint in its spec, not in an annotation", func(t *testing.T) {
		for _, pod := range tc.listComponentPods(t, ns, name, "valkey") {
			assert.NotContains(t, pod.Annotations, tlsMaterialHashAnnotation,
				"pod %s must not carry the superseded metadata carrier", pod.Name)
			assert.NotEmpty(t, recordedMaterialHash(&pod),
				"pod %s must carry the record the rotation is compared against", pod.Name)
		}
	})

	t.Run("only the sidecar container holds a ServiceAccount token", func(t *testing.T) {
		// The security property of ADR 0012 D8 step 4, checked where a kubelet
		// actually built the pod rather than where the operator described it.
		for _, pod := range tc.listComponentPods(t, ns, name, "valkey") {
			require.NotNil(t, pod.Spec.AutomountServiceAccountToken, "pod %s", pod.Name)
			assert.False(t, *pod.Spec.AutomountServiceAccountToken, "pod %s", pod.Name)
			assert.Equal(t, []string{"sidecar"}, tokenHolders(pod),
				"pod %s: exactly one container may reach the Kubernetes API", pod.Name)
		}
	})

	t.Run("the sidecar still labels its pod with the scoped token", func(t *testing.T) {
		// The projection is only correct if the sidecar can authenticate with it.
		// The instanceRole label is the sidecar's single API write, so its presence
		// is the proof: a broken projection leaves every pod unlabelled and the -rw
		// Service without endpoints.
		roles := map[string]string{}
		for _, pod := range tc.listComponentPods(t, ns, name, "valkey") {
			roles[pod.Name] = pod.Labels[instanceRoleLabel]
		}
		for pod, role := range roles {
			assert.NotEmpty(t, role, "pod %s carries no instanceRole; the sidecar could not patch it", pod)
		}
		t.Logf("Roles before the rotation: %v", roles)
	})

	rotationStarted := time.Now()
	t.Log("Forcing the rotation: deleting the Secret so cert-manager reissues")
	require.NoError(t, tc.kube.CoreV1().Secrets(ns).Delete(
		context.Background(), secretName, metav1.DeleteOptions{}))
	tc.waitForReissuedSecret(t, ns, secretName, beforeCert)

	var rotated string
	t.Run("the rotation reaches the StatefulSet template", func(t *testing.T) {
		require.NotEmpty(t, before.hashes[master], "the armed pods must have given us something to compare")
		require.NoError(t, wait.PollUntilContextTimeout(context.Background(), pollInterval, testTimeout, true,
			func(_ context.Context) (bool, error) {
				sts := tc.getStatefulSet(t, ns, name)
				rotated = templateMaterialHash(sts.Spec.Template.Spec.Containers)
				return rotated != "" && rotated != before.hashes[master], nil
			}), "the operator must stamp the new fingerprint onto the pod template")
		t.Logf("Template fingerprint %s -> %s", before.hashes[master], rotated)
	})

	t.Run("every data pod is replaced", func(t *testing.T) {
		require.NoError(t, wait.PollUntilContextTimeout(context.Background(), rollingUpdatePollInterval,
			tlsRotationRollTimeout, true,
			func(_ context.Context) (bool, error) {
				now := tc.dataPodMaterialRecords(t, ns, name, replicas)
				if len(now.uids) != replicas {
					return false, nil
				}
				for pod, uid := range now.uids {
					if uid == before.uids[pod] || now.hashes[pod] != rotated {
						return false, nil
					}
				}
				return true, nil
			}), "the rotation must replace every pod that cannot reload its material")

		tc.waitForStatefulSetReady(t, ns, name, replicas)
		tc.waitForValkeyPhaseAfterRollingUpdate(t, ns, name, "OK")
	})

	t.Run("the staleness report clears itself", func(t *testing.T) {
		cond := tc.waitForValkeyCondition(t, ns, name,
			"TLSMaterialStale", "False", tlsRotationRollTimeout)
		assert.Equal(t, "TLSMaterialCurrent", cond["reason"],
			"a roll that finished must leave the level False with the current reason")
	})

	t.Run("the dataset survived the roll", func(t *testing.T) {
		// A failover-aware roll is lossless by design; a rotation is the one trigger
		// nobody asked for, so it is the one where that claim is worth measuring.
		got := tc.valkeyTLSExec(t, ns, tc.masterPodName(t, ns, name), tlsValkeyPort, "GET", "rotation-canary")
		assert.Equal(t, "before", got, "the canary written before the rotation must still be there")
	})

	t.Run("the roll emitted no Warning events", func(t *testing.T) {
		// ADR 0025 D7: a clean roll is silent, and a rotation roll is an ordinary roll.
		tc.requireNoWarningEventsSince(t, ns, name, rotationStarted)
	})
}

// awaitTemplateFingerprint waits until the operator has stamped a fingerprint onto
// the pod template, which it can only do once cert-manager has issued.
func (tc *testClients) awaitTemplateFingerprint(t *testing.T, namespace, name string) string {
	t.Helper()

	var recorded string
	err := wait.PollUntilContextTimeout(context.Background(), pollInterval, testTimeout, true,
		func(_ context.Context) (bool, error) {
			sts := tc.getStatefulSet(t, namespace, name)
			recorded = templateMaterialHash(sts.Spec.Template.Spec.Containers)
			return recorded != "", nil
		})
	require.NoError(t, err, "the pod template of %s/%s never gained a TLS material fingerprint", namespace, name)
	return recorded
}

// requireNoWarningEventsSince fails on any Warning Event about this Valkey that was
// recorded at or after since.
func (tc *testClients) requireNoWarningEventsSince(t *testing.T, namespace, name string, since time.Time) {
	t.Helper()

	events, err := tc.kube.EventsV1().Events(namespace).List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err, "listing Events for %s/%s", namespace, name)

	for i := range events.Items {
		ev := &events.Items[i]
		if ev.Regarding.Kind != "Valkey" || ev.Regarding.Name != name || ev.Type != corev1.EventTypeWarning {
			continue
		}
		if eventTime(ev).Before(since) {
			continue
		}
		t.Errorf("%s/%s raised a Warning during the rotation roll: %s: %s",
			namespace, name, ev.Reason, ev.Note)
	}
}

// eventTime returns the most recent moment an Event was recorded, preferring the
// series time a repeated Event carries.
func eventTime(ev *eventsv1.Event) time.Time {
	if ev.Series != nil {
		return ev.Series.LastObservedTime.Time
	}
	if !ev.EventTime.IsZero() {
		return ev.EventTime.Time
	}
	return ev.DeprecatedLastTimestamp.Time
}

// tokenHolders names the containers of a pod that mount anything at the path
// client-go reads its in-cluster credentials from.
func tokenHolders(pod corev1.Pod) []string {
	var holders []string
	for _, c := range append(append([]corev1.Container{}, pod.Spec.InitContainers...), pod.Spec.Containers...) {
		for _, m := range c.VolumeMounts {
			if m.MountPath == saTokenMountPath {
				holders = append(holders, c.Name)
			}
		}
	}
	return holders
}

// templateMaterialHash reads the fingerprint out of a pod template's containers.
func templateMaterialHash(containers []corev1.Container) string {
	for _, c := range containers {
		for _, env := range c.Env {
			if env.Name == tlsMaterialHashEnv {
				return env.Value
			}
		}
	}
	return ""
}

// materialRecords is one sweep over the data pods: their UIDs and the fingerprint
// each of them records.
type materialRecords struct {
	uids   map[string]types.UID
	hashes map[string]string
}

func (tc *testClients) dataPodMaterialRecords(t *testing.T, namespace, name string, replicas int) materialRecords {
	t.Helper()

	records := materialRecords{
		uids:   map[string]types.UID{},
		hashes: map[string]string{},
	}
	for i := 0; i < replicas; i++ {
		podName := fmt.Sprintf("%s-%d", name, i)
		pod, err := tc.kube.CoreV1().Pods(namespace).Get(
			context.Background(), podName, metav1.GetOptions{})
		if err != nil {
			continue
		}
		records.uids[podName] = pod.UID
		records.hashes[podName] = recordedMaterialHash(pod)
	}
	return records
}

// waitForReissuedSecret waits until the Secret exists again carrying a different
// certificate than the one recorded before the deletion.
func (tc *testClients) waitForReissuedSecret(t *testing.T, namespace, name, previousCert string) {
	t.Helper()

	err := wait.PollUntilContextTimeout(context.Background(), pollInterval, testTimeout, true,
		func(ctx context.Context) (bool, error) {
			secret, err := tc.kube.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			current := string(secret.Data["tls.crt"])
			return current != "" && current != previousCert, nil
		})
	require.NoError(t, err, "cert-manager must reissue %s/%s with a different certificate", namespace, name)
}

// masterPodName returns the data pod the operator currently labels master.
func (tc *testClients) masterPodName(t *testing.T, namespace, name string) string {
	t.Helper()

	var found string
	err := wait.PollUntilContextTimeout(context.Background(), pollInterval, testTimeout, true,
		func(_ context.Context) (bool, error) {
			for _, pod := range tc.listComponentPods(t, namespace, name, "valkey") {
				if pod.Labels[instanceRoleLabel] == "master" {
					found = pod.Name
					return true, nil
				}
			}
			return false, nil
		})
	require.NoError(t, err, "no data pod of %s/%s is labelled master", namespace, name)
	return found
}
