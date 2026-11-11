package rdb_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/liverty-music/backend/internal/infrastructure/repository"
	"github.com/liverty-music/backend/pkg/eventmsg"

	"uuid"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/workflow"
	"github.com/liverty-music/backend/internal/usecase"
)

// The story tests of stories/win-tickets-in-a-lottery run the fan's whole
// lottery journey against a real local Postgres, the real workflow runtime
// and an in-memory payment provider (fakeCards): the Organizer stocks a
// SeatType for the event with SeatTypeUseCase.Create and
// TicketStockUseCase.Create and creates the sale with
// TicketSaleUseCase.Create, fans enter with LotteryEntryUseCase AuthorizeCard
// and Confirm (card authentication stands in as fakeCards.authenticate), and
// once the injected clock passes the window's end the workflow worker runs
// draw-due-sales and the draw. The announcements the draw records are handed
// to the LOTTERY_ENTRY.won and LOTTERY_ENTRY.lost consumer, which starts the
// issue and release workflows the worker runs and sends the loss emails; the
// payment_intent.succeeded webhook and the ORDER.paid consumer finish the
// win. The Stripe API itself is covered by TestLotteryPipeline_Integration
// and the card authorization repository's integration test.
//
// An entry is stored only before the sale's end time by the database clock,
// so the story's windows are placed after the real current time.

// storyFanName and storyFanPhone are the personal details every fan gives
// when authorizing a card.
const (
	storyFanName  = "山田 花子"
	storyFanPhone = "+819012345678"
)

// storyWait bounds every wait on a workflow, so a broken runtime fails the
// story instead of hanging it.
const storyWait = 60 * time.Second

// storyWindowStart is when the story's first sale opens: two days after the
// real current time, so every window the stories open ends after the database
// clock.
var storyWindowStart = time.Now().UTC().Truncate(time.Hour).Add(48 * time.Hour)

// storyEventStart is the start time of the story's event: well after every
// sale window the stories open.
var storyEventStart = storyWindowStart.Add(30 * 24 * time.Hour)

// lotteryStory wires the real use cases over the real repositories, the
// workflow runtime and the in-memory payment provider, all reading one
// injected clock.
type lotteryStory struct {
	// db is the database of the test.
	db          *rdb.Database
	t           *testing.T
	ctx         context.Context
	now         time.Time
	logger      *logging.Logger
	organizerID string
	seriesID    string
	eventID     string
	saleIDs     []entity.TicketSaleID

	cards *fakeCards
	// checkoutCards captures and releases the card authorizations the
	// in-memory provider did not open: the first-come stories' Stripe ones.
	checkoutCards *routedCards
	mailer        *storyMailer
	pushes        *pushStarts

	seatTypes     usecase.SeatTypeUseCase
	stocks        usecase.TicketStockUseCase
	sales         usecase.TicketSaleUseCase
	lottery       usecase.LotteryEntryUseCase
	orders        usecase.OrderUseCase
	tickets       usecase.TicketUseCase
	journeys      usecase.TicketJourneyUseCase
	webhooks      usecase.StripeWebhookService
	notifications usecase.NotificationUseCase
	drawn         *event.LotteryEntryResultConsumer
	// expiry is the worker's CheckoutUseCase; checkoutStarter starts the
	// workflows that follow an announced Checkout, as the event consumer does.
	expiry          usecase.CheckoutUseCase
	checkoutStarter *event.CheckoutConsumer
	// series is the organizer console's SeriesUseCase, which cancels an
	// Event; lotteryCancellation is the consumer that ends the lottery
	// entries of each Event announced cancelled.
	series              usecase.SeriesUseCase
	lotteryCancellation *event.LotteryCancellationConsumer

	// worker runs the workflows; client starts and awaits them, as the
	// event consumer does.
	worker *workflow.Runtime
	client *workflow.Client
	// drawnMark, paidMark and enteredMark are the outbox offsets up to which
	// the story handed announcements to their consumers.
	drawnMark, paidMark, enteredMark, checkoutMark, cancelledMark int64
	// followUps are the issue and release workflows started for drawn
	// entries and not awaited yet.
	followUps []string

	// resaleWorker is the worker's ResaleEntryUseCase, which draws; and
	// resaleRefunds the worker's ResaleListingUseCase, which refunds a sold
	// listing through refundOrders (the Stripe test API when a test key is
	// set).
	resaleWorker  usecase.ResaleEntryUseCase
	resaleRefunds usecase.ResaleListingUseCase

	orderRepo   entity.OrderRepository
	userRepo    *rdb.UserRepository
	journeyRepo *rdb.TicketJourneyRepository
}

