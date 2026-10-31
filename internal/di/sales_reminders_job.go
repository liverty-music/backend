package di

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
)

// SalesRemindersJobApp is the dependency bundle for the sales-reminders
// CronJob. The job scans the ticket sales for due milestones and starts a
// deliver-reminder workflow for each (user, sale, stage) not yet sent.
type SalesRemindersJobApp struct {
	SalesReminderUC usecase.TicketSaleReminderUseCase
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
	ticketSaleRepo := rdb.NewTicketSaleRepository(db, newOutbox(logger))
	reminderRepo := rdb.NewTicketSaleReminderRepository(db)
	ticketJourneyRepo := rdb.NewTicketJourneyRepository(db, newOutbox(logger))
	userRepo := rdb.NewUserRepository(db, newOutbox(logger))
	seriesRepo := rdb.NewSeriesRepository(db, newOutbox(logger))

	// Each due reminder is started as a deliver-reminder workflow, which the
	// workflow worker runs.
	shutdown.Init(logger)
	workflows, err := provideWorkflows(ctx, db, logger)
	if err != nil {
		return nil, err
	}

	salesReminderUC := usecase.NewTicketSaleReminderUseCase(
		ticketSaleRepo,
		reminderRepo,
		ticketJourneyRepo,
		userRepo,
		seriesRepo,
		workflows,
		cfg.GCP.SalesReminderScanWindow(),
		logger,
	)

	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	return &SalesRemindersJobApp{
		SalesReminderUC: salesReminderUC,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}, nil
}
