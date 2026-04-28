package v1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newValkey(name string, opts ...func(*Valkey)) *Valkey {
	v := &Valkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: ValkeySpec{
			Replicas: 1,
			Image:    "valkey/valkey:8.0",
		},
	}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// --- Helper Method Tests ---

func TestIsSentinelEnabled(t *testing.T) {
	tests := []struct {
		name     string
		sentinel *SentinelSpec
		expected bool
	}{
		{
			name:     "nil sentinel spec",
			sentinel: nil,
			expected: false,
		},
		{
			name:     "sentinel disabled",
			sentinel: &SentinelSpec{Enabled: false},
			expected: false,
		},
		{
			name:     "sentinel enabled",
			sentinel: &SentinelSpec{Enabled: true, Replicas: 3},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newValkey("test", func(v *Valkey) {
				v.Spec.Sentinel = tt.sentinel
			})
			assert.Equal(t, tt.expected, v.IsSentinelEnabled())
		})
	}
}

func TestIsMultiReplicaWithoutSentinel(t *testing.T) {
	tests := []struct {
		name     string
		replicas int32
		sentinel *SentinelSpec
		expected bool
	}{
		{
			name:     "single replica, no sentinel",
			replicas: 1,
			sentinel: nil,
			expected: false,
		},
		{
			name:     "multi replica, no sentinel",
			replicas: 3,
			sentinel: nil,
			expected: true,
		},
		{
			name:     "multi replica, sentinel enabled",
			replicas: 3,
			sentinel: &SentinelSpec{Enabled: true, Replicas: 3},
			expected: false,
		},
		{
			name:     "multi replica, sentinel disabled",
			replicas: 3,
			sentinel: &SentinelSpec{Enabled: false},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newValkey("test", func(v *Valkey) {
				v.Spec.Replicas = tt.replicas
				v.Spec.Sentinel = tt.sentinel
			})
			assert.Equal(t, tt.expected, v.IsMultiReplicaWithoutSentinel())
		})
	}
}

func TestIsAuthEnabled(t *testing.T) {
	tests := []struct {
		name     string
		auth     *AuthSpec
		expected bool
	}{
		{
			name:     "nil auth spec",
			auth:     nil,
			expected: false,
		},
		{
			name:     "empty secret name",
			auth:     &AuthSpec{SecretName: ""},
			expected: false,
		},
		{
			name:     "auth enabled",
			auth:     &AuthSpec{SecretName: "my-secret", SecretPasswordKey: "password"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newValkey("test", func(v *Valkey) {
				v.Spec.Auth = tt.auth
			})
			assert.Equal(t, tt.expected, v.IsAuthEnabled())
		})
	}
}

func TestIsTLSEnabled(t *testing.T) {
	tests := []struct {
		name     string
		tls      *TLSSpec
		expected bool
	}{
		{
			name:     "nil TLS spec",
			tls:      nil,
			expected: false,
		},
		{
			name:     "TLS disabled",
			tls:      &TLSSpec{Enabled: false},
			expected: false,
		},
		{
			name:     "TLS enabled",
			tls:      &TLSSpec{Enabled: true},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newValkey("test", func(v *Valkey) {
				v.Spec.TLS = tt.tls
			})
			assert.Equal(t, tt.expected, v.IsTLSEnabled())
		})
	}
}

func TestIsCertManagerEnabled(t *testing.T) {
	tests := []struct {
		name     string
		tls      *TLSSpec
		expected bool
	}{
		{
			name:     "nil TLS spec",
			tls:      nil,
			expected: false,
		},
		{
			name:     "TLS disabled with cert-manager",
			tls:      &TLSSpec{Enabled: false, CertManager: &CertManagerSpec{Issuer: CertManagerIssuerSpec{Kind: "Issuer", Name: "ca"}}},
			expected: false,
		},
		{
			name:     "TLS enabled without cert-manager",
			tls:      &TLSSpec{Enabled: true, SecretName: "my-secret"},
			expected: false,
		},
		{
			name: "TLS enabled with cert-manager",
			tls: &TLSSpec{
				Enabled: true,
				CertManager: &CertManagerSpec{
					Issuer: CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "ca"},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newValkey("test", func(v *Valkey) {
				v.Spec.TLS = tt.tls
			})
			assert.Equal(t, tt.expected, v.IsCertManagerEnabled())
		})
	}
}

