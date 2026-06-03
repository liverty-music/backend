package httpx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/httpx"
	"github.com/stretchr/testify/assert"
)

func TestLivenessChecker_IsDeadLink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   int
		wantDead bool
	}{
		{"200 OK is alive", http.StatusOK, false},
		{"301 redirect resolves to alive", http.StatusMovedPermanently, false},
		{"404 Not Found is dead", http.StatusNotFound, true},
		{"410 Gone is dead", http.StatusGone, true},
		{"400 Bad Request is dead", http.StatusBadRequest, true},
		{"401 auth wall is ambiguous, alive", http.StatusUnauthorized, false},
		{"403 bot-block is ambiguous, alive", http.StatusForbidden, false},
		{"429 rate-limit is ambiguous, alive", http.StatusTooManyRequests, false},
		{"500 server error is transient, alive", http.StatusInternalServerError, false},
		{"503 unavailable is transient, alive", http.StatusServiceUnavailable, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			checker := httpx.NewLivenessChecker(srv.Client(), nil)
			got := checker.IsDeadLink(context.Background(), srv.URL)
			assert.Equal(t, tt.wantDead, got)
		})
	}
}

func TestLivenessChecker_HardTransportFailureIsDead(t *testing.T) {
	t.Parallel()

	// A host that does not resolve is a hard, non-timeout failure → dead.
	checker := httpx.NewLivenessChecker(nil, nil)
	got := checker.IsDeadLink(context.Background(), "https://nonexistent.invalid.liverty-music-test/goods")
	assert.True(t, got)
}

func TestLivenessChecker_MalformedURLIsNotDead(t *testing.T) {
	t.Parallel()

	// A malformed URL is handled by the validation path, not by liveness; the
	// checker must not report it as a dead link.
	checker := httpx.NewLivenessChecker(nil, nil)
	got := checker.IsDeadLink(context.Background(), "://missing-scheme")
	assert.False(t, got)
}

func TestLivenessChecker_SSRFGuardBlocksLoopback(t *testing.T) {
	t.Parallel()

	// httptest listens on loopback. The nil-client constructor selects the
	// SSRF-hardened production client, whose dialer refuses to connect to a
	// non-public address — so the probe fails at connect (a hard, non-timeout
	// failure) and the URL is classified dead. This proves the guard fires
	// before any request reaches an internal address.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	checker := httpx.NewLivenessChecker(nil, nil)
	assert.True(t, checker.IsDeadLink(context.Background(), srv.URL))
}
