package metrics

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

const (
	testOperatorVersion = "1.11.0"
	testOperatorCommit  = "abc1234"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = vkov1.AddToScheme(s)
	return s
}

// blockedValkey is the resource this whole package exists for: a CR whose
// StatefulSet write is rejected on every pass, so its phase is Error, its
// ReconcileBlocked condition is True, and the newest generation any condition
// reports is one behind metadata.generation.
func blockedValkey() *vkov1.Valkey {
	return &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "stuck",
			Namespace:  "gitlab",
			Generation: 2,
		},
		Spec: vkov1.ValkeySpec{Replicas: 3},
		Status: vkov1.ValkeyStatus{
			Phase:           vkov1.ValkeyPhaseError,
			ReadyReplicas:   3,
			OperatorVersion: "1.9.6",
			Conditions: []metav1.Condition{
				{
					Type:               "Ready",
					Status:             metav1.ConditionTrue,
					Reason:             "HAClusterReady",
					ObservedGeneration: 1,
				},
			},
		},
	}
}

func healthyValkey() *vkov1.Valkey {
	return &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "fine",
			Namespace:  "harbor",
			Generation: 4,
		},
		Spec: vkov1.ValkeySpec{Replicas: 3},
		Status: vkov1.ValkeyStatus{
			Phase:           vkov1.ValkeyPhaseOK,
			ReadyReplicas:   3,
			OperatorVersion: "1.10.48",
			Conditions: []metav1.Condition{
				{
					Type:               "Ready",
					Status:             metav1.ConditionTrue,
					Reason:             "HAClusterReady",
					ObservedGeneration: 4,
				},
			},
		},
	}
}

func collectorOver(objs ...client.Object) *ValkeyCollector {
	return NewValkeyCollector(
		fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(objs...).Build(),
		testOperatorVersion, testOperatorCommit)
}

func TestCollector_ReportsTheBlockedResource(t *testing.T) {
	c := collectorOver(blockedValkey())

	expected := `
# HELP vko_valkey_status_phase The phase the operator last recorded for this Valkey resource, as a label. Exactly one series exists per resource and it carries the current phase.
# TYPE vko_valkey_status_phase gauge
vko_valkey_status_phase{name="stuck",namespace="gitlab",phase="Error"} 1
`
	require.NoError(t, testutil.CollectAndCompare(c, strings.NewReader(expected), metricPhase))
}

// The generation pair is the signal that would have caught the four-month
// outage: the spec moved to generation 2, no condition ever reported observing
// it. The alert subtracts the two, so both series must exist for the same
// resource with the same labels.
func TestCollector_GenerationPairExposesAnUnobservedSpec(t *testing.T) {
	c := collectorOver(blockedValkey())

	expected := `
# HELP vko_valkey_metadata_generation metadata.generation of the Valkey resource: the spec version the API server holds.
# TYPE vko_valkey_metadata_generation gauge
vko_valkey_metadata_generation{name="stuck",namespace="gitlab"} 2
# HELP vko_valkey_status_observed_generation The newest generation any status condition reports as observed. Lower than vko_valkey_metadata_generation means the operator has not finished evaluating the current spec.
# TYPE vko_valkey_status_observed_generation gauge
vko_valkey_status_observed_generation{name="stuck",namespace="gitlab"} 1
`
	require.NoError(t, testutil.CollectAndCompare(c, strings.NewReader(expected),
		metricGeneration, metricObservedGeneration))
}

func TestCollector_ReportsEveryConditionWithItsReason(t *testing.T) {
	v := blockedValkey()
	v.Status.Conditions = append(v.Status.Conditions, metav1.Condition{
		Type:               vkov1.ConditionTypeReconcileBlocked,
		Status:             metav1.ConditionTrue,
		Reason:             vkov1.ReasonWriteFailed,
		ObservedGeneration: 2,
	})

	expected := `
# HELP vko_valkey_status_condition One series per status condition of a Valkey resource, carrying the condition type, its current status and the reason behind it.
# TYPE vko_valkey_status_condition gauge
vko_valkey_status_condition{condition="ReconcileBlocked",name="stuck",namespace="gitlab",reason="WriteFailed",status="True"} 1
vko_valkey_status_condition{condition="Ready",name="stuck",namespace="gitlab",reason="HAClusterReady",status="True"} 1
`
	require.NoError(t, testutil.CollectAndCompare(collectorOver(v),
		strings.NewReader(expected), metricCondition))
}

