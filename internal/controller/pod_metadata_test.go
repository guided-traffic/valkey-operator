package controller

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// setPodMetadataRecord puts the pod metadata record on the named container of a pod,
// or removes it for an empty value -- the pod an operator before the record built.
func setPodMetadataRecord(pod *corev1.Pod, container, value string) {
	for i := range pod.Spec.Containers {
		c := &pod.Spec.Containers[i]
		if c.Name != container {
			continue
		}
		var env []corev1.EnvVar
		for _, e := range c.Env {
			if e.Name != builder.PodMetadataHashEnvName {
				env = append(env, e)
			}
		}
		if value != "" {
			env = append(env, corev1.EnvVar{Name: builder.PodMetadataHashEnvName, Value: value})
		}
		c.Env = env
	}
}

// A podLabels or podAnnotations change moves the data template's metadata record,
// and the OnDelete StatefulSet applies template metadata to no running pod. The data
// tier keeps the presence rule: a pod an operator before the record built is not
// outdated for it, or the upgrade alone would replace the only pod of a
// non-persistent single-replica cluster with its dataset (ADR 0007 D2, D6).
//
// Mutation check: dropping the podMetadataHashChanged term of podOutdated fails the
// "changed" row; treating a missing pod record as a difference fails the "built
// before the record" row.
func TestPodOutdated_PodMetadataRecord(t *testing.T) {
	for _, tc := range []struct {
		name         string
		podRecord    string
		dropDesired  bool
		wantOutdated bool
	}{
		{"record current", "current", false, false},
		{"record changed: the CR author changed the maps", "00000000", false, true},
		{"pod built before the record: not measured", "", false, false},
		{"template without a record: cannot tell (ADR 0007 D3)", "00000000", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestValkey("meta", "default", func(v *vkov1.Valkey) {
				v.Spec.Replicas = 3
				v.Spec.PodLabels = map[string]string{"team": "data"}
			})
			sts := stsForValkey(v)
			desired := builder.DataPodMetadataHash(v)
			if !tc.dropDesired {
				builder.StampPodMetadataHash(sts, builder.SidecarContainerName, desired)
			}
			pod := podFromStsTemplate(v, sts, 0)
			record := tc.podRecord
			if record == "current" {
				record = desired
			}
			setPodMetadataRecord(pod, builder.SidecarContainerName, record)

			assert.Equal(t, tc.wantOutdated, podOutdated(pod, sts))
		})
	}
}

// The Sentinel tier drops the presence rule (ADR 0007 D2, decided 2026-10-05): its
// template carries the record from the release that introduced it, and nothing
// else rolls that tier on an operator upgrade (ADR 0005 D11). With the presence
// rule every Sentinel StatefulSet kept a revision no pod runs, and a
// spec.sentinel.podLabels change rolled none of the pods from before the record.
//
// Mutation check: giving sentinelPodMetadataOutdated the presence rule fails the
// "built before the record" row; dropping its call from sentinelPodNeedsUpdate fails
// the "changed" row as well.
func TestSentinelPodNeedsUpdate_PodMetadataRecord(t *testing.T) {
	for _, tc := range []struct {
		name         string
		podRecord    string
		dropDesired  bool
		wantOutdated bool
	}{
		{"record current", "current", false, false},
		{"record changed: the CR author changed the maps", "00000000", false, true},
		{"pod built before the record: outdated", "", false, true},
		{"template without a record: cannot tell (ADR 0007 D3)", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestValkey("meta", "default", func(v *vkov1.Valkey) {
				v.Spec.Replicas = 3
				v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3,
					PodLabels: map[string]string{"team": "sentinel"}}
			})
			built := builder.BuildSentinelStatefulSet(v)
			desired := builder.SentinelPodMetadataHash(v)
			if !tc.dropDesired {
				builder.StampPodMetadataHash(built, builder.SentinelContainerName, desired)
			}
			tmpl := built.Spec.Template
			pod := &corev1.Pod{Spec: *tmpl.Spec.DeepCopy()}
			pod.Annotations = map[string]string{}
			for k, val := range tmpl.Annotations {
				pod.Annotations[k] = val
			}
			record := tc.podRecord
			if record == "current" {
				record = desired
			}
			setPodMetadataRecord(pod, builder.SentinelContainerName, record)

			assert.Equal(t, tc.wantOutdated, sentinelPodNeedsUpdate(pod, tmpl))
		})
	}
}

// The same rule seen from the Sentinel roll: three Ready Sentinels on the current
// image that carry no metadata record are rolled, one per pass under the quorum
// guard; the same tier carrying the current record is left alone.
func TestCheckAndHandleSentinelRollingUpdate_PodMetadataRecord(t *testing.T) {
	for _, tc := range []struct {
		name        string
		podRecord   string
		wantDeleted int
	}{
		{"pods without the record: one replaced", "", 1},
		{"pods with a changed record: one replaced", "old", 1},
		{"pods with the current record: none replaced", "current", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestValkey("ha", "default", func(v *vkov1.Valkey) {
				v.Spec.Replicas = 3
				v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
			})
			sts := buildTestSentinelSts(v)
			builder.StampPodMetadataHash(sts, builder.SentinelContainerName, "current")
			pods := make([]*corev1.Pod, 3)
			for i := range pods {
				pods[i] = createSentinelPod(v, i, sentinelTestNewImage, true)
				setPodMetadataRecord(pods[i], builder.SentinelContainerName, tc.podRecord)
			}
			r, c := newTestReconciler(v, sts, pods[0], pods[1], pods[2])

			result := r.checkAndHandleSentinelRollingUpdate(context.Background(), v)
			require.NoError(t, result.Error)

			stsName := common.StatefulSetName(v, common.ComponentSentinel)
			deleted := 0
			for i := 0; i < 3; i++ {
				err := c.Get(context.Background(), types.NamespacedName{
					Name: fmt.Sprintf("%s-%d", stsName, i), Namespace: "default"}, &corev1.Pod{})
				if apierrors.IsNotFound(err) {
					deleted++
				} else {
					require.NoError(t, err)
				}
			}
			assert.Equal(t, tc.wantDeleted, deleted)
		})
	}
}

