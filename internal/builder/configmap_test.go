package builder

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

func newTestValkey(name string, opts ...func(*vkov1.Valkey)) *vkov1.Valkey {
	v := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: vkov1.ValkeySpec{
			Replicas: 1,
			Image:    "valkey/valkey:8.0",
		},
	}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// --- ConfigMapName ---

func TestConfigMapName(t *testing.T) {
	v := newTestValkey("my-valkey")
	assert.Equal(t, "my-valkey-config", ConfigMapName(v))
}

// --- GenerateValkeyConf ---

func TestGenerateValkeyConf_Standalone_NoPersistence(t *testing.T) {
	v := newTestValkey("test")

	conf := GenerateValkeyConf(v, false)

	assert.Contains(t, conf, "bind 0.0.0.0")
	assert.Contains(t, conf, "port 6379")
	assert.Contains(t, conf, "protected-mode no")
	assert.Contains(t, conf, "tcp-keepalive 300")
	assert.Contains(t, conf, "save \"\"")
	assert.Contains(t, conf, "appendonly no")
	assert.Contains(t, conf, "daemonize no")
	assert.Contains(t, conf, "maxmemory-policy noeviction")

	// Should NOT contain TLS or auth.
	assert.NotContains(t, conf, "tls-port")
	assert.NotContains(t, conf, "requirepass")
}

func TestGenerateValkeyConf_PersistenceRDB(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Persistence = &vkov1.PersistenceSpec{
			Enabled: true,
			Mode:    vkov1.PersistenceModeRDB,
			Size:    resource.MustParse("1Gi"),
		}
	})

	conf := GenerateValkeyConf(v, false)

	assert.Contains(t, conf, "save 900 1")
	assert.Contains(t, conf, "save 300 10")
	assert.Contains(t, conf, "save 60 10000")
	assert.Contains(t, conf, "dbfilename dump.rdb")
	assert.Contains(t, conf, "dir /data")
	assert.Contains(t, conf, "rdbcompression yes")

	// AOF should be disabled.
	assert.Contains(t, conf, "appendonly no")
}

func TestGenerateValkeyConf_PersistenceAOF(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Persistence = &vkov1.PersistenceSpec{
			Enabled: true,
			Mode:    vkov1.PersistenceModeAOF,
		}
	})

	conf := GenerateValkeyConf(v, false)

	assert.Contains(t, conf, "appendonly yes")
	assert.Contains(t, conf, "appendfilename \"appendonly.aof\"")
	assert.Contains(t, conf, "appendfsync everysec")

	// RDB should be disabled.
	assert.Contains(t, conf, "save \"\"")
}

func TestGenerateValkeyConf_PersistenceBoth(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Persistence = &vkov1.PersistenceSpec{
			Enabled: true,
			Mode:    vkov1.PersistenceModeBoth,
		}
	})

	conf := GenerateValkeyConf(v, false)

	assert.Contains(t, conf, "save 900 1")
	assert.Contains(t, conf, "appendonly yes")
	assert.Contains(t, conf, "dir /data")
}

func TestGenerateValkeyConf_TLSEnabled(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})

	conf := GenerateValkeyConf(v, false)

	assert.Contains(t, conf, "tls-port 16379")
	assert.Contains(t, conf, "port 0")
	assert.Contains(t, conf, "tls-cert-file /tls/tls.crt")
	assert.Contains(t, conf, "tls-key-file /tls/tls.key")
	assert.Contains(t, conf, "tls-ca-cert-file /tls/ca.crt")
	assert.Contains(t, conf, "tls-replication yes")
}

// TestGenerateValkeyConf_TLS_AllowUnencrypted verifies that when TLS is enabled
// and allowUnencrypted is true, the plaintext port (6379) is kept open alongside
// the TLS port (16379). Internal replication always uses TLS (tls-replication yes).
func TestGenerateValkeyConf_TLS_AllowUnencrypted(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled:          true,
			AllowUnencrypted: true,
		}
	})

	conf := GenerateValkeyConf(v, false)

	// Both TLS and plaintext ports must be present.
	assert.Contains(t, conf, "tls-port 16379", "TLS port must be present")
	assert.Contains(t, conf, "port 6379", "plaintext port must be kept open when allowUnencrypted")
	assert.NotContains(t, conf, "port 0", "port must not be disabled when allowUnencrypted")
	// Replication must still be TLS regardless of allowUnencrypted.
	assert.Contains(t, conf, "tls-replication yes")
	assert.Contains(t, conf, "tls-cert-file /tls/tls.crt")
}

// TestGenerateValkeyConf_TLSOnly_PlaintextPortDisabled verifies that without
// allowUnencrypted, the TLS block emits "port 0" to disable the plaintext port.
// Note: the network section always emits "port 6379" first; the subsequent "port 0"
// in the TLS block overrides it (Valkey uses the last occurrence).
func TestGenerateValkeyConf_TLSOnly_PlaintextPortDisabled(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{
			Enabled:          true,
			AllowUnencrypted: false,
		}
	})

	conf := GenerateValkeyConf(v, false)

	// The TLS block must emit "port 0" to override the network section's "port 6379".
	assert.Contains(t, conf, "port 0")
	assert.Contains(t, conf, "tls-port 16379")
}