// A ReconcileBlocked condition stamped with the current generation must not make
// the resource look converged: the maximum is taken across conditions, so the
// blocked condition raises observed_generation to 2 and only the phase and the
// condition itself carry the failure.
func TestCollector_ObservedGenerationTakesTheNewestCondition(t *testing.T) {
	v := blockedValkey()
	v.Status.Conditions = append(v.Status.Conditions, metav1.Condition{
		Type:               vkov1.ConditionTypeReconcileBlocked,
		Status:             metav1.ConditionTrue,
		Reason:             vkov1.ReasonWriteFailed,
		ObservedGeneration: 2,
	})

	expected := `
# HELP vko_valkey_status_observed_generation The newest generation any status condition reports as observed. Lower than vko_valkey_metadata_generation means the operator has not finished evaluating the current spec.
# TYPE vko_valkey_status_observed_generation gauge
vko_valkey_status_observed_generation{name="stuck",namespace="gitlab"} 2
`
	require.NoError(t, testutil.CollectAndCompare(collectorOver(v),
		strings.NewReader(expected), metricObservedGeneration))
}

func TestCollector_ReportsReplicasAndOperatorVersion(t *testing.T) {
	v := blockedValkey()
	v.Status.ReadyReplicas = 2

	expected := `
# HELP vko_valkey_operator_version_info The operator version that last wrote status on this Valkey resource, as a label. A version behind the fleet marks a resource the current operator never converged.
# TYPE vko_valkey_operator_version_info gauge
vko_valkey_operator_version_info{name="stuck",namespace="gitlab",version="1.9.6"} 1
# HELP vko_valkey_spec_replicas Valkey data instances requested by spec.replicas.
# TYPE vko_valkey_spec_replicas gauge
vko_valkey_spec_replicas{name="stuck",namespace="gitlab"} 3
# HELP vko_valkey_status_ready_replicas Ready Valkey data instances the operator last observed.
# TYPE vko_valkey_status_ready_replicas gauge
vko_valkey_status_ready_replicas{name="stuck",namespace="gitlab"} 2
`
	require.NoError(t, testutil.CollectAndCompare(collectorOver(v), strings.NewReader(expected),
		metricReadyReplicas, metricSpecReplicas, metricOperatorVersionInfo))
}

func TestCollector_CoversEveryResourceInEveryNamespace(t *testing.T) {
	c := collectorOver(blockedValkey(), healthyValkey())

	assert.Equal(t, 2, testutil.CollectAndCount(c, metricPhase),
		"one phase series per resource, across namespaces")
	assert.Equal(t, 2, testutil.CollectAndCount(c, metricGeneration))
}

// A resource that disappears must take its series with it. This is the property
// that makes the collect-time model worth its cost: nothing has to remember to
// delete anything.
func TestCollector_DroppedResourceLeavesNoSeries(t *testing.T) {
	stuck := blockedValkey()
	c := collectorOver(stuck, healthyValkey())
	require.Equal(t, 2, testutil.CollectAndCount(c, metricPhase))

	// Rebuild the reader without the blocked resource, as a delete would.
	c = collectorOver(healthyValkey())

	expected := `
# HELP vko_valkey_status_phase The phase the operator last recorded for this Valkey resource, as a label. Exactly one series exists per resource and it carries the current phase.
# TYPE vko_valkey_status_phase gauge
vko_valkey_status_phase{name="fine",namespace="harbor",phase="OK"} 1
`
	require.NoError(t, testutil.CollectAndCompare(c, strings.NewReader(expected), metricPhase))
}

func TestCollector_SuccessIsOneOnAGoodScrape(t *testing.T) {
	expected := `
# HELP vko_valkey_collector_success 1 when the last scrape listed the Valkey resources successfully, 0 when it failed. Guard alerts with this so a failing collector does not read as a healthy fleet.
# TYPE vko_valkey_collector_success gauge
vko_valkey_collector_success 1
`
	require.NoError(t, testutil.CollectAndCompare(collectorOver(blockedValkey()),
		strings.NewReader(expected), metricCollectorSuccess))
}

