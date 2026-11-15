package di

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/nats-io/nats.go"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/mail"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
)

// ConsumerApp represents the event consumer application with a Watermill Router.
type ConsumerApp struct {
	Router          *message.Router
	Logger          *logging.Logger
	ShutdownTimeout time.Duration
	// Health reflects whether the consumer is actively consuming (NATS
	// connected + all durables bound + router running). The entry point wires
	// it into the liveness probe so a wedged pod is restarted.
	Health *messaging.ConsumerHealth
}

// behaviorEntry pairs a behavior name with the subjects it subscribes to and
// the handler function that processes messages. One entry = one JetStream
// durable consumer with durable == deliver_group == behavior name. A behavior
// that acts on several states of one entity lists their subjects, all of one
// stream; the durable's filter (NACK, filterSubjects) delivers exactly those,
// and the handler tells them apart by the subject (design decision 2 of
// align-event-names-with-entities).
type behaviorEntry struct {
	behavior string
	subjects []string
	handler  message.NoPublishHandlerFunc
}

// on lists the subjects of a behaviorEntry.
func on(subjects ...string) []string { return subjects }

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

	// Infrastructure - Messaging
	wmLogger, goChannel, publisher, err := provideMessaging(cfg.NATS, logger)
	if err != nil {
		return nil, err
	}

	// consumerHealth reflects real consumption into the liveness probe: the NATS
	// subscriber updates connection + per-behavior bound state, and the router
	// probe (set below) reports whether the router is running.
	consumerHealth := messaging.NewConsumerHealth()

	b, err := newConsumerBuilder(ctx, cfg, db, logger)
	if err != nil {
		return nil, err
	}
	behaviorTable, externals, err := b.behaviors()
	if err != nil {
		return nil, err
	}

	router, err := provideRouter(ctx, behaviorTable, cfg.NATS, goChannel, wmLogger, publisher, consumerHealth, logger)
	if err != nil {
		return nil, err
	}

	// Register shutdown phases.
	shutdown.Init(logger)
	shutdown.AddFlushPhase(publisher)
	shutdown.AddExternalPhase(externals...)
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	// Goroutine-leak detection (Go 1.27): internal pprof listener + sampler.
	startGoroutineLeakDetection(ctx, cfg.GoroutineLeak, cfg.Telemetry.ServiceName, logger)

	return &ConsumerApp{
		Router:          router,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Health:          consumerHealth,
	}, nil
}

// notificationMailers are the senders of the emails the consumer's
// notification usecase sends.
type notificationMailers struct {
	confirmation     entity.OrderMailer
	resaleSold       entity.ResaleListingSoldEmailSender
	lotteryLoss      entity.LotteryLossEmailSender
	resaleEntryEnded entity.ResaleEntryEndedEmailSender
}

// provideNotificationMailers builds the senders of the purchase confirmation
// and of the sold, loss and ended emails. Each goes through Postmark when its
// server token is configured; otherwise a no-op sender reports Unavailable.
func provideNotificationMailers(pm config.PostmarkConfig, orders mail.OrderStore, logger *logging.Logger) notificationMailers {
	m := notificationMailers{
		confirmation:     mail.NewNoopOrderMailer(logger),
		resaleSold:       mail.NewNoopResaleSoldSender(logger),
		lotteryLoss:      mail.NewNoopLotteryLossSender(logger),
		resaleEntryEnded: mail.NewNoopResaleEntryEndedSender(logger),
	}
	if pm.ServerToken == "" {
		return m
	}
	m.confirmation = mail.NewPostmarkOrderMailer(orders, pm.ServerToken, pm.FromAddress, pm.MessageStream, time.Now, logger)
	m.resaleSold = mail.NewPostmarkResaleSoldSender(pm.ServerToken, pm.FromAddress, pm.MessageStream, logger)
	m.lotteryLoss = mail.NewPostmarkLotteryLossSender(pm.ServerToken, pm.FromAddress, pm.MessageStream, logger)
	m.resaleEntryEnded = mail.NewPostmarkResaleEntryEndedSender(pm.ServerToken, pm.FromAddress, pm.MessageStream, logger)
	return m
}

