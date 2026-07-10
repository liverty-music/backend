package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/stretchr/testify/assert"
)

func get(t *testing.T, h *server.HealthServer, path string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	h.Handler().ServeHTTP(rec, req)
	return rec.Code
}

func TestHealthServer_HealthzHealthyByDefault(t *testing.T) {
	t.Parallel()

	// Before a liveness probe is installed (pod still initializing), /healthz
	// reports healthy so Kubernetes does not kill a booting pod; readiness
	// gates traffic instead.
	h := server.NewHealthServer(":0")

	assert.Equal(t, http.StatusOK, get(t, h, "/healthz"))
}

func TestHealthServer_HealthzReflectsLiveness(t *testing.T) {
	t.Parallel()

	h := server.NewHealthServer(":0")

	live := true
	h.SetLiveness(func() bool { return live })
	assert.Equal(t, http.StatusOK, get(t, h, "/healthz"), "consuming pod should be healthy")

	// A wedged consumer (router stopped / durable unbound / connection down)
	// makes the probe return false, so /healthz returns 503 and Kubernetes
	// restarts the pod.
	live = false
	assert.Equal(t, http.StatusServiceUnavailable, get(t, h, "/healthz"), "wedged pod should be unhealthy")
}

func TestHealthServer_ReadyzGatedUntilReady(t *testing.T) {
	t.Parallel()

	h := server.NewHealthServer(":0")

	assert.Equal(t, http.StatusServiceUnavailable, get(t, h, "/readyz"), "not ready before SetReady")

	h.SetReady()
	assert.Equal(t, http.StatusOK, get(t, h, "/readyz"), "ready after SetReady")

	h.SetShuttingDown()
	assert.Equal(t, http.StatusServiceUnavailable, get(t, h, "/readyz"), "not ready while shutting down")
}
