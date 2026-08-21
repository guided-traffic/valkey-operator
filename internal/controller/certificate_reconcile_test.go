package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

// --- helpers ---

// certGVK is the GroupVersionKind the controller uses for cert-manager
// Certificates. Declared here rather than reusing the production constants so a
// silent rename of those constants shows up as a failing test.
func certGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: "cert-manager.io", Version: "v1", Kind: "Certificate"}
}

func newEmptyCert() *unstructured.Unstructured {
	c := &unstructured.Unstructured{}
	c.SetGroupVersionKind(certGVK())
	return c
}

// getCert reads a cert-manager Certificate from the fake client.
func getCert(t *testing.T, c client.Client, name string) *unstructured.Unstructured {
	t.Helper()
	got := newEmptyCert()
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: name, Namespace: "default"}, got))
	return got
}

// certDNSNames extracts spec.dnsNames as a []string.
func certDNSNames(t *testing.T, u *unstructured.Unstructured) []string {
	t.Helper()
	names, found, err := unstructured.NestedStringSlice(u.Object, "spec", "dnsNames")
	require.NoError(t, err)
	require.True(t, found, "spec.dnsNames must be set")
	return names
}

// newCertManagerValkey returns a TLS+cert-manager enabled Valkey with a UID, so
// owner references carry something distinguishable.
func newCertManagerValkey(opts ...func(*vkov1.Valkey)) *vkov1.Valkey {
	base := func(v *vkov1.Valkey) {
		v.UID = types.UID("valkey-uid-1")
		v.Spec.Replicas = 3
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "cluster-ca"},
			},
		}
	}
	return newTestValkey("test", "default", append([]func(*vkov1.Valkey){base}, opts...)...)
}

func withSentinel(v *vkov1.Valkey) {
	v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
}

func withUnifiedCert(v *vkov1.Valkey) {
	v.Spec.TLS.UnifiedCertificate = true
}

// internalErr returns a non-NotFound API error, the shape a reconciler must
// propagate rather than swallow.
func internalErr(msg string) error {
	return apierrors.NewInternalError(fmt.Errorf("%s", msg))
}

// newReconcilerWithInterceptor builds a reconciler over a fake client with the
// given interceptors, mirroring newTestReconciler's scheme and version handling.
func newReconcilerWithInterceptor(
	version string, funcs interceptor.Funcs, objs ...client.Object,
) (*ValkeyReconciler, client.Client) {
	s := testScheme()
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&vkov1.Valkey{}, &appsv1.StatefulSet{}).
		WithInterceptorFuncs(funcs).
		Build()
	return &ValkeyReconciler{
		Client:          c,
		Scheme:          s,
		InstanceChecker: &mockInstanceChecker{},
		OperatorVersion: version,
	}, c
}

// failCertGet makes every Get of a cert-manager Certificate fail with a
// non-NotFound error.
func failCertGet(msg string) interceptor.Funcs {
	return interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if obj.GetObjectKind().GroupVersionKind().Kind == certManagerKindCertificate {
				return internalErr(msg)
			}
			return cl.Get(ctx, key, obj, opts...)
		},
	}
}

// --- reconcileCertificate: create path ---

func TestReconcileCertificate_Create_WritesSpecOwnerRefAndVersion(t *testing.T) {
	const version = "9.9.9"
	v := newCertManagerValkey()
	r, c := newReconcilerWithInterceptor(version, interceptor.Funcs{}, v)

	require.NoError(t, r.reconcileCertificate(context.Background(), v, builder.BuildValkeyCertificate(v)))

	got := getCert(t, c, builder.ValkeyCertificateName(v))

	secretName, _, _ := unstructured.NestedString(got.Object, "spec", "secretName")
	assert.Equal(t, builder.ValkeyTLSSecretName(v), secretName)

	issuerName, _, _ := unstructured.NestedString(got.Object, "spec", "issuerRef", "name")
	issuerKind, _, _ := unstructured.NestedString(got.Object, "spec", "issuerRef", "kind")
	assert.Equal(t, "cluster-ca", issuerName)
	assert.Equal(t, "ClusterIssuer", issuerKind)

	dnsNames := certDNSNames(t, got)
	assert.Contains(t, dnsNames, "test-0.test-headless.default.svc.cluster.local",
		"per-pod SAN must be present")
	assert.Contains(t, dnsNames, builder.RWServiceName(v))
	assert.Contains(t, dnsNames, "localhost")

	assert.Equal(t, version, got.GetAnnotations()[builder.AnnotationOperatorVersion])
	assert.Equal(t, common.ComponentValkey,
		got.GetLabels()["app.kubernetes.io/component"], "component label must be set")

	refs := got.GetOwnerReferences()
	require.Len(t, refs, 1, "certificate must be owned by the Valkey CR")
	assert.Equal(t, "Valkey", refs[0].Kind)
	assert.Equal(t, "test", refs[0].Name)
	assert.Equal(t, v.UID, refs[0].UID)
	require.NotNil(t, refs[0].Controller)
	assert.True(t, *refs[0].Controller, "owner reference must be the controller ref")
	require.NotNil(t, refs[0].BlockOwnerDeletion)
	assert.True(t, *refs[0].BlockOwnerDeletion)
}

