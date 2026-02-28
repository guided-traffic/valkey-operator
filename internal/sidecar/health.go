package sidecar

import (
	"context"
	"net/http"
	"sync/atomic"
)

// HealthServer exposes a readiness endpoint for the sidecar.
// It returns 503 until the sidecar has successfully detected the Valkey role,
// then returns 200 on /readyz.
type HealthServer struct {
	ready  atomic.Bool
	server *http.Server
}

// NewHealthServer creates a new health server listening on the given address.
func NewHealthServer(addr string) *HealthServer {
	h := &HealthServer{}

	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", h.handleReadyz)
	mux.HandleFunc("/healthz", h.handleHealthz)

	h.server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	return h
}

// SetReady marks the sidecar as ready (role has been detected).
func (h *HealthServer) SetReady() {
	h.ready.Store(true)
}

// IsReady reports whether the sidecar is ready.
func (h *HealthServer) IsReady() bool {
	return h.ready.Load()
}

// ListenAndServe starts the HTTP server. Blocks until the server is shut down.
func (h *HealthServer) ListenAndServe() error {
	err := h.server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Shutdown gracefully shuts down the server.
func (h *HealthServer) Shutdown(ctx context.Context) error {
	return h.server.Shutdown(ctx)
}

// handleReadyz returns 200 if the sidecar has detected the Valkey role, 503 otherwise.
func (h *HealthServer) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if h.ready.Load() {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("not ready"))
}

// handleHealthz always returns 200 (the sidecar process is alive).
func (h *HealthServer) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("alive"))
}
