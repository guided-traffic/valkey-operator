package builder

import (
	"fmt"
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

// GenerateValkeyConf generates the valkey.conf content based on the CRD spec.
func GenerateValkeyConf(v *vkov1.Valkey) string {
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

	// TLS configuration placeholder (applied in Phase 5).
	if v.IsTLSEnabled() {
		lines = append(lines,
			"# TLS (configured by operator)",
			fmt.Sprintf("tls-port %d", ValkeyPort+10000),
			"port 0",
			"tls-cert-file /tls/tls.crt",
			"tls-key-file /tls/tls.key",
			"tls-ca-cert-file /tls/ca.crt",
			"tls-replication yes",
			"tls-auth-clients optional",
			"",
		)
	}

	// Auth configuration placeholder (password injected at runtime via Secret).
	if v.IsAuthEnabled() {
		lines = append(lines,
			"# Auth (password set via requirepass at runtime)",
			"# requirepass is injected via command-line argument or environment variable",
			"",
		)
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

// BuildConfigMap builds the ConfigMap for Valkey configuration.
func BuildConfigMap(v *vkov1.Valkey) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName(v),
			Namespace: v.Namespace,
			Labels:    common.BaseLabels(v, common.ComponentValkey),
		},
		Data: map[string]string{
			ValkeyConfigKey: GenerateValkeyConf(v),
		},
	}
}
