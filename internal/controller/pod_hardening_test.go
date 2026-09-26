package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md,
// D3: an API server whose UserNamespacesSupport gate is off drops hostUsers from a
// pod template without an error. The interceptor below does what such a server does
// -- the write succeeds and the stored object comes back without the field -- which
// envtest (Kubernetes 1.29, gate off) reproduces for real in the integration tier.

// dropHostUsers strips hostUsers from every workload template written through it.
func dropHostUsers() interceptor.Funcs {
	strip := func(obj client.Object) {
		switch o := obj.(type) {
		case *appsv1.StatefulSet:
			o.Spec.Template.Spec.HostUsers = nil
		case *appsv1.Deployment:
			o.Spec.Template.Spec.HostUsers = nil
		}
	}
	return interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			strip(obj)
			return c.Create(ctx, obj, opts...)
		},
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			strip(obj)
			return c.Update(ctx, obj, opts...)
		},
	}
}

func usernsCluster(name string, userns bool) *vkov1.Valkey {
	return newTestValkey(name, "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
		v.Spec.PodSecurity = &vkov1.PodSecuritySpec{UserNamespaces: userns}
	})
}

// TestWriteWorkload_ReportsADroppedUserNamespace drives the three workload
// reconcilers through a create and an update each.
//
// Revert check: returning nil from writeWorkload's HostUsers branch fails every
// "dropped" row; dropping the userns condition (wantUserNamespace) fails the
// "not asked for" rows.
func TestWriteWorkload_ReportsADroppedUserNamespace(t *testing.T) {
	reconcilers := map[string]func(*ValkeyReconciler, context.Context, *vkov1.Valkey) error{
		"data StatefulSet":     (*ValkeyReconciler).reconcileStatefulSet,
		"Sentinel StatefulSet": (*ValkeyReconciler).reconcileSentinelStatefulSet,
		"observer Deployment":  (*ValkeyReconciler).reconcileObserverDeployment,
	}
	for name, reconcile := range reconcilers {
		t.Run(name+": dropped on create and on update", func(t *testing.T) {
			v := usernsCluster("drop", true)
			r, _ := newTestReconcilerWithInterceptor(dropHostUsers(), v)
			for _, write := range []string{"create", "update"} {
				err := reconcile(r, context.Background(), v)
				require.Error(t, err, write)
				assert.True(t, errors.Is(err, errUserNamespacesDropped), "%s: %v", write, err)
				assert.Equal(t, vkov1.ReasonUserNamespacesUnsupported, reconcileBlockedReason(err), write)
				assert.Contains(t, err.Error(), "UserNamespacesSupport", write)
			}
		})
		t.Run(name+": kept", func(t *testing.T) {
			v := usernsCluster("keep", true)
			r, _ := newTestReconciler(v)
			require.NoError(t, reconcile(r, context.Background(), v))
			require.NoError(t, reconcile(r, context.Background(), v), "converged: no write, no report")
		})
		t.Run(name+": not asked for", func(t *testing.T) {
			v := usernsCluster("off", false)
			r, _ := newTestReconcilerWithInterceptor(dropHostUsers(), v)
			require.NoError(t, reconcile(r, context.Background(), v))
		})
	}
}

// TestWriteWorkload_StoresTheUserNamespace: on a server that keeps the field, the
// templates carry it -- the positive control for the rows above.
func TestWriteWorkload_StoresTheUserNamespace(t *testing.T) {
	v := usernsCluster("stored", true)
	r, c := newTestReconciler(v)
	ctx := context.Background()
	require.NoError(t, r.reconcileStatefulSet(ctx, v))
	require.NoError(t, r.reconcileSentinelStatefulSet(ctx, v))
	require.NoError(t, r.reconcileObserverDeployment(ctx, v))

	for _, name := range []string{
		common.StatefulSetName(v, common.ComponentValkey),
		common.StatefulSetName(v, common.ComponentSentinel),
	} {
		sts := &appsv1.StatefulSet{}
		require.NoError(t, c.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, sts))
		assert.Equal(t, ptr.To(false), sts.Spec.Template.Spec.HostUsers, name)
	}
	deps := &appsv1.DeploymentList{}
	require.NoError(t, c.List(ctx, deps, client.InNamespace("default")))
	require.Len(t, deps.Items, 1)
	assert.Equal(t, ptr.To(false), deps.Items[0].Spec.Template.Spec.HostUsers)
	assert.Equal(t, corev1.SeccompProfileTypeRuntimeDefault, deps.Items[0].Spec.Template.Spec.SecurityContext.SeccompProfile.Type)
}

