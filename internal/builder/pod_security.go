package builder

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// Every pod the operator generates runs rootless, with no option
// (docs/adr/0032-generated-pods-run-rootless.md). Root was a defect, not a setting:
// every container that runs the Valkey image sets `command:`, which replaces the
// image ENTRYPOINT -- the script that would have dropped to the `valkey` user --
// so until this file existed valkey-server, valkey-sentinel and both init scripts
// ran as uid 0 with the runtime's default capabilities and no seccomp filter.
//
// The posture is applied by one walk over an assembled PodSpec rather than field by
// field in each container builder, so that a container added later inherits it
// instead of having to remember it (ADR 0032 D1).

const (
	// ValkeyUID is the uid of the `valkey` user the upstream image creates, measured
	// 999 in both pinned lines (test/testimages). Every data and Sentinel pod runs as
	// it -- the sidecar and the exporter included, whose images declare users of
	// their own -- so that the data volume has exactly one owner.
	ValkeyUID int64 = 999

	// ValkeyGID is the primary group of the `valkey` user, and the fsGroup kubelet
	// re-groups the data volume to where the volume type supports it.
	ValkeyGID int64 = 999

	// OperatorUID is the numeric distroless `nonroot` user the operator image
	// declares (USER 65532 in gcr.io/distroless/static-debian12:nonroot). The
	// observer runs the operator image and is pinned to it, uid and gid, rather than
	// inheriting whatever an image built from another base declares.
	OperatorUID int64 = 65532

	// DataWritableCheckContainerName is the pre-flight init container of every
	// persistent data pod. It fails the pod, naming the fix, when the data volume
	// holds anything uid 999 cannot write -- instead of letting valkey-server start
	// and answer MISCONF to every write while the pod stays Ready (ADR 0032 D2).
	DataWritableCheckContainerName = "check-data-writable"

	// DataOwnershipRepairContainerName is the migration-only init container that
	// re-owns root-written data to uid 999 on storage where kubelet does not apply
	// fsGroup. It is the one root process the operator still creates, and only while
	// a data pod built by an earlier operator exists (ADR 0032 D2).
	DataOwnershipRepairContainerName = "fix-data-ownership"
)

// restrictedContainerSecurityContext is the container-level posture of every
// generated container: not privileged, no privilege escalation (no_new_privs), a
// read-only root filesystem -- every path a process writes is a mounted volume --
// and no capability at all. privileged: false is the API default; it is stated so
// that the posture reads complete in the object and a scanner does not have to
// know the default (ADR 0033 D4).
func restrictedContainerSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		Privileged:               ptr.To(false),
		AllowPrivilegeEscalation: ptr.To(false),
		ReadOnlyRootFilesystem:   ptr.To(true),
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
	}
}

// applyPodHardening sets what every generated pod shares beyond its securityContext
// (docs/adr/0033-generated-pods-take-a-seccomp-profile-and-an-opt-in-user-namespace.md):
// no Service environment variables, and the opt-in user namespace. hostNetwork,
// hostPID and hostIPC are not set because they are plain booleans whose zero value
// is false -- the API cannot carry an explicit false, and no builder sets them.
//
// enableServiceLinks: false keeps kubelet from injecting the Docker-link style
// variables (<SERVICE>_SERVICE_HOST, _SERVICE_PORT, _PORT, _PORT_<n>_<PROTO>...) for
// every Service of the namespace into every container: an inventory of the
// namespace no process here reads, and a name collision with variables the
// containers do read. The API server's own KUBERNETES_SERVICE_* variables are
// injected regardless.
func applyPodHardening(spec *corev1.PodSpec, v *vkov1.Valkey) {
	spec.EnableServiceLinks = ptr.To(false)
	spec.HostUsers = nil
	if v.UsesUserNamespaces() {
		spec.HostUsers = ptr.To(false)
	}
}

// restrictContainers applies restrictedContainerSecurityContext to every container
// and init container of spec. It is the walk: a container is covered because it is
// in the PodSpec, not because its builder remembered to ask for it.
func restrictContainers(spec *corev1.PodSpec) {
	for i := range spec.InitContainers {
		spec.InitContainers[i].SecurityContext = restrictedContainerSecurityContext()
	}
	for i := range spec.Containers {
		spec.Containers[i].SecurityContext = restrictedContainerSecurityContext()
	}
}

