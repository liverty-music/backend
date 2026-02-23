// Package di provides dependency injection and application bootstrapping.
package di

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/pannpers/go-logging/logging"
)

func newApp(server *server.ConnectServer, logger *logging.Logger, closers ...io.Closer) *App {
	return &App{
		Server:  server,
		Logger:  logger,
		Closers: closers,
	}
}

// App represents the application with all its dependencies and lifecycle management.
type App struct {
	Server  *server.ConnectServer
	Logger  *logging.Logger
	Closers []io.Closer
}

// Shutdown gracefully shuts down the application and closes all resources.
func (a *App) Shutdown(ctx context.Context) error {
	a.Logger.Info(ctx, "starting application shutdown")

	var errs error

	// First, stop the server gracefully
	if err := a.Server.Stop(); err != nil {
		errs = errors.Join(errs, fmt.Errorf("failed to graceful shutdown server: %w", err))
	}

	// Then close all other resources
	for _, closer := range a.Closers {
		if err := closer.Close(); err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to close system resource: %w", err))
		}
	}

	if errs != nil {
		return errs
	}

	a.Logger.Info(ctx, "application shutdown complete")

	return nil
}
