// Package migrate implements the "migrate" CLI subcommand for the Valkey operator.
// It is intended to run as a Helm pre-upgrade hook Job and ensures that all
// existing Valkey CRs carry the field defaults required by the current operator version.
package migrate

import (
	"context"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

var (
	migrateScheme = runtime.NewScheme()
	migrateLog    = ctrl.Log.WithName("migrate")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(migrateScheme))
	utilruntime.Must(vkov1.AddToScheme(migrateScheme))
}

// Run executes the migrate subcommand.
// It lists all Valkey CRs cluster-wide and patches any that are missing
// field defaults introduced in the current operator version.
// Exits with a non-zero status code on any failure so the hook Job fails fast.
func Run() {
	ctrl.SetLogger(zap.New())

	c, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: migrateScheme})
	if err != nil {
		migrateLog.Error(err, "failed to create kubernetes client")
		os.Exit(1)
	}

	ctx := context.Background()

	var list vkov1.ValkeyList
	if err := c.List(ctx, &list); err != nil {
		migrateLog.Error(err, "failed to list Valkey CRs")
		os.Exit(1)
	}

	migrateLog.Info("starting Valkey CR migration", "total", len(list.Items))

	failed := 0
	migrated := 0

	for i := range list.Items {
		vk := &list.Items[i]
		original := vk.DeepCopy()

		if !applyDefaults(vk) {
			migrateLog.Info("no changes needed", "name", vk.Name, "namespace", vk.Namespace)
			continue
		}

		if err := c.Patch(ctx, vk, client.MergeFrom(original)); err != nil {
			migrateLog.Error(err, "failed to patch Valkey CR",
				"name", vk.Name, "namespace", vk.Namespace)
			failed++
			continue
		}

		migrateLog.Info("migrated Valkey CR", "name", vk.Name, "namespace", vk.Namespace)
		migrated++
	}

	migrateLog.Info("migration complete", "migrated", migrated, "failed", failed, "skipped", len(list.Items)-migrated-failed)

	if failed > 0 {
		migrateLog.Error(fmt.Errorf("%d patch(es) failed", failed), "migration finished with errors")
		os.Exit(1)
	}
}

// applyDefaults sets any missing field defaults on the Valkey CR spec.
// Returns true if any field was changed.
// This is intentionally versionless: it always reconciles to the target state,
// so no version-to-version migration chain is needed.
func applyDefaults(vk *vkov1.Valkey) bool {
	changed := false

	// spec.replicas defaults to 1
	if vk.Spec.Replicas == 0 {
		vk.Spec.Replicas = 1
		changed = true
	}

	// spec.auth.secretPasswordKey defaults to "password"
	if vk.Spec.Auth != nil && vk.Spec.Auth.SecretPasswordKey == "" {
		vk.Spec.Auth.SecretPasswordKey = "password"
		changed = true
	}

	// spec.tls.certManager.issuer.group defaults to "cert-manager.io"
	if vk.Spec.TLS != nil && vk.Spec.TLS.CertManager != nil {
		if vk.Spec.TLS.CertManager.Issuer.Group == "" {
			vk.Spec.TLS.CertManager.Issuer.Group = "cert-manager.io"
			changed = true
		}
	}

	// spec.sentinel.replicas defaults to 3
	if vk.Spec.Sentinel != nil && vk.Spec.Sentinel.Replicas == 0 {
		vk.Spec.Sentinel.Replicas = 3
		changed = true
	}

	// spec.persistence defaults
	if vk.Spec.Persistence != nil {
		if vk.Spec.Persistence.Mode == "" {
			vk.Spec.Persistence.Mode = vkov1.PersistenceModeRDB
			changed = true
		}
		if vk.Spec.Persistence.Size.IsZero() {
			vk.Spec.Persistence.Size = resource.MustParse("1Gi")
			changed = true
		}
	}

	return changed
}
