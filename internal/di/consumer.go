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
	"github.com/nats-io/nats.go"

	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/analytics/posthog"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/liverty-music/backend/internal/infrastructure/geo"
	"github.com/liverty-music/backend/internal/infrastructure/mail"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/infrastructure/music/fanarttv"
	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	infrapayment "github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/liverty-music/backend/internal/infrastructure/repository"
	infratelemetry "github.com/liverty-music/backend/internal/infrastructure/telemetry"
	infrawebpush "github.com/liverty-music/backend/internal/infrastructure/webpush"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
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

	// Repositories
	artistRepo := rdb.NewArtistRepository(db, newOutbox(logger))
	concertRepo := rdb.NewConcertRepository(db, newOutbox(logger))
	pushSubRepo := rdb.NewPushSubscriptionRepository(db, newOutbox(logger))
	followRepo := rdb.NewFollowRepository(db, newOutbox(logger))
	ticketJourneyRepo := rdb.NewTicketJourneyRepository(db, newOutbox(logger))
	userRepo := rdb.NewUserRepository(db, newOutbox(logger))

	// Infrastructure - Messaging
	wmLogger := watermill.NewSlogLogger(logger.Slog())
	var goChannel *gochannel.GoChannel
	if cfg.NATS.URL == "" {
		goChannel = gochannel.NewGoChannel(gochannel.Config{
			OutputChannelBuffer: 256,
		}, wmLogger)
	}

	publisher, err := messaging.NewPublisher(cfg.NATS, wmLogger, goChannel, logger)
	if err != nil {
		return nil, fmt.Errorf("create messaging publisher: %w", err)
	}

	// consumerHealth reflects real consumption into the liveness probe: the NATS
	// subscriber updates connection + per-behavior bound state, and the router
	// probe (set below) reports whether the router is running.
	consumerHealth := messaging.NewConsumerHealth()

	// Infrastructure - MusicBrainz (for artist name resolution)
	extHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	musicbrainzClient := musicbrainz.NewClient(extHTTPClient, logger)

	// Infrastructure - fanart.tv (for artist image resolution)
	fanarttvClient := fanarttv.NewClient(cfg.FanartTVAPIKey, extHTTPClient, logger)

	// Use Cases
	webpushSender := infrawebpush.NewSender(cfg.VAPID.PublicKey, cfg.VAPID.PrivateKey, cfg.VAPID.Contact)
	workflows, err := provideWorkflows(ctx, db, logger)
	if err != nil {
		return nil, err
	}
	notificationRepo := rdb.NewNotificationRepository(db)
	// Purchase confirmation: the email goes through Postmark when its token is
	// configured, otherwise a no-op sender reports Unavailable.
	// The consumer never refunds an Order or reads its charge, so the Order's
	// payment provider part is the no-op.
	orderRepo := repository.NewOrderRepository(rdb.NewOrderRepository(db), infrapayment.NewNoopOrderPayments(logger))
	var confirmationSender entity.OrderMailer
	if cfg.Postmark.ServerToken != "" {
		confirmationSender = mail.NewPostmarkOrderMailer(orderRepo, cfg.Postmark.ServerToken,
			cfg.Postmark.FromAddress, cfg.Postmark.MessageStream, time.Now, logger)
	} else {
		confirmationSender = mail.NewNoopOrderMailer(logger)
	}
	// The sold email of a resold listing goes the same way.
	resaleListingRepo := rdb.NewResaleListingRepository(db, newOutbox(logger))
	var resaleSoldSender entity.ResaleListingSoldEmailSender = mail.NewNoopResaleSoldSender(logger)
	if cfg.Postmark.ServerToken != "" {
		resaleSoldSender = mail.NewPostmarkResaleSoldSender(resaleListingRepo, cfg.Postmark.ServerToken,
			cfg.Postmark.FromAddress, cfg.Postmark.MessageStream, time.Now, logger)
	}
	// The loss email of a Lost lottery entry goes the same way.
	lotteryEntryRepo := rdb.NewLotteryEntryRepository(db, newOutbox(logger))
	var lossSender entity.LotteryLossEmailSender = mail.NewNoopLotteryLossSender(logger)
	if cfg.Postmark.ServerToken != "" {
		lossSender = mail.NewPostmarkLotteryLossSender(lotteryEntryRepo, cfg.Postmark.ServerToken,
			cfg.Postmark.FromAddress, cfg.Postmark.MessageStream, time.Now, logger)
	}
	// The ended email of a Lapsed or Lost resale entry goes the same way.
	resaleEntryRepo := rdb.NewResaleEntryRepository(db, newOutbox(logger))
	var resaleEntryEndedSender entity.ResaleEntryEndedEmailSender = mail.NewNoopResaleEntryEndedSender(logger)
	if cfg.Postmark.ServerToken != "" {
		resaleEntryEndedSender = mail.NewPostmarkResaleEntryEndedSender(resaleEntryRepo, cfg.Postmark.ServerToken,
			cfg.Postmark.FromAddress, cfg.Postmark.MessageStream, time.Now, logger)
	}
	japanTime, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		return nil, fmt.Errorf("load Asia/Tokyo time zone: %w", err)
	}
	notificationUC := usecase.NewNotificationUseCase(notificationRepo, pushSubRepo, webpushSender, workflows, infratelemetry.NewBusinessMetrics(), logger,
		usecase.OrderConfirmationDeps{
			Orders:            orderRepo,
			Users:             userRepo,
			Concerts:          concertRepo,
			EventOrganizer:    rdb.NewEventOrganizerRepository(db),
			Organizers:        rdb.NewOrganizerRepository(db, newOutbox(logger)),
			Mailer:            confirmationSender,
			ResaleListings:    resaleListingRepo,
			ResaleMailer:      resaleSoldSender,
			Entries:           lotteryEntryRepo,
			Stocks:            rdb.NewTicketStockRepository(db),
			LossMailer:        lossSender,
			ResaleEntries:     resaleEntryRepo,
			ResaleEntryMailer: resaleEntryEndedSender,
			Tickets:           rdb.NewTicketRepository(db, newOutbox(logger)),
			TimeZone:          japanTime,
		},
		usecase.NewConcertsDeps{ArtistRepo: artistRepo, ConcertRepo: concertRepo, FollowRepo: followRepo},
		usecase.SubjectDeps{
			Users:         userRepo,
			Follows:       followRepo,
			Artists:       artistRepo,
			Concerts:      concertRepo,
			Orders:        orderRepo,
			TicketSales:   rdb.NewTicketSaleRepository(db, newOutbox(logger)),
			Entries:       lotteryEntryRepo,
			ResaleEntries: resaleEntryRepo,
		})
	ticketJourneyUC := usecase.NewTicketJourneyUseCase(ticketJourneyRepo, userRepo, concertRepo, followRepo, lotteryEntryRepo, logger)
	concertCandidateRepo := rdb.NewConcertCandidateRepository(db)
	venueRepo := rdb.NewVenueRepository(db)
	seriesRepo := rdb.NewSeriesRepository(db, newOutbox(logger))
	// Infrastructure - PostHog analytics client (optional, nil in local dev).
	analyticsClient, err := provideAnalyticsClient(cfg.PostHog, cfg.Telemetry, logger)
	if err != nil {
		return nil, err
	}

	// The consumer calls only ArtistUseCase.Refresh, which needs the
	// repository, the catalog and the clock; the chart lookups and their cache
	// belong to the API server.
	artistUC := usecase.NewArtistUseCase(artistRepo, nil, nil, musicbrainzClient, nil, time.Now, logger)
	artistImageSyncUC := usecase.NewArtistImageSyncUseCase(artistRepo, fanarttvClient, logger)

	// ConcertUseCase: needed here (not just by the RPC server) so the
	// search-first-followed-artist consumer can call
	// SearchNewConcertsOnFirstFollow when FOLLOW.created fires.
	organizerRepo := rdb.NewOrganizerRepository(db, newOutbox(logger))
	concertSearchRepo := rdb.NewConcertSearchRepository(db)
	rejectedConcertRepo := rdb.NewRejectedConcertRepository(db)
	centroidResolver := geo.NewCentroidResolver()
	var geminiSearcher entity.ConcertSearcher
	if cfg.GCP.GeminiSearchAPIKey != "" {
		searcher, err := gemini.NewConcertSearcher(ctx, gemini.Config{
			APIKey:          cfg.GCP.GeminiSearchAPIKey,
			Model:           cfg.GCP.ConcertSearchModel(),
			ThinkingLevel:   cfg.GCP.ConcertSearchThinkingLevel(),
			OmitTemperature: true,

			IncludeServerSideToolInvocations: true,
			Reporter:                         posthog.NewGenerationReporter(analyticsClient, logger),
		}, extHTTPClient, gemini.NewOfficialPageClient(), logger)
		if err != nil {
			return nil, fmt.Errorf("create gemini concert searcher: %w", err)
		}
		geminiSearcher = searcher
	}
	concertUC := usecase.NewConcertUseCase(
		artistRepo,
		concertRepo,
		venueRepo,
		seriesRepo,
		organizerRepo,
		concertSearchRepo,
		concertCandidateRepo,
		rejectedConcertRepo,
		geminiSearcher,
		centroidResolver,
		workflows,
		infratelemetry.NewBusinessMetrics(),
		analyticsClient,
		cfg.GCP.SearchCacheTTL(),
		cfg.GCP.SearchDiscoveryWindow(),
		logger,
	)

	// The TICKET_SALE.discovered consumer's use case. AnnounceDiscoveredSale
	// starts a deliver-notification workflow per recipient, so it depends on
	// Workflows, not NotificationUseCase.
	ticketSaleAnnouncementUC := usecase.NewTicketSaleAnnouncementUseCase(
		userRepo,
		ticketJourneyRepo,
		workflows,
		logger,
	)

	// The consumer's UserUseCase starts the verification and passkey change
	// emails, which the workflow worker sends; it never deletes users.
	userUC := usecase.NewUserUseCase(userRepo, usecase.DeletionBlockerReads{}, workflows, nil, nil, nil,
		usecase.PasskeyMailers{}, logger)

	// Event Consumers
	notificationConsumer := event.NewNotificationConsumer(notificationUC, logger)
	artistRefreshConsumer := event.NewArtistRefreshConsumer(artistUC, logger)
	artistImageConsumer := event.NewArtistImageConsumer(artistImageSyncUC, logger)
	userConsumer := event.NewUserConsumer(userUC, logger)
	analyticsConsumerMetrics := infratelemetry.NewOTelAnalyticsConsumerMetrics()
	analyticsConsumer := event.NewAnalyticsConsumer(analyticsClient, analyticsConsumerMetrics, orderRepo, rdb.NewTicketStockRepository(db), logger)
	poisonConsumer := event.NewPoisonConsumer(logger)
	ticketSaleAnnouncementConsumer := event.NewTicketSaleAnnouncementConsumer(ticketSaleAnnouncementUC, logger)
	followSearchConsumer := event.NewFollowSearchConsumer(concertUC, logger)
	ticketStockRepo := rdb.NewTicketStockRepository(db)
	orderPaidConsumer := event.NewOrderPaidConsumer(notificationUC, ticketJourneyUC, ticketStockRepo, logger)
	lotteryEntryEnteredConsumer := event.NewLotteryEntryEnteredConsumer(ticketJourneyUC, ticketStockRepo, logger)
	resaleListingRefundedConsumer := event.NewResaleListingRefundedConsumer(notificationUC, logger)
	passkeyConsumer := event.NewPasskeyConsumer(userUC, logger)
	// The cancellation and refund notices: the consumer only starts their
	// workflows, which the workflow worker runs.
	cancellationNoticeConsumer := event.NewCancellationNoticeConsumer(notificationUC, logger)
	// The consumer's LotteryEntryUseCase only ends the Entered entries of a
	// cancelled Event (EndForCancelledEvent), which needs the entries alone;
	// the release and the notice follow from the announcement of each.
	lotteryCancellationConsumer := event.NewLotteryCancellationConsumer(usecase.NewLotteryEntryUseCase(usecase.LotteryEntryDeps{
		EntryRepo: lotteryEntryRepo,
		Clock:     time.Now,
		Logger:    logger,
	}), logger)
	// The consumer's OrderUseCase only starts the workflow that follows each
	// Won or Lost entry; the workflow worker runs it.
	lotteryEntryResultConsumer := event.NewLotteryEntryResultConsumer(usecase.NewOrderUseCase(usecase.OrderDeps{
		Workflows: workflows,
		Clock:     time.Now,
		Logger:    logger,
	}), notificationUC, logger)
	// The resale entry's workflow starter and ended notice, and the start of a
	// sold listing's refund; the workflow worker runs the workflows.
	resaleEntryConsumer := event.NewResaleEntryConsumer(usecase.NewOrderUseCase(usecase.OrderDeps{
		Workflows: workflows,
		Clock:     time.Now,
		Logger:    logger,
	}), notificationUC, logger)
	resaleListingSoldConsumer := event.NewResaleListingSoldConsumer(usecase.NewResaleListingUseCase(usecase.ResaleListingDeps{
		Workflows: workflows,
		Clock:     time.Now,
		Logger:    logger,
	}), logger)
	// The consumer's CheckoutUseCase only starts the workflow that follows
	// each announced Checkout; the workflow worker runs it.
	checkoutConsumer := event.NewCheckoutConsumer(usecase.NewCheckoutUseCase(usecase.CheckoutDeps{
		Workflows: workflows,
		Clock:     time.Now,
		Logger:    logger,
	}), logger)

	// behaviorTable is the canonical behavior → subjects → handler mapping.
	// Each row becomes one independent JetStream durable consumer with
	// durable == deliver_group == behavior name, declared by a Consumer CR in
	// cloud-provisioning k8s/namespaces/nats/base/jetstream/. Two rows on the
	// same subject (e.g. SubjectArtistCreated, SubjectUserCreated) produce
	// independent durables — each receives every message on that subject.
	behaviorTable := []behaviorEntry{
		{"notify-concert", on(entity.SubjectConcertCreated), notificationConsumer.Handle},
		{"send-event-cancelled", on(entity.SubjectEventCancelled), cancellationNoticeConsumer.HandleEventCancelled},
		{"end-lottery-entries-of-cancelled-event", on(entity.SubjectEventCancelled), lotteryCancellationConsumer.HandleEventCancelled},
		{"refresh-artist", on(entity.SubjectArtistCreated), artistRefreshConsumer.Handle},
		{"resolve-artist-image", on(entity.SubjectArtistCreated), artistImageConsumer.Handle},
		{"track-artist-represented", on(entity.SubjectArtistRepresented), analyticsConsumer.HandleArtistRepresented},
		{"search-first-followed-artist", on(entity.SubjectFollowCreated), followSearchConsumer.Handle},
		{"track-follow-created", on(entity.SubjectFollowCreated), analyticsConsumer.HandleFollowCreated},
		{"track-follow-removed", on(entity.SubjectFollowRemoved), analyticsConsumer.HandleFollowRemoved},
		{"track-follow-hype-changed", on(entity.SubjectFollowHypeChanged), analyticsConsumer.HandleFollowHypeChanged},
		{"verify-user-email", on(entity.SubjectUserCreated), userConsumer.Handle},
		{"track-user-created", on(entity.SubjectUserCreated), analyticsConsumer.HandleUserCreated},
		{"track-user-signed-in", on(entity.SubjectUserSignedIn), analyticsConsumer.HandleUserSignedIn},
		{"notify-passkey-added", on(entity.SubjectPasskeyAdded), passkeyConsumer.HandlePasskeyAdded},
		{"notify-passkey-removed", on(entity.SubjectPasskeyRemoved), passkeyConsumer.HandlePasskeyRemoved},
		{"track-push-subscription-subscribed", on(entity.SubjectPushSubscriptionSubscribed), analyticsConsumer.HandlePushSubscriptionSubscribed},
		{"track-push-subscription-unsubscribed", on(entity.SubjectPushSubscriptionUnsubscribed), analyticsConsumer.HandlePushSubscriptionUnsubscribed},
		{"notify-ticket-sale-discovered", on(entity.SubjectTicketSaleDiscovered), ticketSaleAnnouncementConsumer.Handle},
		{"track-ticket-journey", on(entity.SubjectTicketJourneyStatusChanged), analyticsConsumer.HandleTicketJourneyStatusChanged},
		{"track-organizer-activated", on(entity.SubjectOrganizerActivated), analyticsConsumer.HandleOrganizerActivated},
		{"send-order-confirmation", on(entity.SubjectOrderPaid), orderPaidConsumer.HandleSendOrderConfirmation},
		{"mark-ticket-journey-paid", on(entity.SubjectOrderPaid), orderPaidConsumer.HandleMarkPaid},
		{"track-order-paid", on(entity.SubjectOrderPaid), analyticsConsumer.HandleOrderPaid},
		{"send-order-refunded", on(entity.SubjectOrderRefunded), cancellationNoticeConsumer.HandleOrderRefunded},
		{"start-resale-refund", on(entity.SubjectResaleListingSold), resaleListingSoldConsumer.HandleStartRefund},
		{"send-resale-sold", on(entity.SubjectResaleListingRefunded), resaleListingRefundedConsumer.Handle},
		{"mark-ticket-journey-applied", on(entity.SubjectLotteryEntryEntered), lotteryEntryEnteredConsumer.HandleMarkApplied},
		{"start-lottery-entry-workflow", on(entity.SubjectLotteryEntryWon, entity.SubjectLotteryEntryLost), lotteryEntryResultConsumer.HandleStartWorkflow},
		{"track-lottery-entry-result", on(entity.SubjectLotteryEntryWon, entity.SubjectLotteryEntryLost), analyticsConsumer.HandleLotteryEntryResult},
		{"send-lottery-loss", on(entity.SubjectLotteryEntryLost), lotteryEntryResultConsumer.HandleSendLotteryLoss},
		{"start-resale-entry-workflow", on(entity.SubjectResaleEntryWon, entity.SubjectResaleEntryLapsed, entity.SubjectResaleEntryLost, entity.SubjectResaleEntryWithdrawn), resaleEntryConsumer.HandleStartWorkflow},
		{"send-resale-entry-ended", on(entity.SubjectResaleEntryLapsed, entity.SubjectResaleEntryLost), resaleEntryConsumer.HandleSendResaleEntryEnded},
		{"start-checkout-expiry", on(entity.SubjectCheckoutHeld), checkoutConsumer.HandleStartWorkflow},
		{"release-expired-checkout", on(entity.SubjectCheckoutExpired), checkoutConsumer.HandleStartWorkflow},
		{"track-checkout-expired", on(entity.SubjectCheckoutExpired), analyticsConsumer.HandleCheckoutExpired},
		{"release-released-checkout", on(entity.SubjectCheckoutReleased), checkoutConsumer.HandleStartWorkflow},
		{"track-ticket-admitted", on(entity.SubjectTicketAdmitted), analyticsConsumer.HandleTicketAdmitted},
		{"log-poison", on(messaging.PoisonQueueSubject), poisonConsumer.Handle},
	}

	// Router
	router, err := messaging.NewRouter(wmLogger, publisher, messaging.PoisonQueueSubject)
	if err != nil {
		return nil, fmt.Errorf("create messaging router: %w", err)
	}

	// IsClosed reports true only after the router has been closed — including
	// when watermill closes it because all handlers stopped (a wedge). It stays
	// false during the pre-start window (the entry point runs the router in a
	// goroutine after wiring), so this probe does not false-negative at boot;
	// readiness gates traffic during initialization. A closed router makes
	// liveness unhealthy so Kubernetes restarts the wedged pod.
	consumerHealth.SetRouterProbe(func() bool {
		return !router.IsClosed()
	})

	if cfg.NATS.URL == "" {
		// Local development: use the single GoChannel subscriber for all
		// handlers. Fan-out and durable isolation are not relevant locally —
		// all handlers run in-process against the same in-memory channel.
		addLocalHandlers(router, behaviorTable, goChannel)
	} else {
		// Production NATS path.
		//
		// Open the long-lived shared *nats.Conn. One TCP connection serves all
		// durables; per-handler isolation comes from distinct durable names, not
		// separate connections. NACK (the external operator) owns durable
		// lifecycle — the app only binds to pre-existing durables.
		sharedConn, err := messaging.ConnectNATS(ctx, cfg.NATS, consumerHealth, logger)
		if err != nil {
			return nil, fmt.Errorf("connect shared NATS connection: %w", err)
		}
		// Drain the shared connection during shutdown (after the router has
		// stopped consuming) so in-flight acks flush before the process exits.
		shutdown.AddExternalPhase(messaging.NATSConnCloser(sharedConn))

		if err := addNATSHandlers(router, behaviorTable, sharedConn, wmLogger, consumerHealth); err != nil {
			return nil, err
		}
	}

	// Register shutdown phases.
	shutdown.Init(logger)
	shutdown.AddFlushPhase(publisher)
	shutdown.AddExternalPhase(musicbrainzClient)
	shutdown.AddExternalPhase(fanarttvClient)
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
