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
	v := newTestValkey("my-valkey")

	role := BuildSidecarRole(v)

	require.NotNil(t, role)
	assert.Equal(t, "my-valkey-sidecar", role.Name)
	assert.Equal(t, "default", role.Namespace)
	assert.Equal(t, common.ManagedBy, role.Labels[common.LabelManagedBy])

	// patch is the only verb the sidecar calls; nothing else may be granted.
	// Exact match, not Contains: a reintroduced get/list would widen the grant
	// silently, and list would also break a future resourceNames restriction.
	require.Len(t, role.Rules, 1)
	rule := role.Rules[0]
	assert.Equal(t, []string{""}, rule.APIGroups)
	assert.Equal(t, []string{"pods"}, rule.Resources)
	assert.Equal(t, []string{"patch"}, rule.Verbs)
}

func TestBuildSidecarRole_Namespace(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "staging"
	})

	role := BuildSidecarRole(v)

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
