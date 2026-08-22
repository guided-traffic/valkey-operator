// Package metrics exports the state of every Valkey resource as Prometheus
// series on the operator's own metrics endpoint.
//
// Why this exists at all: until it did, the operator's only alertable signal was
// controller-runtime's `controller_runtime_reconcile_errors_total`, whose label
// set is `{controller}` — it can say that the Valkey controller is failing, never
// which resource. A CR whose StatefulSet write was rejected on every pass
// therefore sat in phase Error for four months without anything firing. Every
// series below carries namespace and name so an alert can name the resource, and
// the two generation gauges make "the operator never evaluated this spec"
// expressible in PromQL.
package metrics

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// listTimeout bounds the cache read a scrape performs. The read is served from
// the manager's informer cache and returns immediately once that cache is
// synced; the bound exists for the window before it is, where the cached client
// blocks rather than failing.
const listTimeout = 5 * time.Second

// Metric names. The vko_ prefix keeps them clear of controller-runtime's
// built-ins and of the valkey_observer_* series the separate observer process
// exports on its own port.
const (
	metricPhase               = "vko_valkey_status_phase"
	metricCondition           = "vko_valkey_status_condition"
	metricReadyReplicas       = "vko_valkey_status_ready_replicas"
	metricSpecReplicas        = "vko_valkey_spec_replicas"
	metricGeneration          = "vko_valkey_metadata_generation"
	metricObservedGeneration  = "vko_valkey_status_observed_generation"
	metricOperatorVersionInfo = "vko_valkey_operator_version_info"
	metricCollectorSuccess    = "vko_valkey_collector_success"
	metricBuildInfo           = "vko_operator_build_info"
)

// Label names shared by the per-resource series. They are the join keys every
// alert in the shipped PrometheusRule aggregates on, so they are named once.
const (
	labelNamespace = "namespace"
	labelName      = "name"
	labelVersion   = "version"
)

// ValkeyCollector reports the state of every Valkey resource in the cluster.
//
// It is a collect-time collector rather than a set of gauges written during
// reconcile, and that is the load-bearing choice: the series are rebuilt from
// the cache on every scrape, so a deleted resource stops producing series
// without any deletion bookkeeping, and a phase or condition that changed leaves
// no stale predecessor behind. A GaugeVec written from the reconciler would need
// a delete path for every resource that disappears — including the ones that
// disappear while the operator is not running — and getting that wrong keeps an
// alert firing for a cluster that no longer exists.
//
// The reader is the manager's cached client, so a scrape costs no API request.
type ValkeyCollector struct {
	reader  client.Reader
	version string
	commit  string

	phase              *prometheus.Desc
	condition          *prometheus.Desc
	readyReplicas      *prometheus.Desc
	specReplicas       *prometheus.Desc
	generation         *prometheus.Desc
	observedGeneration *prometheus.Desc
	operatorVersion    *prometheus.Desc
	success            *prometheus.Desc
	buildInfo          *prometheus.Desc
}

// NewValkeyCollector builds a collector over the given reader. version and
// commit describe the running operator and are published as build info, which is
// what makes "this resource was last reconciled by an older operator" a join
// rather than a guess about which version the fleet is supposed to be on.
func NewValkeyCollector(reader client.Reader, version, commit string) *ValkeyCollector {
	// Written out per descriptor rather than appended to a shared slice: the
	// label names are part of each metric's identity, and a shared backing array
	// is the classic way to corrupt one of them from an edit to another.
	return &ValkeyCollector{
		reader:  reader,
		version: version,
		commit:  commit,
		phase: prometheus.NewDesc(metricPhase,
			"The phase the operator last recorded for this Valkey resource, as a label. "+
				"Exactly one series exists per resource and it carries the current phase.",
			[]string{labelNamespace, labelName, "phase"}, nil),
		condition: prometheus.NewDesc(metricCondition,
			"One series per status condition of a Valkey resource, carrying the condition "+
				"type, its current status and the reason behind it.",
			[]string{labelNamespace, labelName, "condition", "status", "reason"}, nil),
		readyReplicas: prometheus.NewDesc(metricReadyReplicas,
			"Ready Valkey data instances the operator last observed.",
			[]string{labelNamespace, labelName}, nil),
		specReplicas: prometheus.NewDesc(metricSpecReplicas,
			"Valkey data instances requested by spec.replicas.",
			[]string{labelNamespace, labelName}, nil),
		generation: prometheus.NewDesc(metricGeneration,
			"metadata.generation of the Valkey resource: the spec version the API server holds.",
			[]string{labelNamespace, labelName}, nil),
		observedGeneration: prometheus.NewDesc(metricObservedGeneration,
			"The newest generation any status condition reports as observed. Lower than "+
				"vko_valkey_metadata_generation means the operator has not finished evaluating "+
				"the current spec.",
			[]string{labelNamespace, labelName}, nil),
		operatorVersion: prometheus.NewDesc(metricOperatorVersionInfo,
			"The operator version that last wrote status on this Valkey resource, as a label. "+
				"A version behind the fleet marks a resource the current operator never converged.",
			[]string{labelNamespace, labelName, labelVersion}, nil),
		buildInfo: prometheus.NewDesc(metricBuildInfo,
			"Build information of the running operator, as labels. Join against "+
				"vko_valkey_operator_version_info to find resources the current operator "+
				"has not reconciled since it started.",
			[]string{labelVersion, "commit"}, nil),
		success: prometheus.NewDesc(metricCollectorSuccess,
			"1 when the last scrape listed the Valkey resources successfully, 0 when it failed. "+
				"Guard alerts with this so a failing collector does not read as a healthy fleet.",
			nil, nil),
	}
}

