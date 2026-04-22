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
	followconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/follow/v1/followv1connect"
	pushconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/push_notification/v1/push_notificationv1connect"
	ticketconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/ticket/v1/ticketv1connect"
	ticketemailconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/ticket_email/v1/ticket_emailv1connect"
	ticketjourneyconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/ticket_journey/v1/ticket_journeyv1connect"
	userconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/user/v1/userv1connect"
	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/adapter/webhook"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/infrastructure/blockchain/safe"
	"github.com/liverty-music/backend/internal/infrastructure/blockchain/ticketsbt"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/liverty-music/backend/internal/infrastructure/geo"
	inframerkle "github.com/liverty-music/backend/internal/infrastructure/merkle"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/infrastructure/music/lastfm"
	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/internal/infrastructure/server/ratelimit"
	infratelemetry "github.com/liverty-music/backend/internal/infrastructure/telemetry"
	infrawebpush "github.com/liverty-music/backend/internal/infrastructure/webpush"
	infrazitadel "github.com/liverty-music/backend/internal/infrastructure/zitadel"
	"github.com/liverty-music/backend/internal/infrastructure/zkp"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/cache"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// InitializeApp creates a new App with all dependencies wired up manually.
func InitializeApp(ctx context.Context) (*App, error) {
	cfg, err := config.Load[config.ServerConfig]()
	if err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	logger, err := provideLogger(cfg.Logging)
	if err != nil {
		return nil, err
	}
	slog.SetDefault(logger.Slog())

	if len(cfg.Server.AllowedOrigins) == 0 {
		logger.Warn(ctx, "⚠️  CORS not configured, browser requests will fail")
	}

	db, err := rdb.New(ctx, cfg.Database, cfg.IsLocal(), logger)
	if err != nil {
		return nil, err
	}

	telemetryCloser, err := telemetry.SetupTelemetry(ctx, cfg.Telemetry, cfg.Environment, cfg.ShutdownTimeout)
	if err != nil {
		return nil, err
	}

	// Repositories
	userRepo := rdb.NewUserRepository(db)
	artistRepo := rdb.NewArtistRepository(db)
	followRepo := rdb.NewFollowRepository(db)
	concertRepo := rdb.NewConcertRepository(db)
	venueRepo := rdb.NewVenueRepository(db)
	searchLogRepo := rdb.NewSearchLogRepository(db)
	ticketRepo := rdb.NewTicketRepository(db)
	pushSubRepo := rdb.NewPushSubscriptionRepository(db)
	ticketJourneyRepo := rdb.NewTicketJourneyRepository(db)
	ticketEmailRepo := rdb.NewTicketEmailRepository(db)

	// Infrastructure - Gemini (optional)
	var geminiSearcher entity.ConcertSearcher
	var emailParser entity.TicketEmailParser
	if cfg.GCP.ProjectID != "" {
		geminiHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
		searcher, err := gemini.NewConcertSearcher(ctx, gemini.Config{
			ProjectID:   cfg.GCP.ProjectID,
			Location:    cfg.GCP.Location,
			ModelName:   cfg.GCP.GeminiModel,
			DataStoreID: cfg.GCP.VertexAISearchDataStore,
		}, geminiHTTPClient, true, logger)
		if err != nil {
			return nil, err
		}
		geminiSearcher = searcher

		parser, err := gemini.NewEmailParser(ctx, gemini.EmailParserConfig{
			ProjectID: cfg.GCP.ProjectID,
			Location:  cfg.GCP.Location,
			ModelName: cfg.GCP.GeminiModel,
		}, geminiHTTPClient, true, logger)
		if err != nil {
			return nil, err
		}
		emailParser = parser
	}

	// Infrastructure - Music
	musicHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	lastfmClient := lastfm.NewClient(cfg.LastFMAPIKey, musicHTTPClient, logger)
	musicbrainzClient := musicbrainz.NewClient(musicHTTPClient, logger)

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
		ticketUC = usecase.NewTicketUseCase(ticketRepo, sbtClient, infratelemetry.NewOTelMintMetrics(), logger)
	} else {
		logger.Warn(ctx, "⚠️  Blockchain config absent, ticket minting is disabled")
		_ = ticketRepo // referenced when blockchain is enabled; suppress unused warning
	}

	// Infrastructure - Messaging Publisher
	if err := messaging.EnsureStreams(ctx, cfg.NATS); err != nil {
		return nil, fmt.Errorf("ensure NATS streams: %w", err)
	}

	wmLogger := watermill.NewSlogLogger(logger.Slog())
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

	// Infrastructure - Zitadel API client (optional, nil in local dev)
	var emailVerifier usecase.EmailVerifier
	if cfg.ZitadelMachineKeyPath != "" {
		ev, err := infrazitadel.NewEmailVerifier(ctx, cfg.JWT.Issuer, cfg.ZitadelMachineKeyPath, logger)
		if err != nil {
			return nil, fmt.Errorf("create zitadel email verifier: %w", err)
		}
		emailVerifier = ev
	}

	// Business metrics
	businessMetrics := infratelemetry.NewBusinessMetrics()

	// Use Cases
	eventPublisher := messaging.NewEventPublisher(publisher)
	userUC := usecase.NewUserUseCase(userRepo, eventPublisher, logger)
	centroidResolver := geo.NewCentroidResolver()
	concertUC := usecase.NewConcertUseCase(artistRepo, concertRepo, venueRepo, searchLogRepo, geminiSearcher, centroidResolver, eventPublisher, businessMetrics, logger)
	artistUC := usecase.NewArtistUseCase(artistRepo, lastfmClient, musicbrainzClient, eventPublisher, artistCache, logger)
	followUC := usecase.NewFollowUseCase(followRepo, artistRepo, musicbrainzClient, concertUC, searchLogRepo, businessMetrics, logger)
	ticketJourneyUC := usecase.NewTicketJourneyUseCase(ticketJourneyRepo, logger)
	var ticketEmailUC usecase.TicketEmailUseCase
	if emailParser != nil {
		ticketEmailUC = usecase.NewTicketEmailUseCase(ticketEmailRepo, ticketJourneyRepo, emailParser, logger)
	} else {
		_ = ticketEmailRepo // referenced when email parser is enabled; suppress unused warning
	}
	webpushSender := infrawebpush.NewSender(cfg.VAPID.PublicKey, cfg.VAPID.PrivateKey, cfg.VAPID.Contact)
	pushNotificationUC := usecase.NewPushNotificationUseCase(
		artistRepo,
		concertRepo,
		followRepo,
		pushSubRepo,
		webpushSender,
		businessMetrics,
		logger,
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
		"/" + artistconnect.ArtistServiceName + "/ListTop":             true,
		"/" + artistconnect.ArtistServiceName + "/ListSimilar":         true,
		"/" + artistconnect.ArtistServiceName + "/Search":              true,
		"/" + concertconnect.ConcertServiceName + "/List":              true,
		"/" + concertconnect.ConcertServiceName + "/SearchNewConcerts": true,
		"/" + concertconnect.ConcertServiceName + "/ListWithProximity": true,
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
				rpc.NewUserHandler(userUC, emailVerifier, logger),
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
			return followconnect.NewFollowServiceHandler(
				rpc.NewFollowHandler(followUC, userRepo, logger),
				opts...,
			)
		},
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return pushconnect.NewPushNotificationServiceHandler(
				rpc.NewPushNotificationHandler(pushNotificationUC, userRepo, cfg.BaseConfig, logger),
				opts...,
			)
		},
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return ticketjourneyconnect.NewTicketJourneyServiceHandler(
				rpc.NewTicketJourneyHandler(ticketJourneyUC, userRepo, logger),
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

	if ticketEmailUC != nil {
		handlers = append(handlers, func(opts ...connect.HandlerOption) (string, http.Handler) {
			return ticketemailconnect.NewTicketEmailServiceHandler(
				rpc.NewTicketEmailHandler(ticketEmailUC, userRepo, logger),
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
		merkleBuilder := inframerkle.NewBuilder(usecase.DefaultTreeDepth)

		entryUC := usecase.NewEntryUseCase(verifier, nullifierRepo, merkleTreeRepo, merkleBuilder, eventEntryRepo, ticketRepo, logger)
		handlers = append(handlers, func(opts ...connect.HandlerOption) (string, http.Handler) {
			return entryconnect.NewEntryServiceHandler(
				rpc.NewEntryHandler(entryUC, userRepo, logger),
				opts...,
			)
		})
	} else {
		logger.Warn(ctx, "⚠️  ZKP verification key not configured, entry verification is disabled")
	}

	// ConcertService requires a longer handler timeout because Gemini API + Google Search
	// grounding takes 25-110s per call.
	longTimeoutHandlers := []server.LongTimeoutRPCHandler{
		{
			HandlerFunc: func(opts ...connect.HandlerOption) (string, http.Handler) {
				return concertconnect.NewConcertServiceHandler(
					rpc.NewConcertHandler(concertUC, userRepo, logger),
					opts...,
				)
			},
			Timeout: cfg.Server.ConcertHandlerTimeout,
		},
	}

	rateLimiter := ratelimit.NewLimiter(ratelimit.Config{
		AuthRPS:   cfg.Server.RateLimit.AuthRPS,
		AuthBurst: cfg.Server.RateLimit.AuthBurst,
		AnonRPS:   cfg.Server.RateLimit.AnonRPS,
		AnonBurst: cfg.Server.RateLimit.AnonBurst,
	}, time.Minute)

	srv := server.NewConnectServer(cfg.Server, logger, authFunc, rateLimiter, healthHandler, longTimeoutHandlers, handlers...)

	// Zitadel Actions v2 webhook listener — runs on a separate port so the
	// webhook paths are unreachable via the public GKE Gateway. Validators
	// share the JWKS cache with `jwtValidator` so there is exactly one
	// refresh goroutine for all JWT verification.
	preAccessTokenHandler := webhook.NewPreAccessTokenHandler(
		jwtValidator.NewWebhookValidator(cfg.Webhook.PreAccessTokenAudience),
		logger,
	)
	autoVerifyEmailHandler := webhook.NewAutoVerifyEmailHandler(
		jwtValidator.NewWebhookValidator(cfg.Webhook.AutoVerifyEmailAudience),
		logger,
	)
	webhookSrv := server.NewWebhookServer(cfg.Webhook, logger, map[string]http.Handler{
		"/pre-access-token":  preAccessTokenHandler,
		"/auto-verify-email": autoVerifyEmailHandler,
	})

	// Register shutdown phases.
	// Drain: health → NOT_SERVING, then servers drain in-flight requests,
	// then cache cleanup goroutine stops.
	shutdown.AddDrainPhase(healthChecker, srv, webhookSrv, rateLimiter, artistCache)
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
		WebhookServer:   webhookSrv,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}, nil
}

func provideLogger(logCfg config.LoggingConfig) (*logging.Logger, error) {
	var opts []logging.Option
	switch logCfg.Level {
	case "debug":
		opts = append(opts, logging.WithLevel(slog.LevelDebug))
	case "info":
		opts = append(opts, logging.WithLevel(slog.LevelInfo))
	case "warn":
		opts = append(opts, logging.WithLevel(slog.LevelWarn))
	case "error":
		opts = append(opts, logging.WithLevel(slog.LevelError))
	}
	switch logCfg.Format {
	case "text":
		opts = append(opts, logging.WithFormat(logging.FormatText))
	case "json":
		opts = append(opts, logging.WithFormat(logging.FormatJSON))
	}
	return logging.New(opts...)
}
