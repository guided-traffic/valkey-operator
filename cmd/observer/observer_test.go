package observer

import (
	"flag"
	"io"
	"os"
	"testing"
	"time"

	"github.com/guided-traffic/valkey-operator/internal/observer"
)

// The truthy spellings envBool/envBoolTrue accept. Held as constants because
// goconst counts literals across the package and reports the total against the
// non-test source that also contains them.
const (
	envTrue = "true"
	envYes  = "yes"
)

// observerEnvKeys lists every environment variable parseFlags consults.
// Tests blank them all out first so a developer shell that happens to export
// one of them cannot change the outcome.
var observerEnvKeys = []string{
	"POD_NAMESPACE", "CLUSTER_NAME", "OBSERVER_HEALTH_ADDR", "OBSERVER_POLL_INTERVAL",
	"VALKEY_HEADLESS_SVC", "REPLICAS", "TLS_ENABLED", "TLS_CA_CERT", "TLS_CERT", "TLS_KEY",
	"VALKEY_MTLS", "SENTINEL_MTLS", "SENTINEL_ENABLED", "SENTINEL_ADDRS", "SENTINEL_MONITOR",
	"SENTINEL_DISABLE_AUTH", "OBSERVER_DB", "LOG_LEVEL", "VALKEY_PASSWORD",
	"UNREADY_WHEN_MASTER_UNREACHABLE", "UNREADY_WHEN_WRITE_TEST_FAILURE",
	"UNREADY_WHEN_READ_TEST_FAILURE", "UNREADY_WHEN_REPLICA_SYNC_FAILURE",
	"UNREADY_WHEN_REPLICA_READ_TEST_FAILURE", "UNREADY_WHEN_SENTINEL_UNREACHABLE",
	"UNREADY_WHEN_SENTINEL_QUORUM_FAILURE", "UNREADY_WHEN_SENTINEL_MASTER_DOWN",
	"UNREADY_WHEN_SENTINEL_MASTER_HOSTNAME_INVALID",
	"UNREADY_WHEN_SENTINEL_REPLICA_HOSTNAMES_INVALID",
}

// clearEnv blanks every variable parseFlags reads. All of the env helpers treat
// an empty value exactly like an unset one, so t.Setenv("") is a faithful
// stand-in for "not present in the container environment".
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range observerEnvKeys {
		t.Setenv(k, "")
	}
}

