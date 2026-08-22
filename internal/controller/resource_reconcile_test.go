package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// ownedByValkey stamps the controller ownerReference every reconciler writes on the
// objects it creates. A fixture that stands in for an operator-created object needs
// it: without one the ownership guards read it as foreign and refuse to touch it
// (docs/adr/0020-write-only-what-the-operator-owns.md).
func ownedByValkey(t *testing.T, v *vkov1.Valkey, obj client.Object) {
	t.Helper()
	require.NoError(t, controllerutil.SetControllerReference(v, obj, testScheme()))
}

// stampStatefulSetUID gives a StatefulSet the fake client creates the same
// deterministic UID a staged fixture carries.
//
// The fake client assigns no UID at all, and the pod guards compare exactly that
// field (metav1.IsControlledBy ignores name and kind mismatches only after the UID
// matches). Without this, a StatefulSet the reconciler created under test would have
// an empty UID, every pod fixture pointing at testStsUID would read as foreign, and
// the tests would be measuring the fixture rather than the guard.
func stampStatefulSetUID() interceptor.Funcs {
	return withStatefulSetUID(interceptor.Funcs{})
}

// withStatefulSetUID adds the UID stamp to an existing set of interceptors without
// displacing a Create the caller supplied.
func withStatefulSetUID(funcs interceptor.Funcs) interceptor.Funcs {
	inner := funcs.Create
	funcs.Create = func(ctx context.Context, cl client.WithWatch, obj client.Object,
		opts ...client.CreateOption) error {
		if sts, ok := obj.(*appsv1.StatefulSet); ok && sts.UID == "" {
			sts.UID = types.UID(sts.Name + "-sts-uid")
		}
		if inner != nil {
			return inner(ctx, cl, obj, opts...)
		}
		return cl.Create(ctx, obj, opts...)
	}
	return funcs
}

// testStsUID is the UID every fixture StatefulSet carries and every fixture pod
// points at. It is deterministic so a pod fixture can be built without the
// StatefulSet object in hand, and it is non-empty for the reason newTestValkey gives
// for the CR: metav1.IsControlledBy compares UIDs, so with an empty one on both sides
// an ownership check passes without testing anything.
func testStsUID(v *vkov1.Valkey, component string) types.UID {
	return stsUIDFor(common.StatefulSetName(v, component))
}

// stsUIDFor is testStsUID for fixtures that name their StatefulSet directly. The
// suffix is the same string withStatefulSetUID stamps, so a hand-built StatefulSet
// and a reconciler-created one are indistinguishable to the pod guards.
func stsUIDFor(name string) types.UID {
	return types.UID(name + "-sts-uid")
}

// The two must agree: a pod fixture built without the StatefulSet in hand points at
// testStsUID, and a StatefulSet the reconciler creates gets the same string from
// withStatefulSetUID.

// ownedByTestSts stamps the controller ownerReference the statefulset-controller puts
// on the pods it creates, matching testStsUID. For fixtures that do have the
// StatefulSet object in hand, podOwnedBySts below is the same thing via the scheme.
func ownedByTestSts(v *vkov1.Valkey, component string, pod *corev1.Pod) {
	yes := true
	pod.OwnerReferences = append(pod.OwnerReferences, metav1.OwnerReference{
		APIVersion: appsv1.SchemeGroupVersion.String(),
		Kind:       "StatefulSet",
		Name:       common.StatefulSetName(v, component),
		UID:        testStsUID(v, component),
		Controller: &yes,
	})
}

// podOwnedBySts stamps the controller ownerReference the statefulset-controller puts
// on every pod it creates. A pod fixture needs it: the pod guards prove provenance
// two-hop, pod -> StatefulSet -> CR, so a pod without it reads as foreign
// (docs/adr/0020-write-only-what-the-operator-owns.md D9).
func podOwnedBySts(sts *appsv1.StatefulSet, pod *corev1.Pod) {
	if err := controllerutil.SetControllerReference(sts, pod, testScheme()); err != nil {
		panic(err)
	}
}

// controllerRefTo is ownedByValkey for fixture constructors that have no *testing.T.
// The scheme is fixed, so the only error SetControllerReference can return here is a
// programming mistake — worth a panic, not a threaded error.
func controllerRefTo(v *vkov1.Valkey, obj client.Object) {
	if err := controllerutil.SetControllerReference(v, obj, testScheme()); err != nil {
		panic(err)
	}
}

// mustReconcileSidecarRole runs the step and asserts it neither failed nor refused.
func mustReconcileSidecarRole(t *testing.T, r *ValkeyReconciler, v *vkov1.Valkey) {
	t.Helper()
	owned, err := r.reconcileSidecarRole(context.Background(), v)
	require.NoError(t, err)
	require.True(t, owned, "the sidecar Role must be owned by this Valkey")
}

// --- owner-reference failure seam ---

// newReconcilerWithoutValkeyInScheme returns a reconciler whose Scheme does not
// know the Valkey kind, so every controllerutil.SetControllerReference call
// fails. That is the shape of a mis-wired scheme in main.go, and it must abort
// each reconciler before it writes anything: an unowned managed object would
// never be garbage-collected with the CR.
func newReconcilerWithoutValkeyInScheme() (*ValkeyReconciler, *int) {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = appsv1.AddToScheme(s)

	writes := 0
	countWrites := interceptor.Funcs{
		Create: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.CreateOption) error {
			writes++
			return nil
		},
		Update: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.UpdateOption) error {
			writes++
			return nil
		},
		Delete: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.DeleteOption) error {
			writes++
			return nil
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithInterceptorFuncs(countWrites).Build()
	return &ValkeyReconciler{
		Client:          c,
		Scheme:          s,
		InstanceChecker: &mockInstanceChecker{},
		OperatorImage:   "ghcr.io/guided-traffic/valkey-operator:test",
	}, &writes
}

