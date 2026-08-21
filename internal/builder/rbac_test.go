package builder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// --- SidecarServiceAccountName ---

func TestSidecarServiceAccountName(t *testing.T) {
	v := newTestValkey("my-valkey")
	assert.Equal(t, "my-valkey-sidecar", SidecarServiceAccountName(v))
}

// --- BuildSidecarServiceAccount ---

func TestBuildSidecarServiceAccount(t *testing.T) {
	v := newTestValkey("my-valkey")

	sa := BuildSidecarServiceAccount(v)

	require.NotNil(t, sa)
	assert.Equal(t, "my-valkey-sidecar", sa.Name)
	assert.Equal(t, "default", sa.Namespace)
	assert.Equal(t, "valkey", sa.Labels[common.LabelComponent])
	assert.Equal(t, "my-valkey", sa.Labels[common.LabelInstance])
	assert.Equal(t, common.ManagedBy, sa.Labels[common.LabelManagedBy])
}

func TestBuildSidecarServiceAccount_Namespace(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "production"
	})

	sa := BuildSidecarServiceAccount(v)

	assert.Equal(t, "production", sa.Namespace)
}

// --- BuildSidecarRole ---

func TestBuildSidecarRole(t *testing.T) {
	v := newTestValkey("my-valkey", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })

	role := BuildSidecarRole(v, nil)

	require.NotNil(t, role)
	assert.Equal(t, "my-valkey-sidecar", role.Name)
	assert.Equal(t, "default", role.Namespace)
	assert.Equal(t, common.ManagedBy, role.Labels[common.LabelManagedBy])

	// patch is the only verb the sidecar calls, and only on this cluster's own data
	// pods. Exact match, not Contains: a reintroduced get/list would widen the grant
	// silently, list is incompatible with resourceNames, and a dropped resourceNames
	// list would hand every sidecar token patch access to every pod in the namespace —
	// including another cluster's drain stamp, which the operator consumes as promotion
	// evidence (ADR 0012 D8 step 3).
	require.Len(t, role.Rules, 1)
	rule := role.Rules[0]
	assert.Equal(t, []string{""}, rule.APIGroups)
	assert.Equal(t, []string{"pods"}, rule.Resources)
	assert.Equal(t, []string{"patch"}, rule.Verbs)
	assert.Equal(t, []string{"my-valkey-0", "my-valkey-1", "my-valkey-2"}, rule.ResourceNames)
}

func TestBuildSidecarRole_NoPodsYieldsNoRuleRatherThanAnOpenOne(t *testing.T) {
	v := newTestValkey("my-valkey", func(v *vkov1.Valkey) { v.Spec.Replicas = 0 })

	role := BuildSidecarRole(v, nil)

	// An empty resourceNames list matches every pod in Kubernetes RBAC, so the
	// no-pods case must produce no rule at all.
	assert.Empty(t, role.Rules)
}

// --- SidecarRolePodNames ---

func TestSidecarRolePodNames_CoversTheDesiredReplicas(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })

	// Scale-up: pod 2 does not exist yet, and the Role is written before the
	// StatefulSet in the same pass, so its name has to be granted in advance.
	assert.Equal(t, []string{"test-0", "test-1", "test-2"},
		SidecarRolePodNames(v, []string{"test-0", "test-1"}))
}

func TestSidecarRolePodNames_KeepsPodsThatOutliveTheSpec(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })

	// Scale-down 5 -> 3: pods 3 and 4 are terminating and their drain handlers still
	// patch. The departing master sets instanceRole=draining on itself to leave the
	// -rw Service before it fails over; denying that patch keeps writes flowing into
	// a dying master.
	got := SidecarRolePodNames(v, []string{"test-4", "test-0", "test-3", "test-1", "test-2"})

	assert.Equal(t, []string{"test-0", "test-1", "test-2", "test-3", "test-4"}, got,
		"a pod leaves the grant when it is gone, not when the spec stops asking for it")
}

func TestSidecarRolePodNames_IgnoresNamesThatAreNotThisStatefulSets(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) { v.Spec.Replicas = 1 })

	// The caller selects pods by label, and labels are set by whoever creates the pod.
	// A pod that carries this cluster's labels under a foreign name must not widen the
	// grant to that name.
	got := SidecarRolePodNames(v, []string{
		"other-cluster-0", // a different StatefulSet
		"test-sentinel-0", // this cluster, but not a data pod
		"test-",           // no ordinal
		"test-x",          // not a number
		"test-007",        // not the canonical form the StatefulSet controller writes
		"test--1",         // negative
		"test-1",          // the only legitimate addition
	})

	assert.Equal(t, []string{"test-0", "test-1"}, got)
}

func TestBuildSidecarRole_Namespace(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "staging"
	})

	role := BuildSidecarRole(v, nil)

	assert.Equal(t, "staging", role.Namespace)
}

// --- BuildSidecarRoleBinding ---

func TestBuildSidecarRoleBinding(t *testing.T) {
	v := newTestValkey("my-valkey")

	rb := BuildSidecarRoleBinding(v)

	require.NotNil(t, rb)
	assert.Equal(t, "my-valkey-sidecar", rb.Name)
	assert.Equal(t, "default", rb.Namespace)
	assert.Equal(t, common.ManagedBy, rb.Labels[common.LabelManagedBy])

	// RoleRef must reference the sidecar Role.
	assert.Equal(t, "rbac.authorization.k8s.io", rb.RoleRef.APIGroup)
	assert.Equal(t, "Role", rb.RoleRef.Kind)
	assert.Equal(t, "my-valkey-sidecar", rb.RoleRef.Name)

	// Subject must be the sidecar ServiceAccount.
	require.Len(t, rb.Subjects, 1)
	subj := rb.Subjects[0]
	assert.Equal(t, rbacv1.ServiceAccountKind, subj.Kind)
	assert.Equal(t, "my-valkey-sidecar", subj.Name)
	assert.Equal(t, "default", subj.Namespace)
}

func TestBuildSidecarRoleBinding_SubjectNamespace(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "custom-ns"
	})

	rb := BuildSidecarRoleBinding(v)

	// Subject namespace must match the Valkey resource namespace.
	assert.Equal(t, "custom-ns", rb.Subjects[0].Namespace)
	assert.Equal(t, "custom-ns", rb.Namespace)
}