// parseWith runs parseFlags against a private flag set and argv, so the same
// test binary can exercise it many times over. parseFlags registers on the
// global flag.CommandLine, which would panic on a second registration.
func parseWith(t *testing.T, args ...string) observer.Config {
	t.Helper()

	oldArgs, oldFlags := os.Args, flag.CommandLine
	t.Cleanup(func() {
		os.Args, flag.CommandLine = oldArgs, oldFlags
	})

	fs := flag.NewFlagSet("observer", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	flag.CommandLine = fs
	os.Args = append([]string{"observer"}, args...)

	return parseFlags()
}

func TestParseFlags_DefaultsWithoutEnvOrArgs(t *testing.T) {
	clearEnv(t)

	cfg := parseWith(t)

	if cfg.Namespace != "" || cfg.ClusterName != "" {
		t.Errorf("namespace/cluster-name must stay empty so Run rejects the config, got %q/%q",
			cfg.Namespace, cfg.ClusterName)
	}
	if cfg.HealthAddr != ":8084" {
		t.Errorf("HealthAddr = %q, want :8084 (the port the observer Deployment probes)", cfg.HealthAddr)
	}
	if cfg.PollInterval != 2*time.Second {
		t.Errorf("PollInterval = %v, want 2s", cfg.PollInterval)
	}
	if cfg.Replicas != 1 {
		t.Errorf("Replicas = %d, want 1", cfg.Replicas)
	}
	if cfg.ObserverDB != 15 {
		t.Errorf("ObserverDB = %d, want 15", cfg.ObserverDB)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", cfg.LogLevel)
	}
	if cfg.TLSEnabled || cfg.ValkeyMTLS || cfg.SentinelMTLS || cfg.SentinelEnabled || cfg.SentinelDisableAuth {
		t.Errorf("all security-relevant toggles must default to off, got %+v", cfg)
	}
	if cfg.SentinelAddrList != nil {
		t.Errorf("SentinelAddrList = %v, want nil when no addresses are configured", cfg.SentinelAddrList)
	}
	if cfg.Password != "" {
		t.Errorf("Password = %q, want empty", cfg.Password)
	}
	assertAllUnreadyWhen(t, cfg.UnreadyWhen, true, "")
}

func TestParseFlags_EnvSuppliesEveryValue(t *testing.T) {
	clearEnv(t)
	// Deliberately mixed booleans: an all-true case would not catch two flags
	// writing the same struct field.
	t.Setenv("POD_NAMESPACE", "valkey-ns")
	t.Setenv("CLUSTER_NAME", "my-cluster")
	t.Setenv("OBSERVER_HEALTH_ADDR", "127.0.0.1:9999")
	t.Setenv("OBSERVER_POLL_INTERVAL", "750ms")
	t.Setenv("VALKEY_HEADLESS_SVC", "my-cluster-headless.valkey-ns.svc.cluster.local")
	t.Setenv("REPLICAS", "5")
	t.Setenv("TLS_ENABLED", envTrue)
	t.Setenv("TLS_CA_CERT", "/tls/ca.crt")
	t.Setenv("TLS_CERT", "/tls/tls.crt")
	t.Setenv("TLS_KEY", "/tls/tls.key")
	t.Setenv("VALKEY_MTLS", "yes")
	t.Setenv("SENTINEL_MTLS", "false")
	t.Setenv("SENTINEL_ENABLED", "1")
	t.Setenv("SENTINEL_ADDRS", "s-0:26379,s-1:26379,s-2:26379")
	t.Setenv("SENTINEL_MONITOR", "mymaster")
	t.Setenv("SENTINEL_DISABLE_AUTH", "false")
	t.Setenv("OBSERVER_DB", "7")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("VALKEY_PASSWORD", "s3cr3t")

	cfg := parseWith(t)

	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"Namespace", cfg.Namespace, "valkey-ns"},
		{"ClusterName", cfg.ClusterName, "my-cluster"},
		{"HealthAddr", cfg.HealthAddr, "127.0.0.1:9999"},
		{"PollInterval", cfg.PollInterval, 750 * time.Millisecond},
		{"ValkeyHeadlessSvc", cfg.ValkeyHeadlessSvc, "my-cluster-headless.valkey-ns.svc.cluster.local"},
		{"Replicas", cfg.Replicas, 5},
		{"TLSEnabled", cfg.TLSEnabled, true},
		{"TLSCACert", cfg.TLSCACert, "/tls/ca.crt"},
		{"TLSCert", cfg.TLSCert, "/tls/tls.crt"},
		{"TLSKey", cfg.TLSKey, "/tls/tls.key"},
		{"ValkeyMTLS", cfg.ValkeyMTLS, true},
		{"SentinelMTLS", cfg.SentinelMTLS, false},
		{"SentinelEnabled", cfg.SentinelEnabled, true},
		{"SentinelMonitor", cfg.SentinelMonitor, "mymaster"},
		{"SentinelDisableAuth", cfg.SentinelDisableAuth, false},
		{"ObserverDB", cfg.ObserverDB, 7},
		{"LogLevel", cfg.LogLevel, "debug"},
		{"Password", cfg.Password, "s3cr3t"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}

	want := []string{"s-0:26379", "s-1:26379", "s-2:26379"}
	if len(cfg.SentinelAddrList) != len(want) {
		t.Fatalf("SentinelAddrList = %v, want %v", cfg.SentinelAddrList, want)
	}
	for i := range want {
		if cfg.SentinelAddrList[i] != want[i] {
			t.Errorf("SentinelAddrList[%d] = %q, want %q", i, cfg.SentinelAddrList[i], want[i])
		}
	}
}

// TestParseFlags_CommandLineWinsOverEnv matters because the operator always
// passes CLI flags (buildObserverArgs) while the pod may also inherit env vars
// from the cluster; the explicit argument has to win.
func TestParseFlags_CommandLineWinsOverEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("POD_NAMESPACE", "env-ns")
	t.Setenv("CLUSTER_NAME", "env-cluster")
	t.Setenv("REPLICAS", "9")
	t.Setenv("TLS_ENABLED", envTrue)
	t.Setenv("OBSERVER_POLL_INTERVAL", "1m")

	cfg := parseWith(t,
		"--namespace=flag-ns",
		"--cluster-name=flag-cluster",
		"--replicas=3",
		"--tls-enabled=false",
		"--poll-interval=250ms",
	)

	if cfg.Namespace != "flag-ns" {
		t.Errorf("Namespace = %q, want flag-ns", cfg.Namespace)
	}
	if cfg.ClusterName != "flag-cluster" {
		t.Errorf("ClusterName = %q, want flag-cluster", cfg.ClusterName)
	}
	if cfg.Replicas != 3 {
		t.Errorf("Replicas = %d, want 3", cfg.Replicas)
	}
	if cfg.TLSEnabled {
		t.Error("TLSEnabled = true, want the explicit --tls-enabled=false to win over TLS_ENABLED=true")
	}
	if cfg.PollInterval != 250*time.Millisecond {
		t.Errorf("PollInterval = %v, want 250ms", cfg.PollInterval)
	}
}