func TestReconcilers_AbortWithoutWriting_WhenOwnerReferenceCannotBeSet(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{Enabled: true}
	})

	cases := []struct {
		name    string
		run     func(r *ValkeyReconciler, ctx context.Context) error
		wantMsg string
	}{
		{"configMap", func(r *ValkeyReconciler, ctx context.Context) error {
			return r.reconcileConfigMap(ctx, v)
		}, "setting owner reference on ConfigMap"},
		{"replicaConfigMap", func(r *ValkeyReconciler, ctx context.Context) error {
			return r.reconcileReplicaConfigMap(ctx, v)
		}, "setting owner reference on replica ConfigMap"},
		{"service", func(r *ValkeyReconciler, ctx context.Context) error {
			return r.reconcileHeadlessService(ctx, v)
		}, "setting owner reference on Service test-headless"},
		{"statefulSet", func(r *ValkeyReconciler, ctx context.Context) error {
			return r.reconcileStatefulSet(ctx, v)
		}, "setting owner reference on StatefulSet"},
		{"sidecarServiceAccount", func(r *ValkeyReconciler, ctx context.Context) error {
			_, err := r.reconcileSidecarServiceAccount(ctx, v)
			return err
		}, "setting owner reference on sidecar ServiceAccount"},
		{"sidecarRole", func(r *ValkeyReconciler, ctx context.Context) error {
			_, err := r.reconcileSidecarRole(ctx, v)
			return err
		}, "setting owner reference on sidecar Role"},
		{"sidecarRoleBinding", func(r *ValkeyReconciler, ctx context.Context) error {
			return r.reconcileSidecarRoleBinding(ctx, v)
		}, "setting owner reference on sidecar RoleBinding"},
		{"sentinelConfigMap", func(r *ValkeyReconciler, ctx context.Context) error {
			return r.reconcileSentinelConfigMap(ctx, v)
		}, "setting owner reference on Sentinel ConfigMap"},
		{"sentinelStatefulSet", func(r *ValkeyReconciler, ctx context.Context) error {
			return r.reconcileSentinelStatefulSet(ctx, v)
		}, "setting owner reference on Sentinel StatefulSet"},
		{"observerDeployment", func(r *ValkeyReconciler, ctx context.Context) error {
			return r.reconcileObserverDeployment(ctx, v)
		}, "setting owner reference on Observer Deployment"},
		{"networkPolicy", func(r *ValkeyReconciler, ctx context.Context) error {
			return r.reconcileNetworkPolicy(ctx, v, builder.BuildValkeyNetworkPolicy(v, "valkey-system"))
		}, "setting owner reference on NetworkPolicy"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, writes := newReconcilerWithoutValkeyInScheme()

			err := tc.run(r, context.Background())

			require.Error(t, err, "an unsettable owner reference must fail the step")
			assert.Contains(t, err.Error(), tc.wantMsg)
			assert.Zero(t, *writes, "nothing may be written without an owner reference")
		})
	}
}

// --- reconcileNetworkPolicy ---

func networkPolicyValkey() *vkov1.Valkey {
	return newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.UID = types.UID("np-uid")
		v.Spec.Replicas = 3
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{Enabled: true}
	})
}

func TestReconcileNetworkPolicy_Update_RestoresIngressAndLabels(t *testing.T) {
	const version = "3.0.0"
	v := networkPolicyValkey()
	desired := builder.BuildValkeyNetworkPolicy(v, "valkey-system")

	// Somebody stripped the ingress rules and the labels off the live object.
	stale := desired.DeepCopy()
	stale.Spec.Ingress = nil
	stale.Labels = map[string]string{"hand-edited": "true"}
	ownedByValkey(t, v, stale)

	r, c := newReconcilerWithInterceptor(version, interceptor.Funcs{}, v, stale)

	require.NoError(t, r.reconcileNetworkPolicy(context.Background(), v,
		builder.BuildValkeyNetworkPolicy(v, "valkey-system")))

	got := &networkingv1.NetworkPolicy{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: desired.Name, Namespace: "default"}, got))
	assert.Equal(t, desired.Spec.Ingress, got.Spec.Ingress,
		"the ingress rules must be restored from the desired policy")
	assert.NotEmpty(t, got.Spec.Ingress, "precondition: the desired policy has ingress rules")
	assert.NotContains(t, got.Labels, "hand-edited", "labels must be replaced by the desired set")
	assert.Equal(t, version, got.Annotations[builder.AnnotationOperatorVersion])
}

func TestReconcileNetworkPolicy_SecondPass_IssuesNoUpdate(t *testing.T) {
	v := networkPolicyValkey()
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v)
	ctx := context.Background()
	name := builder.BuildValkeyNetworkPolicy(v, "valkey-system").Name

	require.NoError(t, r.reconcileNetworkPolicy(ctx, v, builder.BuildValkeyNetworkPolicy(v, "valkey-system")))
	first := &networkingv1.NetworkPolicy{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, first))

	require.NoError(t, r.reconcileNetworkPolicy(ctx, v, builder.BuildValkeyNetworkPolicy(v, "valkey-system")))
	second := &networkingv1.NetworkPolicy{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, second))

	assert.Equal(t, first.ResourceVersion, second.ResourceVersion,
		"an unchanged NetworkPolicy must not be rewritten")
}

func TestReconcileNetworkPolicy_PropagatesGetError(t *testing.T) {
	v := networkPolicyValkey()
	funcs := interceptor.Funcs{
		Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			if _, ok := obj.(*networkingv1.NetworkPolicy); ok {
				return internalErr("networkpolicy read failed")
			}
			return nil
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v)

	err := r.reconcileNetworkPolicy(context.Background(), v,
		builder.BuildValkeyNetworkPolicy(v, "valkey-system"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "networkpolicy read failed")
}

func TestReconcileNetworkPolicies_WrapsPerPolicyError(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{Enabled: true}
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	sentinelNP := builder.SentinelNetworkPolicyName(v)
	funcs := interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if obj.GetName() == sentinelNP {
				return internalErr("admission denied")
			}
			return cl.Create(ctx, obj, opts...)
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v)

	err := r.reconcileNetworkPolicies(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "sentinel networkpolicy:")
	// The Valkey policy is reconciled first and must have landed.
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: builder.NetworkPolicyName(v), Namespace: "default",
	}, &networkingv1.NetworkPolicy{}))
}

// --- reconcileObserverDeployment ---

func observerValkey() *vkov1.Valkey {
	return newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.UID = types.UID("obs-uid")
		v.Spec.Replicas = 3
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})
}

func TestReconcileObserverDeployment_Update_AdoptsNewOperatorImage(t *testing.T) {
	const version = "4.0.0"
	v := observerValkey()

	stale := builder.BuildObserverDeployment(v, "ghcr.io/guided-traffic/valkey-operator:old")
	stale.Labels = map[string]string{"stale": "yes"}
	ownedByValkey(t, v, stale)

	r, c := newReconcilerWithInterceptor(version, interceptor.Funcs{}, v, stale)
	r.OperatorImage = "ghcr.io/guided-traffic/valkey-operator:new"

	require.NoError(t, r.reconcileObserverDeployment(context.Background(), v))

	got := &appsv1.Deployment{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: builder.ObserverDeploymentName(v), Namespace: "default",
	}, got))
	require.NotEmpty(t, got.Spec.Template.Spec.Containers)
	assert.Equal(t, "ghcr.io/guided-traffic/valkey-operator:new",
		got.Spec.Template.Spec.Containers[0].Image,
		"an image change must be rolled into the observer Deployment")
	assert.NotContains(t, got.Labels, "stale")
	assert.Equal(t, version, got.Annotations[builder.AnnotationOperatorVersion])
}