func TestIsUnifiedCertificateEnabled(t *testing.T) {
	tests := []struct {
		name     string
		tls      *TLSSpec
		expected bool
	}{
		{
			name:     "nil TLS spec",
			tls:      nil,
			expected: false,
		},
		{
			name: "TLS enabled without unified flag",
			tls: &TLSSpec{
				Enabled: true,
				CertManager: &CertManagerSpec{
					Issuer: CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "ca"},
				},
			},
			expected: false,
		},
		{
			name: "cert-manager + unified flag",
			tls: &TLSSpec{
				Enabled:            true,
				UnifiedCertificate: true,
				CertManager: &CertManagerSpec{
					Issuer: CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "ca"},
				},
			},
			expected: true,
		},
		{
			name: "user secret + unified flag",
			tls: &TLSSpec{
				Enabled:            true,
				UnifiedCertificate: true,
				SecretName:         "my-secret",
			},
			expected: true,
		},
		{
			name: "TLS disabled but unified flag set",
			tls: &TLSSpec{
				Enabled:            false,
				UnifiedCertificate: true,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newValkey("test", func(v *Valkey) {
				v.Spec.TLS = tt.tls
			})
			assert.Equal(t, tt.expected, v.IsUnifiedCertificateEnabled())
		})
	}
}

func TestIsTLSSecretProvided(t *testing.T) {
	tests := []struct {
		name     string
		tls      *TLSSpec
		expected bool
	}{
		{
			name:     "nil TLS spec",
			tls:      nil,
			expected: false,
		},
		{
			name:     "TLS disabled with secret",
			tls:      &TLSSpec{Enabled: false, SecretName: "my-secret"},
			expected: false,
		},
		{
			name:     "TLS enabled with empty secret name",
			tls:      &TLSSpec{Enabled: true},
			expected: false,
		},
		{
			name:     "TLS enabled with secret name",
			tls:      &TLSSpec{Enabled: true, SecretName: "my-tls-secret"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newValkey("test", func(v *Valkey) {
				v.Spec.TLS = tt.tls
			})
			assert.Equal(t, tt.expected, v.IsTLSSecretProvided())
		})
	}
}

func TestIsMetricsEnabled(t *testing.T) {
	tests := []struct {
		name     string
		metrics  *MetricsSpec
		expected bool
	}{
		{
			name:     "nil metrics spec",
			metrics:  nil,
			expected: false,
		},
		{
			name:     "metrics disabled",
			metrics:  &MetricsSpec{Enabled: false},
			expected: false,
		},
		{
			name:     "metrics enabled",
			metrics:  &MetricsSpec{Enabled: true},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newValkey("test", func(v *Valkey) {
				v.Spec.Metrics = tt.metrics
			})
			assert.Equal(t, tt.expected, v.IsMetricsEnabled())
		})
	}
}

func TestIsNetworkPolicyEnabled(t *testing.T) {
	tests := []struct {
		name     string
		np       *NetworkPolicySpec
		expected bool
	}{
		{
			name:     "nil network policy spec",
			np:       nil,
			expected: false,
		},
		{
			name:     "network policy disabled",
			np:       &NetworkPolicySpec{Enabled: false},
			expected: false,
		},
		{
			name:     "network policy enabled",
			np:       &NetworkPolicySpec{Enabled: true, NamePrefix: "my-prefix"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newValkey("test", func(v *Valkey) {
				v.Spec.NetworkPolicy = tt.np
			})
			assert.Equal(t, tt.expected, v.IsNetworkPolicyEnabled())
		})
	}
}

