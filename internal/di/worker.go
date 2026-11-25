package di

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/components/forwarder"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/mail"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	infrapayment "github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/liverty-music/backend/internal/infrastructure/repository"
	infratelemetry "github.com/liverty-music/backend/internal/infrastructure/telemetry"
	infrawebpush "github.com/liverty-music/backend/internal/infrastructure/webpush"
	"github.com/liverty-music/backend/internal/infrastructure/workflow"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
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
	if err := registerWorkflows(ctx, runtime, cfg, db, logger); err != nil {
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
func registerWorkflows(ctx context.Context, rt *workflow.Runtime, cfg *config.WorkerConfig, db *rdb.Database, logger *logging.Logger) error {
	outbox := newOutbox(logger)
	japanTime, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		return fmt.Errorf("load Asia/Tokyo time zone: %w", err)
	}
	registerNotificationWorkflows(rt, cfg, db, outbox, japanTime, logger)
	if err := registerAccountWorkflows(ctx, rt, cfg, db, outbox, japanTime, logger); err != nil {
		return err
	}
	if err := registerLotteryWorkflows(ctx, rt, cfg, db, outbox, logger); err != nil {
		return err
	}
	registerSettlementWorkflows(rt, cfg, db, logger)
	return registerCreateFromDiscovered(ctx, rt, cfg, db, outbox, logger)
}

// registerSettlementWorkflows registers the schedule that pays out the held
// Settlements whose release gate has passed (OpenSpec change
// add-prod-preview-release, design D10). It used to run as a sweeper in every
// fan API process. Without a Stripe key (a local worker) the payment adapters
// are the no-ops that fail with Unavailable, so a sweep pays nothing.
func registerSettlementWorkflows(rt *workflow.Runtime, cfg *config.WorkerConfig, db *rdb.Database, logger *logging.Logger) {
	payments := newPaymentProviders(cfg.StripeSecretKey, logger)
	uc := usecase.NewSettlementUseCase(
		repository.NewSettlementRepository(rdb.NewSettlementRepository(db), payments.settlements),
		repository.NewOrderRepository(rdb.NewOrderRepository(db), payments.orders),
		repository.NewPayoutAccountRepository(rdb.NewPayoutAccountRepository(db), payments.payoutAccounts),
		rdb.NewEventStartTimeRepository(db),
		time.Duration(cfg.SettlementDisputeBufferDays)*24*time.Hour,
		time.Now,
		logger,
	)
	workflow.RegisterSchedule(rt, usecase.WorkflowReleaseDueSettlements, usecase.ReleaseDueSettlementsSchedule, usecase.ReleaseDueSettlementsWorkflow(uc))
}

// registerNotificationWorkflows registers the delivery of a notification and
// of a sales reminder, and the cancellation and refund notices of an Order:
// read, email through Postmark (a no-op sender reporting Unavailable without
// a token), then Deliver. The worker refunds nothing, so the Order's payment
// provider part is the no-op.
func registerNotificationWorkflows(rt *workflow.Runtime, cfg *config.WorkerConfig, db *rdb.Database, outbox rdb.Announcer, japanTime *time.Location, logger *logging.Logger) {
	var orderMailer entity.OrderMailer = mail.NewNoopOrderMailer(logger)
	orderRepo := repository.NewOrderRepository(rdb.NewOrderRepository(db), infrapayment.NewNoopOrderPayments(logger))
	if pm := cfg.Postmark; pm.ServerToken != "" {
		orderMailer = mail.NewPostmarkOrderMailer(pm.ServerToken, pm.FromAddress, pm.MessageStream, logger)
	}
	subjects := usecase.SubjectDeps{
		Now:           time.Now,
		Users:         rdb.NewUserRepository(db, outbox),
		Follows:       rdb.NewFollowRepository(db, outbox),
		Artists:       rdb.NewArtistRepository(db, outbox),
		Concerts:      rdb.NewConcertRepository(db, outbox),
		Orders:        orderRepo,
		TicketSales:   rdb.NewTicketSaleRepository(db, outbox),
		Entries:       rdb.NewLotteryEntryRepository(db, outbox),
		ResaleEntries: rdb.NewResaleEntryRepository(db, outbox),
	}
	notifications := usecase.NewNotificationUseCase(
		rdb.NewNotificationRepository(db),
		rdb.NewPushSubscriptionRepository(db, outbox),
		infrawebpush.NewSender(cfg.VAPID.PublicKey, cfg.VAPID.PrivateKey, cfg.VAPID.Contact),
		nil, infratelemetry.NewBusinessMetrics(), logger,
		usecase.OrderConfirmationDeps{
			Now:      time.Now,
			Orders:   orderRepo,
			Users:    subjects.Users,
			Concerts: subjects.Concerts,
			Tickets:  rdb.NewTicketRepository(db, outbox),
			Mailer:   orderMailer,
			TimeZone: japanTime,
		}, usecase.NewConcertsDeps{}, subjects,
	)
	workflow.Register(rt, usecase.WorkflowDeliverNotification, notifications.RunDeliver)
	workflow.Register(rt, usecase.WorkflowNotifyEventCancelled, notifications.RunNotifyEventCancelled)
	workflow.Register(rt, usecase.WorkflowNotifyOrderRefunded, notifications.RunNotifyOrderRefunded)

	reminders := usecase.NewTicketSaleReminderDeliveryUseCase(
		rdb.NewTicketSaleReminderRepository(db),
		rdb.NewNotificationRepository(db),
		rdb.NewPushSubscriptionRepository(db, outbox),
		infrawebpush.NewSender(cfg.VAPID.PublicKey, cfg.VAPID.PrivateKey, cfg.VAPID.Contact),
		subjects,
		infratelemetry.NewBusinessMetrics(), logger,
	)
	workflow.Register(rt, usecase.WorkflowDeliverReminder, reminders.RunDeliverReminder)
}

