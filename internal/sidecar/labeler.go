package sidecar

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/guided-traffic/valkey-operator/internal/common"
	"github.com/guided-traffic/valkey-operator/internal/valkeyclient"
)

// RoleDetector detects the current Valkey replication role.
// This interface allows mocking in tests.
type RoleDetector interface {
	// DetectRole returns "master", "replica", or an error.
	DetectRole() (string, error)
}

// PodPatcher patches labels on a pod.
// This interface allows mocking in tests.
type PodPatcher interface {
	// PatchLabel patches the given label key to the given value on the pod.
	PatchLabel(ctx context.Context, namespace, name, labelKey, labelValue string) error
}

// Labeler polls the local Valkey instance and patches the pod's role label.
type Labeler struct {
	detector     RoleDetector
	patcher      PodPatcher
	podName      string
	podNamespace string
	pollInterval time.Duration
	lastRole     string
}

// NewLabeler creates a new Labeler from the sidecar config.
func NewLabeler(cfg Config) (*Labeler, error) {
	detector, err := newValkeyRoleDetector(cfg)
	if err != nil {
		return nil, err
	}

	patcher, err := newKubernetesPodPatcher()
	if err != nil {
		return nil, err
	}

	return &Labeler{
		detector:     detector,
		patcher:      patcher,
		podName:      cfg.PodName,
		podNamespace: cfg.PodNamespace,
		pollInterval: cfg.PollInterval,
	}, nil
}

// NewLabelerWithDeps creates a Labeler with injected dependencies (for testing).
func NewLabelerWithDeps(detector RoleDetector, patcher PodPatcher, podName, podNamespace string, pollInterval time.Duration) *Labeler {
	return &Labeler{
		detector:     detector,
		patcher:      patcher,
		podName:      podName,
		podNamespace: podNamespace,
		pollInterval: pollInterval,
	}
}

// Run starts the polling loop. It blocks until the context is done.
// It notifies the health server once a role is successfully detected.
func (l *Labeler) Run(ctx context.Context, health *HealthServer) {
	logger := ctrl.Log.WithName("sidecar").WithName("labeler")
	ticker := time.NewTicker(l.pollInterval)
	defer ticker.Stop()

	// Do an immediate first poll.
	l.poll(ctx, logger, health)

	for {
		select {
		case <-ctx.Done():
			logger.Info("context cancelled, stopping labeler")
			return
		case <-ticker.C:
			l.poll(ctx, logger, health)
		}
	}
}

// poll performs a single poll-and-patch cycle.
func (l *Labeler) poll(ctx context.Context, logger interface {
	Info(string, ...interface{})
	Error(error, string, ...interface{})
}, health *HealthServer) {
	role, err := l.detector.DetectRole()
	if err != nil {
		logger.Error(err, "failed to detect role")
		return
	}

	// Mark ready once we know the role.
	if health != nil {
		health.SetReady()
	}

	// Only patch if role changed.
	if role == l.lastRole {
		return
	}

	logger.Info("role changed", "from", l.lastRole, "to", role)

	if err := l.patcher.PatchLabel(ctx, l.podNamespace, l.podName, common.LabelInstanceRole, role); err != nil {
		logger.Error(err, "failed to patch pod label", "role", role)
		return
	}

	l.lastRole = role
}

// --- valkeyRoleDetector ---

// valkeyRoleDetector detects the role by calling INFO REPLICATION on the local Valkey.
type valkeyRoleDetector struct {
	client *valkeyclient.Client
}

func newValkeyRoleDetector(cfg Config) (*valkeyRoleDetector, error) {
	var client *valkeyclient.Client

	if cfg.TLSEnabled {
		tlsCfg, err := buildSidecarTLSConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("building TLS config: %w", err)
		}
		if cfg.Password != "" {
			client = valkeyclient.NewTLSWithPassword(cfg.ValkeyAddr, tlsCfg, cfg.Password)
		} else {
			client = valkeyclient.NewTLS(cfg.ValkeyAddr, tlsCfg)
		}
	} else {
		if cfg.Password != "" {
			client = valkeyclient.NewWithPassword(cfg.ValkeyAddr, cfg.Password)
		} else {
			client = valkeyclient.New(cfg.ValkeyAddr)
		}
	}

	return &valkeyRoleDetector{
		client: client,
	}, nil
}

// DetectRole queries INFO REPLICATION and returns "master" or "replica".
func (d *valkeyRoleDetector) DetectRole() (string, error) {
	info, err := d.client.InfoReplication()
	if err != nil {
		return "", err
	}

	switch info.Role {
	case "master":
		return common.RoleMaster, nil
	case "slave":
		return common.RoleReplica, nil
	default:
		return info.Role, nil
	}
}

// --- kubernetesPodPatcher ---

// kubernetesPodPatcher uses the Kubernetes API to patch pod labels.
type kubernetesPodPatcher struct {
	clientset kubernetes.Interface
}

func newKubernetesPodPatcher() (*kubernetesPodPatcher, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("getting in-cluster config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}

	return &kubernetesPodPatcher{clientset: clientset}, nil
}

// PatchLabel performs a strategic merge patch on the pod to update a label.
func (p *kubernetesPodPatcher) PatchLabel(ctx context.Context, namespace, name, labelKey, labelValue string) error {
	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]string{
				labelKey: labelValue,
			},
		},
	}

	patchData, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshalling patch: %w", err)
	}

	_, err = p.clientset.CoreV1().Pods(namespace).Patch(
		ctx,
		name,
		types.MergePatchType,
		patchData,
		metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("patching pod %s/%s: %w", namespace, name, err)
	}

	return nil
}

// buildSidecarTLSConfig builds a TLS config from the sidecar configuration.
func buildSidecarTLSConfig(cfg Config) (*tls.Config, error) {
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// Load CA cert if provided.
	if cfg.TLSCACert != "" {
		caCert, err := os.ReadFile(cfg.TLSCACert)
		if err != nil {
			return nil, fmt.Errorf("reading CA cert %s: %w", cfg.TLSCACert, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA cert from %s", cfg.TLSCACert)
		}
		tlsCfg.RootCAs = pool
	}

	// Load client cert/key if provided.
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			return nil, fmt.Errorf("loading client certificate: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return tlsCfg, nil
}
