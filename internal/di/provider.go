package di

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	artistconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/artist/v1/artistv1connect"
	concertconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/concert/v1/concertv1connect"
	entryconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/entry/v1/entryv1connect"
	pushconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/push_notification/v1/push_notificationv1connect"
	ticketconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/ticket/v1/ticketv1connect"
	userconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/user/v1/userv1connect"
	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/infrastructure/blockchain/safe"
	"github.com/liverty-music/backend/internal/infrastructure/blockchain/ticketsbt"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/infrastructure/music/lastfm"
	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/internal/infrastructure/zkp"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/cache"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
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
	userRepo := rdb.NewUserRepository(db)
	artistRepo := rdb.NewArtistRepository(db)
	concertRepo := rdb.NewConcertRepository(db)
	venueRepo := rdb.NewVenueRepository(db)
	searchLogRepo := rdb.NewSearchLogRepository(db)
	ticketRepo := rdb.NewTicketRepository(db)
	pushSubRepo := rdb.NewPushSubscriptionRepository(db)

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
	lastfmClient := lastfm.NewClient(cfg.LastFMAPIKey, nil, logger)
	musicbrainzClient := musicbrainz.NewClient(nil, logger)

	// Cache - Artist discovery results with 1 hour TTL
	artistCache := cache.NewMemoryCache(1 * time.Hour)

	// Initialize the shutdown package for phased resource teardown.
	shutdown.Init(logger)

	// Infrastructure - Blockchain (optional; skipped when config is absent)
	var ticketUC usecase.TicketUseCase
	var sbtCloser io.Closer
	if cfg.Blockchain.RPCURL != "" && cfg.Blockchain.DeployerPrivateKey != "" && cfg.Blockchain.TicketSBTAddress != "" {
		sbtClient, err := ticketsbt.NewClient(
			ctx,
			cfg.Blockchain.RPCURL,
			cfg.Blockchain.DeployerPrivateKey,
			cfg.Blockchain.TicketSBTAddress,
			cfg.Blockchain.ChainID,
			logger,
		)
		if err != nil {
			return nil, err
		}
		sbtCloser = sbtClient
		ticketUC = usecase.NewTicketUseCase(ticketRepo, sbtClient, logger)
	} else {
		logger.Warn(ctx, "⚠️  Blockchain config absent, ticket minting is disabled")
		_ = ticketRepo // referenced when blockchain is enabled; suppress unused warning
	}

	// Infrastructure - Messaging Publisher
	wmLogger := watermill.NewStdLogger(false, false)
	var goChannel *gochannel.GoChannel
	if cfg.NATS.URL == "" {
		goChannel = gochannel.NewGoChannel(gochannel.Config{
			OutputChannelBuffer: 256,
		}, wmLogger)
	}
	publisher, err := messaging.NewPublisher(cfg.NATS, wmLogger, goChannel)
	if err != nil {
		return nil, fmt.Errorf("create messaging publisher: %w", err)
	}

	// Use Cases
	userUC := usecase.NewUserUseCase(userRepo, logger)
	concertUC := usecase.NewConcertUseCase(artistRepo, concertRepo, venueRepo, userRepo, searchLogRepo, geminiSearcher, publisher, logger)
	artistUC := usecase.NewArtistUseCase(artistRepo, userRepo, lastfmClient, musicbrainzClient, musicbrainzClient, concertUC, searchLogRepo, artistCache, logger)
	pushNotificationUC := usecase.NewPushNotificationUseCase(
		artistRepo,
		pushSubRepo,
		logger,
		cfg.VAPID.PublicKey,
		cfg.VAPID.PrivateKey,
		cfg.VAPID.Contact,
	)
	// Auth - JWT Validator and Interceptor
	jwtValidator, err := auth.NewJWTValidator(
		cfg.JWT.Issuer,
		cfg.JWT.Issuer+"/oauth/v2/keys",
		cfg.JWT.JWKSRefreshInterval,
	)
	if err != nil {
		return nil, err
	}

	// Apply additional accepted issuers for multi-provider support (Option C migration).
	if len(cfg.JWT.AcceptedIssuers) > 0 {
		all := append([]string{cfg.JWT.Issuer}, cfg.JWT.AcceptedIssuers...)
		jwtValidator = jwtValidator.WithAcceptedIssuers(all)
	}

	// Public procedures accessible without authentication during onboarding.
	// Read-only endpoints that return publicly available data (artist charts,
	// concert schedules). Write endpoints remain fully authenticated.
	publicProcedures := map[string]bool{
		"/" + artistconnect.ArtistServiceName + "/ListTop":     true,
		"/" + artistconnect.ArtistServiceName + "/ListSimilar": true,
		"/" + artistconnect.ArtistServiceName + "/Search":      true,
		"/" + concertconnect.ConcertServiceName + "/List":      true,
	}

	authFunc := auth.NewAuthFunc(jwtValidator, publicProcedures)

	// Health check handler (public, outside authn middleware).
	// Keep a reference so App.Shutdown can call SetShuttingDown.
	healthChecker := rpc.NewHealthCheckHandler(db, logger)
	healthHandler := func(opts ...connect.HandlerOption) (string, http.Handler) {
		return grpchealth.NewHandler(healthChecker, opts...)
	}

	// RPC handlers (protected by authn middleware)
	handlers := []server.RPCHandlerFunc{
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return userconnect.NewUserServiceHandler(
				rpc.NewUserHandler(userUC, userRepo, logger),
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
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return pushconnect.NewPushNotificationServiceHandler(
				rpc.NewPushNotificationHandler(pushNotificationUC, logger),
				opts...,
			)
		},
	}

	if ticketUC != nil {
		safePredictor := safe.NewPredictor(cfg.Blockchain.SafeProxyFactory, cfg.Blockchain.SafeInitCodeHash)
		handlers = append(handlers, func(opts ...connect.HandlerOption) (string, http.Handler) {
			return ticketconnect.NewTicketServiceHandler(
				rpc.NewTicketHandler(ticketUC, userRepo, safePredictor, logger),
				opts...,
			)
		})
	}

	// Infrastructure - ZKP Verification (optional; skipped when config is absent)
	if cfg.ZKP.VerificationKeyPath != "" {
		verifier, err := zkp.NewVerifier(cfg.ZKP.VerificationKeyPath)
		if err != nil {
			return nil, err
		}

		nullifierRepo := rdb.NewNullifierRepository(db)
		merkleTreeRepo := rdb.NewMerkleTreeRepository(db)
		eventEntryRepo := rdb.NewEventEntryRepository(db)

		entryUC := usecase.NewEntryUseCase(verifier, nullifierRepo, merkleTreeRepo, eventEntryRepo, ticketRepo, logger)
		handlers = append(handlers, func(opts ...connect.HandlerOption) (string, http.Handler) {
			return entryconnect.NewEntryServiceHandler(
				rpc.NewEntryHandler(entryUC, userRepo, logger),
				opts...,
			)
		})
	} else {
		logger.Warn(ctx, "⚠️  ZKP verification key not configured, entry verification is disabled")
	}

	srv := server.NewConnectServer(cfg, logger, db, authFunc, healthHandler, handlers...)

	// Register shutdown phases.
	// Drain: health → NOT_SERVING, then server drains in-flight requests,
	// then cache cleanup goroutine stops.
	shutdown.AddDrainPhase(healthChecker, srv, artistCache)
	shutdown.AddFlushPhase(publisher)
	externalClosers := []io.Closer{lastfmClient, musicbrainzClient}
	if sbtCloser != nil {
		externalClosers = append(externalClosers, sbtCloser)
	}
	shutdown.AddExternalPhase(externalClosers...)
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	return &App{
		Server:          srv,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}, nil
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