// --- reconcileCertificate: no-change path ---

func TestReconcileCertificate_SecondPass_IssuesNoUpdate(t *testing.T) {
	v := newCertManagerValkey()
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v)
	ctx := context.Background()

	require.NoError(t, r.reconcileCertificate(ctx, v, builder.BuildValkeyCertificate(v)))
	rvAfterCreate := getCert(t, c, builder.ValkeyCertificateName(v)).GetResourceVersion()

	require.NoError(t, r.reconcileCertificate(ctx, v, builder.BuildValkeyCertificate(v)))
	rvAfterSecond := getCert(t, c, builder.ValkeyCertificateName(v)).GetResourceVersion()

	assert.Equal(t, rvAfterCreate, rvAfterSecond,
		"an unchanged Certificate must not be rewritten (hot-loop guard)")
}

// TestReconcileCertificate_WebhookDefaultedPrivateKey_IssuesNoUpdate pins the
// reason cleanseCertificateSpec exists: cert-manager's admission webhook adds
// spec.privateKey to the stored object. If it were compared, every reconcile
// would issue an Update and fight the webhook forever.
func TestReconcileCertificate_WebhookDefaultedPrivateKey_IssuesNoUpdate(t *testing.T) {
	const version = "1.0.0"
	v := newCertManagerValkey()

	// Stage the object as the apiserver would return it: our spec plus the
	// webhook-added privateKey block, already annotated with the current version.
	stored := builder.BuildValkeyCertificate(v)
	builder.ApplyOperatorVersion(stored, version)
	spec, _, err := unstructured.NestedMap(stored.Object, "spec")
	require.NoError(t, err)
	spec["privateKey"] = map[string]interface{}{
		"rotationPolicy": "Always",
		"algorithm":      "RSA",
	}
	require.NoError(t, unstructured.SetNestedMap(stored.Object, spec, "spec"))

	r, c := newReconcilerWithInterceptor(version, interceptor.Funcs{}, v, stored)
	ctx := context.Background()
	rvBefore := getCert(t, c, builder.ValkeyCertificateName(v)).GetResourceVersion()

	require.NoError(t, r.reconcileCertificate(ctx, v, builder.BuildValkeyCertificate(v)))

	after := getCert(t, c, builder.ValkeyCertificateName(v))
	assert.Equal(t, rvBefore, after.GetResourceVersion(),
		"webhook-defaulted spec.privateKey must not trigger an Update")
	_, found, err := unstructured.NestedMap(after.Object, "spec", "privateKey")
	require.NoError(t, err)
	assert.True(t, found, "the webhook's privateKey block must survive untouched")
}

// --- reconcileCertificate: update path ---

func TestReconcileCertificate_Update_ReplacesDriftedSpecLabelsAndOwnerRef(t *testing.T) {
	const version = "2.0.0"
	v := newCertManagerValkey()

	stale := builder.BuildValkeyCertificate(v)
	require.NoError(t, unstructured.SetNestedStringSlice(
		stale.Object, []string{"stale.example.com"}, "spec", "dnsNames"))
	require.NoError(t, unstructured.SetNestedField(
		stale.Object, "wrong-secret", "spec", "secretName"))
	stale.SetLabels(map[string]string{"leftover": "yes"})
	stale.SetOwnerReferences(nil)

	r, c := newReconcilerWithInterceptor(version, interceptor.Funcs{}, v, stale)

	require.NoError(t, r.reconcileCertificate(context.Background(), v, builder.BuildValkeyCertificate(v)))

	got := getCert(t, c, builder.ValkeyCertificateName(v))
	dnsNames := certDNSNames(t, got)
	assert.NotContains(t, dnsNames, "stale.example.com", "stale SAN must be dropped")
	assert.Contains(t, dnsNames, "test-0.test-headless.default.svc.cluster.local")

	secretName, _, _ := unstructured.NestedString(got.Object, "spec", "secretName")
	assert.Equal(t, builder.ValkeyTLSSecretName(v), secretName)

	assert.NotContains(t, got.GetLabels(), "leftover", "labels must be replaced, not merged")
	assert.Equal(t, "valkey", got.GetLabels()["app.kubernetes.io/name"])
	assert.Equal(t, version, got.GetAnnotations()[builder.AnnotationOperatorVersion])
	require.Len(t, got.GetOwnerReferences(), 1, "a missing owner reference must be restored")
	assert.Equal(t, v.UID, got.GetOwnerReferences()[0].UID)
}

