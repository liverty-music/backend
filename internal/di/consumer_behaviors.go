package di

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/analytics/posthog"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/geo"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/infrastructure/music/fanarttv"
	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	infrapayment "github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/liverty-music/backend/internal/infrastructure/repository"
	infratelemetry "github.com/liverty-music/backend/internal/infrastructure/telemetry"
	infrawebpush "github.com/liverty-music/backend/internal/infrastructure/webpush"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
)

// consumerBuilder builds the usecases, the event consumers and the behavior
// table of the event consumer process.
type consumerBuilder struct {
	ctx             context.Context
	cfg             *config.ConsumerConfig
	db              *rdb.Database
	workflows       usecase.Workflows
	analyticsClient usecase.AnalyticsClient
	logger          *logging.Logger

	artistRepo        *rdb.ArtistRepository
	concertRepo       *rdb.ConcertRepository
	followRepo        *rdb.FollowRepository
	userRepo          *rdb.UserRepository
	ticketJourneyRepo *rdb.TicketJourneyRepository
	ticketStockRepo   *rdb.TicketStockRepository
	lotteryEntryRepo  *rdb.LotteryEntryRepository
	resaleEntryRepo   *rdb.ResaleEntryRepository
	// orderRepo: the consumer never refunds an Order or reads its charge, so
	// the Order's payment provider part is the no-op.
	orderRepo *repository.OrderRepository
}

// newConsumerBuilder creates the workflow client, the PostHog analytics client
// (optional, nil in local development) and the repositories the consumer's
// usecases share.
func newConsumerBuilder(ctx context.Context, cfg *config.ConsumerConfig, db *rdb.Database, logger *logging.Logger) (*consumerBuilder, error) {
	workflows, err := provideWorkflows(ctx, db, logger)
	if err != nil {
		return nil, err
	}
	analyticsClient, err := provideAnalyticsClient(cfg.PostHog, cfg.Telemetry, logger)
	if err != nil {
		return nil, err
	}
	return &consumerBuilder{
		ctx: ctx, cfg: cfg, db: db, workflows: workflows, analyticsClient: analyticsClient, logger: logger,

		artistRepo:        rdb.NewArtistRepository(db, newOutbox(logger)),
		concertRepo:       rdb.NewConcertRepository(db, newOutbox(logger)),
		followRepo:        rdb.NewFollowRepository(db, newOutbox(logger)),
		userRepo:          rdb.NewUserRepository(db, newOutbox(logger)),
		ticketJourneyRepo: rdb.NewTicketJourneyRepository(db, newOutbox(logger)),
		ticketStockRepo:   rdb.NewTicketStockRepository(db),
		lotteryEntryRepo:  rdb.NewLotteryEntryRepository(db, newOutbox(logger)),
		resaleEntryRepo:   rdb.NewResaleEntryRepository(db, newOutbox(logger)),
		orderRepo:         repository.NewOrderRepository(rdb.NewOrderRepository(db), infrapayment.NewNoopOrderPayments(logger)),
	}, nil
}

// consumerUseCases are the usecases the event consumers call.
type consumerUseCases struct {
	notification       usecase.NotificationUseCase
	ticketJourney      usecase.TicketJourneyUseCase
	artist             usecase.ArtistUseCase
	artistImageSync    usecase.ArtistImageSyncUseCase
	concert            usecase.ConcertUseCase
	ticketSaleAnnounce usecase.TicketSaleAnnouncementUseCase
	user               usecase.UserUseCase
}

// behaviors builds the behavior table, one row per JetStream durable consumer.
// It also returns the external clients to close in the external phase of the
// shutdown.
func (b *consumerBuilder) behaviors() ([]behaviorEntry, []io.Closer, error) {
	// Infrastructure - MusicBrainz (for artist name resolution)
	extHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	musicbrainzClient := musicbrainz.NewClient(extHTTPClient, b.logger)

	// Infrastructure - fanart.tv (for artist image resolution)
	fanarttvClient := fanarttv.NewClient(b.cfg.FanartTVAPIKey, extHTTPClient, b.logger)

	uc, err := b.useCases(extHTTPClient, musicbrainzClient, fanarttvClient)
	if err != nil {
		return nil, nil, err
	}
	return b.table(uc), []io.Closer{musicbrainzClient, fanarttvClient}, nil
}

