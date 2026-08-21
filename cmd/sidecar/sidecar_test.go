package sidecar

import (
	"flag"
	"io"
	"os"
	"testing"
	"time"

	"github.com/guided-traffic/valkey-operator/internal/sidecar"
)

// The truthy spellings envBool/envBoolTrue accept. Held as constants because
// goconst counts literals across the package and reports the total against the
// non-test source that also contains them.
const (
	envTrue = "true"
	envYes  = "yes"
)

// sidecarEnvKeys lists every environment variable parseFlags consults. Tests
// blank them all out first so a developer shell that happens to export one of
// them cannot change the outcome.
var sidecarEnvKeys = []string{
	"SIDECAR_POLL_INTERVAL", "SIDECAR_VALKEY_ADDR", "SIDECAR_HEALTH_ADDR",
	"POD_NAME", "POD_NAMESPACE", "TLS_ENABLED", "TLS_CA_CERT", "TLS_CERT", "TLS_KEY",
	"SENTINEL_ENABLED", "SENTINEL_MONITOR", "SENTINEL_ADDRS", "SENTINEL_DISABLE_AUTH",
	"HEADLESS_SVC", "REPLICAS", "SIDECAR_FAILOVER_TIMEOUT", "VALKEY_PASSWORD",
}

// clearEnv blanks every variable parseFlags reads. All of the env helpers treat
// an empty value exactly like an unset one, so t.Setenv("") is a faithful
// stand-in for "not present in the container environment".
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range sidecarEnvKeys {
		t.Setenv(k, "")
	}
}

