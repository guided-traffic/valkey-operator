package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

func newTestValkey(name, image string) *vkov1.Valkey {
	return &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: vkov1.ValkeySpec{
			Replicas: 3,
			Image:    image,
		},
	}
}

// --- ExtractVersionFromImage ---

func TestExtractVersionFromImage(t *testing.T) {
	tests := []struct {
		name     string
		image    string
		expected string
	}{
		{
			name:     "standard tag",
			image:    "valkey/valkey:8.0",
			expected: "8.0",
		},
		{
			name:     "semver tag",
			image:    "valkey/valkey:8.0.1",
			expected: "8.0.1",
		},
		{
			name:     "latest tag",
			image:    "valkey/valkey:latest",
			expected: "latest",
		},
		{
			name:     "no tag defaults to latest",
			image:    "valkey/valkey",
			expected: "latest",
		},
		{
			name:     "registry with port and tag",
			image:    "registry.example.com:5000/valkey/valkey:8.0",
			expected: "8.0",
		},
		{
			name:     "registry with port and no tag",
			image:    "registry.example.com:5000/valkey/valkey",
			expected: "latest",
		},
		{
			name:     "digest reference",
			image:    "valkey/valkey@sha256:abc123",
			expected: "sha256:abc123",
		},
		{
			name:     "simple image with tag",
			image:    "valkey:7.2",
			expected: "7.2",
		},
		{
			name:     "simple image without tag",
			image:    "valkey",
			expected: "latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractVersionFromImage(tt.image)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// --- BaseLabels ---

func TestBaseLabels(t *testing.T) {
	v := newTestValkey("my-cluster", "valkey/valkey:8.0")

	labels := BaseLabels(v, ComponentValkey)

	expected := map[string]string{
		LabelComponent: "valkey",
		LabelInstance:   "my-cluster",
		LabelManagedBy:  ManagedBy,
		LabelName:       "valkey",
		LabelVersion:    "8.0",
		LabelCluster:    "my-cluster",
	}

	assert.Equal(t, expected, labels)
}

func TestBaseLabels_Sentinel(t *testing.T) {
	v := newTestValkey("my-cluster", "valkey/valkey:8.0")

	labels := BaseLabels(v, ComponentSentinel)

	assert.Equal(t, "sentinel", labels[LabelComponent])
	assert.Equal(t, "my-cluster", labels[LabelInstance])
}

// --- PodLabels ---

func TestPodLabels_WithUserLabels(t *testing.T) {
	v := newTestValkey("test", "valkey/valkey:8.0")
	userLabels := map[string]string{
		"custom-label": "custom-value",
		"app":          "my-app",
	}

	labels := PodLabels(v, ComponentValkey, "test-0", RoleMaster, userLabels)

	// Operator-managed labels present.
	assert.Equal(t, "valkey", labels[LabelComponent])
	assert.Equal(t, "test", labels[LabelInstance])
	assert.Equal(t, ManagedBy, labels[LabelManagedBy])
	assert.Equal(t, "valkey", labels[LabelName])
	assert.Equal(t, "8.0", labels[LabelVersion])
	assert.Equal(t, "test", labels[LabelCluster])
	assert.Equal(t, "test-0", labels[LabelInstanceName])
	assert.Equal(t, RoleMaster, labels[LabelInstanceRole])

	// User labels present.
	assert.Equal(t, "custom-value", labels["custom-label"])
	assert.Equal(t, "my-app", labels["app"])
}

func TestPodLabels_OperatorLabelsOverrideUser(t *testing.T) {
	v := newTestValkey("test", "valkey/valkey:8.0")
	userLabels := map[string]string{
		LabelManagedBy: "someone-else", // Should be overridden.
	}

	labels := PodLabels(v, ComponentValkey, "test-0", RoleMaster, userLabels)

	assert.Equal(t, ManagedBy, labels[LabelManagedBy], "operator labels must take precedence")
}

func TestPodLabels_NilUserLabels(t *testing.T) {
	v := newTestValkey("test", "valkey/valkey:8.0")

	labels := PodLabels(v, ComponentValkey, "test-0", RoleReplica, nil)

	require.NotNil(t, labels)
	assert.Equal(t, "valkey", labels[LabelComponent])
	assert.Equal(t, "test-0", labels[LabelInstanceName])
	assert.Equal(t, RoleReplica, labels[LabelInstanceRole])
}

func TestPodLabels_ReplicaRole(t *testing.T) {
	v := newTestValkey("test", "valkey/valkey:8.0")

	labels := PodLabels(v, ComponentValkey, "test-1", RoleReplica, nil)

	assert.Equal(t, RoleReplica, labels[LabelInstanceRole])
	assert.Equal(t, "test-1", labels[LabelInstanceName])
}

// --- SelectorLabels ---

func TestSelectorLabels(t *testing.T) {
	v := newTestValkey("test", "valkey/valkey:8.0")

	labels := SelectorLabels(v, ComponentValkey)

	expected := map[string]string{
		LabelInstance:   "test",
		LabelManagedBy:  ManagedBy,
		LabelComponent:  "valkey",
	}

	assert.Equal(t, expected, labels)
	assert.Len(t, labels, 3, "selector labels should be minimal")
}

// --- StatefulSetName ---

func TestStatefulSetName(t *testing.T) {
	v := newTestValkey("my-cluster", "valkey/valkey:8.0")

	assert.Equal(t, "my-cluster", StatefulSetName(v, ComponentValkey))
	assert.Equal(t, "my-cluster-sentinel", StatefulSetName(v, ComponentSentinel))
}

// --- HeadlessServiceName ---

func TestHeadlessServiceName(t *testing.T) {
	v := newTestValkey("my-cluster", "valkey/valkey:8.0")

	assert.Equal(t, "my-cluster-headless", HeadlessServiceName(v, ComponentValkey))
	assert.Equal(t, "my-cluster-sentinel-headless", HeadlessServiceName(v, ComponentSentinel))
}

// --- MergeLabels ---

func TestMergeLabels(t *testing.T) {
	a := map[string]string{"a": "1", "b": "2"}
	b := map[string]string{"b": "override", "c": "3"}

	result := MergeLabels(a, b)

	expected := map[string]string{
		"a": "1",
		"b": "override",
		"c": "3",
	}
	assert.Equal(t, expected, result)
}

func TestMergeLabels_NilMaps(t *testing.T) {
	result := MergeLabels(nil, nil)
	require.NotNil(t, result)
	assert.Empty(t, result)
}

func TestMergeLabels_SingleMap(t *testing.T) {
	a := map[string]string{"a": "1"}

	result := MergeLabels(a)

	assert.Equal(t, map[string]string{"a": "1"}, result)
}

func TestMergeLabels_EmptyAndNonEmpty(t *testing.T) {
	a := map[string]string{}
	b := map[string]string{"b": "2"}

	result := MergeLabels(a, b)

	assert.Equal(t, map[string]string{"b": "2"}, result)
}

// --- MergeAnnotations ---

func TestMergeAnnotations_SameAsMergeLabels(t *testing.T) {
	a := map[string]string{"x": "1"}
	b := map[string]string{"y": "2"}

	result := MergeAnnotations(a, b)

	assert.Equal(t, map[string]string{"x": "1", "y": "2"}, result)
}
