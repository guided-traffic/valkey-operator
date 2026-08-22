package v1

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func boolPtr(b bool) *bool    { return &b }
func int32Ptr(i int32) *int32 { return &i }
func int64Ptr(i int64) *int64 { return &i }

// fixtureTime is a fixed timestamp so two independently built fixtures compare
// equal. metav1.Date carries no monotonic reading and time.UTC is a singleton.
func fixtureTime() metav1.Time {
	return metav1.Date(2026, 8, 20, 10, 30, 0, 0, time.UTC)
}

// fullValkey builds a Valkey with every field populated: every pointer non-nil,
// every slice and map non-empty, every scalar non-zero. TestFullValkeyFixture_IsExhaustive
// enforces that property, so a new spec field that is not added here fails the
// build-out rather than silently creating a hole in the DeepCopy tests.
func fullValkey() *Valkey {
	return &Valkey{
		TypeMeta: metav1.TypeMeta{APIVersion: "vko.gtrfc.com/v1", Kind: "Valkey"},
		ObjectMeta: metav1.ObjectMeta{
			Name:              "full",
			Namespace:         "prod",
			UID:               "0f6b6b0c-0000-4000-8000-000000000001",
			ResourceVersion:   "4242",
			Generation:        7,
			CreationTimestamp: fixtureTime(),
			Labels:            map[string]string{"team": "data"},
			Annotations:       map[string]string{"vko.gtrfc.com/known-master": "full-0"},
			Finalizers:        []string{"vko.gtrfc.com/cleanup"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         "apps/v1",
				Kind:               "StatefulSet",
				Name:               "full",
				UID:                "0f6b6b0c-0000-4000-8000-000000000002",
				Controller:         boolPtr(true),
				BlockOwnerDeletion: boolPtr(true),
			}},
		},
		Spec:   fullValkeySpec(),
		Status: fullValkeyStatus(),
	}
}

func fullValkeySpec() ValkeySpec {
	return ValkeySpec{
		Replicas: 3,
		Image:    "valkey/valkey:8.0",
		Sentinel: &SentinelSpec{
			Enabled:          true,
			Replicas:         3,
			PodLabels:        map[string]string{"app": "sentinel"},
			PodAnnotations:   map[string]string{"example.com/sentinel": "true"},
			AllowUnencrypted: true,
			DisableAuth:      true,
		},
		Auth: &AuthSpec{
			SecretName:        "my-valkey-secret",
			SecretPasswordKey: "password",
		},
		TLS: &TLSSpec{
			Enabled: true,
			CertManager: &CertManagerSpec{
				Issuer: CertManagerIssuerSpec{
					Group: "cert-manager.io",
					Kind:  "ClusterIssuer",
					Name:  "cluster-ca",
				},
				ExtraDNSNames: []string{"valkey.example.com", "valkey.internal"},
			},
			SecretName:         "my-tls-secret",
			AllowUnencrypted:   true,
			UnifiedCertificate: true,
		},
		Metrics: &MetricsSpec{
			Enabled: true,
			Image:   "oliver006/redis_exporter:v1.66.0",
			Port:    9121,
			Resources: &corev1.ResourceRequirements{
				Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
				Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Mi")},
				Claims:   []corev1.ResourceClaim{{Name: "exporter-claim", Request: "exporter-request"}},
			},
			ExtraArgs: []string{"--check-keys=*"},
			Service: &MetricsServiceSpec{
				Enabled: boolPtr(true),
				Labels:  map[string]string{"scrape": "yes"},
			},
			ServiceMonitor: &ServiceMonitorSpec{
				Enabled:       true,
				Interval:      "30s",
				ScrapeTimeout: "10s",
				Labels:        map[string]string{"release": "prometheus"},
			},
		},
		NetworkPolicy: &NetworkPolicySpec{Enabled: true, NamePrefix: "my-prefix"},
		Persistence: &PersistenceSpec{
			Enabled:      true,
			Mode:         PersistenceModeBoth,
			StorageClass: "fast-ssd",
			Size:         resource.MustParse("1Gi"),
		},
		Observer: &ObserverSpec{
			Enabled: true,
			DB:      intPtr(15),
			MTLS:    &ObserverMTLSSpec{Valkey: boolPtr(true), Sentinel: boolPtr(true)},
			Resources: &corev1.ResourceRequirements{
				Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m")},
				Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("128Mi")},
				Claims:   []corev1.ResourceClaim{{Name: "observer-claim", Request: "observer-request"}},
			},
			LogLevel:    ObserverLogLevelDebug,
			UnreadyWhen: fullUnreadyWhen(),
		},
		PodLabels:      map[string]string{"app": "valkey"},
		PodAnnotations: map[string]string{"example.com/annotation": "true"},
		Resources: corev1.ResourceRequirements{
			Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")},
			Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("256Mi")},
			Claims:   []corev1.ResourceClaim{{Name: "valkey-claim", Request: "valkey-request"}},
		},
		RollingUpdate:       &RollingUpdateSpec{SyncTimeout: &metav1.Duration{Duration: 7 * time.Minute}},
		PodDisruptionBudget: &PodDisruptionBudgetSpec{Enabled: true, MaxUnavailable: int32Ptr(1)},
		AntiAffinity:        &AntiAffinitySpec{Mode: AntiAffinityModeHard, TopologyKey: "topology.kubernetes.io/zone"},
	}
}

