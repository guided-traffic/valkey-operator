package builder_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

func materialSecret(data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "test-tls", Namespace: "default"},
		Data:       data,
	}
}

func TestComputeTLSMaterialHash_IsStableForIdenticalMaterial(t *testing.T) {
	data := map[string][]byte{
		"ca.crt":  []byte("ca"),
		"tls.crt": []byte("cert"),
		"tls.key": []byte("key"),
	}

	first := builder.ComputeTLSMaterialHash(materialSecret(data))
	second := builder.ComputeTLSMaterialHash(materialSecret(data))

	assert.NotEmpty(t, first)
	assert.Equal(t, first, second, "a stable fingerprint is what keeps a reconcile from rolling the fleet")
}

// Every part of the mounted material moves the fingerprint. The key matters as
// much as the certificate: cert-manager rotates the pair, and a fingerprint that
// only watched tls.crt would miss a re-keying that kept the same certificate
// bytes for a moment.
func TestComputeTLSMaterialHash_ChangesWithEveryMountedKey(t *testing.T) {
	base := map[string][]byte{
		"ca.crt":  []byte("ca"),
		"tls.crt": []byte("cert"),
		"tls.key": []byte("key"),
	}
	baseline := builder.ComputeTLSMaterialHash(materialSecret(base))

	for _, key := range []string{"ca.crt", "tls.crt", "tls.key"} {
		t.Run(key, func(t *testing.T) {
			rotated := map[string][]byte{}
			for k, v := range base {
				rotated[k] = v
			}
			rotated[key] = []byte("rotated")

			assert.NotEqual(t, baseline, builder.ComputeTLSMaterialHash(materialSecret(rotated)))
		})
	}
}

// A Secret carries fields no pod reads -- cert-manager writes annotations and,
// depending on the issuer, extra keys. Rolling a cluster for one of those would
// be a restart for a change no process can observe.
func TestComputeTLSMaterialHash_IgnoresKeysNoPodMounts(t *testing.T) {
	base := materialSecret(map[string][]byte{
		"ca.crt":  []byte("ca"),
		"tls.crt": []byte("cert"),
		"tls.key": []byte("key"),
	})
	noisy := materialSecret(map[string][]byte{
		"ca.crt":                []byte("ca"),
		"tls.crt":               []byte("cert"),
		"tls.key":               []byte("key"),
		"tls-combined.pem":      []byte("whatever"),
		"some.issuer.annotated": []byte("whatever"),
	})
	noisy.Annotations = map[string]string{"cert-manager.io/certificate-name": "test-tls"}

	assert.Equal(t, builder.ComputeTLSMaterialHash(base), builder.ComputeTLSMaterialHash(noisy))
}

// A field that moves between keys must not collide: the length prefix is what
// makes the concatenation unambiguous.
func TestComputeTLSMaterialHash_DoesNotCollideOnShiftedBytes(t *testing.T) {
	a := materialSecret(map[string][]byte{"ca.crt": []byte("ab"), "tls.crt": []byte("c")})
	b := materialSecret(map[string][]byte{"ca.crt": []byte("a"), "tls.crt": []byte("bc")})

	assert.NotEqual(t, builder.ComputeTLSMaterialHash(a), builder.ComputeTLSMaterialHash(b))
}

// A Secret that is missing a key is a different fingerprint, not the same one.
func TestComputeTLSMaterialHash_MissingKeyIsItsOwnFingerprint(t *testing.T) {
	full := materialSecret(map[string][]byte{
		"ca.crt": []byte("ca"), "tls.crt": []byte("cert"), "tls.key": []byte("key"),
	})
	partial := materialSecret(map[string][]byte{"ca.crt": []byte("ca"), "tls.crt": []byte("cert")})

	assert.NotEqual(t, builder.ComputeTLSMaterialHash(full), builder.ComputeTLSMaterialHash(partial))
}

// The absent-Secret path: no fingerprint means no annotation, which means no pod
// is ever restarted for it.
func TestComputeTLSMaterialHash_NilSecretHasNoFingerprint(t *testing.T) {
	assert.Empty(t, builder.ComputeTLSMaterialHash(nil))
}

// --- the carrier: pod spec, not pod metadata (ADR 0031) ---

func carrierValkey() *vkov1.Valkey {
	return &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: vkov1.ValkeySpec{
			Replicas: 3,
			Image:    "valkey/valkey:8.0",
			TLS:      &vkov1.TLSSpec{Enabled: true},
		},
	}
}

func envOn(sts *appsv1.StatefulSet, container string) (string, bool) {
	for _, c := range sts.Spec.Template.Spec.Containers {
		if c.Name != container {
			continue
		}
		for _, env := range c.Env {
			if env.Name == builder.TLSMaterialHashEnvName {
				return env.Value, true
			}
		}
	}
	return "", false
}