func TestReconcileCertificate_Update_OnOperatorVersionBumpAlone(t *testing.T) {
	v := newCertManagerValkey()

	// Identical spec, stamped by an older operator version.
	stored := builder.BuildValkeyCertificate(v)
	builder.ApplyOperatorVersion(stored, "1.0.0")

	r, c := newReconcilerWithInterceptor("1.1.0", interceptor.Funcs{}, v, stored)

	require.NoError(t, r.reconcileCertificate(context.Background(), v, builder.BuildValkeyCertificate(v)))

	got := getCert(t, c, builder.ValkeyCertificateName(v))
	assert.Equal(t, "1.1.0", got.GetAnnotations()[builder.AnnotationOperatorVersion],
		"the version annotation alone must be enough to trigger an Update")
}

// --- reconcileCertificate: error branches ---

func TestReconcileCertificate_PropagatesGetError(t *testing.T) {
	v := newCertManagerValkey()
	r, _ := newReconcilerWithInterceptor("1.0.0", failCertGet("apiserver down"), v)

	err := r.reconcileCertificate(context.Background(), v, builder.BuildValkeyCertificate(v))

	require.Error(t, err, "a non-NotFound Get error must not be mistaken for absence")
	assert.Contains(t, err.Error(), "apiserver down")
}

func TestReconcileCertificate_PropagatesCreateError(t *testing.T) {
	v := newCertManagerValkey()
	funcs := interceptor.Funcs{
		Create: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.CreateOption) error {
			return internalErr("quota exceeded")
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v)

	err := r.reconcileCertificate(context.Background(), v, builder.BuildValkeyCertificate(v))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota exceeded")
	getErr := c.Get(context.Background(),
		types.NamespacedName{Name: builder.ValkeyCertificateName(v), Namespace: "default"}, newEmptyCert())
	assert.True(t, apierrors.IsNotFound(getErr), "nothing may be persisted when Create fails")
}

func TestReconcileCertificate_PropagatesUpdateError(t *testing.T) {
	v := newCertManagerValkey()
	stale := builder.BuildValkeyCertificate(v)
	require.NoError(t, unstructured.SetNestedStringSlice(
		stale.Object, []string{"stale.example.com"}, "spec", "dnsNames"))

	funcs := interceptor.Funcs{
		Update: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.UpdateOption) error {
			return internalErr("conflict storm")
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v, stale)

	err := r.reconcileCertificate(context.Background(), v, builder.BuildValkeyCertificate(v))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "conflict storm")
	assert.Equal(t, []string{"stale.example.com"},
		certDNSNames(t, getCert(t, c, builder.ValkeyCertificateName(v))),
		"the stored object must be unchanged when the Update fails")
}

// --- reconcileTLSCertificates ---

func TestReconcileTLSCertificates_SplitMode_CreatesTwoDisjointCertificates(t *testing.T) {
	v := newCertManagerValkey(withSentinel)
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v)

	require.NoError(t, r.reconcileTLSCertificates(context.Background(), v))

	valkeyCert := getCert(t, c, builder.ValkeyCertificateName(v))
	sentinelCert := getCert(t, c, builder.SentinelCertificateName(v))

	sentinelHeadless := common.HeadlessServiceName(v, common.ComponentSentinel)
	assert.NotContains(t, certDNSNames(t, valkeyCert), sentinelHeadless,
		"in split mode the Valkey certificate must not cover Sentinel hostnames")
	assert.Contains(t, certDNSNames(t, sentinelCert), sentinelHeadless)

	valkeySecret, _, _ := unstructured.NestedString(valkeyCert.Object, "spec", "secretName")
	sentinelSecret, _, _ := unstructured.NestedString(sentinelCert.Object, "spec", "secretName")
	assert.NotEqual(t, valkeySecret, sentinelSecret,
		"split mode must issue into two distinct Secrets")
	assert.Equal(t, common.ComponentSentinel,
		sentinelCert.GetLabels()["app.kubernetes.io/component"])
}