// applyValkeyPodSecurity is the posture of the data and Sentinel pods: uid and gid
// 999, fsGroup 999 so kubelet hands the data volume to that group where the volume
// type allows it, RuntimeDefault seccomp, and the restricted container posture on
// every container.
//
// fsGroupChangePolicy is deliberately left unset, which means Always:
// OnRootMismatch inspects only the volume root and would skip files a later root
// writer left beneath a correctly owned root, and a Valkey data directory holds a
// handful of files, so the recursive walk costs nothing worth saving.
//
// The seccomp profile is RuntimeDefault unless spec.podSecurity.seccompProfile
// names a Localhost one; Unconfined is refused by the CRD (ADR 0033 D1).
func applyValkeyPodSecurity(spec *corev1.PodSpec, v *vkov1.Valkey) {
	spec.SecurityContext = &corev1.PodSecurityContext{
		RunAsNonRoot:   ptr.To(true),
		RunAsUser:      ptr.To(ValkeyUID),
		RunAsGroup:     ptr.To(ValkeyGID),
		FSGroup:        ptr.To(ValkeyGID),
		SeccompProfile: v.GetSeccompProfile(),
	}
	restrictContainers(spec)
	applyPodHardening(spec, v)
}

// applyObserverPodSecurity is the observer's posture: the operator image's numeric
// distroless `nonroot` user (65532) as uid, gid and fsGroup -- the fsGroup makes the
// optional TLS Secret volume readable to that group whatever its mode -- the same
// seccomp profile as the Valkey pods, and the restricted container posture.
func applyObserverPodSecurity(spec *corev1.PodSpec, v *vkov1.Valkey) {
	spec.SecurityContext = &corev1.PodSecurityContext{
		RunAsNonRoot:   ptr.To(true),
		RunAsUser:      ptr.To(OperatorUID),
		RunAsGroup:     ptr.To(OperatorUID),
		FSGroup:        ptr.To(OperatorUID),
		SeccompProfile: v.GetSeccompProfile(),
	}
	restrictContainers(spec)
	applyPodHardening(spec, v)
}

// dataWritableCheckScript fails when anything valkey-server has to write is not
// writable by the uid the pod runs as. Shell builtins only ([, echo, exit): it is
// the first thing that runs in the pod and must not depend on anything the image
// might drop.
//
// It checks the two directories valkey-server writes into and every regular file
// directly in them. Other entries -- lost+found on a fresh ext4 volume above all --
// are none of valkey-server's business and would only produce false failures.
const dataWritableCheckScript = `fail=0
for d in ` + DataDir + ` ` + DataDir + `/appendonlydir; do
  [ -d "$d" ] || continue
  if [ ! -w "$d" ] || [ ! -x "$d" ]; then
    echo "check-data-writable: directory $d is not writable by this pod's uid" >&2
    fail=1
  fi
  for f in "$d"/* "$d"/.[!.]*; do
    [ -f "$f" ] || continue
    if [ ! -w "$f" ]; then
      echo "check-data-writable: file $f is not writable by this pod's uid" >&2
      fail=1
    fi
  done
done
if [ "$fail" -ne 0 ]; then
  echo "check-data-writable: the data volume is not writable by uid 999. Either an earlier operator version wrote it as root and neither kubelet (fsGroup) nor the ownership repair could re-own it -- NFS with root_squash is the known case, as is a claim retained from an earlier scale-down -- or the storage creates its volume root owned by root and kubelet applies no fsGroup to it (a static hostPath, a CSI driver with fsGroupPolicy None). Fix: chown -R 999:999 the volume on the storage side, then delete this pod (docs/adr/0032-generated-pods-run-rootless.md)." >&2
  exit 1
fi`

