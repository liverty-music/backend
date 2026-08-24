package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDebugServer_ServesGoroutineLeakProfile confirms the internal pprof
// listener exposes the Go 1.27 goroutineleak profile (and the pprof index),
// so operators can pull a full profile with stacks on demand.
func TestDebugServer_ServesGoroutineLeakProfile(t *testing.T) {
	t.Parallel()

	logger, err := logging.New()
	require.NoError(t, err)

	h := server.NewDebugServer(config.GoroutineLeakConfig{Host: "127.0.0.1", Port: 6060}, logger).Handler()

	for _, path := range []string{"/debug/pprof/", "/debug/pprof/goroutineleak?debug=1"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equalf(t, http.StatusOK, rec.Code, "GET %s must be served on the debug listener", path)
	}
}

// TestDebugServer_IsIsolatedFromHealth guards the "not on a public route"
// property at the mux level: the health listener (the one probe traffic hits)
// must not serve any pprof path. Combined with binding to a dedicated,
// non-ingress, loopback port, this keeps the profiling surface off public routes.
func TestDebugServer_IsIsolatedFromHealth(t *testing.T) {
	t.Parallel()

	health := server.NewHealthServer(":0").Handler()

	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/goroutineleak", nil)
	rec := httptest.NewRecorder()
	health.ServeHTTP(rec, req)

	assert.NotEqual(t, http.StatusOK, rec.Code, "health server must not serve pprof; it belongs on the isolated debug listener")
	assert.NotContains(t, rec.Body.String(), "goroutine profile", "health server must not leak a pprof body")
}
