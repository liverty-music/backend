// Package main provides the API server entry point.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/pannpers/go-logging/logging"
)

// fallbackShutdownTimeout is used when DI initialization fails and
// app.ShutdownTimeout is unavailable.
const fallbackShutdownTimeout = 10 * time.Second

func main() {
	logger := di.NewBootstrapLogger()
	if err := run(logger); err != nil {
		logger.Error(context.Background(), "server failed", err)
		os.Exit(1)
	}
}

func run(bootLogger *logging.Logger) error {
	// Create a context that will be canceled when OS signals are received
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt,    // SIGINT (Ctrl+C)
		syscall.SIGTERM, // SIGTERM (k8s termination signal)
		syscall.SIGQUIT, // SIGQUIT
	)
	defer stop()

	bootLogger.Info(ctx, "starting server")

	// Register shutdown before DI so partially-initialized resources are
	// cleaned up even when initialization fails partway through.
	var app *di.App
	defer func() {
		timeout := fallbackShutdownTimeout
		if app != nil {
			timeout = app.ShutdownTimeout
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := shutdown.Shutdown(ctx); err != nil {
			bootLogger.Error(context.Background(), "error during shutdown", err)
		}
	}()

	var err error
	app, err = di.InitializeApp(ctx)
	if err != nil {
		return err
	}

	errChan := startServers(app)

	// Wait for either context cancellation (signal) or server error
	select {
	case <-ctx.Done():
		// Go 1.26: context.Cause returns the specific OS signal that triggered cancellation.
		cause := context.Cause(ctx)
		app.Logger.Info(ctx, "received shutdown signal, stopping server gracefully",
			slog.String("cause", cause.Error()),
		)
		return nil

	case err := <-errChan:
		app.Logger.Error(ctx, "server failed to start", err)
		return err
	}
}

// starter is a server that serves until it fails or is stopped.
type starter interface {
	Start() error
}

// startServers starts all four Connect-RPC servers (fan, admin, organizer,
// reception) and the Zitadel webhook listener, each in its own goroutine, and
// returns the channel their startup or serving errors arrive on. The webhook
// listener runs on a distinct port (default 9090) that is only exposed by the
// internal-only `server-webhook-svc` Service, not by the public Gateway; the
// admin, organizer and reception servers each have their own port and route
// (`api.admin.{env}`, `api.organizer.{env}`, `api.reception.{env}`).
func startServers(app *di.App) <-chan error {
	servers := []starter{app.Server, app.AdminServer, app.OrganizerServer, app.ReceptionServer, app.WebhookServer}
	errChan := make(chan error, len(servers))
	for _, srv := range servers {
		go func() {
			if err := srv.Start(); err != nil {
				errChan <- err
			}
		}()
	}
	return errChan
}