// useCases builds the usecases of the consumer.
func (b *consumerBuilder) useCases(extHTTPClient *http.Client, identities entity.ArtistIdentityManager, fanart entity.ArtistImageResolver) (consumerUseCases, error) {
	logger := b.logger
	ticketJourneyUC := usecase.NewTicketJourneyUseCase(b.ticketJourneyRepo, b.userRepo, b.concertRepo, b.followRepo, b.lotteryEntryRepo, logger)

	// The consumer calls only ArtistUseCase.Refresh, which needs the
	// repository, the catalog and the clock; the chart lookups and their cache
	// belong to the API server.
	artistUC := usecase.NewArtistUseCase(b.artistRepo, nil, nil, identities, nil, time.Now, logger)
	artistImageSyncUC := usecase.NewArtistImageSyncUseCase(b.artistRepo, fanart, time.Now, logger)

	concertUC, err := b.concertUseCase(extHTTPClient)
	if err != nil {
		return consumerUseCases{}, err
	}
	notificationUC, err := b.notificationUseCase()
	if err != nil {
		return consumerUseCases{}, err
	}
	return consumerUseCases{
		notification:    notificationUC,
		ticketJourney:   ticketJourneyUC,
		artist:          artistUC,
		artistImageSync: artistImageSyncUC,
		concert:         concertUC,
		// The TICKET_SALE.discovered consumer's use case. AnnounceDiscoveredSale
		// starts a deliver-notification workflow per recipient, so it depends on
		// Workflows, not NotificationUseCase.
		ticketSaleAnnounce: usecase.NewTicketSaleAnnouncementUseCase(b.userRepo, b.ticketJourneyRepo, b.workflows, logger),
		// The consumer's UserUseCase starts the verification and passkey change
		// emails, which the workflow worker sends; it never deletes users.
		user: usecase.NewUserUseCase(b.userRepo, usecase.DeletionBlockerReads{}, b.workflows, nil, nil, nil,
			usecase.PasskeyMailers{}, time.Now, logger),
	}, nil
}

// notificationUseCase builds the notification usecase: push delivery and the
// emails of purchases, sold listings and ended entries. The Postmark emails go
// through Postmark when its token is configured, otherwise a no-op sender
// reports Unavailable.
func (b *consumerBuilder) notificationUseCase() (usecase.NotificationUseCase, error) {
	cfg, db, logger := b.cfg, b.db, b.logger
	mailers := provideNotificationMailers(cfg.Postmark, logger)
	japanTime, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		return nil, fmt.Errorf("load Asia/Tokyo time zone: %w", err)
	}
	return usecase.NewNotificationUseCase(rdb.NewNotificationRepository(db), rdb.NewPushSubscriptionRepository(db, newOutbox(logger)),
		infrawebpush.NewSender(cfg.VAPID.PublicKey, cfg.VAPID.PrivateKey, cfg.VAPID.Contact), b.workflows, infratelemetry.NewBusinessMetrics(), logger,
		usecase.OrderConfirmationDeps{
			Now:               time.Now,
			Orders:            b.orderRepo,
			Users:             b.userRepo,
			Concerts:          b.concertRepo,
			EventOrganizer:    rdb.NewEventOrganizerRepository(db),
			Organizers:        rdb.NewOrganizerRepository(db, newOutbox(logger)),
			Mailer:            mailers.confirmation,
			ResaleListings:    rdb.NewResaleListingRepository(db, newOutbox(logger)),
			ResaleMailer:      mailers.resaleSold,
			Entries:           b.lotteryEntryRepo,
			Stocks:            b.ticketStockRepo,
			LossMailer:        mailers.lotteryLoss,
			ResaleEntries:     b.resaleEntryRepo,
			ResaleEntryMailer: mailers.resaleEntryEnded,
			Tickets:           rdb.NewTicketRepository(db, newOutbox(logger)),
			TimeZone:          japanTime,
		},
		usecase.NewConcertsDeps{ArtistRepo: b.artistRepo, ConcertRepo: b.concertRepo, FollowRepo: b.followRepo},
		usecase.SubjectDeps{
			Now:           time.Now,
			Users:         b.userRepo,
			Follows:       b.followRepo,
			Artists:       b.artistRepo,
			Concerts:      b.concertRepo,
			Orders:        b.orderRepo,
			TicketSales:   rdb.NewTicketSaleRepository(db, newOutbox(logger)),
			Entries:       b.lotteryEntryRepo,
			ResaleEntries: b.resaleEntryRepo,
		}), nil
}

