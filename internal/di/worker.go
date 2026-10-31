package di

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/components/forwarder"

	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	infratelemetry "github.com/liverty-music/backend/internal/infrastructure/telemetry"
	infrawebpush "github.com/liverty-music/backend/internal/infrastructure/webpush"
	"github.com/liverty-music/backend/internal/infrastructure/workflow"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
)

// WorkerApp is the workflow worker: the only process that runs workflows,
// their queues and their schedules (OpenSpec change introduce-durable-workflows).
type WorkerApp struct {
	Runtime *workflow.Runtime
	// Forwarder delivers the announcements recorded in the outbox to NATS.
	// Nil in a local run without NATS: announcements then stay recorded.
	Forwarder       *forwarder.Forwarder
	Logger          *logging.Logger
	ShutdownTimeout time.Duration
}

// InitializeWorkerApp connects to the database, waiting for it as every
// workload does, builds the workflow runtime and registers its workflows. The
// caller calls Runtime.Launch; the worker is ready once it returns.
//
// Besides the runtime's own schedules, the worker registers the usecase
// workflows of registerWorkflows.
func InitializeWorkerApp(ctx context.Context) (*WorkerApp, error) {
	cfg, err := config.Load[config.WorkerConfig]()
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
	shutdown.Init(logger)

	telemetryCloser, err := telemetry.SetupTelemetry(ctx, cfg.Telemetry, cfg.Environment, cfg.ShutdownTimeout)
	if err != nil {
		return nil, err
	}
	shutdown.AddObservePhase(telemetryCloser)

	db, err := rdb.New(ctx, cfg.Database, cfg.IsLocal(), logger)
	if err != nil {
		return nil, err
	}

	runtime, err := workflow.NewRuntime(ctx, db.Pool.Raw(), logger)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	// Closers of one phase run at once, so the runtime stops its workflows in
	// the drain phase, while the database is still open. It also closes the
	// shared pool (DBOS closes the pool it is given); the database closes it
	// again, harmlessly, with the Cloud SQL dialer.
	shutdown.AddDrainPhase(runtime)
	shutdown.AddDatastorePhase(db)

	fwd, err := newOutboxForwarder(ctx, cfg, db, logger)
	if err != nil {
		return nil, err
	}
	if err := registerWorkflows(runtime, cfg, db, logger); err != nil {
		return nil, err
	}

	return &WorkerApp{
		Runtime:         runtime,
		Forwarder:       fwd,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}, nil
}

// registerWorkflows builds the usecases whose workflows the worker runs and
// registers each workflow by its stable name.
func registerWorkflows(rt *workflow.Runtime, cfg *config.WorkerConfig, db *rdb.Database, logger *logging.Logger) error {
	outbox := newOutbox(logger)
	notifications := usecase.NewNotificationUseCase(
		rdb.NewNotificationRepository(db, outbox),
		rdb.NewPushSubscriptionRepository(db, outbox),
		infrawebpush.NewSender(cfg.VAPID.PublicKey, cfg.VAPID.PrivateKey, cfg.VAPID.Contact),
		nil, infratelemetry.NewBusinessMetrics(), logger,
		usecase.OrderConfirmationDeps{}, usecase.NewConcertsDeps{},
	)
	workflow.Register(rt, usecase.WorkflowDeliverNotification, notifications.RunDeliver)

	reminders := usecase.NewTicketSaleReminderDeliveryUseCase(
		rdb.NewTicketSaleReminderRepository(db),
		rdb.NewNotificationRepository(db, outbox),
		rdb.NewPushSubscriptionRepository(db, outbox),
		infrawebpush.NewSender(cfg.VAPID.PublicKey, cfg.VAPID.PrivateKey, cfg.VAPID.Contact),
		infratelemetry.NewBusinessMetrics(), logger,
	)
	workflow.Register(rt, usecase.WorkflowDeliverReminder, reminders.RunDeliverReminder)

	// The worker's UserUseCase sends the verification codes and the passkey
	// recovery links the fan API requests; it starts nothing and never
	// deletes users.
	emailVerifier, err := newEmailVerifier(&cfg.AccountEmailConfig, logger)
	if err != nil {
		return err
	}
	users := usecase.NewUserUseCase(rdb.NewUserRepository(db, outbox), usecase.DeletionBlockerReads{}, nil, emailVerifier, nil, nil,
		usecase.PasskeyMailers{RegistrationLinks: newPasskeyRegistrationLinkSender(&cfg.AccountEmailConfig, logger)}, logger)
	workflow.Register(rt, usecase.WorkflowReissueEmailVerification, users.RunReissueEmailVerification)
	workflow.Register(rt, usecase.WorkflowSendPasskeyRecoveryLink, users.RunSendPasskeyRecoveryLink)
	return nil
}

// newOutboxForwarder builds the forwarder from the outbox to the NATS
// publisher, or returns nil in a local run without a NATS URL. It stops in
// the drain phase, and the publisher after it.
func newOutboxForwarder(ctx context.Context, cfg *config.WorkerConfig, db *rdb.Database, logger *logging.Logger) (*forwarder.Forwarder, error) {
	if cfg.NATS.URL == "" {
		logger.Warn(ctx, "NATS_URL is not set; recorded announcements stay in the outbox")
		return nil, nil
	}
	wmLogger := watermill.NewSlogLogger(logger.Slog())
	pub, err := messaging.NewPublisher(cfg.NATS, wmLogger, nil, logger)
	if err != nil {
		return nil, err
	}
	fwd, err := messaging.NewForwarder(db.Pool.Raw(), pub, wmLogger)
	if err != nil {
		_ = pub.Close()
		return nil, err
	}
	shutdown.AddDrainPhase(fwd)
	shutdown.AddExternalPhase(pub)
	return fwd, nil
}

// InitializeWorkflowAdmin builds the workflow client the `workflow`
// subcommands use: an admin runs them in the worker's Pod, so the writes go
// through the worker's own database identity.
func InitializeWorkflowAdmin(ctx context.Context) (*workflow.Client, *logging.Logger, error) {
	cfg, err := config.Load[config.WorkerConfig]()
	if err != nil {
		return nil, nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	// The command prints its result on stdout, so its logs go to stderr.
	logger, err := provideLogger(cfg.Logging, logging.WithWriter(os.Stderr))
	if err != nil {
		return nil, nil, err
	}
	shutdown.Init(logger)
	db, err := rdb.New(ctx, cfg.Database, cfg.IsLocal(), logger)
	if err != nil {
		return nil, nil, err
	}
	client, err := workflow.NewClient(ctx, db.Pool.Raw(), logger)
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	shutdown.AddExternalPhase(client)
	shutdown.AddDatastorePhase(db)
	return client, logger, nil
}

// provideWorkflows returns the client a workload starts, signals and awaits
// workflows through; the workflow worker runs them. It closes in the
// external phase, before the database.
func provideWorkflows(ctx context.Context, db *rdb.Database, logger *logging.Logger) (*workflow.Client, error) {
	client, err := workflow.NewClient(ctx, db.Pool.Raw(), logger)
	if err != nil {
		return nil, err
	}
	shutdown.AddExternalPhase(client)
	return client, nil
}
