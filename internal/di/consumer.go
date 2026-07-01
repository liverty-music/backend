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

	subscriber, err := messaging.NewSubscriber(cfg.NATS, wmLogger, goChannel)
	if err != nil {
		return nil, fmt.Errorf("create messaging subscriber: %w", err)
	}

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
	concertCreationUC := usecase.NewConcertCreationUseCase(stagedConcertRepo, placeSearcher, logger)
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
		eventPublisher,
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

	// Router
	router, err := messaging.NewRouter(wmLogger, publisher, messaging.PoisonQueueSubject)
	if err != nil {
		return nil, fmt.Errorf("create messaging router: %w", err)
	}

	router.AddConsumerHandler(
		"create-concerts",
		entity.SubjectConcertDiscovered,
		subscriber,
		concertConsumer.Handle,
	)

	router.AddConsumerHandler(
		"notify-fans",
		entity.SubjectConcertCreated,
		subscriber,
		notificationConsumer.Handle,
	)

	router.AddConsumerHandler(
		"resolve-artist-name",
		entity.SubjectArtistCreated,
		subscriber,
		artistNameConsumer.Handle,
	)

	router.AddConsumerHandler(
		"resolve-artist-image",
		entity.SubjectArtistCreated,
		subscriber,
		artistImageConsumer.Handle,
	)

	router.AddConsumerHandler(
		"send-email-verification",
		entity.SubjectUserCreated,
		subscriber,
		userConsumer.Handle,
	)

	router.AddConsumerHandler(
		"forward-user-created-to-analytics",
		entity.SubjectUserCreated,
		subscriber,
		analyticsConsumer.HandleUserCreated,
	)

	router.AddConsumerHandler(
		"forward-user-preferred-language-updated-to-analytics",
		entity.SubjectUserPreferredLanguageUpdated,
		subscriber,
		analyticsConsumer.HandleUserPreferredLanguageUpdated,
	)

	router.AddConsumerHandler(
		"forward-artist-followed-to-analytics",
		entity.SubjectArtistFollowed,
		subscriber,
		analyticsConsumer.HandleArtistFollowed,
	)

	router.AddConsumerHandler(
		"forward-artist-unfollowed-to-analytics",
		entity.SubjectArtistUnfollowed,
		subscriber,
		analyticsConsumer.HandleArtistUnfollowed,
	)

	router.AddConsumerHandler(
		"forward-notification-subscribed-to-analytics",
		entity.SubjectNotificationSubscribed,
		subscriber,
		analyticsConsumer.HandleNotificationSubscribed,
	)

	router.AddConsumerHandler(
		"forward-notification-unsubscribed-to-analytics",
		entity.SubjectNotificationUnsubscribed,
		subscriber,
		analyticsConsumer.HandleNotificationUnsubscribed,
	)

	// NOTIFICATION.delivered matches the existing NOTIFICATION.* stream
	// (see messaging.streams) — no new JetStream stream is required.
	router.AddConsumerHandler(
		"forward-notification-delivered-to-analytics",
		entity.SubjectNotificationDelivered,
		subscriber,
		analyticsConsumer.HandleNotificationDelivered,
	)

	router.AddConsumerHandler(
		"forward-entry-zk-proof-verified-to-analytics",
		entity.SubjectEntryZkProofVerified,
		subscriber,
		analyticsConsumer.HandleEntryZkProofVerified,
	)

	router.AddConsumerHandler(
		"forward-entry-zk-proof-rejected-to-analytics",
		entity.SubjectEntryZkProofRejected,
		subscriber,
		analyticsConsumer.HandleEntryZkProofRejected,
	)

	router.AddConsumerHandler(
		"forward-ticket-journey-status-changed-to-analytics",
		entity.SubjectTicketJourneyStatusChanged,
		subscriber,
		analyticsConsumer.HandleTicketJourneyStatusChanged,
	)

	router.AddConsumerHandler(
		"forward-ticket-mint-completed-to-analytics",
		entity.SubjectTicketMintCompleted,
		subscriber,
		analyticsConsumer.HandleTicketMintCompleted,
	)

	router.AddConsumerHandler(
		"forward-ticket-email-parsed-to-analytics",
		entity.SubjectTicketEmailParsed,
		subscriber,
		analyticsConsumer.HandleTicketEmailParsed,
	)

	router.AddConsumerHandler(
		"forward-sales-reminder-delivered-to-analytics",
		entity.SubjectSalesReminderDelivered,
		subscriber,
		analyticsConsumer.HandleSalesReminderDelivered,
	)

	router.AddConsumerHandler(
		"log-poison-queue",
		messaging.PoisonQueueSubject,
		subscriber,
		poisonConsumer.Handle,
	)

	router.AddConsumerHandler(
		"announce-sales-phase",
		entity.SubjectSalesPhaseDiscovered,
		subscriber,
		salesPhaseAnnouncementConsumer.Handle,
	)

	router.AddConsumerHandler(
		"send-sales-phase-reminder",
		entity.SubjectSalesPhaseReminderDue,
		subscriber,
		salesReminderConsumer.Handle,
	)

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
	}, nil
}
