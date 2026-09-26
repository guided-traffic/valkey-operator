package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

// docs/adr/0032-generated-pods-run-rootless.md, the operator side: the migration
// evidence that decides whether the data template carries the ownership repair
// (D2), and the single-pod rule (D3).

// legacyHash stands for the pod-spec hash an operator before ADR 0032 stamped: any
// value the current template does not carry makes the pod outdated.
const legacyHash = "0ldh4sh0"

// rootless gives a fixture pod the evidence a pod built by this operator carries.
func rootless(pod *corev1.Pod) *corev1.Pod {
	pod.Spec.SecurityContext = &corev1.PodSecurityContext{RunAsNonRoot: ptr.To(true)}
	return pod
}

// legacy makes a fixture pod look like one an earlier operator built: no posture,
// and a pod-spec hash the current template does not carry.
func legacy(pod *corev1.Pod) *corev1.Pod {
	pod.Spec.SecurityContext = nil
	pod.Annotations[builder.AnnotationPodSpecHash] = legacyHash
	return pod
}

func persistentCluster(name string, replicas int32) (*vkov1.Valkey, *appsv1.StatefulSet) {
	v := newTestValkey(name, "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = replicas
		v.Spec.Persistence = &vkov1.PersistenceSpec{Enabled: true}
	})
	return v, stsForValkey(v)
}

// --- D2: the migration evidence -----------------------------------------------------

func TestDataOwnershipRepairNeeded(t *testing.T) {
	type podShape int
	const (
		missing podShape = iota
		legacyPod
		rootlessPod
		foreignLegacy
		rootlessNeverStarted
		rootlessNotReadyYet
	)
	for _, tc := range []struct {
		name           string
		persistence    bool
		carried        bool
		legacyTemplate bool
		pods           []podShape
		want           bool
	}{
		{"persistence off never repairs", false, false, false, []podShape{legacyPod, legacyPod, legacyPod}, false},
		{"a fresh cluster (no pods) gets no repair", true, false, false, []podShape{missing, missing, missing}, false},
		{"every pod legacy", true, false, false, []podShape{legacyPod, legacyPod, legacyPod}, true},
		{"one legacy pod is enough", true, false, false, []podShape{rootlessPod, legacyPod, rootlessPod}, true},
		{"all rootless: no repair", true, false, false, []podShape{rootlessPod, rootlessPod, rootlessPod}, false},
		{"all rootless: a carried repair goes", true, true, false, []podShape{rootlessPod, rootlessPod, rootlessPod}, false},
		{"carried, the last legacy pod gone and not yet recreated: kept", true, true, false,
			[]podShape{rootlessPod, rootlessPod, missing}, true},
		{"not carried, a pod missing: absence is no evidence", true, false, false,
			[]podShape{rootlessPod, rootlessPod, missing}, false},
		{"a foreign pod is no evidence for the repair", true, false, false,
			[]podShape{rootlessPod, foreignLegacy, rootlessPod}, false},
		{"nor proof that a carried one can go", true, true, false,
			[]podShape{rootlessPod, foreignLegacy, rootlessPod}, true},
		{"a template an earlier operator wrote is evidence on its own, a pod missing", true, false, true,
			[]podShape{missing, legacyPod, legacyPod}, true},
		{"a legacy template with no pod at all still brings the repair", true, false, true,
			[]podShape{missing, missing, missing}, true},
		{"carried, every ordinal rootless but one never got past the repair: kept", true, true, false,
			[]podShape{rootlessPod, rootlessPod, rootlessNeverStarted}, true},
		{"not carried, a rootless pod not started yet: no evidence", true, false, false,
			[]podShape{rootlessPod, rootlessPod, rootlessNeverStarted}, false},
		// Past the pre-flight is not migrated: the removal starts the second roll, and
		// that roll must not delete the replacement the first one is still waiting on.
		{"carried, the last replacement past its pre-flight but not Ready: kept", true, true, false,
			[]podShape{rootlessPod, rootlessPod, rootlessNotReadyYet}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestValkey("ev", "default", func(v *vkov1.Valkey) {
				v.Spec.Replicas = int32(len(tc.pods))
				if tc.persistence {
					v.Spec.Persistence = &vkov1.PersistenceSpec{Enabled: true}
				}
			})
			sts := stsForValkey(v)
			if tc.carried {
				builder.WithDataOwnershipRepair(sts)
			}
			if tc.legacyTemplate {
				sts.Spec.Template.Spec.SecurityContext = nil
			}
			objs := []client.Object{v, sts}
			for i, shape := range tc.pods {
				pod := podFromStsTemplate(v, sts, i)
				switch shape {
				case missing:
					continue
				case legacyPod:
					legacy(pod)
				case rootlessPod:
					rootless(pod)
				case foreignLegacy:
					legacy(pod).OwnerReferences = nil
				case rootlessNeverStarted:
					rootless(pod).Status.Conditions = nil // e.g. ImagePullBackOff before any init container
				case rootlessNotReadyYet:
					rootless(pod).Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}}
					pod.Status.InitContainerStatuses = []corev1.ContainerStatus{{
						Name:  builder.DataWritableCheckContainerName,
						State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}},
					}}
				}
				objs = append(objs, pod)
			}
			r, _ := newTestReconciler(objs...)

			assert.Equal(t, tc.want, r.dataOwnershipRepairNeeded(context.Background(), v, sts))
		})
	}
}

