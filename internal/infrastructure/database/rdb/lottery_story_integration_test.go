package rdb_test

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v87"
	"uuid"
)

// The story tests of stories/win-tickets-in-a-lottery run the fan's whole
// lottery journey against a real local Postgres and the real Stripe test API:
// the Organizer stocks a SeatType for the event with SeatTypeUseCase.Create
// and TicketStockUseCase.Create and creates the sale with
// TicketSaleUseCase.Create, fans enter
// with LotteryEntryUseCase AuthorizeCard and Confirm (card authentication
// stands in as a server-side confirmation with a non-3DS test card), the draw
// job (DrawDueSales) runs once the injected clock passes the window's end, and
// the issuance job (IssueDueWins) and the payment_intent.succeeded webhook
// turn wins into Tickets. Opt-in like TestLotteryPipeline_Integration.
//
// An entry is stored only before the sale's end time by the database clock,
// so the story's windows are placed after the real current time.

// storyFanName and storyFanPhone are the personal details every fan gives
// when authorizing a card.
const (
	storyFanName  = "山田 花子"
	storyFanPhone = "+819012345678"
)

// storyWindowStart is when the story's first sale opens: two days after the
// real current time, so every window the stories open ends after the database
// clock.
var storyWindowStart = time.Now().UTC().Truncate(time.Hour).Add(48 * time.Hour)

// storyEventStart is the start time of the story's event: well after every
// sale window the stories open.
var storyEventStart = storyWindowStart.Add(30 * 24 * time.Hour)

// takePaid returns the ORDER.paid announcements of the story's event recorded
// since the previous take, as the mark-ticket-journey-paid consumer reads
// them; the story hands them to the ticket journey as the consumer would.
func (s *lotteryStory) takePaid() []entity.OrderPaidData {
	s.t.Helper()
	orders := announcedSince(s.t, s.paidMark, entity.SubjectOrderPaid, func() *entityv1.Order { return &entityv1.Order{} },
		func(o *entityv1.Order) bool {
			var eventID string
			require.NoError(s.t, testDB.Pool.QueryRow(s.ctx, `SELECT event_id FROM ticket_stocks WHERE id = $1`,
				o.GetTicketStockId().GetValue()).Scan(&eventID))
			return eventID == s.eventID
		})
	s.paidMark = outboxMark(s.t)
	paid := make([]entity.OrderPaidData, 0, len(orders))
	for _, o := range orders {
		paid = append(paid, entity.OrderPaidData{OrderID: o.GetId().GetValue(), UserID: o.GetUserId().GetValue(), EventID: s.eventID})
	}
	return paid
}

// lotteryStory wires the real use cases over the real repositories and the
// Stripe test-mode card authorization repository, all reading one injected clock.
type lotteryStory struct {
	t           *testing.T
	ctx         context.Context
	key         string
	now         time.Time
	organizerID string
	seriesID    string
	eventID     string

	seatTypes usecase.SeatTypeUseCase
	stocks    usecase.TicketStockUseCase
	sales     usecase.TicketSaleUseCase
	lottery   usecase.LotteryEntryUseCase
	issuance  usecase.OrderUseCase
	tickets   usecase.TicketUseCase
	journeys  usecase.TicketJourneyUseCase
	webhooks  usecase.StripeWebhookService
	// paidMark is the outbox offset up to which takePaid has read.
	paidMark int64

	orderRepo   entity.OrderRepository
	userRepo    *rdb.UserRepository
	journeyRepo *rdb.TicketJourneyRepository
}

