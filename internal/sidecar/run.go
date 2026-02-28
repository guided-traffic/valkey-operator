// Package sidecar implements the sidecar logic that runs alongside each Valkey
// pod. It polls the local Valkey instance, patches the pod's role label, and
// exposes a readiness endpoint.
package sidecar

import (
	"context"
	"fmt"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	ctrl "sigs.k8s.io/controller-runtime"
)

// Config holds the sidecar configuration parsed from flags/env vars.
type Config struct {
	PollInterval time.Duration
	ValkeyAddr   string
	HealthAddr   string
	PodName      string
	PodNamespace string
	Password     string

	// TLS settings.
	TLSEnabled bool
	TLSCACert  string
	TLSCert    string
	TLSKey     string

	// Drain/failover settings.
	SentinelEnabled bool
	SentinelMonitor string
	SentinelAddrs   string // comma-separated sentinel addresses
	HeadlessSvc     string // headless service FQDN for replica discovery
	Replicas        int    // number of Valkey replicas in the StatefulSet
	FailoverTimeout time.Duration
}

// Run starts the sidecar polling loop and health server. It blocks until
// the context is cancelled (SIGTERM/SIGINT), then runs the graceful drain
// handler before shutting down.
func Run(ctx context.Context, cfg Config) error {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	logger := ctrl.Log.WithName("sidecar")

	logger.Info("starting sidecar",
		"pod", cfg.PodName,
		"namespace", cfg.PodNamespace,
		"valkeyAddr", cfg.ValkeyAddr,
		"pollInterval", cfg.PollInterval,
		"tlsEnabled", cfg.TLSEnabled,
		"sentinelEnabled", cfg.SentinelEnabled,
	)

	// Create shared dependencies used by both the labeler and drain handler.
	detector, err := newValkeyRoleDetector(cfg)
	if err != nil {
		return fmt.Errorf("creating role detector: %w", err)
	}

	patcher, err := newKubernetesPodPatcher()
	if err != nil {
		return fmt.Errorf("creating pod patcher: %w", err)
	}

	labeler := NewLabelerWithDeps(detector, patcher, cfg.PodName, cfg.PodNamespace, cfg.PollInterval)

	drainHandler, err := buildDrainHandler(cfg, detector, patcher)
	if err != nil {
		return fmt.Errorf("creating drain handler: %w", err)
	}

	healthSrv := NewHealthServer(cfg.HealthAddr)

	// Start the health server in the background.
	go func() {
		if srvErr := healthSrv.ListenAndServe(); srvErr != nil {
			logger.Error(srvErr, "health server error")
		}
	}()

	// Run the polling loop (blocks until SIGTERM/SIGINT cancels the context).
	labeler.Run(ctx, healthSrv)

	// Context was cancelled (SIGTERM received). Handle graceful drain.
	logger.Info("signal received, starting graceful drain")
	drainTimeout := cfg.FailoverTimeout
	if drainTimeout == 0 {
		drainTimeout = 60 * time.Second
	}
	drainCtx, drainCancel := context.WithTimeout(context.Background(), drainTimeout)
	defer drainCancel()
	if drainErr := drainHandler.Handle(drainCtx); drainErr != nil {
		logger.Error(drainErr, "drain handler error")
	}

	// Shut down health server.
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := healthSrv.Shutdown(shutCtx); err != nil {
		logger.Error(err, "health server shutdown error")
	}

	logger.Info("sidecar stopped")
	return nil
}
