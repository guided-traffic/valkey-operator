// Package main is the entry point for the Valkey operator.
package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/cmd/migrate"
	observercmd "github.com/guided-traffic/valkey-operator/cmd/observer"
	"github.com/guided-traffic/valkey-operator/cmd/sidecar"
	"github.com/guided-traffic/valkey-operator/internal/controller"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")

	// Build information, set via ldflags
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(vkov1.AddToScheme(scheme))
}

// hasSubcommand reports whether args carries name as its first argument.
// The subcommand must come first: flags placed before it are not inspected.
func hasSubcommand(args []string, name string) bool {
	return len(args) > 1 && args[1] == name
}

// stripSubcommand removes the subcommand from args so that the subcommand's own
// flag.Parse sees its flags where it expects them.
func stripSubcommand(args []string) []string {
	return append(args[:1], args[2:]...)
}

// operatorFlags holds the command line options of the operator mode.
type operatorFlags struct {
	metricsAddr             string
	probeAddr               string
	enableLeaderElection    bool
	operatorImage           string
	maxConcurrentReconciles int
}

// bindOperatorFlags declares the operator flags on fs and returns the struct
// they write into once fs.Parse has run.
func bindOperatorFlags(fs *flag.FlagSet) *operatorFlags {
	f := &operatorFlags{}

	fs.StringVar(&f.metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	fs.StringVar(&f.probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	fs.BoolVar(&f.enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	fs.StringVar(&f.operatorImage, "operator-image", os.Getenv("OPERATOR_IMAGE"),
		"The operator container image, used for the sidecar. Can also be set via OPERATOR_IMAGE env var.")
	fs.IntVar(&f.maxConcurrentReconciles, "max-concurrent-reconciles", controller.DefaultMaxConcurrentReconciles,
		"How many Valkey resources are reconciled at the same time. One worker couples every "+
			"cluster to the slowest of them, because a pass dials its pods with a 5 s timeout each. "+
			"Passes for the same resource stay serialised at any value.")

	return f
}

// managerOptions builds the controller-runtime manager options from the parsed flags.
func managerOptions(f *operatorFlags) ctrl.Options {
	return ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: f.metricsAddr},
		HealthProbeBindAddress: f.probeAddr,
		LeaderElection:         f.enableLeaderElection,
		LeaderElectionID:       "valkey-operator.vko.gtrfc.com",
	}
}

// newReconciler builds the Valkey reconciler from the manager and the parsed flags.
func newReconciler(mgr ctrl.Manager, f *operatorFlags, operatorNamespace string) *controller.ValkeyReconciler {
	return &controller.ValkeyReconciler{
		Client:                  mgr.GetClient(),
		Scheme:                  mgr.GetScheme(),
		Recorder:                mgr.GetEventRecorder("valkey-operator"),
		OperatorImage:           f.operatorImage,
		OperatorNamespace:       operatorNamespace,
		OperatorVersion:         version,
		MaxConcurrentReconciles: f.maxConcurrentReconciles,
	}
}

func main() {
	// Dispatch to sidecar mode if first argument is "sidecar".
	if hasSubcommand(os.Args, "sidecar") {
		// Remove "sidecar" from os.Args so the sidecar's flag.Parse sees its own flags.
		os.Args = stripSubcommand(os.Args)
		sidecar.Run()
		return
	}

	// Dispatch to migrate mode if first argument is "migrate".
	// Used by the Helm pre-upgrade hook Job to apply field defaults to existing Valkey CRs.
	if hasSubcommand(os.Args, "migrate") {
		migrate.Run()
		return
	}

	// Dispatch to observer mode if first argument is "observer".
	if hasSubcommand(os.Args, "observer") {
		os.Args = stripSubcommand(os.Args)
		observercmd.Run()
		return
	}

	flags := bindOperatorFlags(flag.CommandLine)

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	setupLog.Info("starting valkey-operator",
		"version", version,
		"commit", commit,
		"buildTime", buildTime,
	)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), managerOptions(flags))
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	operatorNamespace := os.Getenv("POD_NAMESPACE")

	if err = newReconciler(mgr, flags, operatorNamespace).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Valkey")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
