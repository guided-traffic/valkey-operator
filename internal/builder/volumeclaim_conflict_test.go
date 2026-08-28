package builder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// VolumeClaimTemplatesConflict decides whether the operator submits a StatefulSet
// update at all, so both of its failure directions are expensive and neither is
// self-correcting:
//
//   - a difference it misses is a rejected write on every pass, or worse, an
//     accepted one that leaves the pod template mounting an emptyDir while the
//     claims stay on the object;
//   - a difference it invents is a permanent StorageSpecNotApplied condition and a
//     Warning Event on a cluster whose storage is perfectly fine.
//
// The comparison is therefore a whitelist over the fields the builder itself
// decides, and the tests below drive it from real BuildStatefulSet output wherever
// the CR can express the difference, so a row cannot pin a shape the builder never
// produces. docs/adr/0023-volume-claim-templates-are-immutable.md D1.

// persistentFixtureValkey returns a CR with persistence enabled, which is the only
// shape for which the builder emits a claim at all. Callers override the
// persistence block to produce the second side of a comparison.
func persistentFixtureValkey(opts ...func(*vkov1.Valkey)) *vkov1.Valkey {
	base := func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Persistence = &vkov1.PersistenceSpec{
			Enabled: true,
			Mode:    vkov1.PersistenceModeRDB,
			Size:    resource.MustParse("1Gi"),
		}
	}
	return newTestValkey("test", append([]func(*vkov1.Valkey){base}, opts...)...)
}

// ephemeralFixtureValkey returns the same CR with persistence off: no claim, and an
// emptyDir named "data" in the pod template instead.
func ephemeralFixtureValkey() *vkov1.Valkey {
	return newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
	})
}

// soleClaim returns the one claim a persistent fixture produces, failing the test
// when the fixture carries a different number: a mutation applied to a claim that
// does not exist would assert nothing.
func soleClaim(t *testing.T, sts *appsv1.StatefulSet) *corev1.PersistentVolumeClaim {
	t.Helper()
	require.Len(t, sts.Spec.VolumeClaimTemplates, 1,
		"the persistent fixture must produce exactly one claim")
	return &sts.Spec.VolumeClaimTemplates[0]
}