// TestDataOwnershipRepairNeeded_StaysWhileARollIsRecorded pins the ordering half of
// ADR 0032 D4: with every ordinal migrated, the repair still stays in the template
// while the data tier records a roll. reconcileStatefulSet runs before the rolling
// update in the same pass, so removing it under the first roll outdates every pod
// before that roll finalized, and clearStaleRollingUpdateState then discards its
// state -- on the non-Sentinel path in the middle of the topology restoration. The
// state is a gate on keeping the repair only: it is never evidence for adding one.
func TestDataOwnershipRepairNeeded_StaysWhileARollIsRecorded(t *testing.T) {
	for _, tc := range []struct {
		name    string
		carried bool
		state   string
		want    bool
	}{
		{"carried, the first roll restoring the topology: kept", true, stateRestoringTopology, true},
		{"carried, the first roll replacing the master: kept", true, stateReplacingMaster, true},
		{"carried, no roll recorded: goes", true, "", false},
		{"not carried, a roll recorded: no evidence", false, stateReplacingReplicas, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, sts := persistentCluster("ord", 3)
			if tc.state != "" {
				v.Annotations = map[string]string{annotationRollingUpdateState: tc.state}
			}
			if tc.carried {
				builder.WithDataOwnershipRepair(sts)
			}
			objs := []client.Object{v, sts}
			for i := 0; i < 3; i++ {
				objs = append(objs, rootless(podFromStsTemplate(v, sts, i)))
			}
			r, _ := newTestReconciler(objs...)

			assert.Equal(t, tc.want, r.dataOwnershipRepairNeeded(context.Background(), v, sts))
		})
	}
}

