package server

import (
	"net/http"

	connectcors "connectrpc.com/cors"
	"github.com/rs/cors"
)

// NewCORSHandler creates a new CORS middleware using connectrpc helpers.
func NewCORSHandler(mu http.Handler, allowedOrigins []string) http.Handler {
	return cors.New(GetCorsOptions(allowedOrigins)).Handler(mu)
}

// preflightMaxAgeSeconds is how long a browser may reuse a preflight answer.
// Without it Chrome keeps one for 5 s, so every RPC method's first call in a
// page load (each method is its own URL) paid an extra OPTIONS round trip.
// Chrome caps the value at 2 h.
const preflightMaxAgeSeconds = 2 * 60 * 60

// GetCorsOptions returns the rs/cors Options used by the handler.
func GetCorsOptions(allowedOrigins []string) cors.Options {
	allowedHeaders := append(connectcors.AllowedHeaders(), "Authorization", "Traceparent", "Tracestate")
	return cors.Options{
		AllowedOrigins: allowedOrigins,
		AllowedMethods: connectcors.AllowedMethods(),
		AllowedHeaders: allowedHeaders,
		ExposedHeaders: connectcors.ExposedHeaders(),
		MaxAge:         preflightMaxAgeSeconds,
	}
}