// newLotteryStory starts the workflow worker on db, the database of the
// test, and seeds an Organizer with complete business details and one published, timed event
// of its Series with a per-account limit of 4.
func newLotteryStory(t *testing.T, db *rdb.Database) *lotteryStory {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	s := &lotteryStory{
		db:  db,
		t:   t,
		ctx: context.Background(),
		// The Organizer sets the sale up a day before it opens.
		now:           storyWindowStart.Add(-24 * time.Hour),
		logger:        logger,
		cards:         newFakeCards(db),
		pushes:        &pushStarts{},
		drawnMark:     outboxMark(t, db),
		paidMark:      outboxMark(t, db),
		enteredMark:   outboxMark(t, db),
		checkoutMark:  outboxMark(t, db),
		cancelledMark: outboxMark(t, db),
		orderRepo:     orderRepository(t, rdb.NewOrderRepository(db)),
		userRepo:      rdb.NewUserRepository(db, testOutbox),
		journeyRepo:   rdb.NewTicketJourneyRepository(db, testOutbox),
	}
	s.seedOrganizerEvent()
	s.checkoutCards = &routedCards{fake: s.cards}
	cardAuthorizations := rdb.NewCardAuthorizationRepository(db, s.checkoutCards)

	clock := usecase.Clock(func() time.Time { return s.now })
	saleRepo := rdb.NewTicketSaleRepository(db, testOutbox)
	stockRepo := rdb.NewTicketStockRepository(db)
	seatTypeRepo := rdb.NewSeatTypeRepository(db)
	entryRepo := rdb.NewLotteryEntryRepository(db, testOutbox)
	eventRepo := rdb.NewEventRepository(db)
	seriesRepo := rdb.NewSeriesRepository(db, testOutbox)
	ticketRepo := rdb.NewTicketRepository(db, testOutbox)
	organizerRepo := rdb.NewOrganizerRepository(db, testOutbox)
	eventOrganizerRepo := rdb.NewEventOrganizerRepository(db)
	eventState := rdb.NewEventPublishStateRepository(db)
	resaleEntryRepo := rdb.NewResaleEntryRepository(db, testOutbox)
	resaleListingRepo := rdb.NewResaleListingRepository(db, testOutbox)
	s.mailer = &storyMailer{orders: s.orderRepo, entries: entryRepo, listings: resaleListingRepo, resaleEntries: resaleEntryRepo}

	s.seatTypes = usecase.NewSeatTypeUseCase(seatTypeRepo, seriesRepo)
	s.stocks = usecase.NewTicketStockUseCase(stockRepo, seatTypeRepo, eventRepo, seriesRepo, eventOrganizerRepo, clock)
	s.sales = usecase.NewTicketSaleUseCase(saleRepo, stockRepo, entryRepo, s.orderRepo, seriesRepo, organizerRepo,
		eventRepo, eventState, rdb.NewEventStartTimeRepository(db), clock)
	s.lottery = usecase.NewLotteryEntryUseCase(lotteryEntryDeps(t, db, cardAuthorizations, clock, logger))
	// The worker refunds through refundOrders: a sold listing's refund, and
	// the charge of a resale win whose listing was cancelled.
	refundOrders := s.orderRepo
	if key := os.Getenv("STRIPE_SECRET_KEY"); strings.HasPrefix(key, "sk_test_") || strings.HasPrefix(key, "rk_test_") {
		refundOrders = repository.NewOrderRepository(rdb.NewOrderRepository(db), payment.NewStripeOrderPayments(key, logger))
	}
	// The worker's OrderUseCase: issue, release, and the webhook's fulfilment.
	s.orders = usecase.NewOrderUseCase(usecase.OrderDeps{
		IssuanceRepo:             rdb.NewIssuanceRepository(db, testOutbox),
		OrderRepo:                refundOrders,
		EntryRepo:                entryRepo,
		ResaleEntryRepo:          resaleEntryRepo,
		EventOrganizerRepo:       eventOrganizerRepo,
		OrganizerRepo:            organizerRepo,
		IdentityVerificationRepo: rdb.NewIdentityVerificationRepository(db),
		CheckoutRepo:             rdb.NewCheckoutRepository(db, testOutbox),
		TicketSaleRepo:           saleRepo,
		StockRepo:                stockRepo,
		TicketRepo:               ticketRepo,
		EventState:               eventState,
		EventRepo:                eventRepo,
		CardAuthorizations:       cardAuthorizations,
		Clock:                    clock,
		Logger:                   logger,
	})
	s.expiry = usecase.NewCheckoutUseCase(usecase.CheckoutDeps{
		CheckoutRepo: rdb.NewCheckoutRepository(db, testOutbox), Clock: clock, Logger: logger,
	})
	s.tickets = usecase.NewTicketUseCase(s.orderRepo, ticketRepo, rdb.NewScannerRepository(db),
		eventRepo, rdb.NewWalletPublicKeyRepository(db), rdb.NewAdmissionRepository(db),
		rdb.NewRejectedScanRepository(db), logger)
	s.journeys = usecase.NewTicketJourneyUseCase(s.journeyRepo, s.userRepo, rdb.NewConcertRepository(db, testOutbox),
		rdb.NewFollowRepository(db, testOutbox), entryRepo, logger)
	// No refund flows in these stories, so the refund use case stays unset.
	s.webhooks = usecase.NewStripeWebhookService(rdb.NewProcessedWebhookEventRepository(db), s.orderRepo, nil, cardAuthorizations, logger)
	japanTime, err := time.LoadLocation("Asia/Tokyo")
	require.NoError(t, err)
	s.notifications = usecase.NewNotificationUseCase(rdb.NewNotificationRepository(db),
		rdb.NewPushSubscriptionRepository(db, testOutbox), nil, s.pushes, nil, logger,
		usecase.OrderConfirmationDeps{
			Orders:            s.orderRepo,
			Users:             s.userRepo,
			Concerts:          rdb.NewConcertRepository(db, testOutbox),
			EventOrganizer:    eventOrganizerRepo,
			Organizers:        organizerRepo,
			Mailer:            s.mailer,
			Entries:           entryRepo,
			Stocks:            stockRepo,
			LossMailer:        s.mailer,
			ResaleListings:    resaleListingRepo,
			ResaleMailer:      s.mailer,
			ResaleEntries:     resaleEntryRepo,
			ResaleEntryMailer: s.mailer,
			TimeZone:          japanTime,
			// The stories' pushes are only started (pushStarts), so nothing renders
			// a Notification.
		}, usecase.NewConcertsDeps{}, usecase.SubjectDeps{})

	s.resaleWorker = usecase.NewResaleEntryUseCase(usecase.ResaleEntryDeps{
		EntryRepo: resaleEntryRepo, StockRepo: stockRepo, EventRepo: eventRepo, EventState: eventState, Clock: clock, Logger: logger,
	})
	s.resaleRefunds = usecase.NewResaleListingUseCase(usecase.ResaleListingDeps{
		Listings: resaleListingRepo, Orders: refundOrders, Clock: clock, Logger: logger,
	})
	s.startWorker()
	s.client, err = workflow.NewClient(s.ctx, s.newPool(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.client.Close() })
	// The event consumer's OrderUseCase only starts workflows.
	s.drawn = event.NewLotteryEntryResultConsumer(usecase.NewOrderUseCase(usecase.OrderDeps{
		Workflows: s.client, Clock: clock, Logger: logger,
	}), s.notifications, logger)
	s.checkoutStarter = event.NewCheckoutConsumer(usecase.NewCheckoutUseCase(usecase.CheckoutDeps{
		Workflows: s.client, Clock: clock, Logger: logger,
	}), logger)
	s.series = usecase.NewSeriesUseCase(seriesRepo, rdb.NewSeriesImageRepository(db, testOutbox),
		rdb.NewVenueRepository(db), nil, organizerRepo, saleRepo, nil, usecase.StorageBuckets{}, logger, clock)
	// The event consumer's LotteryEntryUseCase only ends the entries of a
	// cancelled Event.
	s.lotteryCancellation = event.NewLotteryCancellationConsumer(usecase.NewLotteryEntryUseCase(usecase.LotteryEntryDeps{
		EntryRepo: entryRepo, Clock: clock, Logger: logger,
	}), logger)
	return s
}

// cancel has a member of the Organizer cancel (中止) the Event, hands each
// EVENT.cancelled announcement to the consumer that ends its lottery
// entries, as NATS delivers it, and runs the work that follows each entry
// announced since: the release of its card authorization and its loss email
// and push.
func (s *lotteryStory) cancel(eventID string) {
	s.t.Helper()
	require.NoError(s.t, s.series.Cancel(s.ctx, s.organizerID, s.seriesID, []string{eventID}))
	next := outboxMark(s.t, s.db)
	msgs := announcedMessages(s.t, s.db, s.cancelledMark, entity.SubjectEventCancelled)
	s.cancelledMark = next
	require.NotEmpty(s.t, msgs, "the cancellation is announced")
	for _, msg := range msgs {
		require.NoError(s.t, s.lotteryCancellation.HandleEventCancelled(msg))
	}
	s.startFollowUps()
	s.awaitFollowUps()
}

// pushedLoss reports whether a lottery_loss push was started for the entry.
func (s *lotteryStory) pushedLoss(id entity.LotteryEntryID) bool {
	s.pushes.mu.Lock()
	defer s.pushes.mu.Unlock()
	return s.pushes.ids["deliver:"+string(entity.NotificationTypeLotteryLoss)+":"+string(id)]
}

// secondEvent inserts another published event of the story's Series a day
// after the first, stocks it with quantity tickets of a new SeatType of the
// price, and returns the event and the stock.
func (s *lotteryStory) secondEvent(price int64, quantity int) (string, *entity.TicketStock) {
	s.t.Helper()
	eventID := uuid.NewV7().String()
	start := storyEventStart.Add(24 * time.Hour)
	_, err := s.db.Pool.Exec(s.ctx, `
		INSERT INTO events (id, series_id, venue_id, local_event_date, start_at, publish_state, per_account_limit)
		SELECT $1, series_id, venue_id, $3, $4, 'PUBLISHED', 4 FROM events WHERE id = $2
	`, eventID, s.eventID, start.Format(time.DateOnly), start)
	require.NoError(s.t, err)
	seatType, err := s.seatTypes.Create(s.ctx, s.organizerID, s.seriesID, "S席 2日目", price)
	require.NoError(s.t, err)
	stock, err := s.stocks.Create(s.ctx, s.organizerID, eventID, seatType.ID, quantity)
	require.NoError(s.t, err)
	return eventID, stock
}

// newPool returns the database pool of one simulated process: DBOS closes
// the pool when the runtime or client using it shuts down.
func (s *lotteryStory) newPool() *pgxpool.Pool {
	s.t.Helper()
	// At most 4 connections, as newTestDB: the default is one per CPU, and
	// the stories that run together would pass the server's max_connections.
	pool, err := pgxpool.New(s.ctx, databaseOf(s.t, s.db).URL("")+"&pool_max_conns=4")
	require.NoError(s.t, err)
	s.t.Cleanup(pool.Close)
	return pool
}