// TestReconcileStatefulSet_RepairComesAndGoesAndTheRetiredRepairRolls drives the
// StatefulSet step through a migration: legacy pods bring the repair in, a second
// pass writes nothing (no flip-flop), rootless pods take it out again, and the
// pod-spec hash on the persisted template never moves -- so neither template write
// is itself a roll. What rolls afterwards are the pods that still carry the repair
// in their spec: the second roll of ADR 0032 D2.
func TestReconcileStatefulSet_RepairComesAndGoesAndTheRetiredRepairRolls(t *testing.T) {
	v, sts := persistentCluster("mig", 3)
	pods := []*corev1.Pod{
		legacy(podFromStsTemplate(v, sts, 0)),
		legacy(podFromStsTemplate(v, sts, 1)),
		legacy(podFromStsTemplate(v, sts, 2)),
	}
	r, c := newTestReconciler(v, sts, pods[0], pods[1], pods[2])
	ctx := context.Background()
	hash := sts.Spec.Template.Annotations[builder.AnnotationPodSpecHash]

	require.NoError(t, r.reconcileStatefulSet(ctx, crGet(t, c, "mig")))
	live := getSts(t, c, "mig")
	assert.True(t, builder.HasDataOwnershipRepair(&live.Spec.Template.Spec), "legacy pods bring the repair in")
	assert.Equal(t, hash, live.Spec.Template.Annotations[builder.AnnotationPodSpecHash],
		"the repair is invisible to the pod-spec hash, so no pod becomes outdated on its account")

	rv := live.ResourceVersion
	require.NoError(t, r.reconcileStatefulSet(ctx, crGet(t, c, "mig")))
	assert.Equal(t, rv, getSts(t, c, "mig").ResourceVersion, "a second pass over the same evidence writes nothing")

	// The roll replaced every pod; each came back rootless.
	for i := range pods {
		live := &corev1.Pod{}
		require.NoError(t, c.Get(ctx, types.NamespacedName{Name: pods[i].Name, Namespace: "default"}, live))
		rootless(live)
		live.Annotations[builder.AnnotationPodSpecHash] = hash
		require.NoError(t, c.Update(ctx, live))
	}

	require.NoError(t, r.reconcileStatefulSet(ctx, crGet(t, c, "mig")))
	live = getSts(t, c, "mig")
	assert.False(t, builder.HasDataOwnershipRepair(&live.Spec.Template.Spec), "with no legacy pod left the repair goes")
	assert.Equal(t, hash, live.Spec.Template.Annotations[builder.AnnotationPodSpecHash])

	rv = live.ResourceVersion
	require.NoError(t, r.reconcileStatefulSet(ctx, crGet(t, c, "mig")))
	assert.Equal(t, rv, getSts(t, c, "mig").ResourceVersion, "and stays gone")

	// Before the pods carry it, nothing is outdated: no roll on a clean tier.
	result := r.checkAndHandleRollingUpdate(ctx, crGet(t, c, "mig"))
	require.NoError(t, result.Error)
	assert.False(t, result.NeedsRequeue, "a tier whose pods carry no repair does not roll")

	// The pods created while the template carried the repair keep it in their
	// immutable spec: root on every sandbox restart, and refused by Pod Security
	// "restricted". Now that the template is clean they are outdated for that alone,
	// and the failover-aware roll replaces them (ADR 0032 D2, second roll).
	for i := range pods {
		live := &corev1.Pod{}
		require.NoError(t, c.Get(ctx, types.NamespacedName{Name: pods[i].Name, Namespace: "default"}, live))
		live.Spec.InitContainers = append([]corev1.Container{{
			Name: builder.DataOwnershipRepairContainerName, Image: v.Spec.Image,
		}}, live.Spec.InitContainers...)
		require.NoError(t, c.Update(ctx, live))
	}
	result = r.checkAndHandleRollingUpdate(ctx, crGet(t, c, "mig"))
	require.NoError(t, result.Error)
	assert.True(t, result.NeedsRequeue, "the retired repair starts the second roll")
	deleted := 0
	for i := range pods {
		if !podExists(t, c, pods[i].Name) {
			deleted++
		}
	}
	assert.Equal(t, 1, deleted, "one pod at a time, like every roll")
	assert.False(t, builder.HasDataOwnershipRepair(&getSts(t, c, "mig").Spec.Template.Spec),
		"a missing pod during the second roll is no evidence: the repair does not come back")
	require.NoError(t, r.reconcileStatefulSet(ctx, crGet(t, c, "mig")))
	assert.False(t, builder.HasDataOwnershipRepair(&getSts(t, c, "mig").Spec.Template.Spec))
}

func TestPodCarriesRetiredRepair(t *testing.T) {
	v, sts := persistentCluster("retired", 1)
	with := podFromStsTemplate(v, sts, 0)
	with.Spec.InitContainers = append([]corev1.Container{{Name: builder.DataOwnershipRepairContainerName}},
		with.Spec.InitContainers...)
	without := podFromStsTemplate(v, sts, 0)

	assert.True(t, podCarriesRetiredRepair(with, sts), "pod carries it, template does not: outdated")
	assert.False(t, podCarriesRetiredRepair(without, sts))

	carrying := sts.DeepCopy()
	builder.WithDataOwnershipRepair(carrying)
	assert.False(t, podCarriesRetiredRepair(with, carrying), "during the migration it is not outdated")
}

