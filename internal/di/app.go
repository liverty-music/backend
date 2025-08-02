package di

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/pannpers/go-backend-scaffold/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-backend-scaffold/internal/infrastructure/server"
)

func newApp(server *server.ConnectServer, db *rdb.Database, telemetryCloser io.Closer) *App {
	return &App{
		Server:  server,
		Closers: []io.Closer{db, telemetryCloser},
	}
}

type App struct {
	Server  *server.ConnectServer
	Closers []io.Closer
}

func (a *App) Shutdown(ctx context.Context) error {
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
