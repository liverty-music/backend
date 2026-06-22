package di

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
)

// SalesRemindersJobApp is the dependency bundle for the sales-reminders
// CronJob. The job scans upcoming sales phases for due milestones and
// publishes SALES_PHASE.reminder.due events for each (user, phase, stage)
// triple not yet sent.
type SalesRemindersJobApp struct {
	SalesReminderUC usecase.SalesReminderUseCase
	Logger          *logging.Logger
	ShutdownTimeout time.Duration
}

// InitializeSalesRemindersJobApp wires the sales-reminders scan job.
func InitializeSalesRemindersJobApp(ctx context.Context) (*SalesRemindersJobApp, error) {
	cfg, err := config.Load[config.JobConfig]()
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
	salesPhaseRepo := rdb.NewSalesPhaseRepository(db)
	reminderRepo := rdb.NewSalesPhaseReminderRepository(db)
	ticketJourneyRepo := rdb.NewTicketJourneyRepository(db)
	userRepo := rdb.NewUserRepository(db)

	// Messaging
	//
	// Fail fast in non-local environments: a missing NATS_URL would silently
	// route published events to an in-process GoChannel that nothing consumes,
	// dropping every event. Local development still falls back to the
	// in-process GoChannel below.
	if !cfg.IsLocal() && cfg.NATS.URL == "" {
		return nil, fmt.Errorf("NATS_URL is required for the sales-reminders job in non-local environments")
	}
	if err := messaging.EnsureStreams(ctx, cfg.NATS); err != nil {
		return nil, fmt.Errorf("ensure NATS streams: %w", err)
	}
	wmLogger := watermill.NewSlogLogger(logger.Slog())
	var goChannel *gochannel.GoChannel
	if cfg.NATS.URL == "" {
		goChannel = gochannel.NewGoChannel(gochannel.Config{OutputChannelBuffer: 256}, wmLogger)
	}
	publisher, err := messaging.NewPublisher(cfg.NATS, wmLogger, goChannel)
	if err != nil {
		return nil, fmt.Errorf("create messaging publisher: %w", err)
	}
	eventPublisher := messaging.NewEventPublisher(publisher)

	salesReminderUC := usecase.NewSalesReminderUseCase(
		salesPhaseRepo,
		reminderRepo,
		ticketJourneyRepo,
		userRepo,
		eventPublisher,
		cfg.GCP.SalesReminderScanWindow(),
		logger,
	)

	shutdown.Init(logger)
	shutdown.AddFlushPhase(publisher)
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	return &SalesRemindersJobApp{
		SalesReminderUC: salesReminderUC,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}, nil
}