// concertUseCase builds the ConcertUseCase: needed here (not just by the RPC
// server) so the search-first-followed-artist consumer can call
// SearchNewConcertsOnFirstFollow when FOLLOW.created fires.
func (b *consumerBuilder) concertUseCase(extHTTPClient *http.Client) (usecase.ConcertUseCase, error) {
	cfg, db, logger := b.cfg, b.db, b.logger
	geminiSearcher, err := provideConcertSearcher(b.ctx, cfg.GCP, posthog.NewGenerationReporter(b.analyticsClient, logger), extHTTPClient, logger)
	if err != nil {
		return nil, fmt.Errorf("create gemini concert searcher: %w", err)
	}
	return usecase.NewConcertUseCase(
		b.artistRepo,
		b.concertRepo,
		rdb.NewVenueRepository(db),
		rdb.NewSeriesRepository(db, newOutbox(logger)),
		rdb.NewOrganizerRepository(db, newOutbox(logger)),
		rdb.NewConcertSearchRepository(db),
		rdb.NewConcertCandidateRepository(db),
		rdb.NewRejectedConcertRepository(db),
		geminiSearcher,
		geo.NewCentroidResolver(),
		b.workflows,
		infratelemetry.NewBusinessMetrics(),
		b.analyticsClient,
		cfg.GCP.SearchCacheTTL(),
		cfg.GCP.SearchDiscoveryWindow(), time.Now,
		logger,
	), nil
}

// table builds the canonical behavior → subjects → handler mapping.
// Each row becomes one independent JetStream durable consumer with
// durable == deliver_group == behavior name, declared by a Consumer CR in
// cloud-provisioning k8s/namespaces/nats/base/jetstream/. Two rows on the
// same subject (e.g. SubjectArtistCreated, SubjectUserCreated) produce
// independent durables — each receives every message on that subject.
func (b *consumerBuilder) table(uc consumerUseCases) []behaviorEntry {
	analytics := event.NewAnalyticsConsumer(b.analyticsClient, infratelemetry.NewOTelAnalyticsConsumerMetrics(), b.orderRepo, b.ticketStockRepo, b.logger)
	return append(b.catalogueRows(uc, analytics), b.commerceRows(uc, analytics)...)
}

// catalogueRows are the behaviors of artists, concerts, follows, accounts,
// ticket sales and journeys, and of the poison queue.
func (b *consumerBuilder) catalogueRows(uc consumerUseCases, analytics *event.AnalyticsConsumer) []behaviorEntry {
	logger := b.logger
	notificationConsumer := event.NewNotificationConsumer(uc.notification, logger)
	artistRefreshConsumer := event.NewArtistRefreshConsumer(uc.artist, logger)
	artistImageConsumer := event.NewArtistImageConsumer(uc.artistImageSync, logger)
	userConsumer := event.NewUserConsumer(uc.user, logger)
	poisonConsumer := event.NewPoisonConsumer(logger)
	ticketSaleAnnouncementConsumer := event.NewTicketSaleAnnouncementConsumer(uc.ticketSaleAnnounce, logger)
	followSearchConsumer := event.NewFollowSearchConsumer(uc.concert, logger)
	passkeyConsumer := event.NewPasskeyConsumer(uc.user, logger)
	return []behaviorEntry{
		{"notify-concert", on(entity.SubjectConcertCreated), notificationConsumer.Handle},
		{"refresh-artist", on(entity.SubjectArtistCreated), artistRefreshConsumer.Handle},
		{"resolve-artist-image", on(entity.SubjectArtistCreated), artistImageConsumer.Handle},
		{"track-artist-represented", on(entity.SubjectArtistRepresented), analytics.HandleArtistRepresented},
		{"search-first-followed-artist", on(entity.SubjectFollowCreated), followSearchConsumer.Handle},
		{"track-follow-created", on(entity.SubjectFollowCreated), analytics.HandleFollowCreated},
		{"track-follow-removed", on(entity.SubjectFollowRemoved), analytics.HandleFollowRemoved},
		{"track-follow-hype-changed", on(entity.SubjectFollowHypeChanged), analytics.HandleFollowHypeChanged},
		{"verify-user-email", on(entity.SubjectUserCreated), userConsumer.Handle},
		{"track-user-created", on(entity.SubjectUserCreated), analytics.HandleUserCreated},
		{"track-user-signed-in", on(entity.SubjectUserSignedIn), analytics.HandleUserSignedIn},
		{"notify-passkey-added", on(entity.SubjectPasskeyAdded), passkeyConsumer.HandlePasskeyAdded},
		{"notify-passkey-removed", on(entity.SubjectPasskeyRemoved), passkeyConsumer.HandlePasskeyRemoved},
		{"track-push-subscription-subscribed", on(entity.SubjectPushSubscriptionSubscribed), analytics.HandlePushSubscriptionSubscribed},
		{"track-push-subscription-unsubscribed", on(entity.SubjectPushSubscriptionUnsubscribed), analytics.HandlePushSubscriptionUnsubscribed},
		{"notify-ticket-sale-discovered", on(entity.SubjectTicketSaleDiscovered), ticketSaleAnnouncementConsumer.Handle},
		{"track-ticket-journey", on(entity.SubjectTicketJourneyStatusChanged), analytics.HandleTicketJourneyStatusChanged},
		{"track-organizer-activated", on(entity.SubjectOrganizerActivated), analytics.HandleOrganizerActivated},
		{"track-ticket-admitted", on(entity.SubjectTicketAdmitted), analytics.HandleTicketAdmitted},
		{"log-poison", on(messaging.PoisonQueueSubject), poisonConsumer.Handle},
	}
}

