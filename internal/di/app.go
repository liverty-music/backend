// Package di provides dependency injection and application bootstrapping.
package di

import (
	"time"

	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/pannpers/go-logging/logging"
)

// App represents the application with all its dependencies.
// Resource lifecycle is managed by the shutdown package; App itself
// holds only the references needed by cmd/ entry points.
type App struct {
	Server *server.ConnectServer
	// WebhookServer handles Zitadel Actions v2 callbacks
	// (/pre-access-token) on a separate internal-only port. See
	// `internal/infrastructure/server/webhook.go` for the port-isolation
	// rationale.
	WebhookServer   *server.WebhookServer
	Logger          *logging.Logger
	ShutdownTimeout time.Duration
}