func TestIsPersistenceEnabled(t *testing.T) {
	tests := []struct {
		name        string
		persistence *PersistenceSpec
		expected    bool
	}{
		{
			name:        "nil persistence spec",
			persistence: nil,
			expected:    false,
		},
		{
			name:        "persistence disabled",
			persistence: &PersistenceSpec{Enabled: false},
			expected:    false,
		},
		{
			name: "persistence enabled with RDB",
			persistence: &PersistenceSpec{
				Enabled: true,
				Mode:    PersistenceModeRDB,
				Size:    resource.MustParse("1Gi"),
			},
			expected: true,
		},
		{
			name: "persistence enabled with AOF",
			persistence: &PersistenceSpec{
				Enabled: true,
				Mode:    PersistenceModeAOF,
			},
			expected: true,
		},
		{
			name: "persistence enabled with both",
			persistence: &PersistenceSpec{
				Enabled: true,
				Mode:    PersistenceModeBoth,
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newValkey("test", func(v *Valkey) {
				v.Spec.Persistence = tt.persistence
			})
			assert.Equal(t, tt.expected, v.IsPersistenceEnabled())
		})
	}
}

func TestIsValkeyUnencryptedAllowed(t *testing.T) {
	tests := []struct {
		name     string
		tls      *TLSSpec
		expected bool
	}{
		{
			name:     "nil TLS spec",
			tls:      nil,
			expected: false,
		},
		{
			name:     "TLS disabled with allowUnencrypted true",
			tls:      &TLSSpec{Enabled: false, AllowUnencrypted: true},
			expected: false,
		},
		{
			name:     "TLS enabled, allowUnencrypted false",
			tls:      &TLSSpec{Enabled: true, AllowUnencrypted: false},
			expected: false,
		},
		{
			name:     "TLS enabled, allowUnencrypted true",
			tls:      &TLSSpec{Enabled: true, AllowUnencrypted: true},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newValkey("test", func(v *Valkey) {
				v.Spec.TLS = tt.tls
			})
			assert.Equal(t, tt.expected, v.IsValkeyUnencryptedAllowed())
		})
	}
}

func TestIsSentinelUnencryptedAllowed(t *testing.T) {
	tests := []struct {
		name     string
		tls      *TLSSpec
		sentinel *SentinelSpec
		expected bool
	}{
		{
			name:     "nil TLS, nil Sentinel",
			tls:      nil,
			sentinel: nil,
			expected: false,
		},
		{
			name:     "TLS disabled",
			tls:      &TLSSpec{Enabled: false},
			sentinel: &SentinelSpec{Enabled: true, Replicas: 3, AllowUnencrypted: true},
			expected: false,
		},
		{
			name:     "TLS enabled, Sentinel disabled",
			tls:      &TLSSpec{Enabled: true},
			sentinel: &SentinelSpec{Enabled: false, AllowUnencrypted: true},
			expected: false,
		},
		{
			name:     "TLS enabled, Sentinel enabled, allowUnencrypted false",
			tls:      &TLSSpec{Enabled: true},
			sentinel: &SentinelSpec{Enabled: true, Replicas: 3, AllowUnencrypted: false},
			expected: false,
		},
		{
			name:     "TLS enabled, Sentinel enabled, allowUnencrypted true",
			tls:      &TLSSpec{Enabled: true},
			sentinel: &SentinelSpec{Enabled: true, Replicas: 3, AllowUnencrypted: true},
			expected: true,
		},
		{
			name:     "TLS enabled, nil Sentinel",
			tls:      &TLSSpec{Enabled: true},
			sentinel: nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newValkey("test", func(v *Valkey) {
				v.Spec.TLS = tt.tls
				v.Spec.Sentinel = tt.sentinel
			})
			assert.Equal(t, tt.expected, v.IsSentinelUnencryptedAllowed())
		})
	}
}

// --- Full CRD Struct Construction ---