// A persistent single pod migrated with the repair is replaced once more when the
// repair leaves the template -- the second restart the decision accepted.
func TestHandleStandaloneRollingUpdate_ReplacesAPodCarryingTheRetiredRepair(t *testing.T) {
	v, sts := persistentCluster("second", 1)
	pod := rootless(podFromStsTemplate(v, sts, 0))
	pod.Spec.InitContainers = append([]corev1.Container{{Name: builder.DataOwnershipRepairContainerName}},
		pod.Spec.InitContainers...)
	r, c := newTestReconciler(v, sts, pod)

	result := r.handleStandaloneRollingUpdate(context.Background(), crGet(t, c, "second"), sts)
	require.NoError(t, result.Error)
	assert.False(t, podExists(t, c, "second-0"))
}

func getSts(t *testing.T, c client.Client, name string) *appsv1.StatefulSet {
	t.Helper()
	sts := &appsv1.StatefulSet{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "default"}, sts))
	return sts
}

// --- D3: single-pod clusters ---------------------------------------------------------

const (
	currentValkeyImage = "valkey/valkey:9.0"
	olderSidecarImage  = "ghcr.io/guided-traffic/valkey-operator:previous"
	desiredSidecar     = "ghcr.io/guided-traffic/valkey-operator:test"
)

func singlePod(rootlessPod, sidecarDrift, valkeyDrift bool) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "one-0", Namespace: "default"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: builder.ValkeyContainerName, Image: currentValkeyImage},
			{Name: builder.SidecarContainerName, Image: desiredSidecar},
		}},
	}
	if sidecarDrift {
		pod.Spec.Containers[1].Image = olderSidecarImage
	}
	if valkeyDrift {
		pod.Spec.Containers[0].Image = "valkey/valkey:8.0"
	}
	if rootlessPod {
		rootless(pod)
	}
	return pod
}

func TestSinglePodDeferral(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		replicas                    int32
		persistent                  bool
		rootless, sidecar, valkey   bool
		otherDrift                  string
		wantRoot, wantSidecarPodSet bool
	}{
		{"rootless, sidecar-only drift: the sidecar deferral as before", 1, false, true, true, false, "", false, true},
		{"rootless, no image drift: replaced", 1, false, true, false, false, "", false, false},
		{"root, persistent, sidecar bump: NOT sidecar-only, replaced", 1, true, false, true, false, "", false, false},
		{"root, persistent, posture only: replaced", 1, true, false, false, false, "", false, false},
		{"root, not persistent, sidecar bump: deferred on both accounts", 1, false, false, true, false, "", true, true},
		{"root, not persistent, posture only: deferred", 1, false, false, false, false, "", true, false},
		{"root, not persistent, the CR author changed the image: replaced", 1, false, false, true, true, "", false, false},
		{"root, not persistent, rotated TLS material: replaced (ADR 0030)", 1, false, false, false, false, "tls", false, false},
		{"root, not persistent, changed configuration: replaced", 1, false, false, false, false, "config", false, false},
		{"multi-replica: never deferred, the roll is failover-aware", 3, false, false, true, false, "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := newTestValkey("one", "default", func(v *vkov1.Valkey) {
				v.Spec.Replicas = tc.replicas
				if tc.persistent {
					v.Spec.Persistence = &vkov1.PersistenceSpec{Enabled: true}
				}
			})
			sts := singlePodSts(v)
			pod := singlePod(tc.rootless, tc.sidecar, tc.valkey)
			switch tc.otherDrift {
			case "tls":
				sts.Spec.Template.Annotations[builder.AnnotationTLSMaterialHash] = "new-material"
				pod.Annotations = map[string]string{builder.AnnotationTLSMaterialHash: "old-material"}
			case "config":
				pod.Annotations = map[string]string{builder.AnnotationConfigHash: "old-config"}
			}
			root, sidecar := singlePodDeferral(v, sts, pod)
			assert.Equal(t, tc.wantRoot, root == pod.Name, "root deferral")
			assert.Equal(t, tc.wantSidecarPodSet, sidecar == pod.Name, "sidecar deferral")
			if !tc.wantRoot {
				assert.Empty(t, root)
			}
			if !tc.wantSidecarPodSet {
				assert.Empty(t, sidecar)
			}
		})
	}
}