// newLotteryStory skips unless the Stripe test API and the local database are
// available, empties the database, and seeds an Organizer with complete
// business details and one published, timed event of its Series with a
// per-account limit of 4.
func newLotteryStory(t *testing.T) *lotteryStory {
	t.Helper()
	if os.Getenv("STRIPE_INTEGRATION_TEST") != "1" {
		t.Skip("opt-in: set STRIPE_INTEGRATION_TEST=1 and STRIPE_SECRET_KEY=sk_test_… to run")
	}
	key := os.Getenv("STRIPE_SECRET_KEY")
	if !strings.HasPrefix(key, "sk_test_") && !strings.HasPrefix(key, "rk_test_") {
		t.Skip("STRIPE_SECRET_KEY must be a test-mode key (sk_test_… or rk_test_…)")
	}
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)

	logger, err := logging.New()
	require.NoError(t, err)
	s := &lotteryStory{
		t:   t,
		ctx: context.Background(),
		key: key,
		// The Organizer sets the sale up a day before it opens.
		now:         storyWindowStart.Add(-24 * time.Hour),
		paidMark:    outboxMark(t),
		orderRepo:   orderRepository(t, rdb.NewOrderRepository(testDB)),
		userRepo:    rdb.NewUserRepository(testDB, testOutbox),
		journeyRepo: rdb.NewTicketJourneyRepository(testDB, testOutbox),
	}
	s.seedOrganizerEvent()

	clock := usecase.Clock(func() time.Time { return s.now })
	saleRepo := rdb.NewTicketSaleRepository(testDB, testOutbox)
	stockRepo := rdb.NewTicketStockRepository(testDB)
	seatTypeRepo := rdb.NewSeatTypeRepository(testDB)
	entryRepo := rdb.NewLotteryEntryRepository(testDB, testOutbox)
	eventRepo := rdb.NewEventRepository(testDB)
	seriesRepo := rdb.NewSeriesRepository(testDB, testOutbox)
	ticketRepo := rdb.NewTicketRepository(testDB, testOutbox)
	identityVerificationRepo := rdb.NewIdentityVerificationRepository(testDB)
	organizerRepo := rdb.NewOrganizerRepository(testDB, testOutbox)
	eventOrganizerRepo := rdb.NewEventOrganizerRepository(testDB)
	eventState := rdb.NewEventPublishStateRepository(testDB)
	cardAuthorizations := payment.NewStripeCardAuthorizationRepository(key, payment.LotteryCardAuthorizationPolicy, logger)

	s.seatTypes = usecase.NewSeatTypeUseCase(seatTypeRepo, seriesRepo)
	s.stocks = usecase.NewTicketStockUseCase(stockRepo, seatTypeRepo, eventRepo, seriesRepo, eventOrganizerRepo, clock)
	s.sales = usecase.NewTicketSaleUseCase(saleRepo, stockRepo, entryRepo, s.orderRepo, seriesRepo, organizerRepo,
		eventRepo, eventState, rdb.NewEventStartTimeRepository(testDB), clock)
	s.lottery = usecase.NewLotteryEntryUseCase(lotteryEntryDeps(t, cardAuthorizations, clock, logger))
	s.issuance = usecase.NewOrderUseCase(usecase.OrderDeps{
		IssuanceRepo:             rdb.NewIssuanceRepository(testDB, testOutbox),
		OrderRepo:                s.orderRepo,
		EntryRepo:                entryRepo,
		EventOrganizerRepo:       eventOrganizerRepo,
		OrganizerRepo:            organizerRepo,
		IdentityVerificationRepo: identityVerificationRepo,
		CheckoutRepo:             rdb.NewCheckoutRepository(testDB, testOutbox),
		TicketSaleRepo:           saleRepo,
		TicketRepo:               ticketRepo,
		EventState:               eventState,
		EventRepo:                eventRepo,
		CardAuthorizations:       payment.NewStripeCardAuthorizationRepository(key, payment.CheckoutCardAuthorizationPolicy, logger),
		Clock:                    clock,
		Logger:                   logger,
	})
	s.tickets = usecase.NewTicketUseCase(s.orderRepo, ticketRepo, rdb.NewScannerRepository(testDB),
		eventRepo, rdb.NewWalletPublicKeyRepository(testDB), rdb.NewAdmissionRepository(testDB),
		rdb.NewRejectedScanRepository(testDB), logger)
	s.journeys = usecase.NewTicketJourneyUseCase(s.journeyRepo, logger)
	// No refund flows in these stories, so the refund use case stays unset.
	s.webhooks = usecase.NewStripeWebhookService(rdb.NewProcessedWebhookEventRepository(testDB), s.orderRepo, nil,
		s.issuance, logger)
	return s
}

// seedOrganizerEvent inserts an Organizer with complete business details and
// a published Series with one event starting at storyEventStart.
func (s *lotteryStory) seedOrganizerEvent() {
	t := s.t
	s.organizerID = seedOrganizer(t)
	_, err := rdb.NewOrganizerRepository(testDB, testOutbox).SetBusinessDetails(s.ctx, s.organizerID, entity.BusinessDetails{
		LegalName:           "株式会社テスト興行",
		RepresentativeName:  "代表 太郎",
		BusinessAddress:     "東京都渋谷区1-1-1",
		BusinessPhoneNumber: "+81312345678",
		ContactEmail:        "contact@example.test",
	})
	require.NoError(t, err)

	s.seriesID = uuid.NewV7().String()
	_, err = testDB.Pool.Exec(s.ctx, `
		INSERT INTO series (id, title, type, organizer_id, visibility)
		VALUES ($1, 'Story Live', 'SINGLE', $2, 'PUBLIC')
	`, s.seriesID, s.organizerID)
	require.NoError(t, err)

	s.eventID = uuid.NewV7().String()
	_, err = testDB.Pool.Exec(s.ctx, `
		INSERT INTO events (id, series_id, venue_id, local_event_date, start_at, publish_state, per_account_limit) VALUES ($1, $2, $3, $4, $5, 'PUBLISHED', 4)
	`, s.eventID, s.seriesID, seedVenue(t, "story-venue"), storyEventStart.Format(time.DateOnly), storyEventStart)
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
	return sale.ID
}

