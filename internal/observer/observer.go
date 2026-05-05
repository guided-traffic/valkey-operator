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

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap/zapcore"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	// checkMasterReachable is the check identifier for master reachability.
	checkMasterReachable = "master_reachable"
	// roleMaster is the Valkey replication role for the master instance.
	roleMaster = "master"
	// observerHealthKey is the key used by the observer to write its health probe value.
	observerHealthKey = "__vko_observer_health"
	// selectCommand is the SELECT command name used by the Valkey RESP protocol.
	selectCommand = "SELECT"
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

	// LogLevel sets the verbosity of observer log output.
	// Supported: debug, info, warn, error. Default: info.
	// At debug level, stack traces are included for all errors.
	// At info, warn, and error levels, stack traces are suppressed.
	LogLevel string

	// ValkeyMTLS controls whether the observer sends a client certificate to Valkey pods.
	// Default: true (mTLS enabled).
	ValkeyMTLS bool
	// SentinelMTLS controls whether the observer sends a client certificate to Sentinel pods.
	// Default: false (server-only TLS verification).
	SentinelMTLS bool

	// Sentinel settings.
	SentinelEnabled     bool
	SentinelAddrs       string
	SentinelAddrList    []string
	SentinelMonitor     string
	SentinelDisableAuth bool

	// Observer DB for health key.
	ObserverDB int

	// UnreadyWhen holds per-check unReady behaviour. True = failure causes unReady.
	UnreadyWhen UnreadyWhenConfig
}

// UnreadyWhenConfig holds the effective per-check unReady flags.
// All fields default to true when not explicitly set.
type UnreadyWhenConfig struct {
	MasterUnreachable               bool
	WriteTestFailure                bool
	ReadTestFailure                 bool
	ReplicaSyncFailure              bool
	ReplicaReadTestFailure          bool
	SentinelUnreachable             bool
	SentinelQuorumFailure           bool
	SentinelMasterDown              bool
	SentinelMasterHostnameInvalid   bool
	SentinelReplicaHostnamesInvalid bool
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
	cfg               Config
	tlsConfig         *tls.Config
	sentinelTLSConfig *tls.Config
	mu                sync.RWMutex
	result            CheckResult
	metrics           *observerMetrics
}

// New creates a new Observer with the given configuration.
func New(cfg Config) (*Observer, error) {
	var tlsCfg, sentinelTLSCfg *tls.Config
	if cfg.TLSEnabled {
		var err error
		tlsCfg, err = buildTLSConfig(cfg, cfg.ValkeyMTLS)
		if err != nil {
			return nil, fmt.Errorf("building TLS config: %w", err)
		}
		if cfg.SentinelEnabled {
			sentinelTLSCfg, err = buildTLSConfig(cfg, cfg.SentinelMTLS)
			if err != nil {
				return nil, fmt.Errorf("building sentinel TLS config: %w", err)
			}
		}
	}

	return &Observer{
		cfg:               cfg,
		tlsConfig:         tlsCfg,
		sentinelTLSConfig: sentinelTLSCfg,
		result: CheckResult{
			Ready:  false,
			Checks: make(map[string]bool),
		},
		metrics: newObserverMetrics(),
	}, nil
}

// buildObserverLogger creates a logr.Logger configured for the given log level.
// At debug level, stack traces are included for all errors (dev mode).
// At info, warn, and error levels, stack traces are suppressed so that
// expected check failures do not pollute logs with call stacks.
func buildObserverLogger(logLevel string) {
	switch logLevel {
	case "debug":
		ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	case "warn":
		lvl := zapcore.WarnLevel
		ctrl.SetLogger(zap.New(zap.Level(&lvl), zap.StacktraceLevel(zapcore.DPanicLevel)))
	case "error":
		lvl := zapcore.ErrorLevel
		ctrl.SetLogger(zap.New(zap.Level(&lvl), zap.StacktraceLevel(zapcore.DPanicLevel)))
	default: // info
		lvl := zapcore.InfoLevel
		ctrl.SetLogger(zap.New(zap.Level(&lvl), zap.StacktraceLevel(zapcore.DPanicLevel)))
	}
}

// Run starts the observer polling loop and health server.
// It blocks until the context is cancelled.
func Run(ctx context.Context, cfg Config) error {
	buildObserverLogger(cfg.LogLevel)
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
		logger.Error(err, "check failed", "check", "masterUnreachable")
		o.setResult(false, map[string]bool{checkMasterReachable: false},
			fmt.Sprintf("[masterUnreachable] master discovery failed: %v", err), start)
		o.metrics.recordCycle(time.Since(start), false)
		return
	}

	// 2-6. Core checks.
	healthValue := fmt.Sprintf("%d", time.Now().UnixNano())
	firstFailMsg = o.runCoreChecks(ctx, logger, masterAddr, healthValue, checks)

	// 7-8. Sentinel checks.
	if o.cfg.SentinelEnabled {
		if msg := o.runSentinelChecks(logger, checks); msg != "" && firstFailMsg == "" {
			firstFailMsg = msg
		}
	}

	allOK := firstFailMsg == ""
	if !allOK {
		logger.Error(fmt.Errorf("%s", firstFailMsg), "poll cycle failed")
	}
	o.setResult(allOK, checks, firstFailMsg, start)
	o.metrics.recordCycle(time.Since(start), allOK)
}