func fullUnreadyWhen() *ObserverUnreadyWhenSpec {
	return &ObserverUnreadyWhenSpec{
		MasterUnreachable:               boolPtr(true),
		WriteTestFailure:                boolPtr(true),
		ReadTestFailure:                 boolPtr(true),
		ReplicaSyncFailure:              boolPtr(true),
		ReplicaReadTestFailure:          boolPtr(true),
		SentinelUnreachable:             boolPtr(true),
		SentinelQuorumFailure:           boolPtr(true),
		SentinelMasterDown:              boolPtr(true),
		SentinelMasterHostnameInvalid:   boolPtr(true),
		SentinelReplicaHostnamesInvalid: boolPtr(true),
	}
}

func fullValkeyStatus() ValkeyStatus {
	return ValkeyStatus{
		ReadyReplicas:   3,
		MasterPod:       "full-0",
		Phase:           ValkeyPhaseOK,
		Message:         "all replicas in sync",
		OperatorVersion: "v1.2.3",
		ObserverReady:   boolPtr(true),
		Conditions: []metav1.Condition{{
			Type:               ConditionTypeTopologyRestored,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: 7,
			LastTransitionTime: fixtureTime(),
			Reason:             "TopologyRestored",
			Message:            "pod-0 is master again",
		}},
	}
}

// mutateEverything writes to every mutable location reachable from v: the
// pointed-to value behind every pointer, every slice element and every map
// entry. Applied to a DeepCopy, none of it may reach the original.
func mutateEverything(v *Valkey) {
	mutateObjectMeta(&v.ObjectMeta)
	mutateSpec(&v.Spec)
	mutateStatus(&v.Status)
}

func mutateObjectMeta(m *metav1.ObjectMeta) {
	m.Labels["team"] = "mutated"
	m.Annotations["vko.gtrfc.com/known-master"] = "mutated"
	m.Finalizers[0] = "mutated"
	m.OwnerReferences[0].Name = "mutated"
	*m.OwnerReferences[0].Controller = false
	*m.OwnerReferences[0].BlockOwnerDeletion = false
}

func mutateSpec(s *ValkeySpec) {
	s.Sentinel.Replicas = 99
	s.Sentinel.PodLabels["app"] = "mutated"
	s.Sentinel.PodAnnotations["example.com/sentinel"] = "mutated"
	s.Auth.SecretName = "mutated"
	s.TLS.SecretName = "mutated"
	s.TLS.CertManager.Issuer.Name = "mutated"
	s.TLS.CertManager.ExtraDNSNames[0] = "mutated"
	s.Metrics.Port = 99
	s.Metrics.ExtraArgs[0] = "--mutated"
	*s.Metrics.Service.Enabled = false
	s.Metrics.Service.Labels["scrape"] = "mutated"
	s.Metrics.ServiceMonitor.Labels["release"] = "mutated"
	mutateResources(s.Metrics.Resources)
	s.NetworkPolicy.NamePrefix = "mutated"
	s.Persistence.StorageClass = "mutated"
	s.Persistence.Size = resource.MustParse("99Gi")
	mutateObserver(s.Observer)
	s.PodLabels["app"] = "mutated"
	s.PodAnnotations["example.com/annotation"] = "mutated"
	mutateResources(&s.Resources)
	s.RollingUpdate.SyncTimeout.Duration = 99 * time.Second
	*s.PodDisruptionBudget.MaxUnavailable = 99
	s.AntiAffinity.TopologyKey = "mutated"
}

