package builder

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

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
// The role grants patch access to this cluster's data pods so the sidecar can
// update the instanceRole label on its own pod and the drain stamp on a peer pod.
//
// patch is the only verb the sidecar calls — patchMetadata in
// internal/sidecar/labeler.go is the package's single clientset call site — so
// nothing else is granted. Dropping the unused get/list was the precondition for
// the resourceNames restriction below, which is incompatible with list
// (SECURITY_ARCHITECTURE.md section 4.2, ADR 0012 D8).
//
// livePodNames are the pods that currently carry this cluster's data-pod selector
// labels; SidecarRolePodNames explains why the grant is not derived from
// spec.replicas alone.
func BuildSidecarRole(v *vkov1.Valkey, livePodNames []string) *rbacv1.Role {
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SidecarServiceAccountName(v),
			Namespace: v.Namespace,
			Labels:    common.BaseLabels(v, common.ComponentValkey),
		},
	}

	names := SidecarRolePodNames(v, livePodNames)
	if len(names) == 0 {
		// An empty resourceNames list is not an empty grant in Kubernetes RBAC — it
		// matches every object of the resource. A cluster with no pod to patch gets
		// no rule at all rather than a namespace-wide one.
		role.Rules = []rbacv1.PolicyRule{}
		return role
	}

	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups:     []string{""},
			Resources:     []string{"pods"},
			Verbs:         []string{"patch"},
			ResourceNames: names,
		},
	}
	return role
}

// SidecarRolePodNames returns the data-pod names the sidecar Role grants patch on,
// sorted by ordinal: the union of the pods spec.replicas asks for and the pods that
// actually exist right now.
//
// Both halves are load-bearing. The desired half covers scale-up: the operator writes
// the Role before the StatefulSet in the same pass (reconcileResources), so the new
// pod's sidecar finds its name already granted instead of 403-ing until the next pass.
// The live half covers scale-down: 5 -> 3 leaves pods 3 and 4 terminating, and their
// drain handlers still patch — the departing master sets instanceRole=draining on
// itself to leave the -rw Service before it fails over. A list derived from
// spec.replicas alone would deny exactly that patch and keep writes flowing into a
// dying master. The name drops out of the grant when the pod is gone, not before.
//
// livePodNames are filtered to this StatefulSet's own <name>-<ordinal> pattern: the
// caller selects by label, and a label is something a pod author controls, so an
// arbitrary pod name must not be able to widen the grant.
func SidecarRolePodNames(v *vkov1.Valkey, livePodNames []string) []string {
	base := common.StatefulSetName(v, common.ComponentValkey)

	ordinals := make(map[int]struct{}, int(v.Spec.Replicas)+len(livePodNames))
	for i := 0; i < int(v.Spec.Replicas); i++ {
		ordinals[i] = struct{}{}
	}
	for _, name := range livePodNames {
		if ordinal, ok := dataPodOrdinal(base, name); ok {
			ordinals[ordinal] = struct{}{}
		}
	}

	sorted := make([]int, 0, len(ordinals))
	for ordinal := range ordinals {
		sorted = append(sorted, ordinal)
	}
	sort.Ints(sorted)

	names := make([]string, 0, len(sorted))
	for _, ordinal := range sorted {
		names = append(names, fmt.Sprintf("%s-%d", base, ordinal))
	}
	return names
}

// dataPodOrdinal reports the StatefulSet ordinal of podName, or false when the name
// is not of the form <base>-<non-negative integer>.
func dataPodOrdinal(base, podName string) (int, bool) {
	suffix, found := strings.CutPrefix(podName, base+"-")
	if !found || suffix == "" {
		return 0, false
	}
	ordinal, err := strconv.Atoi(suffix)
	if err != nil || ordinal < 0 || strconv.Itoa(ordinal) != suffix {
		// The canonical check rejects "+5" and "007": a name that is not exactly what
		// the StatefulSet controller produces is not one of its pods.
		return 0, false
	}
	return ordinal, true
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
