// Package main provides the identity service entry point: the only backend
// process that holds identity-provider credentials. It serves the event
// consumer and the admin console API on one listener each, and the gRPC health
// check on a third.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/pannpers/go-logging/logging"
)

// fallbackShutdownTimeout is used when DI initialization fails and
// app.ShutdownTimeout is unavailable.
const fallbackShutdownTimeout = 10 * time.Second

func main() {
	logger := di.NewBootstrapLogger()
	if err := run(logger); err != nil {
		logger.Error(context.Background(), "identity service failed", err)
		os.Exit(1)
	}
}

func run(bootLogger *logging.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
		syscall.SIGQUIT,
	)
	defer stop()

	bootLogger.Info(ctx, "starting identity service")

	var app *di.IdentityApp
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
	app, err = di.InitializeIdentityApp(ctx)
	if err != nil {
		return err
	}

	errChan := make(chan error, 3)
	serve := func(srv *server.ConnectServer, ln net.Listener) {
		go func() {
			if err := srv.Serve(ln); err != nil {
				errChan <- err
			}
		}()
	}

	// Bind every listener before reporting ready, so readiness means the
	// callers' requests are accepted.
	servers := []*server.ConnectServer{app.HealthServer, app.EventConsumerServer, app.AdminConsoleAPIServer}
	for _, srv := range servers {
		ln, err := net.Listen("tcp", srv.Address())
		if err != nil {
			return err
		}
		serve(srv, ln)
	}
	app.Health.SetReady()
	app.Logger.Info(ctx, "identity service ready")

	select {
	case <-ctx.Done():
		app.Logger.Info(ctx, "received shutdown signal, stopping identity service gracefully",
			slog.String("cause", context.Cause(ctx).Error()),
		)
		return nil
	case err := <-errChan:
		app.Logger.Error(ctx, "identity service listener failed", err)
		return err
	}
}
