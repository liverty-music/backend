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

// GetCorsOptions returns the rs/cors Options used by the handler.
func GetCorsOptions(allowedOrigins []string) cors.Options {
	return cors.Options{
		AllowedOrigins: allowedOrigins,
		AllowedMethods: connectcors.AllowedMethods(),
		AllowedHeaders: connectcors.AllowedHeaders(),
		ExposedHeaders: connectcors.ExposedHeaders(),
	}
}