func mutateObserver(o *ObserverSpec) {
	*o.DB = 0
	*o.MTLS.Valkey = false
	*o.MTLS.Sentinel = false
	mutateResources(o.Resources)
	*o.UnreadyWhen.MasterUnreachable = false
	*o.UnreadyWhen.WriteTestFailure = false
	*o.UnreadyWhen.ReadTestFailure = false
	*o.UnreadyWhen.ReplicaSyncFailure = false
	*o.UnreadyWhen.ReplicaReadTestFailure = false
	*o.UnreadyWhen.SentinelUnreachable = false
	*o.UnreadyWhen.SentinelQuorumFailure = false
	*o.UnreadyWhen.SentinelMasterDown = false
	*o.UnreadyWhen.SentinelMasterHostnameInvalid = false
	*o.UnreadyWhen.SentinelReplicaHostnamesInvalid = false
}

func mutateResources(r *corev1.ResourceRequirements) {
	for k := range r.Limits {
		r.Limits[k] = resource.MustParse("999")
	}
	for k := range r.Requests {
		r.Requests[k] = resource.MustParse("999")
	}
	r.Claims[0].Name = "mutated"
}

func mutateStatus(s *ValkeyStatus) {
	*s.ObserverReady = false
	s.Conditions[0].Message = "mutated"
	s.Conditions[0].Reason = "Mutated"
}

func TestValkey_DeepCopy_EqualsOriginal(t *testing.T) {
	original := fullValkey()

	copied := original.DeepCopy()

	require.NotSame(t, original, copied)
	require.Equal(t, original, copied)
}

// TestValkey_DeepCopy_MutatingCopyLeavesOriginalUntouched is the assertion that
// actually catches an aliasing bug: equality alone passes even when the copy
// shares every map, slice and pointer with the original.
func TestValkey_DeepCopy_MutatingCopyLeavesOriginalUntouched(t *testing.T) {
	original := fullValkey()
	copied := original.DeepCopy()

	mutateEverything(copied)

	require.Equal(t, fullValkey(), original,
		"mutating the deep copy leaked into the original")
	require.NotEqual(t, original, copied,
		"mutateEverything must actually change the copy")
}

func TestValkey_DeepCopy_MutatingOriginalLeavesCopyUntouched(t *testing.T) {
	original := fullValkey()
	copied := original.DeepCopy()

	mutateEverything(original)

	require.Equal(t, fullValkey(), copied,
		"mutating the original leaked into the deep copy")
}

// TestValkey_DeepCopy_SharesNoReferences walks the whole object graph and fails
// on any pointer, slice backing array or map that the copy shares with the
// original. Unlike the explicit mutation test it needs no new assertion when a
// field is added -- populating fullValkey is enough.
func TestValkey_DeepCopy_SharesNoReferences(t *testing.T) {
	original := fullValkey()

	copied := original.DeepCopy()

	assertNoSharedReferences(t, "Valkey", reflect.ValueOf(original), reflect.ValueOf(copied))
}

// TestFullValkeyFixture_IsExhaustive keeps the fixture honest: an unpopulated
// field is an untested DeepCopyInto branch, so every field of every type
// declared in this package must be non-zero, every pointer non-nil and every
// slice and map non-empty.
func TestFullValkeyFixture_IsExhaustive(t *testing.T) {
	assertFullyPopulated(t, "Valkey", reflect.ValueOf(fullValkey()))
}

func TestValkey_DeepCopyObject(t *testing.T) {
	original := fullValkey()

	obj := original.DeepCopyObject()

	copied, ok := obj.(*Valkey)
	require.True(t, ok, "DeepCopyObject must return a *Valkey")
	require.NotSame(t, original, copied)
	require.Equal(t, original, copied)

	mutateEverything(copied)
	require.Equal(t, fullValkey(), original)
}

