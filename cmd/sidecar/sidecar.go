// Package sidecar implements the sidecar entry point that runs alongside each
// Valkey pod. It polls the local Valkey instance for its replication role and
// patches the pod's instanceRole label accordingly, and exposes a readiness
// health endpoint.
package sidecar

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/guided-traffic/valkey-operator/internal/sidecar"
)

// Run is the main entry point for the sidecar process.
// It parses CLI flags / env vars, starts the labeler polling loop and the
// readiness HTTP server, and blocks until SIGTERM or SIGINT is received.
func Run() {
	cfg := parseFlags()

	if cfg.PodName == "" || cfg.PodNamespace == "" {
		fmt.Fprintln(os.Stderr, "error: --pod-name and --pod-namespace (or POD_NAME / POD_NAMESPACE env vars) are required")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	if err := sidecar.Run(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "sidecar error: %v\n", err)
		os.Exit(1)
	}
}

// parseFlags parses CLI flags and falls back to environment variables.
func parseFlags() sidecar.Config {
	var cfg sidecar.Config

	flag.DurationVar(&cfg.PollInterval, "poll-interval", envDuration("SIDECAR_POLL_INTERVAL", 1*time.Second), "How often to poll INFO replication")
	flag.StringVar(&cfg.ValkeyAddr, "valkey-addr", envString("SIDECAR_VALKEY_ADDR", "localhost:6379"), "Address of the local Valkey instance")
	flag.StringVar(&cfg.HealthAddr, "health-addr", envString("SIDECAR_HEALTH_ADDR", ":8082"), "Address for the readiness HTTP endpoint")
	flag.StringVar(&cfg.PodName, "pod-name", envString("POD_NAME", ""), "Own pod name (from Downward API)")
	flag.StringVar(&cfg.PodNamespace, "pod-namespace", envString("POD_NAMESPACE", ""), "Own namespace (from Downward API)")
	flag.BoolVar(&cfg.TLSEnabled, "tls-enabled", envBool("TLS_ENABLED"), "Enable TLS for Valkey connection")
	flag.StringVar(&cfg.TLSCACert, "tls-ca-cert", envString("TLS_CA_CERT", ""), "Path to TLS CA certificate")
	flag.StringVar(&cfg.TLSCert, "tls-cert", envString("TLS_CERT", ""), "Path to TLS client certificate")
	flag.StringVar(&cfg.TLSKey, "tls-key", envString("TLS_KEY", ""), "Path to TLS client key")

	flag.Parse()

	// Read auth password from environment (same var as the main container).
	cfg.Password = os.Getenv("VALKEY_PASSWORD")

	return cfg
}

// envString returns the environment variable value or a default.
func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envBool returns true if the environment variable is set to a truthy value.
func envBool(key string) bool {
	v := os.Getenv(key)
	return v == "true" || v == "1" || v == "yes"
}

// envDuration parses a duration from the environment variable or returns the default.
func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
	}
	return def
}