func TestReconcileObserverDeployment_SecondPass_IssuesNoUpdate(t *testing.T) {
	v := observerValkey()
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v)
	ctx := context.Background()
	key := types.NamespacedName{Name: builder.ObserverDeploymentName(v), Namespace: "default"}

	require.NoError(t, r.reconcileObserverDeployment(ctx, v))
	first := &appsv1.Deployment{}
	require.NoError(t, c.Get(ctx, key, first))

	require.NoError(t, r.reconcileObserverDeployment(ctx, v))
	second := &appsv1.Deployment{}
	require.NoError(t, c.Get(ctx, key, second))

	assert.Equal(t, first.ResourceVersion, second.ResourceVersion,
		"an unchanged observer Deployment must not be rewritten")
}

func TestReconcileObserverDeployment_PropagatesGetError(t *testing.T) {
	v := observerValkey()
	funcs := interceptor.Funcs{
		Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			if _, ok := obj.(*appsv1.Deployment); ok {
				return internalErr("deployment read failed")
			}
			return nil
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v)

	err := r.reconcileObserverDeployment(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "deployment read failed")
}

// --- reconcileSentinelConfigMap ---

func TestReconcileSentinelConfigMap_Update_ReplacesDriftedData(t *testing.T) {
	const version = "5.0.0"
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.UID = types.UID("sent-uid")
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	desired := builder.BuildSentinelConfigMap(v)
	stale := desired.DeepCopy()
	stale.Data = map[string]string{"sentinel.conf": "# hand-edited\n"}
	stale.Labels = map[string]string{"stale": "yes"}
	ownedByValkey(t, v, stale)

	r, c := newReconcilerWithInterceptor(version, interceptor.Funcs{}, v, stale)

	require.NoError(t, r.reconcileSentinelConfigMap(context.Background(), v))

	got := &corev1.ConfigMap{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: desired.Name, Namespace: "default"}, got))
	assert.Equal(t, desired.Data, got.Data, "the generated Sentinel config must win over hand edits")
	assert.NotContains(t, got.Data["sentinel.conf"], "hand-edited")
	assert.NotContains(t, got.Labels, "stale")
	assert.Equal(t, version, got.Annotations[builder.AnnotationOperatorVersion])
}

func TestReconcileSentinelConfigMap_PropagatesGetError(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	funcs := interceptor.Funcs{
		Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			if _, ok := obj.(*corev1.ConfigMap); ok {
				return internalErr("configmap read failed")
			}
			return nil
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v)

	err := r.reconcileSentinelConfigMap(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "configmap read failed")
}

func TestReconcileSentinelResources_WrapsConfigMapError(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	funcs := interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*corev1.ConfigMap); ok {
				return internalErr("configmap create failed")
			}
			return cl.Create(ctx, obj, opts...)
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v)

	err := r.reconcileSentinelResources(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "sentinel configmap:")
	// The step aborts before the Sentinel StatefulSet is created.
	stsErr := c.Get(context.Background(), types.NamespacedName{
		Name: common.StatefulSetName(v, common.ComponentSentinel), Namespace: "default",
	}, &appsv1.StatefulSet{})
	assert.True(t, apierrors.IsNotFound(stsErr),
		"a failing Sentinel ConfigMap must stop the Sentinel StatefulSet from being created")
}

// --- reconcileServiceMonitor ---

func serviceMonitorValkey() *vkov1.Valkey {
	return newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.UID = types.UID("sm-uid")
		v.Spec.Metrics = &vkov1.MetricsSpec{
			Enabled: true,
			ServiceMonitor: &vkov1.ServiceMonitorSpec{
				Enabled: true,
				Labels:  map[string]string{"release": "prometheus"},
			},
		}
	})
}

func getSM(t *testing.T, c client.Client, name string) *unstructured.Unstructured {
	t.Helper()
	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(builder.ServiceMonitorGVK())
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: name, Namespace: "default"}, got))
	return got
}

func TestReconcileServiceMonitor_Create_SelectsMetricsServiceAndIsOwned(t *testing.T) {
	const version = "6.0.0"
	v := serviceMonitorValkey()
	r, c := newReconcilerWithInterceptor(version, interceptor.Funcs{}, v)

	require.NoError(t, r.reconcileServiceMonitor(context.Background(), v))

	got := getSM(t, c, builder.ServiceMonitorName(v))
	matchLabels, found, err := unstructured.NestedStringMap(got.Object, "spec", "selector", "matchLabels")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "true", matchLabels[builder.MetricsServiceLabel],
		"the ServiceMonitor must select only the marked metrics Service")

	endpoints, found, err := unstructured.NestedSlice(got.Object, "spec", "endpoints")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, endpoints, 1)
	assert.Equal(t, builder.ExporterPortName, endpoints[0].(map[string]interface{})["port"])

	assert.Equal(t, "prometheus", got.GetLabels()["release"],
		"user-supplied serviceMonitor labels must reach the object")
	refs := got.GetOwnerReferences()
	require.Len(t, refs, 1)
	require.NotNil(t, refs[0].Controller)
	assert.True(t, *refs[0].Controller)
	assert.Equal(t, v.UID, refs[0].UID)
	assert.Equal(t, version, got.GetAnnotations()[builder.AnnotationOperatorVersion])
}

func TestReconcileServiceMonitor_Update_ReplacesDriftedSpec(t *testing.T) {
	const version = "6.1.0"
	v := serviceMonitorValkey()

	stale := builder.BuildServiceMonitor(v)
	stale.Object["spec"] = map[string]interface{}{
		"endpoints": []interface{}{map[string]interface{}{"port": "bogus"}},
	}
	stale.SetLabels(map[string]string{"stale": "yes"})
	controllerRefTo(v, stale)

	r, c := newReconcilerWithInterceptor(version, interceptor.Funcs{}, v, stale)

	require.NoError(t, r.reconcileServiceMonitor(context.Background(), v))

	got := getSM(t, c, builder.ServiceMonitorName(v))
	endpoints, _, err := unstructured.NestedSlice(got.Object, "spec", "endpoints")
	require.NoError(t, err)
	require.Len(t, endpoints, 1)
	assert.Equal(t, builder.ExporterPortName, endpoints[0].(map[string]interface{})["port"],
		"a drifted scrape port must be corrected")
	assert.NotContains(t, got.GetLabels(), "stale")
	require.Len(t, got.GetOwnerReferences(), 1,
		"the update must not change who controls the object")
	assert.Equal(t, version, got.GetAnnotations()[builder.AnnotationOperatorVersion])
}

func TestReconcileServiceMonitor_SecondPass_IssuesNoUpdate(t *testing.T) {
	v := serviceMonitorValkey()
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v)
	ctx := context.Background()

	require.NoError(t, r.reconcileServiceMonitor(ctx, v))
	first := getSM(t, c, builder.ServiceMonitorName(v)).GetResourceVersion()

	require.NoError(t, r.reconcileServiceMonitor(ctx, v))
	second := getSM(t, c, builder.ServiceMonitorName(v)).GetResourceVersion()

	assert.Equal(t, first, second, "an unchanged ServiceMonitor must not be rewritten")
}