// startWorker starts a workflow worker running the lottery's workflows, as
// cmd/worker registers them; draw-due-sales is started by the story with the
// time of its run instead of by its schedule.
func (s *lotteryStory) startWorker() {
	s.t.Helper()
	rt, err := workflow.NewRuntime(s.ctx, s.newPool(), s.logger)
	require.NoError(s.t, err)
	workflow.Register(rt, usecase.WorkflowDrawDueSales, s.lottery.DrawDueSales)
	workflow.Register(rt, usecase.WorkflowDraw, s.lottery.Draw)
	require.NoError(s.t, rt.RegisterQueue(usecase.PaymentQueue, usecase.PaymentQueueConcurrency))
	workflow.Register(rt, usecase.WorkflowIssue, s.orders.Issue)
	workflow.Register(rt, usecase.WorkflowReleaseCardAuthorization, s.orders.ReleaseCardAuthorization)
	require.NoError(s.t, rt.RegisterQueue(usecase.CheckoutQueue, usecase.CheckoutQueueConcurrency))
	workflow.Register(rt, usecase.WorkflowExpireCheckout, s.expiry.Expire)
	workflow.Register(rt, usecase.WorkflowDrawResaleStock, s.resaleWorker.DrawStock)
	workflow.Register(rt, usecase.WorkflowDrawResale, s.resaleWorker.DrawResale)
	workflow.Register(rt, usecase.WorkflowRefundSold, s.resaleRefunds.RefundSold)
	require.NoError(s.t, rt.Launch())
	var once sync.Once
	s.worker = rt
	s.t.Cleanup(func() { once.Do(func() { _ = rt.Close() }) })
}

// stopWorker stops the worker as a terminated Pod does: running workflows
// are interrupted and left for the next worker.
func (s *lotteryStory) stopWorker() {
	s.t.Helper()
	require.NoError(s.t, s.worker.Close())
}

// await waits for the workflow id to end and fails the story on its error.
func (s *lotteryStory) await(id string) {
	s.t.Helper()
	require.NoError(s.t, s.client.Await(s.ctx, id, storyWait, nil), "workflow %s", id)
}

// seedOrganizerEvent inserts an Organizer with complete business details and
// a published Series with one event starting at storyEventStart.
func (s *lotteryStory) seedOrganizerEvent() {
	t := s.t
	s.organizerID = seedOrganizer(t, s.db)
	_, err := rdb.NewOrganizerRepository(s.db, testOutbox).SetBusinessDetails(s.ctx, s.organizerID, entity.BusinessDetails{
		LegalName:           "株式会社テスト興行",
		RepresentativeName:  "代表 太郎",
		BusinessAddress:     "東京都渋谷区1-1-1",
		BusinessPhoneNumber: "+81312345678",
		ContactEmail:        "contact@example.test",
	})
	require.NoError(t, err)

	s.seriesID = uuid.NewV7().String()
	_, err = s.db.Pool.Exec(s.ctx, `
		INSERT INTO series (id, title, type, organizer_id, visibility)
		VALUES ($1, 'Story Live', 'SINGLE', $2, 'PUBLIC')
	`, s.seriesID, s.organizerID)
	require.NoError(t, err)

	s.eventID = uuid.NewV7().String()
	_, err = s.db.Pool.Exec(s.ctx, `
		INSERT INTO events (id, series_id, venue_id, local_event_date, start_at, publish_state, per_account_limit) VALUES ($1, $2, $3, $4, $5, 'PUBLISHED', 4)
	`, s.eventID, s.seriesID, seedVenue(t, s.db, "story-venue"), storyEventStart.Format(time.DateOnly), storyEventStart)
	require.NoError(t, err)
}

// stock has the Organizer add a SeatType of the price to the Series and stock
// the event with quantity tickets of it, and returns the TicketStock.
func (s *lotteryStory) stock(name string, price int64, quantity int) *entity.TicketStock {
	s.t.Helper()
	seatType, err := s.seatTypes.Create(s.ctx, s.organizerID, s.seriesID, name, price)
	require.NoError(s.t, err)
	stock, err := s.stocks.Create(s.ctx, s.organizerID, s.eventID, seatType.ID, quantity)
	require.NoError(s.t, err)
	return stock
}

// createLottery has the Organizer create a Lottery sale naming the event,
// open from start to end, and returns it.
func (s *lotteryStory) createLottery(name string, start, end time.Time) entity.TicketSaleID {
	s.t.Helper()
	sale, err := s.sales.Create(s.ctx, s.organizerID, usecase.CreateTicketSaleInput{
		SeriesID:  s.seriesID,
		Name:      name,
		Method:    entity.TicketSaleMethodLottery,
		SaleStart: start,
		SaleEnd:   end,
		EventIDs:  []string{s.eventID},
	})
	require.NoError(s.t, err)
	s.saleIDs = append(s.saleIDs, sale.ID)
	return sale.ID
}

// newFan signs a new fan up.
func (s *lotteryStory) newFan() entity.UserID {
	s.t.Helper()
	return entity.UserID(seedUser(s.t, s.db, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String()))
}

// emailsTo returns the emails sent to the fan's address.
func (s *lotteryStory) emailsTo(fan entity.UserID) []sentEmail {
	s.t.Helper()
	user, err := s.userRepo.Get(s.ctx, string(fan))
	require.NoError(s.t, err)
	return s.mailer.to(user.Email)
}

// authorizeCard opens the fan's card authorization for count tickets of the
// stock in the sale with the code.
func (s *lotteryStory) authorizeCard(saleID entity.TicketSaleID, stockID entity.TicketStockID, fan entity.UserID, count int, code *entity.ReferralCode) *entity.CardAuthorization {
	s.t.Helper()
	authorization, err := s.lottery.AuthorizeCard(s.ctx, saleID, stockID, fan, count, storyFanName, storyFanPhone, code)
	require.NoError(s.t, err)
	return authorization
}

// authorize opens the fan's card authorization for count tickets and
// completes card authentication, leaving it Authorized.
func (s *lotteryStory) authorize(saleID entity.TicketSaleID, stockID entity.TicketStockID, fan entity.UserID, count int, code *entity.ReferralCode) *entity.CardAuthorization {
	s.t.Helper()
	authorization := s.authorizeCard(saleID, stockID, fan, count, code)
	s.cards.authenticate(s.t, authorization.ID)
	return authorization
}

// enter makes the fan's confirmed entry for count tickets of the stock.
func (s *lotteryStory) enter(saleID entity.TicketSaleID, stockID entity.TicketStockID, fan entity.UserID, count int) *entity.LotteryEntry {
	s.t.Helper()
	authorization := s.authorize(saleID, stockID, fan, count, nil)
	entered, err := s.lottery.Confirm(s.ctx, authorization.ID, &fan)
	require.NoError(s.t, err)
	require.Equal(s.t, entity.LotteryEntryStateEntered, entered.State)
	return entered
}

// draw advances the clock 90 seconds past the window's end and has the
// worker run draw-due-sales at that time and every draw it starts.
func (s *lotteryStory) draw(end time.Time) {
	s.t.Helper()
	s.now = end.Add(90 * time.Second)
	id := usecase.WorkflowDrawDueSales + ":" + s.now.Format(time.RFC3339Nano)
	require.NoError(s.t, s.client.Start(s.ctx, usecase.WorkflowDrawDueSales, id, s.now))
	due, err := usecase.Await[int](s.ctx, s.client, id, storyWait)
	require.NoError(s.t, err)
	if due == 0 {
		return
	}
	for _, sale := range s.saleIDs {
		err := s.client.Await(s.ctx, usecase.DrawWorkflowID(sale), storyWait, nil)
		if !errorsIsNotFound(err) {
			require.NoError(s.t, err, "draw of sale %s", sale)
		}
	}
}

