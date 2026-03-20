package observer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestObserver(ready bool, checks map[string]bool, msg string) *Observer {
	obs := &Observer{
		cfg: Config{
			Namespace:   "default",
			ClusterName: "test",
		},
		result: CheckResult{
			Ready:     ready,
			Checks:    checks,
			Message:   msg,
			LastCheck: time.Now(),
		},
		metrics: newObserverMetrics(),
	}
	return obs
}

func TestHealthServer_Healthz(t *testing.T) {
	obs := newTestObserver(false, nil, "")
	srv := NewHealthServer(":0", obs)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"ok"`)
}

func TestHealthServer_Readyz_Healthy(t *testing.T) {
	obs := newTestObserver(true, map[string]bool{
		"master_reachable": true,
		"write_test":       true,
		"read_test":        true,
	}, "")
	srv := NewHealthServer(":0", obs)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result CheckResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.True(t, result.Ready)
	assert.True(t, result.Checks["master_reachable"])
	assert.True(t, result.Checks["write_test"])
}

func TestHealthServer_Readyz_Unhealthy(t *testing.T) {
	obs := newTestObserver(false, map[string]bool{
		"master_reachable": false,
	}, "master unreachable")
	srv := NewHealthServer(":0", obs)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var result CheckResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.False(t, result.Ready)
	assert.Equal(t, "master unreachable", result.Message)
}

func TestHealthServer_Metrics(t *testing.T) {
	obs := newTestObserver(true, nil, "")
	srv := NewHealthServer(":0", obs)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)

	// Prometheus metrics handler returns 200 and text content.
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/plain")
}

func TestHealthServer_Readyz_ContentType(t *testing.T) {
	obs := newTestObserver(true, map[string]bool{}, "")
	srv := NewHealthServer(":0", obs)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)

	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
}

func TestHealthServer_Readyz_SentinelHostnameChecks(t *testing.T) {
	obs := newTestObserver(true, map[string]bool{
		"master_reachable":           true,
		"sentinel_master_hostname":   true,
		"sentinel_replica_hostnames": true,
	}, "")
	srv := NewHealthServer(":0", obs)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result CheckResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.True(t, result.Ready)
	assert.True(t, result.Checks["sentinel_master_hostname"])
	assert.True(t, result.Checks["sentinel_replica_hostnames"])
}
