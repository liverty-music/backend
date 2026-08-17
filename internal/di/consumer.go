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
	"golang.org/x/oauth2/google"

	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/analytics/posthog"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	googlemaps "github.com/liverty-music/backend/internal/infrastructure/maps/google"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/infrastructure/music/fanarttv"
	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	infratelemetry "github.com/liverty-music/backend/internal/infrastructure/telemetry"
	infrawebpush "github.com/liverty-music/backend/internal/infrastructure/webpush"
	infrazitadel "github.com/liverty-music/backend/internal/infrastructure/zitadel"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/httpx"
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
	artistRepo := rdb.NewArtistRepository(db)
	concertRepo := rdb.NewConcertRepository(db)
	pushSubRepo := rdb.NewPushSubscriptionRepository(db)
	followRepo := rdb.NewFollowRepository(db)
	ticketJourneyRepo := rdb.NewTicketJourneyRepository(db)
	salesReminderRepo := rdb.NewSalesPhaseReminderRepository(db)
	userRepo := rdb.NewUserRepository(db)

	// Infrastructure - Messaging
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

	// consumerHealth reflects real consumption into the liveness probe: the NATS
	// subscriber updates connection + per-behavior bound state, and the router
	// probe (set below) reports whether the router is running.
	consumerHealth := messaging.NewConsumerHealth()

	// Infrastructure - Google Maps Places API (required for venue resolution).
	// Uses OAuth via ADC (Workload Identity in GKE).
	if cfg.GCP.ProjectID == "" {
		return nil, fmt.Errorf("GCP project ID is required for Google Maps Places API")
	}
	gmTokenSource, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("obtain google maps token source: %w", err)
	}
	gmHTTPClient := &http.Client{
		Transport: otelhttp.NewTransport(httpx.NewRetryTransport(nil)),
		Timeout:   10 * time.Second,
	}
	gmClient := googlemaps.NewClient(gmTokenSource, cfg.GCP.ProjectID, gmHTTPClient, logger)
	placeSearcher := googlemaps.NewPlaceSearcher(gmClient)

	// Infrastructure - MusicBrainz (for artist name resolution)
	extHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	musicbrainzClient := musicbrainz.NewClient(extHTTPClient, logger)

	// Infrastructure - fanart.tv (for artist image resolution)
	fanarttvClient := fanarttv.NewClient(cfg.FanartTVAPIKey, extHTTPClient, logger)
	logoFetcher := fanarttv.NewLogoFetcher(extHTTPClient)

	// Use Cases
	webpushSender := infrawebpush.NewSender(cfg.VAPID.PublicKey, cfg.VAPID.PrivateKey, cfg.VAPID.Contact)
	eventPublisher := messaging.NewEventPublisher(publisher)
	notificationRepo := rdb.NewNotificationRepository(db)
	notificationUC := usecase.NewNotificationUseCase(notificationRepo, pushSubRepo, webpushSender, eventPublisher, infratelemetry.NewBusinessMetrics(), logger)
	pushNotificationUC := usecase.NewPushNotificationUseCase(
		artistRepo,
		concertRepo,
		followRepo,
		pushSubRepo,
		eventPublisher,
		notificationUC,
		logger,
	)
	stagedConcertRepo := rdb.NewStagedConcertRepository(db)
	venueRepo := rdb.NewVenueRepository(db)
	seriesRepo := rdb.NewSeriesRepository(db)
	suppressedConcertRepo := rdb.NewSuppressedConcertRepository(db)
	concertCreationUC := usecase.NewConcertCreationUseCase(
		stagedConcertRepo,
		venueRepo,
		concertRepo,
		seriesRepo,
		suppressedConcertRepo,
		placeSearcher,
		eventPublisher,
		logger,
	)
	artistNameResolutionUC := usecase.NewArtistNameResolutionUseCase(artistRepo, musicbrainzClient, logger)
	artistImageSyncUC := usecase.NewArtistImageSyncUseCase(artistRepo, fanarttvClient, logoFetcher, logger)

	// Infrastructure - Zitadel API client (optional, nil in local dev).
	var emailVerifier usecase.EmailVerifier
	if cfg.ZitadelMachineKeyForBackendAppPath != "" {
		ev, err := infrazitadel.NewEmailVerifier(ctx, cfg.ZitadelDomain, cfg.ZitadelMachineKeyForBackendAppPath, logger)
		if err != nil {
			return nil, fmt.Errorf("create zitadel email verifier: %w", err)
		}
		emailVerifier = ev
	}

	// Infrastructure - PostHog analytics client (optional, nil in local dev).
	var analyticsClient usecase.AnalyticsClient
	if cfg.PostHog.ProjectAPIKey != "" {
		ac, err := posthog.New(cfg.PostHog.APIHost, cfg.PostHog.ProjectAPIKey, logger)
		if err != nil {
			return nil, fmt.Errorf("create posthog analytics client: %w", err)
		}
		analyticsClient = ac
	}

	// Sales-phase use cases for the two new consumers. Both dispatch through the
	// notification service so every announcement / reminder gets a durable record.
	salesPhaseAnnouncementUC := usecase.NewSalesPhaseAnnouncementUseCase(
		userRepo,
		ticketJourneyRepo,
		notificationUC,
		logger,
	)
	salesReminderDeliveryUC := usecase.NewSalesReminderDeliveryUseCase(
		salesReminderRepo,
		notificationUC,
		logger,
	)

	// Event Consumers
	concertConsumer := event.NewConcertConsumer(concertCreationUC, logger)
	notificationConsumer := event.NewNotificationConsumer(pushNotificationUC, logger)
	artistNameConsumer := event.NewArtistNameConsumer(artistNameResolutionUC, logger)
	artistImageConsumer := event.NewArtistImageConsumer(artistImageSyncUC, logger)
	userConsumer := event.NewUserConsumer(emailVerifier, logger)
	analyticsConsumerMetrics := infratelemetry.NewOTelAnalyticsConsumerMetrics()
	analyticsConsumer := event.NewAnalyticsConsumer(analyticsClient, analyticsConsumerMetrics, logger)
	poisonConsumer := event.NewPoisonConsumer(logger)
	salesPhaseAnnouncementConsumer := event.NewSalesPhaseAnnouncementConsumer(salesPhaseAnnouncementUC, logger)
	salesReminderConsumer := event.NewSalesReminderConsumer(salesReminderDeliveryUC, logger)

	// behaviorTable is the canonical behavior → subject → handler mapping.
	// Each row becomes one independent JetStream durable consumer with
	// durable == deliver_group == behavior name. Two rows on the same subject
	// (e.g. SubjectArtistCreated, SubjectUserCreated) produce independent
	// durables — each receives every message on that subject — which is the
	// fan-out fix the old single shared-group subscriber broke.
	behaviorTable := []behaviorEntry{
		{"ingest-concert", entity.SubjectConcertDiscovered, concertConsumer.Handle},
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
		{"track-ticket-email", entity.SubjectTicketEmailParsed, analyticsConsumer.HandleTicketEmailParsed},
		{"analytics-organizer-created", entity.SubjectOrganizerCreated, analyticsConsumer.HandleOrganizerCreated},
		{"analytics-organizer-artist-associated", entity.SubjectOrganizerArtistAssociated, analyticsConsumer.HandleOrganizerArtistAssociated},
		{"log-poison", messaging.PoisonQueueSubject, poisonConsumer.Handle},
		{"notify-sales-phase", entity.SubjectSalesPhaseDiscovered, salesPhaseAnnouncementConsumer.Handle},
		{"notify-sales-reminder", entity.SubjectSalesPhaseReminderDue, salesReminderConsumer.Handle},
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
		// Step 1: delete stale durables before the router subscribes so we do
		// not leave old consumer_* prefixed, shared-group, or orphaned
		// durables that would misbind new per-behavior subscriptions.
		desiredBehaviors := make([]string, 0, len(behaviorTable))
		for _, e := range behaviorTable {
			desiredBehaviors = append(desiredBehaviors, e.behavior)
		}
		if err := messaging.ReconcileConsumers(ctx, cfg.NATS, desiredBehaviors, logger.Slog()); err != nil {
			return nil, fmt.Errorf("reconcile NATS consumers: %w", err)
		}

		// Step 2: open the long-lived shared *nats.Conn. One TCP connection
		// serves all ~20 durables; per-handler isolation comes from distinct
		// durable names and deliver groups, not separate connections.
		sharedConn, err := messaging.ConnectNATS(ctx, cfg.NATS, consumerHealth)
		if err != nil {
			return nil, fmt.Errorf("connect shared NATS connection: %w", err)
		}
		// Drain the shared connection during shutdown (after the router has
		// stopped consuming) so in-flight acks flush before the process exits.
		shutdown.AddExternalPhase(messaging.NATSConnCloser(sharedConn))

		// Step 3: create one per-behavior watermill subscriber for each entry
		// and register it with the router. Each subscriber binds to the
		// behavior-named durable, so the router sees 20 independent cursors.
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
	if analyticsClient != nil {
		shutdown.AddExternalPhase(analyticsClient.(*posthog.AnalyticsClient))
	}
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	return &ConsumerApp{
		Router:          router,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Health:          consumerHealth,
	}, nil
}
