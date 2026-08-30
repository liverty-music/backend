package di

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	gcsstorage "github.com/liverty-music/backend/internal/infrastructure/gcp/storage"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
)

// MediaJobApp is the event-consumer application for the media-processor job.
// It subscribes to MEDIA.uploaded, generates WebP variants via libvips, and
// performs the cut-over of series_media in a single transaction.
type MediaJobApp struct {
	Router          *message.Router
	Logger          *logging.Logger
	ShutdownTimeout time.Duration
	Health          *messaging.ConsumerHealth
}

// InitializeMediaJobApp creates a MediaJobApp with all dependencies wired.
func InitializeMediaJobApp(ctx context.Context) (*MediaJobApp, error) {
	cfg, err := config.Load[config.MediaJobConfig]()
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
	seriesRepo := rdb.NewSeriesRepository(db)

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

	// Infrastructure - GCS image storer (nil in local dev; required in production
	// for reading originals and writing variants).
	var imageStorer *gcsstorage.GCSStorer
	if !cfg.IsLocal() {
		storer, err := gcsstorage.NewGCSStorer(ctx, logger)
		if err != nil {
			return nil, fmt.Errorf("create GCS storer: %w", err)
		}
		imageStorer = storer
		shutdown.AddExternalPhase(storer)
	}

	consumerHealth := messaging.NewConsumerHealth()

	// Media processor (stub when libvips absent; production binary uses -tags vips).
	processor := event.NewMediaProcessor(logger)

	// MediaConsumer
	mediaConsumer := event.NewMediaConsumer(
		seriesRepo,
		imageStorer,
		processor,
		logger,
	)

	behaviorTable := []behaviorEntry{
		{"media_uploaded", entity.SubjectMediaUploaded, mediaConsumer.Handle},
	}

	router, err := messaging.NewRouter(wmLogger, publisher, messaging.PoisonQueueSubject)
	if err != nil {
		return nil, fmt.Errorf("create messaging router: %w", err)
	}

	consumerHealth.SetRouterProbe(func() bool {
		return !router.IsClosed()
	})

	if cfg.NATS.URL == "" {
		for _, e := range behaviorTable {
			router.AddConsumerHandler(e.behavior, e.subject, goChannel, e.handler)
		}
	} else {
		desiredBehaviors := make([]string, 0, len(behaviorTable))
		for _, e := range behaviorTable {
			desiredBehaviors = append(desiredBehaviors, e.behavior)
		}
		if err := messaging.ReconcileConsumers(ctx, cfg.NATS, desiredBehaviors, logger.Slog()); err != nil {
			return nil, fmt.Errorf("reconcile NATS consumers: %w", err)
		}

		sharedConn, err := messaging.ConnectNATS(ctx, cfg.NATS, consumerHealth)
		if err != nil {
			return nil, fmt.Errorf("connect NATS: %w", err)
		}
		shutdown.AddExternalPhase(messaging.NATSConnCloser(sharedConn))

		for _, e := range behaviorTable {
			sub, err := messaging.NewBehaviorSubscriber(sharedConn, e.behavior, wmLogger, consumerHealth)
			if err != nil {
				return nil, fmt.Errorf("create subscriber for %q: %w", e.behavior, err)
			}
			router.AddConsumerHandler(e.behavior, e.subject, sub, e.handler)
		}
	}

	shutdown.Init(logger)
	shutdown.AddFlushPhase(publisher)
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	startGoroutineLeakDetection(ctx, cfg.GoroutineLeak, cfg.Telemetry.ServiceName, logger)

	return &MediaJobApp{
		Router:          router,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Health:          consumerHealth,
	}, nil
}