func TestReconcileTLSCertificates_UnifiedMode_OneCertificateCoveringBoth(t *testing.T) {
	v := newCertManagerValkey(withSentinel, withUnifiedCert)
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v)

	require.NoError(t, r.reconcileTLSCertificates(context.Background(), v))

	valkeyCert := getCert(t, c, builder.ValkeyCertificateName(v))
	dnsNames := certDNSNames(t, valkeyCert)
	assert.Contains(t, dnsNames, common.HeadlessServiceName(v, common.ComponentSentinel),
		"the unified certificate must carry the Sentinel SANs")
	assert.Contains(t, dnsNames, common.HeadlessServiceName(v, common.ComponentValkey))

	err := c.Get(context.Background(),
		types.NamespacedName{Name: builder.SentinelCertificateName(v), Namespace: "default"}, newEmptyCert())
	assert.True(t, apierrors.IsNotFound(err),
		"unified mode must not create the separate Sentinel Certificate")
}

func TestReconcileTLSCertificates_SentinelDisabled_NoSentinelCertificate(t *testing.T) {
	v := newCertManagerValkey()
	r, c := newReconcilerWithInterceptor("1.0.0", interceptor.Funcs{}, v)

	require.NoError(t, r.reconcileTLSCertificates(context.Background(), v))

	getCert(t, c, builder.ValkeyCertificateName(v))
	err := c.Get(context.Background(),
		types.NamespacedName{Name: builder.SentinelCertificateName(v), Namespace: "default"}, newEmptyCert())
	assert.True(t, apierrors.IsNotFound(err))
}

func TestReconcileTLSCertificates_LabelsValkeyCertificateError(t *testing.T) {
	v := newCertManagerValkey(withSentinel)
	funcs := interceptor.Funcs{
		Create: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.CreateOption) error {
			if obj.GetName() == builder.ValkeyCertificateName(v) {
				return internalErr("issuer missing")
			}
			return nil
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v)

	err := r.reconcileTLSCertificates(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "valkey certificate:",
		"the failing certificate must be named in the error")
	assert.NotContains(t, err.Error(), "sentinel certificate:")
}

func TestReconcileTLSCertificates_LabelsSentinelCertificateError(t *testing.T) {
	v := newCertManagerValkey(withSentinel)
	funcs := interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if obj.GetName() == builder.SentinelCertificateName(v) {
				return internalErr("issuer missing")
			}
			return cl.Create(ctx, obj, opts...)
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v)

	err := r.reconcileTLSCertificates(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "sentinel certificate:")
	getCert(t, c, builder.ValkeyCertificateName(v))
}

// --- reconcileLegacySentinelCertificateCleanup: remaining guards ---

// TestReconcileLegacySentinelCleanup_Noop_WhenLegacyNameIsTheActiveSecret
// exercises the defensive guard: a user-supplied spec.tls.secretName may collide
// with the legacy Certificate name, and the cleanup must then delete nothing.
func TestReconcileLegacySentinelCleanup_Noop_WhenLegacyNameIsTheActiveSecret(t *testing.T) {
	v := newTestValkey("test", "default", func(v *vkov1.Valkey) {
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled:            true,
			UnifiedCertificate: true,
			// The active Secret is named exactly like the legacy one.
			SecretName:  "test-sentinel-tls",
			CertManager: &vkov1.CertManagerSpec{Issuer: vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "ca"}},
		}
	})
	require.Equal(t, builder.SentinelCertificateName(v), builder.ValkeyTLSSecretName(v),
		"precondition: the names must collide for this guard to be under test")

	active := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "test-sentinel-tls", Namespace: "default"},
		Data:       map[string][]byte{"tls.crt": []byte("live")},
	}
	deletes := 0
	funcs := interceptor.Funcs{
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			deletes++
			return cl.Delete(ctx, obj, opts...)
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v, active)

	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v))

	assert.Zero(t, deletes, "the active TLS Secret must never be deleted")
	got := &corev1.Secret{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: "test-sentinel-tls", Namespace: "default"}, got))
	assert.Equal(t, []byte("live"), got.Data["tls.crt"])
}