// singlePodSts is the persisted StatefulSet singlePodDeferral reads, with the
// images singlePod compares against.
func singlePodSts(v *vkov1.Valkey) *appsv1.StatefulSet {
	sts := stsForValkey(v)
	for i := range sts.Spec.Template.Spec.Containers {
		switch sts.Spec.Template.Spec.Containers[i].Name {
		case builder.ValkeyContainerName:
			sts.Spec.Template.Spec.Containers[i].Image = currentValkeyImage
		case builder.SidecarContainerName:
			sts.Spec.Template.Spec.Containers[i].Image = desiredSidecar
		}
	}
	return sts
}

// The CR asks for persistence, the operator refused to write it (volumeClaimTemplates
// are immutable, ADR 0023), and the pod still runs on an emptyDir. Reading the CR
// here deleted the only pod together with its dataset.
//
// Mutation check: deciding persistence from v.IsPersistenceEnabled() in
// singlePodDeferral replaces the pod and fails the assertion.
func TestSinglePodDeferral_ReadsPersistenceOffThePersistedStatefulSet(t *testing.T) {
	ephemeral := newTestValkey("toggle", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 1 })
	sts := singlePodSts(ephemeral)
	require.Empty(t, sts.Spec.VolumeClaimTemplates)

	toggled := ephemeral.DeepCopy()
	toggled.Spec.Persistence = &vkov1.PersistenceSpec{Enabled: true}
	pod := singlePod(false, false, false)

	root, _ := singlePodDeferral(toggled, sts, pod)
	assert.Equal(t, pod.Name, root, "the pods were built without a volume: still deferred")
}

// singlePodCluster is a spec.replicas: 1 cluster whose only pod an earlier operator
// built: root, an older sidecar image, an older pod-spec hash -- the shape every
// such cluster has at the first reconcile after the upgrade on the Helm path.
func singlePodCluster(t *testing.T, name string, persistent bool) (*ValkeyReconciler, client.Client, *vkov1.Valkey) {
	t.Helper()
	v := newTestValkey(name, "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 1
		if persistent {
			v.Spec.Persistence = &vkov1.PersistenceSpec{Enabled: true}
		}
	})
	sts := stsForValkey(v)
	pod := legacy(podFromStsTemplate(v, sts, 0))
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == builder.SidecarContainerName {
			pod.Spec.Containers[i].Image = olderSidecarImage
		}
	}
	r, c := newTestReconciler(v, sts, pod)
	return r, c, crGet(t, c, name)
}

func podSecurityPendingCondition(t *testing.T, c client.Client, name string) *metav1.Condition {
	t.Helper()
	return apimeta.FindStatusCondition(crGet(t, c, name).Status.Conditions, vkov1.ConditionTypePodSecurityUpdatePending)
}

// The regression the image-only test would have shipped: a sidecar bump plus the
// posture change on a persistent single pod is not sidecar-only. The pod is
// replaced -- one restart, the data on its volume.
//
// Mutation check: returning isSidecarOnlyChange's verdict for a root pod in
// singlePodDeferral keeps one-0 and fails the first assertion.
func TestHandleStandaloneRollingUpdate_ReplacesAPersistentRootPod(t *testing.T) {
	r, c, v := singlePodCluster(t, "persist", true)
	sts := getSts(t, c, "persist")

	result := r.handleStandaloneRollingUpdate(context.Background(), v, sts)

	require.NoError(t, result.Error)
	assert.False(t, podExists(t, c, "persist-0"), "a persistent root pod is replaced at the upgrade")
	assert.Empty(t, result.rootDeferredPod)
}