// TestVolumeClaimTemplatesConflict_BuiltFromCRPairs drives the comparison with both
// sides produced by BuildStatefulSet, one CR each. Every row is a difference a user
// can actually create by editing spec.persistence, plus the two agreement controls
// without which a comparison that always answered "conflict" would still pass the
// conflict rows (ADR 0017 D11).
func TestVolumeClaimTemplatesConflict_BuiltFromCRPairs(t *testing.T) {
	tests := []struct {
		name       string
		desiredCR  *vkov1.Valkey
		currentCR  *vkov1.Valkey
		premise    func(t *testing.T, desired, current *appsv1.StatefulSet)
		want       VolumeClaimConflictKind
		wantDetail []string
		why        string
	}{
		{
			name:      "same persistent CR on both sides",
			desiredCR: persistentFixtureValkey(),
			currentCR: persistentFixtureValkey(),
			premise: func(t *testing.T, desired, current *appsv1.StatefulSet) {
				require.NotEmpty(t, desired.Spec.VolumeClaimTemplates,
					"this row is only a control if the fixture really carries a claim")
				require.NotEmpty(t, current.Spec.VolumeClaimTemplates)
			},
			want: VolumeClaimsAgree,
			why:  "a cluster whose storage never changed must never be reported as stuck",
		},
		{
			name:      "same non-persistent CR on both sides",
			desiredCR: ephemeralFixtureValkey(),
			currentCR: ephemeralFixtureValkey(),
			premise: func(t *testing.T, desired, current *appsv1.StatefulSet) {
				require.Empty(t, desired.Spec.VolumeClaimTemplates,
					"persistence is off, so the builder must emit no claim")
				require.Empty(t, current.Spec.VolumeClaimTemplates)
			},
			want: VolumeClaimsAgree,
			why:  "empty against empty is the case every non-persistent cluster hits on every pass",
		},
		{
			name:      "persistence enabled on a StatefulSet created without it",
			desiredCR: persistentFixtureValkey(),
			currentCR: ephemeralFixtureValkey(),
			premise: func(t *testing.T, desired, current *appsv1.StatefulSet) {
				require.Len(t, desired.Spec.VolumeClaimTemplates, 1)
				require.Empty(t, current.Spec.VolumeClaimTemplates)
				require.True(t, hasEmptyDirVolume(current, DataVolumeName),
					"the live object must carry the emptyDir the claim would replace, "+
						"or this row does not reproduce the toggle")
			},
			want:       VolumeClaimsStructuralConflict,
			wantDetail: []string{DataVolumeName, "does not have"},
			why:        "the API server rejects the added claim on every pass; only a recreate applies it",
		},
		{
			name:      "persistence disabled on a StatefulSet created with it",
			desiredCR: ephemeralFixtureValkey(),
			currentCR: persistentFixtureValkey(),
			premise: func(t *testing.T, desired, current *appsv1.StatefulSet) {
				require.Empty(t, desired.Spec.VolumeClaimTemplates)
				require.Len(t, current.Spec.VolumeClaimTemplates, 1)
				require.True(t, hasEmptyDirVolume(desired, DataVolumeName),
					"the desired pod template must mount the emptyDir that would land "+
						"next to the surviving claim")
			},
			want:       VolumeClaimsStructuralConflict,
			wantDetail: []string{DataVolumeName, "no longer asks for"},
			why: "this direction is accepted by the API server and is the worse one: the template " +
				"gains an emptyDir while the claim stays, so it must be refused before the write",
		},
		{
			// The written form has to survive parsing for this row to mean anything,
			// and most do not: 1024Mi and 1048576Ki both come out of resource.ParseQuantity
			// as a Quantity byte-identical to 1Gi, so they could not distinguish a
			// semantic comparison from a struct one. A plain byte count does survive —
			// it parses as DecimalSI and keeps its own rendering — and it is what a
			// user who wrote the number out gets stored on the live claim.
			name:      "same size written as a plain byte count",
			desiredCR: persistentFixtureValkey(),
			currentCR: persistentFixtureValkey(func(v *vkov1.Valkey) {
				v.Spec.Persistence.Size = resource.MustParse("1073741824")
			}),
			premise: func(t *testing.T, desired, current *appsv1.StatefulSet) {
				d := soleClaim(t, desired).Spec.Resources.Requests[corev1.ResourceStorage]
				c := soleClaim(t, current).Spec.Resources.Requests[corev1.ResourceStorage]
				require.NotEqual(t, d.String(), c.String(),
					"the two sides must be written differently, or the row proves nothing "+
						"about semantic comparison")
				require.NotEqual(t, d, c,
					"the two Quantity values must differ structurally, or a DeepEqual would "+
						"have agreed as well and the row guards nothing")
			},
			want: VolumeClaimsAgree,
			why: "1Gi and 1073741824 are the same request; comparing the rendered value or the " +
				"Quantity struct would report a conflict no write can ever settle",
		},
		{
			name: "size raised",
			desiredCR: persistentFixtureValkey(func(v *vkov1.Valkey) {
				v.Spec.Persistence.Size = resource.MustParse("5Gi")
			}),
			currentCR:  persistentFixtureValkey(),
			want:       VolumeClaimsParameterConflict,
			wantDetail: []string{DataVolumeName, "5Gi", "1Gi"},
			why: "a resize is unwritable but leaves the pod template writable, so the pass " +
				"continues and only reports it",
		},
		{
			name: "storage class named where the live claim uses the cluster default",
			desiredCR: persistentFixtureValkey(func(v *vkov1.Valkey) {
				v.Spec.Persistence.StorageClass = "fast-ssd"
			}),
			currentCR: persistentFixtureValkey(),
			premise: func(t *testing.T, desired, current *appsv1.StatefulSet) {
				require.NotNil(t, soleClaim(t, desired).Spec.StorageClassName)
				require.Nil(t, soleClaim(t, current).Spec.StorageClassName,
					"an empty spec.persistence.storageClass must reach the claim as nil")
			},
			want:       VolumeClaimsParameterConflict,
			wantDetail: []string{DataVolumeName, "fast-ssd", "(cluster default)"},
			why:        "nil is a value like any other here, and this direction is the common one",
		},
		{
			name:      "cluster default requested where the live claim names a class",
			desiredCR: persistentFixtureValkey(),
			currentCR: persistentFixtureValkey(func(v *vkov1.Valkey) {
				v.Spec.Persistence.StorageClass = "fast-ssd"
			}),
			want:       VolumeClaimsParameterConflict,
			wantDetail: []string{DataVolumeName, "fast-ssd", "(cluster default)"},
			why: "the nil-aware comparison has to work in both directions, or removing the class " +
				"from the CR would silently read as agreement",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			desired := BuildStatefulSet(tt.desiredCR, testOperatorImage)
			current := BuildStatefulSet(tt.currentCR, testOperatorImage)
			if tt.premise != nil {
				tt.premise(t, desired, current)
			}

			kind, detail := VolumeClaimTemplatesConflict(desired, current)

			assert.Equal(t, tt.want, kind, tt.why)
			for _, want := range tt.wantDetail {
				assert.Contains(t, detail, want,
					"the detail is the Event and condition message; it has to name the difference")
			}
			if tt.want == VolumeClaimsAgree {
				assert.Empty(t, detail, "agreement carries no message")
			}
		})
	}
}