// Both StatefulSet steps write the record of their own tier onto their carrier
// container, from the tier's own maps.
//
// Mutation check: dropping either StampPodMetadataHash call in valkey_controller.go
// fails the matching assertion.
func TestReconcileStatefulSets_StampThePodMetadataRecordOfTheirTier(t *testing.T) {
	v := newTestValkey("meta", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.PodLabels = map[string]string{"team": "data"}
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3,
			PodLabels: map[string]string{"team": "sentinel"}}
	})
	r, c := newTestReconciler(v)
	require.NoError(t, r.reconcileStatefulSet(context.Background(), v))
	require.NoError(t, r.reconcileSentinelStatefulSet(context.Background(), v))

	data := storedSts(t, c, v.Name, v.Namespace)
	sentinel := storedSts(t, c, common.StatefulSetName(v, common.ComponentSentinel), v.Namespace)
	assert.Equal(t, builder.DataPodMetadataHash(v), recordOn(data.Spec.Template.Spec, builder.SidecarContainerName))
	assert.Equal(t, builder.SentinelPodMetadataHash(v),
		recordOn(sentinel.Spec.Template.Spec, builder.SentinelContainerName))
	assert.NotEqual(t, builder.DataPodMetadataHash(v), builder.SentinelPodMetadataHash(v))
}

func recordOn(spec corev1.PodSpec, container string) string {
	for _, c := range spec.Containers {
		if c.Name != container {
			continue
		}
		for _, e := range c.Env {
			if e.Name == builder.PodMetadataHashEnvName {
				return e.Value
			}
		}
	}
	return ""
}

// A metadata change that lands while a rootless single pod still runs an old sidecar
// is not a sidecar-only delta (sidecarOnlyDelta, ADR 0007 D6): the sidecar every
// operator upgrade leaves on such a pod would otherwise hold the CR author's label
// change for as long as the pod lived. A pod built before the record is not measured,
// so the upgrade that introduces the record defers it exactly as before.
//
// Mutation check: dropping the podMetadataHashChanged term of sidecarOnlyDelta defers
// the two "changed" rows.
func TestSinglePodDeferral_AMetadataChangeIsNotSidecarOnly(t *testing.T) {
	for _, tc := range []struct {
		name        string
		persistent  bool
		podRecord   string
		wantSidecar bool
	}{
		{"metadata changed, not persistent: replaced", false, "00000000", false},
		{"metadata changed, persistent: replaced", true, "00000000", false},
		{"pod built before the record: the sidecar-only deferral stands", false, "", true},
		{"metadata unchanged: the sidecar-only deferral stands", false, "current", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestValkey("meta", "default", func(v *vkov1.Valkey) {
				v.Spec.PodLabels = map[string]string{"team": "data"}
				if tc.persistent {
					v.Spec.Persistence = &vkov1.PersistenceSpec{Enabled: true}
				}
			})
			sts := stsForValkey(v)
			builder.StampPodMetadataHash(sts, builder.SidecarContainerName, builder.DataPodMetadataHash(v))
			pod := oldSidecarPod(v, sts, tc.podRecord)

			security, sidecar := singlePodDeferral(v, sts, pod)

			assert.Empty(t, security.pod, "a rootless pod holds no security repair")
			if tc.wantSidecar {
				assert.Equal(t, pod.Name, sidecar)
			} else {
				assert.Empty(t, sidecar, "the pod is replaced")
			}
		})
	}
}

// End to end through the single-pod handler: the only data pod of a non-persistent
// cluster, rootless, with an old sidecar and a changed metadata record, is deleted.
func TestHandleStandaloneRollingUpdate_AMetadataChangeReplacesTheSinglePod(t *testing.T) {
	v := newTestValkey("meta", "default", func(v *vkov1.Valkey) {
		v.Spec.PodLabels = map[string]string{"team": "data"}
	})
	sts := stsForValkey(v)
	builder.StampPodMetadataHash(sts, builder.SidecarContainerName, builder.DataPodMetadataHash(v))
	pod := oldSidecarPod(v, sts, "00000000")
	r, c := newTestReconciler(v, sts, pod)

	result := r.handleStandaloneRollingUpdate(context.Background(), crGet(t, c, "meta"), sts)
	require.NoError(t, result.Error)
	assert.False(t, podExists(t, c, pod.Name), "the metadata change replaces the pod")
}

// oldSidecarPod is the only data pod as an earlier operator left it -- the sidecar
// image behind the template -- carrying record ("current" for the template's, empty
// for none).
func oldSidecarPod(v *vkov1.Valkey, sts *appsv1.StatefulSet, record string) *corev1.Pod {
	pod := podFromStsTemplate(v, sts, 0)
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == builder.SidecarContainerName {
			pod.Spec.Containers[i].Image = olderSidecarImage
		}
	}
	if record == "current" {
		record = builder.DataPodMetadataHash(v)
	}
	setPodMetadataRecord(pod, builder.SidecarContainerName, record)
	return pod
}
