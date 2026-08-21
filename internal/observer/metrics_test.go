package observer

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The metric names below are a public contract: dashboards and alerts are wired
// to them, so the tests assert on the rendered exposition format rather than on
// the Go objects.
var observerMetricNames = []string{
	"valkey_observer_master_reachable",
	"valkey_observer_replica_sync_ok",
	"valkey_observer_write_test_ok",
	"valkey_observer_read_test_ok",
	"valkey_observer_replica_read_test_ok",
	"valkey_observer_sentinel_reachable",
	"valkey_observer_sentinel_quorum_ok",
	"valkey_observer_sentinel_flags_ok",
	"valkey_observer_sentinel_master_hostname_ok",
	"valkey_observer_sentinel_replica_hostnames_ok",
	"valkey_observer_healthy",
	"valkey_observer_check_duration_seconds",
	"valkey_observer_checks_total",
	"valkey_observer_check_failures_total",
}

// registerMetrics puts a fresh metric set into its own registry, so the tests
// stay independent of the process-wide default registry that Run uses.
func registerMetrics(t *testing.T) (*observerMetrics, *prometheus.Registry) {
	t.Helper()
	m := newObserverMetrics()
	reg := prometheus.NewRegistry()
	for _, collector := range m.collectors() {
		require.NoError(t, reg.Register(collector))
	}
	return m, reg
}

// scrapeRegistry renders a registry in the Prometheus text exposition format,
// which is exactly what a scrape would see.
func scrapeRegistry(t *testing.T, reg *prometheus.Registry) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	return w.Body.String()
}

// metricValue extracts the value of an unlabelled sample from a scrape.
func metricValue(t *testing.T, scrape, name string) float64 {
	t.Helper()
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + ` (\S+)$`)
	match := re.FindStringSubmatch(scrape)
	require.NotNil(t, match, "sample %q not found in scrape:\n%s", name, scrape)
	value, err := strconv.ParseFloat(match[1], 64)
	require.NoError(t, err)
	return value
}

func TestObserverMetrics_CollectorsCoverEveryDocumentedMetric(t *testing.T) {
	m, reg := registerMetrics(t)
	assert.Len(t, m.collectors(), len(observerMetricNames))

	scrape := scrapeRegistry(t, reg)
	for _, name := range observerMetricNames {
		assert.Contains(t, scrape, "# TYPE "+name, "metric %s must be exported", name)
	}
}

func TestRecordCycle_CountsCyclesAndFailures(t *testing.T) {
	m, reg := registerMetrics(t)

	m.recordCycle(40*time.Millisecond, true)
	m.recordCycle(20*time.Millisecond, false)
	m.recordCycle(10*time.Millisecond, false)

	scrape := scrapeRegistry(t, reg)
	assert.Equal(t, 3.0, metricValue(t, scrape, "valkey_observer_checks_total"))
	assert.Equal(t, 2.0, metricValue(t, scrape, "valkey_observer_check_failures_total"),
		"only failed cycles count as failures")
	assert.Equal(t, 3.0, metricValue(t, scrape, "valkey_observer_check_duration_seconds_count"),
		"every cycle is observed in the duration histogram")
	assert.InDelta(t, 0.07, metricValue(t, scrape, "valkey_observer_check_duration_seconds_sum"), 0.001)
}

func TestUpdateGauges_ReflectsTheCheckMap(t *testing.T) {
	allChecks := map[string]bool{
		checkMasterReachable:         true,
		"replica_sync":               true,
		"write_test":                 true,
		"read_test":                  true,
		"replica_read_test":          true,
		"sentinel_reachable":         true,
		"sentinel_quorum":            true,
		"sentinel_flags":             true,
		"sentinel_master_hostname":   true,
		"sentinel_replica_hostnames": true,
	}
	gaugeNames := observerMetricNames[:10]

	t.Run("passing checks and a healthy verdict", func(t *testing.T) {
		m, reg := registerMetrics(t)

		m.updateGauges(allChecks, true)

		scrape := scrapeRegistry(t, reg)
		for _, name := range gaugeNames {
			assert.Equal(t, 1.0, metricValue(t, scrape, name), "gauge %s", name)
		}
		assert.Equal(t, 1.0, metricValue(t, scrape, "valkey_observer_healthy"))
	})

	t.Run("failing checks and an unhealthy verdict", func(t *testing.T) {
		m, reg := registerMetrics(t)
		failing := make(map[string]bool, len(allChecks))
		for name := range allChecks {
			failing[name] = false
		}

		m.updateGauges(failing, false)

		scrape := scrapeRegistry(t, reg)
		for _, name := range gaugeNames {
			assert.Equal(t, 0.0, metricValue(t, scrape, name), "gauge %s", name)
		}
		assert.Equal(t, 0.0, metricValue(t, scrape, "valkey_observer_healthy"))
	})

	// A check that did not run is absent from the map. Its gauge must keep the
	// last value it had rather than dropping to 0, which would otherwise look
	// like a failure in the dashboards for every skipped check.
	t.Run("a check that did not run keeps its previous gauge value", func(t *testing.T) {
		m, reg := registerMetrics(t)
		m.updateGauges(allChecks, true)

		m.updateGauges(map[string]bool{checkMasterReachable: true}, true)

		scrape := scrapeRegistry(t, reg)
		assert.Equal(t, 1.0, metricValue(t, scrape, "valkey_observer_replica_sync_ok"))
		assert.Equal(t, 1.0, metricValue(t, scrape, "valkey_observer_sentinel_quorum_ok"))
	})
}
