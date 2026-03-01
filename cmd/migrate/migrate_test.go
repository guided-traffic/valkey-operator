package migrate

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

func TestApplyDefaults_ReplicasZero(t *testing.T) {
	vk := &vkov1.Valkey{}
	vk.Spec.Replicas = 0

	if !applyDefaults(vk) {
		t.Fatal("expected changed=true when replicas is 0")
	}
	if vk.Spec.Replicas != 1 {
		t.Errorf("expected Replicas=1, got %d", vk.Spec.Replicas)
	}
}

func TestApplyDefaults_ReplicasAlreadySet(t *testing.T) {
	vk := &vkov1.Valkey{}
	vk.Spec.Replicas = 3

	if applyDefaults(vk) {
		t.Fatal("expected changed=false when replicas already set")
	}
}

func TestApplyDefaults_AuthPasswordKeyDefault(t *testing.T) {
	vk := &vkov1.Valkey{}
	vk.Spec.Replicas = 1
	vk.Spec.Auth = &vkov1.AuthSpec{SecretName: "my-secret"}

	if !applyDefaults(vk) {
		t.Fatal("expected changed=true for empty SecretPasswordKey")
	}
	if vk.Spec.Auth.SecretPasswordKey != "password" {
		t.Errorf("expected SecretPasswordKey=password, got %s", vk.Spec.Auth.SecretPasswordKey)
	}
}

func TestApplyDefaults_AuthPasswordKeyAlreadySet(t *testing.T) {
	vk := &vkov1.Valkey{}
	vk.Spec.Replicas = 1
	vk.Spec.Auth = &vkov1.AuthSpec{SecretName: "my-secret", SecretPasswordKey: "pass"}

	if applyDefaults(vk) {
		t.Fatal("expected changed=false when SecretPasswordKey is already set")
	}
}

func TestApplyDefaults_TLSIssuerGroupDefault(t *testing.T) {
	vk := &vkov1.Valkey{}
	vk.Spec.Replicas = 1
	vk.Spec.TLS = &vkov1.TLSSpec{
		Enabled: true,
		CertManager: &vkov1.CertManagerSpec{
			Issuer: vkov1.CertManagerIssuerSpec{Kind: "ClusterIssuer", Name: "my-ca"},
		},
	}

	if !applyDefaults(vk) {
		t.Fatal("expected changed=true for empty issuer group")
	}
	if vk.Spec.TLS.CertManager.Issuer.Group != "cert-manager.io" {
		t.Errorf("expected group=cert-manager.io, got %s", vk.Spec.TLS.CertManager.Issuer.Group)
	}
}

func TestApplyDefaults_TLSIssuerGroupAlreadySet(t *testing.T) {
	vk := &vkov1.Valkey{}
	vk.Spec.Replicas = 1
	vk.Spec.TLS = &vkov1.TLSSpec{
		Enabled: true,
		CertManager: &vkov1.CertManagerSpec{
			Issuer: vkov1.CertManagerIssuerSpec{
				Group: "cert-manager.io",
				Kind:  "ClusterIssuer",
				Name:  "my-ca",
			},
		},
	}

	if applyDefaults(vk) {
		t.Fatal("expected changed=false when issuer group already set")
	}
}

func TestApplyDefaults_SentinelReplicasDefault(t *testing.T) {
	vk := &vkov1.Valkey{}
	vk.Spec.Replicas = 3
	vk.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true}

	if !applyDefaults(vk) {
		t.Fatal("expected changed=true for sentinel.replicas=0")
	}
	if vk.Spec.Sentinel.Replicas != 3 {
		t.Errorf("expected Sentinel.Replicas=3, got %d", vk.Spec.Sentinel.Replicas)
	}
}

func TestApplyDefaults_PersistenceModeDefault(t *testing.T) {
	vk := &vkov1.Valkey{}
	vk.Spec.Replicas = 1
	vk.Spec.Persistence = &vkov1.PersistenceSpec{
		Enabled: true,
		Size:    resource.MustParse("1Gi"),
	}

	if !applyDefaults(vk) {
		t.Fatal("expected changed=true for empty persistence mode")
	}
	if vk.Spec.Persistence.Mode != vkov1.PersistenceModeRDB {
		t.Errorf("expected Mode=rdb, got %s", vk.Spec.Persistence.Mode)
	}
}

func TestApplyDefaults_PersistenceSizeDefault(t *testing.T) {
	vk := &vkov1.Valkey{}
	vk.Spec.Replicas = 1
	vk.Spec.Persistence = &vkov1.PersistenceSpec{
		Enabled: true,
		Mode:    vkov1.PersistenceModeRDB,
	}

	if !applyDefaults(vk) {
		t.Fatal("expected changed=true for zero persistence size")
	}
	expected := resource.MustParse("1Gi")
	if vk.Spec.Persistence.Size.Cmp(expected) != 0 {
		t.Errorf("expected Size=1Gi, got %s", vk.Spec.Persistence.Size.String())
	}
}

func TestApplyDefaults_NoChangesNeeded(t *testing.T) {
	vk := &vkov1.Valkey{}
	vk.Spec.Replicas = 1
	vk.Spec.Sentinel = &vkov1.SentinelSpec{Enabled: true, Replicas: 3}
	vk.Spec.Auth = &vkov1.AuthSpec{SecretName: "s", SecretPasswordKey: "password"}
	vk.Spec.TLS = &vkov1.TLSSpec{
		Enabled: true,
		CertManager: &vkov1.CertManagerSpec{
			Issuer: vkov1.CertManagerIssuerSpec{
				Group: "cert-manager.io",
				Kind:  "ClusterIssuer",
				Name:  "ca",
			},
		},
	}
	vk.Spec.Persistence = &vkov1.PersistenceSpec{
		Enabled: true,
		Mode:    vkov1.PersistenceModeBoth,
		Size:    resource.MustParse("5Gi"),
	}

	if applyDefaults(vk) {
		t.Fatal("expected changed=false when all fields already have correct values")
	}
}

func TestApplyDefaults_NilOptionalFields(t *testing.T) {
	// Nil optional structs must not cause panics and must not count as changes
	// (when replicas is already 1)
	vk := &vkov1.Valkey{}
	vk.Spec.Replicas = 1
	// Auth, TLS, Sentinel, Persistence all nil

	if applyDefaults(vk) {
		t.Fatal("expected changed=false for nil optional fields with replicas=1")
	}
}
