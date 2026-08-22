package sidecar

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// freeLoopbackAddr reserves and releases a loopback port, returning its address.
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

func TestHealthServer_ServesUntilShutdown(t *testing.T) {
	addr := freeLoopbackAddr(t)
	h := NewHealthServer(addr)

	served := make(chan error, 1)
	go func() { served <- h.ListenAndServe() }()

	// Wait until the listener is actually up.
	var resp *http.Response
	var err error
	for i := 0; i < 50; i++ {
		resp, err = http.Get("http://" + addr + "/healthz")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, h.Shutdown(ctx))

	// A graceful shutdown must not surface as an error to the caller.
	select {
	case err := <-served:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("ListenAndServe did not return after Shutdown")
	}
}

func TestHealthServer_ListenAndServeReportsBindFailure(t *testing.T) {
	h := NewHealthServer("127.0.0.1:not-a-port")

	assert.Error(t, h.ListenAndServe())
}
