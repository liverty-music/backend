package di

import (
	"context"
	"fmt"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	googlemaps "github.com/liverty-music/backend/internal/infrastructure/maps/google"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
)

// ConsumerApp represents the event consumer application with a Watermill Router.
type ConsumerApp struct {
	Router          *message.Router
	HealthServer    *server.HealthServer
	Logger          *logging.Logger
	ShutdownTimeout time.Duration
}

// InitializeConsumerApp creates a ConsumerApp with all event handler dependencies wired.
func InitializeConsumerApp(ctx context.Context) (*ConsumerApp, error) {
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

	db, err := rdb.New(ctx, cfg, logger)
	if err != nil {
		return nil, err
	}

	telemetryCloser, err := telemetry.SetupTelemetry(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// Repositories
	artistRepo := rdb.NewArtistRepository(db)
	concertRepo := rdb.NewConcertRepository(db)
	venueRepo := rdb.NewVenueRepository(db)
	pushSubRepo := rdb.NewPushSubscriptionRepository(db)

	// Infrastructure - Messaging
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

	subscriber, err := messaging.NewSubscriber(cfg.NATS, wmLogger, goChannel)
	if err != nil {
		return nil, fmt.Errorf("create messaging subscriber: %w", err)
	}

	// Infrastructure - Venue Enrichment
	musicbrainzClient := musicbrainz.NewClient(nil, logger)
	mbSearcher := musicbrainz.NewPlaceSearcher(musicbrainzClient)
	var searchers []usecase.VenueNamedSearcher
	searchers = append(searchers, usecase.VenueNamedSearcher{Searcher: mbSearcher, AssignToMBID: true})

	if cfg.GoogleMapsAPIKey != "" {
		gmClient := googlemaps.NewClient(cfg.GoogleMapsAPIKey, nil, logger)
		gmSearcher := googlemaps.NewPlaceSearcher(gmClient)
		searchers = append(searchers, usecase.VenueNamedSearcher{Searcher: gmSearcher, AssignToMBID: false})
	}

	// Use Cases
	pushNotificationUC := usecase.NewPushNotificationUseCase(
		artistRepo,
		pushSubRepo,
		logger,
		cfg.VAPID.PublicKey,
		cfg.VAPID.PrivateKey,
		cfg.VAPID.Contact,
	)
	venueEnrichUC := usecase.NewVenueEnrichmentUseCase(venueRepo, venueRepo, venueRepo, logger, searchers...)
	concertCreationUC := usecase.NewConcertCreationUseCase(venueRepo, concertRepo, publisher, logger)

	// Event Consumers
	concertConsumer := event.NewConcertConsumer(concertCreationUC, logger)
	notificationConsumer := event.NewNotificationConsumer(artistRepo, concertRepo, pushNotificationUC, logger)
	venueConsumer := event.NewVenueConsumer(venueEnrichUC, logger)

	// Router
	router, err := messaging.NewRouter(wmLogger, publisher, "poison-queue")
	if err != nil {
		return nil, fmt.Errorf("create messaging router: %w", err)
	}

	// create-concerts publishes to multiple topics (concert.created.v1, venue.created.v1)
	// so it uses AddConsumerHandler and publishes manually via ConcertCreationUseCase.
	router.AddConsumerHandler(
		"create-concerts",
		messaging.EventTypeConcertDiscovered,
		subscriber,
		concertConsumer.Handle,
	)

	router.AddConsumerHandler(
		"notify-fans",
		messaging.EventTypeConcertCreated,
		subscriber,
		notificationConsumer.Handle,
	)

	router.AddConsumerHandler(
		"enrich-venue",
		messaging.EventTypeVenueCreated,
		subscriber,
		venueConsumer.Handle,
	)

	// Health probe server for Kubernetes readiness/liveness checks.
	healthSrv := server.NewHealthServer(":8081")

	// Register shutdown phases.
	// Drain: health → 503. The Watermill Router is NOT registered here
	// because Router.Run(ctx) internally closes the router when ctx is
	// cancelled, making an explicit Drain-phase Close redundant (and noisy).
	shutdown.Init(logger)
	shutdown.AddDrainPhase(healthSrv)
	shutdown.AddFlushPhase(publisher)
	shutdown.AddExternalPhase(musicbrainzClient)
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	return &ConsumerApp{
		Router:          router,
		HealthServer:    healthSrv,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}, nil
}