// parseWith runs parseFlags against a private flag set and argv, so the same
// test binary can exercise it many times over. parseFlags registers on the
// global flag.CommandLine, which would panic on a second registration.
func parseWith(t *testing.T, args ...string) sidecar.Config {
	t.Helper()

	oldArgs, oldFlags := os.Args, flag.CommandLine
	t.Cleanup(func() {
		os.Args, flag.CommandLine = oldArgs, oldFlags
	})

	fs := flag.NewFlagSet("sidecar", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flag.CommandLine = fs
	os.Args = append([]string{"sidecar"}, args...)

	return parseFlags()
}

func TestParseFlags_DefaultsWithoutEnvOrArgs(t *testing.T) {
	clearEnv(t)

	cfg := parseWith(t)

	if cfg.PodName != "" || cfg.PodNamespace != "" {
		t.Errorf("pod-name/pod-namespace must stay empty so Run rejects the config, got %q/%q",
			cfg.PodName, cfg.PodNamespace)
	}
	if cfg.PollInterval != time.Second {
		t.Errorf("PollInterval = %v, want 1s", cfg.PollInterval)
	}
	// The plaintext port is the default. With TLS on, the operator has to pass
	// --valkey-addr=localhost:16379 explicitly (buildSidecarContainer does).
	if cfg.ValkeyAddr != "localhost:6379" {
		t.Errorf("ValkeyAddr = %q, want localhost:6379", cfg.ValkeyAddr)
	}
	if cfg.HealthAddr != ":8082" {
		t.Errorf("HealthAddr = %q, want :8082 (the port the pod readiness probe hits)", cfg.HealthAddr)
	}
	if cfg.FailoverTimeout != 60*time.Second {
		t.Errorf("FailoverTimeout = %v, want 60s", cfg.FailoverTimeout)
	}
	if cfg.Replicas != 1 {
		t.Errorf("Replicas = %d, want 1", cfg.Replicas)
	}
	if cfg.TLSEnabled || cfg.SentinelEnabled || cfg.SentinelDisableAuth {
		t.Errorf("all security-relevant toggles must default to off, got %+v", cfg)
	}
	if cfg.TLSCACert != "" || cfg.TLSCert != "" || cfg.TLSKey != "" {
		t.Errorf("TLS paths must default to empty, got %q/%q/%q", cfg.TLSCACert, cfg.TLSCert, cfg.TLSKey)
	}
	if cfg.Password != "" {
		t.Errorf("Password = %q, want empty", cfg.Password)
	}
}

func TestParseFlags_EnvSuppliesEveryValue(t *testing.T) {
	clearEnv(t)
	// Deliberately mixed booleans: an all-true case would not catch two flags
	// writing the same struct field.
	t.Setenv("SIDECAR_POLL_INTERVAL", "3s")
	t.Setenv("SIDECAR_VALKEY_ADDR", "localhost:16379")
	t.Setenv("SIDECAR_HEALTH_ADDR", "127.0.0.1:9082")
	t.Setenv("POD_NAME", "my-cluster-0")
	t.Setenv("POD_NAMESPACE", "valkey-ns")
	t.Setenv("TLS_ENABLED", envTrue)
	t.Setenv("TLS_CA_CERT", "/tls/ca.crt")
	t.Setenv("TLS_CERT", "/tls/tls.crt")
	t.Setenv("TLS_KEY", "/tls/tls.key")
	t.Setenv("SENTINEL_ENABLED", "yes")
	t.Setenv("SENTINEL_MONITOR", "mymaster")
	t.Setenv("SENTINEL_ADDRS", "s-0:26379,s-1:26379")
	t.Setenv("SENTINEL_DISABLE_AUTH", "false")
	t.Setenv("HEADLESS_SVC", "my-cluster-headless.valkey-ns.svc.cluster.local")
	t.Setenv("REPLICAS", "3")
	t.Setenv("SIDECAR_FAILOVER_TIMEOUT", "90s")
	t.Setenv("VALKEY_PASSWORD", "s3cr3t")

	cfg := parseWith(t)

	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"PollInterval", cfg.PollInterval, 3 * time.Second},
		{"ValkeyAddr", cfg.ValkeyAddr, "localhost:16379"},
		{"HealthAddr", cfg.HealthAddr, "127.0.0.1:9082"},
		{"PodName", cfg.PodName, "my-cluster-0"},
		{"PodNamespace", cfg.PodNamespace, "valkey-ns"},
		{"TLSEnabled", cfg.TLSEnabled, true},
		{"TLSCACert", cfg.TLSCACert, "/tls/ca.crt"},
		{"TLSCert", cfg.TLSCert, "/tls/tls.crt"},
		{"TLSKey", cfg.TLSKey, "/tls/tls.key"},
		{"SentinelEnabled", cfg.SentinelEnabled, true},
		{"SentinelMonitor", cfg.SentinelMonitor, "mymaster"},
		{"SentinelAddrs", cfg.SentinelAddrs, "s-0:26379,s-1:26379"},
		{"SentinelDisableAuth", cfg.SentinelDisableAuth, false},
		{"HeadlessSvc", cfg.HeadlessSvc, "my-cluster-headless.valkey-ns.svc.cluster.local"},
		{"Replicas", cfg.Replicas, 3},
		{"FailoverTimeout", cfg.FailoverTimeout, 90 * time.Second},
		{"Password", cfg.Password, "s3cr3t"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
}

// TestParseFlags_CommandLineWinsOverEnv matters because the operator passes the
// sidecar its configuration as CLI flags (buildSidecarContainer) while the pod
// also carries env vars such as POD_NAME; the explicit argument has to win.
func TestParseFlags_CommandLineWinsOverEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("POD_NAME", "env-pod")
	t.Setenv("POD_NAMESPACE", "env-ns")
	t.Setenv("SIDECAR_VALKEY_ADDR", "localhost:6379")
	t.Setenv("TLS_ENABLED", "false")
	t.Setenv("REPLICAS", "1")
	t.Setenv("SIDECAR_FAILOVER_TIMEOUT", "10s")

	cfg := parseWith(t,
		"--pod-name=flag-pod-0",
		"--pod-namespace=flag-ns",
		"--valkey-addr=localhost:16379",
		"--tls-enabled=true",
		"--replicas=3",
		"--failover-timeout=45s",
	)

	if cfg.PodName != "flag-pod-0" {
		t.Errorf("PodName = %q, want flag-pod-0", cfg.PodName)
	}
	if cfg.PodNamespace != "flag-ns" {
		t.Errorf("PodNamespace = %q, want flag-ns", cfg.PodNamespace)
	}
	if cfg.ValkeyAddr != "localhost:16379" {
		t.Errorf("ValkeyAddr = %q, want localhost:16379", cfg.ValkeyAddr)
	}
	if !cfg.TLSEnabled {
		t.Error("TLSEnabled = false, want the explicit --tls-enabled=true to win over TLS_ENABLED=false")
	}
	if cfg.Replicas != 3 {
		t.Errorf("Replicas = %d, want 3", cfg.Replicas)
	}
	if cfg.FailoverTimeout != 45*time.Second {
		t.Errorf("FailoverTimeout = %v, want 45s", cfg.FailoverTimeout)
	}
}

// TestParseFlags_MalformedEnvSilentlyFallsBackToDefault documents that a typo in
// an integer or duration variable is not reported anywhere: the sidecar starts
// with the default value instead of failing loudly. For REPLICAS that is the
// dangerous one - the drain handler uses it to find failover targets, and a
// silent 1 means "no peers".
func TestParseFlags_MalformedEnvSilentlyFallsBackToDefault(t *testing.T) {
	clearEnv(t)
	t.Setenv("SIDECAR_POLL_INTERVAL", "1") // no unit, not a valid duration
	t.Setenv("SIDECAR_FAILOVER_TIMEOUT", "one minute")
	t.Setenv("REPLICAS", "3x")

	cfg := parseWith(t)

	if cfg.PollInterval != time.Second {
		t.Errorf("PollInterval = %v, want the 1s default after a malformed value", cfg.PollInterval)
	}
	if cfg.FailoverTimeout != 60*time.Second {
		t.Errorf("FailoverTimeout = %v, want the 60s default after a malformed value", cfg.FailoverTimeout)
	}
	if cfg.Replicas != 1 {
		t.Errorf("Replicas = %d, want the default 1 after a malformed value", cfg.Replicas)
	}
}