// noMatchOnServiceMonitor models a cluster without the Prometheus-Operator CRDs:
// the RESTMapper cannot resolve the kind at all.
func noMatchOnServiceMonitor() error {
	return &meta.NoKindMatchError{
		GroupKind:        schema.GroupKind{Group: builder.ServiceMonitorGVK().Group, Kind: builder.ServiceMonitorGVK().Kind},
		SearchedVersions: []string{builder.ServiceMonitorGVK().Version},
	}
}

func TestReconcileServiceMonitor_SkipsWhenCRDMissing(t *testing.T) {
	v := serviceMonitorValkey()
	creates := 0
	funcs := interceptor.Funcs{
		Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			if obj.GetObjectKind().GroupVersionKind().Kind == builder.ServiceMonitorGVK().Kind {
				return noMatchOnServiceMonitor()
			}
			return nil
		},
		Create: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.CreateOption) error {
			creates++
			return nil
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v)

	require.NoError(t, r.reconcileServiceMonitor(context.Background(), v),
		"a missing monitoring.coreos.com CRD must not fail the reconcile")
	assert.Zero(t, creates, "no ServiceMonitor may be attempted when the CRD is absent")
}

func TestReconcileServiceMonitor_PropagatesGetError(t *testing.T) {
	v := serviceMonitorValkey()
	funcs := interceptor.Funcs{
		Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			if obj.GetObjectKind().GroupVersionKind().Kind == builder.ServiceMonitorGVK().Kind {
				return internalErr("servicemonitor read failed")
			}
			return nil
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v)

	err := r.reconcileServiceMonitor(context.Background(), v)

	require.Error(t, err, "a real API error must not be treated like a missing CRD")
	assert.Contains(t, err.Error(), "servicemonitor read failed")
}

func TestReconcileServiceMonitor_PropagatesUpdateError(t *testing.T) {
	v := serviceMonitorValkey()
	stale := builder.BuildServiceMonitor(v)
	stale.Object["spec"] = map[string]interface{}{"endpoints": []interface{}{}}
	controllerRefTo(v, stale)

	funcs := interceptor.Funcs{
		Update: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.UpdateOption) error {
			return internalErr("servicemonitor update rejected")
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v, stale)

	err := r.reconcileServiceMonitor(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "servicemonitor update rejected")
}

// --- cleanupServiceMonitor / cleanupMetricsService ---

func TestCleanupServiceMonitor_SkipsWhenCRDMissing(t *testing.T) {
	v := serviceMonitorValkey()
	funcs := interceptor.Funcs{
		Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
			return noMatchOnServiceMonitor()
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v)

	assert.NoError(t, r.cleanupServiceMonitor(context.Background(), v))
}

func TestCleanupServiceMonitor_PropagatesGetError(t *testing.T) {
	v := serviceMonitorValkey()
	funcs := interceptor.Funcs{
		Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
			return internalErr("servicemonitor read failed")
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v)

	err := r.cleanupServiceMonitor(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "servicemonitor read failed")
}

func TestCleanupServiceMonitor_PropagatesDeleteError(t *testing.T) {
	v := serviceMonitorValkey()
	sm := builder.BuildServiceMonitor(v)
	controllerRefTo(v, sm)
	funcs := interceptor.Funcs{
		Delete: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.DeleteOption) error {
			return internalErr("servicemonitor delete forbidden")
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v, sm)

	err := r.cleanupServiceMonitor(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "deleting ServiceMonitor")
	getSM(t, c, builder.ServiceMonitorName(v))
}

func TestCleanupMetricsService_PropagatesGetError(t *testing.T) {
	v := serviceMonitorValkey()
	funcs := interceptor.Funcs{
		Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			if _, ok := obj.(*corev1.Service); ok {
				return internalErr("service read failed")
			}
			return nil
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v)

	err := r.cleanupMetricsService(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "service read failed")
}

func TestCleanupMetricsService_PropagatesDeleteError(t *testing.T) {
	v := serviceMonitorValkey()
	svc := builder.BuildMetricsService(v)
	controllerRefTo(v, svc)
	funcs := interceptor.Funcs{
		Delete: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.DeleteOption) error {
			return internalErr("service delete forbidden")
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v, svc)

	err := r.cleanupMetricsService(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "deleting metrics Service")
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: builder.MetricsServiceName(v), Namespace: "default",
	}, &corev1.Service{}))
}

// --- sidecar RBAC write failures ---

func TestReconcileSidecarRBAC_StopsAtFirstFailure(t *testing.T) {
	v := newTestValkey("test", "default")
	funcs := interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*corev1.ServiceAccount); ok {
				return internalErr("serviceaccount quota")
			}
			return cl.Create(ctx, obj, opts...)
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v)

	err := r.reconcileSidecarRBAC(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating sidecar ServiceAccount")
	roleErr := c.Get(context.Background(), types.NamespacedName{
		Name: builder.BuildSidecarRole(v, nil).Name, Namespace: "default",
	}, &rbacv1.Role{})
	assert.True(t, apierrors.IsNotFound(roleErr),
		"the Role must not be created once the ServiceAccount step failed")
}

func TestReconcileSidecarServiceAccount_PropagatesUpdateError(t *testing.T) {
	v := newTestValkey("test", "default")
	stale := builder.BuildSidecarServiceAccount(v)
	stale.Labels = map[string]string{"stale": "yes"}
	ownedByValkey(t, v, stale)

	funcs := interceptor.Funcs{
		Update: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.UpdateOption) error {
			return internalErr("serviceaccount update rejected")
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v, stale)

	_, err := r.reconcileSidecarServiceAccount(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "updating sidecar ServiceAccount")
	got := &corev1.ServiceAccount{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: stale.Name, Namespace: "default",
	}, got))
	assert.Equal(t, "yes", got.Labels["stale"], "the stored object must be unchanged")
}

// --- sentinelRolloutComplete ---

func TestSentinelRolloutComplete_TrueWhenSentinelDisabled(t *testing.T) {
	v := newTestValkey("test", "default")
	r, _ := newTestReconciler(v)

	ready, err := r.sentinelRolloutComplete(context.Background(), v)

	require.NoError(t, err)
	assert.True(t, ready, "with Sentinel disabled no pod can be bound to the legacy Secret")
}

func TestSentinelRolloutComplete_PropagatesStatefulSetGetError(t *testing.T) {
	v := newTestValkeyUnified()
	funcs := interceptor.Funcs{
		Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			if _, ok := obj.(*appsv1.StatefulSet); ok {
				return internalErr("sts read failed")
			}
			return nil
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v)

	ready, err := r.sentinelRolloutComplete(context.Background(), v)

	require.Error(t, err)
	assert.False(t, ready, "an unreadable StatefulSet must never report the rollout as complete")
	assert.Contains(t, err.Error(), "get sentinel statefulset")
}