// dataWritableCheck returns the pre-flight init container of a persistent data pod,
// or nothing. It returns a slice so buildPodSpec appends unconditionally: that
// function has no complexity budget left for a branch.
//
// The pod runs it before anything else so that an unwritable data volume stops the
// pod loudly. Without it a legacy RDB dataset on a root-owned 0755 volume root
// starts, serves reads, answers PONG -- and then fails every BGSAVE, and because
// the generated config sets stop-writes-on-bgsave-error, every write returns
// MISCONF while the pod stays Ready (measured, T31).
func dataWritableCheck(v *vkov1.Valkey) []corev1.Container {
	if !v.IsPersistenceEnabled() {
		return nil
	}
	return []corev1.Container{{
		Name:    DataWritableCheckContainerName,
		Image:   v.Spec.Image,
		Command: []string{"sh", "-c", dataWritableCheckScript},
		VolumeMounts: []corev1.VolumeMount{{
			Name:      DataVolumeName,
			MountPath: DataDir,
		}},
		// The last lines of the log become the termination message, so `kubectl
		// describe pod` shows the fix without anyone reading logs.
		TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
	}}
}

// dataOwnershipRepairScript re-owns everything under the data mount that uid 999
// does not own. It needs CAP_CHOWN and nothing else: the legacy files are owned by
// root, which is the uid this container runs as, so traversing them needs no
// DAC override (measured, T31).
//
// It is best-effort and always exits 0; the pre-flight after it is the one gate.
// A pod created while the template carried the repair keeps it in its immutable
// spec and re-runs it on every sandbox restart, and a second run cannot traverse a
// directory the first one handed to 999 with mode 0700 -- lost+found on an ext4
// root -- without the DAC override it deliberately lacks. A failing repair would
// then block a migrated pod after every node reboot. -h re-owns a symlink itself,
// never its target.
const dataOwnershipRepairScript = `find ` + DataDir + ` ! -user 999 -exec chown -h 999:999 {} + ; exit 0`

// dataOwnershipRepairContainer is the migration-only repair: uid 0 with every
// capability dropped except CHOWN, no privilege escalation and a read-only root
// filesystem. It is the one container that does not get the restricted posture,
// and the reason the template carrying it passes Pod Security "baseline" but not
// "restricted".
func dataOwnershipRepairContainer(image string) corev1.Container {
	return corev1.Container{
		Name:    DataOwnershipRepairContainerName,
		Image:   image,
		Command: []string{"sh", "-c", dataOwnershipRepairScript},
		VolumeMounts: []corev1.VolumeMount{{
			Name:      DataVolumeName,
			MountPath: DataDir,
		}},
		SecurityContext: &corev1.SecurityContext{
			RunAsUser:                ptr.To(int64(0)),
			RunAsGroup:               ptr.To(int64(0)),
			RunAsNonRoot:             ptr.To(false),
			Privileged:               ptr.To(false),
			AllowPrivilegeEscalation: ptr.To(false),
			ReadOnlyRootFilesystem:   ptr.To(true),
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
				Add:  []corev1.Capability{"CHOWN"},
			},
		},
	}
}

// WithDataOwnershipRepair inserts the ownership repair in front of every other init
// container of an already-built data StatefulSet. It is idempotent.
//
// It runs on the built object, after ComputePodSpecHash, and that ordering is the
// whole design (ADR 0032 D2): the pod-spec hash never sees the repair, so neither
// the template write that adds it while legacy pods exist nor the one that removes
// it is itself a roll -- a narrow, recorded exception to ADR 0005 D7. The pods
// created while it was in the template keep it in their immutable spec, and once it
// has left the template the controller replaces them for that alone
// (podCarriesRetiredRepair): the second roll of D2.
func WithDataOwnershipRepair(sts *appsv1.StatefulSet) {
	spec := &sts.Spec.Template.Spec
	if HasDataOwnershipRepair(spec) {
		return
	}
	image := ""
	for _, c := range spec.Containers {
		if c.Name == ValkeyContainerName {
			image = c.Image
		}
	}
	spec.InitContainers = append([]corev1.Container{dataOwnershipRepairContainer(image)}, spec.InitContainers...)
}

// HasDataOwnershipRepair reports whether a pod spec carries the ownership repair.
func HasDataOwnershipRepair(spec *corev1.PodSpec) bool {
	for _, c := range spec.InitContainers {
		if c.Name == DataOwnershipRepairContainerName {
			return true
		}
	}
	return false
}