// TestParseFlags_MalformedEnvSilentlyFallsBackToDefault documents that a typo in
// an integer or duration variable is not reported anywhere: the observer starts
// with the default value instead of failing loudly.
func TestParseFlags_MalformedEnvSilentlyFallsBackToDefault(t *testing.T) {
	clearEnv(t)
	t.Setenv("OBSERVER_POLL_INTERVAL", "2") // no unit, not a valid duration
	t.Setenv("REPLICAS", "three")
	t.Setenv("OBSERVER_DB", "15.5")

	cfg := parseWith(t)

	if cfg.PollInterval != 2*time.Second {
		t.Errorf("PollInterval = %v, want the 2s default after a malformed value", cfg.PollInterval)
	}
	if cfg.Replicas != 1 {
		t.Errorf("Replicas = %d, want the default 1 after a malformed value", cfg.Replicas)
	}
	if cfg.ObserverDB != 15 {
		t.Errorf("ObserverDB = %d, want the default 15 after a malformed value", cfg.ObserverDB)
	}
}

func TestParseFlags_SentinelAddrListSplitting(t *testing.T) {
	tests := []struct {
		name  string
		addrs string
		want  []string
	}{
		{"empty stays nil", "", nil},
		{"single address", "s-0:26379", []string{"s-0:26379"}},
		{"three addresses", "a:26379,b:26379,c:26379", []string{"a:26379", "b:26379", "c:26379"}},
		{
			// Documents the sharp edge: a stray comma produces an empty entry
			// that is later dialled as an address rather than being dropped.
			name:  "trailing comma yields an empty entry",
			addrs: "a:26379,",
			want:  []string{"a:26379", ""},
		},
		{"whitespace is not trimmed", "a:26379, b:26379", []string{"a:26379", " b:26379"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t)
			cfg := parseWith(t, "--sentinel-addrs="+tt.addrs)

			if cfg.SentinelAddrs != tt.addrs {
				t.Errorf("SentinelAddrs = %q, want %q", cfg.SentinelAddrs, tt.addrs)
			}
			if len(cfg.SentinelAddrList) != len(tt.want) {
				t.Fatalf("SentinelAddrList = %#v, want %#v", cfg.SentinelAddrList, tt.want)
			}
			for i := range tt.want {
				if cfg.SentinelAddrList[i] != tt.want[i] {
					t.Errorf("SentinelAddrList[%d] = %q, want %q", i, cfg.SentinelAddrList[i], tt.want[i])
				}
			}
		})
	}
}

// unreadyWhenFields maps each unReady toggle to its flag name, env variable and
// accessor. Ten near-identical registrations are exactly where a copy-paste slip
// makes two flags write the same field.
var unreadyWhenFields = []struct {
	name string
	flag string
	env  string
	get  func(observer.UnreadyWhenConfig) bool
}{
	{"MasterUnreachable", "unready-when-master-unreachable", "UNREADY_WHEN_MASTER_UNREACHABLE",
		func(u observer.UnreadyWhenConfig) bool { return u.MasterUnreachable }},
	{"WriteTestFailure", "unready-when-write-test-failure", "UNREADY_WHEN_WRITE_TEST_FAILURE",
		func(u observer.UnreadyWhenConfig) bool { return u.WriteTestFailure }},
	{"ReadTestFailure", "unready-when-read-test-failure", "UNREADY_WHEN_READ_TEST_FAILURE",
		func(u observer.UnreadyWhenConfig) bool { return u.ReadTestFailure }},
	{"ReplicaSyncFailure", "unready-when-replica-sync-failure", "UNREADY_WHEN_REPLICA_SYNC_FAILURE",
		func(u observer.UnreadyWhenConfig) bool { return u.ReplicaSyncFailure }},
	{"ReplicaReadTestFailure", "unready-when-replica-read-test-failure", "UNREADY_WHEN_REPLICA_READ_TEST_FAILURE",
		func(u observer.UnreadyWhenConfig) bool { return u.ReplicaReadTestFailure }},
	{"SentinelUnreachable", "unready-when-sentinel-unreachable", "UNREADY_WHEN_SENTINEL_UNREACHABLE",
		func(u observer.UnreadyWhenConfig) bool { return u.SentinelUnreachable }},
	{"SentinelQuorumFailure", "unready-when-sentinel-quorum-failure", "UNREADY_WHEN_SENTINEL_QUORUM_FAILURE",
		func(u observer.UnreadyWhenConfig) bool { return u.SentinelQuorumFailure }},
	{"SentinelMasterDown", "unready-when-sentinel-master-down", "UNREADY_WHEN_SENTINEL_MASTER_DOWN",
		func(u observer.UnreadyWhenConfig) bool { return u.SentinelMasterDown }},
	{"SentinelMasterHostnameInvalid", "unready-when-sentinel-master-hostname-invalid",
		"UNREADY_WHEN_SENTINEL_MASTER_HOSTNAME_INVALID",
		func(u observer.UnreadyWhenConfig) bool { return u.SentinelMasterHostnameInvalid }},
	{"SentinelReplicaHostnamesInvalid", "unready-when-sentinel-replica-hostnames-invalid",
		"UNREADY_WHEN_SENTINEL_REPLICA_HOSTNAMES_INVALID",
		func(u observer.UnreadyWhenConfig) bool { return u.SentinelReplicaHostnamesInvalid }},
}

