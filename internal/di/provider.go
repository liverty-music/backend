package di

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	artistconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/artist/v1/artistv1connect"
	concertconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/concert/v1/concertv1connect"
	userconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/user/v1/userv1connect"
	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/liverty-music/backend/internal/infrastructure/music/lastfm"
	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/cache"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
)

// InitializeApp creates a new App with all dependencies wired up manually.
func InitializeApp(ctx context.Context) (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	logger, err := provideLogger(cfg)
	if err != nil {
		return nil, err
	}

	if len(cfg.Server.AllowedOrigins) == 0 {
		logger.Warn(ctx, "⚠️  CORS not configured, browser requests will fail")
	}

	db, err := rdb.New(ctx, cfg, logger)
	if err != nil {
		return nil, err
	}

	telemetryCloser, err := telemetry.SetupTelemetry(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// Repositories
	userRepo := provideUserRepository(db)
	artistRepo := rdb.NewArtistRepository(db)
	concertRepo := rdb.NewConcertRepository(db)
	venueRepo := rdb.NewVenueRepository(db)
	searchLogRepo := rdb.NewSearchLogRepository(db)

	// Infrastructure - Gemini (optional)
	var geminiSearcher entity.ConcertSearcher
	if cfg.GCP.ProjectID != "" {
		searcher, err := gemini.NewConcertSearcher(ctx, gemini.Config{
			ProjectID:   cfg.GCP.ProjectID,
			Location:    cfg.GCP.Location,
			ModelName:   cfg.GCP.GeminiModel,
			DataStoreID: cfg.GCP.VertexAISearchDataStore,
		}, nil, logger)
		if err != nil {
			return nil, err
		}
		geminiSearcher = searcher
	}

	// Infrastructure - Music
	lastfmClient := lastfm.NewClient(cfg.LastFMAPIKey, nil)
	musicbrainzClient := musicbrainz.NewClient(nil)

	// Cache - Artist discovery results with 1 hour TTL
	artistCache := cache.NewMemoryCache(1 * time.Hour)

	// Start background cleanup for cache to prevent memory leak
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				artistCache.Cleanup()
			}
		}
	}()

	// Use Cases
	userUC := usecase.NewUserUseCase(userRepo, logger)
	artistUC := usecase.NewArtistUseCase(artistRepo, lastfmClient, musicbrainzClient, artistCache, logger)
	concertUC := usecase.NewConcertUseCase(artistRepo, concertRepo, venueRepo, searchLogRepo, geminiSearcher, logger)

	// Auth - JWT Validator and Interceptor
	jwtValidator, err := auth.NewJWTValidator(
		cfg.JWT.Issuer,
		cfg.JWT.Issuer+"/oauth/v2/keys",
		cfg.JWT.JWKSRefreshInterval,
	)
	if err != nil {
		return nil, err
	}

	authInterceptor := auth.NewAuthInterceptor(jwtValidator)

	// Handlers
	handlers := []server.RPCHandlerFunc{
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return grpchealth.NewHandler(
				rpc.NewHealthCheckHandler(db, logger),
				opts...,
			)
		},
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return userconnect.NewUserServiceHandler(
				rpc.NewUserHandler(userUC, logger),
				opts...,
			)
		},
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return artistconnect.NewArtistServiceHandler(
				rpc.NewArtistHandler(artistUC, logger),
				opts...,
			)
		},
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return concertconnect.NewConcertServiceHandler(
				rpc.NewConcertHandler(concertUC, logger),
				opts...,
			)
		},
	}

	srv := server.NewConnectServer(cfg, logger, db, authInterceptor, handlers...)

	return newApp(srv, db, telemetryCloser, lastfmClient, musicbrainzClient), nil
}

func provideLogger(cfg *config.Config) (*logging.Logger, error) {
	var opts []logging.Option
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
	switch cfg.Logging.Format {
	case "text":
		opts = append(opts, logging.WithFormat(logging.FormatText))
	case "json":
		opts = append(opts, logging.WithFormat(logging.FormatJSON))
	}
	return logging.New(opts...)
}

func provideUserRepository(_ *rdb.Database) entity.UserRepository {
	return &MockUserRepository{}
}

// MockUserRepository is a simple mock implementation for development
type MockUserRepository struct{}

// Create is a mock implementation that always returns nil.
func (m *MockUserRepository) Create(_ context.Context, _ *entity.NewUser) (*entity.User, error) {
	return nil, nil
}

// Get is a mock implementation that always returns nil.
func (m *MockUserRepository) Get(_ context.Context, _ string) (*entity.User, error) {
	return nil, nil
}

// GetByEmail is a mock implementation that always returns nil.
func (m *MockUserRepository) GetByEmail(_ context.Context, _ string) (*entity.User, error) {
	return nil, nil
}

// Update is a mock implementation that always returns nil.
func (m *MockUserRepository) Update(_ context.Context, _ string, _ *entity.NewUser) (*entity.User, error) {
	return nil, nil
}

// Delete is a mock implementation that always returns nil.
func (m *MockUserRepository) Delete(_ context.Context, _ string) error {
	return nil
}

// List is a mock implementation that always returns nil.
func (m *MockUserRepository) List(_ context.Context, _, _ int) ([]*entity.User, error) {
	return nil, nil
}