func TestValkey_DeepCopyObject_NilReceiver(t *testing.T) {
	var v *Valkey

	require.Nil(t, v.DeepCopyObject())
}

func fullValkeyList() *ValkeyList {
	return &ValkeyList{
		TypeMeta: metav1.TypeMeta{APIVersion: "vko.gtrfc.com/v1", Kind: "ValkeyList"},
		ListMeta: metav1.ListMeta{
			ResourceVersion:    "4242",
			Continue:           "continue-token",
			RemainingItemCount: int64Ptr(3),
		},
		Items: []Valkey{*fullValkey(), *minimalValkey()},
	}
}

func TestValkeyList_DeepCopy_MutatingCopyLeavesOriginalUntouched(t *testing.T) {
	original := fullValkeyList()
	copied := original.DeepCopy()

	require.Equal(t, original, copied)
	require.NotSame(t, &original.Items[0], &copied.Items[0],
		"list items must not share backing array")

	*copied.RemainingItemCount = 99
	mutateEverything(&copied.Items[0])
	copied.Items[1].Spec.Image = "valkey/valkey:mutated"

	require.Equal(t, fullValkeyList(), original,
		"mutating the copied list leaked into the original")
}

func TestValkeyList_DeepCopy_SharesNoReferences(t *testing.T) {
	original := fullValkeyList()

	copied := original.DeepCopy()

	assertNoSharedReferences(t, "ValkeyList", reflect.ValueOf(original), reflect.ValueOf(copied))
}

func TestValkeyList_DeepCopyObject(t *testing.T) {
	original := fullValkeyList()

	obj := original.DeepCopyObject()

	copied, ok := obj.(*ValkeyList)
	require.True(t, ok, "DeepCopyObject must return a *ValkeyList")
	require.Equal(t, original, copied)

	mutateEverything(&copied.Items[0])
	require.Equal(t, fullValkeyList(), original)
}

func TestValkeyList_DeepCopyObject_NilReceiver(t *testing.T) {
	var l *ValkeyList

	require.Nil(t, l.DeepCopyObject())
}

func TestValkeyList_DeepCopy_EmptyItems(t *testing.T) {
	original := &ValkeyList{ListMeta: metav1.ListMeta{ResourceVersion: "1"}}

	copied := original.DeepCopy()

	require.Equal(t, original, copied)
	require.Nil(t, copied.Items, "a nil item slice must not become an empty slice")
}

// minimalValkey is the counterpart of fullValkey: every optional field is
// omitted, so it exercises the "not set" side of every pointer branch in
// DeepCopyInto.
func minimalValkey() *Valkey {
	return &Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: "minimal", Namespace: "default"},
		Spec:       ValkeySpec{Replicas: 1, Image: "valkey/valkey:8.0"},
	}
}

// TestValkey_DeepCopy_NilOptionalFieldsStayNil guards the semantics of an
// omitted block: allocating an empty struct instead of keeping nil would turn
// "no metrics configured" into "metrics block present but empty".
func TestValkey_DeepCopy_NilOptionalFieldsStayNil(t *testing.T) {
	original := minimalValkey()

	copied := original.DeepCopy()

	require.Equal(t, original, copied)
	require.Nil(t, copied.Spec.Sentinel)
	require.Nil(t, copied.Spec.Auth)
	require.Nil(t, copied.Spec.TLS)
	require.Nil(t, copied.Spec.Metrics)
	require.Nil(t, copied.Spec.NetworkPolicy)
	require.Nil(t, copied.Spec.Persistence)
	require.Nil(t, copied.Spec.Observer)
	require.Nil(t, copied.Spec.PodLabels)
	require.Nil(t, copied.Spec.PodAnnotations)
	require.Nil(t, copied.Spec.RollingUpdate)
	require.Nil(t, copied.Spec.PodDisruptionBudget)
	require.Nil(t, copied.Spec.AntiAffinity)
	require.Nil(t, copied.Status.ObserverReady)
	require.Nil(t, copied.Status.Conditions)
}