// runCoreChecks runs PING, replica sync, write, read, and replica read checks.
// Returns the first failure message (empty if all passed).
func (o *Observer) runCoreChecks(_ context.Context, logger logr.Logger, masterAddr, healthValue string, checks map[string]bool) string {
	var firstFailMsg string
	uw := o.cfg.UnreadyWhen

	// PING master.
	firstFailMsg = runCheck(logger, checks, checkMasterReachable, "masterUnreachable",
		firstFailMsg, uw.MasterUnreachable, func() error {
			return o.pingHost(masterAddr)
		})

	// Replica sync (only multi-replica).
	if o.cfg.Replicas > 1 {
		firstFailMsg = runCheck(logger, checks, "replica_sync", "replicaSyncFailure",
			firstFailMsg, uw.ReplicaSyncFailure, func() error {
				return o.checkReplicaSync(masterAddr)
			})
	}

	// Write test on master.
	firstFailMsg = runCheck(logger, checks, "write_test", "writeTestFailure",
		firstFailMsg, uw.WriteTestFailure, func() error {
			return o.writeHealthKey(masterAddr, healthValue)
		})

	// Read test on master (only if write succeeded).
	if checks["write_test"] {
		firstFailMsg = runCheck(logger, checks, "read_test", "readTestFailure",
			firstFailMsg, uw.ReadTestFailure, func() error {
				return o.readHealthKey(masterAddr, healthValue)
			})
	} else {
		checks["read_test"] = false
	}

	// Replica read test (only multi-replica and write succeeded).
	if o.cfg.Replicas > 1 && checks["write_test"] {
		firstFailMsg = runCheck(logger, checks, "replica_read_test", "replicaReadTestFailure",
			firstFailMsg, uw.ReplicaReadTestFailure, func() error {
				return o.checkReplicaRead(healthValue)
			})
	}

	return firstFailMsg
}

// runSentinelChecks runs sentinel reachability, quorum, flag, and hostname checks.
// Returns the first failure message (empty if all passed).
func (o *Observer) runSentinelChecks(logger logr.Logger, checks map[string]bool) string {
	var firstFailMsg string
	uw := o.cfg.UnreadyWhen

	firstFailMsg = runCheck(logger, checks, "sentinel_reachable", "sentinelUnreachable",
		firstFailMsg, uw.SentinelUnreachable, func() error {
			return o.checkSentinelReachable()
		})

	quorumOK, flagsOK, sentErr := o.checkSentinelQuorumAndFlags()
	checks["sentinel_quorum"] = quorumOK
	checks["sentinel_flags"] = flagsOK
	if !quorumOK && uw.SentinelQuorumFailure && firstFailMsg == "" && sentErr != nil {
		firstFailMsg = fmt.Sprintf("[sentinelQuorumFailure] %v", sentErr)
	}
	if !flagsOK && uw.SentinelMasterDown && firstFailMsg == "" && sentErr != nil {
		firstFailMsg = fmt.Sprintf("[sentinelMasterDown] %v", sentErr)
	}
	if !quorumOK {
		logger.Error(sentErr, "check failed", "check", "sentinelQuorumFailure")
	}
	if !flagsOK {
		logger.Error(sentErr, "check failed", "check", "sentinelMasterDown")
	}

	firstFailMsg = runCheck(logger, checks, "sentinel_master_hostname", "sentinelMasterHostnameInvalid",
		firstFailMsg, uw.SentinelMasterHostnameInvalid, func() error {
			return o.checkSentinelMasterHostname()
		})

	firstFailMsg = runCheck(logger, checks, "sentinel_replica_hostnames", "sentinelReplicaHostnamesInvalid",
		firstFailMsg, uw.SentinelReplicaHostnamesInvalid, func() error {
			return o.checkSentinelReplicaHostnames()
		})

	return firstFailMsg
}

// runCheck executes a single check and records the result.
// label is the canonical unreadyWhen field name (e.g. "masterUnreachable").
// causesUnready controls whether a failure updates firstFailMsg.
// Failures are always logged regardless of causesUnready.
func runCheck(
	logger logr.Logger,
	checks map[string]bool,
	name string,
	label string,
	firstFailMsg string,
	causesUnready bool,
	fn func() error,
) string {
	err := fn()
	if err != nil {
		checks[name] = false
		logger.Error(err, "check failed", "check", label)
		if causesUnready && firstFailMsg == "" {
			return fmt.Sprintf("[%s] %v", label, err)
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

// buildTLSConfig builds a TLS config for the observer.
// When withClientCert is true, the client certificate and key are loaded
// for mutual TLS (mTLS). When false, only the CA certificate is loaded
// for server-only verification.
func buildTLSConfig(cfg Config, withClientCert bool) (*tls.Config, error) {
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

	if withClientCert && cfg.TLSCert != "" && cfg.TLSKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			return nil, fmt.Errorf("loading client certificate: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return tlsCfg, nil
}