// provideMessaging builds the publisher of the consumer processes. Local
// development (no NATS URL) publishes on an in-memory GoChannel, which the
// handlers also subscribe to and which is returned for them; otherwise it is
// nil.
func provideMessaging(natsCfg config.NATSConfig, logger *logging.Logger) (watermill.LoggerAdapter, *gochannel.GoChannel, message.Publisher, error) {
	wmLogger := watermill.NewSlogLogger(logger.Slog())
	var goChannel *gochannel.GoChannel
	if natsCfg.URL == "" {
		goChannel = gochannel.NewGoChannel(gochannel.Config{
			OutputChannelBuffer: 256,
		}, wmLogger)
	}
	publisher, err := messaging.NewPublisher(natsCfg, wmLogger, goChannel, logger)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create messaging publisher: %w", err)
	}
	return wmLogger, goChannel, publisher, nil
}

// provideRouter builds the router of a consumer process and binds the
// behaviors of table to it (bindBehaviors).
//
// The router probe reports whether the router is running: IsClosed reports
// true only after the router has been closed — including when watermill
// closes it because all handlers stopped (a wedge). It stays false during the
// pre-start window (the entry point runs the router in a goroutine after
// wiring), so this probe does not false-negative at boot; readiness gates
// traffic during initialization. A closed router makes liveness unhealthy so
// Kubernetes restarts the wedged pod.
func provideRouter(ctx context.Context, table []behaviorEntry, natsCfg config.NATSConfig, goChannel *gochannel.GoChannel, wmLogger watermill.LoggerAdapter, publisher message.Publisher, health *messaging.ConsumerHealth, logger *logging.Logger) (*message.Router, error) {
	router, err := messaging.NewRouter(wmLogger, publisher, messaging.PoisonQueueSubject)
	if err != nil {
		return nil, fmt.Errorf("create messaging router: %w", err)
	}
	health.SetRouterProbe(func() bool {
		return !router.IsClosed()
	})
	if err := bindBehaviors(ctx, router, table, natsCfg, goChannel, wmLogger, health, logger); err != nil {
		return nil, err
	}
	return router, nil
}

// bindBehaviors registers the behaviors of table on the router.
//
// Local development (no NATS URL) uses the single GoChannel subscriber for all
// handlers. Fan-out and durable isolation are not relevant locally — all
// handlers run in-process against the same in-memory channel.
//
// Production opens the long-lived shared *nats.Conn. One TCP connection
// serves all durables; per-handler isolation comes from distinct durable
// names, not separate connections. NACK (the external operator) owns durable
// lifecycle — the app only binds to pre-existing durables. The shared
// connection is drained during shutdown (after the router has stopped
// consuming) so in-flight acks flush before the process exits.
func bindBehaviors(ctx context.Context, router *message.Router, table []behaviorEntry, natsCfg config.NATSConfig, goChannel *gochannel.GoChannel, wmLogger watermill.LoggerAdapter, health *messaging.ConsumerHealth, logger *logging.Logger) error {
	if natsCfg.URL == "" {
		addLocalHandlers(router, table, goChannel)
		return nil
	}
	sharedConn, err := messaging.ConnectNATS(ctx, natsCfg, health, logger)
	if err != nil {
		return fmt.Errorf("connect shared NATS connection: %w", err)
	}
	shutdown.AddExternalPhase(messaging.NATSConnCloser(sharedConn))
	return addNATSHandlers(router, table, sharedConn, wmLogger, health)
}

// addLocalHandlers registers every behavior of table on the in-memory
// channel. A GoChannel topic is one subject, so a behavior of several
// subjects gets one handler per subject, named behavior/subject.
func addLocalHandlers(router *message.Router, table []behaviorEntry, ch *gochannel.GoChannel) {
	for _, e := range table {
		for _, subject := range e.subjects {
			name := e.behavior
			if len(e.subjects) > 1 {
				name += "/" + subject
			}
			router.AddConsumerHandler(name, subject, ch, e.handler)
		}
	}
}

// addNATSHandlers registers one pull subscriber per behavior of table. Each
// binds (bind-only, no create) to the behavior-named durable, so the router
// sees independent cursors. The durable's own filter selects the subjects it
// receives; the subscriber only needs one of them to find the stream, which
// every subject of an entry shares.
func addNATSHandlers(router *message.Router, table []behaviorEntry, conn *nats.Conn, wmLogger watermill.LoggerAdapter, health *messaging.ConsumerHealth) error {
	for _, e := range table {
		sub, err := messaging.NewBehaviorSubscriber(conn, e.behavior, wmLogger, health)
		if err != nil {
			return fmt.Errorf("create subscriber for behavior %q: %w", e.behavior, err)
		}
		router.AddConsumerHandler(e.behavior, e.subjects[0], sub, e.handler)
	}
	return nil
}