// errorsIsNotFound reports whether err says the workflow does not exist: a
// sale that was not due has no draw.
func errorsIsNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not_found")
}

// startFollowUps hands the LOTTERY_ENTRY.won and LOTTERY_ENTRY.lost
// announcements recorded since the last call to their consumers, as NATS
// delivers them: both to the workflow starter, LOTTERY_ENTRY.lost also to the
// loss notifier. It remembers the workflows the starter starts.
func (s *lotteryStory) startFollowUps() int {
	s.t.Helper()
	next := outboxMark(s.t, s.db)
	msgs := announcedMessages(s.t, s.db, s.drawnMark, entity.SubjectLotteryEntryWon, entity.SubjectLotteryEntryLost)
	s.drawnMark = next
	for _, msg := range msgs {
		require.NoError(s.t, s.drawn.HandleStartWorkflow(msg))
		var e entityv1.LotteryEntry
		require.NoError(s.t, proto.Unmarshal(msg.Payload, &e))
		orderID := entity.OrderID(e.GetOrderId().GetValue())
		switch eventmsg.CloudEventType(msg) {
		case entity.SubjectLotteryEntryWon:
			s.followUps = append(s.followUps, usecase.IssueWorkflowID(orderID))
		case entity.SubjectLotteryEntryLost:
			require.NoError(s.t, s.drawn.HandleSendLotteryLoss(msg))
			s.followUps = append(s.followUps, usecase.ReleaseWorkflowID(orderID))
		}
	}
	return len(msgs)
}

// awaitFollowUps waits for every started issue and release workflow, and
// hands on the announcements they record (a reverted win) until none is new.
func (s *lotteryStory) awaitFollowUps() {
	s.t.Helper()
	for len(s.followUps) > 0 {
		pending := s.followUps
		s.followUps = nil
		for _, id := range pending {
			s.await(id)
		}
		s.startFollowUps()
	}
}

// startCheckoutFollowUps hands the announcements of subjects (by default
// CHECKOUT.held, CHECKOUT.expired and CHECKOUT.released)
// recorded since the last call to the workflow starter, as NATS delivers
// them, at the story's clock, and returns the ids of the workflows it starts:
// a hold's expiry, delayed until the hold expiry, and the release of an ended
// Checkout's card authorization.
func (s *lotteryStory) startCheckoutFollowUps(subjects ...string) []string {
	s.t.Helper()
	if len(subjects) == 0 {
		subjects = []string{entity.SubjectCheckoutHeld, entity.SubjectCheckoutExpired, entity.SubjectCheckoutReleased}
	}
	next := outboxMark(s.t, s.db)
	var ids []string
	for _, subject := range subjects {
		for _, msg := range announcedMessages(s.t, s.db, s.checkoutMark, subject) {
			require.NoError(s.t, s.checkoutStarter.HandleStartWorkflow(msg))
			var c entityv1.Checkout
			require.NoError(s.t, proto.Unmarshal(msg.Payload, &c))
			if subject == entity.SubjectCheckoutHeld {
				ids = append(ids, usecase.ExpireWorkflowID(entity.CheckoutID(c.GetId().GetValue())))
			} else {
				ids = append(ids, usecase.ReleaseWorkflowID(entity.OrderID(c.GetOrderId().GetValue())))
			}
		}
	}
	s.checkoutMark = next
	return ids
}

// endLapsedHolds runs, at the story's clock after the holds lapsed, the work
// that follows each announced Checkout: the expiry of each hold, then the
// release of each ended Checkout's card authorization.
func (s *lotteryStory) endLapsedHolds() {
	s.t.Helper()
	for ids := s.startCheckoutFollowUps(); len(ids) > 0; ids = s.startCheckoutFollowUps() {
		for _, id := range ids {
			s.await(id)
		}
	}
}

// releaseEndedCheckouts runs the release of the card authorization of each
// Checkout announced as ended, leaving the holds' expiries unstarted.
func (s *lotteryStory) releaseEndedCheckouts() {
	s.t.Helper()
	for _, id := range s.startCheckoutFollowUps(entity.SubjectCheckoutExpired, entity.SubjectCheckoutReleased) {
		s.await(id)
	}
}

// closeAndDraw draws the closed sale and runs the work that follows each
// drawn entry: the charge and issuance of a winner, the release of a loser
// and its loss email.
func (s *lotteryStory) closeAndDraw(end time.Time) {
	s.t.Helper()
	s.draw(end)
	s.startFollowUps()
	s.awaitFollowUps()
}

// issueWins advances the clock 30 more seconds, delivers Stripe's
// payment_intent.succeeded for each charged win, and hands every ORDER.paid
// announcement to its consumers: the confirmation email and push, and the
// ticket journey.
func (s *lotteryStory) issueWins(won ...*entity.LotteryEntry) {
	s.t.Helper()
	s.now = s.now.Add(30 * time.Second)
	for _, e := range won {
		require.NoError(s.t, s.webhooks.HandleEvent(s.ctx, usecase.StripeWebhookEvent{
			ProviderEventID:   "evt_" + uuid.NewV7().String(),
			Type:              usecase.StripeEventPaymentIntentSucceeded,
			ProviderPaymentID: fakeProviderPrefix + e.Order.CardAuthorizationID,
			ReceivedAt:        s.now,
		}))
	}
	for _, paid := range s.takePaid() {
		require.NoError(s.t, s.notifications.SendOrderConfirmation(s.ctx, paid))
		require.NoError(s.t, s.journeys.MarkPaid(s.ctx, paid))
	}
}

// markEnteredApplied hands the LOTTERY_ENTRY.entered announcements of
// the story's event recorded since the previous call to the ticket journey,
// as the mark-ticket-journey-applied consumer does.
func (s *lotteryStory) markEnteredApplied() {
	s.t.Helper()
	next := outboxMark(s.t, s.db)
	orders := announcedSince(s.t, s.db, s.enteredMark, entity.SubjectLotteryEntryEntered, func() *entityv1.Order { return &entityv1.Order{} },
		func(o *entityv1.Order) bool {
			var eventID string
			require.NoError(s.t, s.db.Pool.QueryRow(s.ctx, `SELECT event_id FROM ticket_stocks WHERE id = $1`,
				o.GetTicketStockId().GetValue()).Scan(&eventID))
			return eventID == s.eventID
		})
	s.enteredMark = next
	for _, o := range orders {
		require.NoError(s.t, s.journeys.MarkApplied(s.ctx, entity.LotteryEntryEnteredData{
			LotteryEntryID: entity.LotteryEntryID(o.GetLotteryEntryId().GetValue()), UserID: o.GetUserId().GetValue(), EventID: s.eventID,
		}))
	}
}

// takePaid returns the ORDER.paid announcements of the story's event recorded
// since the previous take, as the order-paid consumers read them.
func (s *lotteryStory) takePaid() []entity.OrderPaidData {
	s.t.Helper()
	next := outboxMark(s.t, s.db)
	orders := announcedSince(s.t, s.db, s.paidMark, entity.SubjectOrderPaid, func() *entityv1.Order { return &entityv1.Order{} },
		func(o *entityv1.Order) bool {
			var eventID string
			require.NoError(s.t, s.db.Pool.QueryRow(s.ctx, `SELECT event_id FROM ticket_stocks WHERE id = $1`,
				o.GetTicketStockId().GetValue()).Scan(&eventID))
			return eventID == s.eventID
		})
	s.paidMark = next
	paid := make([]entity.OrderPaidData, 0, len(orders))
	for _, o := range orders {
		paid = append(paid, entity.OrderPaidData{
			OrderID: o.GetId().GetValue(), UserID: o.GetUserId().GetValue(), EventID: s.eventID,
			TicketCount: int(o.GetTicketCount()), Amount: o.GetAmount(), Currency: o.GetCurrency(),
		})
	}
	return paid
}