// hasEmptyDirVolume reports whether the pod template carries an emptyDir under the
// given name. The structural rows use it to prove they reproduce the persistence
// toggle rather than an arbitrary claim-set difference.
func hasEmptyDirVolume(sts *appsv1.StatefulSet, name string) bool {
	for _, vol := range sts.Spec.Template.Spec.Volumes {
		if vol.Name == name && vol.EmptyDir != nil {
			return true
		}
	}
	return false
}

// TestVolumeClaimTemplatesConflict_LiveClaimShapes covers the differences the CR
// cannot express: access modes, which the builder hardcodes, and the fields the API
// server writes onto the stored claim on its own. Both sides start as real builder
// output and the live side is then mutated the way the cluster would have mutated it.
func TestVolumeClaimTemplatesConflict_LiveClaimShapes(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(t *testing.T, desired, current *corev1.PersistentVolumeClaim)
		want       VolumeClaimConflictKind
		wantDetail []string
		why        string
	}{
		{
			name: "access modes listed in another order",
			mutate: func(t *testing.T, desired, current *corev1.PersistentVolumeClaim) {
				desired.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{
					corev1.ReadWriteOnce, corev1.ReadOnlyMany,
				}
				current.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{
					corev1.ReadOnlyMany, corev1.ReadWriteOnce,
				}
			},
			want: VolumeClaimsAgree,
			why:  "access modes are a set; the stored order is the API server's business",
		},
		{
			name: "access mode genuinely different",
			mutate: func(t *testing.T, desired, current *corev1.PersistentVolumeClaim) {
				require.Equal(t, []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					desired.Spec.AccessModes, "the builder is expected to request RWO")
				current.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}
			},
			want:       VolumeClaimsParameterConflict,
			wantDetail: []string{DataVolumeName, string(corev1.ReadWriteOnce), string(corev1.ReadWriteMany)},
			why:        "a claim bound RWM cannot be turned into an RWO one by an update",
		},
		{
			name: "API server defaults and foreign labels on the stored claim",
			mutate: func(t *testing.T, desired, current *corev1.PersistentVolumeClaim) {
				require.Nil(t, desired.Spec.VolumeMode,
					"the builder must not set volumeMode, or this row pins nothing")
				current.Spec.VolumeMode = ptr.To(corev1.PersistentVolumeFilesystem)
				current.Status.Phase = corev1.ClaimPending
				current.Labels["kubernetes.io/created-by"] = "statefulset-controller"
			},
			want: VolumeClaimsAgree,
			why: "every stored claim carries these; reporting them would put a permanent " +
				"StorageSpecNotApplied condition on every persistent cluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			desired := BuildStatefulSet(persistentFixtureValkey(), testOperatorImage)
			current := desired.DeepCopy()
			tt.mutate(t, soleClaim(t, desired), soleClaim(t, current))

			kind, detail := VolumeClaimTemplatesConflict(desired, current)

			assert.Equal(t, tt.want, kind, tt.why)
			for _, want := range tt.wantDetail {
				assert.Contains(t, detail, want)
			}
			if tt.want == VolumeClaimsAgree {
				assert.Empty(t, detail, "agreement carries no message")
			}
		})
	}
}

