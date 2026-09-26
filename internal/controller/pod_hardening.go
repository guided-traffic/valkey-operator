package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// errSeccompProfileNotAllowed marks a Valkey resource naming a Localhost seccomp
// profile the operator was not started with
// (docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md,
// D9). The CRD refuses Unconfined by name, but a Localhost profile is only as strict
// as the file it names, and Pod Security "restricted" accepts every one: without the
// allow-list, whoever may create a Valkey resource could run its pods under any
// profile an administrator ever put on a node, an allow-everything one included.
var errSeccompProfileNotAllowed = errors.New("the Localhost seccomp profile is not on the operator's allow-list")

// seccompProfileAllowed reports a Localhost profile outside
// --allowed-seccomp-localhost-profiles. RuntimeDefault is always allowed; an empty
// allow-list refuses every Localhost profile.
func (r *ValkeyReconciler) seccompProfileAllowed(v *vkov1.Valkey) error {
	profile := v.GetSeccompProfile()
	if profile.Type != corev1.SeccompProfileTypeLocalhost || profile.LocalhostProfile == nil ||
		slices.Contains(r.AllowedSeccompLocalhostProfiles, *profile.LocalhostProfile) {
		return nil
	}
	return fmt.Errorf("%w: spec.podSecurity.seccompProfile names %q, which --allowed-seccomp-localhost-profiles "+
		"(chart value valkeyPodSecurity.allowedSeccompLocalhostProfiles) does not list; the workloads are not "+
		"written until an administrator allows the profile or the spec names another",
		errSeccompProfileNotAllowed, *profile.LocalhostProfile)
}

// errUserNamespacesDropped marks a workload write whose pod template came back
// without the hostUsers: false the operator sent
// (docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md,
// D3). The API server drops the field without an error while its
// UserNamespacesSupport feature gate is off -- the default before Kubernetes 1.33 --
// so without this check spec.podSecurity.userNamespaces would read as applied while
// every pod runs in the node's user namespace.
var errUserNamespacesDropped = errors.New("the API server dropped hostUsers from the pod template")

// writeWorkload creates or updates obj, whose pod template is spec, and fails the
// step when the stored template lost the user namespace the operator asked for.
// controller-runtime decodes the API server's answer into obj, so spec is the stored
// template once the write returns. The comparison sees the same drift again on the
// next pass and writes again: the report stands for as long as the cluster drops
// the field, and the rate limiter paces the retries.
func (r *ValkeyReconciler) writeWorkload(ctx context.Context, obj client.Object, spec *corev1.PodSpec,
	kind string, create bool) error {
	wantUserNamespace := spec.HostUsers != nil && !*spec.HostUsers
	var err error
	if create {
		err = r.Create(ctx, obj)
	} else {
		err = r.Update(ctx, obj)
	}
	if err != nil {
		return err
	}
	if wantUserNamespace && spec.HostUsers == nil {
		return fmt.Errorf("%w of %s %s: spec.podSecurity.userNamespaces is true, but the cluster's "+
			"UserNamespacesSupport feature gate is off (the default before Kubernetes 1.33), so the pods run "+
			"without a user namespace; enable the gate or set spec.podSecurity.userNamespaces to false",
			errUserNamespacesDropped, kind, obj.GetName())
	}
	return nil
}