// assertAllUnreadyWhen checks every toggle is want, except the one named by
// exceptField which must hold the opposite value.
func assertAllUnreadyWhen(t *testing.T, u observer.UnreadyWhenConfig, want bool, exceptField string) {
	t.Helper()
	for _, f := range unreadyWhenFields {
		expect := want
		if f.name == exceptField {
			expect = !want
		}
		if got := f.get(u); got != expect {
			t.Errorf("UnreadyWhen.%s = %v, want %v", f.name, got, expect)
		}
	}
}

// TestParseFlags_UnreadyWhenFlagsAreIndependent turns each toggle off on its own
// and proves the other nine stay on, once via the flag and once via the env var.
func TestParseFlags_UnreadyWhenFlagsAreIndependent(t *testing.T) {
	for _, f := range unreadyWhenFields {
		t.Run(f.name+"/flag", func(t *testing.T) {
			clearEnv(t)
			cfg := parseWith(t, "--"+f.flag+"=false")
			assertAllUnreadyWhen(t, cfg.UnreadyWhen, true, f.name)
		})
		t.Run(f.name+"/env", func(t *testing.T) {
			clearEnv(t)
			t.Setenv(f.env, "false")
			cfg := parseWith(t)
			assertAllUnreadyWhen(t, cfg.UnreadyWhen, true, f.name)
		})
	}
}

func TestMissingRequiredFlags(t *testing.T) {
	const want = "error: --namespace and --cluster-name (or POD_NAMESPACE / CLUSTER_NAME env vars) are required"

	tests := []struct {
		name    string
		cfg     observer.Config
		wantMsg string
	}{
		{"both missing", observer.Config{}, want},
		{"namespace missing", observer.Config{ClusterName: "c"}, want},
		{"cluster name missing", observer.Config{Namespace: "ns"}, want},
		{"both present", observer.Config{Namespace: "ns", ClusterName: "c"}, ""},
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
		// "TRUE" via strconv.ParseBool and accepts it, this helper does not.
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

func TestEnvBoolTrue(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"", true},
		{envTrue, true},
		{"1", true},
		{envYes, true},
		{"false", false},
		{"0", false},
		{"no", false},
		// Same case sensitivity, and here it inverts the intent: an operator who
		// writes TRUE to keep a check enabled disables it instead.
		{"TRUE", false},
		{"anything-else", false},
	}

	for _, tt := range tests {
		t.Run("value="+tt.value, func(t *testing.T) {
			t.Setenv("VKO_TEST_BOOL_TRUE", tt.value)
			if got := envBoolTrue("VKO_TEST_BOOL_TRUE"); got != tt.want {
				t.Errorf("envBoolTrue(%q) = %v, want %v", tt.value, got, tt.want)
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
		{"zero is honoured", "0", 15, 0},
		{"negative", "-3", 1, -3},
		{"empty falls back", "", 15, 15},
		{"not a number falls back", "twelve", 15, 15},
		{"float falls back", "1.5", 15, 15},
		{"trailing space falls back", "3 ", 15, 15},
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
		{"zero is honoured", "0s", 2 * time.Second, 0},
		{"empty falls back", "", 2 * time.Second, 2 * time.Second},
		{"missing unit falls back", "5", 2 * time.Second, 2 * time.Second},
		{"garbage falls back", "soon", 2 * time.Second, 2 * time.Second},
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