// TestValkey_DeepCopy_NestedStructsWithNilInnerFields covers the combination the
// two fixtures miss: the outer block is present but all of its own optional
// fields are nil.
func TestValkey_DeepCopy_NestedStructsWithNilInnerFields(t *testing.T) {
	original := minimalValkey()
	original.Spec.TLS = &TLSSpec{Enabled: true}
	original.Spec.Metrics = &MetricsSpec{Enabled: true}
	original.Spec.Observer = &ObserverSpec{Enabled: true}
	original.Spec.Sentinel = &SentinelSpec{Enabled: true, Replicas: 3}
	original.Spec.RollingUpdate = &RollingUpdateSpec{}
	original.Spec.PodDisruptionBudget = &PodDisruptionBudgetSpec{Enabled: true}
	original.Spec.Metrics.ServiceMonitor = &ServiceMonitorSpec{Enabled: true}
	original.Spec.Metrics.Service = &MetricsServiceSpec{}
	original.Spec.Observer.MTLS = &ObserverMTLSSpec{}
	original.Spec.Observer.UnreadyWhen = &ObserverUnreadyWhenSpec{}
	original.Spec.TLS.CertManager = &CertManagerSpec{Issuer: CertManagerIssuerSpec{Kind: "Issuer", Name: "ca"}}

	copied := original.DeepCopy()

	require.Equal(t, original, copied)
	require.NotSame(t, original.Spec.TLS, copied.Spec.TLS)
	require.NotSame(t, original.Spec.TLS.CertManager, copied.Spec.TLS.CertManager)
	require.NotSame(t, original.Spec.Metrics.Service, copied.Spec.Metrics.Service)
	require.NotSame(t, original.Spec.Metrics.ServiceMonitor, copied.Spec.Metrics.ServiceMonitor)
	require.NotSame(t, original.Spec.Observer.MTLS, copied.Spec.Observer.MTLS)
	require.NotSame(t, original.Spec.Observer.UnreadyWhen, copied.Spec.Observer.UnreadyWhen)
	require.NotSame(t, original.Spec.RollingUpdate, copied.Spec.RollingUpdate)
	require.NotSame(t, original.Spec.PodDisruptionBudget, copied.Spec.PodDisruptionBudget)
	require.Nil(t, copied.Spec.TLS.CertManager.ExtraDNSNames)
	require.Nil(t, copied.Spec.Metrics.Service.Enabled)
	require.Nil(t, copied.Spec.Observer.MTLS.Valkey)
	require.Nil(t, copied.Spec.Observer.UnreadyWhen.MasterUnreachable)
	require.Nil(t, copied.Spec.RollingUpdate.SyncTimeout)
	require.Nil(t, copied.Spec.PodDisruptionBudget.MaxUnavailable)
}

// TestDeepCopy_NilReceiverReturnsNil covers the "if in == nil" branch that
// controller-gen emits for every generated type. A DeepCopy that dereferenced a
// nil receiver would panic inside client-go caches.
func TestDeepCopy_NilReceiverReturnsNil(t *testing.T) {
	tests := []struct {
		name     string
		deepCopy func() any
	}{
		{"AntiAffinitySpec", func() any { var in *AntiAffinitySpec; return in.DeepCopy() }},
		{"AuthSpec", func() any { var in *AuthSpec; return in.DeepCopy() }},
		{"CertManagerIssuerSpec", func() any { var in *CertManagerIssuerSpec; return in.DeepCopy() }},
		{"CertManagerSpec", func() any { var in *CertManagerSpec; return in.DeepCopy() }},
		{"MetricsServiceSpec", func() any { var in *MetricsServiceSpec; return in.DeepCopy() }},
		{"MetricsSpec", func() any { var in *MetricsSpec; return in.DeepCopy() }},
		{"NetworkPolicySpec", func() any { var in *NetworkPolicySpec; return in.DeepCopy() }},
		{"ObserverMTLSSpec", func() any { var in *ObserverMTLSSpec; return in.DeepCopy() }},
		{"ObserverSpec", func() any { var in *ObserverSpec; return in.DeepCopy() }},
		{"ObserverUnreadyWhenSpec", func() any { var in *ObserverUnreadyWhenSpec; return in.DeepCopy() }},
		{"PersistenceSpec", func() any { var in *PersistenceSpec; return in.DeepCopy() }},
		{"PodDisruptionBudgetSpec", func() any { var in *PodDisruptionBudgetSpec; return in.DeepCopy() }},
		{"RollingUpdateSpec", func() any { var in *RollingUpdateSpec; return in.DeepCopy() }},
		{"SentinelSpec", func() any { var in *SentinelSpec; return in.DeepCopy() }},
		{"ServiceMonitorSpec", func() any { var in *ServiceMonitorSpec; return in.DeepCopy() }},
		{"TLSSpec", func() any { var in *TLSSpec; return in.DeepCopy() }},
		{"Valkey", func() any { var in *Valkey; return in.DeepCopy() }},
		{"ValkeyList", func() any { var in *ValkeyList; return in.DeepCopy() }},
		{"ValkeySpec", func() any { var in *ValkeySpec; return in.DeepCopy() }},
		{"ValkeyStatus", func() any { var in *ValkeyStatus; return in.DeepCopy() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Nil(t, tt.deepCopy())
		})
	}
}