func TestIsSentinelAuthDisabled(t *testing.T) {
	tests := []struct {
		name     string
		auth     *AuthSpec
		sentinel *SentinelSpec
		expected bool
	}{
		{
			name:     "no auth, no sentinel",
			auth:     nil,
			sentinel: nil,
			expected: false,
		},
		{
			name:     "auth enabled, no sentinel",
			auth:     &AuthSpec{SecretName: "my-secret", SecretPasswordKey: "password"},
			sentinel: nil,
			expected: false,
		},
		{
			name:     "auth enabled, sentinel enabled, disableAuth false",
			auth:     &AuthSpec{SecretName: "my-secret", SecretPasswordKey: "password"},
			sentinel: &SentinelSpec{Enabled: true, Replicas: 3, DisableAuth: false},
			expected: false,
		},
		{
			name:     "auth enabled, sentinel enabled, disableAuth true",
			auth:     &AuthSpec{SecretName: "my-secret", SecretPasswordKey: "password"},
			sentinel: &SentinelSpec{Enabled: true, Replicas: 3, DisableAuth: true},
			expected: true,
		},
		{
			name:     "no auth, sentinel disableAuth true",
			auth:     nil,
			sentinel: &SentinelSpec{Enabled: true, Replicas: 3, DisableAuth: true},
			expected: false,
		},
		{
			name:     "auth enabled, sentinel disabled, disableAuth true",
			auth:     &AuthSpec{SecretName: "my-secret", SecretPasswordKey: "password"},
			sentinel: &SentinelSpec{Enabled: false, DisableAuth: true},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newValkey("test", func(v *Valkey) {
				v.Spec.Auth = tt.auth
				v.Spec.Sentinel = tt.sentinel
			})
			assert.Equal(t, tt.expected, v.IsSentinelAuthDisabled())
		})
	}
}

func TestValkeySpec_FullConfiguration(t *testing.T) {
	v := newValkey("full-test", func(v *Valkey) {
		v.Spec = ValkeySpec{
			Replicas: 3,
			Image:    "valkey/valkey:8.0",
			Sentinel: &SentinelSpec{
				Enabled:  true,
				Replicas: 3,
				PodLabels: map[string]string{
					"app": "sentinel",
				},
				PodAnnotations: map[string]string{
					"example.com/sentinel": "true",
				},
			},
			Auth: &AuthSpec{
				SecretName:        "my-secret",
				SecretPasswordKey: "password",
			},
			TLS: &TLSSpec{
				Enabled: true,
				CertManager: &CertManagerSpec{
					Issuer: CertManagerIssuerSpec{
						Group: "cert-manager.io",
						Kind:  "ClusterIssuer",
						Name:  "cluster-ca",
					},
				},
			},
			Metrics: &MetricsSpec{
				Enabled: true,
			},
			NetworkPolicy: &NetworkPolicySpec{
				Enabled:    true,
				NamePrefix: "my-prefix",
			},
			Persistence: &PersistenceSpec{
				Enabled:      true,
				Mode:         PersistenceModeRDB,
				StorageClass: "fast-ssd",
				Size:         resource.MustParse("10Gi"),
			},
			PodLabels: map[string]string{
				"app": "valkey",
			},
			PodAnnotations: map[string]string{
				"example.com/annotation": "true",
			},
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("250m"),
					corev1.ResourceMemory: resource.MustParse("256Mi"),
				},
			},
		}
	})

	assert.True(t, v.IsSentinelEnabled())
	assert.True(t, v.IsAuthEnabled())
	assert.True(t, v.IsTLSEnabled())
	assert.True(t, v.IsMetricsEnabled())
	assert.True(t, v.IsNetworkPolicyEnabled())
	assert.True(t, v.IsPersistenceEnabled())
	assert.Equal(t, int32(3), v.Spec.Replicas)
	assert.Equal(t, "valkey/valkey:8.0", v.Spec.Image)
	assert.Equal(t, int32(3), v.Spec.Sentinel.Replicas)
	assert.Equal(t, "ClusterIssuer", v.Spec.TLS.CertManager.Issuer.Kind)
	assert.Equal(t, PersistenceModeRDB, v.Spec.Persistence.Mode)
	assert.Equal(t, "fast-ssd", v.Spec.Persistence.StorageClass)
}

func TestValkeySpec_StandaloneMinimal(t *testing.T) {
	v := newValkey("standalone")

	assert.False(t, v.IsSentinelEnabled())
	assert.False(t, v.IsAuthEnabled())
	assert.False(t, v.IsTLSEnabled())
	assert.False(t, v.IsMetricsEnabled())
	assert.False(t, v.IsNetworkPolicyEnabled())
	assert.False(t, v.IsPersistenceEnabled())
	assert.Equal(t, int32(1), v.Spec.Replicas)
	assert.Equal(t, "valkey/valkey:8.0", v.Spec.Image)
}