// ownEntry returns the fan's entry as ListByTicketSale shows it.
func (s *lotteryStory) ownEntry(saleID entity.TicketSaleID, fan entity.UserID, id entity.LotteryEntryID) *entity.LotteryEntry {
	s.t.Helper()
	entries, err := s.lottery.ListByTicketSale(s.ctx, saleID, fan)
	require.NoError(s.t, err)
	for _, e := range entries {
		if e.ID == id {
			return e
		}
	}
	s.t.Fatalf("entry %s is not among the fan's entries", id)
	return nil
}

// eventTickets returns the fan's Tickets for the event, as TicketService.List
// shows them.
func (s *lotteryStory) eventTickets(fan entity.UserID) []*entity.Ticket {
	s.t.Helper()
	all, err := s.tickets.List(s.ctx, fan)
	require.NoError(s.t, err)
	var out []*entity.Ticket
	for _, tk := range all {
		if tk.EventID == s.eventID {
			out = append(out, tk)
		}
	}
	return out
}

// assertOrderVoid asserts the entry's Order will never be paid.
func (s *lotteryStory) assertOrderVoid(id entity.LotteryEntryID) {
	s.t.Helper()
	order, err := s.orderRepo.GetByLotteryEntryID(s.ctx, id)
	require.NoError(s.t, err)
	assert.Equal(s.t, entity.OrderStatusVoid, order.Status, "the entry's Order is Void")
}

// assertNoEntry asserts the fan has no entry in the sale.
func (s *lotteryStory) assertNoEntry(saleID entity.TicketSaleID, fan entity.UserID) {
	s.t.Helper()
	entries, err := s.lottery.ListByTicketSale(s.ctx, saleID, fan)
	require.NoError(s.t, err)
	assert.Empty(s.t, entries, "the fan has no entry")
}

// winPresale runs a presale of the stock the fan wins with count tickets:
// entry, draw and issuance. It returns the end of the presale window.
func (s *lotteryStory) winPresale(stock *entity.TicketStock, fan entity.UserID, count int) time.Time {
	s.t.Helper()
	end := storyWindowStart.Add(2 * 24 * time.Hour)
	presale := s.createLottery("先行抽選", storyWindowStart, end)
	s.now = storyWindowStart.Add(time.Hour)
	entry := s.enter(presale, stock.ID, fan, count)
	s.closeAndDraw(end)
	require.Equal(s.t, entity.LotteryEntryStateWon, s.ownEntry(presale, fan, entry.ID).State)
	s.issueWins(entry)
	require.Len(s.t, s.eventTickets(fan), count)
	return end
}

// openGeneralSale has the Organizer create a general Lottery sale opening a
// day after the presale's end, and moves the clock an hour into it.
func (s *lotteryStory) openGeneralSale(presaleEnd time.Time) entity.TicketSaleID {
	s.t.Helper()
	start := presaleEnd.Add(24 * time.Hour)
	general := s.createLottery("一般抽選", start, start.Add(3*24*time.Hour))
	s.now = start.Add(time.Hour)
	return general
}

func TestLotteryStory_DemandWithinQuantity(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// @spec stories/win-tickets-in-a-lottery "Demand within quantity"
	s := newLotteryStory(t, db)
	stock := s.stock("S席", 8000, 100)
	end := storyWindowStart.Add(3 * 24 * time.Hour)
	sale := s.createLottery("抽選", storyWindowStart, end)

	s.now = storyWindowStart.Add(time.Hour)
	fan := s.newFan()
	entry := s.enter(sale, stock.ID, fan, 2)

	// -- after the draw: Won and 16000 yen charged --
	s.closeAndDraw(end)
	assert.Equal(t, entity.LotteryEntryStateWon, s.ownEntry(sale, fan, entry.ID).State)
	charged, err := s.cards.Get(s.ctx, entry.Order.CardAuthorizationID)
	require.NoError(t, err)
	assert.Equal(t, entity.CardAuthorizationStatusCaptured, charged.Status)
	assert.Equal(t, int64(16000), charged.CapturedPayment.AmountJPY, "16000 yen is charged")
	assert.Equal(t, 1, s.cards.charged(entry.Order.CardAuthorizationID))

	// -- 2 Issued Tickets, the fan's name and phone on their faces, the
	//    ticket journey Paid, and one confirmation email saying the entry won --
	s.issueWins(entry)
	tickets := s.eventTickets(fan)
	require.Len(t, tickets, 2)
	user, err := s.userRepo.Get(s.ctx, string(fan))
	require.NoError(t, err)
	for _, tk := range tickets {
		assert.Equal(t, entity.TicketStatusIssued, tk.Status)
		face := tk.Face(user)
		assert.Equal(t, new(storyFanName), face.FullName)
		assert.Equal(t, new(storyFanPhone), face.PhoneNumber)
		assert.Equal(t, s.eventID, face.EventID)
		assert.True(t, face.ResaleWithoutConsentProhibited)
	}
	order, err := s.orderRepo.GetByLotteryEntryID(s.ctx, entry.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusPaid, order.Status)
	assert.Equal(t, int64(16000), order.Amount)
	journey, err := s.journeyRepo.Get(s.ctx, string(fan), s.eventID)
	require.NoError(t, err)
	assert.Equal(t, entity.TicketJourneyStatusPaid, journey.Status)
	emails := s.emailsTo(fan)
	require.Len(t, emails, 1, "one email")
	assert.Contains(t, emails[0].subject, "当選しました", "the confirmation says the entry won")
}

func TestLotteryStory_EntryMarksJourneyApplied(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	s := newLotteryStory(t, db)
	stock := s.stock("S席", 8000, 100)
	end := storyWindowStart.Add(3 * 24 * time.Hour)
	sale := s.createLottery("抽選", storyWindowStart, end)

	s.now = storyWindowStart.Add(time.Hour)
	fan := s.newFan()
	entry := s.enter(sale, stock.ID, fan, 2)
	entered := entity.LotteryEntryEnteredData{LotteryEntryID: entry.ID, UserID: string(fan), EventID: s.eventID}

	t.Run("entry made", func(t *testing.T) {
		// @spec stories/win-tickets-in-a-lottery "Entry made"
		// The entry is announced, and its receiver records Applied.
		s.markEnteredApplied()

		journey, err := s.journeyRepo.Get(s.ctx, string(fan), s.eventID)
		require.NoError(t, err)
		assert.Equal(t, entity.TicketJourneyStatusApplied, journey.Status)
		assert.Equal(t, entity.TicketJourneySetByPlatform, journey.StatusSetBy)

		// A redelivered announcement changes nothing.
		require.NoError(t, s.journeys.MarkApplied(s.ctx, entered))
		again, err := s.journeyRepo.Get(s.ctx, string(fan), s.eventID)
		require.NoError(t, err)
		assert.Equal(t, journey, again)
	})

	t.Run("entry wins", func(t *testing.T) {
		// @spec stories/win-tickets-in-a-lottery "Entry wins"
		s.closeAndDraw(end)
		s.issueWins(entry)

		journey, err := s.journeyRepo.Get(s.ctx, string(fan), s.eventID)
		require.NoError(t, err)
		assert.Equal(t, entity.TicketJourneyStatusPaid, journey.Status)
		assert.Equal(t, entity.TicketJourneySetByPlatform, journey.StatusSetBy)

		// The entry's announcement held back until after the draw records
		// nothing.
		require.NoError(t, s.journeys.MarkApplied(s.ctx, entered))
		late, err := s.journeyRepo.Get(s.ctx, string(fan), s.eventID)
		require.NoError(t, err)
		assert.Equal(t, entity.TicketJourneyStatusPaid, late.Status)
	})
}

