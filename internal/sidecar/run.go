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
}

// Run starts the sidecar polling loop and health server. It blocks until
// the context is cancelled (SIGTERM/SIGINT).
func Run(ctx context.Context, cfg Config) error {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	logger := ctrl.Log.WithName("sidecar")

	logger.Info("starting sidecar",
		"pod", cfg.PodName,
		"namespace", cfg.PodNamespace,
		"valkeyAddr", cfg.ValkeyAddr,
		"pollInterval", cfg.PollInterval,
		"tlsEnabled", cfg.TLSEnabled,
	)

	labeler, err := NewLabeler(cfg)
	if err != nil {
		return fmt.Errorf("creating labeler: %w", err)
	}

	healthSrv := NewHealthServer(cfg.HealthAddr)

	// Start the health server in the background.
	go func() {
		if srvErr := healthSrv.ListenAndServe(); srvErr != nil {
			logger.Error(srvErr, "health server error")
		}
	}()

	// Run the polling loop.
	labeler.Run(ctx, healthSrv)

	// Shut down health server.
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := healthSrv.Shutdown(shutCtx); err != nil {
		logger.Error(err, "health server shutdown error")
	}

	logger.Info("sidecar stopped")
	return nil
}
