// Package observer implements the observer entry point that runs as a
// standalone deployment alongside the Valkey cluster. It continuously
// monitors cluster health and exposes the results via HTTP endpoints.
package observer

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/guided-traffic/valkey-operator/internal/observer"
)

// Run is the main entry point for the observer process.
// It parses CLI flags / env vars, starts the observer polling loop and the
// health HTTP server, and blocks until SIGTERM or SIGINT is received.
func Run() {
	cfg := parseFlags()

	if cfg.Namespace == "" || cfg.ClusterName == "" {
		fmt.Fprintln(os.Stderr, "error: --namespace and --cluster-name (or POD_NAMESPACE / CLUSTER_NAME env vars) are required")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	if err := observer.Run(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "observer error: %v\n", err)
		os.Exit(1)
	}
}

// parseFlags parses CLI flags and falls back to environment variables.
func parseFlags() observer.Config {
	var cfg observer.Config

	flag.StringVar(&cfg.Namespace, "namespace", envString("POD_NAMESPACE", ""), "Namespace of the Valkey cluster")
	flag.StringVar(&cfg.ClusterName, "cluster-name", envString("CLUSTER_NAME", ""), "Name of the Valkey CR")
	flag.StringVar(&cfg.HealthAddr, "health-addr", envString("OBSERVER_HEALTH_ADDR", ":8084"), "Health server bind address")
	flag.DurationVar(&cfg.PollInterval, "poll-interval", envDuration("OBSERVER_POLL_INTERVAL", 2*time.Second), "Check interval")
	flag.StringVar(&cfg.ValkeyHeadlessSvc, "valkey-headless-svc", envString("VALKEY_HEADLESS_SVC", ""), "Headless Service FQDN of Valkey pods")
	flag.IntVar(&cfg.Replicas, "replicas", envInt("REPLICAS", 1), "Expected number of Valkey replicas")

	// TLS flags.
	flag.BoolVar(&cfg.TLSEnabled, "tls-enabled", envBool("TLS_ENABLED"), "TLS enabled")
	flag.StringVar(&cfg.TLSCACert, "tls-ca-cert", envString("TLS_CA_CERT", ""), "CA certificate path")
	flag.StringVar(&cfg.TLSCert, "tls-cert", envString("TLS_CERT", ""), "Client certificate path")
	flag.StringVar(&cfg.TLSKey, "tls-key", envString("TLS_KEY", ""), "Client key path")
	flag.BoolVar(&cfg.ValkeyMTLS, "valkey-mtls", envBool("VALKEY_MTLS"), "Send client certificate to Valkey (mTLS)")
	flag.BoolVar(&cfg.SentinelMTLS, "sentinel-mtls", envBool("SENTINEL_MTLS"), "Send client certificate to Sentinel (mTLS)")

	// Sentinel flags.
	flag.BoolVar(&cfg.SentinelEnabled, "sentinel-enabled", envBool("SENTINEL_ENABLED"), "Sentinel mode active")
	flag.StringVar(&cfg.SentinelAddrs, "sentinel-addrs", envString("SENTINEL_ADDRS", ""), "Comma-separated Sentinel addresses")
	flag.StringVar(&cfg.SentinelMonitor, "sentinel-monitor", envString("SENTINEL_MONITOR", ""), "Sentinel monitor name")
	flag.BoolVar(&cfg.SentinelDisableAuth, "sentinel-disable-auth", envBool("SENTINEL_DISABLE_AUTH"), "Sentinel auth disabled")

	// Observer DB.
	flag.IntVar(&cfg.ObserverDB, "observer-db", envInt("OBSERVER_DB", 15), "Valkey DB for health key (0-15)")

	// Log level.
	flag.StringVar(&cfg.LogLevel, "log-level", envString("LOG_LEVEL", "info"), "Log verbosity: debug, info, warn, error")

	flag.Parse()

	// Read auth password from environment.
	cfg.Password = os.Getenv("VALKEY_PASSWORD")

	// Parse sentinel addresses into slice.
	if cfg.SentinelAddrs != "" {
		cfg.SentinelAddrList = strings.Split(cfg.SentinelAddrs, ",")
	}

	return cfg
}

func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string) bool {
	v := os.Getenv(key)
	return v == "true" || v == "1" || v == "yes"
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
	}
	return def
}
