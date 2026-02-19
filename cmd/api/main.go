// Package main provides the API server entry point.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/liverty-music/backend/internal/di"
)

func main() {
	if err := run(); err != nil {
		log.Printf("Server failed: %v", err)
		os.Exit(1)
	}
}

func run() error {
	// Create a context that will be canceled when OS signals are received
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt,    // SIGINT (Ctrl+C)
		syscall.SIGTERM, // SIGTERM (k8s termination signal)
		syscall.SIGQUIT, // SIGQUIT
	)
	defer stop()

	log.Println("Starting server...")

	app, err := di.InitializeApp(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err := app.Shutdown(context.Background()); err != nil {
			log.Printf("error during shutdown: %v", err)
		}
	}()

	// Start server in a goroutine
	errChan := make(chan error, 1)

	go func() {
		if err := app.Server.Start(); err != nil {
			errChan <- err
		}
	}()

	// Wait for either context cancellation (signal) or server error
	select {
	case <-ctx.Done():
		log.Println("Received shutdown signal, stopping server gracefully...")
		return nil

	case err := <-errChan:
		log.Printf("Server failed to start: %v", err)
		return err
	}
}
