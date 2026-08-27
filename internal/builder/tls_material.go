package builder

import (
	"fmt"
	"hash/fnv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	// TLSMaterialHashEnvName carries the TLS material fingerprint on the pod, as an
	// environment variable of the container that records it.
	//
	// It lives in the pod *spec* and not in pod metadata for one reason: a pod can
	// rewrite its own metadata and cannot rewrite its own spec. The sidecar Role
	// grants pods: patch on this cluster's data pods, and env is not one of the five
	// entries in the API server's updatablePodSpecFields, so the record the operator
	// reads back is the record the operator wrote (ADR 0031).
	//
	// Nothing in the pod reads the variable. It is a record, not configuration.
	TLSMaterialHashEnvName = "VKO_TLS_MATERIAL_HASH"

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

// StampTLSMaterialHash records hash on the named container of the StatefulSet's
// pod template, replacing any value already there.
//
// It runs on the built StatefulSet rather than inside buildPodSpec, and that
// placement is load-bearing: ComputePodSpecHash digests the spec the builder
// produced, so folding the fingerprint in there would make one rotation move two
// independent signals and roll the fleet twice over for one event.
//
// An empty hash writes nothing. A pod carrying no record is unmeasured rather
// than stale, which is the whole upgrade-neutrality story for this mechanism
// (ADR 0005, ADR 0030 D8) -- and it is also what a Secret that cert-manager has
// not issued yet must look like.
func StampTLSMaterialHash(sts *appsv1.StatefulSet, containerName, hash string) {
	if hash == "" {
		return
	}

	containers := sts.Spec.Template.Spec.Containers
	for i := range containers {
		if containers[i].Name != containerName {
			continue
		}
		for j := range containers[i].Env {
			if containers[i].Env[j].Name == TLSMaterialHashEnvName {
				containers[i].Env[j].Value = hash
				return
			}
		}
		containers[i].Env = append(containers[i].Env, corev1.EnvVar{
			Name:  TLSMaterialHashEnvName,
			Value: hash,
		})
		return
	}
}

// RecordedTLSMaterialHash returns the TLS material fingerprint recorded on a pod
// or on a pod template: the env carrier first, the superseded annotation second.
//
// The two arguments are the two halves of the same object -- (pod.Spec,
// pod.Annotations) or (template.Spec, template.Annotations) -- so that one
// function answers for a live pod and for the persisted template it is compared
// against.
//
// The annotation fallback exists for exactly one population: objects written
// before the carrier moved into the spec. Without it a Sentinel pod would go
// unmeasured for as long as its tier does not roll, and a rotation in that window
// would neither replace it nor report it -- the silent failure ADR 0030 exists to
// prevent. It is self-extinguishing, because the roll it enables replaces the pod
// with one that carries the env, and it is not a way back in for a forger: the
// annotation is consulted only when there is no env to consult, and every pod the
// operator writes from now on has one.
func RecordedTLSMaterialHash(spec *corev1.PodSpec, annotations map[string]string) string {
	if spec != nil {
		for i := range spec.Containers {
			for _, env := range spec.Containers[i].Env {
				if env.Name == TLSMaterialHashEnvName {
					return env.Value
				}
			}
		}
	}
	return annotations[AnnotationTLSMaterialHash]
}