func TestReconcileLegacySentinelCleanup_PropagatesRolloutProbeError(t *testing.T) {
	v := newTestValkeyUnified()
	legacyName := builder.SentinelCertificateName(v)
	legacySecret := newLegacySentinelSecret(legacyName, "iam")

	funcs := interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*appsv1.StatefulSet); ok {
				return internalErr("etcd unavailable")
			}
			return cl.Get(ctx, key, obj, opts...)
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v, legacySecret)

	err := r.reconcileLegacySentinelCertificateCleanup(context.Background(), v)

	require.Error(t, err, "an unreadable Sentinel StatefulSet must not be read as rollout complete")
	assert.Contains(t, err.Error(), "get sentinel statefulset")
	assertLegacySecretExists(t, c, legacyName)
}

func TestReconcileLegacySentinelCleanup_PropagatesCertificateGetError(t *testing.T) {
	v := newTestValkeyUnified()
	legacyName := builder.SentinelCertificateName(v)
	legacySecret := newLegacySentinelSecret(legacyName, "iam")

	r, c := newReconcilerWithInterceptor("1.0.0", failCertGet("cert-manager CRD flapping"), v, legacySecret)

	err := r.reconcileLegacySentinelCertificateCleanup(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get legacy certificate "+legacyName)
	// The Secret must not be dropped while the Certificate state is unknown.
	assertLegacySecretExists(t, c, legacyName)
}

func TestReconcileLegacySentinelCleanup_PropagatesCertificateDeleteError(t *testing.T) {
	v := newTestValkeyUnified()
	legacyName := builder.SentinelCertificateName(v)
	legacyCert := newLegacySentinelCert(v, legacyName)
	legacySecret := newLegacySentinelSecret(legacyName, "iam")

	funcs := interceptor.Funcs{
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if obj.GetObjectKind().GroupVersionKind().Kind == certManagerKindCertificate {
				return internalErr("finalizer stuck")
			}
			return cl.Delete(ctx, obj, opts...)
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v, legacyCert, legacySecret)

	err := r.reconcileLegacySentinelCertificateCleanup(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete certificate "+legacyName)
	// The Secret delete must not run after the Certificate delete failed.
	assertLegacySecretExists(t, c, legacyName)
}

func TestReconcileLegacySentinelCleanup_PropagatesSecretGetError(t *testing.T) {
	v := newTestValkeyUnified()
	legacyName := builder.SentinelCertificateName(v)
	legacyCert := newLegacySentinelCert(v, legacyName)

	funcs := interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*corev1.Secret); ok {
				return internalErr("secret read blocked")
			}
			return cl.Get(ctx, key, obj, opts...)
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v, legacyCert)

	err := r.reconcileLegacySentinelCertificateCleanup(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get legacy secret "+legacyName)
}

func TestReconcileLegacySentinelCleanup_PropagatesSecretDeleteError(t *testing.T) {
	v := newTestValkeyUnified()
	legacyName := builder.SentinelCertificateName(v)
	legacySecret := newLegacySentinelSecret(legacyName, "iam")

	funcs := interceptor.Funcs{
		Delete: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.DeleteOption) error {
			return internalErr("secret delete forbidden")
		},
	}
	r, c := newReconcilerWithInterceptor("1.0.0", funcs, v, legacySecret)

	err := r.reconcileLegacySentinelCertificateCleanup(context.Background(), v)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete secret "+legacyName)
	assertLegacySecretExists(t, c, legacyName)
}

// TestReconcileLegacySentinelCleanup_ToleratesConcurrentDeletion covers the
// read-then-delete race: another actor removes the object between the GET and
// the DELETE. NotFound on the delete is not an error.
func TestReconcileLegacySentinelCleanup_ToleratesConcurrentDeletion(t *testing.T) {
	v := newTestValkeyUnified()
	legacyName := builder.SentinelCertificateName(v)
	legacyCert := newLegacySentinelCert(v, legacyName)
	legacySecret := newLegacySentinelSecret(legacyName, "iam")

	funcs := interceptor.Funcs{
		Delete: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.DeleteOption) error {
			gvk := obj.GetObjectKind().GroupVersionKind()
			return apierrors.NewNotFound(
				schema.GroupResource{Group: gvk.Group, Resource: gvk.Kind}, obj.GetName())
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v, legacyCert, legacySecret)

	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v),
		"a concurrent deletion must not fail the reconcile")
}

