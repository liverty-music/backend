package di

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"

	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/analytics/posthog"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/liverty-music/backend/internal/infrastructure/geo"
	"github.com/liverty-music/backend/internal/infrastructure/mail"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/infrastructure/music/fanarttv"
	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	infrapayment "github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/liverty-music/backend/internal/infrastructure/repository"
	infratelemetry "github.com/liverty-music/backend/internal/infrastructure/telemetry"
	infrawebpush "github.com/liverty-music/backend/internal/infrastructure/webpush"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// ConsumerApp represents the event consumer application with a Watermill Router.
type ConsumerApp struct {
	Router          *message.Router
	Logger          *logging.Logger
	ShutdownTimeout time.Duration
	// Health reflects whether the consumer is actively consuming (NATS
	// connected + all durables bound + router running). The entry point wires
	// it into the liveness probe so a wedged pod is restarted.
	Health *messaging.ConsumerHealth
}

// behaviorEntry pairs a behavior name with the subject it subscribes to and
// the handler function that processes messages. One entry = one JetStream
// durable consumer with durable == deliver_group == behavior name.
type behaviorEntry struct {
	behavior string
	subject  string
	handler  message.NoPublishHandlerFunc
}

// InitializeConsumerApp creates a ConsumerApp with all event handler dependencies wired.
func InitializeConsumerApp(ctx context.Context) (*ConsumerApp, error) {
	cfg, err := config.Load[config.ConsumerConfig]()
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

	db, err := rdb.New(ctx, cfg.Database, cfg.IsLocal(), logger)
	if err != nil {
		return nil, err
	}

	telemetryCloser, err := telemetry.SetupTelemetry(ctx, cfg.Telemetry, cfg.Environment, cfg.ShutdownTimeout)
	if err != nil {
		return nil, err
	}

	// Repositories
	artistRepo := rdb.NewArtistRepository(db, newOutbox(logger))
	concertRepo := rdb.NewConcertRepository(db, newOutbox(logger))
	pushSubRepo := rdb.NewPushSubscriptionRepository(db, newOutbox(logger))
	followRepo := rdb.NewFollowRepository(db, newOutbox(logger))
	ticketJourneyRepo := rdb.NewTicketJourneyRepository(db, newOutbox(logger))
	userRepo := rdb.NewUserRepository(db, newOutbox(logger))

	// Infrastructure - Messaging
	wmLogger := watermill.NewSlogLogger(logger.Slog())
	var goChannel *gochannel.GoChannel
	if cfg.NATS.URL == "" {
		goChannel = gochannel.NewGoChannel(gochannel.Config{
			OutputChannelBuffer: 256,
		}, wmLogger)
	}

	publisher, err := messaging.NewPublisher(cfg.NATS, wmLogger, goChannel, logger)
	if err != nil {
		return nil, fmt.Errorf("create messaging publisher: %w", err)
	}

	// consumerHealth reflects real consumption into the liveness probe: the NATS
	// subscriber updates connection + per-behavior bound state, and the router
	// probe (set below) reports whether the router is running.
	consumerHealth := messaging.NewConsumerHealth()

	// Infrastructure - MusicBrainz (for artist name resolution)
	extHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	musicbrainzClient := musicbrainz.NewClient(extHTTPClient, logger)

	// Infrastructure - fanart.tv (for artist image resolution)
	fanarttvClient := fanarttv.NewClient(cfg.FanartTVAPIKey, extHTTPClient, logger)
	logoFetcher := fanarttv.NewLogoFetcher(extHTTPClient)

	// Use Cases
	webpushSender := infrawebpush.NewSender(cfg.VAPID.PublicKey, cfg.VAPID.PrivateKey, cfg.VAPID.Contact)
	workflows, err := provideWorkflows(ctx, db, logger)
	if err != nil {
		return nil, err
	}
	notificationRepo := rdb.NewNotificationRepository(db, newOutbox(logger))
	// Purchase confirmation: the email goes through Postmark when its token is
	// configured, otherwise a no-op sender reports Unavailable.
	// The consumer never refunds an Order or reads its charge, so the Order's
	// payment provider part is the no-op.
	orderRepo := repository.NewOrderRepository(rdb.NewOrderRepository(db), infrapayment.NewNoopOrderPayments(logger))
	var confirmationSender entity.OrderConfirmationSender
	if cfg.Postmark.ServerToken != "" {
		confirmationSender = mail.NewPostmarkConfirmationSender(orderRepo, cfg.Postmark.ServerToken,
			cfg.Postmark.FromAddress, cfg.Postmark.MessageStream, time.Now, logger)
	} else {
		confirmationSender = mail.NewNoopConfirmationSender(logger)
	}
	// The sold email of a resold listing goes the same way.
	resaleListingRepo := rdb.NewResaleListingRepository(db, newOutbox(logger))
	var resaleSoldSender entity.ResaleListingSoldEmailSender = mail.NewNoopResaleSoldSender(logger)
	if cfg.Postmark.ServerToken != "" {
		resaleSoldSender = mail.NewPostmarkResaleSoldSender(resaleListingRepo, cfg.Postmark.ServerToken,
			cfg.Postmark.FromAddress, cfg.Postmark.MessageStream, time.Now, logger)
	}
	japanTime, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		return nil, fmt.Errorf("load Asia/Tokyo time zone: %w", err)
	}
	notificationUC := usecase.NewNotificationUseCase(notificationRepo, pushSubRepo, webpushSender, workflows, infratelemetry.NewBusinessMetrics(), logger,
		usecase.OrderConfirmationDeps{
			Orders:         orderRepo,
			Users:          userRepo,
			Concerts:       concertRepo,
			EventOrganizer: rdb.NewEventOrganizerRepository(db),
			Organizers:     rdb.NewOrganizerRepository(db, newOutbox(logger)),
			Mailer:         confirmationSender,
			ResaleListings: resaleListingRepo,
			ResaleMailer:   resaleSoldSender,
			TimeZone:       japanTime,
		},
		usecase.NewConcertsDeps{ArtistRepo: artistRepo, ConcertRepo: concertRepo, FollowRepo: followRepo})
	ticketJourneyUC := usecase.NewTicketJourneyUseCase(ticketJourneyRepo, logger)
	concertCandidateRepo := rdb.NewConcertCandidateRepository(db)
	venueRepo := rdb.NewVenueRepository(db)
	seriesRepo := rdb.NewSeriesRepository(db, newOutbox(logger))
	// Infrastructure - PostHog analytics client (optional, nil in local dev).
	analyticsClient, err := provideAnalyticsClient(cfg.PostHog, cfg.Telemetry, logger)
	if err != nil {
		return nil, err
	}

	artistNameResolutionUC := usecase.NewArtistNameResolutionUseCase(artistRepo, musicbrainzClient, logger)
	artistImageSyncUC := usecase.NewArtistImageSyncUseCase(artistRepo, fanarttvClient, logoFetcher, logger)

	// ConcertUseCase: needed here (not just by the RPC server) so the
	// search-first-followed-artist consumer can call
	// SearchNewConcertsOnFirstFollow when ARTIST.followed fires.
	organizerRepo := rdb.NewOrganizerRepository(db, newOutbox(logger))
	concertSearchRepo := rdb.NewConcertSearchRepository(db)
	rejectedConcertRepo := rdb.NewRejectedConcertRepository(db)
	centroidResolver := geo.NewCentroidResolver()
	var geminiSearcher entity.ConcertSearcher
	if cfg.GCP.GeminiSearchAPIKey != "" {
		searcher, err := gemini.NewConcertSearcher(ctx, gemini.Config{
			APIKey:          cfg.GCP.GeminiSearchAPIKey,
			Model:           cfg.GCP.ConcertSearchModel(),
			ThinkingLevel:   cfg.GCP.ConcertSearchThinkingLevel(),
			OmitTemperature: true,

			IncludeServerSideToolInvocations: true,
			Reporter:                         posthog.NewGenerationReporter(analyticsClient, logger),
		}, extHTTPClient, gemini.NewOfficialPageClient(), logger)
		if err != nil {
			return nil, fmt.Errorf("create gemini concert searcher: %w", err)
		}
		geminiSearcher = searcher
	}
	concertUC := usecase.NewConcertUseCase(
		artistRepo,
		concertRepo,
		venueRepo,
		seriesRepo,
		organizerRepo,
		concertSearchRepo,
		concertCandidateRepo,
		rejectedConcertRepo,
		geminiSearcher,
		centroidResolver,
		workflows,
		infratelemetry.NewBusinessMetrics(),
		analyticsClient,
		cfg.GCP.SearchCacheTTL(),
		cfg.GCP.SearchDiscoveryWindow(),
		logger,
	)

	// The SALES_PHASE.discovered consumer's use case. AnnounceDiscoveredSale
	// starts a deliver-notification workflow per recipient, so it depends on
	// Workflows, not NotificationUseCase.
	ticketSaleAnnouncementUC := usecase.NewTicketSaleAnnouncementUseCase(
		userRepo,
		ticketJourneyRepo,
		seriesRepo,
		workflows,
		logger,
	)

	// The consumer's UserUseCase starts the verification and passkey change
	// emails, which the workflow worker sends; it never deletes users.
	userUC := usecase.NewUserUseCase(userRepo, usecase.DeletionBlockerReads{}, workflows, nil, nil, nil,
		usecase.PasskeyMailers{}, logger)

	// Event Consumers
	notificationConsumer := event.NewNotificationConsumer(notificationUC, logger)
	artistNameConsumer := event.NewArtistNameConsumer(artistNameResolutionUC, logger)
	artistImageConsumer := event.NewArtistImageConsumer(artistImageSyncUC, logger)
	userConsumer := event.NewUserConsumer(userUC, logger)
	analyticsConsumerMetrics := infratelemetry.NewOTelAnalyticsConsumerMetrics()
	analyticsConsumer := event.NewAnalyticsConsumer(analyticsClient, analyticsConsumerMetrics, logger)
	poisonConsumer := event.NewPoisonConsumer(logger)
	ticketSaleAnnouncementConsumer := event.NewTicketSaleAnnouncementConsumer(ticketSaleAnnouncementUC, logger)
	followSearchConsumer := event.NewFollowSearchConsumer(concertUC, logger)
	orderPaidConsumer := event.NewOrderPaidConsumer(notificationUC, ticketJourneyUC, rdb.NewTicketStockRepository(db), logger)
	resaleListingRefundedConsumer := event.NewResaleListingRefundedConsumer(notificationUC, logger)
	passkeyConsumer := event.NewPasskeyConsumer(userUC, logger)

	// behaviorTable is the canonical behavior → subject → handler mapping.
	// Each row becomes one independent JetStream durable consumer with
	// durable == deliver_group == behavior name. Two rows on the same subject
	// (e.g. SubjectArtistCreated, SubjectUserCreated) produce independent
	// durables — each receives every message on that subject — which is the
	// fan-out fix the old single shared-group subscriber broke.
	behaviorTable := []behaviorEntry{
		{"notify-concert", entity.SubjectConcertCreated, notificationConsumer.Handle},
		{"resolve-artist-name", entity.SubjectArtistCreated, artistNameConsumer.Handle},
		{"resolve-artist-image", entity.SubjectArtistCreated, artistImageConsumer.Handle},
		{"verify-user-email", entity.SubjectUserCreated, userConsumer.Handle},
		{"track-user-created", entity.SubjectUserCreated, analyticsConsumer.HandleUserCreated},
		{"track-user-logged-in", entity.SubjectUserLoggedIn, analyticsConsumer.HandleUserLoggedIn},
		{"track-artist-followed", entity.SubjectArtistFollowed, analyticsConsumer.HandleArtistFollowed},
		{"track-artist-unfollowed", entity.SubjectArtistUnfollowed, analyticsConsumer.HandleArtistUnfollowed},
		{"track-notification-subscribed", entity.SubjectNotificationSubscribed, analyticsConsumer.HandleNotificationSubscribed},
		{"track-notification-unsubscribed", entity.SubjectNotificationUnsubscribed, analyticsConsumer.HandleNotificationUnsubscribed},
		{"track-notification-delivered", entity.SubjectNotificationDelivered, analyticsConsumer.HandleNotificationDelivered},
		{"track-ticket-journey", entity.SubjectTicketJourneyStatusChanged, analyticsConsumer.HandleTicketJourneyStatusChanged},
		{"analytics-organizer-created", entity.SubjectOrganizerCreated, analyticsConsumer.HandleOrganizerCreated},
		{"analytics-organizer-artist-associated", entity.SubjectOrganizerArtistAssociated, analyticsConsumer.HandleOrganizerArtistAssociated},
		{"track-artist-hype-changed", entity.SubjectArtistHypeChanged, analyticsConsumer.HandleArtistHypeChanged},
		{"track-order-paid", entity.SubjectOrderPaid, analyticsConsumer.HandleOrderPaid},
		{"track-lottery-entry-drawn", entity.SubjectLotteryEntryDrawn, analyticsConsumer.HandleLotteryEntryDrawn},
		{"track-checkout-expired", entity.SubjectCheckoutExpired, analyticsConsumer.HandleCheckoutExpired},
		{"track-ticket-admitted", entity.SubjectTicketAdmitted, analyticsConsumer.HandleTicketAdmitted},
		{"log-poison", messaging.PoisonQueueSubject, poisonConsumer.Handle},
		{"notify-sales-phase", entity.SubjectTicketSaleDiscovered, ticketSaleAnnouncementConsumer.Handle},
		{"search-first-followed-artist", entity.SubjectArtistFollowed, followSearchConsumer.Handle},
		{"send-order-confirmation", entity.SubjectOrderPaid, orderPaidConsumer.HandleSendOrderConfirmation},
		{"mark-ticket-journey-paid", entity.SubjectOrderPaid, orderPaidConsumer.HandleMarkPaid},
		{"send-resale-sold", entity.SubjectResaleListingRefunded, resaleListingRefundedConsumer.Handle},
		{"notify-passkey-added", entity.SubjectUserPasskeyAdded, passkeyConsumer.HandlePasskeyAdded},
		{"notify-passkey-removed", entity.SubjectUserPasskeyRemoved, passkeyConsumer.HandlePasskeyRemoved},
	}

	// Router
	router, err := messaging.NewRouter(wmLogger, publisher, messaging.PoisonQueueSubject)
	if err != nil {
		return nil, fmt.Errorf("create messaging router: %w", err)
	}

	// IsClosed reports true only after the router has been closed — including
	// when watermill closes it because all handlers stopped (a wedge). It stays
	// false during the pre-start window (the entry point runs the router in a
	// goroutine after wiring), so this probe does not false-negative at boot;
	// readiness gates traffic during initialization. A closed router makes
	// liveness unhealthy so Kubernetes restarts the wedged pod.
	consumerHealth.SetRouterProbe(func() bool {
		return !router.IsClosed()
	})

	if cfg.NATS.URL == "" {
		// Local development: use the single GoChannel subscriber for all
		// handlers. Fan-out and durable isolation are not relevant locally —
		// all handlers run in-process against the same in-memory channel.
		for _, e := range behaviorTable {
			router.AddConsumerHandler(e.behavior, e.subject, goChannel, e.handler)
		}
	} else {
		// Production NATS path.
		//
		// Open the long-lived shared *nats.Conn. One TCP connection serves all
		// durables; per-handler isolation comes from distinct durable names, not
		// separate connections. NACK (the external operator) owns durable
		// lifecycle — the app only binds to pre-existing durables.
		sharedConn, err := messaging.ConnectNATS(ctx, cfg.NATS, consumerHealth, logger)
		if err != nil {
			return nil, fmt.Errorf("connect shared NATS connection: %w", err)
		}
		// Drain the shared connection during shutdown (after the router has
		// stopped consuming) so in-flight acks flush before the process exits.
		shutdown.AddExternalPhase(messaging.NATSConnCloser(sharedConn))

		// Create one per-behavior pull subscriber for each entry and register
		// it with the router. Each subscriber binds (bind-only, no create) to
		// the behavior-named durable, so the router sees independent cursors.
		for _, e := range behaviorTable {
			sub, err := messaging.NewBehaviorSubscriber(sharedConn, e.behavior, wmLogger, consumerHealth)
			if err != nil {
				return nil, fmt.Errorf("create subscriber for behavior %q: %w", e.behavior, err)
			}
			router.AddConsumerHandler(e.behavior, e.subject, sub, e.handler)
		}
	}

	// Register shutdown phases.
	shutdown.Init(logger)
	shutdown.AddFlushPhase(publisher)
	shutdown.AddExternalPhase(musicbrainzClient)
	shutdown.AddExternalPhase(fanarttvClient)
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	// Goroutine-leak detection (Go 1.27): internal pprof listener + sampler.
	startGoroutineLeakDetection(ctx, cfg.GoroutineLeak, cfg.Telemetry.ServiceName, logger)

	return &ConsumerApp{
		Router:          router,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Health:          consumerHealth,
	}, nil
}