// A non-persistent single pod is never discarded by an operator upgrade: it keeps
// running as root, and the condition says so and names it. Once it restarts for any
// other reason and comes back rootless, the condition is retracted.
func TestCheckAndHandleRollingUpdate_DefersANonPersistentRootPodAndReportsIt(t *testing.T) {
	r, c, v := singlePodCluster(t, "ephemeral", false)
	ctx := context.Background()

	result := r.checkAndHandleRollingUpdate(ctx, v)
	require.NoError(t, result.Error)
	assert.False(t, result.NeedsRequeue)
	assert.True(t, podExists(t, c, "ephemeral-0"), "replacing it would discard the dataset")

	cond := podSecurityPendingCondition(t, c, "ephemeral")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, vkov1.ReasonPodRunsAsRoot, cond.Reason)
	assert.Contains(t, cond.Message, "ephemeral-0")
	sidecar := apimeta.FindStatusCondition(crGet(t, c, "ephemeral").Status.Conditions,
		vkov1.ConditionTypeSidecarUpdatePending)
	require.NotNil(t, sidecar, "the sidecar drift is deferred with it and still reported")
	assert.Equal(t, metav1.ConditionTrue, sidecar.Status)

	// The pod restarts for another reason and comes back from the current template.
	sts := getSts(t, c, "ephemeral")
	live := &corev1.Pod{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: "ephemeral-0", Namespace: "default"}, live))
	require.NoError(t, c.Delete(ctx, live))
	fresh := rootless(podFromStsTemplate(crGet(t, c, "ephemeral"), sts, 0))
	require.NoError(t, c.Create(ctx, fresh))

	result = r.checkAndHandleRollingUpdate(ctx, crGet(t, c, "ephemeral"))
	require.NoError(t, result.Error)
	cond = podSecurityPendingCondition(t, c, "ephemeral")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, vkov1.ReasonPodSecurityUpdateApplied, cond.Reason)
}

// Upgrade neutrality of the condition: a cluster with nothing deferred never gains it.
func TestCheckAndHandleRollingUpdate_NoPodSecurityConditionWithoutADeferral(t *testing.T) {
	for _, persistent := range []bool{true, false} {
		v := newTestValkey("clean", "default", func(v *vkov1.Valkey) {
			v.Spec.Replicas = 1
			if persistent {
				v.Spec.Persistence = &vkov1.PersistenceSpec{Enabled: true}
			}
		})
		sts := stsForValkey(v)
		r, c := newTestReconciler(v, sts, rootless(podFromStsTemplate(v, sts, 0)))

		result := r.checkAndHandleRollingUpdate(context.Background(), crGet(t, c, "clean"))
		require.NoError(t, result.Error)
		assert.Nil(t, podSecurityPendingCondition(t, c, "clean"))
	}

	// Nor does a persistent root pod, which is replaced rather than deferred.
	r, c, v := singlePodCluster(t, "replaced", true)
	result := r.checkAndHandleRollingUpdate(context.Background(), v)
	require.NoError(t, result.Error)
	assert.Nil(t, podSecurityPendingCondition(t, c, "replaced"))
}

// TestCompletedRoll_AsksForThePassThatRemovesTheRepair: the repair stays while a
// roll is recorded, so it can only leave in the pass after the completion -- and a
// completing pass schedules none (the CR watch is generation-gated, there is no Pod
// watch). Measured on Kind before this recheck existed: the persistent tiers kept
// the repair after their first roll and the second roll never started.
//
// Revert check: deleting the requestRecheck branch of finishDataRoll (the
// completion step dispatchDataRollingUpdate calls) fails the first row.
func TestCompletedRoll_AsksForThePassThatRemovesTheRepair(t *testing.T) {
	for _, tc := range []struct {
		name        string
		carried     bool
		wantRecheck bool
	}{
		{"template still carries the repair: a follow-up pass is requested", true, true},
		{"no repair: the completing pass asks for nothing", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, sts := persistentCluster("fin", 1)
			v.Annotations = map[string]string{annotationRollingUpdateState: stateReplacingReplicas}
			if tc.carried {
				builder.WithDataOwnershipRepair(sts)
			}
			pod := rootless(podFromStsTemplate(v, sts, 0))
			r, c := newTestReconciler(v, sts, pod)
			state := &passState{}
			ctx := withPassState(context.Background(), state)

			result := r.checkAndHandleRollingUpdate(ctx, crGet(t, c, "fin"))
			require.NoError(t, result.Error)
			require.True(t, result.Completed, "premise: the recorded roll completes in this pass")
			assert.Empty(t, crGet(t, c, "fin").Annotations[annotationRollingUpdateState])
			if tc.wantRecheck {
				assert.Equal(t, rollingUpdateRequeueDelay, state.interval())
			} else {
				assert.Zero(t, state.interval())
			}
		})
	}
}