// Describe implements prometheus.Collector. Every descriptor is announced, which
// makes this a checked collector: the registry rejects a Collect that emits a
// descriptor Describe never mentioned. Label *values* still vary per scrape —
// only the label names are fixed here — so nothing about the varying phase,
// status or reason labels needs the collector to stay unchecked.
func (c *ValkeyCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{
		c.phase, c.condition, c.readyReplicas, c.specReplicas,
		c.generation, c.observedGeneration, c.operatorVersion, c.buildInfo, c.success,
	} {
		ch <- desc
	}
}

// Collect implements prometheus.Collector. It lists the Valkey resources from
// the cache and emits one set of series per resource.
//
// A failed list emits vko_valkey_collector_success 0 and nothing else, rather
// than emitting a partial or empty fleet: an alert that reads "no resource is in
// phase Error" from a collector that could not look is worse than no alert.
func (c *ValkeyCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(c.buildInfo, prometheus.GaugeValue, 1,
		c.version, c.commit)

	ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
	defer cancel()

	var list vkov1.ValkeyList
	if err := c.reader.List(ctx, &list); err != nil {
		ch <- prometheus.MustNewConstMetric(c.success, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.success, prometheus.GaugeValue, 1)

	for i := range list.Items {
		c.collectOne(ch, &list.Items[i])
	}
}

// collectOne emits the series of a single Valkey resource.
func (c *ValkeyCollector) collectOne(ch chan<- prometheus.Metric, v *vkov1.Valkey) {
	namespace, name := v.Namespace, v.Name

	ch <- prometheus.MustNewConstMetric(c.phase, prometheus.GaugeValue, 1,
		namespace, name, string(v.Status.Phase))

	// Deduplicated by condition type. Two series with identical labels in one
	// scrape make the registry fail the whole gather, which would take every
	// other resource's state down with it — and status.conditions is a plain
	// list that only convention keeps unique. The cost of being wrong here is
	// the entire metrics endpoint, so the first entry per type wins and the
	// duplicate is dropped.
	seen := make(map[string]struct{}, len(v.Status.Conditions))
	for _, condition := range v.Status.Conditions {
		if _, duplicate := seen[condition.Type]; duplicate {
			continue
		}
		seen[condition.Type] = struct{}{}
		ch <- prometheus.MustNewConstMetric(c.condition, prometheus.GaugeValue, 1,
			namespace, name, condition.Type, string(condition.Status), condition.Reason)
	}

	ch <- prometheus.MustNewConstMetric(c.readyReplicas, prometheus.GaugeValue,
		float64(v.Status.ReadyReplicas), namespace, name)
	ch <- prometheus.MustNewConstMetric(c.specReplicas, prometheus.GaugeValue,
		float64(v.Spec.Replicas), namespace, name)
	ch <- prometheus.MustNewConstMetric(c.generation, prometheus.GaugeValue,
		float64(v.Generation), namespace, name)
	ch <- prometheus.MustNewConstMetric(c.observedGeneration, prometheus.GaugeValue,
		float64(newestObservedGeneration(v.Status.Conditions)), namespace, name)

	ch <- prometheus.MustNewConstMetric(c.operatorVersion, prometheus.GaugeValue, 1,
		namespace, name, v.Status.OperatorVersion)
}

// newestObservedGeneration returns the highest observedGeneration stamped on any
// condition, or 0 when there are no conditions.
//
// The maximum rather than one designated condition: conditions are written by
// different code paths and a resource that is blocked carries a fresh
// ReconcileBlocked next to a Ready left over from the generation before. Taking
// the newest answers the question the metric is for — the newest spec version
// the operator has evaluated at all — while any single condition would report a
// resource as stale that merely has one condition nobody rewrote.
func newestObservedGeneration(conditions []metav1.Condition) int64 {
	var newest int64
	for _, condition := range conditions {
		if condition.ObservedGeneration > newest {
			newest = condition.ObservedGeneration
		}
	}
	return newest
}

// Register adds the collector to the controller-runtime registry, which is what
// the manager serves on --metrics-bind-address.
//
// It returns the registry error rather than panicking through MustRegister so a
// duplicate registration surfaces as a startup error with a message instead of
// as a stack trace.
func Register(reader client.Reader, version, commit string) error {
	return ctrlmetrics.Registry.Register(NewValkeyCollector(reader, version, commit))
}
