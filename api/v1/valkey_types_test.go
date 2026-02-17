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

// --- Full CRD Struct Construction ---

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

// --- PersistenceMode ---

func TestPersistenceMode_Values(t *testing.T) {
	assert.Equal(t, PersistenceMode("rdb"), PersistenceModeRDB)
	assert.Equal(t, PersistenceMode("aof"), PersistenceModeAOF)
	assert.Equal(t, PersistenceMode("both"), PersistenceModeBoth)
}
