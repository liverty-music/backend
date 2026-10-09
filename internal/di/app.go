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
	// AdminServer is a second Connect listener in the same binary serving only
	// admin-scoped RPCs on its own port and ingress host, with boundary-level
	// admin-role authorization. See `internal/infrastructure/server/connect.go`.
	AdminServer *server.ConnectServer
	// OrganizerServer is a third Connect listener in the same binary serving
	// only the organizer-facing OrganizerService on its own port and ingress
	// host (api.organizer.{base-domain}), with org-scoped role-claim
	// authorization via OrgScopedInterceptor.
	OrganizerServer *server.ConnectServer
	// ReceptionServer is a fourth Connect listener in the same binary serving
	// only the ReceptionService a venue staff device calls without a sign-in,
	// on its own port and ingress host (api.reception.{base-domain}). It is
	// exposed only by the reception-api workload, whose database role holds
	// the reception grants alone.
	ReceptionServer *server.ConnectServer
	// WebhookServer handles Zitadel Actions v2 callbacks
	// (/pre-access-token) on a separate internal-only port. See
	// `internal/infrastructure/server/webhook.go` for the port-isolation
	// rationale.
	WebhookServer   *server.WebhookServer
	Logger          *logging.Logger
	ShutdownTimeout time.Duration
}