func TestLotteryStory_OversubscribedSale(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// @spec stories/win-tickets-in-a-lottery "Oversubscribed sale"
	s := newLotteryStory(t, db)
	stock := s.stock("S席", 8000, 4)
	end := storyWindowStart.Add(3 * 24 * time.Hour)
	sale := s.createLottery("抽選", storyWindowStart, end)

	// -- entries for 12 tickets against 4: exactly one entry of 4 is admitted --
	s.now = storyWindowStart.Add(time.Hour)
	type fanEntry struct {
		fan   entity.UserID
		entry *entity.LotteryEntry
	}
	var entries []fanEntry
	for range 3 {
		fan := s.newFan()
		entries = append(entries, fanEntry{fan: fan, entry: s.enter(sale, stock.ID, fan, 4)})
	}

	s.closeAndDraw(end)
	var won []*entity.LotteryEntry
	var lost []fanEntry
	for _, fe := range entries {
		switch got := s.ownEntry(sale, fe.fan, fe.entry.ID); got.State {
		case entity.LotteryEntryStateWon:
			won = append(won, fe.entry)
		case entity.LotteryEntryStateLost:
			lost = append(lost, fe)
		default:
			t.Fatalf("unexpected entry state after the draw: %v", got.State)
		}
	}
	require.Len(t, won, 1)
	require.Len(t, lost, 2)
	s.issueWins(won...)

	// -- each fan not admitted: Lost, card authorization released, one loss
	//    email, nothing charged, no Ticket --
	for _, fe := range lost {
		ref := fe.entry.Order.CardAuthorizationID
		assert.Equal(t, entity.CardAuthorizationStatusReleased, s.cards.status(ref), "the card authorization is released")
		assert.Zero(t, s.cards.charged(ref), "the fan is not charged")
		assert.Empty(t, s.eventTickets(fe.fan), "the fan holds no Ticket for the event")
		s.assertOrderVoid(fe.entry.ID)
		emails := s.emailsTo(fe.fan)
		require.Len(t, emails, 1, "one email")
		assert.Contains(t, emails[0].body, "落選", "the email says the entry lost")
		assert.Contains(t, emails[0].body, "請求はありません", "and that nothing is charged")
	}
}

func TestLotteryStory_StoppedAfterTheFirstCharge(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// @spec stories/win-tickets-in-a-lottery "Stopped after the first charge"
	s := newLotteryStory(t, db)
	stock := s.stock("S席", 8000, 4)
	end := storyWindowStart.Add(3 * 24 * time.Hour)
	sale := s.createLottery("抽選", storyWindowStart, end)

	s.now = storyWindowStart.Add(time.Hour)
	fans := []entity.UserID{s.newFan(), s.newFan(), s.newFan()}
	entries := []*entity.LotteryEntry{
		s.enter(sale, stock.ID, fans[0], 2),
		s.enter(sale, stock.ID, fans[1], 2),
		s.enter(sale, stock.ID, fans[2], 3),
	}

	// -- the worker stops right after the first card is charged, before the
	//    charge is recorded --
	charged := make(chan struct{}, 1)
	var first sync.Once
	s.cards.afterCharge = func(ctx context.Context, _ string) error {
		stop := false
		first.Do(func() { stop = true })
		if !stop {
			return nil
		}
		charged <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	s.draw(end)
	winnersBefore := map[entity.LotteryEntryID]entity.LotteryEntryState{}
	for i, e := range entries {
		winnersBefore[e.ID] = s.ownEntry(sale, fans[i], e.ID).State
	}
	s.startFollowUps()
	select {
	case <-charged:
	case <-time.After(storyWait):
		t.Fatal("timed out waiting for the first charge")
	}
	s.stopWorker()
	s.cards.afterCharge = nil

	// -- it starts again and finishes the draw's work --
	s.startWorker()
	s.awaitFollowUps()
	var won []*entity.LotteryEntry
	for i, e := range entries {
		got := s.ownEntry(sale, fans[i], e.ID)
		assert.Equal(t, winnersBefore[e.ID], got.State, "the same entries are Won")
		if got.State == entity.LotteryEntryStateWon {
			won = append(won, e)
		}
	}
	s.issueWins(won...)

	// The random order decides which entries win (both 2-ticket entries, or
	// the 3-ticket one alone); whatever it was, the restart keeps it.
	require.NotEmpty(t, won)
	for i, e := range entries {
		ref := e.Order.CardAuthorizationID
		if winnersBefore[e.ID] == entity.LotteryEntryStateWon {
			assert.Equal(t, 1, s.cards.charged(ref), "each winner is charged once")
			assert.Len(t, s.eventTickets(fans[i]), e.Order.TicketCount, "and holds its Issued Tickets")
		} else {
			assert.Equal(t, entity.LotteryEntryStateLost, winnersBefore[e.ID])
			assert.Zero(t, s.cards.charged(ref), "a losing entry is never charged")
			assert.Equal(t, entity.CardAuthorizationStatusReleased, s.cards.status(ref))
			assert.Empty(t, s.eventTickets(fans[i]))
		}
		assert.Len(t, s.emailsTo(fans[i]), 1, "one email per fan")
	}
}

func TestLotteryStory_CardClosedBeforeTheCharge(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// @spec stories/win-tickets-in-a-lottery "Card closed before the charge"
	// @spec components/usecase/order/release-card-authorization "Win reverted"
	s := newLotteryStory(t, db)
	stock := s.stock("S席", 8000, 100)
	end := storyWindowStart.Add(3 * 24 * time.Hour)
	sale := s.createLottery("抽選", storyWindowStart, end)

	s.now = storyWindowStart.Add(time.Hour)
	fan := s.newFan()
	entry := s.enter(sale, stock.ID, fan, 2)
	s.cards.closeCard(entry.Order.CardAuthorizationID)

	s.closeAndDraw(end)
	s.issueWins()

	got := s.ownEntry(sale, fan, entry.ID)
	assert.Equal(t, entity.LotteryEntryStateLost, got.State, "the fan's entry is Lost")
	assert.Equal(t, entity.LotteryEntryLostReasonCardNotCharged, got.LostReason)
	assert.Zero(t, s.cards.charged(entry.Order.CardAuthorizationID), "nothing is charged")
	assert.Equal(t, entity.CardAuthorizationStatusReleased, s.cards.status(entry.Order.CardAuthorizationID),
		"the reverted win's card authorization is released")
	assert.Empty(t, s.eventTickets(fan), "the fan holds no Ticket for the event")
	s.assertOrderVoid(entry.ID)
	emails := s.emailsTo(fan)
	require.Len(t, emails, 1, "one email")
	assert.Contains(t, emails[0].body, "カードで決済できなかったため、当選を取り消しました", "the win was cancelled because the card could not be charged")
}

func TestLotteryStory_CountChangedBeforeConfirming(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// @spec stories/win-tickets-in-a-lottery "Count changed before confirming"
	s := newLotteryStory(t, db)
	stock := s.stock("S席", 8000, 100)
	end := storyWindowStart.Add(3 * 24 * time.Hour)
	sale := s.createLottery("抽選", storyWindowStart, end)

	s.now = storyWindowStart.Add(time.Hour)
	fan := s.newFan()
	two := s.authorizeCard(sale, stock.ID, fan, 2, nil) // the fan goes back before authenticating
	three := s.authorize(sale, stock.ID, fan, 3, nil)
	entered, err := s.lottery.Confirm(s.ctx, three.ID, &fan)
	require.NoError(t, err)

	entries, err := s.lottery.ListByTicketSale(s.ctx, sale, fan)
	require.NoError(t, err)
	require.Len(t, entries, 1, "the fan has one entry")
	assert.Equal(t, entered.ID, entries[0].ID)
	assert.Equal(t, entity.LotteryEntryStateEntered, entries[0].State)
	assert.Equal(t, 3, entries[0].Order.TicketCount)
	assert.Equal(t, three.ID, entries[0].Order.CardAuthorizationID)
	assert.Equal(t, entity.CardAuthorizationStatusPending, s.cards.status(two.ID), "nothing is held for 2 tickets")
	assert.Zero(t, s.cards.charged(two.ID))
}

func TestLotteryStory_BrowserClosedAfterCardAuthentication(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// @spec stories/win-tickets-in-a-lottery "Browser closed after card authentication"
	s := newLotteryStory(t, db)
	stock := s.stock("S席", 8000, 100)
	end := storyWindowStart.Add(3 * 24 * time.Hour)
	sale := s.createLottery("抽選", storyWindowStart, end)

	s.now = storyWindowStart.Add(time.Hour)
	fan := s.newFan()
	authorization := s.authorize(sale, stock.ID, fan, 2, nil)
	// The browser closes: the payment provider's notice runs Confirm without
	// the fan.
	entered, err := s.lottery.Confirm(s.ctx, authorization.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, entity.LotteryEntryStateEntered, s.ownEntry(sale, fan, entered.ID).State)

	// -- the entry takes part in the draw --
	s.closeAndDraw(end)
	got := s.ownEntry(sale, fan, entered.ID)
	assert.Equal(t, entity.LotteryEntryStateWon, got.State)
	assert.NotNil(t, got.DrawPosition)
}

func TestLotteryStory_EntryMadeOnlyAfterTheWindowClosed(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// @spec stories/win-tickets-in-a-lottery "Entry made only after the window closed"
	s := newLotteryStory(t, db)
	stock := s.stock("S席", 8000, 100)
	// A window that has already ended by the database clock: the fan
	// completes card authentication 10 seconds before the end time, and the
	// confirmation reaches the platform only after it.
	end := time.Now().Add(-time.Minute)
	start := end.Add(-24 * time.Hour)
	s.now = start.Add(-24 * time.Hour)
	sale := s.createLottery("抽選", start, end)

	s.now = end.Add(-10 * time.Second)
	fan := s.newFan()
	authorization := s.authorize(sale, stock.ID, fan, 2, nil)

	s.now = end.Add(5 * time.Second)
	_, err := s.lottery.Confirm(s.ctx, authorization.ID, &fan)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)

	s.assertNoEntry(sale, fan)
	assert.Equal(t, entity.CardAuthorizationStatusReleased, s.cards.status(authorization.ID), "the card authorization is released")
	assert.Zero(t, s.cards.charged(authorization.ID), "the fan is never charged")

	// The payment provider's notice, arriving later still, changes nothing.
	_, err = s.lottery.Confirm(s.ctx, authorization.ID, nil)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	s.assertNoEntry(sale, fan)
}

