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

func main() {
	// Dispatch to sidecar mode if first argument is "sidecar".
	if len(os.Args) > 1 && os.Args[1] == "sidecar" {
		// Remove "sidecar" from os.Args so the sidecar's flag.Parse sees its own flags.
		os.Args = append(os.Args[:1], os.Args[2:]...)
		sidecar.Run()
		return
	}

	// Dispatch to migrate mode if first argument is "migrate".
	// Used by the Helm pre-upgrade hook Job to apply field defaults to existing Valkey CRs.
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		migrate.Run()
		return
	}

	// Dispatch to observer mode if first argument is "observer".
	if len(os.Args) > 1 && os.Args[1] == "observer" {
		os.Args = append(os.Args[:1], os.Args[2:]...)
		observercmd.Run()
		return
	}

	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var operatorImage string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&operatorImage, "operator-image", os.Getenv("OPERATOR_IMAGE"),
		"The operator container image, used for the sidecar. Can also be set via OPERATOR_IMAGE env var.")

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

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "valkey-operator.vko.gtrfc.com",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	operatorNamespace := os.Getenv("POD_NAMESPACE")

	if err = (&controller.ValkeyReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		Recorder:          mgr.GetEventRecorder("valkey-operator"),
		OperatorImage:     operatorImage,
		OperatorNamespace: operatorNamespace,
		OperatorVersion:   version,
	}).SetupWithManager(mgr); err != nil {
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
