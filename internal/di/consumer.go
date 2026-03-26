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
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	googlemaps "github.com/liverty-music/backend/internal/infrastructure/maps/google"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/infrastructure/music/fanarttv"
	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	infrawebpush "github.com/liverty-music/backend/internal/infrastructure/webpush"
	infrazitadel "github.com/liverty-music/backend/internal/infrastructure/zitadel"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/httpx"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
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

	telemetryCloser, err := telemetry.SetupTelemetry(ctx, cfg.Telemetry, cfg.ShutdownTimeout)
	if err != nil {
		return nil, err
	}

	// Repositories
	artistRepo := rdb.NewArtistRepository(db)
	concertRepo := rdb.NewConcertRepository(db)
	venueRepo := rdb.NewVenueRepository(db)
	pushSubRepo := rdb.NewPushSubscriptionRepository(db)
	followRepo := rdb.NewFollowRepository(db)

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
		Transport: httpx.NewRetryTransport(nil),
		Timeout:   10 * time.Second,
	}
	gmClient := googlemaps.NewClient(gmTokenSource, cfg.GCP.ProjectID, gmHTTPClient, logger)
	placeSearcher := googlemaps.NewPlaceSearcher(gmClient)

	// Infrastructure - MusicBrainz (for artist name resolution)
	musicbrainzClient := musicbrainz.NewClient(nil, logger)

	// Infrastructure - fanart.tv (for artist image resolution)
	fanarttvClient := fanarttv.NewClient(cfg.FanartTVAPIKey, nil, logger)
	logoFetcher := fanarttv.NewLogoFetcher(nil)

	// Use Cases
	webpushSender := infrawebpush.NewSender(cfg.VAPID.PublicKey, cfg.VAPID.PrivateKey, cfg.VAPID.Contact)
	pushNotificationUC := usecase.NewPushNotificationUseCase(
		followRepo,
		pushSubRepo,
		webpushSender,
		logger,
	)
	concertCreationUC := usecase.NewConcertCreationUseCase(venueRepo, concertRepo, placeSearcher, messaging.NewEventPublisher(publisher), logger)
	artistNameResolutionUC := usecase.NewArtistNameResolutionUseCase(artistRepo, musicbrainzClient, logger)
	artistImageSyncUC := usecase.NewArtistImageSyncUseCase(artistRepo, fanarttvClient, logoFetcher, logger)

	// Infrastructure - Zitadel API client (optional, nil in local dev)
	var emailVerifier usecase.EmailVerifier
	if cfg.ZitadelMachineKeyPath != "" {
		ev, err := infrazitadel.NewEmailVerifier(ctx, cfg.ZitadelDomain, cfg.ZitadelMachineKeyPath, logger)
		if err != nil {
			return nil, fmt.Errorf("create zitadel email verifier: %w", err)
		}
		emailVerifier = ev
	}

	// Event Consumers
	concertConsumer := event.NewConcertConsumer(concertCreationUC, logger)
	notificationConsumer := event.NewNotificationConsumer(artistRepo, concertRepo, pushNotificationUC, logger)
	artistNameConsumer := event.NewArtistNameConsumer(artistNameResolutionUC, logger)
	artistImageConsumer := event.NewArtistImageConsumer(artistImageSyncUC, logger)
	userConsumer := event.NewUserConsumer(emailVerifier, logger)

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

	// Register shutdown phases.
	shutdown.Init(logger)
	shutdown.AddFlushPhase(publisher)
	shutdown.AddExternalPhase(musicbrainzClient)
	shutdown.AddExternalPhase(fanarttvClient)
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	return &ConsumerApp{
		Router:          router,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}, nil
}