// A failed list must not read as an empty, healthy fleet. It reports success 0
// and emits no per-resource series at all, so an alert guarded on success stops
// evaluating instead of silently clearing.
func TestCollector_FailedListReportsZeroAndEmitsNothingElse(t *testing.T) {
	failing := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(blockedValkey()).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList,
				_ ...client.ListOption) error {
				return errors.New("cache not started")
			},
		}).
		Build()
	c := NewValkeyCollector(failing, testOperatorVersion, testOperatorCommit)

	expected := `
# HELP vko_valkey_collector_success 1 when the last scrape listed the Valkey resources successfully, 0 when it failed. Guard alerts with this so a failing collector does not read as a healthy fleet.
# TYPE vko_valkey_collector_success gauge
vko_valkey_collector_success 0
`
	require.NoError(t, testutil.CollectAndCompare(c, strings.NewReader(expected), metricCollectorSuccess))
	assert.Equal(t, 0, testutil.CollectAndCount(c, metricPhase),
		"a scrape that could not list must publish no resource state")
}

func TestCollector_HandlesAResourceWithoutConditions(t *testing.T) {
	v := blockedValkey()
	v.Status.Conditions = nil

	c := collectorOver(v)
	assert.Equal(t, 0, testutil.CollectAndCount(c, metricCondition))

	expected := `
# HELP vko_valkey_status_observed_generation The newest generation any status condition reports as observed. Lower than vko_valkey_metadata_generation means the operator has not finished evaluating the current spec.
# TYPE vko_valkey_status_observed_generation gauge
vko_valkey_status_observed_generation{name="stuck",namespace="gitlab"} 0
`
	require.NoError(t, testutil.CollectAndCompare(c, strings.NewReader(expected),
		metricObservedGeneration))
}

func TestNewestObservedGeneration(t *testing.T) {
	tests := []struct {
		name       string
		conditions []metav1.Condition
		want       int64
	}{
		{name: "no conditions", conditions: nil, want: 0},
		{
			name: "single condition",
			conditions: []metav1.Condition{
				{Type: "Ready", ObservedGeneration: 3},
			},
			want: 3,
		},
		{
			name: "the newest wins regardless of order",
			conditions: []metav1.Condition{
				{Type: "Ready", ObservedGeneration: 7},
				{Type: "ReconcileBlocked", ObservedGeneration: 2},
			},
			want: 7,
		},
		{
			name: "unstamped conditions do not lower the result",
			conditions: []metav1.Condition{
				{Type: "RollingUpdatePaused"},
				{Type: "Ready", ObservedGeneration: 5},
			},
			want: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, newestObservedGeneration(tt.conditions))
		})
	}
}

func TestRegister_RejectsADuplicate(t *testing.T) {
	reader := fake.NewClientBuilder().WithScheme(testScheme()).Build()

	require.NoError(t, Register(reader, testOperatorVersion, testOperatorCommit))
	assert.Error(t, Register(reader, testOperatorVersion, testOperatorCommit),
		"a second registration must be reported, not panic through MustRegister")
}

// status.conditions is a plain list, and two entries of the same type would make
// the registry fail the entire gather — taking every other resource's series
// with it. The collector drops the duplicate instead.
func TestCollector_DuplicateConditionTypeDoesNotBreakTheScrape(t *testing.T) {
	v := blockedValkey()
	v.Status.Conditions = append(v.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             "Duplicate",
		ObservedGeneration: 1,
	})

	c := collectorOver(v)
	assert.Equal(t, 1, testutil.CollectAndCount(c, metricCondition),
		"the duplicate condition type must be dropped, not emitted twice")

	expected := `
# HELP vko_valkey_status_condition One series per status condition of a Valkey resource, carrying the condition type, its current status and the reason behind it.
# TYPE vko_valkey_status_condition gauge
vko_valkey_status_condition{condition="Ready",name="stuck",namespace="gitlab",reason="HAClusterReady",status="True"} 1
`
	require.NoError(t, testutil.CollectAndCompare(c, strings.NewReader(expected), metricCondition),
		"the first entry per type wins")
}

// Build info is emitted before the list, so it is present even on a scrape that
// could not read the resources — an alert joining resource versions against it
// must not lose its right-hand side exactly when the operator is in trouble.
func TestCollector_BuildInfoIsEmittedEvenWhenTheListFails(t *testing.T) {
	failing := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList,
				_ ...client.ListOption) error {
				return errors.New("cache not started")
			},
		}).
		Build()

	expected := `
# HELP vko_operator_build_info Build information of the running operator, as labels. Join against vko_valkey_operator_version_info to find resources the current operator has not reconciled since it started.
# TYPE vko_operator_build_info gauge
vko_operator_build_info{commit="abc1234",version="1.11.0"} 1
`
	require.NoError(t, testutil.CollectAndCompare(
		NewValkeyCollector(failing, testOperatorVersion, testOperatorCommit),
		strings.NewReader(expected), metricBuildInfo))
}
