//go:build integration

package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
	"github.com/guided-traffic/valkey-operator/internal/builder"
)

// What only a real API server decides here: whether the hand-declared token
// projection is a valid pod spec at all.
//
// The unit tier proves the operator builds the right shape; it cannot prove the
// shape is accepted. A `projected` volume with a `serviceAccountToken` source is
// validated by the API server -- an unsupported field, a bad path or a mount
// colliding with a reserved one is rejected there and nowhere else, and the
// symptom in a cluster would be a StatefulSet that never produces a pod.
//
// envtest runs no kubelet, so nothing here proves the sidecar can *use* the
// token. That is the e2e tier's job, and the whole existing e2e suite covers it
// implicitly: every assertion on the instanceRole label is an assertion that the
// sidecar authenticated and patched its own pod (ADR 0012 D8 step 4).

const tokenProjectionTimeout = 60 * time.Second

func TestSidecarTokenProjection_IsAcceptedByTheAPIServer_Integration(t *testing.T) {
	ctx := testCtx
	crName := "token-projection-test"

	v := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: "default"},
		Spec: vkov1.ValkeySpec{
			Replicas: 3,
			Image:    "valkey/valkey:8.0",
			Sentinel: &vkov1.SentinelSpec{Enabled: true, Replicas: 3},
			Metrics:  &vkov1.MetricsSpec{Enabled: true},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, v))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, v) })

	sts := waitForStatefulSet(t, crName, tokenProjectionTimeout)
	spec := sts.Spec.Template.Spec

	// The round-trip is the assertion: the object came back from etcd, so the API
	// server validated and defaulted it.
	require.NotNil(t, spec.AutomountServiceAccountToken)
	assert.False(t, *spec.AutomountServiceAccountToken)

	var projection *corev1.Volume
	for i := range spec.Volumes {
		if spec.Volumes[i].Name == builder.SidecarTokenVolumeName {
			projection = &spec.Volumes[i]
		}
	}
	require.NotNil(t, projection, "the token projection must survive the write")
	require.NotNil(t, projection.Projected)
	require.Len(t, projection.Projected.Sources, 2)
	require.NotNil(t, projection.Projected.Sources[0].ServiceAccountToken,
		"the API server accepts a serviceAccountToken source; a rejected one would leave the volume out")
	assert.Equal(t, "token", projection.Projected.Sources[0].ServiceAccountToken.Path)

	var mounters []string
	for _, c := range append(append([]corev1.Container{}, spec.InitContainers...), spec.Containers...) {
		for _, m := range c.VolumeMounts {
			if m.MountPath == builder.ServiceAccountTokenMountPath {
				mounters = append(mounters, c.Name)
			}
		}
	}
	assert.Equal(t, []string{builder.SidecarContainerName}, mounters,
		"as persisted, exactly one container can reach the Kubernetes API")

	sentinel := waitForStatefulSet(t, crName+"-sentinel", tokenProjectionTimeout)
	require.NotNil(t, sentinel.Spec.Template.Spec.AutomountServiceAccountToken)
	assert.False(t, *sentinel.Spec.Template.Spec.AutomountServiceAccountToken,
		"Sentinel pods never call the API and must mount no token")
}
