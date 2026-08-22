package v1

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestGroupVersion(t *testing.T) {
	require.Equal(t, "vko.gtrfc.com", GroupVersion.Group)
	require.Equal(t, "v1", GroupVersion.Version)
	require.Equal(t, "vko.gtrfc.com/v1", GroupVersion.String())
}

// TestAddToScheme_RegistersValkeyKinds asserts what the controller actually
// depends on: after AddToScheme the scheme can construct both kinds by GVK and
// can recognise a typed object. A registration that silently misses ValkeyList
// breaks every List call at runtime, not at compile time.
func TestAddToScheme_RegistersValkeyKinds(t *testing.T) {
	scheme := runtime.NewScheme()

	require.NoError(t, AddToScheme(scheme))

	valkey, err := scheme.New(GroupVersion.WithKind("Valkey"))
	require.NoError(t, err)
	require.IsType(t, &Valkey{}, valkey)

	list, err := scheme.New(GroupVersion.WithKind("ValkeyList"))
	require.NoError(t, err)
	require.IsType(t, &ValkeyList{}, list)

	gvks, unversioned, err := scheme.ObjectKinds(&Valkey{})
	require.NoError(t, err)
	require.False(t, unversioned)
	require.Contains(t, gvks, GroupVersion.WithKind("Valkey"))
}

// TestAddToScheme_RegistersMetaTypes covers the metav1.AddToGroupVersion call:
// without it, a client cannot decode a ListOptions or a Status for this group.
func TestAddToScheme_RegistersMetaTypes(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, AddToScheme(scheme))

	opts, err := scheme.New(GroupVersion.WithKind("ListOptions"))
	require.NoError(t, err)
	require.IsType(t, &metav1.ListOptions{}, opts)
}

func TestAddToScheme_IsIdempotent(t *testing.T) {
	scheme := runtime.NewScheme()

	require.NoError(t, AddToScheme(scheme))
	require.NoError(t, AddToScheme(scheme))

	gvks, _, err := scheme.ObjectKinds(&Valkey{})
	require.NoError(t, err)
	require.Len(t, gvks, 1, "re-registering must not duplicate the GVK")
}
