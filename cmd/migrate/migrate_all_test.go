package migrate

import (
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// testNamespace is the namespace every CR in this file lives in; the migration
// is cluster-wide, so the namespace itself carries no behaviour.
const testNamespace = "ns"

// legacyValkey is a CR as it looks before the migration: replicas unset and an
// auth block without the password key.
func legacyValkey(name string) *vkov1.Valkey {
	return &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: name},
		Spec: vkov1.ValkeySpec{
			Image: "valkey/valkey:8.0",
			Auth:  &vkov1.AuthSpec{SecretName: "auth"},
		},
	}
}

// currentValkey is a CR that already carries every default the migration applies.
func currentValkey(name string) *vkov1.Valkey {
	return &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: name},
		Spec: vkov1.ValkeySpec{
			Image:    "valkey/valkey:8.0",
			Replicas: 3,
			Auth:     &vkov1.AuthSpec{SecretName: "auth", SecretPasswordKey: "password"},
		},
	}
}

func newFakeClient(objs ...client.Object) client.WithWatch {
	return fake.NewClientBuilder().WithScheme(migrateScheme).WithObjects(objs...).Build()
}

// readBack fetches the stored CR so a test asserts on what the API server holds,
// not on the in-memory copy migrateAll mutated.
func readBack(t *testing.T, c client.Client, name string) *vkov1.Valkey {
	t.Helper()
	var got vkov1.Valkey
	key := types.NamespacedName{Namespace: testNamespace, Name: name}
	if err := c.Get(context.Background(), key, &got); err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	return &got
}

func TestMigrateAll_EmptyList(t *testing.T) {
	c := newFakeClient()

	migrated, failed := migrateAll(context.Background(), c, nil)

	if migrated != 0 || failed != 0 {
		t.Errorf("migrateAll(empty) = (%d, %d), want (0, 0)", migrated, failed)
	}
}

func TestMigrateAll_PatchesTheDefaultsIntoTheStoredObject(t *testing.T) {
	vk := legacyValkey("legacy")
	c := newFakeClient(vk)

	migrated, failed := migrateAll(context.Background(), c, []vkov1.Valkey{*vk})

	if migrated != 1 || failed != 0 {
		t.Fatalf("migrateAll() = (%d, %d), want (1, 0)", migrated, failed)
	}

	got := readBack(t, c, "legacy")
	if got.Spec.Replicas != 1 {
		t.Errorf("stored Replicas = %d, want 1", got.Spec.Replicas)
	}
	if got.Spec.Auth.SecretPasswordKey != defaultPasswordKey {
		t.Errorf("stored SecretPasswordKey = %q, want %q", got.Spec.Auth.SecretPasswordKey, defaultPasswordKey)
	}
	// The patch is a merge patch built from the pre-migration copy, so it must not
	// clobber fields the migration does not touch.
	if got.Spec.Image != "valkey/valkey:8.0" {
		t.Errorf("stored Image = %q, want it untouched", got.Spec.Image)
	}
	if got.Spec.Auth.SecretName != "auth" {
		t.Errorf("stored SecretName = %q, want it untouched", got.Spec.Auth.SecretName)
	}
}

func TestMigrateAll_UpToDateCRIsNotPatched(t *testing.T) {
	vk := currentValkey("current")
	c := newFakeClient(vk)
	before := readBack(t, c, "current")

	migrated, failed := migrateAll(context.Background(), c, []vkov1.Valkey{*vk})

	if migrated != 0 || failed != 0 {
		t.Fatalf("migrateAll() = (%d, %d), want (0, 0) for an up-to-date CR", migrated, failed)
	}

	after := readBack(t, c, "current")
	// No patch call at all means the stored resourceVersion cannot have moved.
	if after.ResourceVersion != before.ResourceVersion {
		t.Errorf("resourceVersion moved from %s to %s: the CR was written even though nothing changed",
			before.ResourceVersion, after.ResourceVersion)
	}
}

// TestMigrateAll_OneFailingPatchDoesNotStopTheRest is the contract that makes the
// Helm pre-upgrade hook safe to run on a cluster with many CRs: a single CR that
// cannot be patched must be counted, not abort the loop.
func TestMigrateAll_OneFailingPatchDoesNotStopTheRest(t *testing.T) {
	first := legacyValkey("a-broken")
	second := legacyValkey("b-healthy")

	base := newFakeClient(first, second)
	c := interceptor.NewClient(base, interceptor.Funcs{
		Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object,
			patch client.Patch, opts ...client.PatchOption) error {
			if obj.GetName() == "a-broken" {
				return errors.New("admission webhook denied the request")
			}
			return cl.Patch(ctx, obj, patch, opts...)
		},
	})

	migrated, failed := migrateAll(context.Background(), c, []vkov1.Valkey{*first, *second})

	if migrated != 1 {
		t.Errorf("migrated = %d, want 1: the healthy CR after the failing one must still be patched", migrated)
	}
	if failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}

	if got := readBack(t, c, "b-healthy"); got.Spec.Replicas != 1 {
		t.Errorf("b-healthy Replicas = %d, want 1: it was skipped because a-broken failed", got.Spec.Replicas)
	}
	if got := readBack(t, c, "a-broken"); got.Spec.Replicas != 0 {
		t.Errorf("a-broken Replicas = %d, want 0: the failing patch must not have been applied", got.Spec.Replicas)
	}
}

// TestMigrateAll_CountsAddUpToTheSkippedFigure pins the arithmetic Run reports:
// skipped = total - migrated - failed. A miscount there would make the hook Job
// log a wrong summary while still exiting 0.
func TestMigrateAll_CountsAddUpToTheSkippedFigure(t *testing.T) {
	toMigrate := legacyValkey("a-legacy")
	upToDate := currentValkey("b-current")
	broken := legacyValkey("c-broken")

	base := newFakeClient(toMigrate, upToDate, broken)
	c := interceptor.NewClient(base, interceptor.Funcs{
		Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object,
			patch client.Patch, opts ...client.PatchOption) error {
			if obj.GetName() == "c-broken" {
				return errors.New("conflict")
			}
			return cl.Patch(ctx, obj, patch, opts...)
		},
	})

	items := []vkov1.Valkey{*toMigrate, *upToDate, *broken}
	migrated, failed := migrateAll(context.Background(), c, items)

	if migrated != 1 {
		t.Errorf("migrated = %d, want 1", migrated)
	}
	if failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}
	if skipped := len(items) - migrated - failed; skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
	// failed > 0 is what makes Run exit non-zero, which is what makes the Helm
	// pre-upgrade hook block the release.
	if failed == 0 {
		t.Error("failed must stay > 0 so the hook Job fails the upgrade")
	}
}

func TestMigrateAll_MutatesOnlyTheItemsItPatches(t *testing.T) {
	legacy := legacyValkey("legacy")
	current := currentValkey("current")
	c := newFakeClient(legacy, current)

	items := []vkov1.Valkey{*legacy, *current}
	migrateAll(context.Background(), c, items)

	// migrateAll writes the defaults into the caller's slice as well; that is
	// harmless for Run but pinning it prevents a surprise if someone reuses the
	// slice afterwards.
	if items[0].Spec.Replicas != 1 {
		t.Errorf("items[0].Spec.Replicas = %d, want the applied default 1", items[0].Spec.Replicas)
	}
	if items[1].Spec.Replicas != 3 {
		t.Errorf("items[1].Spec.Replicas = %d, want the untouched 3", items[1].Spec.Replicas)
	}
}