// PodRunsRootless reports whether a pod was built with the rootless posture. A pod
// spec cannot be edited after creation, so this is evidence no label or
// annotation can forge, and it survives an operator restart: it is how the
// migration tells a pod built by an earlier operator from one built by this one.
func PodRunsRootless(pod *corev1.Pod) bool {
	sc := pod.Spec.SecurityContext
	return sc != nil && sc.RunAsNonRoot != nil && *sc.RunAsNonRoot
}

// podSecurityContextChanged reports whether current lacks or contradicts a
// pod-level field desired sets. Subset semantics: a field desired leaves unset is
// not compared -- nil and an empty struct are the same statement -- so a field an
// admission policy adds on top of the operator's (a mutating Kyverno policy, say)
// is not a drift the operator rewrites the StatefulSet over on every pass.
func podSecurityContextChanged(desired, current *corev1.PodSecurityContext) bool {
	if desired == nil {
		return false
	}
	if current == nil {
		current = &corev1.PodSecurityContext{}
	}
	return ptrDiffers(desired.RunAsNonRoot, current.RunAsNonRoot) ||
		ptrDiffers(desired.RunAsUser, current.RunAsUser) ||
		ptrDiffers(desired.RunAsGroup, current.RunAsGroup) ||
		ptrDiffers(desired.FSGroup, current.FSGroup) ||
		seccompProfileDiffers(desired.SeccompProfile, current.SeccompProfile)
}

// containerSecurityContextChanged is podSecurityContextChanged for a container,
// with one field that is deliberately not a subset: current may not add a
// capability desired does not add. A subset comparison there would let an
// out-of-band edit grant NET_RAW and never be converged back.
func containerSecurityContextChanged(desired, current *corev1.SecurityContext) bool {
	if desired == nil {
		return false
	}
	if current == nil {
		current = &corev1.SecurityContext{}
	}
	return ptrDiffers(desired.Privileged, current.Privileged) ||
		ptrDiffers(desired.AllowPrivilegeEscalation, current.AllowPrivilegeEscalation) ||
		ptrDiffers(desired.ReadOnlyRootFilesystem, current.ReadOnlyRootFilesystem) ||
		ptrDiffers(desired.RunAsNonRoot, current.RunAsNonRoot) ||
		ptrDiffers(desired.RunAsUser, current.RunAsUser) ||
		ptrDiffers(desired.RunAsGroup, current.RunAsGroup) ||
		capabilitiesDiffer(desired.Capabilities, current.Capabilities)
}

// podHardeningChanged compares the two pod-level fields applyPodHardening sets.
// hostUsers is compared exactly, not as a subset: turning spec.podSecurity.
// userNamespaces off leaves desired unset, and a subset comparison would never
// converge the persisted false back -- the capabilities.add argument, for a field
// whose unset value is the weaker one.
func podHardeningChanged(desired, current *corev1.PodSpec) bool {
	return ptrDiffers(desired.EnableServiceLinks, current.EnableServiceLinks) ||
		!ptr.Equal(desired.HostUsers, current.HostUsers)
}

// ptrDiffers reports whether desired sets a value current does not carry.
func ptrDiffers[T comparable](desired, current *T) bool {
	return desired != nil && (current == nil || *desired != *current)
}

// seccompProfileDiffers compares the profile type desired sets.
func seccompProfileDiffers(desired, current *corev1.SeccompProfile) bool {
	if desired == nil {
		return false
	}
	return current == nil || current.Type != desired.Type ||
		ptrDiffers(desired.LocalhostProfile, current.LocalhostProfile)
}

// capabilitiesDiffer reports whether current drops less than desired drops, or
// adds anything desired does not add.
func capabilitiesDiffer(desired, current *corev1.Capabilities) bool {
	if desired == nil {
		return false
	}
	if current == nil {
		current = &corev1.Capabilities{}
	}
	return !containsAllCapabilities(current.Drop, desired.Drop) ||
		!containsAllCapabilities(desired.Add, current.Add)
}

// containsAllCapabilities reports whether every capability in want is in have.
func containsAllCapabilities(have, want []corev1.Capability) bool {
	set := make(map[corev1.Capability]bool, len(have))
	for _, c := range have {
		set[c] = true
	}
	for _, c := range want {
		if !set[c] {
			return false
		}
	}
	return true
}
