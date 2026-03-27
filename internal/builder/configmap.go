package builder

import (
	"fmt"
	"hash/fnv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/common"
)

const (
	// ValkeyPort is the default Valkey server port.
	ValkeyPort = 6379

	// ValkeyConfigKey is the key used in the ConfigMap for the valkey configuration.
	ValkeyConfigKey = "valkey.conf"

	// DataDir is the directory where Valkey stores its data.
	DataDir = "/data"
)

// ConfigMapName returns the name for the Valkey ConfigMap.
func ConfigMapName(v *vkov1.Valkey) string {
	return fmt.Sprintf("%s-config", v.Name)
}

// MasterAddress returns the DNS address of the master pod (pod-0 of the StatefulSet).
// Used for `replicaof` configuration in replica pods.
func MasterAddress(v *vkov1.Valkey) string {
	return fmt.Sprintf("%s-0.%s.%s.svc.cluster.local",
		common.StatefulSetName(v, common.ComponentValkey),
		common.HeadlessServiceName(v, common.ComponentValkey),
		v.Namespace,
	)
}

// GenerateValkeyConf generates the valkey.conf content based on the CRD spec.
// The isReplica parameter controls whether replicaof directives are included.
// When the Valkey CR carries the AnnotationKnownMaster annotation (set after
// a sentinel failover), the replica config's replicaof directive uses that
// address instead of the default pod-0 address.
func GenerateValkeyConf(v *vkov1.Valkey, isReplica bool) string {
	return generateValkeyConf(v, isReplica, true)
}

// GenerateValkeyConfForHash generates the valkey.conf content without using the
// AnnotationKnownMaster override. Use this when computing the config hash for
// pod update detection. The AnnotationKnownMaster changes during rolling-update
// failovers (set by persistKnownMaster) and must NOT affect the hash — including
// it would cause all pods to appear outdated immediately after a failover,
// triggering an infinite restart loop.
func GenerateValkeyConfForHash(v *vkov1.Valkey, isReplica bool) string {
	return generateValkeyConf(v, isReplica, false)
}

// generateValkeyConf is the internal implementation shared by GenerateValkeyConf
// and GenerateValkeyConfForHash.
func generateValkeyConf(v *vkov1.Valkey, isReplica bool, useKnownMaster bool) string {
	var lines []string

	// Network configuration.
	lines = append(lines,
		"# Network",
		"bind 0.0.0.0",
		fmt.Sprintf("port %d", ValkeyPort),
		"protected-mode no",
		"tcp-backlog 511",
		"timeout 0",
		"tcp-keepalive 300",
		"",
	)

	// TLS configuration.
	if v.IsTLSEnabled() {
		// When allowUnencrypted is true, keep the plaintext port open alongside TLS.
		// Otherwise disable plaintext entirely (port 0).
		plaintextPort := "port 0"
		if v.IsValkeyUnencryptedAllowed() {
			plaintextPort = fmt.Sprintf("port %d", ValkeyPort)
		}
		lines = append(lines,
			"# TLS (configured by operator)",
			fmt.Sprintf("tls-port %d", TLSPort),
			plaintextPort,
			"tls-cert-file /tls/tls.crt",
			"tls-key-file /tls/tls.key",
			"tls-ca-cert-file /tls/ca.crt",
			"tls-replication yes",
			"tls-auth-clients optional",
			"",
		)
	}

	// Auth configuration — password is injected at runtime via environment variable.
	// The valkey-server command is started with --requirepass and --masterauth flags
	// that reference the VALKEY_PASSWORD environment variable from the auth Secret.
	if v.IsAuthEnabled() {
		lines = append(lines,
			"# Auth (password injected via command-line arguments from Secret)",
			"",
		)
	}

	// Replication configuration (multi-replica mode).
	if v.IsSentinelEnabled() || v.IsMultiReplicaWithoutSentinel() {
		lines = append(lines, replicationConfig(v, isReplica, useKnownMaster)...)
	}

	// Persistence configuration.
	lines = append(lines, persistenceConfig(v)...)

	// General settings.
	lines = append(lines,
		"# General",
		"daemonize no",
		"loglevel notice",
		"databases 16",
		"always-show-logo no",
		"",
	)

	// Memory settings.
	lines = append(lines,
		"# Memory",
		"maxmemory-policy noeviction",
		"lazyfree-lazy-eviction yes",
		"lazyfree-lazy-expire yes",
		"lazyfree-lazy-server-del yes",
		"lazyfree-lazy-user-del yes",
		"",
	)

	return strings.Join(lines, "\n")
}