// registerAccountWorkflows registers the workflows of a fan's account. The
// worker's UserUseCase sends the verification codes and the passkey recovery
// links the fan API requests, and the verification email of each new User and
// the change email of each announced Passkey; it starts nothing and never
// deletes users. The passkey use case records each passkey notice, whose
// removal waits for the run of the same passkey's addition.
func registerAccountWorkflows(ctx context.Context, rt *workflow.Runtime, cfg *config.WorkerConfig, db *rdb.Database, outbox rdb.Announcer, japanTime *time.Location, logger *logging.Logger) error {
	emailVerifier, err := newEmailVerifier(&cfg.AccountEmailConfig, logger)
	if err != nil {
		return err
	}
	changeEmails, err := newPasskeyChangeEmailSender(&cfg.AccountEmailConfig, japanTime, logger)
	if err != nil {
		return err
	}
	users := usecase.NewUserUseCase(rdb.NewUserRepository(db, outbox), rdb.NewAreaRepository(db), usecase.DeletionBlockerReads{}, nil, emailVerifier, nil, nil,
		usecase.PasskeyMailers{
			RegistrationLinks: newPasskeyRegistrationLinkSender(&cfg.AccountEmailConfig, logger),
			ChangeEmails:      changeEmails,
		}, time.Now, logger)
	workflow.Register(rt, usecase.WorkflowReissueEmailVerification, users.RunReissueEmailVerification)
	workflow.Register(rt, usecase.WorkflowSendPasskeyRecoveryLink, users.RunSendPasskeyRecoveryLink)
	workflow.Register(rt, usecase.WorkflowNotifyPasskeyChange, users.RunNotifyPasskeyChange)
	workflow.Register(rt, usecase.WorkflowSendVerification, users.RunSendVerification)

	workflows, err := provideWorkflows(ctx, db, logger)
	if err != nil {
		return err
	}
	passkeys := usecase.NewPasskeyUseCase(rdb.NewPasskeyRepository(db, outbox), rdb.NewUserRepository(db, outbox), workflows, time.Now, logger)
	workflow.Register(rt, usecase.WorkflowRecordPasskeyChange, passkeys.RunRecordChange)
	return nil
}

// registerCreateFromDiscovered registers CreateFromDiscovered, which resolves
// venues with Google Maps and reports each batch to product analytics (when
// PostHog is configured). A local worker without a GCP project leaves the
// started batches waiting.
func registerCreateFromDiscovered(ctx context.Context, rt *workflow.Runtime, cfg *config.WorkerConfig, db *rdb.Database, outbox rdb.Announcer, logger *logging.Logger) error {
	if cfg.GCPProjectID == "" {
		logger.Warn(ctx, "GCP_PROJECT_ID is not set; create-from-discovered is not run")
		return nil
	}
	placeSearcher, err := newPlaceSearcher(ctx, cfg.GCPProjectID, logger)
	if err != nil {
		return err
	}
	analyticsClient, err := provideAnalyticsClient(cfg.PostHog, cfg.Telemetry, logger)
	if err != nil {
		return err
	}
	concerts := usecase.NewConcertCreationUseCase(
		rdb.NewConcertCandidateRepository(db),
		rdb.NewVenueRepository(db),
		rdb.NewConcertRepository(db, outbox),
		rdb.NewSeriesRepository(db, outbox),
		placeSearcher,
		analyticsClient, time.Now,
		logger,
	)
	workflow.Register(rt, usecase.WorkflowCreateFromDiscovered, concerts.RunCreateFromDiscovered)
	return nil
}

