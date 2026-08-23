package builder

import (
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// A StatefulSet's volumeClaimTemplates are immutable: the API server rejects every
// update that changes spec outside the replicas / ordinals / template /
// updateStrategy / persistentVolumeClaimRetentionPolicy / minReadySeconds
// whitelist, with one error that names none of them. The operator never writes
// them either — reconcileStatefulSet copies replicas, template and labels onto the
// live object — so a difference between what the spec asks for and what the live
// StatefulSet carries is not drift a further pass converges.
//
// This file is the comparison that says so, and how badly.
// docs/adr/0023-volume-claim-templates-are-immutable.md.

// VolumeClaimConflictKind classifies how the volumeClaimTemplates of a desired
// StatefulSet differ from the ones the live object carries.
type VolumeClaimConflictKind int

const (
	// VolumeClaimsAgree means every claim the builder would write is on the live
	// object under the same name with the same requested values.
	VolumeClaimsAgree VolumeClaimConflictKind = iota

	// VolumeClaimsStructuralConflict means the set of claims itself differs:
	// spec.persistence was toggled after the StatefulSet was created. Neither
	// direction can be written. Enabling produces a pod template that mounts a
	// volume no live claim backs, which the API server rejects on every pass.
	// Disabling is worse because it is accepted: the template gains an emptyDir
	// under the same name while the claims stay on the object, and the
	// statefulset-controller keeps generating pods backed by the claim.
	VolumeClaimsStructuralConflict

	// VolumeClaimsParameterConflict means the same claims exist under the same
	// names but a requested value differs — size, storage class or access modes.
	// The pod template stays writable, so the operator applies everything else and
	// only the storage parameters are stuck.
	VolumeClaimsParameterConflict
)

// VolumeClaimTemplatesConflict compares the volumeClaimTemplates of a desired
// StatefulSet against the live one and reports whether the difference can be
// written at all.
//
// Only the fields the builder itself decides are compared, and each is compared
// semantically rather than structurally:
//
//   - the claim names, because they are what the pod template mounts;
//   - the storage request, through Quantity.Cmp, so a size written as a plain byte
//     count and the same size written as 1Gi agree. (1024Mi is not an example of
//     this: ParseQuantity canonicalises binary suffixes, so it *becomes* 1Gi before
//     any comparison sees it.)
//   - the storage class, nil-aware, nil meaning the cluster default class;
//   - the access modes, as a set.
//
// Everything else is deliberately out of scope, and the labels above all. The
// builder stamps the common label set on the claim, and that set carries the
// Valkey version extracted from spec.image — while the live claim is frozen at
// creation time. Comparing labels would therefore report a conflict on the first
// image bump and never stop, on every persistent cluster. The API server also
// defaults fields on the stored object that the builder never sets (volumeMode,
// status), which is the second reason this is a whitelist and not a DeepEqual.
//
// The returned detail is empty when the claims agree and otherwise names the
// difference, for the Event and the condition message.
func VolumeClaimTemplatesConflict(desired, current *appsv1.StatefulSet) (VolumeClaimConflictKind, string) {
	if desired == nil || current == nil {
		return VolumeClaimsAgree, ""
	}

	desiredClaims := volumeClaimsByName(desired.Spec.VolumeClaimTemplates)
	currentClaims := volumeClaimsByName(current.Spec.VolumeClaimTemplates)

	missing := missingClaimNames(desiredClaims, currentClaims)
	obsolete := missingClaimNames(currentClaims, desiredClaims)
	if len(missing) > 0 || len(obsolete) > 0 {
		return VolumeClaimsStructuralConflict, structuralClaimDetail(missing, obsolete)
	}

	var details []string
	for _, name := range sortedClaimNames(desiredClaims) {
		if detail := claimParameterDetail(name, desiredClaims[name], currentClaims[name]); detail != "" {
			details = append(details, detail)
		}
	}
	if len(details) > 0 {
		return VolumeClaimsParameterConflict, strings.Join(details, "; ")
	}

	return VolumeClaimsAgree, ""
}

// volumeClaimsByName indexes claims by name. The API server rejects duplicate
// names inside volumeClaimTemplates, so the index loses nothing.
func volumeClaimsByName(claims []corev1.PersistentVolumeClaim) map[string]corev1.PersistentVolumeClaim {
	byName := make(map[string]corev1.PersistentVolumeClaim, len(claims))
	for _, claim := range claims {
		byName[claim.Name] = claim
	}
	return byName
}

// missingClaimNames returns the names present in a but not in b, sorted so the
// message is stable across passes.
func missingClaimNames(a, b map[string]corev1.PersistentVolumeClaim) []string {
	var names []string
	for name := range a {
		if _, ok := b[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// sortedClaimNames returns the claim names in a stable order.
func sortedClaimNames(claims map[string]corev1.PersistentVolumeClaim) []string {
	names := make([]string, 0, len(claims))
	for name := range claims {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// structuralClaimDetail names which claims the spec asks for that the live object
// does not have, and which it has that the spec no longer asks for.
func structuralClaimDetail(missing, obsolete []string) string {
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "the spec asks for "+strings.Join(missing, ", ")+
			", which the live StatefulSet does not have")
	}
	if len(obsolete) > 0 {
		parts = append(parts, "the live StatefulSet has "+strings.Join(obsolete, ", ")+
			", which the spec no longer asks for")
	}
	return strings.Join(parts, "; ")
}

// claimParameterDetail names every requested value that differs between two claims
// of the same name, or returns an empty string when they agree.
func claimParameterDetail(name string, desired, current corev1.PersistentVolumeClaim) string {
	var parts []string

	desiredSize := claimStorageRequest(desired)
	currentSize := claimStorageRequest(current)
	if desiredSize.Cmp(currentSize) != 0 {
		parts = append(parts, fmt.Sprintf("size %s requested, %s in use", desiredSize.String(), currentSize.String()))
	}

	if d, c := storageClassKey(desired.Spec.StorageClassName), storageClassKey(current.Spec.StorageClassName); d != c {
		parts = append(parts, fmt.Sprintf("storage class %s requested, %s in use", d, c))
	}

	if d, c := accessModeKey(desired.Spec.AccessModes), accessModeKey(current.Spec.AccessModes); d != c {
		parts = append(parts, fmt.Sprintf("access modes %s requested, %s in use", d, c))
	}

	if len(parts) == 0 {
		return ""
	}
	return name + ": " + strings.Join(parts, ", ")
}

// claimStorageRequest returns the requested storage, or the zero quantity when the
// claim requests none. Indexing an absent key yields the zero Quantity, which
// compares as 0 and is exactly the wanted answer for "nothing requested".
func claimStorageRequest(claim corev1.PersistentVolumeClaim) resource.Quantity {
	return claim.Spec.Resources.Requests[corev1.ResourceStorage]
}

// storageClassKey renders a storage class name for comparison and for the message.
// A nil name is the cluster default class, which is a value like any other here —
// switching a claim from an explicit class to the default is as unwritable as any
// other change.
func storageClassKey(name *string) string {
	if name == nil {
		return "(cluster default)"
	}
	return *name
}

// accessModeKey renders access modes as an order-independent key.
func accessModeKey(modes []corev1.PersistentVolumeAccessMode) string {
	out := make([]string, 0, len(modes))
	for _, mode := range modes {
		out = append(out, string(mode))
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}
