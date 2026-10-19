package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/stretchr/testify/assert"
)

func TestGetCorsOptions(t *testing.T) {
	allowedOrigins := []string{"http://localhost:9000", "https://liverty.music"}
	options := server.GetCorsOptions(allowedOrigins)

	assert.Equal(t, allowedOrigins, options.AllowedOrigins)
	assert.Contains(t, options.AllowedMethods, http.MethodPost)
	assert.Contains(t, options.AllowedHeaders, "Connect-Protocol-Version")
	assert.Contains(t, options.AllowedHeaders, "Authorization")
	assert.Contains(t, options.AllowedHeaders, "Traceparent")
	assert.Contains(t, options.AllowedHeaders, "Tracestate")
	assert.Contains(t, options.ExposedHeaders, "Grpc-Status")
}

func TestNewCORSHandler(t *testing.T) {
	allowedOrigins := []string{"http://localhost:1234"}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	corsHandler := server.NewCORSHandler(handler, allowedOrigins)
	assert.NotNil(t, corsHandler)
}

func TestNewCORSHandler_PreflightIsCacheable(t *testing.T) {
	t.Parallel()
	handler := server.NewCORSHandler(http.NotFoundHandler(), []string{"https://liverty-music.app"})
	req := httptest.NewRequest(http.MethodOptions, "/liverty_music.rpc.artist.v1.ArtistService/ListSimilar", nil)
	req.Header.Set("Origin", "https://liverty-music.app")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	// Browsers send the list lowercase and sorted, which rs/cors expects.
	req.Header.Set("Access-Control-Request-Headers", "authorization,connect-protocol-version,content-type")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, "https://liverty-music.app", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "7200", rec.Header().Get("Access-Control-Max-Age"))
}