// registerLotteryWorkflows registers the lottery draw, its schedule, the
// issue workflow of a Won entry or a placed Checkout, the
// release-card-authorization workflow of a Void Order, and the expiry of a
// Checkout's hold. The issue of an entry and every release call the payment
// provider from the payment queue, the issue of a Checkout from the checkout
// queue (OpenSpec changes make-lottery-draw-durable, unify-order-issuance).
func registerLotteryWorkflows(ctx context.Context, rt *workflow.Runtime, cfg *config.WorkerConfig, db *rdb.Database, outbox rdb.Announcer, logger *logging.Logger) error {
	provider := rdb.CardAuthorizationProvider(infrapayment.NewNoopCardAuthorizationProvider(logger))
	if cfg.StripeSecretKey != "" {
		provider = infrapayment.NewStripeCardAuthorizationProvider(cfg.StripeSecretKey, infrapayment.LotteryCardAuthorizationPolicy, logger)
	} else {
		logger.Warn(ctx, "STRIPE_SECRET_KEY is not set; lottery captures and releases fail with Unavailable")
	}
	w := lotteryWorkerRepos{
		db:                 db,
		outbox:             outbox,
		logger:             logger,
		cardAuthorizations: entity.CardAuthorizationRepository(rdb.NewCardAuthorizationRepository(db, provider)),
		saleRepo:           rdb.NewTicketSaleRepository(db, outbox),
		stockRepo:          rdb.NewTicketStockRepository(db),
		entryRepo:          rdb.NewLotteryEntryRepository(db, outbox),
		orderRepo:          repository.NewOrderRepository(rdb.NewOrderRepository(db), newPaymentProviders(cfg.StripeSecretKey, logger).orders),
	}
	w.registerDraws(rt)
	if err := w.registerOrderWorkflows(rt); err != nil {
		return err
	}
	w.registerCheckoutAndRefunds(rt)
	return nil
}

// lotteryWorkerRepos are the repositories the lottery, resale and order
// workflows of the worker share.
type lotteryWorkerRepos struct {
	db                 *rdb.Database
	outbox             rdb.Announcer
	logger             *logging.Logger
	cardAuthorizations entity.CardAuthorizationRepository
	saleRepo           entity.TicketSaleRepository
	stockRepo          entity.TicketStockRepository
	entryRepo          entity.LotteryEntryRepository
	orderRepo          entity.OrderRepository
}

// registerDraws registers the lottery draw and its schedule, and the official
// resale by lottery: draw-resale every minute starts the draw of each stock
// due at that time (OpenSpec change resell-tickets-by-lottery).
func (w lotteryWorkerRepos) registerDraws(rt *workflow.Runtime) {
	lottery := usecase.NewLotteryEntryUseCase(usecase.LotteryEntryDeps{
		SaleRepo:                 w.saleRepo,
		StockRepo:                w.stockRepo,
		EntryRepo:                w.entryRepo,
		OrderRepo:                w.orderRepo,
		EventRepo:                rdb.NewEventRepository(w.db),
		EventState:               rdb.NewEventPublishStateRepository(w.db),
		IdentityVerificationRepo: rdb.NewIdentityVerificationRepository(w.db),
		UserRepo:                 rdb.NewUserRepository(w.db, w.outbox),
		CardAuthorizations:       w.cardAuthorizations,
		Clock:                    time.Now,
		Logger:                   w.logger,
	})
	workflow.RegisterSchedule(rt, usecase.WorkflowDrawDueSales, usecase.DrawDueSalesSchedule, lottery.DrawDueSales)
	workflow.Register(rt, usecase.WorkflowDraw, lottery.Draw)

	resale := usecase.NewResaleEntryUseCase(usecase.ResaleEntryDeps{
		EntryRepo:  rdb.NewResaleEntryRepository(w.db, w.outbox),
		StockRepo:  w.stockRepo,
		EventRepo:  rdb.NewEventRepository(w.db),
		EventState: rdb.NewEventPublishStateRepository(w.db),
		Clock:      time.Now,
		Logger:     w.logger,
	})
	workflow.RegisterSchedule(rt, usecase.WorkflowDrawResale, usecase.DrawResaleSchedule, resale.DrawResale)
	workflow.Register(rt, usecase.WorkflowDrawResaleStock, resale.DrawStock)
}

