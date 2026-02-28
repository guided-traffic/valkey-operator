package sidecar

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHealthServer_NotReadyByDefault(t *testing.T) {
	h := NewHealthServer(":0")

	assert.False(t, h.IsReady())
}

func TestHealthServer_SetReady(t *testing.T) {
	h := NewHealthServer(":0")

	h.SetReady()
	assert.True(t, h.IsReady())
}

func TestHealthServer_ReadyzNotReady(t *testing.T) {
	h := NewHealthServer(":0")

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	h.handleReadyz(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "not ready", w.Body.String())
}

func TestHealthServer_ReadyzReady(t *testing.T) {
	h := NewHealthServer(":0")
	h.SetReady()

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	h.handleReadyz(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "ready", w.Body.String())
}

func TestHealthServer_HealthzAlwaysOK(t *testing.T) {
	h := NewHealthServer(":0")

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	h.handleHealthz(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "alive", w.Body.String())
}

func TestHealthServer_HealthzAlwaysOKEvenWhenNotReady(t *testing.T) {
	h := NewHealthServer(":0")
	// Explicitly not setting ready.

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	h.handleHealthz(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}
