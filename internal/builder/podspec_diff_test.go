package builder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// StatefulSetHasChanged decides whether the operator rewrites the live
// StatefulSet, which in turn decides whether the failover-aware rolling update
// replaces every data pod. A field the comparison forgets is drift that never
// converges: the operator writes the desired object once, the API server keeps the
// old one, and no later pass notices. A field it over-reports is a rolling restart
// of the whole cluster on every reconcile.
//
// The table below therefore drives one realistic drift per row against a
// StatefulSet the operator itself built, and every row states which of the two
// failures it guards.

// diffFixtureValkey is the CR that produces the richest StatefulSet the builder
// emits: TLS (a Secret volume plus per-container mounts), auth (env sourced from a
// Secret key), the metrics exporter (a third container with its own ports) and
// persistence, all of which the comparison has to look at.
func diffFixtureValkey() *vkov1.Valkey {
	return newTestValkey("test", func(v *vkov1.Valkey) {
		v.Spec.Replicas = 3
		v.Spec.Auth = &vkov1.AuthSpec{SecretName: "valkey-auth", SecretPasswordKey: "password"}
		v.Spec.TLS = &vkov1.TLSSpec{Enabled: true}
		v.Spec.Metrics = &vkov1.MetricsSpec{Enabled: true}
		v.Spec.Persistence = &vkov1.PersistenceSpec{Enabled: true, Size: resource.MustParse("1Gi")}
	})
}

// firstVolumeWithSource returns the index of the first volume whose source matches
// the predicate, failing the test when the fixture carries none: a mutation applied
// to a volume that does not exist would assert nothing.
func firstVolumeWithSource(t *testing.T, sts *appsv1.StatefulSet, match func(corev1.VolumeSource) bool) int {
	t.Helper()
	for i, vol := range sts.Spec.Template.Spec.Volumes {
		if match(vol.VolumeSource) {
			return i
		}
	}
	t.Fatalf("fixture carries no matching volume; volumes: %d", len(sts.Spec.Template.Spec.Volumes))
	return -1
}

// valkeyContainer returns the data container of the built pod template, which is
// the one carrying a command line, secret-backed env and volume mounts.
func valkeyContainer(t *testing.T, sts *appsv1.StatefulSet) *corev1.Container {
	t.Helper()
	c := findContainer(sts.Spec.Template.Spec, ValkeyContainerName)
	require.NotNil(t, c, "the fixture must contain the valkey container")
	return c
}

// secretEnvIndex locates the container env entry that reads the auth password out
// of the Secret, i.e. the one entry whose comparison needs ValueFrom.
func secretEnvIndex(t *testing.T, c *corev1.Container) int {
	t.Helper()
	for i, e := range c.Env {
		if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
			return i
		}
	}
	t.Fatalf("container %q carries no secret-backed env var", c.Name)
	return -1
}