// TestReconcileLegacySentinelCleanup_NA49_LeavesForeignSecretUnderLegacyName is
// the closed form of NA49. <cr>-sentinel-tls is derived from a CR name, and a
// principal who may create Valkey CRs in a namespace picks that name — so the
// name is attacker-chosen input, not evidence. A Secret the operator never
// created (no owning Certificate, no cert-manager provenance annotation,
// unrelated payload) survives, and the refusal is reported as an Event.
func TestReconcileLegacySentinelCleanup_NA49_LeavesForeignSecretUnderLegacyName(t *testing.T) {
	v := newTestValkeyUnified() // name oauth2-valkey, namespace iam
	legacyName := builder.SentinelCertificateName(v)

	foreign := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      legacyName,
			Namespace: "iam",
			// Deliberately NOT owned by the CR and not stamped by cert-manager.
			Labels:      map[string]string{"app": "unrelated-workload"},
			Annotations: map[string]string{"owner": "platform-team"},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"api-token": []byte("s3cr3t")},
	}

	stsName := common.StatefulSetName(v, common.ComponentSentinel)
	sts := stagedSentinelStatefulSet(stsName, builder.ValkeyTLSSecretName(v))
	objs := append([]client.Object{v, foreign, sts}, readySentinelPods(stsName, 3)...)
	r, c := newTestReconciler(objs...)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v))

	got := &corev1.Secret{}
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: legacyName, Namespace: "iam"}, got),
		"NA49: a foreign Secret under the legacy name must survive")
	assert.Equal(t, []byte("s3cr3t"), got.Data["api-token"], "its payload must be untouched")
	assert.Len(t, rec.withReason(reasonLegacySentinelTLSNotOwned), 1,
		"the refusal must be reported, not silent")
}

// TestReconcileLegacySentinelCleanup_NA49_NoSentinelStillGuardsTheDelete keeps the
// sharpest shape of NA49 pinned: with Sentinel disabled sentinelRolloutComplete
// short-circuits to "complete", so the delete is reached on the very first
// reconcile with no rollout window to wait for. certManager plus
// unifiedCertificate on a Sentinel-less instance is a valid spec. The guard, not
// the timing, is what protects the Secret — so it must hold here too.
func TestReconcileLegacySentinelCleanup_NA49_NoSentinelStillGuardsTheDelete(t *testing.T) {
	v := newTestValkey("payments", "prod", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 1
		// Sentinel deliberately left disabled.
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled:            true,
			UnifiedCertificate: true,
			CertManager: &vkov1.CertManagerSpec{
				Issuer: vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "ca"},
			},
		}
	})
	require.False(t, v.IsSentinelEnabled(), "precondition: no Sentinel, so no rollout to wait for")

	foreign := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      builder.SentinelCertificateName(v), // payments-sentinel-tls
			Namespace: "prod",
			Labels:    map[string]string{"app": "unrelated-workload"},
		},
		Data: map[string][]byte{"api-token": []byte("s3cr3t")},
	}
	r, c := newTestReconciler(v, foreign)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v))

	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: foreign.Name, Namespace: "prod"}, &corev1.Secret{}),
		"NA49: without a rollout window the provenance guard is the only protection")
	assert.Len(t, rec.withReason(reasonLegacySentinelTLSNotOwned), 1,
		"the refusal must be reported, not silent")
}

// TestReconcileLegacySentinelCleanup_NA49_LeavesForeignCertificate covers the
// other half of the same hazard, which predates NA37 entirely: the chart always
// granted delete on cert-manager Certificates. A foreign Certificate under the
// legacy name is left alone — deleting it would stop somebody else's issuance and
// renewal. Ownership is decided by ownerReference because the operator sets that
// reference itself on every Certificate it creates.
func TestReconcileLegacySentinelCleanup_NA49_LeavesForeignCertificate(t *testing.T) {
	v := newTestValkeyUnified()
	legacyName := builder.SentinelCertificateName(v)
	foreignCert := newForeignLegacySentinelCert(legacyName, "iam")

	r, c := newTestReconciler(v, foreignCert)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v))

	got := newEmptyCert()
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: legacyName, Namespace: "iam"}, got),
		"a Certificate this Valkey does not control must survive")
	assert.Len(t, rec.withReason(reasonLegacySentinelTLSNotOwned), 1,
		"the refusal must be reported, not silent")
}