func TestSentinelRolloutComplete_FalseWhenUpdateRevisionEmpty(t *testing.T) {
	v := newTestValkeyUnified()
	stsName := common.StatefulSetName(v, common.ComponentSentinel)
	sts := stagedSentinelStatefulSet(v, stsName, builder.ValkeyTLSSecretName(v))
	// The StatefulSet controller has not computed a revision yet.
	sts.Status.UpdateRevision = ""
	objs := append([]client.Object{v, sts}, readySentinelPods(stsName, 3)...)
	r, _ := newTestReconciler(objs...)

	ready, err := r.sentinelRolloutComplete(context.Background(), v)

	require.NoError(t, err)
	assert.False(t, ready, "without an UpdateRevision no pod can be proven to be on the new revision")
}

func TestSentinelRolloutComplete_FalseWhenPodMissing(t *testing.T) {
	v := newTestValkeyUnified()
	stsName := common.StatefulSetName(v, common.ComponentSentinel)
	sts := stagedSentinelStatefulSet(v, stsName, builder.ValkeyTLSSecretName(v))
	// Only two of the three pods exist — the third is being recreated.
	objs := append([]client.Object{v, sts}, readySentinelPods(stsName, 2)...)
	r, _ := newTestReconciler(objs...)

	ready, err := r.sentinelRolloutComplete(context.Background(), v)

	require.NoError(t, err)
	assert.False(t, ready, "a pod that does not exist yet cannot be counted as rolled out")
}

func TestSentinelRolloutComplete_PropagatesPodGetError(t *testing.T) {
	v := newTestValkeyUnified()
	stsName := common.StatefulSetName(v, common.ComponentSentinel)
	sts := stagedSentinelStatefulSet(v, stsName, builder.ValkeyTLSSecretName(v))
	funcs := interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Pod); ok {
				return internalErr("pod read failed")
			}
			return cl.Get(ctx, key, obj, opts...)
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v, sts)

	ready, err := r.sentinelRolloutComplete(context.Background(), v)

	require.Error(t, err)
	assert.False(t, ready)
	assert.Contains(t, err.Error(), "get sentinel pod "+stsName+"-0")
}

// --- sentinelStatefulSetUsesSecret ---

func TestSentinelStatefulSetUsesSecret(t *testing.T) {
	withVolume := func(vol corev1.Volume) *appsv1.StatefulSet {
		return &appsv1.StatefulSet{
			Spec: appsv1.StatefulSetSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{Volumes: []corev1.Volume{vol}},
				},
			},
		}
	}

	tests := []struct {
		name string
		sts  *appsv1.StatefulSet
		want bool
	}{
		{
			name: "matching secret volume",
			sts: withVolume(corev1.Volume{
				Name:         builder.TLSVolumeName,
				VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "unified-tls"}},
			}),
			want: true,
		},
		{
			name: "tls volume mounts a different secret",
			sts: withVolume(corev1.Volume{
				Name:         builder.TLSVolumeName,
				VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "legacy-tls"}},
			}),
			want: false,
		},
		{
			name: "tls volume is not a Secret source",
			sts: withVolume(corev1.Volume{
				Name:         builder.TLSVolumeName,
				VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{}},
			}),
			want: false,
		},
		{
			name: "no tls volume at all",
			sts: withVolume(corev1.Volume{
				Name:         "data",
				VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "unified-tls"}},
			}),
			want: false,
		},
		{
			name: "no volumes at all",
			sts:  &appsv1.StatefulSet{},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, sentinelStatefulSetUsesSecret(tc.sts, "unified-tls"))
		})
	}
}

// --- deleteLegacyServices ---

func TestDeleteLegacyServices_PropagatesGetError(t *testing.T) {
	v := newTestValkey("test", "default")
	funcs := interceptor.Funcs{
		Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
			if _, ok := obj.(*corev1.Service); ok {
				return internalErr("service read failed")
			}
			return nil
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v)

	err := r.deleteLegacyServices(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "checking legacy service test")
}

func TestDeleteLegacyServices_PropagatesDeleteError(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.UID = types.UID("legacy-uid") })
	legacy := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test",
			Namespace:       "default",
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "vko.gtrfc.com/v1", Kind: "Valkey", Name: "test", UID: v.UID}},
		},
	}
	funcs := interceptor.Funcs{
		Delete: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.DeleteOption) error {
			return internalErr("service delete forbidden")
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v, legacy)

	err := r.deleteLegacyServices(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "deleting legacy service test")
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: "test", Namespace: "default"}, &corev1.Service{}))
}

func TestDeleteLegacyServices_SkipsServiceOwnedByAnotherInstance(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.UID = types.UID("legacy-uid") })
	foreign := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{APIVersion: "vko.gtrfc.com/v1", Kind: "Valkey", Name: "other", UID: types.UID("someone-else")},
			},
		},
	}
	deletes := 0
	funcs := interceptor.Funcs{
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			deletes++
			return cl.Delete(ctx, obj, opts...)
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v, foreign)

	require.NoError(t, r.deleteLegacyServices(context.Background(), v))

	assert.Zero(t, deletes, "a Service owned by a different UID must never be deleted")
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: "test", Namespace: "default"}, &corev1.Service{}))
}

// --- Get-error propagation across the create-or-update reconcilers ---

// failGetFor makes every Get whose target is the given object type fail with a
// non-NotFound error. A reconciler that treats such an error as "absent" would
// try to Create over an existing object and hot-loop on AlreadyExists.
func failGetFor(match func(client.Object) bool, msg string) interceptor.Funcs {
	return interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if match(obj) {
				return internalErr(msg)
			}
			return cl.Get(ctx, key, obj, opts...)
		},
	}
}

