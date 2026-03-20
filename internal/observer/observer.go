// Package observer implements the observer polling loop and health check
// orchestration for monitoring Valkey cluster health.
package observer

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// Config holds the observer configuration parsed from flags/env vars.
type Config struct {
	Namespace         string
	ClusterName       string
	HealthAddr        string
	PollInterval      time.Duration
	ValkeyHeadlessSvc string
	Replicas          int
	Password          string

	// TLS settings.
	TLSEnabled bool
	TLSCACert  string
	TLSCert    string
	TLSKey     string

	// Sentinel settings.
	SentinelEnabled     bool
	SentinelAddrs       string
	SentinelAddrList    []string
	SentinelMonitor     string
	SentinelDisableAuth bool

	// Observer DB for health key.
	ObserverDB int
}

// CheckResult holds the outcome of a single check cycle.
type CheckResult struct {
	Ready     bool            `json:"ready"`
	Checks    map[string]bool `json:"checks"`
	Message   string          `json:"message,omitempty"`
	LastCheck time.Time       `json:"lastCheck"`
}

// Observer runs periodic health checks against a Valkey cluster.
type Observer struct {
	cfg       Config
	tlsConfig *tls.Config
	mu        sync.RWMutex
	result    CheckResult
	metrics   *observerMetrics
}

// New creates a new Observer with the given configuration.
func New(cfg Config) (*Observer, error) {
	var tlsCfg *tls.Config
	if cfg.TLSEnabled {
		var err error
		tlsCfg, err = buildTLSConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("building TLS config: %w", err)
		}
	}

	return &Observer{
		cfg:       cfg,
		tlsConfig: tlsCfg,
		result: CheckResult{
			Ready:  false,
			Checks: make(map[string]bool),
		},
		metrics: newObserverMetrics(),
	}, nil
}

// Run starts the observer polling loop and health server.
// It blocks until the context is cancelled.
func Run(ctx context.Context, cfg Config) error {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	logger := ctrl.Log.WithName("observer")

	logger.Info("starting observer",
		"namespace", cfg.Namespace,
		"cluster", cfg.ClusterName,
		"pollInterval", cfg.PollInterval,
		"replicas", cfg.Replicas,
		"sentinelEnabled", cfg.SentinelEnabled,
		"tlsEnabled", cfg.TLSEnabled,
		"observerDB", cfg.ObserverDB,
	)

	obs, err := New(cfg)
	if err != nil {
		return fmt.Errorf("creating observer: %w", err)
	}

	// Register Prometheus metrics.
	prometheus.MustRegister(obs.metrics.collectors()...)

	srv := NewHealthServer(cfg.HealthAddr, obs)
	go func() {
		if srvErr := srv.ListenAndServe(); srvErr != nil {
			logger.Error(srvErr, "health server error")
		}
	}()

	// Polling loop.
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("shutting down observer")
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutCtx)
			return nil
		case <-ticker.C:
			obs.runChecks(ctx)
		}
	}
}

// GetResult returns the latest check result (thread-safe).
func (o *Observer) GetResult() CheckResult {
	o.mu.RLock()
	defer o.mu.RUnlock()
	// Return a copy.
	checks := make(map[string]bool, len(o.result.Checks))
	for k, v := range o.result.Checks {
		checks[k] = v
	}
	return CheckResult{
		Ready:     o.result.Ready,
		Checks:    checks,
		Message:   o.result.Message,
		LastCheck: o.result.LastCheck,
	}
}

