package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
)

// WebhookServer is a dedicated HTTP server for Zitadel Actions v2 webhooks.
//
// It listens on a separate port from the Connect-RPC server so the webhook
// paths are physically absent from the public Connect-RPC listener, and
// therefore unreachable via the GKE Gateway / `server-svc` HTTPRoute.
// The expected topology is: a new internal-only `server-webhook-svc`
// ClusterIP Service targets this listener directly, and only Zitadel pods
// call it over in-cluster DNS.
//
// The listener has NO authn.Middleware — incoming requests carry their own
// PAYLOAD_TYPE_JWT body which each handler verifies via WebhookValidator.
type WebhookServer struct {
	server  *http.Server
	logger  *logging.Logger
	address string
}

// NewWebhookServer registers the given handlers on a new mux and wraps it
// with sensible HTTP server defaults. `handlers` maps URL path → handler.
func NewWebhookServer(
	cfg config.WebhookSettings,
	logger *logging.Logger,
	handlers map[string]http.Handler,
) *WebhookServer {
	mux := http.NewServeMux()
	for path, handler := range handlers {
		mux.Handle(path, handler)
	}

	address := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	return &WebhookServer{
		server: &http.Server{
			Addr:              address,
			Handler:           mux,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			ReadTimeout:       cfg.ReadTimeout,
			IdleTimeout:       cfg.IdleTimeout,
		},
		logger:  logger,
		address: address,
	}
}

// Start begins listening. Blocks until the server is stopped; returns
// http.ErrServerClosed on graceful shutdown.
func (s *WebhookServer) Start() error {
	s.logger.Info(context.Background(), fmt.Sprintf("Webhook Server starting on %s", s.address))
	return s.server.ListenAndServe()
}

// webhookDrainTimeout bounds how long Shutdown waits for in-flight webhook
// calls to complete before killing the server. Webhook handlers are fast
// (single DB-free JWT verify + JSON encode), so 10s is generous.
const webhookDrainTimeout = 10 * time.Second

// Close gracefully stops the server. Implements io.Closer so the server can
// be registered with the shutdown package's Drain phase alongside the
// Connect-RPC server.
func (s *WebhookServer) Close() error {
	if s.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), webhookDrainTimeout)
	defer cancel()
	s.logger.Info(ctx, "draining Webhook server", slog.Duration("timeout", webhookDrainTimeout))
	return s.server.Shutdown(ctx)
}