// TestReconcileLegacySentinelCleanup_NA49_LeavesForeignCertificateAndItsSecret
// pins the composition of the two guards: a foreign Certificate is not deleted,
// and because it is foreign it also grants no provenance to the Secret beside it.
// The Secret carries the annotation of a DIFFERENT Certificate, so neither proof
// holds and both objects survive.
func TestReconcileLegacySentinelCleanup_NA49_LeavesForeignCertificateAndItsSecret(t *testing.T) {
	v := newTestValkeyUnified()
	legacyName := builder.SentinelCertificateName(v)
	foreignCert := newForeignLegacySentinelCert(legacyName, "iam")
	foreignSecret := newLegacySentinelSecret(legacyName, "iam")
	foreignSecret.Annotations[certManagerCertificateNameAnnotation] = "someone-elses-cert"

	r, c := newTestReconciler(v, foreignCert, foreignSecret)

	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v))

	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: legacyName, Namespace: "iam"}, newEmptyCert()))
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: legacyName, Namespace: "iam"}, &corev1.Secret{}))
}

// TestReconcileLegacySentinelCleanup_NA49_AnnotationAloneAuthorisesTheDelete is
// the migration path that matters in practice. The Certificate is already gone —
// an earlier pass deleted it and the Secret delete then failed 403, which is
// exactly the state NA37 repaired — so the in-pass ownership proof is unavailable
// and only the retroactive cert-manager annotation remains. Without this path the
// guard would strand every Secret it exists to clean up.
func TestReconcileLegacySentinelCleanup_NA49_AnnotationAloneAuthorisesTheDelete(t *testing.T) {
	v := newTestValkeyUnified()
	legacyName := builder.SentinelCertificateName(v)
	orphaned := newLegacySentinelSecret(legacyName, "iam")

	r, c := newTestReconciler(v, orphaned) // no Certificate under that name

	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v))

	err := c.Get(context.Background(),
		types.NamespacedName{Name: legacyName, Namespace: "iam"}, &corev1.Secret{})
	assert.True(t, apierrors.IsNotFound(err),
		"the cert-manager provenance annotation must authorise the delete on its own: %v", err)
}

// TestReconcileLegacySentinelCleanup_NA49_OwnedCertificateAuthorisesUnstampedSecret
// covers the reverse asymmetry: a Secret with no provenance annotation is still
// deleted when this same pass found a Certificate this Valkey controls that issues
// into that exact name. Guards against a cert-manager release that drops or renames
// the annotation — the in-pass proof is self-issued and survives that.
func TestReconcileLegacySentinelCleanup_NA49_OwnedCertificateAuthorisesUnstampedSecret(t *testing.T) {
	v := newTestValkeyUnified()
	legacyName := builder.SentinelCertificateName(v)
	ownedCert := newLegacySentinelCert(v, legacyName)
	unstamped := newLegacySentinelSecret(legacyName, "iam")
	delete(unstamped.Annotations, certManagerCertificateNameAnnotation)

	r, c := newTestReconciler(v, ownedCert, unstamped)

	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v))

	err := c.Get(context.Background(),
		types.NamespacedName{Name: legacyName, Namespace: "iam"}, &corev1.Secret{})
	assert.True(t, apierrors.IsNotFound(err), "the owned Certificate must authorise the delete: %v", err)
}

// TestReconcileLegacySentinelCleanup_NA49_OwnedCertificatePointingElsewhere shows
// why the secretName comparison is explicit rather than assumed. The Certificate is
// ours, but it issues into a different Secret, so it says nothing about the object
// under the legacy name. Today the two names coincide by construction; if that
// derivation is ever split, this fails instead of authorising the wrong Secret.
func TestReconcileLegacySentinelCleanup_NA49_OwnedCertificatePointingElsewhere(t *testing.T) {
	v := newTestValkeyUnified()
	legacyName := builder.SentinelCertificateName(v)
	ownedCert := newLegacySentinelCert(v, legacyName)
	ownedCert.Object["spec"] = map[string]interface{}{"secretName": "somewhere-else"}
	unstamped := newLegacySentinelSecret(legacyName, "iam")
	delete(unstamped.Annotations, certManagerCertificateNameAnnotation)

	r, c := newTestReconciler(v, ownedCert, unstamped)

	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v))

	// The Certificate is ours, so it is still cleaned up.
	certErr := c.Get(context.Background(),
		types.NamespacedName{Name: legacyName, Namespace: "iam"}, newEmptyCert())
	assert.True(t, apierrors.IsNotFound(certErr), "an owned Certificate is deleted regardless: %v", certErr)

	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: legacyName, Namespace: "iam"}, &corev1.Secret{}),
		"a Certificate that issues elsewhere proves nothing about this Secret")
}

