package server

import (
	"net/http"

	connectcors "connectrpc.com/cors"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/rs/cors"
)

// NewCORSHandler creates a new CORS middleware using connectrpc helpers.
func NewCORSHandler(mu http.Handler, srvConfig *config.ServerConfig) http.Handler {
	return cors.New(cors.Options{
		AllowedOrigins: srvConfig.AllowedOrigins,
		AllowedMethods: connectcors.AllowedMethods(),
		AllowedHeaders: connectcors.AllowedHeaders(),
		ExposedHeaders: connectcors.ExposedHeaders(),
	}).Handler(mu)
}