// TestDeepCopy_SubStructsAreIndependent covers the per-type DeepCopy entry
// points on their own, not only through Valkey.DeepCopy.
func TestDeepCopy_SubStructsAreIndependent(t *testing.T) {
	spec := fullValkeySpec()
	specCopy := spec.DeepCopy()
	mutateSpec(specCopy)
	require.Equal(t, fullValkeySpec(), spec)

	status := fullValkeyStatus()
	statusCopy := status.DeepCopy()
	mutateStatus(statusCopy)
	require.Equal(t, fullValkeyStatus(), status)

	unreadyWhen := fullUnreadyWhen()
	unreadyWhenCopy := unreadyWhen.DeepCopy()
	*unreadyWhenCopy.MasterUnreachable = false
	require.True(t, *unreadyWhen.MasterUnreachable)

	sentinel := spec.Sentinel.DeepCopy()
	sentinel.PodLabels["app"] = "mutated"
	require.Equal(t, "sentinel", spec.Sentinel.PodLabels["app"])

	tls := spec.TLS.DeepCopy()
	tls.CertManager.ExtraDNSNames[0] = "mutated"
	require.Equal(t, "valkey.example.com", spec.TLS.CertManager.ExtraDNSNames[0])

	certManager := spec.TLS.CertManager.DeepCopy()
	certManager.Issuer.Name = "mutated"
	require.Equal(t, "cluster-ca", spec.TLS.CertManager.Issuer.Name)

	issuer := spec.TLS.CertManager.Issuer.DeepCopy()
	issuer.Name = "mutated"
	require.Equal(t, "cluster-ca", spec.TLS.CertManager.Issuer.Name)

	metrics := spec.Metrics.DeepCopy()
	metrics.ExtraArgs[0] = "--mutated"
	require.Equal(t, "--check-keys=*", spec.Metrics.ExtraArgs[0])

	metricsService := spec.Metrics.Service.DeepCopy()
	*metricsService.Enabled = false
	require.True(t, *spec.Metrics.Service.Enabled)

	serviceMonitor := spec.Metrics.ServiceMonitor.DeepCopy()
	serviceMonitor.Labels["release"] = "mutated"
	require.Equal(t, "prometheus", spec.Metrics.ServiceMonitor.Labels["release"])

	observer := spec.Observer.DeepCopy()
	*observer.DB = 0
	require.Equal(t, 15, *spec.Observer.DB)

	mtls := spec.Observer.MTLS.DeepCopy()
	*mtls.Valkey = false
	require.True(t, *spec.Observer.MTLS.Valkey)

	persistence := spec.Persistence.DeepCopy()
	persistence.Size = resource.MustParse("99Gi")
	require.Equal(t, "1Gi", spec.Persistence.Size.String())

	pdb := spec.PodDisruptionBudget.DeepCopy()
	*pdb.MaxUnavailable = 99
	require.Equal(t, int32(1), *spec.PodDisruptionBudget.MaxUnavailable)

	rollingUpdate := spec.RollingUpdate.DeepCopy()
	rollingUpdate.SyncTimeout.Duration = time.Second
	require.Equal(t, 7*time.Minute, spec.RollingUpdate.SyncTimeout.Duration)

	antiAffinity := spec.AntiAffinity.DeepCopy()
	antiAffinity.TopologyKey = "mutated"
	require.Equal(t, "topology.kubernetes.io/zone", spec.AntiAffinity.TopologyKey)

	auth := spec.Auth.DeepCopy()
	auth.SecretName = "mutated"
	require.Equal(t, "my-valkey-secret", spec.Auth.SecretName)

	networkPolicy := spec.NetworkPolicy.DeepCopy()
	networkPolicy.NamePrefix = "mutated"
	require.Equal(t, "my-prefix", spec.NetworkPolicy.NamePrefix)
}