// localhostCluster names a Localhost seccomp profile on a cluster that renders
// every workload kind.
func localhostCluster(name, profile string) *vkov1.Valkey {
	v := usernsCluster(name, false)
	v.Spec.PodSecurity.SeccompProfile = &vkov1.SeccompProfileSpec{
		Type: corev1.SeccompProfileTypeLocalhost, LocalhostProfile: ptr.To(profile),
	}
	return v
}

// TestSeccompProfileAllowed pins the D9 rule: RuntimeDefault is always allowed, a
// Localhost profile only when --allowed-seccomp-localhost-profiles lists it, and an
// empty list -- the default -- refuses every one.
//
// Revert check: returning nil from seccompProfileAllowed fails every refused row;
// dropping the slices.Contains term fails the "listed" row.
func TestSeccompProfileAllowed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		allowed []string
		v       *vkov1.Valkey
		refused bool
	}{
		{"RuntimeDefault with no allow-list", nil, usernsCluster("rd", false), false},
		{"no podSecurity at all", nil, newTestValkey("none", "default"), false},
		{"Localhost with the default, empty allow-list", nil, localhostCluster("lh", "profiles/a.json"), true},
		{"Localhost listed", []string{"profiles/b.json", "profiles/a.json"}, localhostCluster("lh", "profiles/a.json"), false},
		{"Localhost not listed", []string{"profiles/b.json"}, localhostCluster("lh", "profiles/a.json"), true},
		{"a prefix of a listed profile is not listed", []string{"profiles/a.json.bak"}, localhostCluster("lh", "profiles/a.json"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &ValkeyReconciler{AllowedSeccompLocalhostProfiles: tc.allowed}
			err := r.seccompProfileAllowed(tc.v)
			if !tc.refused {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.True(t, errors.Is(err, errSeccompProfileNotAllowed))
			assert.Equal(t, vkov1.ReasonSeccompProfileNotAllowed, reconcileBlockedReason(err))
			assert.Contains(t, err.Error(), "profiles/a.json", "the message names the profile")
		})
	}
}

// TestSeccompProfileNotAllowed_NoWorkloadIsWritten: the refusal holds every
// workload write, on create and on update, and only the data StatefulSet step
// reports it -- one cause, one message in the phase.
//
// Revert check: deleting the seccompProfileAllowed call from reconcileStatefulSet,
// reconcileSentinelStatefulSet or reconcileObserverDeployment makes the matching
// object appear (create) or change (update).
func TestSeccompProfileNotAllowed_NoWorkloadIsWritten(t *testing.T) {
	ctx := context.Background()
	names := func(v *vkov1.Valkey) []string {
		return []string{common.StatefulSetName(v, common.ComponentValkey), common.StatefulSetName(v, common.ComponentSentinel)}
	}

	t.Run("create", func(t *testing.T) {
		v := localhostCluster("deny", "profiles/unlisted.json")
		r, c := newTestReconciler(v)
		r.AllowedSeccompLocalhostProfiles = []string{"profiles/listed.json"}

		err := r.reconcileStatefulSet(ctx, v)
		require.Error(t, err)
		assert.True(t, errors.Is(err, errSeccompProfileNotAllowed))
		require.NoError(t, r.reconcileSentinelStatefulSet(ctx, v), "reported once, by the data step")
		require.NoError(t, r.reconcileObserverDeployment(ctx, v))

		for _, name := range names(v) {
			err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, &appsv1.StatefulSet{})
			assert.True(t, apierrors.IsNotFound(err), "%s must not be created: %v", name, err)
		}
		deps := &appsv1.DeploymentList{}
		require.NoError(t, c.List(ctx, deps, client.InNamespace("default")))
		assert.Empty(t, deps.Items, "the observer must not be created")
	})

	t.Run("update leaves the running template alone", func(t *testing.T) {
		v := usernsCluster("deny-upd", false)
		r, c := newTestReconciler(v)
		r.AllowedSeccompLocalhostProfiles = []string{"profiles/listed.json"}
		require.NoError(t, r.reconcileStatefulSet(ctx, v))
		require.NoError(t, r.reconcileSentinelStatefulSet(ctx, v))
		require.NoError(t, r.reconcileObserverDeployment(ctx, v))
		before := map[string]string{}
		for _, name := range names(v) {
			sts := &appsv1.StatefulSet{}
			require.NoError(t, c.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, sts))
			before[name] = sts.ResourceVersion
		}

		v.Spec.PodSecurity.SeccompProfile = &vkov1.SeccompProfileSpec{
			Type: corev1.SeccompProfileTypeLocalhost, LocalhostProfile: ptr.To("profiles/unlisted.json"),
		}
		require.Error(t, r.reconcileStatefulSet(ctx, v))
		require.NoError(t, r.reconcileSentinelStatefulSet(ctx, v))
		require.NoError(t, r.reconcileObserverDeployment(ctx, v))
		for _, name := range names(v) {
			sts := &appsv1.StatefulSet{}
			require.NoError(t, c.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, sts))
			assert.Equal(t, before[name], sts.ResourceVersion, "%s must not be written", name)
			assert.Equal(t, corev1.SeccompProfileTypeRuntimeDefault, sts.Spec.Template.Spec.SecurityContext.SeccompProfile.Type)
		}
		deps := &appsv1.DeploymentList{}
		require.NoError(t, c.List(ctx, deps, client.InNamespace("default")))
		require.Len(t, deps.Items, 1)
		assert.Equal(t, corev1.SeccompProfileTypeRuntimeDefault,
			deps.Items[0].Spec.Template.Spec.SecurityContext.SeccompProfile.Type, "the observer keeps its template")
	})

	t.Run("a listed profile is written to every workload", func(t *testing.T) {
		v := localhostCluster("allow", "profiles/listed.json")
		r, c := newTestReconciler(v)
		r.AllowedSeccompLocalhostProfiles = []string{"profiles/listed.json"}
		require.NoError(t, r.reconcileStatefulSet(ctx, v))
		require.NoError(t, r.reconcileSentinelStatefulSet(ctx, v))
		require.NoError(t, r.reconcileObserverDeployment(ctx, v))
		for _, name := range names(v) {
			sts := &appsv1.StatefulSet{}
			require.NoError(t, c.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, sts))
			assert.Equal(t, ptr.To("profiles/listed.json"), sts.Spec.Template.Spec.SecurityContext.SeccompProfile.LocalhostProfile, name)
		}
	})
}

