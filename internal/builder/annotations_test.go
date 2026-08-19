package builder

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// --- ApplyOperatorVersion ---

func TestApplyOperatorVersion_SetsAnnotationOnNilMap(t *testing.T) {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
	// Annotations are nil initially.
	ApplyOperatorVersion(cm, "1.2.3")

	assert.Equal(t, "1.2.3", cm.Annotations[AnnotationOperatorVersion])
}

func TestApplyOperatorVersion_SetsAnnotationOnExistingMap(t *testing.T) {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name:        "test",
		Annotations: map[string]string{"other": "value"},
	}}
	ApplyOperatorVersion(cm, "2.0.0")

	assert.Equal(t, "2.0.0", cm.Annotations[AnnotationOperatorVersion])
	// Existing annotations must be preserved.
	assert.Equal(t, "value", cm.Annotations["other"])
}

func TestApplyOperatorVersion_OverwritesExistingVersion(t *testing.T) {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Annotations: map[string]string{AnnotationOperatorVersion: "1.0.0"},
	}}
	ApplyOperatorVersion(cm, "1.1.0")

	assert.Equal(t, "1.1.0", cm.Annotations[AnnotationOperatorVersion])
}

func TestApplyOperatorVersion_EmptyVersionDoesNothing(t *testing.T) {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
	ApplyOperatorVersion(cm, "")

	assert.Nil(t, cm.Annotations)
}

// --- OperatorVersionChanged ---

func TestOperatorVersionChanged_ReturnsTrueWhenAnnotationMissing(t *testing.T) {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
	assert.True(t, OperatorVersionChanged(cm, "1.0.0"))
}

func TestOperatorVersionChanged_ReturnsTrueWhenVersionDiffers(t *testing.T) {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Annotations: map[string]string{AnnotationOperatorVersion: "1.0.0"},
	}}
	assert.True(t, OperatorVersionChanged(cm, "1.1.0"))
}

func TestOperatorVersionChanged_ReturnsFalseWhenVersionMatches(t *testing.T) {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Annotations: map[string]string{AnnotationOperatorVersion: "1.2.3"},
	}}
	assert.False(t, OperatorVersionChanged(cm, "1.2.3"))
}

func TestOperatorVersionChanged_ReturnsFalseWhenVersionEmpty(t *testing.T) {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
	// Empty version means "unversioned" — no change should be detected.
	assert.False(t, OperatorVersionChanged(cm, ""))
}

// --- NudgeDue ---

func TestNudgeDue_TrueWhenAnnotationMissing(t *testing.T) {
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
	assert.True(t, NudgeDue(sts, time.Now()))
}

func TestNudgeDue_FalseWithinInterval(t *testing.T) {
	now := time.Now()
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{
		Annotations: map[string]string{AnnotationNudge: now.Add(-NudgeInterval / 2).UTC().Format(time.RFC3339)},
	}}
	assert.False(t, NudgeDue(sts, now), "a nudge written less than NudgeInterval ago must not be repeated")
}

func TestNudgeDue_TrueAfterInterval(t *testing.T) {
	now := time.Now()
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{
		Annotations: map[string]string{AnnotationNudge: now.Add(-NudgeInterval - time.Second).UTC().Format(time.RFC3339)},
	}}
	assert.True(t, NudgeDue(sts, now))
}

func TestNudgeDue_TrueOnUnparsableValue(t *testing.T) {
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{
		Annotations: map[string]string{AnnotationNudge: "not-a-timestamp"},
	}}
	assert.True(t, NudgeDue(sts, time.Now()))
}

func TestNudgeDue_TrueOnFutureValue(t *testing.T) {
	// A timestamp from the future (clock skew or a hand-edited value) must not
	// block nudges until that time is reached.
	now := time.Now()
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{
		Annotations: map[string]string{AnnotationNudge: now.Add(time.Hour).UTC().Format(time.RFC3339)},
	}}
	assert.True(t, NudgeDue(sts, now))
}

// --- NudgePatch ---

func TestNudgePatch_SetsAnnotationToTimestamp(t *testing.T) {
	now := time.Date(2026, 8, 19, 13, 51, 28, 0, time.UTC)

	var patch struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal(NudgePatch(now), &patch))
	assert.Equal(t, "2026-08-19T13:51:28Z", patch.Metadata.Annotations[AnnotationNudge])
	assert.Len(t, patch.Metadata.Annotations, 1, "patch must touch nothing but the nudge annotation")
}

// TestNudgeAnnotation_DoesNotTriggerDriftDetection guards the core assumption of
// the nudge: it is written on the StatefulSet metadata, so neither the Valkey nor
// the Sentinel drift check sees it and no rolling update is caused.
func TestNudgeAnnotation_DoesNotTriggerDriftDetection(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	desired := BuildStatefulSet(v, "")
	current := BuildStatefulSet(v, "")
	current.Annotations = map[string]string{AnnotationNudge: time.Now().UTC().Format(time.RFC3339)}
	assert.False(t, StatefulSetHasChanged(desired, current))
	assert.False(t, OperatorVersionChanged(current, ""))

	desiredSentinel := BuildSentinelStatefulSet(v)
	currentSentinel := BuildSentinelStatefulSet(v)
	currentSentinel.Annotations = map[string]string{AnnotationNudge: time.Now().UTC().Format(time.RFC3339)}
	assert.False(t, SentinelStatefulSetHasChanged(desiredSentinel, currentSentinel))
}