func TestGenerateValkeyConf_AuthEnabled(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
	})

	conf := GenerateValkeyConf(v, false)

	// Auth section should be present (password injected at runtime via command-line args).
	assert.Contains(t, conf, "# Auth")
	// Password should NOT be embedded in the ConfigMap (it comes from the Secret env var).
	assert.NotContains(t, conf, "requirepass")
	assert.NotContains(t, conf, "masterauth")
}

func TestGenerateValkeyConf_AuthEnabled_NoPasswordInConfig(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
	})

	conf := GenerateValkeyConf(v, false)

	// The ConfigMap must never contain the actual password or Secret name.
	assert.NotContains(t, conf, "my-secret")
}

func TestGenerateValkeyConf_AuthWithHA(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
		v.Spec.Sentinel = &vkov1.SentinelSpec{
			Enabled:  true,
			Replicas: 3,
		}
	})

	// Master config.
	masterConf := GenerateValkeyConf(v, false)
	assert.Contains(t, masterConf, "# Auth")
	assert.NotContains(t, masterConf, "replicaof")

	// Replica config.
	replicaConf := GenerateValkeyConf(v, true)
	assert.Contains(t, replicaConf, "# Auth")
	assert.Contains(t, replicaConf, "replicaof")
}

func TestGenerateValkeyConf_AuthWithTLS(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Auth = &vkov1.AuthSpec{
			SecretName:        "my-secret",
			SecretPasswordKey: "password",
		}
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
	})

	conf := GenerateValkeyConf(v, false)

	assert.Contains(t, conf, "# Auth")
	assert.Contains(t, conf, "tls-port 16379")
}

func TestGenerateValkeyConf_NoEmptyLines(t *testing.T) {
	v := newTestValkey("test")
	conf := GenerateValkeyConf(v, false)

	// Config should not start or end with empty line (just reasonable formatting).
	lines := strings.Split(conf, "\n")
	require.True(t, len(lines) > 5, "config should have multiple lines")
}

// --- BuildConfigMap ---

func TestBuildConfigMap(t *testing.T) {
	v := newTestValkey("test")

	cm := BuildConfigMap(v)

	assert.Equal(t, "test-config", cm.Name)
	assert.Equal(t, "default", cm.Namespace)
	assert.Contains(t, cm.Data, ValkeyConfigKey)
	assert.NotEmpty(t, cm.Data[ValkeyConfigKey])

	// Labels.
	assert.Equal(t, "valkey", cm.Labels["app.kubernetes.io/component"])
	assert.Equal(t, "test", cm.Labels["app.kubernetes.io/instance"])
	assert.Equal(t, "vko.gtrfc.com", cm.Labels["app.kubernetes.io/managed-by"])
}

func TestBuildConfigMap_DifferentNamespaces(t *testing.T) {
	v := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Namespace = "custom-ns"
	})

	cm := BuildConfigMap(v)

	assert.Equal(t, "custom-ns", cm.Namespace)
}

// --- ComputeConfigHash ---

func TestComputeConfigHash_Deterministic(t *testing.T) {
	v := newTestValkey("test")
	// Same input must always yield the same hash.
	assert.Equal(t, ComputeConfigHash(v), ComputeConfigHash(v))
}

func TestComputeConfigHash_NotEmpty(t *testing.T) {
	v := newTestValkey("test")
	assert.NotEmpty(t, ComputeConfigHash(v))
}

func TestComputeConfigHash_ChangesWhenAllowUnencryptedToggled(t *testing.T) {
	// TLS enabled, allowUnencrypted = false.
	v1 := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true, SecretName: "tls-secret"}
	})

	// TLS enabled, allowUnencrypted = true → different config → different hash.
	v2 := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true, SecretName: "tls-secret", AllowUnencrypted: true}
	})

	assert.NotEqual(t, ComputeConfigHash(v1), ComputeConfigHash(v2),
		"hash must differ when allowUnencrypted is toggled")
}

func TestComputeConfigHash_ChangesWhenTLSToggled(t *testing.T) {
	vNoTLS := newTestValkey("test")
	vTLS := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true, SecretName: "tls-secret"}
	})
	assert.NotEqual(t, ComputeConfigHash(vNoTLS), ComputeConfigHash(vTLS))
}

func TestComputeConfigHash_IncludesSentinelConfig(t *testing.T) {
	// Standalone hash must differ from HA hash even if Valkey config is the same.
	vStandalone := newTestValkey("test")
	vHA := newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	})
	assert.NotEqual(t, ComputeConfigHash(vStandalone), ComputeConfigHash(vHA))
}