// TestParseFlags_SentinelAddrsStaysRaw pins that the sidecar keeps the
// comma-separated string as given - unlike the observer, it does not split here.
func TestParseFlags_SentinelAddrsStaysRaw(t *testing.T) {
	clearEnv(t)

	cfg := parseWith(t, "--sentinel-addrs=a:26379,b:26379,c:26379")

	if cfg.SentinelAddrs != "a:26379,b:26379,c:26379" {
		t.Errorf("SentinelAddrs = %q, want the raw comma-separated string", cfg.SentinelAddrs)
	}
}

func TestMissingRequiredFlags(t *testing.T) {
	const want = "error: --pod-name and --pod-namespace (or POD_NAME / POD_NAMESPACE env vars) are required"

	tests := []struct {
		name    string
		cfg     sidecar.Config
		wantMsg string
	}{
		{"both missing", sidecar.Config{}, want},
		{"pod name missing", sidecar.Config{PodNamespace: "ns"}, want},
		{"pod namespace missing", sidecar.Config{PodName: "v-0"}, want},
		{"both present", sidecar.Config{PodName: "v-0", PodNamespace: "ns"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := missingRequiredFlags(tt.cfg); got != tt.wantMsg {
				t.Errorf("missingRequiredFlags() = %q, want %q", got, tt.wantMsg)
			}
		})
	}
}

func TestEnvString(t *testing.T) {
	tests := []struct {
		name  string
		value string
		def   string
		want  string
	}{
		{"set", "from-env", "fallback", "from-env"},
		{"empty falls back", "", "fallback", "fallback"},
		{"empty with empty default", "", "", ""},
		{"whitespace counts as set", " ", "fallback", " "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("VKO_TEST_STRING", tt.value)
			if got := envString("VKO_TEST_STRING", tt.def); got != tt.want {
				t.Errorf("envString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEnvBool(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{envTrue, true},
		{"1", true},
		{envYes, true},
		{"false", false},
		{"0", false},
		{"no", false},
		{"", false},
		// Case sensitivity is intentional to record here: the flag package parses
		// "TRUE" via strconv.ParseBool and accepts it, this helper does not. So
		// TLS_ENABLED=TRUE leaves the sidecar talking plaintext.
		{"TRUE", false},
		{"True", false},
		{"Yes", false},
	}

	for _, tt := range tests {
		t.Run("value="+tt.value, func(t *testing.T) {
			t.Setenv("VKO_TEST_BOOL", tt.value)
			if got := envBool("VKO_TEST_BOOL"); got != tt.want {
				t.Errorf("envBool(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestEnvInt(t *testing.T) {
	tests := []struct {
		name  string
		value string
		def   int
		want  int
	}{
		{"valid", "42", 1, 42},
		{"zero is honoured", "0", 1, 0},
		{"negative", "-3", 1, -3},
		{"empty falls back", "", 1, 1},
		{"not a number falls back", "three", 1, 1},
		{"float falls back", "1.5", 1, 1},
		{"trailing space falls back", "3 ", 1, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("VKO_TEST_INT", tt.value)
			if got := envInt("VKO_TEST_INT", tt.def); got != tt.want {
				t.Errorf("envInt(%q, %d) = %d, want %d", tt.value, tt.def, got, tt.want)
			}
		})
	}
}

func TestEnvDuration(t *testing.T) {
	tests := []struct {
		name  string
		value string
		def   time.Duration
		want  time.Duration
	}{
		{"seconds", "5s", time.Second, 5 * time.Second},
		{"milliseconds", "250ms", time.Second, 250 * time.Millisecond},
		{"compound", "1m30s", time.Second, 90 * time.Second},
		{"zero is honoured", "0s", time.Second, 0},
		{"empty falls back", "", time.Second, time.Second},
		{"missing unit falls back", "5", time.Second, time.Second},
		{"garbage falls back", "soon", time.Second, time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("VKO_TEST_DURATION", tt.value)
			if got := envDuration("VKO_TEST_DURATION", tt.def); got != tt.want {
				t.Errorf("envDuration(%q, %v) = %v, want %v", tt.value, tt.def, got, tt.want)
			}
		})
	}
}
