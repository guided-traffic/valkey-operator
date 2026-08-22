//go:build integration

package integration

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vkov1 "github.com/guided-traffic/valkey-operator/api/v1"
)

// scrapeMetrics reads the manager's metrics endpoint the way Prometheus would.
func scrapeMetrics(t *testing.T) string {
	t.Helper()

	resp, err := http.Get(metricsURL)
	require.NoError(t, err, "the manager should serve %s", metricsURL)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

// eventuallyScrapeContains polls the endpoint until it contains want.
func eventuallyScrapeContains(t *testing.T, want, msg string) {
	t.Helper()
	require.Eventually(t, func() bool {
		return strings.Contains(scrapeMetrics(t), want)
	}, 15*time.Second, 500*time.Millisecond, msg)
}

// TestMetricsEndpoint_Integration is the only tier that can prove the metrics
// wiring: that the collector registered in TestMain reaches the manager's own
// HTTP endpoint, that it reads a cache a real API server populates, and that a
// deleted resource takes its series with it.
//
// The unit tests build the collector by hand over a fake client, so none of that
// is covered there — a collector that is never registered, or registered into a
// registry the manager does not serve, passes every unit test in the package.
func TestMetricsEndpoint_Integration(t *testing.T) {
	ctx := testCtx

	t.Run("build info identifies the running operator", func(t *testing.T) {
		want := fmt.Sprintf(`vko_operator_build_info{commit="%s",version="%s"} 1`,
			testOperatorCommit, testOperatorVersion)
		eventuallyScrapeContains(t, want, "the endpoint should publish the operator build info")
	})

	t.Run("the collector reports a successful read", func(t *testing.T) {
		eventuallyScrapeContains(t, "vko_valkey_collector_success 1",
			"the cache-backed list should succeed once the manager is running")
	})

	v := &vkov1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: "metrics-test", Namespace: "default"},
		Spec: vkov1.ValkeySpec{
			Replicas: 3,
			Image:    "valkey/valkey:8.0",
		},
	}
	require.NoError(t, k8sClient.Create(ctx, v))

	t.Run("a resource appears with namespace and name", func(t *testing.T) {
		eventuallyScrapeContains(t,
			`vko_valkey_spec_replicas{name="metrics-test",namespace="default"} 3`,
			"spec.replicas should be exported per resource")

		body := scrapeMetrics(t)
		assert.Contains(t, body,
			`vko_valkey_metadata_generation{name="metrics-test",namespace="default"} 1`,
			"metadata.generation comes from the API server, not from the spec")
		assert.Contains(t, body,
			`vko_valkey_status_observed_generation{name="metrics-test",namespace="default"}`,
			"the observed-generation counterpart must exist for the alert to subtract them")
	})

	t.Run("the phase the operator recorded is exported as a label", func(t *testing.T) {
		// A NON-EMPTY phase, which only the operator can produce: the series
		// itself exists from the first scrape with phase="" and asserting only its
		// presence would pass without the controller ever running. Which phase
		// does not matter — no kubelet runs in envtest, so the cluster never
		// reaches OK.
		var seen string
		require.Eventually(t, func() bool {
			for _, line := range strings.Split(scrapeMetrics(t), "\n") {
				if !strings.HasPrefix(line, "vko_valkey_status_phase{") ||
					!strings.Contains(line, `name="metrics-test"`) ||
					strings.Contains(line, `phase=""`) {
					continue
				}
				seen = line
				return true
			}
			return false
		}, 30*time.Second, 500*time.Millisecond,
			"the operator should record a phase and the collector should export it")
		t.Logf("phase series: %s", seen)

		// A condition series must appear too, otherwise the ReconcileBlocked
		// alert has nothing to match on. Which condition is not pinned: the
		// operator writes them as the pass progresses, and this test must not
		// encode the order in which it happens to do so.
		var condition string
		require.Eventually(t, func() bool {
			for _, line := range strings.Split(scrapeMetrics(t), "\n") {
				if strings.HasPrefix(line, "vko_valkey_status_condition{") &&
					strings.Contains(line, `name="metrics-test"`) {
					condition = line
					return true
				}
			}
			return false
		}, 30*time.Second, 500*time.Millisecond,
			"conditions the operator writes should be exported per resource")
		t.Logf("condition series: %s", condition)
	})

	t.Run("deleting the resource removes its series", func(t *testing.T) {
		require.NoError(t, k8sClient.Delete(ctx, v))

		require.Eventually(t, func() bool {
			return !strings.Contains(scrapeMetrics(t), `name="metrics-test"`)
		}, 30*time.Second, 500*time.Millisecond,
			"no series may outlive the resource: the collector rebuilds from the cache")
	})
}
