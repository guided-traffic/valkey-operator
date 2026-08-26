package builder

import (
	"fmt"
	"hash/fnv"

	corev1 "k8s.io/api/core/v1"
)

const (
	// TLSCACertKey is the Secret key and mounted file name of the CA bundle.
	TLSCACertKey = "ca.crt"
	// TLSCertKey is the Secret key and mounted file name of the leaf certificate.
	TLSCertKey = "tls.crt"
	// TLSPrivateKeyKey is the Secret key and mounted file name of the private key.
	TLSPrivateKeyKey = "tls.key"
)

// tlsMaterialKeys are the Secret keys that make up the TLS material a pod mounts,
// in the order they are fed to the hash. The three are fixed: the whole Secret is
// mounted at TLSMountPath and every generated script, container argument and
// exporter env var addresses exactly these file names.
//
// Naming them rather than hashing the whole Secret is deliberate. A Secret
// carries fields nobody in a pod reads -- cert-manager writes and rewrites
// annotations and, in some issuer configurations, additional keys -- and a
// fingerprint over those would roll the fleet for a change no process can see.
var tlsMaterialKeys = []string{TLSCACertKey, TLSCertKey, TLSPrivateKeyKey}

// ComputeTLSMaterialHash returns a short hex digest of the TLS material in secret.
// An absent key contributes its own length-prefixed emptiness, so a Secret that
// gains or loses one is a different fingerprint rather than a collision.
//
// A nil secret returns the empty string, which every consumer reads as "no
// fingerprint known": no annotation is written, and a pod without the annotation
// is never restarted for it.
func ComputeTLSMaterialHash(secret *corev1.Secret) string {
	if secret == nil {
		return ""
	}

	h := fnv.New32a()
	for _, key := range tlsMaterialKeys {
		value := secret.Data[key]
		// The length prefix is what keeps the concatenation unambiguous: without
		// it, moving a byte from the end of one field to the start of the next
		// would hash identically.
		_, _ = fmt.Fprintf(h, "%s:%d:", key, len(value))
		_, _ = h.Write(value)
	}
	return fmt.Sprintf("%08x", h.Sum32())
}