func TestReconcilers_PropagateNonNotFoundGetErrors(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})

	isConfigMap := func(o client.Object) bool { _, ok := o.(*corev1.ConfigMap); return ok }
	isService := func(o client.Object) bool { _, ok := o.(*corev1.Service); return ok }
	isStatefulSet := func(o client.Object) bool { _, ok := o.(*appsv1.StatefulSet); return ok }
	isRole := func(o client.Object) bool { _, ok := o.(*rbacv1.Role); return ok }
	isRoleBinding := func(o client.Object) bool { _, ok := o.(*rbacv1.RoleBinding); return ok }
	isServiceAccount := func(o client.Object) bool { _, ok := o.(*corev1.ServiceAccount); return ok }

	cases := []struct {
		name  string
		match func(client.Object) bool
		run   func(r *ValkeyReconciler, ctx context.Context) error
	}{
		{"configMap", isConfigMap, func(r *ValkeyReconciler, ctx context.Context) error {
			return r.reconcileConfigMap(ctx, v)
		}},
		{"replicaConfigMap", isConfigMap, func(r *ValkeyReconciler, ctx context.Context) error {
			return r.reconcileReplicaConfigMap(ctx, v)
		}},
		{"service", isService, func(r *ValkeyReconciler, ctx context.Context) error {
			return r.reconcileHeadlessService(ctx, v)
		}},
		{"statefulSet", isStatefulSet, func(r *ValkeyReconciler, ctx context.Context) error {
			return r.reconcileStatefulSet(ctx, v)
		}},
		{"sentinelStatefulSet", isStatefulSet, func(r *ValkeyReconciler, ctx context.Context) error {
			return r.reconcileSentinelStatefulSet(ctx, v)
		}},
		{"sidecarServiceAccount", isServiceAccount, func(r *ValkeyReconciler, ctx context.Context) error {
			_, err := r.reconcileSidecarServiceAccount(ctx, v)
			return err
		}},
		{"sidecarRole", isRole, func(r *ValkeyReconciler, ctx context.Context) error {
			_, err := r.reconcileSidecarRole(ctx, v)
			return err
		}},
		{"sidecarRoleBinding", isRoleBinding, func(r *ValkeyReconciler, ctx context.Context) error {
			return r.reconcileSidecarRoleBinding(ctx, v)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			creates := 0
			funcs := failGetFor(tc.match, "apiserver unavailable")
			funcs.Create = func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.CreateOption) error {
				creates++
				return nil
			}
			r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v)

			err := tc.run(r, context.Background())

			require.Error(t, err, "a non-NotFound Get error must abort the step")
			assert.Contains(t, err.Error(), "apiserver unavailable")
			assert.Zero(t, creates, "an unreadable object must not be treated as absent and re-created")
		})
	}
}

// --- sidecar RBAC write-error branches ---

func TestReconcileSidecarRole_PropagatesCreateError(t *testing.T) {
	v := newTestValkey("test", "default")
	funcs := interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*rbacv1.Role); ok {
				return internalErr("role create denied")
			}
			return cl.Create(ctx, obj, opts...)
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v)

	_, err := r.reconcileSidecarRole(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating sidecar Role")
}

func TestReconcileSidecarRole_UpdatesDriftedRulesAndPropagatesUpdateError(t *testing.T) {
	v := newTestValkey("test", "default")
	desired := builder.BuildSidecarRole(v, nil)

	// A Role whose rules were narrowed by hand: the sidecar could no longer patch
	// its own pod label, so the drift must be corrected.
	stale := desired.DeepCopy()
	stale.Rules = []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}}}
	ownedByValkey(t, v, stale)

	t.Run("update succeeds", func(t *testing.T) {
		r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, stale.DeepCopy())

		mustReconcileSidecarRole(t, r, v)

		got := &rbacv1.Role{}
		require.NoError(t, c.Get(context.Background(),
			types.NamespacedName{Name: desired.Name, Namespace: "default"}, got))
		assert.Equal(t, desired.Rules, got.Rules, "narrowed rules must be restored")
	})

	t.Run("update error propagates", func(t *testing.T) {
		funcs := interceptor.Funcs{
			Update: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.UpdateOption) error {
				return internalErr("role update denied")
			},
		}
		r, c := newReconcilerWithInterceptor("1.0.0", funcs, v, stale.DeepCopy())

		_, err := r.reconcileSidecarRole(context.Background(), v)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "updating sidecar Role")
		got := &rbacv1.Role{}
		require.NoError(t, c.Get(context.Background(),
			types.NamespacedName{Name: desired.Name, Namespace: "default"}, got))
		assert.Equal(t, stale.Rules, got.Rules, "the stored Role must be unchanged")
	})
}

func TestReconcileSidecarRoleBinding_PropagatesCreateError(t *testing.T) {
	v := newTestValkey("test", "default")
	funcs := interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*rbacv1.RoleBinding); ok {
				return internalErr("rolebinding create denied")
			}
			return cl.Create(ctx, obj, opts...)
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v)

	err := r.reconcileSidecarRoleBinding(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating sidecar RoleBinding")
}

func TestReconcileSidecarRoleBinding_PropagatesUpdateError(t *testing.T) {
	v := newTestValkey("test", "default")
	stale := builder.BuildSidecarRoleBinding(v)
	stale.Subjects = nil // drift: the binding no longer names the sidecar ServiceAccount
	ownedByValkey(t, v, stale)

	funcs := interceptor.Funcs{
		Update: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.UpdateOption) error {
			return internalErr("rolebinding update denied")
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v, stale)

	err := r.reconcileSidecarRoleBinding(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "updating sidecar RoleBinding")
	got := &rbacv1.RoleBinding{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: stale.Name, Namespace: "default"}, got))
	assert.Empty(t, got.Subjects, "the stored RoleBinding must be unchanged")
}

// staleRoleRefBinding returns a RoleBinding this Valkey owns whose immutable
// RoleRef points somewhere else, forcing the delete-and-recreate path. Ownership is
// part of the fixture: a binding the operator does not own is refused before the
// RoleRef is ever compared.
func staleRoleRefBinding(t *testing.T, v *vkov1.Valkey) *rbacv1.RoleBinding {
	t.Helper()
	rb := builder.BuildSidecarRoleBinding(v)
	rb.RoleRef = rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "cluster-admin"}
	ownedByValkey(t, v, rb)
	return rb
}

func TestReconcileSidecarRoleBinding_PropagatesDeleteErrorOnRoleRefChange(t *testing.T) {
	v := newTestValkey("test", "default")
	stale := staleRoleRefBinding(t, v)

	creates := 0
	funcs := interceptor.Funcs{
		Delete: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.DeleteOption) error {
			return internalErr("rolebinding delete denied")
		},
		Create: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.CreateOption) error {
			creates++
			return nil
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v, stale)

	err := r.reconcileSidecarRoleBinding(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "deleting sidecar RoleBinding for recreation")
	assert.Zero(t, creates, "the recreate must not run when the delete failed")
	got := &rbacv1.RoleBinding{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: stale.Name, Namespace: "default"}, got))
	assert.Equal(t, "cluster-admin", got.RoleRef.Name)
}

// TestReconcileSidecarRoleBinding_PropagatesRecreateError covers the window in
// which the old RoleBinding is already gone and the replacement fails: the
// sidecar loses its pod-patch permission until a later reconcile succeeds, so
// the error must surface rather than be swallowed.
func TestReconcileSidecarRoleBinding_PropagatesRecreateError(t *testing.T) {
	v := newTestValkey("test", "default")
	stale := staleRoleRefBinding(t, v)

	funcs := interceptor.Funcs{
		Create: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.CreateOption) error {
			if _, ok := obj.(*rbacv1.RoleBinding); ok {
				return internalErr("rolebinding recreate denied")
			}
			return nil
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v, stale)

	err := r.reconcileSidecarRoleBinding(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "recreating sidecar RoleBinding")
	getErr := c.Get(context.Background(),
		types.NamespacedName{Name: stale.Name, Namespace: "default"}, &rbacv1.RoleBinding{})
	assert.True(t, apierrors.IsNotFound(getErr),
		"the old RoleBinding is gone at this point - the error must be reported so the next pass retries")
}