// TestReconcileLegacySentinelCleanup_NA49_NonTLSTypeIsNeverDeleted pins the type
// precondition. It does not establish provenance on its own — an attacker can
// point the name at a real TLS Secret — but it removes the whole class of
// accidental collateral, and it outranks the annotation: a Secret that is not
// kubernetes.io/tls was not issued by cert-manager whatever it claims.
func TestReconcileLegacySentinelCleanup_NA49_NonTLSTypeIsNeverDeleted(t *testing.T) {
	v := newTestValkeyUnified()
	legacyName := builder.SentinelCertificateName(v)
	ownedCert := newLegacySentinelCert(v, legacyName)
	opaque := newLegacySentinelSecret(legacyName, "iam")
	opaque.Type = corev1.SecretTypeOpaque // keeps the annotation, and the owned Certificate

	r, c := newTestReconciler(v, ownedCert, opaque)
	rec := &fakeEventRecorder{}
	r.Recorder = rec

	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v))

	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: legacyName, Namespace: "iam"}, &corev1.Secret{}),
		"an Opaque Secret is not cert-manager TLS material, whatever else points at it")
	assert.Len(t, rec.withReason(reasonLegacySentinelTLSNotOwned), 1,
		"the refusal must be reported, not silent")
}

// TestReconcileLegacySentinelCleanup_NA49_UIDPreconditionOnBothDeletes pins the
// NA31 discipline on this path: both ownership decisions are made on cache-backed
// reads, so both Deletes must name the UID they inspected. A Conflict means the
// name now holds a different object and is not an error.
func TestReconcileLegacySentinelCleanup_NA49_UIDPreconditionOnBothDeletes(t *testing.T) {
	v := newTestValkeyUnified()
	legacyName := builder.SentinelCertificateName(v)
	ownedCert := newLegacySentinelCert(v, legacyName)
	ownedCert.SetUID("cert-uid")
	secret := newLegacySentinelSecret(legacyName, "iam")
	secret.UID = "secret-uid"

	var seen []string
	funcs := interceptor.Funcs{
		Delete: func(_ context.Context, _ client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			var o client.DeleteOptions
			for _, opt := range opts {
				opt.ApplyToDelete(&o)
			}
			require.NotNil(t, o.Preconditions, "delete of %s must carry a precondition", obj.GetName())
			require.NotNil(t, o.Preconditions.UID)
			seen = append(seen, string(*o.Preconditions.UID))
			// Model the object having been replaced under its name.
			return apierrors.NewConflict(schema.GroupResource{Resource: "x"}, obj.GetName(), errors.New("uid mismatch"))
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v, ownedCert, secret)

	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v),
		"a failed precondition is the guard working, not a reconcile error")
	// The Secret is still reached: it carries the cert-manager annotation, which
	// authorises it independently of the Certificate. Both deletes name their UID.
	assert.Equal(t, []string{"cert-uid", "secret-uid"}, seen)
}

// TestReconcileLegacySentinelCleanup_NA49_CertificateConflictRevokesInPassProof
// isolates what a failed Certificate precondition actually costs. The object under
// that name is no longer the one this pass inspected, so it proves nothing about
// the Secret beside it — and a Secret with no annotation of its own then has no
// admissible proof left and survives.
func TestReconcileLegacySentinelCleanup_NA49_CertificateConflictRevokesInPassProof(t *testing.T) {
	v := newTestValkeyUnified()
	legacyName := builder.SentinelCertificateName(v)
	ownedCert := newLegacySentinelCert(v, legacyName)
	ownedCert.SetUID("cert-uid")
	unstamped := newLegacySentinelSecret(legacyName, "iam")
	delete(unstamped.Annotations, certManagerCertificateNameAnnotation)

	funcs := interceptor.Funcs{
		Delete: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.DeleteOption) error {
			if obj.GetObjectKind().GroupVersionKind().Kind == certManagerKindCertificate {
				return apierrors.NewConflict(
					schema.GroupResource{Resource: "certificates"}, obj.GetName(), errors.New("uid mismatch"))
			}
			return errors.New("the Secret must not be deleted without an admissible proof")
		},
	}
	r, _ := newReconcilerWithInterceptor("1.0.0", funcs, v, ownedCert, unstamped)

	require.NoError(t, r.reconcileLegacySentinelCertificateCleanup(context.Background(), v))
}
