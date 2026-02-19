// Package di provides dependency injection and application bootstrapping.
package di

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/liverty-music/backend/internal/infrastructure/server"
)

func newApp(server *server.ConnectServer, closers ...io.Closer) *App {
	return &App{
		Server:  server,
		Closers: closers,
	}
}

// App represents the application with all its dependencies and lifecycle management.
type App struct {
	Server  *server.ConnectServer
	Closers []io.Closer
}

// Shutdown gracefully shuts down the application and closes all resources.
func (a *App) Shutdown(_ context.Context) error {
	log.Println("Starting application shutdown...")

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

	log.Println("Application shutdown complete")

	return nil
}
