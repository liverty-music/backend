package di

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	gcsstorage "github.com/liverty-music/backend/internal/infrastructure/gcp/storage"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
)

// SeriesImageConsumerApp is the event-consumer application for the series-image-consumer.
// It subscribes to SERIES_IMAGE.uploaded, generates WebP variants via libvips, and
// performs the cut-over of series_cover_images in a single transaction.
type SeriesImageConsumerApp struct {
	Router          *message.Router
	Logger          *logging.Logger
	ShutdownTimeout time.Duration
	Health          *messaging.ConsumerHealth
}

// provideImageStorer builds the GCS image storer, which reads originals and
// writes variants: nil in local development, required in production. It stops
// in the external phase of the shutdown.
func provideImageStorer(ctx context.Context, local bool, logger *logging.Logger) (*gcsstorage.GCSStorer, error) {
	if local {
		return nil, nil
	}
	storer, err := gcsstorage.NewGCSStorer(ctx, logger)
	if err != nil {
		return nil, fmt.Errorf("create GCS storer: %w", err)
	}
	shutdown.AddExternalPhase(storer)
	return storer, nil
}

// provideSeriesImageUseCase builds the SeriesImageUseCase that
// SeriesImageConsumer calls: it orchestrates SeriesImageUseCase.Process, and
// the consumer only decodes the SERIES_IMAGE.uploaded message. The image
// processor is a stub when libvips is absent; the production binary uses
// -tags vips.
func provideSeriesImageUseCase(ctx context.Context, local bool, db *rdb.Database, logger *logging.Logger) (usecase.SeriesImageUseCase, error) {
	// Infrastructure - GCS image storer (nil in local dev; required in production
	// for reading originals and writing variants).
	imageStorer, err := provideImageStorer(ctx, local, logger)
	if err != nil {
		return nil, err
	}
	return usecase.NewSeriesImageUseCase(
		rdb.NewSeriesRepository(db, newOutbox(logger)),
		rdb.NewSeriesImageRepository(db, newOutbox(logger)),
		imageStorer, event.NewSeriesImageProcessor(logger), logger), nil
}

// InitializeSeriesImageConsumerApp creates a SeriesImageConsumerApp with all dependencies wired.
func InitializeSeriesImageConsumerApp(ctx context.Context) (*SeriesImageConsumerApp, error) {
	cfg, err := config.Load[config.SeriesImageConsumerConfig]()
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

	// Infrastructure - Messaging
	wmLogger, goChannel, publisher, err := provideMessaging(cfg.NATS, logger)
	if err != nil {
		return nil, err
	}

	consumerHealth := messaging.NewConsumerHealth()

	seriesImageUC, err := provideSeriesImageUseCase(ctx, cfg.IsLocal(), db, logger)
	if err != nil {
		return nil, err
	}

	behaviorTable := []behaviorEntry{
		{"process-series-image", on(entity.SubjectSeriesImageUploaded), event.NewSeriesImageConsumer(seriesImageUC, logger).Handle},
	}

	router, err := provideRouter(ctx, behaviorTable, cfg.NATS, goChannel, wmLogger, publisher, consumerHealth, logger)
	if err != nil {
		return nil, err
	}

	shutdown.Init(logger)
	shutdown.AddFlushPhase(publisher)
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	startGoroutineLeakDetection(ctx, cfg.GoroutineLeak, cfg.Telemetry.ServiceName, logger)

	return &SeriesImageConsumerApp{
		Router:          router,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Health:          consumerHealth,
	}, nil
}