// --- Status Tests ---

func TestValkeyStatus_PhaseValues(t *testing.T) {
	assert.Equal(t, ValkeyPhase("OK"), ValkeyPhaseOK)
	assert.Equal(t, ValkeyPhase("Provisioning"), ValkeyPhaseProvisioning)
	assert.Equal(t, ValkeyPhase("Syncing"), ValkeyphaseSyncing)
	assert.Equal(t, ValkeyPhase("Rolling Update"), ValkeyPhaseRollingUpdate)
	assert.Equal(t, ValkeyPhase("Failover in progress"), ValkeyPhaseFailover)
	assert.Equal(t, ValkeyPhase("Error"), ValkeyPhaseError)
}

func TestValkeyStatus_ConditionsSlice(t *testing.T) {
	v := newValkey("test")
	v.Status = ValkeyStatus{
		ReadyReplicas: 3,
		MasterPod:     "test-0",
		Phase:         ValkeyPhaseOK,
		Message:       "All replicas healthy",
		Conditions: []metav1.Condition{
			{
				Type:               "Ready",
				Status:             metav1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
				Reason:             "AllReplicasReady",
				Message:            "All replicas are ready",
			},
		},
	}

	assert.Equal(t, int32(3), v.Status.ReadyReplicas)
	assert.Equal(t, "test-0", v.Status.MasterPod)
	assert.Equal(t, ValkeyPhaseOK, v.Status.Phase)
	assert.Len(t, v.Status.Conditions, 1)
	assert.Equal(t, "Ready", v.Status.Conditions[0].Type)
}

// --- Observer Helper Tests ---

func TestIsObserverEnabled(t *testing.T) {
	tests := []struct {
		name     string
		observer *ObserverSpec
		expected bool
	}{
		{
			name:     "nil observer spec",
			observer: nil,
			expected: false,
		},
		{
			name:     "observer disabled",
			observer: &ObserverSpec{Enabled: false},
			expected: false,
		},
		{
			name:     "observer enabled",
			observer: &ObserverSpec{Enabled: true},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newValkey("test", func(v *Valkey) {
				v.Spec.Observer = tt.observer
			})
			assert.Equal(t, tt.expected, v.IsObserverEnabled())
		})
	}
}

func TestGetObserverDB(t *testing.T) {
	tests := []struct {
		name     string
		observer *ObserverSpec
		expected int
	}{
		{
			name:     "nil observer spec returns default 15",
			observer: nil,
			expected: 15,
		},
		{
			name:     "observer spec with nil DB returns default 15",
			observer: &ObserverSpec{Enabled: true},
			expected: 15,
		},
		{
			name:     "observer spec with explicit DB",
			observer: &ObserverSpec{Enabled: true, DB: intPtr(3)},
			expected: 3,
		},
		{
			name:     "observer spec with DB 0",
			observer: &ObserverSpec{Enabled: true, DB: intPtr(0)},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := newValkey("test", func(v *Valkey) {
				v.Spec.Observer = tt.observer
			})
			assert.Equal(t, tt.expected, v.GetObserverDB())
		})
	}
}

func TestGetObserverResources(t *testing.T) {
	t.Run("nil observer spec returns defaults", func(t *testing.T) {
		v := newValkey("test")
		res := v.GetObserverResources()
		assert.Equal(t, resource.MustParse("50m"), res.Requests[corev1.ResourceCPU])
		assert.Equal(t, resource.MustParse("64Mi"), res.Requests[corev1.ResourceMemory])
		assert.Empty(t, res.Limits)
	})

	t.Run("custom resources", func(t *testing.T) {
		customRes := &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
		}
		v := newValkey("test", func(v *Valkey) {
			v.Spec.Observer = &ObserverSpec{Enabled: true, Resources: customRes}
		})
		res := v.GetObserverResources()
		assert.Equal(t, resource.MustParse("100m"), res.Requests[corev1.ResourceCPU])
		assert.Equal(t, resource.MustParse("128Mi"), res.Requests[corev1.ResourceMemory])
		assert.Equal(t, resource.MustParse("256Mi"), res.Limits[corev1.ResourceMemory])
	})
}