func TestStatefulSetHasChanged_DriftPerField(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, sts *appsv1.StatefulSet)
		want   bool
		why    string
	}{
		{
			name:   "identical objects",
			mutate: func(_ *testing.T, _ *appsv1.StatefulSet) {},
			want:   false,
			why:    "an unchanged StatefulSet must not be rewritten, or every pass restarts the cluster",
		},
		{
			name: "terminationGracePeriodSeconds dropped",
			mutate: func(t *testing.T, sts *appsv1.StatefulSet) {
				require.NotNil(t, sts.Spec.Template.Spec.TerminationGracePeriodSeconds,
					"the builder sets a grace period; without one this row pins nothing")
				sts.Spec.Template.Spec.TerminationGracePeriodSeconds = nil
			},
			want: true,
			why:  "the drain handler needs the grace period, so losing it must be corrected",
		},
		{
			name: "container volume mount path moved",
			mutate: func(t *testing.T, sts *appsv1.StatefulSet) {
				c := valkeyContainer(t, sts)
				require.NotEmpty(t, c.VolumeMounts)
				c.VolumeMounts[0].MountPath += "-moved"
			},
			want: true,
			why:  "a mount path the container no longer sees is a broken pod, not cosmetic drift",
		},
		{
			name: "volume renamed",
			mutate: func(_ *testing.T, sts *appsv1.StatefulSet) {
				sts.Spec.Template.Spec.Volumes[0].Name += "-renamed"
			},
			want: true,
			why:  "a volume the desired spec names and the live object does not is missing storage",
		},
		{
			name: "ConfigMap volume replaced by an emptyDir",
			mutate: func(t *testing.T, sts *appsv1.StatefulSet) {
				i := firstVolumeWithSource(t, sts, func(s corev1.VolumeSource) bool { return s.ConfigMap != nil })
				sts.Spec.Template.Spec.Volumes[i].VolumeSource = corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				}
			},
			want: true,
			why:  "same name, no ConfigMap behind it: the pod would start without its valkey.conf",
		},
		{
			name: "TLS Secret volume replaced by an emptyDir",
			mutate: func(t *testing.T, sts *appsv1.StatefulSet) {
				i := firstVolumeWithSource(t, sts, func(s corev1.VolumeSource) bool { return s.Secret != nil })
				sts.Spec.Template.Spec.Volumes[i].VolumeSource = corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				}
			},
			want: true,
			why:  "same name, no Secret behind it: TLS material silently disappears",
		},
		{
			name: "TLS Secret volume points at another Secret",
			mutate: func(t *testing.T, sts *appsv1.StatefulSet) {
				i := firstVolumeWithSource(t, sts, func(s corev1.VolumeSource) bool { return s.Secret != nil })
				sts.Spec.Template.Spec.Volumes[i].Secret.SecretName = "someone-elses-tls"
			},
			want: true,
			why:  "a rotated certificate Secret must reach the pods",
		},
		{
			name: "pod template label value changed",
			mutate: func(t *testing.T, sts *appsv1.StatefulSet) {
				labels := sts.Spec.Template.Labels
				require.NotEmpty(t, labels)
				for k := range labels {
					labels[k] += "-drifted"
					return
				}
			},
			want: true,
			why:  "the Services select on these labels; a changed value takes pods out of the endpoints",
		},
		{
			name: "resource limit added to a container",
			mutate: func(t *testing.T, sts *appsv1.StatefulSet) {
				c := valkeyContainer(t, sts)
				if c.Resources.Limits == nil {
					c.Resources.Limits = corev1.ResourceList{}
				}
				c.Resources.Limits[corev1.ResourceMemory] = resource.MustParse("512Mi")
			},
			want: true,
			why:  "spec.resources is user-visible configuration and has to converge",
		},
		{
			name: "env var renamed",
			mutate: func(t *testing.T, sts *appsv1.StatefulSet) {
				c := valkeyContainer(t, sts)
				require.NotEmpty(t, c.Env)
				c.Env[0].Name += "_RENAMED"
			},
			want: true,
			why:  "the container reads its configuration by env var name",
		},
		{
			name: "env var value changed",
			mutate: func(t *testing.T, sts *appsv1.StatefulSet) {
				c := valkeyContainer(t, sts)
				require.NotEmpty(t, c.Env)
				c.Env[0].Value += "-drifted"
			},
			want: true,
			why:  "a plain env value is the payload of the setting it carries",
		},
		{
			name: "secret-backed env var lost its source",
			mutate: func(t *testing.T, sts *appsv1.StatefulSet) {
				c := valkeyContainer(t, sts)
				i := secretEnvIndex(t, c)
				c.Env[i].ValueFrom = nil
			},
			want: true,
			why: "name and (empty) value still match, so only comparing ValueFrom notices that " +
				"the container would start without a password",
		},
		{
			name: "secret-backed env var switched to a field ref",
			mutate: func(t *testing.T, sts *appsv1.StatefulSet) {
				c := valkeyContainer(t, sts)
				i := secretEnvIndex(t, c)
				c.Env[i].ValueFrom = &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
				}
			},
			want: true,
			why:  "both sides carry a ValueFrom, so only looking at SecretKeyRef catches this",
		},
		{
			name: "secret-backed env var points at another Secret key",
			mutate: func(t *testing.T, sts *appsv1.StatefulSet) {
				c := valkeyContainer(t, sts)
				i := secretEnvIndex(t, c)
				c.Env[i].ValueFrom.SecretKeyRef.Key += "-old"
			},
			want: true,
			why:  "spec.auth.secretPasswordKey changed; the pods must be re-created against the new key",
		},
		{
			name: "container command argument changed",
			mutate: func(t *testing.T, sts *appsv1.StatefulSet) {
				c := valkeyContainer(t, sts)
				require.NotEmpty(t, c.Command, "the valkey container is started through an explicit command")
				c.Command[len(c.Command)-1] += " # drifted"
			},
			want: true,
			why:  "the command line is how the config file reaches the server process",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := diffFixtureValkey()
			desired := BuildStatefulSet(v, testOperatorImage)
			current := desired.DeepCopy()
			tt.mutate(t, current)

			assert.Equal(t, tt.want, StatefulSetHasChanged(desired, current), tt.why)
		})
	}
}

// A pod template that gained a label must still be reported as changed even though
// every label the operator writes is still present: the length check is the only
// thing standing between "the operator owns this map" and a foreign controller
// quietly adding keys forever.
func TestStatefulSetHasChanged_ExtraPodTemplateLabel(t *testing.T) {
	v := diffFixtureValkey()
	desired := BuildStatefulSet(v, testOperatorImage)
	current := desired.DeepCopy()
	current.Spec.Template.Labels["injected-by"] = "someone-else"

	assert.True(t, StatefulSetHasChanged(desired, current))
}

// terminationGracePeriodEqual is asymmetric-looking code (three branches for two
// pointers) and the desired-side nil is the direction the table above cannot
// produce, because the builder always sets the field.
func TestTerminationGracePeriodEqual(t *testing.T) {
	assert.True(t, terminationGracePeriodEqual(nil, nil), "both unset is agreement")
	assert.True(t, terminationGracePeriodEqual(ptr.To(int64(30)), ptr.To(int64(30))),
		"equal values behind different pointers agree")
	assert.False(t, terminationGracePeriodEqual(nil, ptr.To(int64(30))))
	assert.False(t, terminationGracePeriodEqual(ptr.To(int64(30)), nil))
	assert.False(t, terminationGracePeriodEqual(ptr.To(int64(30)), ptr.To(int64(60))))
}