// registerOrderWorkflows registers the issue and release workflows of the
// worker's OrderUseCase, which commits, charges and releases; it starts no
// workflow, places no order and runs no refund. A card authorization's policy
// only names the keys of its Create, so the lottery's repository also
// captures and releases a Checkout's.
func (w lotteryWorkerRepos) registerOrderWorkflows(rt *workflow.Runtime) error {
	orders := usecase.NewOrderUseCase(usecase.OrderDeps{
		IssuanceRepo:             rdb.NewIssuanceRepository(w.db, w.outbox),
		OrderRepo:                w.orderRepo,
		EntryRepo:                w.entryRepo,
		ResaleEntryRepo:          rdb.NewResaleEntryRepository(w.db, w.outbox),
		EventRepo:                rdb.NewEventRepository(w.db),
		EventState:               rdb.NewEventPublishStateRepository(w.db),
		CheckoutRepo:             rdb.NewCheckoutRepository(w.db, w.outbox),
		EventOrganizerRepo:       rdb.NewEventOrganizerRepository(w.db),
		OrganizerRepo:            rdb.NewOrganizerRepository(w.db, w.outbox),
		IdentityVerificationRepo: rdb.NewIdentityVerificationRepository(w.db),
		TicketSaleRepo:           w.saleRepo,
		StockRepo:                w.stockRepo,
		CardAuthorizations:       w.cardAuthorizations,
		Clock:                    time.Now,
		Logger:                   w.logger,
	})
	if err := rt.RegisterQueue(usecase.PaymentQueue, usecase.PaymentQueueConcurrency); err != nil {
		return err
	}
	if err := rt.RegisterQueue(usecase.CheckoutQueue, usecase.CheckoutQueueConcurrency); err != nil {
		return err
	}
	workflow.Register(rt, usecase.WorkflowIssue, orders.Issue)
	workflow.Register(rt, usecase.WorkflowReleaseCardAuthorization, orders.ReleaseCardAuthorization)
	return nil
}

// registerCheckoutAndRefunds registers the refund of a sold listing, which
// runs in its own workflow started from the sale's announcement (design D10),
// and the worker's CheckoutUseCase, which only ends holds.
func (w lotteryWorkerRepos) registerCheckoutAndRefunds(rt *workflow.Runtime) {
	resaleListings := usecase.NewResaleListingUseCase(usecase.ResaleListingDeps{
		Listings: rdb.NewResaleListingRepository(w.db, w.outbox),
		Orders:   w.orderRepo,
		Clock:    time.Now,
		Logger:   w.logger,
	})
	workflow.Register(rt, usecase.WorkflowRefundSold, resaleListings.RefundSold)

	checkouts := usecase.NewCheckoutUseCase(usecase.CheckoutDeps{
		CheckoutRepo: rdb.NewCheckoutRepository(w.db, w.outbox),
		Clock:        time.Now,
		Logger:       w.logger,
	})
	workflow.Register(rt, usecase.WorkflowExpireCheckout, checkouts.Expire)
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

// provideAPIWorkflows returns the workflow client of an API workload that
// starts workflows, and noWorkflows for one that does not: the organizer and
// reception roles have no grant on the workflow tables, which the client
// checks when it connects.
func provideAPIWorkflows(ctx context.Context, cfg *config.ServerConfig, db *rdb.Database, logger *logging.Logger) (usecase.Workflows, error) {
	if !cfg.StartsWorkflows() {
		return noWorkflows{}, nil
	}
	return provideWorkflows(ctx, db, logger)
}

// noWorkflows is the usecase.Workflows of a workload that starts none; a
// call is a wiring mistake and fails with FailedPrecondition.
type noWorkflows struct{}

var errNoWorkflows = apperr.New(codes.FailedPrecondition, "this workload starts no workflows")

// Start fails with FailedPrecondition.
func (noWorkflows) Start(context.Context, string, string, any, ...usecase.StartOption) error {
	return errNoWorkflows
}

// Send fails with FailedPrecondition.
func (noWorkflows) Send(context.Context, string, string, any) error { return errNoWorkflows }

// Await fails with FailedPrecondition.
func (noWorkflows) Await(context.Context, string, time.Duration, any) error { return errNoWorkflows }

// Cancel fails with FailedPrecondition.
func (noWorkflows) Cancel(context.Context, string) (bool, error) { return false, errNoWorkflows }

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
