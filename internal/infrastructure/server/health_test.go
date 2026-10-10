package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// @spec components/infrastructure/backend/process/structured-logging "Normal shutdown"
func TestHealthServer_StartReturnsNilOnClose(t *testing.T) {
	t.Parallel()

	// The event consumer logs a non-nil Start error at ERROR. Close is how
	// every rollout stops the server, so it must not surface as an error.
	h := server.NewHealthServer("127.0.0.1:0")
	done := make(chan error, 1)
	go func() { done <- h.Start() }()

	require.NoError(t, h.Close())

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after Close")
	}
}
