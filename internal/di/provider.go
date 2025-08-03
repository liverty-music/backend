package di

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"

	v1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/v1/rpcv1connect"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/logging"
	"github.com/liverty-music/backend/pkg/telemetry"
)

// provideConfig creates a new config instance.
func provideConfig() (*config.Config, error) {
	return config.Load("")
}

// provideLogger creates a new logger instance based on config.
func provideLogger(cfg *config.Config) *logging.Logger {
	var opts []logging.Option

	// Set log level based on config
	switch cfg.Logging.Level {
	case "debug":
		opts = append(opts, logging.WithLevel(slog.LevelDebug))
	case "info":
		opts = append(opts, logging.WithLevel(slog.LevelInfo))
	case "warn":
		opts = append(opts, logging.WithLevel(slog.LevelWarn))
	case "error":
		opts = append(opts, logging.WithLevel(slog.LevelError))
	}

	// Set log format based on config
	switch cfg.Logging.Format {
	case "text":
		opts = append(opts, logging.WithFormat(logging.FormatText))
	case "json":
		opts = append(opts, logging.WithFormat(logging.FormatJSON))
	}

	return logging.New(opts...)
}

// provideDatabase creates a new database instance.
func provideDatabase(ctx context.Context, cfg *config.Config, logger *logging.Logger) (*rdb.Database, error) {
	return rdb.New(ctx, cfg, logger)
}

// provideTelemetry creates a new telemetry instance and returns the closer.
func provideTelemetry(ctx context.Context, cfg *config.Config) (io.Closer, error) {
	return telemetry.SetupTelemetry(ctx, cfg)
}

func provideHandlerFuncs(logger *logging.Logger, db *rdb.Database, userUseCase *usecase.UserUseCase) []server.RPCHandlerFunc {
	return []server.RPCHandlerFunc{
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return grpchealth.NewHandler(
				rpc.NewHealthCheckHandler(db, logger),
				opts...,
			)
		},
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return v1connect.NewUserServiceHandler(
				// rpc.NewUserHandler(userUseCase, logger),
				nil,
				opts...,
			)
		},
	}
}

// Mock implementations for development/testing
// TODO: Replace with actual database implementations

// MockUserRepository is a simple mock implementation for development
type MockUserRepository struct{}

func (m *MockUserRepository) Create(ctx context.Context, params *entity.NewUser) (*entity.User, error) {
	return &entity.User{
		ID:        "mock-user-id",
		Name:      params.Name,
		Email:     params.Email,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

func (m *MockUserRepository) Get(ctx context.Context, id string) (*entity.User, error) {
	return &entity.User{
		ID:        id,
		Name:      "Mock User",
		Email:     "mock@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

func (m *MockUserRepository) Delete(ctx context.Context, id string) error {
	return nil
}

// provideUserRepository creates a user repository implementation using the database.
func provideUserRepository(db *rdb.Database) entity.UserRepository {
	// return rdb.NewUserRepository(db)
	return nil
}