// runChecks executes all health checks and updates the result.
func (o *Observer) runChecks(ctx context.Context) {
	logger := ctrl.Log.WithName("observer")
	start := time.Now()

	checks := make(map[string]bool)
	var firstFailMsg string

	// 1. Identify master address.
	masterAddr, err := o.discoverMaster(ctx)
	if err != nil {
		logger.Error(err, "failed to discover master")
		o.setResult(false, map[string]bool{"master_reachable": false}, fmt.Sprintf("master discovery failed: %v", err), start)
		o.metrics.recordCycle(time.Since(start), false)
		return
	}

	// 2-6. Core checks.
	healthValue := fmt.Sprintf("%d", time.Now().UnixNano())
	firstFailMsg = o.runCoreChecks(ctx, masterAddr, healthValue, checks)

	// 7-8. Sentinel checks.
	if o.cfg.SentinelEnabled {
		if msg := o.runSentinelChecks(checks); msg != "" && firstFailMsg == "" {
			firstFailMsg = msg
		}
	}

	allOK := firstFailMsg == ""
	o.setResult(allOK, checks, firstFailMsg, start)
	o.metrics.recordCycle(time.Since(start), allOK)
}

// runCoreChecks runs PING, replica sync, write, read, and replica read checks.
// Returns the first failure message (empty if all passed).
func (o *Observer) runCoreChecks(_ context.Context, masterAddr, healthValue string, checks map[string]bool) string {
	var firstFailMsg string

	// PING master.
	firstFailMsg = runCheck(checks, "master_reachable", firstFailMsg, func() error {
		return o.pingHost(masterAddr)
	})

	// Replica sync (only multi-replica).
	if o.cfg.Replicas > 1 {
		firstFailMsg = runCheck(checks, "replica_sync", firstFailMsg, func() error {
			return o.checkReplicaSync(masterAddr)
		})
	}

	// Write test on master.
	firstFailMsg = runCheck(checks, "write_test", firstFailMsg, func() error {
		return o.writeHealthKey(masterAddr, healthValue)
	})

	// Read test on master (only if write succeeded).
	if checks["write_test"] {
		firstFailMsg = runCheck(checks, "read_test", firstFailMsg, func() error {
			return o.readHealthKey(masterAddr, healthValue)
		})
	} else {
		checks["read_test"] = false
	}

	// Replica read test (only multi-replica and write succeeded).
	if o.cfg.Replicas > 1 && checks["write_test"] {
		firstFailMsg = runCheck(checks, "replica_read_test", firstFailMsg, func() error {
			return o.checkReplicaRead(healthValue)
		})
	}

	return firstFailMsg
}

// runSentinelChecks runs sentinel reachability, quorum, and flag checks.
// Returns the first failure message (empty if all passed).
func (o *Observer) runSentinelChecks(checks map[string]bool) string {
	var firstFailMsg string

	firstFailMsg = runCheck(checks, "sentinel_reachable", firstFailMsg, func() error {
		return o.checkSentinelReachable()
	})

	quorumOK, flagsOK, sentErr := o.checkSentinelQuorumAndFlags()
	checks["sentinel_quorum"] = quorumOK
	checks["sentinel_flags"] = flagsOK
	if (!quorumOK || !flagsOK) && firstFailMsg == "" && sentErr != nil {
		firstFailMsg = fmt.Sprintf("sentinel check failed: %v", sentErr)
	}

	return firstFailMsg
}

// runCheck executes a single check, records the result, and returns the updated first failure message.
func runCheck(checks map[string]bool, name, firstFailMsg string, fn func() error) string {
	if err := fn(); err != nil {
		checks[name] = false
		if firstFailMsg == "" {
			return fmt.Sprintf("%s failed: %v", name, err)
		}
		return firstFailMsg
	}
	checks[name] = true
	return firstFailMsg
}

// setResult updates the observer's current result (thread-safe).
func (o *Observer) setResult(ready bool, checks map[string]bool, message string, checkTime time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.result = CheckResult{
		Ready:     ready,
		Checks:    checks,
		Message:   message,
		LastCheck: checkTime,
	}
	o.metrics.updateGauges(checks, ready)
}

func buildTLSConfig(cfg Config) (*tls.Config, error) {
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	if cfg.TLSCACert != "" {
		caCert, err := os.ReadFile(cfg.TLSCACert)
		if err != nil {
			return nil, fmt.Errorf("reading CA cert: %w", err)
		}
		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		tlsCfg.RootCAs = certPool
	}

	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			return nil, fmt.Errorf("loading client certificate: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return tlsCfg, nil
}