// commerceRows are the behaviors of orders, checkouts, lottery and resale, and
// the cancellation and refund notices.
func (b *consumerBuilder) commerceRows(uc consumerUseCases, analytics *event.AnalyticsConsumer) []behaviorEntry {
	logger, workflows, notificationUC := b.logger, b.workflows, uc.notification
	orderPaidConsumer := event.NewOrderPaidConsumer(notificationUC, uc.ticketJourney, b.ticketStockRepo, logger)
	lotteryEntryEnteredConsumer := event.NewLotteryEntryEnteredConsumer(uc.ticketJourney, b.ticketStockRepo, logger)
	resaleListingRefundedConsumer := event.NewResaleListingRefundedConsumer(notificationUC, logger)
	// The cancellation and refund notices: the consumer only starts their
	// workflows, which the workflow worker runs.
	cancellationNoticeConsumer := event.NewCancellationNoticeConsumer(notificationUC, logger)
	// The consumer's LotteryEntryUseCase only ends the Entered entries of a
	// cancelled Event (EndForCancelledEvent), which needs the entries alone;
	// the release and the notice follow from the announcement of each.
	lotteryCancellationConsumer := event.NewLotteryCancellationConsumer(usecase.NewLotteryEntryUseCase(usecase.LotteryEntryDeps{
		EntryRepo: b.lotteryEntryRepo,
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
	return []behaviorEntry{
		{"send-event-cancelled", on(entity.SubjectEventCancelled), cancellationNoticeConsumer.HandleEventCancelled},
		{"end-lottery-entries-of-cancelled-event", on(entity.SubjectEventCancelled), lotteryCancellationConsumer.HandleEventCancelled},
		{"send-order-confirmation", on(entity.SubjectOrderPaid), orderPaidConsumer.HandleSendOrderConfirmation},
		{"mark-ticket-journey-paid", on(entity.SubjectOrderPaid), orderPaidConsumer.HandleMarkPaid},
		{"track-order-paid", on(entity.SubjectOrderPaid), analytics.HandleOrderPaid},
		{"send-order-refunded", on(entity.SubjectOrderRefunded), cancellationNoticeConsumer.HandleOrderRefunded},
		{"start-resale-refund", on(entity.SubjectResaleListingSold), resaleListingSoldConsumer.HandleStartRefund},
		{"send-resale-sold", on(entity.SubjectResaleListingRefunded), resaleListingRefundedConsumer.Handle},
		{"mark-ticket-journey-applied", on(entity.SubjectLotteryEntryEntered), lotteryEntryEnteredConsumer.HandleMarkApplied},
		{"start-lottery-entry-workflow", on(entity.SubjectLotteryEntryWon, entity.SubjectLotteryEntryLost), lotteryEntryResultConsumer.HandleStartWorkflow},
		{"track-lottery-entry-result", on(entity.SubjectLotteryEntryWon, entity.SubjectLotteryEntryLost), analytics.HandleLotteryEntryResult},
		{"send-lottery-loss", on(entity.SubjectLotteryEntryLost), lotteryEntryResultConsumer.HandleSendLotteryLoss},
		{"start-resale-entry-workflow", on(entity.SubjectResaleEntryWon, entity.SubjectResaleEntryLapsed, entity.SubjectResaleEntryLost, entity.SubjectResaleEntryWithdrawn), resaleEntryConsumer.HandleStartWorkflow},
		{"send-resale-entry-ended", on(entity.SubjectResaleEntryLapsed, entity.SubjectResaleEntryLost), resaleEntryConsumer.HandleSendResaleEntryEnded},
		{"start-checkout-expiry", on(entity.SubjectCheckoutHeld), checkoutConsumer.HandleStartWorkflow},
		{"release-expired-checkout", on(entity.SubjectCheckoutExpired), checkoutConsumer.HandleStartWorkflow},
		{"track-checkout-expired", on(entity.SubjectCheckoutExpired), analytics.HandleCheckoutExpired},
		{"release-released-checkout", on(entity.SubjectCheckoutReleased), checkoutConsumer.HandleStartWorkflow},
	}
}
