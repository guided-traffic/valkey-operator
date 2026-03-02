package builder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
