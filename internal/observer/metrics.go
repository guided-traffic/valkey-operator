package observer

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type observerMetrics struct {
	masterReachable    prometheus.Gauge
	replicaSyncOK      prometheus.Gauge
	writeTestOK        prometheus.Gauge
	readTestOK         prometheus.Gauge
	replicaReadTestOK  prometheus.Gauge
	sentinelReachable  prometheus.Gauge
	sentinelQuorumOK   prometheus.Gauge
	sentinelFlagsOK    prometheus.Gauge
	healthy            prometheus.Gauge
	checkDuration      prometheus.Histogram
	checksTotal        prometheus.Counter
	checkFailuresTotal prometheus.Counter
}

func newObserverMetrics() *observerMetrics {
	return &observerMetrics{
		masterReachable: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "valkey_observer_master_reachable",
			Help: "Whether the master is reachable (1=yes, 0=no)",
		}),
		replicaSyncOK: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "valkey_observer_replica_sync_ok",
			Help: "Whether all replicas are synchronised (1=yes, 0=no)",
		}),
		writeTestOK: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "valkey_observer_write_test_ok",
			Help: "Whether write to master succeeded (1=yes, 0=no)",
		}),
		readTestOK: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "valkey_observer_read_test_ok",
			Help: "Whether read from master succeeded (1=yes, 0=no)",
		}),
		replicaReadTestOK: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "valkey_observer_replica_read_test_ok",
			Help: "Whether read from all replicas succeeded (1=yes, 0=no)",
		}),
		sentinelReachable: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "valkey_observer_sentinel_reachable",
			Help: "Whether all sentinels are reachable (1=yes, 0=no)",
		}),
		sentinelQuorumOK: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "valkey_observer_sentinel_quorum_ok",
			Help: "Whether sentinel quorum is consistent (1=yes, 0=no)",
		}),
		sentinelFlagsOK: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "valkey_observer_sentinel_flags_ok",
			Help: "Whether sentinel master flags are clean (1=yes, 0=no)",
		}),
		healthy: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "valkey_observer_healthy",
			Help: "Whether all checks passed (1=yes, 0=no)",
		}),
		checkDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "valkey_observer_check_duration_seconds",
			Help:    "Duration of a complete check cycle",
			Buckets: prometheus.DefBuckets,
		}),
		checksTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "valkey_observer_checks_total",
			Help: "Total number of check cycles",
		}),
		checkFailuresTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "valkey_observer_check_failures_total",
			Help: "Total number of failed check cycles",
		}),
	}
}

func (m *observerMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{
		m.masterReachable,
		m.replicaSyncOK,
		m.writeTestOK,
		m.readTestOK,
		m.replicaReadTestOK,
		m.sentinelReachable,
		m.sentinelQuorumOK,
		m.sentinelFlagsOK,
		m.healthy,
		m.checkDuration,
		m.checksTotal,
		m.checkFailuresTotal,
	}
}

func (m *observerMetrics) recordCycle(duration time.Duration, ok bool) {
	m.checkDuration.Observe(duration.Seconds())
	m.checksTotal.Inc()
	if !ok {
		m.checkFailuresTotal.Inc()
	}
}

func (m *observerMetrics) updateGauges(checks map[string]bool, healthy bool) {
	setGauge := func(g prometheus.Gauge, key string) {
		v, exists := checks[key]
		if exists && v {
			g.Set(1)
		} else if exists {
			g.Set(0)
		}
	}

	setGauge(m.masterReachable, "master_reachable")
	setGauge(m.replicaSyncOK, "replica_sync")
	setGauge(m.writeTestOK, "write_test")
	setGauge(m.readTestOK, "read_test")
	setGauge(m.replicaReadTestOK, "replica_read_test")
	setGauge(m.sentinelReachable, "sentinel_reachable")
	setGauge(m.sentinelQuorumOK, "sentinel_quorum")
	setGauge(m.sentinelFlagsOK, "sentinel_flags")

	if healthy {
		m.healthy.Set(1)
	} else {
		m.healthy.Set(0)
	}
}