// TestValkey_ImplementsRuntimeObject pins the interface the controller-runtime
// client requires; losing it is a compile-time break that only shows up at the
// call site otherwise.
func TestValkey_ImplementsRuntimeObject(t *testing.T) {
	var _ runtime.Object = &Valkey{}
	var _ runtime.Object = &ValkeyList{}

	obj := runtime.Object(fullValkey())
	require.Equal(t, "Valkey", obj.GetObjectKind().GroupVersionKind().Kind)
}

var (
	timeType = reflect.TypeOf(time.Time{})
	apiV1Pkg = reflect.TypeOf(ValkeySpec{}).PkgPath()
)

// assertNoSharedReferences fails when the copy shares a pointer, a slice
// backing array or a map with the original. time.Time is skipped: its
// *time.Location is immutable and legitimately shared by every copy.
func assertNoSharedReferences(t *testing.T, path string, orig, copied reflect.Value) {
	t.Helper()

	if orig.Type() == timeType {
		return
	}

	switch orig.Kind() {
	case reflect.Pointer:
		if orig.IsNil() || copied.IsNil() {
			return
		}
		require.NotEqual(t, orig.Pointer(), copied.Pointer(), "aliased pointer at %s", path)
		assertNoSharedReferences(t, path+".*", orig.Elem(), copied.Elem())
	case reflect.Slice:
		if orig.Len() == 0 || copied.Len() == 0 {
			return
		}
		require.NotEqual(t, orig.Pointer(), copied.Pointer(), "aliased slice backing array at %s", path)
		for i := 0; i < orig.Len() && i < copied.Len(); i++ {
			assertNoSharedReferences(t, path+"[i]", orig.Index(i), copied.Index(i))
		}
	case reflect.Map:
		if orig.Len() == 0 || copied.Len() == 0 {
			return
		}
		require.NotEqual(t, orig.Pointer(), copied.Pointer(), "aliased map at %s", path)
		for _, key := range orig.MapKeys() {
			other := copied.MapIndex(key)
			if !other.IsValid() {
				continue
			}
			assertNoSharedReferences(t, path+"[k]", orig.MapIndex(key), other)
		}
	case reflect.Interface:
		if orig.IsNil() || copied.IsNil() {
			return
		}
		assertNoSharedReferences(t, path+".(iface)", orig.Elem(), copied.Elem())
	case reflect.Struct:
		for i := 0; i < orig.NumField(); i++ {
			assertNoSharedReferences(t, path+"."+orig.Type().Field(i).Name, orig.Field(i), copied.Field(i))
		}
	}
}

// assertFullyPopulated fails on any zero value inside a type declared in this
// package. Types from other packages are only checked for a non-nil pointer,
// because populating all of corev1.ResourceRequirements is not this package's
// contract.
func assertFullyPopulated(t *testing.T, path string, v reflect.Value) {
	t.Helper()

	switch v.Kind() {
	case reflect.Pointer:
		require.False(t, v.IsNil(), "fullValkey leaves %s nil, so its DeepCopy branch is untested", path)
		assertFullyPopulated(t, path+".*", v.Elem())
	case reflect.Slice, reflect.Map:
		require.NotZero(t, v.Len(), "fullValkey leaves %s empty, so its DeepCopy branch is untested", path)
		if v.Kind() == reflect.Slice {
			assertFullyPopulated(t, path+"[0]", v.Index(0))
		}
	case reflect.Struct:
		if v.Type().PkgPath() != apiV1Pkg {
			return
		}
		for i := 0; i < v.NumField(); i++ {
			assertFullyPopulated(t, path+"."+v.Type().Field(i).Name, v.Field(i))
		}
	default:
		require.False(t, v.IsZero(), "fullValkey leaves %s at its zero value", path)
	}
}
