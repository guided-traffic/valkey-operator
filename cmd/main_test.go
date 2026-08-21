package main

import (
	"flag"
	"io"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// newTestFlagSet returns a FlagSet that reports parse errors instead of calling
// os.Exit, so a test can drive bindOperatorFlags without killing the test binary.
func newTestFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("valkey-operator", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// TestSchemeRegistersEveryTypeTheOperatorTouches guards the package init: the
// manager cache resolves these GVKs, and a missing AddToScheme surfaces only at
// runtime as "no kind is registered".
func TestSchemeRegistersEveryTypeTheOperatorTouches(t *testing.T) {
	tests := []struct {
		name string
		gvk  schema.GroupVersionKind
	}{
		{"valkey CRD", vkov1.GroupVersion.WithKind("Valkey")},
		{"valkey CRD list", vkov1.GroupVersion.WithKind("ValkeyList")},
		{"core pod", corev1.SchemeGroupVersion.WithKind("Pod")},
		{"core service", corev1.SchemeGroupVersion.WithKind("Service")},
		{"core secret", corev1.SchemeGroupVersion.WithKind("Secret")},
		{"core configmap", corev1.SchemeGroupVersion.WithKind("ConfigMap")},
		{"apps statefulset", appsv1.SchemeGroupVersion.WithKind("StatefulSet")},
		{"policy poddisruptionbudget", policyv1.SchemeGroupVersion.WithKind("PodDisruptionBudget")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !scheme.Recognizes(tt.gvk) {
				t.Errorf("scheme does not recognize %s", tt.gvk.String())
			}
		})
	}
}

func TestHasSubcommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want map[string]bool
	}{
		{
			name: "no arguments at all",
			args: []string{"valkey-operator"},
			want: map[string]bool{"sidecar": false, "migrate": false, "observer": false},
		},
		{
			name: "empty argv does not panic",
			args: nil,
			want: map[string]bool{"sidecar": false, "migrate": false, "observer": false},
		},
		{
			name: "bare subcommand",
			args: []string{"valkey-operator", "sidecar"},
			want: map[string]bool{"sidecar": true, "migrate": false, "observer": false},
		},
		{
			name: "subcommand followed by its own flags",
			args: []string{"valkey-operator", "observer", "--namespace=ns", "--cluster-name=c"},
			want: map[string]bool{"sidecar": false, "migrate": false, "observer": true},
		},
		{
			name: "subcommand must be first, a leading flag hides it",
			args: []string{"valkey-operator", "--leader-elect", "migrate"},
			want: map[string]bool{"sidecar": false, "migrate": false, "observer": false},
		},
		{
			name: "prefix of a subcommand does not match",
			args: []string{"valkey-operator", "side"},
			want: map[string]bool{"sidecar": false, "migrate": false, "observer": false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for name, want := range tt.want {
				if got := hasSubcommand(tt.args, name); got != want {
					t.Errorf("hasSubcommand(%v, %q) = %v, want %v", tt.args, name, got, want)
				}
			}
		})
	}
}

func TestStripSubcommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "subcommand with flags: only the subcommand is removed",
			args: []string{"valkey-operator", "sidecar", "--pod-name=v-0", "--tls-enabled=true"},
			want: []string{"valkey-operator", "--pod-name=v-0", "--tls-enabled=true"},
		},
		{
			name: "bare subcommand leaves just the program name",
			args: []string{"valkey-operator", "observer"},
			want: []string{"valkey-operator"},
		},
		{
			name: "a second occurrence of the name survives",
			args: []string{"valkey-operator", "sidecar", "sidecar"},
			want: []string{"valkey-operator", "sidecar"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripSubcommand(tt.args)
			if len(got) != len(tt.want) {
				t.Fatalf("stripSubcommand(...) = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("arg[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestBindOperatorFlags_Defaults(t *testing.T) {
	t.Setenv("OPERATOR_IMAGE", "")

	fs := newTestFlagSet()
	f := bindOperatorFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse: %v", err)
	}

	if f.metricsAddr != ":8080" {
		t.Errorf("metricsAddr = %q, want :8080", f.metricsAddr)
	}
	if f.probeAddr != ":8081" {
		t.Errorf("probeAddr = %q, want :8081", f.probeAddr)
	}
	if f.enableLeaderElection {
		t.Error("leader election must default to off so a single-replica deploy needs no lease RBAC")
	}
	if f.operatorImage != "" {
		t.Errorf("operatorImage = %q, want empty when OPERATOR_IMAGE is unset", f.operatorImage)
	}
}

func TestBindOperatorFlags_OperatorImageFromEnv(t *testing.T) {
	t.Setenv("OPERATOR_IMAGE", "guidedtraffic/valkey-operator:v1.2.3")

	fs := newTestFlagSet()
	f := bindOperatorFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse: %v", err)
	}

	if f.operatorImage != "guidedtraffic/valkey-operator:v1.2.3" {
		t.Errorf("operatorImage = %q, want the OPERATOR_IMAGE value", f.operatorImage)
	}
}

func TestBindOperatorFlags_FlagWinsOverEnv(t *testing.T) {
	t.Setenv("OPERATOR_IMAGE", "from-env:1")

	fs := newTestFlagSet()
	f := bindOperatorFlags(fs)
	if err := fs.Parse([]string{"--operator-image=from-flag:2"}); err != nil {
		t.Fatalf("parse: %v", err)
	}

	if f.operatorImage != "from-flag:2" {
		t.Errorf("operatorImage = %q, want the command line value to win", f.operatorImage)
	}
}

func TestBindOperatorFlags_AllFlagsParsed(t *testing.T) {
	t.Setenv("OPERATOR_IMAGE", "")

	fs := newTestFlagSet()
	f := bindOperatorFlags(fs)
	args := []string{
		"--metrics-bind-address=:9090",
		"--health-probe-bind-address=127.0.0.1:9091",
		"--leader-elect",
		"--operator-image=repo/img:tag",
	}
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse: %v", err)
	}

	if f.metricsAddr != ":9090" {
		t.Errorf("metricsAddr = %q, want :9090", f.metricsAddr)
	}
	if f.probeAddr != "127.0.0.1:9091" {
		t.Errorf("probeAddr = %q, want 127.0.0.1:9091", f.probeAddr)
	}
	if !f.enableLeaderElection {
		t.Error("enableLeaderElection = false, want true for --leader-elect")
	}
	if f.operatorImage != "repo/img:tag" {
		t.Errorf("operatorImage = %q, want repo/img:tag", f.operatorImage)
	}
}

// TestBindOperatorFlags_UnknownFlagIsRejected pins that the flag set is closed:
// a typo such as --leader-election must not be silently accepted. In production
// flag.CommandLine uses ExitOnError, so this surfaces as exit code 2.
func TestBindOperatorFlags_UnknownFlagIsRejected(t *testing.T) {
	fs := newTestFlagSet()
	bindOperatorFlags(fs)

	if err := fs.Parse([]string{"--leader-election"}); err == nil {
		t.Fatal("expected an error for an unknown flag, got nil")
	}
}

func TestManagerOptions(t *testing.T) {
	f := &operatorFlags{probeAddr: ":8081", enableLeaderElection: true}
	opts := managerOptions(f)

	if opts.Scheme != scheme {
		t.Error("manager must use the package scheme, otherwise the cache cannot decode Valkey CRs")
	}
	if opts.HealthProbeBindAddress != ":8081" {
		t.Errorf("HealthProbeBindAddress = %q, want :8081", opts.HealthProbeBindAddress)
	}
	if !opts.LeaderElection {
		t.Error("LeaderElection = false, want the flag value to be honoured")
	}
	// The lease name is part of the deployment contract: changing it lets an old
	// and a new operator pod both believe they are leader during a rollout.
	if opts.LeaderElectionID != "valkey-operator.vko.gtrfc.com" {
		t.Errorf("LeaderElectionID = %q, want valkey-operator.vko.gtrfc.com", opts.LeaderElectionID)
	}
}

func TestManagerOptions_LeaderElectionOff(t *testing.T) {
	opts := managerOptions(&operatorFlags{probeAddr: "0", enableLeaderElection: false})

	if opts.LeaderElection {
		t.Error("LeaderElection = true, want false")
	}
	if opts.HealthProbeBindAddress != "0" {
		t.Errorf("HealthProbeBindAddress = %q, want the literal 0 that disables the probe server",
			opts.HealthProbeBindAddress)
	}
}

// TestManagerOptions_MetricsBindAddress pins that --metrics-bind-address reaches
// ctrl.Options.Metrics. Before this wiring the parsed value went nowhere and
// controller-runtime silently fell back to its ":8080" default, which happened to
// equal both the flag default and the chart argument — so a non-default value,
// including the documented "0" that disables the metrics server, was ignored.
func TestManagerOptions_MetricsBindAddress(t *testing.T) {
	f := &operatorFlags{metricsAddr: ":9090", probeAddr: ":8081"}
	opts := managerOptions(f)

	if opts.Metrics.BindAddress != ":9090" {
		t.Errorf("Metrics.BindAddress = %q, want the flag value :9090", opts.Metrics.BindAddress)
	}
}

func TestManagerOptions_MetricsDisabledByZero(t *testing.T) {
	opts := managerOptions(&operatorFlags{metricsAddr: "0", probeAddr: ":8081"})

	if opts.Metrics.BindAddress != "0" {
		t.Errorf("Metrics.BindAddress = %q, want the literal 0 that disables the metrics server",
			opts.Metrics.BindAddress)
	}
}

func TestNewReconciler(t *testing.T) {
	mgr, err := ctrl.NewManager(&rest.Config{Host: "http://127.0.0.1:1"}, ctrl.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	f := &operatorFlags{operatorImage: "guidedtraffic/valkey-operator:v9"}
	r := newReconciler(mgr, f, "valkey-system")

	if r.Client == nil {
		t.Error("Client is nil, the reconciler cannot read or write objects")
	}
	if r.Scheme != scheme {
		t.Error("Scheme must be the manager scheme, otherwise SetOwnerReference fails")
	}
	if r.Recorder == nil {
		t.Error("Recorder is nil, event emission would panic")
	}
	// The sidecar image and the namespace are distinct inputs; swapping them
	// would produce pods with an image name as their namespace.
	if r.OperatorImage != "guidedtraffic/valkey-operator:v9" {
		t.Errorf("OperatorImage = %q, want the --operator-image value", r.OperatorImage)
	}
	if r.OperatorNamespace != "valkey-system" {
		t.Errorf("OperatorNamespace = %q, want valkey-system", r.OperatorNamespace)
	}
	if r.OperatorVersion != version {
		t.Errorf("OperatorVersion = %q, want the ldflags build version %q", r.OperatorVersion, version)
	}
}