func TestStampTLSMaterialHash_RecordsOnTheNamedContainerOnly(t *testing.T) {
	sts := builder.BuildStatefulSet(carrierValkey(), "operator:test")

	builder.StampTLSMaterialHash(sts, builder.SidecarContainerName, "abcd1234")

	got, ok := envOn(sts, builder.SidecarContainerName)
	require.True(t, ok)
	assert.Equal(t, "abcd1234", got)

	_, onValkey := envOn(sts, builder.ValkeyContainerName)
	assert.False(t, onValkey, "the record belongs to one container; a second copy is a second truth")
	assert.NotContains(t, sts.Spec.Template.Annotations, builder.AnnotationTLSMaterialHash)
}

// The auflage that keeps one rotation to one signal: the pod-spec hash digests
// what the builder produced, and the fingerprint is stamped after that. Folding
// it in would move both hashes for a single event.
func TestStampTLSMaterialHash_DoesNotMoveThePodSpecHash(t *testing.T) {
	v := carrierValkey()
	sts := builder.BuildStatefulSet(v, "operator:test")
	before := sts.Spec.Template.Annotations[builder.AnnotationPodSpecHash]
	require.NotEmpty(t, before)

	builder.StampTLSMaterialHash(sts, builder.SidecarContainerName, "abcd1234")

	assert.Equal(t, before, sts.Spec.Template.Annotations[builder.AnnotationPodSpecHash])
	assert.Equal(t, before, builder.ComputePodSpecHash(v, "operator:test"),
		"a later pass must recompute the same value, or every reconcile would look like a change")
}

// ...and the change still has to reach the StatefulSet, or the rotation never
// rolls anything.
func TestStampTLSMaterialHash_IsSeenByTheStatefulSetComparison(t *testing.T) {
	v := carrierValkey()
	current := builder.BuildStatefulSet(v, "operator:test")
	builder.StampTLSMaterialHash(current, builder.SidecarContainerName, "aaaa")

	desired := builder.BuildStatefulSet(v, "operator:test")
	builder.StampTLSMaterialHash(desired, builder.SidecarContainerName, "bbbb")

	assert.True(t, builder.StatefulSetHasChanged(desired, current))
}

func TestStampTLSMaterialHash_EmptyHashWritesNothing(t *testing.T) {
	sts := builder.BuildStatefulSet(carrierValkey(), "operator:test")

	builder.StampTLSMaterialHash(sts, builder.SidecarContainerName, "")

	_, ok := envOn(sts, builder.SidecarContainerName)
	assert.False(t, ok, "no fingerprint known means no record, and an unrecorded pod is never rolled for one")
}

func TestStampTLSMaterialHash_UnknownContainerIsANoOp(t *testing.T) {
	sts := builder.BuildStatefulSet(carrierValkey(), "operator:test")

	assert.NotPanics(t, func() {
		builder.StampTLSMaterialHash(sts, "does-not-exist", "abcd1234")
	})
	assert.Empty(t, builder.RecordedTLSMaterialHash(&sts.Spec.Template.Spec, sts.Spec.Template.Annotations))
}

func TestRecordedTLSMaterialHash_PrefersTheSpecOverTheAnnotation(t *testing.T) {
	spec := &corev1.PodSpec{Containers: []corev1.Container{{
		Name: builder.SidecarContainerName,
		Env:  []corev1.EnvVar{{Name: builder.TLSMaterialHashEnvName, Value: "from-spec"}},
	}}}
	annotations := map[string]string{builder.AnnotationTLSMaterialHash: "from-metadata"}

	assert.Equal(t, "from-spec", builder.RecordedTLSMaterialHash(spec, annotations))
}

func TestRecordedTLSMaterialHash_FallsBackToTheSupersededAnnotation(t *testing.T) {
	// The population the fallback exists for: an object written before the carrier
	// moved. Without it a Sentinel pod would go unmeasured until its tier next
	// rolls, and a rotation in that window would be silent.
	spec := &corev1.PodSpec{Containers: []corev1.Container{{Name: builder.SidecarContainerName}}}
	annotations := map[string]string{builder.AnnotationTLSMaterialHash: "from-metadata"}

	assert.Equal(t, "from-metadata", builder.RecordedTLSMaterialHash(spec, annotations))
}

func TestRecordedTLSMaterialHash_NothingRecordedIsTheEmptyString(t *testing.T) {
	assert.Empty(t, builder.RecordedTLSMaterialHash(nil, nil))
	assert.Empty(t, builder.RecordedTLSMaterialHash(&corev1.PodSpec{}, map[string]string{}))
}