func TestReconcileSidecarRBAC_StopsWhenRoleFails(t *testing.T) {
	v := newTestValkey("test", "default")
	funcs := interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*rbacv1.Role); ok {
				return internalErr("role create denied")
			}
			return cl.Create(ctx, obj, opts...)
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v)

	err := r.reconcileSidecarRBAC(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating sidecar Role")
	// The ServiceAccount ran first and must exist; the RoleBinding must not.
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: builder.SidecarServiceAccountName(v), Namespace: "default",
	}, &corev1.ServiceAccount{}))
	rbErr := c.Get(context.Background(), types.NamespacedName{
		Name: builder.BuildSidecarRoleBinding(v).Name, Namespace: "default",
	}, &rbacv1.RoleBinding{})
	assert.True(t, apierrors.IsNotFound(rbErr),
		"a RoleBinding without its Role would grant nothing and must not be created")
}

// --- reconcileSentinelResources error labelling ---

func TestReconcileSentinelResources_WrapsHeadlessServiceError(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	funcs := interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*corev1.Service); ok {
				return internalErr("service create denied")
			}
			return cl.Create(ctx, obj, opts...)
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v)

	err := r.reconcileSentinelResources(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "sentinel headless service:")
	stsErr := c.Get(context.Background(), types.NamespacedName{
		Name: common.StatefulSetName(v, common.ComponentSentinel), Namespace: "default",
	}, &appsv1.StatefulSet{})
	assert.True(t, apierrors.IsNotFound(stsErr),
		"Sentinel pods without their headless Service cannot form a quorum, so the StatefulSet must wait")
}

func TestReconcileSentinelResources_WrapsStatefulSetError(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	funcs := interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*appsv1.StatefulSet); ok {
				return internalErr("statefulset create denied")
			}
			return cl.Create(ctx, obj, opts...)
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v)

	err := r.reconcileSentinelResources(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "sentinel statefulset:")
}

func TestReconcileNetworkPolicies_WrapsObserverPolicyError(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{Enabled: true}
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})
	observerNP := builder.ObserverNetworkPolicyName(v)
	funcs := interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if obj.GetName() == observerNP {
				return internalErr("admission denied")
			}
			return cl.Create(ctx, obj, opts...)
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v)

	err := r.reconcileNetworkPolicies(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "observer networkpolicy:")
}

// --- cleanupObserverDeployment ---

func TestCleanupObserverDeployment_RemovesDeploymentAndPolicy(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{Enabled: true}
		// Observer intentionally left disabled: this is the "turned off" path.
	})
	deploy := builder.BuildObserverDeployment(v, "img")
	ownedByValkey(t, v, deploy)
	np := builder.BuildObserverNetworkPolicy(v)
	ownedByValkey(t, v, np)
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, deploy, np)

	require.NoError(t, r.cleanupObserverDeployment(context.Background(), v))

	deployErr := c.Get(context.Background(), types.NamespacedName{
		Name: builder.ObserverDeploymentName(v), Namespace: "default",
	}, &appsv1.Deployment{})
	assert.True(t, apierrors.IsNotFound(deployErr))
	npErr := c.Get(context.Background(), types.NamespacedName{
		Name: builder.ObserverNetworkPolicyName(v), Namespace: "default",
	}, &networkingv1.NetworkPolicy{})
	assert.True(t, apierrors.IsNotFound(npErr))
}

func TestCleanupObserverDeployment_LeavesPolicyWhenNetworkPolicyDisabled(t *testing.T) {
	v := newTestValkey("test", "default")
	deploy := builder.BuildObserverDeployment(v, "img")
	ownedByValkey(t, v, deploy)
	np := builder.BuildObserverNetworkPolicy(v)
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, deploy, np)

	require.NoError(t, r.cleanupObserverDeployment(context.Background(), v))

	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: builder.ObserverNetworkPolicyName(v), Namespace: "default",
	}, &networkingv1.NetworkPolicy{}),
		"with networkPolicy disabled the operator does not manage the observer policy")
}

func TestCleanupObserverDeployment_PropagatesDeploymentDeleteError(t *testing.T) {
	v := newTestValkey("test", "default")
	deploy := builder.BuildObserverDeployment(v, "img")
	ownedByValkey(t, v, deploy)
	funcs := interceptor.Funcs{
		Delete: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.DeleteOption) error {
			return internalErr("deployment delete denied")
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v, deploy)

	err := r.cleanupObserverDeployment(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "deleting observer deployment")
}

func TestCleanupObserverDeployment_PropagatesNetworkPolicyDeleteError(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.NetworkPolicy = &vkov1.NetworkPolicySpec{Enabled: true}
	})
	np := builder.BuildObserverNetworkPolicy(v)
	ownedByValkey(t, v, np)
	funcs := interceptor.Funcs{
		Delete: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.DeleteOption) error {
			if _, ok := obj.(*networkingv1.NetworkPolicy); ok {
				return internalErr("networkpolicy delete denied")
			}
			return nil
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v, np)

	err := r.cleanupObserverDeployment(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "deleting observer NetworkPolicy")
}

// --- isObserverDeploymentReady ---

func TestIsObserverDeploymentReady(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Observer = &vkov1.ObserverSpec{Enabled: true}
	})

	t.Run("absent deployment is not ready", func(t *testing.T) {
		r, _ := newTestReconciler(v)
		assert.False(t, r.isObserverDeploymentReady(context.Background(), v))
	})

	t.Run("zero ready replicas is not ready", func(t *testing.T) {
		deploy := builder.BuildObserverDeployment(v, "img")
		deploy.Status.ReadyReplicas = 0
		r, _ := newTestReconciler(v, deploy)
		assert.False(t, r.isObserverDeploymentReady(context.Background(), v))
	})

	t.Run("one ready replica is ready", func(t *testing.T) {
		deploy := builder.BuildObserverDeployment(v, "img")
		deploy.Status.ReadyReplicas = 1
		r, _ := newTestReconciler(v, deploy)
		assert.True(t, r.isObserverDeploymentReady(context.Background(), v))
	})
}

// --- cleanseCertificateSpec ---

func TestCleanseCertificateSpec_NilSpecIsSafe(t *testing.T) {
	// unstructured.NestedMap returns nil for a Certificate stored without a spec;
	// the cleanser must tolerate it rather than panic mid-reconcile.
	assert.NotPanics(t, func() { cleanseCertificateSpec(nil) })
}

// --- reconcileSidecarRole: the resourceNames grant (ADR 0012 D8 step 3) ---