func intPtr(i int) *int {
	return &i
}

// --- PersistenceMode ---

func TestPersistenceMode_Values(t *testing.T) {
	assert.Equal(t, PersistenceMode("rdb"), PersistenceModeRDB)
	assert.Equal(t, PersistenceMode("aof"), PersistenceModeAOF)
	assert.Equal(t, PersistenceMode("both"), PersistenceModeBoth)
}

func TestIsObserverValkeyMTLSEnabled(t *testing.T) {
	t.Run("nil Observer defaults to false", func(t *testing.T) {
		v := newValkey("test")
		assert.False(t, v.IsObserverValkeyMTLSEnabled())
	})

	t.Run("Observer set MTLS nil defaults to false", func(t *testing.T) {
		v := newValkey("test", func(v *Valkey) {
			v.Spec.Observer = &ObserverSpec{Enabled: true}
		})
		assert.False(t, v.IsObserverValkeyMTLSEnabled())
	})

	t.Run("MTLS set Valkey nil defaults to false", func(t *testing.T) {
		v := newValkey("test", func(v *Valkey) {
			v.Spec.Observer = &ObserverSpec{MTLS: &ObserverMTLSSpec{}}
		})
		assert.False(t, v.IsObserverValkeyMTLSEnabled())
	})

	t.Run("explicitly false", func(t *testing.T) {
		f := false
		v := newValkey("test", func(v *Valkey) {
			v.Spec.Observer = &ObserverSpec{MTLS: &ObserverMTLSSpec{Valkey: &f}}
		})
		assert.False(t, v.IsObserverValkeyMTLSEnabled())
	})

	t.Run("explicitly true", func(t *testing.T) {
		tr := true
		v := newValkey("test", func(v *Valkey) {
			v.Spec.Observer = &ObserverSpec{MTLS: &ObserverMTLSSpec{Valkey: &tr}}
		})
		assert.True(t, v.IsObserverValkeyMTLSEnabled())
	})
}

func TestIsObserverMTLSActive(t *testing.T) {
	t.Run("both nil defaults to false", func(t *testing.T) {
		v := newValkey("test")
		assert.False(t, v.IsObserverMTLSActive())
	})

	t.Run("valkey true activates", func(t *testing.T) {
		tr := true
		v := newValkey("test", func(v *Valkey) {
			v.Spec.Observer = &ObserverSpec{MTLS: &ObserverMTLSSpec{Valkey: &tr}}
		})
		assert.True(t, v.IsObserverMTLSActive())
	})

	t.Run("sentinel true activates", func(t *testing.T) {
		tr := true
		v := newValkey("test", func(v *Valkey) {
			v.Spec.Observer = &ObserverSpec{MTLS: &ObserverMTLSSpec{Sentinel: &tr}}
		})
		assert.True(t, v.IsObserverMTLSActive())
	})

	t.Run("both false not active", func(t *testing.T) {
		f := false
		v := newValkey("test", func(v *Valkey) {
			v.Spec.Observer = &ObserverSpec{MTLS: &ObserverMTLSSpec{Valkey: &f, Sentinel: &f}}
		})
		assert.False(t, v.IsObserverMTLSActive())
	})
}

