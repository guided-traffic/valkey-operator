package builder_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