// TestSeccompProfileNotAllowed_GateSitsAtTheWrite pins where the D9 gate runs:
// after the proofs and guards of the StatefulSet step, before its drift detection.
// At the head of the step it hid a name collision (no ForeignObject report) and
// froze the StorageSpecNotApplied level, which is re-measured every pass
// (ADR 0027). Before the drift detection, it also reports a template that already
// carries a profile the allow-list no longer holds.
//
// Revert check: moving the gate back to the head of reconcileStatefulSet fails the
// first row; moving it inside the drift branch fails the second.
func TestSeccompProfileNotAllowed_GateSitsAtTheWrite(t *testing.T) {
	ctx := context.Background()

	t.Run("a foreign StatefulSet is still reported as foreign", func(t *testing.T) {
		v := localhostCluster("gate-foreign", "profiles/unlisted.json")
		foreign := &appsv1.StatefulSet{}
		foreign.Name = common.StatefulSetName(v, common.ComponentValkey)
		foreign.Namespace = "default"
		r, _ := newTestReconciler(v, foreign)

		err := r.reconcileStatefulSet(ctx, v)
		require.Error(t, err)
		assert.True(t, errors.Is(err, errForeignObject), "the collision is the first thing to say: %v", err)
	})

	t.Run("a live template the allow-list no longer holds is reported without drift", func(t *testing.T) {
		v := localhostCluster("gate-shrunk", "profiles/listed.json")
		r, c := newTestReconciler(v)
		r.AllowedSeccompLocalhostProfiles = []string{"profiles/listed.json"}
		require.NoError(t, r.reconcileStatefulSet(ctx, v))
		sts := &appsv1.StatefulSet{}
		key := types.NamespacedName{Name: common.StatefulSetName(v, common.ComponentValkey), Namespace: "default"}
		require.NoError(t, c.Get(ctx, key, sts))
		rv := sts.ResourceVersion

		r.AllowedSeccompLocalhostProfiles = nil
		err := r.reconcileStatefulSet(ctx, v)
		require.Error(t, err)
		assert.True(t, errors.Is(err, errSeccompProfileNotAllowed))
		require.NoError(t, c.Get(ctx, key, sts))
		assert.Equal(t, rv, sts.ResourceVersion, "nothing is written")
	})
}