func TestIsObserverSentinelMTLSEnabled(t *testing.T) {
	t.Run("nil Observer defaults to false", func(t *testing.T) {
		v := newValkey("test")
		assert.False(t, v.IsObserverSentinelMTLSEnabled())
	})

	t.Run("Observer set MTLS nil defaults to false", func(t *testing.T) {
		v := newValkey("test", func(v *Valkey) {
			v.Spec.Observer = &ObserverSpec{Enabled: true}
		})
		assert.False(t, v.IsObserverSentinelMTLSEnabled())
	})

	t.Run("MTLS set Sentinel nil defaults to false", func(t *testing.T) {
		v := newValkey("test", func(v *Valkey) {
			v.Spec.Observer = &ObserverSpec{MTLS: &ObserverMTLSSpec{}}
		})
		assert.False(t, v.IsObserverSentinelMTLSEnabled())
	})

	t.Run("explicitly true", func(t *testing.T) {
		tr := true
		v := newValkey("test", func(v *Valkey) {
			v.Spec.Observer = &ObserverSpec{MTLS: &ObserverMTLSSpec{Sentinel: &tr}}
		})
		assert.True(t, v.IsObserverSentinelMTLSEnabled())
	})

	t.Run("explicitly false", func(t *testing.T) {
		f := false
		v := newValkey("test", func(v *Valkey) {
			v.Spec.Observer = &ObserverSpec{MTLS: &ObserverMTLSSpec{Sentinel: &f}}
		})
		assert.False(t, v.IsObserverSentinelMTLSEnabled())
	})
}

func TestGetObserverLogLevel(t *testing.T) {
	t.Run("nil observer returns info default", func(t *testing.T) {
		v := newValkey("test")
		assert.Equal(t, "info", v.GetObserverLogLevel())
	})

	t.Run("empty log level returns info default", func(t *testing.T) {
		v := newValkey("test", func(v *Valkey) {
			v.Spec.Observer = &ObserverSpec{Enabled: true}
		})
		assert.Equal(t, "info", v.GetObserverLogLevel())
	})

	t.Run("debug level", func(t *testing.T) {
		v := newValkey("test", func(v *Valkey) {
			v.Spec.Observer = &ObserverSpec{LogLevel: ObserverLogLevelDebug}
		})
		assert.Equal(t, "debug", v.GetObserverLogLevel())
	})

	t.Run("warn level", func(t *testing.T) {
		v := newValkey("test", func(v *Valkey) {
			v.Spec.Observer = &ObserverSpec{LogLevel: ObserverLogLevelWarn}
		})
		assert.Equal(t, "warn", v.GetObserverLogLevel())
	})

	t.Run("error level", func(t *testing.T) {
		v := newValkey("test", func(v *Valkey) {
			v.Spec.Observer = &ObserverSpec{LogLevel: ObserverLogLevelError}
		})
		assert.Equal(t, "error", v.GetObserverLogLevel())
	})
}
func TestUnreadyWhenDefault(t *testing.T) {
	tr := true
	fa := false

	assert.True(t, UnreadyWhenDefault(nil), "nil pointer should default to true")
	assert.True(t, UnreadyWhenDefault(&tr), "explicit true remains true")
	assert.False(t, UnreadyWhenDefault(&fa), "explicit false returns false")
}

func TestGetObserverUnreadyWhen_NoObserver(t *testing.T) {
	v := newValkey("test")
	uw := v.GetObserverUnreadyWhen()
	// All fields nil → all effective values are true.
	assert.True(t, UnreadyWhenDefault(uw.MasterUnreachable))
	assert.True(t, UnreadyWhenDefault(uw.WriteTestFailure))
	assert.True(t, UnreadyWhenDefault(uw.ReplicaSyncFailure))
	assert.True(t, UnreadyWhenDefault(uw.SentinelUnreachable))
}

func TestGetObserverUnreadyWhen_NilUnreadyWhenSpec(t *testing.T) {
	v := newValkey("test", func(v *Valkey) {
		v.Spec.Observer = &ObserverSpec{Enabled: true}
	})
	uw := v.GetObserverUnreadyWhen()
	assert.True(t, UnreadyWhenDefault(uw.MasterUnreachable))
}

func TestGetObserverUnreadyWhen_PartialOverride(t *testing.T) {
	f := false
	v := newValkey("test", func(v *Valkey) {
		v.Spec.Observer = &ObserverSpec{
			Enabled: true,
			UnreadyWhen: &ObserverUnreadyWhenSpec{
				ReplicaSyncFailure: &f,
			},
		}
	})
	uw := v.GetObserverUnreadyWhen()
	assert.True(t, UnreadyWhenDefault(uw.MasterUnreachable), "unset field defaults to true")
	assert.False(t, UnreadyWhenDefault(uw.ReplicaSyncFailure), "explicitly false field is false")
}
