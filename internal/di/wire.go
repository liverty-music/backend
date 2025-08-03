//go:build wireinject
// +build wireinject

package di

import (
	"context"

	"github.com/google/wire"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/internal/usecase"
)

// InitializeApp creates a new App with all dependencies wired up.
func InitializeApp(ctx context.Context) (*App, error) {
	wire.Build(
		newApp,
		server.NewConnectServer,
		provideDatabase,
		provideConfig,
		provideLogger,
		provideTelemetry,

		// Repository layer
		provideUserRepository,
		providePostRepository,

		// Use case layer
		usecase.NewUserUseCase,
		usecase.NewPostUseCase,

		// Handler layer
		provideHandlerFuncs,
	)
	return nil, nil
}