func TestReconcileSidecarRole_GrantsPatchOnThisClustersPodsOnly(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v)

	mustReconcileSidecarRole(t, r, v)

	role := &rbacv1.Role{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: builder.SidecarServiceAccountName(v), Namespace: "default",
	}, role))
	require.Len(t, role.Rules, 1)
	assert.Equal(t, []string{"patch"}, role.Rules[0].Verbs)
	assert.Equal(t, []string{"test-0", "test-1", "test-2"}, role.Rules[0].ResourceNames,
		"a namespace-wide patch grant lets one cluster's sidecar token stamp another "+
			"cluster's pods, and the operator consumes that stamp as promotion evidence")
}

func TestReconcileSidecarRole_KeepsTerminatingPodsInTheGrant(t *testing.T) {
	// Scale-down 5 -> 3: the spec asks for three, five pods still exist. The two on
	// their way out still run a drain handler that patches its own draining label.
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	pods := []client.Object{
		masterLabeledPod(v, 0, common.RoleMaster),
		masterLabeledPod(v, 1, common.RoleReplica),
		masterLabeledPod(v, 2, common.RoleReplica),
		masterLabeledPod(v, 3, common.RoleReplica),
		masterLabeledPod(v, 4, common.RoleReplica),
	}
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{},
		withDataStatefulSet(append([]client.Object{v}, pods...))...)

	mustReconcileSidecarRole(t, r, v)

	role := &rbacv1.Role{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: builder.SidecarServiceAccountName(v), Namespace: "default",
	}, role))
	require.Len(t, role.Rules, 1)
	assert.Equal(t, []string{"test-0", "test-1", "test-2", "test-3", "test-4"},
		role.Rules[0].ResourceNames,
		"a departing master that cannot set its own draining label keeps taking writes "+
			"while it fails over")
}

func TestReconcileSidecarRole_NarrowsALegacyNamespaceWideRole(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 2 })
	legacy := builder.BuildSidecarRole(v, nil)
	legacy.Rules = []rbacv1.PolicyRule{{
		APIGroups: []string{""},
		Resources: []string{"pods"},
		Verbs:     []string{"get", "list", "patch"},
	}}
	ownedByValkey(t, v, legacy)
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, legacy)

	mustReconcileSidecarRole(t, r, v)

	role := &rbacv1.Role{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{
		Name: builder.SidecarServiceAccountName(v), Namespace: "default",
	}, role))
	require.Len(t, role.Rules, 1)
	assert.Equal(t, []string{"patch"}, role.Rules[0].Verbs)
	assert.Equal(t, []string{"test-0", "test-1"}, role.Rules[0].ResourceNames,
		"an existing cluster must narrow on its next reconcile, with no migration step")
}

func TestReconcileSidecarRole_FailsTheStepWhenThePodListFails(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) { v.Spec.Replicas = 3 })
	funcs := interceptor.Funcs{
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*corev1.PodList); ok {
				return internalErr("pod list denied")
			}
			return cl.List(ctx, list, opts...)
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v)

	_, err := r.reconcileSidecarRole(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing data pods for the sidecar Role")
	// Narrowing on an incomplete pod list would revoke a live pod's grant, so the
	// step writes nothing at all and the existing (wider) Role stays.
	getErr := c.Get(context.Background(), types.NamespacedName{
		Name: builder.SidecarServiceAccountName(v), Namespace: "default",
	}, &rbacv1.Role{})
	assert.True(t, apierrors.IsNotFound(getErr))
}

// --- reconcileObserver: the observer's own Role-less ServiceAccount (ADR 0012 D8 step 2) ---

func TestReconcileObserver_CreatesTheServiceAccountTheDeploymentNames(t *testing.T) {
	v := observerValkey()
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v)
	ctx := context.Background()

	require.NoError(t, r.reconcileObserver(ctx, v))

	sa := &corev1.ServiceAccount{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{
		Name: builder.ObserverServiceAccountName(v), Namespace: "default",
	}, sa))
	assert.Len(t, sa.OwnerReferences, 1, "the ServiceAccount must be garbage-collected with the CR")

	deploy := &appsv1.Deployment{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{
		Name: builder.ObserverDeploymentName(v), Namespace: "default",
	}, deploy))
	assert.Equal(t, sa.Name, deploy.Spec.Template.Spec.ServiceAccountName)
	assert.NotEqual(t, builder.SidecarServiceAccountName(v), deploy.Spec.Template.Spec.ServiceAccountName)

	// Nothing binds a Role to it: that is the whole point of the split.
	roleBindings := &rbacv1.RoleBindingList{}
	require.NoError(t, c.List(ctx, roleBindings, client.InNamespace("default")))
	for i := range roleBindings.Items {
		for _, s := range roleBindings.Items[i].Subjects {
			assert.NotEqual(t, sa.Name, s.Name,
				"the observer ServiceAccount must stay bound to nothing")
		}
	}
}

func TestReconcileObserver_StopsBeforeTheDeploymentWhenTheServiceAccountFails(t *testing.T) {
	v := observerValkey()
	funcs := interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*corev1.ServiceAccount); ok {
				return internalErr("serviceaccount create denied")
			}
			return cl.Create(ctx, obj, opts...)
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v)
	ctx := context.Background()

	err := r.reconcileObserver(ctx, v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating Observer ServiceAccount")
	// A pod naming a ServiceAccount that does not exist is rejected by admission, so
	// creating the Deployment first would only produce an unschedulable pod.
	getErr := c.Get(ctx, types.NamespacedName{
		Name: builder.ObserverDeploymentName(v), Namespace: "default",
	}, &appsv1.Deployment{})
	assert.True(t, apierrors.IsNotFound(getErr))
}

func TestCleanupObserver_DeletesTheOwnedServiceAccount(t *testing.T) {
	v := observerValkey()
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v)
	ctx := context.Background()
	require.NoError(t, r.reconcileObserver(ctx, v))

	v.Spec.Observer = &vkov1.ObserverSpec{Enabled: false}
	require.NoError(t, r.reconcileObserver(ctx, v))

	getErr := c.Get(ctx, types.NamespacedName{
		Name: builder.ObserverServiceAccountName(v), Namespace: "default",
	}, &corev1.ServiceAccount{})
	assert.True(t, apierrors.IsNotFound(getErr))
}

func TestCleanupObserver_LeavesAForeignServiceAccountAlone(t *testing.T) {
	v := observerValkey()
	v.Spec.Observer = &vkov1.ObserverSpec{Enabled: false}

	// <cr-name>-observer is a name a CR author can aim at a pre-existing
	// ServiceAccount; deleting it would take every Role bound to it out of service
	// (ADR 0006).
	foreign := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
		Name: builder.ObserverServiceAccountName(v), Namespace: "default",
	}}
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v, foreign)
	ctx := context.Background()

	require.NoError(t, r.reconcileObserver(ctx, v))

	assert.NoError(t, c.Get(ctx, types.NamespacedName{
		Name: builder.ObserverServiceAccountName(v), Namespace: "default",
	}, &corev1.ServiceAccount{}), "a ServiceAccount the operator does not own must survive")
}
