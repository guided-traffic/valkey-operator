//go:build integration

package integration

import (
	"context"
	"os"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/controller"
	"github.com/guided-traffic/valkey-operator/internal/health"
)

// Package-level shared test infrastructure.
// A single envtest control plane and controller manager is started once and
// shared across all integration tests in this package. This avoids the
// "controller with name valkey already exists" error that occurs when multiple
// tests each try to register their own controller in the same process.
var (
	testCtx    context.Context
	testCancel context.CancelFunc
	k8sClient  client.Client
	testEnv    *envtest.Environment

	// slowProbes is the InstanceChecker of the shared reconciler. It delegates to
	// the real Checker unless a test arms it, so only the CR of
	// TestReconcileConcurrency_StuckClusterDoesNotBlockOthers is ever delayed.
	slowProbes *slowProbeChecker
)

// TestMain sets up a shared envtest environment, registers schemes, starts
// the controller manager, and then runs all tests.
func TestMain(m *testing.M) {
	log.SetLogger(zap.New(zap.UseDevMode(true)))

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{"../../config/crd/bases"},
	}

	cfg, err := testEnv.Start()
	if err != nil {
		panic("failed to start envtest: " + err.Error())
	}

	// Register schemes.
	if err := vkov1.AddToScheme(scheme.Scheme); err != nil {
		panic("failed to register vkov1 scheme: " + err.Error())
	}
	if err := appsv1.AddToScheme(scheme.Scheme); err != nil {
		panic("failed to register appsv1 scheme: " + err.Error())
	}
	if err := rbacv1.AddToScheme(scheme.Scheme); err != nil {
		panic("failed to register rbacv1 scheme: " + err.Error())
	}

	// Create manager and register the Valkey controller once.
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	if err != nil {
		panic("failed to create manager: " + err.Error())
	}

	slowProbes = &slowProbeChecker{delegate: health.NewChecker(mgr.GetClient())}

	reconciler := &controller.ValkeyReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		OperatorImage:   "valkey-operator:test",
		InstanceChecker: slowProbes,
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		panic("failed to setup controller: " + err.Error())
	}

	testCtx, testCancel = context.WithCancel(context.Background())

	go func() {
		if err := mgr.Start(testCtx); err != nil {
			panic("manager exited with error: " + err.Error())
		}
	}()

	if !mgr.GetCache().WaitForCacheSync(testCtx) {
		panic("cache did not sync")
	}

	k8sClient = mgr.GetClient()

	// Run all tests.
	code := m.Run()

	// Teardown.
	testCancel()
	if err := testEnv.Stop(); err != nil {
		panic("failed to stop envtest: " + err.Error())
	}

	os.Exit(code)
}