// newFan signs a new fan up.
func (s *lotteryStory) newFan() entity.UserID {
	s.t.Helper()
	return entity.UserID(seedUser(s.t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String()))
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
	confirmCardAuthorization(s.t, s.key, authorization.ID)
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

// closeAndDraw advances the clock 90 seconds past the window's end, within
// the 2 minutes the story allows, and runs the draw job.
func (s *lotteryStory) closeAndDraw(end time.Time) {
	s.t.Helper()
	s.now = end.Add(90 * time.Second)
	require.NoError(s.t, s.lottery.DrawDueSales(s.ctx))
}

// issueWins advances the clock 30 more seconds, runs the issuance job,
// delivers Stripe's payment_intent.succeeded for each captured win, and hands
// every ORDER.paid announcement to the ticket journey as its consumer does.
func (s *lotteryStory) issueWins(won ...*entity.LotteryEntry) {
	s.t.Helper()
	s.now = s.now.Add(30 * time.Second)
	require.NoError(s.t, s.issuance.IssueDueWins(s.ctx))
	for _, e := range won {
		require.NoError(s.t, s.webhooks.HandleEvent(s.ctx, usecase.StripeWebhookEvent{
			ProviderEventID:   "evt_" + uuid.NewV7().String(),
			Type:              usecase.StripeEventPaymentIntentSucceeded,
			ProviderPaymentID: e.Order.CardAuthorizationID,
			ReceivedAt:        s.now,
		}))
	}
	for _, paid := range s.takePaid() {
		require.NoError(s.t, s.journeys.MarkPaid(s.ctx, paid))
	}
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

// paymentIntent reads a PaymentIntent from the Stripe test API.
func (s *lotteryStory) paymentIntent(cardAuthorizationID string) *stripe.PaymentIntent {
	s.t.Helper()
	pi, err := stripe.NewClient(s.key).V1PaymentIntents.Retrieve(s.ctx, cardAuthorizationID, nil)
	require.NoError(s.t, err)
	return pi
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
	// @spec stories/win-tickets-in-a-lottery "Demand within quantity"
	s := newLotteryStory(t)
	stock := s.stock("S席", 8000, 100)
	end := storyWindowStart.Add(3 * 24 * time.Hour)
	sale := s.createLottery("抽選", storyWindowStart, end)

	s.now = storyWindowStart.Add(time.Hour)
	fan := s.newFan()
	entry := s.enter(sale, stock.ID, fan, 2)

	// -- within 2 minutes of the close: Won and 16000 yen charged --
	s.closeAndDraw(end)
	assert.Equal(t, entity.LotteryEntryStateWon, s.ownEntry(sale, fan, entry.ID).State)
	pi := s.paymentIntent(entry.Order.CardAuthorizationID)
	assert.Equal(t, stripe.PaymentIntentStatusSucceeded, pi.Status)
	assert.Equal(t, int64(16000), pi.AmountReceived, "16000 yen is charged")

	// -- within 1 more minute: 2 Issued Tickets, the fan's name and phone on
	//    their faces, and the ticket journey Paid --
	s.issueWins(entry)
	tickets := s.eventTickets(fan)
	require.Len(t, tickets, 2)
	user, err := s.userRepo.Get(s.ctx, string(fan))
	require.NoError(t, err)
	for _, tk := range tickets {
		assert.Equal(t, entity.TicketStatusIssued, tk.Status)
		face := tk.Face(user)
		assert.Equal(t, storyFanName, face.FullName)
		assert.Equal(t, storyFanPhone, face.PhoneNumber)
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
}

func TestLotteryStory_OversubscribedSale(t *testing.T) {
	// @spec stories/win-tickets-in-a-lottery "Oversubscribed sale"
	s := newLotteryStory(t)
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

	// -- each fan not admitted: Lost, card authorization released, no Ticket --
	for _, fe := range lost {
		pi := s.paymentIntent(fe.entry.Order.CardAuthorizationID)
		assert.Equal(t, stripe.PaymentIntentStatusCanceled, pi.Status, "the card authorization is released")
		assert.Zero(t, pi.AmountReceived, "the fan is not charged")
		assert.Empty(t, s.eventTickets(fe.fan), "the fan holds no Ticket for the event")
		s.assertOrderVoid(fe.entry.ID)
	}
}

func TestLotteryStory_CountChangedBeforeConfirming(t *testing.T) {
	// @spec stories/win-tickets-in-a-lottery "Count changed before confirming"
	s := newLotteryStory(t)
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
	pi := s.paymentIntent(two.ID)
	assert.Equal(t, stripe.PaymentIntentStatusRequiresPaymentMethod, pi.Status, "nothing is held for 2 tickets")
	assert.Zero(t, pi.AmountCapturable)
	assert.Zero(t, pi.AmountReceived)
}

func TestLotteryStory_BrowserClosedAfterCardAuthentication(t *testing.T) {
	// @spec stories/win-tickets-in-a-lottery "Browser closed after card authentication"
	s := newLotteryStory(t)
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
	// @spec stories/win-tickets-in-a-lottery "Entry made only after the window closed"
	s := newLotteryStory(t)
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
	pi := s.paymentIntent(authorization.ID)
	assert.Equal(t, stripe.PaymentIntentStatusCanceled, pi.Status, "the card authorization is released")
	assert.Zero(t, pi.AmountReceived, "the fan is never charged")

	// The payment provider's notice, arriving later still, changes nothing.
	_, err = s.lottery.Confirm(s.ctx, authorization.ID, nil)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	s.assertNoEntry(sale, fan)
}

func TestLotteryStory_CardAuthenticationAbandoned(t *testing.T) {
	// @spec stories/win-tickets-in-a-lottery "Card authentication abandoned"
	s := newLotteryStory(t)
	stock := s.stock("S席", 8000, 100)
	end := storyWindowStart.Add(3 * 24 * time.Hour)
	sale := s.createLottery("抽選", storyWindowStart, end)

	s.now = storyWindowStart.Add(time.Hour)
	fan := s.newFan()
	authorization := s.authorizeCard(sale, stock.ID, fan, 2, nil) // the fan leaves before card authentication

	s.closeAndDraw(end)
	s.issueWins()
	s.assertNoEntry(sale, fan)
	pi := s.paymentIntent(authorization.ID)
	assert.Equal(t, stripe.PaymentIntentStatusRequiresPaymentMethod, pi.Status, "nothing is held on the card")
	assert.Zero(t, pi.AmountReceived, "the fan is never charged")
	assert.Empty(t, s.eventTickets(fan))
}

// TestLotteryStory_EntryFromAReferralLink checks the lottery sentence of
// stories/attribute-a-purchase-to-a-referral-link: an entry authorized with a
// code carries it on its Order, and once the Order is Paid the Organizer's
// sales count it under that code.
func TestLotteryStory_EntryFromAReferralLink(t *testing.T) {
	s := newLotteryStory(t)
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
	// @spec stories/win-tickets-in-a-lottery "Presale winner at the limit"
	s := newLotteryStory(t)
	stock := s.stock("S席", 8000, 100)
	fan := s.newFan()
	general := s.openGeneralSale(s.winPresale(stock, fan, 4))

	_, err := s.lottery.AuthorizeCard(s.ctx, general, stock.ID, fan, 1, storyFanName, storyFanPhone, nil)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "the entry into the general sale is refused")
	s.assertNoEntry(general, fan)
	assert.Len(t, s.eventTickets(fan), 4, "the fan still holds 4 Tickets")
}

func TestLotteryStory_PresaleWinnerBelowTheLimit(t *testing.T) {
	// @spec stories/win-tickets-in-a-lottery "Presale winner below the limit"
	s := newLotteryStory(t)
	stock := s.stock("S席", 8000, 100)
	fan := s.newFan()
	general := s.openGeneralSale(s.winPresale(stock, fan, 2))

	entered := s.enter(general, stock.ID, fan, 2)
	assert.Equal(t, entity.LotteryEntryStateEntered, s.ownEntry(general, fan, entered.ID).State, "the entry is made")
}

func TestLotteryStory_PresaleWinnerPicksAnotherSeatType(t *testing.T) {
	// @spec stories/win-tickets-in-a-lottery "Presale winner picks another seat type"
	s := newLotteryStory(t)
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
	// @spec stories/win-tickets-in-a-lottery "Second entry into the same sale"
	s := newLotteryStory(t)
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
