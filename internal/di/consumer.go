package di

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	googlemaps "github.com/liverty-music/backend/internal/infrastructure/maps/google"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
)

// ConsumerApp represents the event consumer application with a Watermill Router.
type ConsumerApp struct {
	Router  *message.Router
	Logger  *logging.Logger
	closers []io.Closer
}

// Shutdown closes all resources held by the consumer application.
func (a *ConsumerApp) Shutdown(ctx context.Context) error {
	a.Logger.Info(ctx, "starting consumer shutdown")

	var errs error
	for _, closer := range a.closers {
		if err := closer.Close(); err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to close resource: %w", err))
		}
	}

	if errs != nil {
		return errs
	}

	a.Logger.Info(ctx, "consumer shutdown complete")
	return nil
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

	// Event Handlers
	concertHandler := event.NewConcertHandler(venueRepo, concertRepo, publisher, logger)
	notificationHandler := event.NewNotificationHandler(artistRepo, concertRepo, pushNotificationUC, logger)
	venueHandler := event.NewVenueHandler(venueEnrichUC, logger)

	// Router
	router, err := messaging.NewRouter(wmLogger, publisher, "poison-queue")
	if err != nil {
		return nil, fmt.Errorf("create messaging router: %w", err)
	}

	// create-concerts publishes to multiple topics (concert.created.v1, venue.created.v1)
	// so it uses AddConsumerHandler and publishes manually via the injected publisher.
	router.AddConsumerHandler(
		"create-concerts",
		messaging.EventTypeConcertDiscovered,
		subscriber,
		concertHandler.Handle,
	)

	router.AddConsumerHandler(
		"notify-fans",
		messaging.EventTypeConcertCreated,
		subscriber,
		notificationHandler.Handle,
	)

	router.AddConsumerHandler(
		"enrich-venue",
		messaging.EventTypeVenueCreated,
		subscriber,
		venueHandler.Handle,
	)

	closers := []io.Closer{db, telemetryCloser, publisher, musicbrainzClient}

	return &ConsumerApp{
		Router:  router,
		Logger:  logger,
		closers: closers,
	}, nil
}
