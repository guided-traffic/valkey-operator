package builder

import (
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// SidecarServiceAccountName returns the name of the ServiceAccount used by the sidecar container.
func SidecarServiceAccountName(v *vkov1.Valkey) string {
	return v.Name + "-sidecar"
}

// BuildSidecarServiceAccount builds the ServiceAccount for the sidecar container.
// Each Valkey instance gets its own ServiceAccount to limit blast radius.
func BuildSidecarServiceAccount(v *vkov1.Valkey) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SidecarServiceAccountName(v),
			Namespace: v.Namespace,
			Labels:    common.BaseLabels(v, common.ComponentValkey),
		},
	}
}

// BuildSidecarRole builds the namespaced Role for the sidecar container.
// The role grants patch access to pods owned by this Valkey instance so the sidecar
// can update the instanceRole label on its own pod.
func BuildSidecarRole(v *vkov1.Valkey) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SidecarServiceAccountName(v),
			Namespace: v.Namespace,
			Labels:    common.BaseLabels(v, common.ComponentValkey),
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"pods"},
				Verbs:     []string{"get", "list", "patch"},
			},
		},
	}
}

// BuildSidecarRoleBinding builds the RoleBinding that binds the sidecar Role to its ServiceAccount.
func BuildSidecarRoleBinding(v *vkov1.Valkey) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SidecarServiceAccountName(v),
			Namespace: v.Namespace,
			Labels:    common.BaseLabels(v, common.ComponentValkey),
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     SidecarServiceAccountName(v),
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      SidecarServiceAccountName(v),
				Namespace: v.Namespace,
			},
		},
	}
}
