package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetCorsOptions(t *testing.T) {
	allowedOrigins := []string{"http://localhost:9000", "https://liverty.music"}
	options := GetCorsOptions(allowedOrigins)

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

	corsHandler := NewCORSHandler(handler, allowedOrigins)
	assert.NotNil(t, corsHandler)
}