// An image bump must not be reported as a storage conflict. The builder stamps the
// common label set on the claim and that set carries the Valkey version taken from
// spec.image, while the live claim is frozen at the version it was created with — so
// a comparison that looked at claim metadata would report a conflict on the first
// image bump of every persistent cluster and never stop: a permanent
// StorageSpecNotApplied condition, a Warning Event per pass, and a "recreate the
// StatefulSet" instruction for storage that is entirely fine.
//
// If this test fails, someone widened VolumeClaimTemplatesConflict from the
// whitelist of requested values to claim metadata (ADR 0023 D1). The premise is
// asserted below, so the test cannot quietly stop covering the case if the builder
// ever stops labelling claims.
func TestVolumeClaimTemplatesConflict_ImageBumpIsNoConflict_ClaimLabelsAreNotCompared(t *testing.T) {
	current := BuildStatefulSet(
		persistentFixtureValkey(func(v *vkov1.Valkey) { v.Spec.Image = "valkey/valkey:8.0" }),
		testOperatorImage,
	)
	desired := BuildStatefulSet(
		persistentFixtureValkey(func(v *vkov1.Valkey) { v.Spec.Image = "valkey/valkey:9.0" }),
		testOperatorImage,
	)

	desiredLabels := soleClaim(t, desired).Labels
	currentLabels := soleClaim(t, current).Labels
	require.NotEmpty(t, currentLabels[common.LabelVersion],
		"the builder is expected to stamp the version label on the claim")
	require.NotEqual(t, desiredLabels[common.LabelVersion], currentLabels[common.LabelVersion],
		"the two builds must differ in the claim labels, or this test guards nothing")

	kind, detail := VolumeClaimTemplatesConflict(desired, current)

	assert.Equal(t, VolumeClaimsAgree, kind,
		"only the requested storage values are compared; the claim labels are not")
	assert.Empty(t, detail)
}

// The guard runs before the live StatefulSet exists — the first pass of a new CR has
// nothing to compare against — so a nil side is agreement and never a panic.
func TestVolumeClaimTemplatesConflict_NilSideAgrees(t *testing.T) {
	sts := BuildStatefulSet(persistentFixtureValkey(), testOperatorImage)
	require.NotEmpty(t, sts.Spec.VolumeClaimTemplates,
		"a nil-side test is only meaningful against a side that does carry claims")

	for _, tc := range []struct {
		name             string
		desired, current *appsv1.StatefulSet
	}{
		{name: "nil desired", desired: nil, current: sts},
		{name: "nil current", desired: sts, current: nil},
		{name: "both nil", desired: nil, current: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kind, detail := VolumeClaimTemplatesConflict(tc.desired, tc.current)
			assert.Equal(t, VolumeClaimsAgree, kind, "a missing object is not a storage conflict")
			assert.Empty(t, detail)
		})
	}
}

// reconcileSentinelStatefulSet calls the same guard as the data one, and today that
// call compares empty against empty because Sentinel keeps its state on an emptyDir
// (ADR 0023 D4). This test pins that premise. Adding storage to the Sentinel
// StatefulSet breaks it here, which is the point: the claims would then be immutable
// too, and every rule this package carries for the data StatefulSet — the structural
// refusal, the parameter report, the recreate instruction — has to be reconsidered
// for Sentinel before the feature ships.
func TestBuildSentinelStatefulSet_WritesNoClaims_ImmutabilityGuardComparesEmptyAgainstEmpty(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Spec.Persistence = &vkov1.PersistenceSpec{
			Enabled: true,
			Mode:    vkov1.PersistenceModeRDB,
			Size:    resource.MustParse("1Gi"),
		}
	})

	sts := BuildSentinelStatefulSet(v)

	assert.Empty(t, sts.Spec.VolumeClaimTemplates,
		"Sentinel state lives on an emptyDir; spec.persistence must not reach its StatefulSet")

	kind, detail := VolumeClaimTemplatesConflict(sts, sts.DeepCopy())
	assert.Equal(t, VolumeClaimsAgree, kind, "the shared guard must cost the Sentinel path nothing")
	assert.Empty(t, detail)
}