// replicationConfig returns replication-related config lines for multi-replica mode.
// When useKnownMaster is true and the Valkey CR carries the AnnotationKnownMaster
// annotation, the replicaof directive uses the annotated address instead of the
// default pod-0 address. This ensures the replica ConfigMap reflects the actual
// post-failover master, so pods that restart when Sentinel is unavailable connect
// to the correct master via the init container's ConfigMap fallback.
func replicationConfig(v *vkov1.Valkey, isReplica bool, useKnownMaster bool) []string {
	var lines []string

	// replica-announce-ip and replica-announce-port are injected dynamically
	// by the init container, as they depend on the pod's hostname.
	lines = append(lines, "# Replication")

	// Use TLS port for replication when TLS is enabled.
	replicationPort := ValkeyPort
	if v.IsTLSEnabled() {
		replicationPort = TLSPort
	}

	if isReplica {
		// Determine the master address. When useKnownMaster is true and the CR
		// has been annotated with the actual post-failover master, use that address.
		// Otherwise fall back to the default pod-0 address.
		masterAddr := MasterAddress(v)
		if useKnownMaster && v.Annotations != nil {
			if override, ok := v.Annotations[AnnotationKnownMaster]; ok && override != "" {
				masterAddr = override
			}
		}
		lines = append(lines,
			fmt.Sprintf("replicaof %s %d", masterAddr, replicationPort),
		)
	}

	// Allow replicas to serve stale data during sync.
	lines = append(lines,
		"replica-serve-stale-data yes",
		"replica-read-only yes",
		"repl-diskless-sync yes",
		"repl-diskless-sync-delay 5",
		"",
	)

	return lines
}

// persistenceConfig returns the persistence-related config lines.
func persistenceConfig(v *vkov1.Valkey) []string {
	var lines []string

	if !v.IsPersistenceEnabled() {
		lines = append(lines,
			"# Persistence (disabled)",
			"save \"\"",
			"appendonly no",
			"",
		)
		return lines
	}

	mode := v.Spec.Persistence.Mode

	// RDB configuration.
	if mode == vkov1.PersistenceModeRDB || mode == vkov1.PersistenceModeBoth {
		lines = append(lines,
			"# RDB Persistence",
			"save 900 1",
			"save 300 10",
			"save 60 10000",
			"stop-writes-on-bgsave-error yes",
			"rdbcompression yes",
			"rdbchecksum yes",
			"dbfilename dump.rdb",
			fmt.Sprintf("dir %s", DataDir),
			"",
		)
	} else {
		lines = append(lines,
			"# RDB Persistence (disabled)",
			"save \"\"",
			"",
		)
	}

	// AOF configuration.
	if mode == vkov1.PersistenceModeAOF || mode == vkov1.PersistenceModeBoth {
		lines = append(lines,
			"# AOF Persistence",
			"appendonly yes",
			"appendfilename \"appendonly.aof\"",
			"appendfsync everysec",
			"no-appendfsync-on-rewrite no",
			"auto-aof-rewrite-percentage 100",
			"auto-aof-rewrite-min-size 64mb",
			fmt.Sprintf("dir %s", DataDir),
			"",
		)
	} else {
		lines = append(lines,
			"# AOF Persistence (disabled)",
			"appendonly no",
			"",
		)
	}

	return lines
}

// ReplicaConfigMapName returns the name for the replica Valkey ConfigMap (HA mode).
func ReplicaConfigMapName(v *vkov1.Valkey) string {
	return fmt.Sprintf("%s-replica-config", v.Name)
}

// BuildConfigMap builds the ConfigMap for Valkey configuration.
// In standalone mode or for the master in HA mode, isReplica should be false.
func BuildConfigMap(v *vkov1.Valkey) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName(v),
			Namespace: v.Namespace,
			Labels:    common.BaseLabels(v, common.ComponentValkey),
		},
		Data: map[string]string{
			ValkeyConfigKey: GenerateValkeyConf(v, false),
		},
	}
}

// BuildReplicaConfigMap builds the ConfigMap for Valkey replica configuration (HA mode).
// It includes the `replicaof` directive pointing to the master.
func BuildReplicaConfigMap(v *vkov1.Valkey) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ReplicaConfigMapName(v),
			Namespace: v.Namespace,
			Labels:    common.BaseLabels(v, common.ComponentValkey),
		},
		Data: map[string]string{
			ValkeyConfigKey: GenerateValkeyConf(v, true),
		},
	}
}

// ComputeConfigHash returns a short hex digest representing the generated Valkey
// (and Sentinel, if applicable) configuration content. It is embedded in the
// StatefulSet pod template annotations so that config changes — such as toggling
// allowUnencrypted — cause the pod template annotation to change. The operator's
// rolling update logic detects the annotation mismatch on running pods and
// triggers a controlled rolling restart.
//
// Only pods that already carry the AnnotationConfigHash annotation are checked;
// pods created by an older operator version (without the annotation) are not
// forced to restart until they are replaced for another reason.
func ComputeConfigHash(v *vkov1.Valkey) string {
	h := fnv.New32a()
	// Use GenerateValkeyConfForHash (no AnnotationKnownMaster) so that a
	// post-failover master-address change does not alter the hash and trigger
	// an unwanted rolling restart of all Valkey pods.
	_, _ = fmt.Fprint(h, GenerateValkeyConfForHash(v, false))
	_, _ = fmt.Fprint(h, GenerateValkeyConfForHash(v, true))
	if v.IsSentinelEnabled() {
		// Same reasoning for the sentinel config hash.
		_, _ = fmt.Fprint(h, GenerateSentinelConfForHash(v))
	}
	return fmt.Sprintf("%08x", h.Sum32())
}