func TestLotteryStory_CardAuthenticationAbandoned(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// @spec stories/win-tickets-in-a-lottery "Card authentication abandoned"
	s := newLotteryStory(t, db)
	stock := s.stock("S席", 8000, 100)
	end := storyWindowStart.Add(3 * 24 * time.Hour)
	sale := s.createLottery("抽選", storyWindowStart, end)

	s.now = storyWindowStart.Add(time.Hour)
	fan := s.newFan()
	authorization := s.authorizeCard(sale, stock.ID, fan, 2, nil) // the fan leaves before card authentication

	s.closeAndDraw(end)
	s.issueWins()
	s.assertNoEntry(sale, fan)
	assert.Equal(t, entity.CardAuthorizationStatusPending, s.cards.status(authorization.ID), "nothing is held on the card")
	assert.Zero(t, s.cards.charged(authorization.ID), "the fan is never charged")
	assert.Empty(t, s.eventTickets(fan))
}

// TestLotteryStory_EntryFromAReferralLink checks the lottery sentence of
// stories/attribute-a-purchase-to-a-referral-link: an entry authorized with a
// code carries it on its Order, and once the Order is Paid the Organizer's
// sales count it under that code.
func TestLotteryStory_EntryFromAReferralLink(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	s := newLotteryStory(t, db)
	stock := s.stock("S席", 8000, 100)
	end := storyWindowStart.Add(3 * 24 * time.Hour)
	sale := s.createLottery("抽選", storyWindowStart, end)

	s.now = storyWindowStart.Add(time.Hour)
	fan := s.newFan()
	code := entity.ReferralCode("member-a")
	authorization := s.authorize(sale, stock.ID, fan, 2, &code)
	entered, err := s.lottery.Confirm(s.ctx, authorization.ID, &fan)
	require.NoError(t, err)
	require.NotNil(t, entered.Order.ReferralCode)
	assert.Equal(t, code, *entered.Order.ReferralCode, "the entry's Order carries the code it was authorized with")

	own, err := s.sales.ListOwnByEvent(s.ctx, s.organizerID, s.eventID)
	require.NoError(t, err)
	assert.Empty(t, own.ReferralCodeStats, "a Pending Order is not counted")

	s.closeAndDraw(end)
	s.issueWins(entered)
	own, err = s.sales.ListOwnByEvent(s.ctx, s.organizerID, s.eventID)
	require.NoError(t, err)
	assert.Equal(t, []entity.ReferralCodeStats{{Code: &code, OrderCount: 1, TicketCount: 2, Amount: 16000}}, own.ReferralCodeStats)
}

func TestLotteryStory_PresaleWinnerAtTheLimit(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// @spec stories/win-tickets-in-a-lottery "Presale winner at the limit"
	s := newLotteryStory(t, db)
	stock := s.stock("S席", 8000, 100)
	fan := s.newFan()
	general := s.openGeneralSale(s.winPresale(stock, fan, 4))

	_, err := s.lottery.AuthorizeCard(s.ctx, general, stock.ID, fan, 1, storyFanName, storyFanPhone, nil)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "the entry into the general sale is refused")
	s.assertNoEntry(general, fan)
	assert.Len(t, s.eventTickets(fan), 4, "the fan still holds 4 Tickets")
}

func TestLotteryStory_PresaleWinnerBelowTheLimit(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// @spec stories/win-tickets-in-a-lottery "Presale winner below the limit"
	s := newLotteryStory(t, db)
	stock := s.stock("S席", 8000, 100)
	fan := s.newFan()
	general := s.openGeneralSale(s.winPresale(stock, fan, 2))

	entered := s.enter(general, stock.ID, fan, 2)
	assert.Equal(t, entity.LotteryEntryStateEntered, s.ownEntry(general, fan, entered.ID).State, "the entry is made")
}

func TestLotteryStory_PresaleWinnerPicksAnotherSeatType(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// @spec stories/win-tickets-in-a-lottery "Presale winner picks another seat type"
	s := newLotteryStory(t, db)
	sSeat := s.stock("S席", 8000, 100)
	aSeat := s.stock("A席", 6000, 100)
	fan := s.newFan()
	general := s.openGeneralSale(s.winPresale(sSeat, fan, 2))

	_, err := s.lottery.AuthorizeCard(s.ctx, general, aSeat.ID, fan, 1, storyFanName, storyFanPhone, nil)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "the entry for A席 is refused")
	s.assertNoEntry(general, fan)
	tickets := s.eventTickets(fan)
	require.Len(t, tickets, 2, "the fan still holds 2 Tickets")
	for _, tk := range tickets {
		order, err := s.orderRepo.Get(s.ctx, tk.OrderID)
		require.NoError(t, err)
		assert.Equal(t, sSeat.ID, order.TicketStockID, "the fan's Tickets are S席")
	}
}

func TestLotteryStory_SecondEntryIntoTheSameSale(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// @spec stories/win-tickets-in-a-lottery "Second entry into the same sale"
	s := newLotteryStory(t, db)
	stock := s.stock("S席", 8000, 100)
	end := storyWindowStart.Add(3 * 24 * time.Hour)
	sale := s.createLottery("抽選", storyWindowStart, end)

	s.now = storyWindowStart.Add(time.Hour)
	fan := s.newFan()
	first := s.enter(sale, stock.ID, fan, 2)

	_, err := s.lottery.AuthorizeCard(s.ctx, sale, stock.ID, fan, 1, storyFanName, storyFanPhone, nil)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "the second entry is refused")
	entries, err := s.lottery.ListByTicketSale(s.ctx, sale, fan)
	require.NoError(t, err)
	require.Len(t, entries, 1, "the fan keeps one entry")
	assert.Equal(t, first.ID, entries[0].ID)
}

