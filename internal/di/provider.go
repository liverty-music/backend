package di

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	adminorganizerconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/admin/organizer/v1/organizerv1connect"
	adminconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/admin/v1/adminv1connect"
	artistconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/artist/v1/artistv1connect"
	concertconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/concert/v1/concertv1connect"
	followconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/follow/v1/followv1connect"
	identityconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/identity/v1/identityv1connect"
	notificationconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/notification/v1/notificationv1connect"
	organizerconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/v1/organizerv1connect"
	pushconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/push_notification/v1/push_notificationv1connect"
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
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	gcsstorage "github.com/liverty-music/backend/internal/infrastructure/gcp/storage"
	"github.com/liverty-music/backend/internal/infrastructure/geo"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/infrastructure/music/lastfm"
	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	infrapocketsign "github.com/liverty-music/backend/internal/infrastructure/pocketsign"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/internal/infrastructure/server/ratelimit"
	infratelemetry "github.com/liverty-music/backend/internal/infrastructure/telemetry"
	infrawebpush "github.com/liverty-music/backend/internal/infrastructure/webpush"
	infrazitadel "github.com/liverty-music/backend/internal/infrastructure/zitadel"
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
	seriesRepo := rdb.NewSeriesRepository(db)
	searchLogRepo := rdb.NewSearchLogRepository(db)
	stagedConcertRepo := rdb.NewStagedConcertRepository(db)
	rejectedConcertRepo := rdb.NewRejectedConcertLogRepository(db)
	pushSubRepo := rdb.NewPushSubscriptionRepository(db)
	ticketJourneyRepo := rdb.NewTicketJourneyRepository(db)
	ticketEmailRepo := rdb.NewTicketEmailRepository(db)
	organizerRepo := rdb.NewOrganizerRepository(db)
	verifiedIdentityRepo := rdb.NewVerifiedIdentityRepository(db)

	// Infrastructure - Gemini (optional)
	var geminiSearcher entity.ConcertSearcher
	var emailParser entity.TicketEmailParser
	if cfg.GCP.GeminiSearchAPIKey != "" {
		geminiHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
		searcher, err := gemini.NewConcertSearcher(ctx, gemini.Config{
			APIKey:          cfg.GCP.GeminiSearchAPIKey,
			ModelExtract:    cfg.GCP.SearchModelExtract(),
			ModelParse:      cfg.GCP.SearchModelParse(),
			Temperature:     cfg.GCP.GeminiSearchTemperature,
			ThinkingLevel:   cfg.GCP.GeminiSearchThinkingLevel,
			ThinkingExtract: cfg.GCP.GeminiSearchThinkingExtract,
			ThinkingParse:   cfg.GCP.GeminiSearchThinkingParse,
		}, geminiHTTPClient, logger)
		if err != nil {
			return nil, err
		}
		geminiSearcher = searcher

		// The Gemini ticket-email parser (gemini.NewEmailParser) is intentionally
		// NOT wired here: the share-target email-import feature is dormant. The
		// frontend disables its entry point (the import-ticket-email route serves
		// the "unavailable" state and skips all network calls) and there is no
		// production traffic. Leaving emailParser nil keeps TicketEmailUseCase and
		// its RPC handler unregistered via the emailParser != nil guards below.
		//
		// Not wiring it also removes the #414 footgun: NewEmailParser calls
		// UseDefaultCredentials, which mutates the passed *http.Client's Transport
		// in place to attach an ADC `Authorization: Bearer` header. Sharing that
		// client with the searcher made its generativelanguage.googleapis.com
		// calls fail with 403 ACCESS_TOKEN_SCOPE_INSUFFICIENT. To revive: give the
		// parser its OWN dedicated *http.Client (never the searcher's) and
		// re-enable the frontend entry point.
	}

	// Infrastructure - Music
	musicHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	lastfmClient := lastfm.NewClient(cfg.LastFMAPIKey, musicHTTPClient, logger)
	musicbrainzClient := musicbrainz.NewClient(musicHTTPClient, logger)

	// Cache - Artist discovery results with 1 hour TTL
	artistCache := cache.NewMemoryCache(1 * time.Hour)

	// Initialize the shutdown package for phased resource teardown.
	shutdown.Init(logger)

	// Infrastructure - Messaging Publisher
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

	// Infrastructure - Zitadel API client (optional, nil in local dev).
	var emailVerifier usecase.EmailVerifier
	if cfg.ZitadelMachineKeyForBackendAppPath != "" {
		ev, err := infrazitadel.NewEmailVerifier(ctx, cfg.JWT.Issuer, cfg.ZitadelMachineKeyForBackendAppPath, logger)
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
	concertUC := usecase.NewConcertUseCase(artistRepo, concertRepo, venueRepo, seriesRepo, organizerRepo, searchLogRepo, stagedConcertRepo, rejectedConcertRepo, geminiSearcher, centroidResolver, eventPublisher, businessMetrics, cfg.GCP.SearchCacheTTL(), cfg.GCP.SearchDiscoveryWindow(), logger)
	artistUC := usecase.NewArtistUseCase(artistRepo, lastfmClient, musicbrainzClient, eventPublisher, artistCache, logger)
	// Organizer tenant provisioner: the real Zitadel Management-API client when the
	// dedicated organizer-provisioner credential is mounted (isolated admin
	// workload), otherwise a no-op for local dev (no live Zitadel).
	var organizerProvisioner usecase.OrganizerProvisioner
	if cfg.ZitadelMachineKeyForOrganizerProvisionerPath != "" {
		op, err := infrazitadel.NewOrganizerProvisioner(ctx, cfg.JWT.Issuer, cfg.ZitadelMachineKeyForOrganizerProvisionerPath, cfg.OrganizerConsoleProjectID, logger)
		if err != nil {
			return nil, fmt.Errorf("create organizer provisioner: %w", err)
		}
		organizerProvisioner = op
	} else {
		organizerProvisioner = infrazitadel.NewNoopOrganizerProvisioner(logger)
	}
	organizerUC := usecase.NewOrganizerUseCase(organizerRepo, artistRepo, organizerProvisioner, eventPublisher, businessMetrics, logger)
	// Run the provisioning reconciler only where the real provisioner credential
	// is mounted (the isolated admin workload); it completes any organizer left
	// in the provisioning state after a partial failure.
	if cfg.ZitadelMachineKeyForOrganizerProvisionerPath != "" {
		startOrganizerReconciler(ctx, organizerUC, logger)
	}
	// GCS image storer for organizer media (optional: nil when GCP credentials
	// are unavailable in local dev so signed-URL issuance returns Internal
	// rather than panicking during startup).
	var imageStorer usecase.ImageStorer
	if !cfg.IsLocal() {
		storer, err := gcsstorage.NewGCSStorer(ctx, logger)
		if err != nil {
			logger.Warn(ctx, "failed to create GCS image storer; media upload disabled",
				slog.Any("error", err),
			)
		} else {
			imageStorer = storer
			shutdown.AddExternalPhase(storer)
		}
	}
	concertAuthoringUC := usecase.NewConcertAuthoringUseCase(seriesRepo, venueRepo, organizerUC, eventPublisher, logger)

	// Identity eKYC — select the real Pocket Sign Verify API client when
	// POCKET_SIGN_BASE_URL and POCKET_SIGN_TOKEN are both configured; fall back
	// to StubVerifier (returns UNAVAILABLE) when unconfigured so local dev and
	// pre-onboarding environments stay safe and inert.
	var pocketSignVerifier usecase.PocketSignVerifier
	psConfig := infrapocketsign.VerifyClientConfig{
		BaseURL: cfg.PocketSign.BaseURL,
		Token:   cfg.PocketSign.Token,
	}
	if psConfig.IsConfigured() {
		// Bound every vendor call so a hung Pocket Sign API cannot block a
		// handler goroutine indefinitely when the request context lacks a
		// deadline.
		psHTTPClient := &http.Client{
			Timeout:   30 * time.Second,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		}
		verifyClient := infrapocketsign.NewVerifyClient(psConfig, psHTTPClient, logger)
		pocketSignVerifier = verifyClient
		// Register the client's nonce-cache cleanup goroutine for graceful
		// shutdown, like the other MemoryCache instances (cf. artistCache).
		shutdown.AddDrainPhase(verifyClient)
		logger.Info(ctx, "pocket sign verify client initialized",
			slog.String("base_url", cfg.PocketSign.BaseURL),
		)
	} else {
		pocketSignVerifier = infrapocketsign.NewStubVerifier()
		logger.Info(ctx, "pocket sign verify client not configured; using stub (identity verification unavailable)")
	}
	identityVerificationUC := usecase.NewIdentityVerificationUseCase(verifiedIdentityRepo, userRepo, pocketSignVerifier, logger)
	mediaUC := usecase.NewMediaUseCase(seriesRepo, seriesRepo, organizerUC, imageStorer, eventPublisher, logger)

	followUC := usecase.NewFollowUseCase(followRepo, artistRepo, musicbrainzClient, concertUC, searchLogRepo, eventPublisher, businessMetrics, logger)
	ticketJourneyUC := usecase.NewTicketJourneyUseCase(ticketJourneyRepo, eventPublisher, logger)
	var ticketEmailUC usecase.TicketEmailUseCase
	if emailParser != nil {
		ticketEmailUC = usecase.NewTicketEmailUseCase(ticketEmailRepo, ticketJourneyRepo, emailParser, eventPublisher, logger)
	} else {
		_ = ticketEmailRepo // referenced when email parser is enabled; suppress unused warning
	}
	webpushSender := infrawebpush.NewSender(cfg.VAPID.PublicKey, cfg.VAPID.PrivateKey, cfg.VAPID.Contact)
	notificationRepo := rdb.NewNotificationRepository(db)
	notificationUC := usecase.NewNotificationUseCase(notificationRepo, pushSubRepo, webpushSender, eventPublisher, businessMetrics, logger)
	pushNotificationUC := usecase.NewPushNotificationUseCase(
		artistRepo,
		concertRepo,
		followRepo,
		pushSubRepo,
		eventPublisher,
		notificationUC,
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
		"/" + concertconnect.ConcertServiceName + "/ListByArtists":     true,
		"/" + concertconnect.ConcertServiceName + "/ListByLocation":    true,
	}

	authFunc := auth.NewAuthFunc(jwtValidator, publicProcedures)

	// Health check handler (public, outside authn middleware).
	// Keep a reference so App.Shutdown can call SetShuttingDown.
	healthChecker := rpc.NewHealthCheckHandler(db, logger)
	healthHandler := func(opts ...connect.HandlerOption) (string, http.Handler) {
		return grpchealth.NewHandler(healthChecker, opts...)
	}

	// Admin RPC handlers — served ONLY by the dedicated admin server, whose
	// server-wide RequireRoleInterceptor gates every procedure on the "admin"
	// role. The consumer server below does NOT register these, so the admin
	// surface cannot be reached via the consumer host.
	adminHandlers := []server.RPCHandlerFunc{
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return adminconnect.NewConcertServiceHandler(
				rpc.NewAdminConcertHandler(concertUC, logger),
				opts...,
			)
		},
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return adminorganizerconnect.NewOrganizerServiceHandler(
				rpc.NewAdminOrganizerHandler(organizerUC, logger),
				opts...,
			)
		},
		// Mount the consumer ArtistService on the admin server so the admin
		// console can reuse ArtistService.Search (to pick an artist to
		// associate) via the admin host + admin token, instead of a
		// cross-origin call to the consumer API. Per design D2, the
		// higher-privilege admin server may additionally mount consumer
		// handlers as needed; the server-wide RequireRoleInterceptor(admin)
		// gates every method here.
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return artistconnect.NewArtistServiceHandler(
				rpc.NewArtistHandler(artistUC, logger),
				opts...,
			)
		},
	}

	// Consumer RPC handlers (protected by authn middleware)
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
			return notificationconnect.NewNotificationServiceHandler(
				rpc.NewNotificationHandler(notificationUC, userRepo, logger),
				opts...,
			)
		},
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return ticketjourneyconnect.NewTicketJourneyServiceHandler(
				rpc.NewTicketJourneyHandler(ticketJourneyUC, userRepo, logger),
				opts...,
			)
		},
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return identityconnect.NewIdentityVerificationServiceHandler(
				rpc.NewIdentityVerificationHandler(identityVerificationUC, userUC, logger),
				opts...,
			)
		},
	}

	if ticketEmailUC != nil {
		handlers = append(handlers, func(opts ...connect.HandlerOption) (string, http.Handler) {
			return ticketemailconnect.NewTicketEmailServiceHandler(
				rpc.NewTicketEmailHandler(ticketEmailUC, userRepo, logger),
				opts...,
			)
		})
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

	// Consumer Connect server — all consumer services, no admin service, no
	// extra interceptors.
	srv := server.NewConnectServer(cfg.Server, logger, authFunc, rateLimiter, healthHandler, nil, longTimeoutHandlers, handlers...)

	// Admin Connect server — a second listener in the same binary on its own
	// port and CORS allowlist, serving ONLY admin services. Its server-wide
	// RequireRoleInterceptor (admin role) is the sole, structural authorization
	// gate (handlers carry no per-method role check). It shares the auth func,
	// rate limiter, and health checker with the consumer server.
	adminServerCfg := cfg.Server
	adminServerCfg.Port = cfg.Server.AdminPort
	adminServerCfg.AllowedOrigins = cfg.Server.AdminAllowedOrigins
	adminInterceptors := []connect.Interceptor{auth.NewRequireRoleInterceptor("admin")}
	adminSrv := server.NewConnectServer(adminServerCfg, logger, authFunc, rateLimiter, healthHandler, adminInterceptors, nil, adminHandlers...)

	// Organizer Connect server — a third listener in the same binary on its
	// own port and CORS allowlist, serving ONLY the organizer-facing
	// OrganizerService. Its server-wide OrgScopedInterceptor enforces token
	// audience, login-scope org derivation, and role cross-check before any
	// handler runs. No fan or admin services are registered here.
	organizerServerCfg := cfg.Server
	organizerServerCfg.Port = cfg.Server.OrganizerPort
	organizerServerCfg.AllowedOrigins = cfg.Server.OrganizerAllowedOrigins
	organizerInterceptors := []connect.Interceptor{auth.NewOrgScopedInterceptor(cfg.OrganizerConsoleProjectID)}
	organizerHandlers := []server.RPCHandlerFunc{
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return organizerconnect.NewOrganizerServiceHandler(
				rpc.NewOrganizerHandler(organizerUC, logger),
				opts...,
			)
		},
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return organizerconnect.NewConcertServiceHandler(
				rpc.NewOrganizerConcertHandler(concertAuthoringUC, organizerUC, mediaUC, logger),
				opts...,
			)
		},
	}
	organizerSrv := server.NewConnectServer(organizerServerCfg, logger, authFunc, rateLimiter, healthHandler, organizerInterceptors, nil, organizerHandlers...)

	// Zitadel Actions v2 webhook listener — runs on a separate port so the
	// webhook paths are unreachable via the public GKE Gateway. Validators
	// share the JWKS cache with `jwtValidator` so there is exactly one
	// refresh goroutine for all JWT verification.
	preAccessTokenHandler := webhook.NewPreAccessTokenHandler(
		jwtValidator.NewWebhookValidator(cfg.Webhook.PreAccessTokenAudience),
		logger,
	)
	// Login-event Actions v2 handler — bound to the event execution on
	// session.user.checked; emits account.login once per interactive login,
	// never on token refresh or a machine grant. The Target is PAYLOAD_TYPE_JSON,
	// so the handler verifies the HMAC ZITADEL-Signature with the Target's signing
	// key. Publishes a best-effort ACCOUNT.login event and never fails login.
	loginEventHandler := webhook.NewLoginEventHandler(
		cfg.Webhook.LoginEventSigningKey,
		userUC,
		eventPublisher,
		logger,
	)
	webhookSrv := server.NewWebhookServer(cfg.Webhook, logger, map[string]http.Handler{
		"/pre-access-token":    preAccessTokenHandler,
		"/account-login-event": loginEventHandler,
	})

	// Register shutdown phases.
	// Drain: health → NOT_SERVING, then servers drain in-flight requests,
	// then cache cleanup goroutine stops.
	shutdown.AddDrainPhase(healthChecker, srv, adminSrv, organizerSrv, webhookSrv, rateLimiter, artistCache)
	shutdown.AddFlushPhase(publisher)
	shutdown.AddExternalPhase(lastfmClient, musicbrainzClient)
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	// Goroutine-leak detection (Go 1.27): internal pprof listener + sampler.
	startGoroutineLeakDetection(ctx, cfg.GoroutineLeak, cfg.Telemetry.ServiceName, logger)

	return &App{
		Server:          srv,
		AdminServer:     adminSrv,
		OrganizerServer: organizerSrv,
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