func TestLotteryStory_CancelledDuringTheWindow(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// @spec stories/win-tickets-in-a-lottery "Cancelled during the window"
	// @spec components/usecase/lottery-entry/end-for-cancelled-event "Concert cancelled during the window"
	s := newLotteryStory(t, db)
	stock := s.stock("S席", 8000, 100)
	end := storyWindowStart.Add(3 * 24 * time.Hour)
	sale := s.createLottery("抽選", storyWindowStart, end)

	s.now = storyWindowStart.Add(time.Hour)
	fans := []entity.UserID{s.newFan(), s.newFan()}
	entries := []*entity.LotteryEntry{s.enter(sale, stock.ID, fans[0], 2), s.enter(sale, stock.ID, fans[1], 1)}

	s.now = storyWindowStart.Add(2 * time.Hour)
	s.cancel(s.eventID)

	for i, e := range entries {
		ref := e.Order.CardAuthorizationID
		got := s.ownEntry(sale, fans[i], e.ID)
		assert.Equal(t, entity.LotteryEntryStateLost, got.State, "fan %d's entry is Lost", i)
		assert.Equal(t, entity.LotteryEntryLostReasonConcertCancelled, got.LostReason)
		s.assertOrderVoid(e.ID)
		assert.Equal(t, entity.CardAuthorizationStatusReleased, s.cards.status(ref), "fan %d's card authorization is released", i)
		emails := s.emailsTo(fans[i])
		require.Len(t, emails, 1, "fan %d gets one email", i)
		assert.Contains(t, emails[0].subject, "公演中止")
		assert.Contains(t, emails[0].body, "公演が中止になったため")
		assert.Contains(t, emails[0].body, "カードへの請求はありません")
		assert.True(t, s.pushedLoss(e.ID), "fan %d is pushed once", i)
	}
	saleAfter, err := rdb.NewTicketSaleRepository(db, testOutbox).Get(s.ctx, sale)
	require.NoError(t, err)
	assert.Equal(t, end, saleAfter.SaleEndTime.UTC(), "the window of the sale is unchanged")

	// -- the window closes --
	s.closeAndDraw(end)
	s.issueWins()
	for i, e := range entries {
		assert.Zero(t, s.cards.charged(e.Order.CardAuthorizationID), "fan %d is never charged", i)
		assert.Empty(t, s.eventTickets(fans[i]), "fan %d holds no Ticket", i)
		assert.Len(t, s.emailsTo(fans[i]), 1, "fan %d gets no second email", i)
	}
}

func TestLotteryStory_CancelledBetweenTheDrawAndTheCharge(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// @spec stories/win-tickets-in-a-lottery "Cancelled between the draw and the charge"
	// @spec components/usecase/order/release-card-authorization "Win reverted for a cancelled concert"
	s := newLotteryStory(t, db)
	stock := s.stock("S席", 8000, 100)
	end := storyWindowStart.Add(3 * 24 * time.Hour)
	sale := s.createLottery("抽選", storyWindowStart, end)

	s.now = storyWindowStart.Add(time.Hour)
	fan := s.newFan()
	entry := s.enter(sale, stock.ID, fan, 2)

	// The draw records the entry Won; the member cancels before the issue
	// runs, which happens when the announcement of the draw is delivered.
	s.draw(end)
	require.Equal(t, entity.LotteryEntryStateWon, s.ownEntry(sale, fan, entry.ID).State)
	s.cancel(s.eventID)
	s.issueWins()

	ref := entry.Order.CardAuthorizationID
	got := s.ownEntry(sale, fan, entry.ID)
	assert.Equal(t, entity.LotteryEntryStateLost, got.State, "the entry is Lost")
	assert.Equal(t, entity.LotteryEntryLostReasonConcertCancelled, got.LostReason)
	assert.Zero(t, s.cards.charged(ref), "nothing is charged")
	assert.Equal(t, entity.CardAuthorizationStatusReleased, s.cards.status(ref), "the card authorization is released")
	assert.Empty(t, s.eventTickets(fan))
	s.assertOrderVoid(entry.ID)
	emails := s.emailsTo(fan)
	require.Len(t, emails, 1, "one email")
	assert.Contains(t, emails[0].body, "公演が中止になったため", "the fan is told the concert is cancelled")
}

func TestLotteryStory_EnteringAfterTheCancellation(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// @spec stories/win-tickets-in-a-lottery "Entering after the cancellation"
	s := newLotteryStory(t, db)
	stock := s.stock("S席", 8000, 100)
	end := storyWindowStart.Add(3 * 24 * time.Hour)
	sale := s.createLottery("抽選", storyWindowStart, end)

	s.now = storyWindowStart.Add(time.Hour)
	fan := s.newFan()
	// The fan authenticates the card with the entry screen open, and the
	// Event is cancelled before the confirmation arrives.
	authorization := s.authorize(sale, stock.ID, fan, 2, nil)
	s.cancel(s.eventID)

	_, err := s.lottery.Confirm(s.ctx, authorization.ID, &fan)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "the entry is refused")
	s.assertNoEntry(sale, fan)
	assert.Equal(t, entity.CardAuthorizationStatusReleased, s.cards.status(authorization.ID), "nothing stays held on the card")
	assert.Zero(t, s.cards.charged(authorization.ID))

	// Starting again from the screen is refused before any card authentication.
	_, err = s.lottery.AuthorizeCard(s.ctx, sale, stock.ID, fan, 2, storyFanName, storyFanPhone, nil)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	s.assertNoEntry(sale, fan)
}

func TestLotteryStory_AnotherDateOfTheSale(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// @spec stories/win-tickets-in-a-lottery "Another date of the sale"
	s := newLotteryStory(t, db)
	first := s.stock("S席", 8000, 100)
	secondEventID, second := s.secondEvent(8000, 100)
	end := storyWindowStart.Add(3 * 24 * time.Hour)
	created, err := s.sales.Create(s.ctx, s.organizerID, usecase.CreateTicketSaleInput{
		SeriesID: s.seriesID, Name: "抽選", Method: entity.TicketSaleMethodLottery,
		SaleStart: storyWindowStart, SaleEnd: end, EventIDs: []string{s.eventID, secondEventID},
	})
	require.NoError(t, err)
	sale := created.ID
	s.saleIDs = append(s.saleIDs, sale)

	s.now = storyWindowStart.Add(time.Hour)
	cancelledFan, otherFan := s.newFan(), s.newFan()
	cancelled := s.enter(sale, first.ID, cancelledFan, 2)
	other := s.enter(sale, second.ID, otherFan, 2)

	s.cancel(s.eventID)

	assert.Equal(t, entity.LotteryEntryStateLost, s.ownEntry(sale, cancelledFan, cancelled.ID).State, "the cancelled Event's entry is Lost")
	assert.Equal(t, entity.LotteryEntryStateEntered, s.ownEntry(sale, otherFan, other.ID).State, "the other Event's entry stays Entered")
	assert.Empty(t, s.emailsTo(otherFan))

	// -- the window closes: the other date is drawn --
	s.closeAndDraw(end)
	got := s.ownEntry(sale, otherFan, other.ID)
	assert.Equal(t, entity.LotteryEntryStateWon, got.State, "the other Event's entry is drawn")
	assert.NotNil(t, got.DrawPosition)
	assert.Equal(t, 1, s.cards.charged(other.Order.CardAuthorizationID))
	assert.Zero(t, s.cards.charged(cancelled.Order.CardAuthorizationID))
}
