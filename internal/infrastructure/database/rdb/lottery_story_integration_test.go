package rdb_test

import (
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
	stripe "github.com/stripe/stripe-go/v86"
	"uuid"
)

// The story tests of stories/win-tickets-in-a-lottery run the fan's whole
// lottery journey against a real local Postgres and the real Stripe test API:
// the Organizer creates the sale with TicketSaleUseCase.Create, fans enter
// with LotteryEntryUseCase Start/Authorize/Confirm (card authentication stands in
// as a server-side confirmation with a non-3DS test card), the draw job
// (DrawDueSales) runs once the injected clock passes the window's end, and
// the issuance job (IssueDueWins) and the payment_intent.succeeded webhook
// turn wins into Tickets. Opt-in like TestLotteryPipeline_Integration.

// storyFanName and storyFanPhone are the personal details every fan gives
// when authorizing a hold.
const (
	storyFanName  = "山田 花子"
	storyFanPhone = "+819012345678"
)

// storyEventStart is the start time of the story's event: well after every
// sale window the stories open.
var storyEventStart = saleWindowStart.Add(30 * 24 * time.Hour)

// recordingPublisher records the ORDER.paid announcements, standing in for
// NATS; the story hands them to the ticket journey as the consumer would.
type recordingPublisher struct {
	paid []entity.OrderPaidData
}

func (p *recordingPublisher) PublishEvent(_ context.Context, subject string, data any) error {
	p.record(subject, data)
	return nil
}

func (p *recordingPublisher) PublishEventWithID(_ context.Context, subject, _ string, data any) error {
	p.record(subject, data)
	return nil
}

func (p *recordingPublisher) record(subject string, data any) {
	if subject != entity.SubjectOrderPaid {
		return
	}
	switch d := data.(type) {
	case *entity.OrderPaidData:
		p.paid = append(p.paid, *d)
	case entity.OrderPaidData:
		p.paid = append(p.paid, d)
	}
}

// lotteryStory wires the real use cases over the real repositories and the
// Stripe test-mode hold port, all reading one injected clock.
type lotteryStory struct {
	t           *testing.T
	ctx         context.Context
	key         string
	now         time.Time
	organizerID string
	seriesID    string
	eventID     string

	sales     usecase.TicketSaleUseCase
	lottery   usecase.LotteryEntryUseCase
	issuance  usecase.OrderUseCase
	tickets   usecase.TicketUseCase
	journeys  usecase.TicketJourneyUseCase
	webhooks  usecase.StripeWebhookService
	published *recordingPublisher

	entryRepo   *rdb.LotteryEntryRepository
	orderRepo   entity.OrderRepository
	userRepo    *rdb.UserRepository
	journeyRepo *rdb.TicketJourneyRepository
}

// newLotteryStory skips unless the Stripe test API and the local database are
// available, empties the database, and seeds an Organizer with complete
// business details and one published, timed event of its Series.
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
		now:         saleWindowStart.Add(-24 * time.Hour),
		published:   &recordingPublisher{},
		entryRepo:   rdb.NewLotteryEntryRepository(testDB),
		orderRepo:   orderRepository(t, rdb.NewOrderRepository(testDB)),
		userRepo:    rdb.NewUserRepository(testDB),
		journeyRepo: rdb.NewTicketJourneyRepository(testDB),
	}
	s.seedOrganizerEvent()

	clock := usecase.Clock(func() time.Time { return s.now })
	saleRepo := rdb.NewTicketSaleRepository(testDB)
	ticketRepo := rdb.NewTicketRepository(testDB)
	identityVerificationRepo := rdb.NewIdentityVerificationRepository(testDB)
	organizerRepo := rdb.NewOrganizerRepository(testDB)
	eventState := rdb.NewEventPublishStateRepository(testDB)
	holds := payment.NewStripeCardAuthorizationRepository(key, payment.LotteryCardAuthorizationPolicy, logger)

	s.sales = usecase.NewTicketSaleUseCase(saleRepo, s.entryRepo, rdb.NewSeriesRepository(testDB), organizerRepo,
		eventState, rdb.NewEventStartTimeRepository(testDB), clock)
	s.lottery = usecase.NewLotteryEntryUseCase(saleRepo, s.entryRepo, ticketRepo, identityVerificationRepo, s.userRepo, holds, clock, logger)
	s.issuance = usecase.NewOrderUseCase(usecase.OrderDeps{
		IssuanceRepo:             rdb.NewIssuanceRepository(testDB),
		OrderRepo:                s.orderRepo,
		EntryRepo:                s.entryRepo,
		EventOrganizerRepo:       rdb.NewEventOrganizerRepository(testDB),
		OrganizerRepo:            organizerRepo,
		IdentityVerificationRepo: identityVerificationRepo,
		CheckoutRepo:             rdb.NewCheckoutRepository(testDB),
		TicketSaleRepo:           saleRepo,
		EventState:               eventState,
		CardAuthorizations:       payment.NewStripeCardAuthorizationRepository(key, payment.CheckoutCardAuthorizationPolicy, logger),
		Publisher:                s.published,
		Clock:                    clock,
		Logger:                   logger,
	})
	s.tickets = usecase.NewTicketUseCase(s.orderRepo, ticketRepo, rdb.NewReceptionLinkRepository(testDB),
		rdb.NewEventRepository(testDB), rdb.NewWalletPublicKeyRepository(testDB), rdb.NewAdmissionRepository(testDB),
		rdb.NewRejectedScanRepository(testDB), logger)
	s.journeys = usecase.NewTicketJourneyUseCase(s.journeyRepo, s.published, logger)
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
	_, err := rdb.NewOrganizerRepository(testDB).SetBusinessDetails(s.ctx, s.organizerID, entity.BusinessDetails{
		LegalName:           "株式会社テスト興行",
		RepresentativeName:  "代表 太郎",
		BusinessAddress:     "東京都渋谷区1-1-1",
		BusinessPhoneNumber: "+81312345678",
		ContactEmail:        "contact@example.test",
	})
	require.NoError(t, err)

	s.seriesID = uuid.NewV7().String()
	_, err = testDB.Pool.Exec(s.ctx, `
		INSERT INTO series (id, title, type, organizer_id, visibility, publish_state, published_at)
		VALUES ($1, 'Story Live', 'SINGLE', $2, 'PUBLIC', 'PUBLISHED', now())
	`, s.seriesID, s.organizerID)
	require.NoError(t, err)

	s.eventID = uuid.NewV7().String()
	_, err = testDB.Pool.Exec(s.ctx, `
		INSERT INTO events (id, series_id, venue_id, local_event_date, start_at) VALUES ($1, $2, $3, $4, $5)
	`, s.eventID, s.seriesID, seedVenue(t, "story-venue"), storyEventStart.Format(time.DateOnly), storyEventStart)
	require.NoError(t, err)
}

// createLottery has the Organizer create a Lottery sale of the event open
// from start to end, with one TicketType, and returns that TicketType.
func (s *lotteryStory) createLottery(name string, start, end time.Time, price int64, quantity, limit int) *entity.TicketType {
	s.t.Helper()
	view, err := s.sales.Create(s.ctx, s.organizerID, usecase.CreateTicketSaleInput{
		SeriesID:  s.seriesID,
		Name:      name,
		Method:    entity.TicketSaleMethodLottery,
		SaleStart: start,
		SaleEnd:   &end,
		TicketTypes: []usecase.TicketTypeInput{{
			EventID: s.eventID, Price: price, Quantity: quantity, PerAccountLimit: limit,
		}},
	})
	require.NoError(s.t, err)
	require.Len(s.t, view.TicketTypes, 1)
	return view.TicketTypes[0].TicketType
}

// newFan signs a new fan up.
func (s *lotteryStory) newFan() entity.UserID {
	s.t.Helper()
	return entity.UserID(seedUser(s.t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String()))
}

// authorize starts the fan's entry for count tickets, opens its hold and
// completes card authentication, leaving an authorized Draft.
func (s *lotteryStory) authorize(ttID entity.TicketTypeID, fan entity.UserID, count int) *entity.LotteryEntry {
	s.t.Helper()
	draft, err := s.lottery.Start(s.ctx, ttID, fan, count)
	require.NoError(s.t, err)
	_, err = s.lottery.AuthorizeCard(s.ctx, draft.ID, fan, storyFanName, storyFanPhone)
	require.NoError(s.t, err)
	authorized, err := s.entryRepo.Get(s.ctx, draft.ID)
	require.NoError(s.t, err)
	confirmHold(s.t, s.key, authorized.CardAuthorizationID)
	return authorized
}

// enter makes the fan's confirmed entry for count tickets.
func (s *lotteryStory) enter(ttID entity.TicketTypeID, fan entity.UserID, count int) *entity.LotteryEntry {
	s.t.Helper()
	authorized := s.authorize(ttID, fan, count)
	entered, err := s.lottery.Confirm(s.ctx, authorized.ID, fan)
	require.NoError(s.t, err)
	require.Equal(s.t, entity.LotteryEntryStateEntered, entered.State)
	return entered
}

// closeAndDraw advances the clock 30 seconds past the window's end and runs
// the draw job.
func (s *lotteryStory) closeAndDraw(end time.Time) {
	s.t.Helper()
	s.now = end.Add(30 * time.Second)
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
			ProviderPaymentID: e.CardAuthorizationID,
			ReceivedAt:        s.now,
		}))
	}
	for _, paid := range s.published.paid {
		require.NoError(s.t, s.journeys.MarkPaid(s.ctx, paid))
	}
	s.published.paid = nil
}

// ownEntry returns the fan's entry as ListByTicketType shows it.
func (s *lotteryStory) ownEntry(ttID entity.TicketTypeID, fan entity.UserID, id entity.LotteryEntryID) *entity.LotteryEntry {
	s.t.Helper()
	entries, err := s.lottery.ListByTicketType(s.ctx, ttID, fan)
	require.NoError(s.t, err)
	for _, e := range entries {
		if e.ID == id {
			return e
		}
	}
	s.t.Fatalf("entry %s is not among the fan's entries", id)
	return nil
}

// eventTickets returns the fan's Tickets for the event, as GetMyTickets
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
func (s *lotteryStory) paymentIntent(ref string) *stripe.PaymentIntent {
	s.t.Helper()
	pi, err := stripe.NewClient(s.key).V1PaymentIntents.Retrieve(s.ctx, ref, nil)
	require.NoError(s.t, err)
	return pi
}

// assertNoOrder asserts the entry produced no Order.
func (s *lotteryStory) assertNoOrder(id entity.LotteryEntryID) {
	s.t.Helper()
	_, err := s.orderRepo.GetByLotteryEntryID(s.ctx, id)
	assert.ErrorIs(s.t, err, apperr.ErrNotFound, "no Order may exist for the entry")
}

// winPresale runs a presale the fan wins with count tickets: entry, draw and
// issuance. It returns the end of the presale window.
func (s *lotteryStory) winPresale(fan entity.UserID, count int) time.Time {
	s.t.Helper()
	end := saleWindowStart.Add(2 * 24 * time.Hour)
	presale := s.createLottery("先行抽選", saleWindowStart, end, 8000, 100, 4)
	s.now = saleWindowStart.Add(time.Hour)
	entry := s.enter(presale.ID, fan, count)
	s.closeAndDraw(end)
	require.Equal(s.t, entity.LotteryEntryStateWon, s.ownEntry(presale.ID, fan, entry.ID).State)
	s.issueWins(entry)
	require.Len(s.t, s.eventTickets(fan), count)
	return end
}

func TestLotteryStory_DemandWithinCapacity(t *testing.T) {
	// @spec stories/win-tickets-in-a-lottery "Demand within quantity"
	s := newLotteryStory(t)
	end := saleWindowStart.Add(3 * 24 * time.Hour)
	tt := s.createLottery("抽選", saleWindowStart, end, 8000, 100, 0)

	s.now = saleWindowStart.Add(time.Hour)
	fan := s.newFan()
	entry := s.enter(tt.ID, fan, 2)

	// -- within 1 minute of the close: Won and 16000 yen charged --
	s.closeAndDraw(end)
	assert.Equal(t, entity.LotteryEntryStateWon, s.ownEntry(tt.ID, fan, entry.ID).State)
	pi := s.paymentIntent(entry.CardAuthorizationID)
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
	assert.Equal(t, int64(16000), order.Amount)
	journey, err := s.journeyRepo.Get(s.ctx, string(fan), s.eventID)
	require.NoError(t, err)
	assert.Equal(t, entity.TicketJourneyStatusPaid, journey.Status)
}

func TestLotteryStory_OversubscribedPhase(t *testing.T) {
	// @spec stories/win-tickets-in-a-lottery "Oversubscribed sale"
	s := newLotteryStory(t)
	end := saleWindowStart.Add(3 * 24 * time.Hour)
	tt := s.createLottery("抽選", saleWindowStart, end, 8000, 4, 4)

	// -- entries for 12 tickets against 4: exactly one entry of 4 is admitted --
	s.now = saleWindowStart.Add(time.Hour)
	type fanEntry struct {
		fan   entity.UserID
		entry *entity.LotteryEntry
	}
	var entries []fanEntry
	for range 3 {
		fan := s.newFan()
		entries = append(entries, fanEntry{fan: fan, entry: s.enter(tt.ID, fan, 4)})
	}

	s.closeAndDraw(end)
	var won []*entity.LotteryEntry
	var lost []fanEntry
	for _, fe := range entries {
		switch got := s.ownEntry(tt.ID, fe.fan, fe.entry.ID); got.State {
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

	// -- each fan not admitted: Lost, hold released, no Ticket --
	for _, fe := range lost {
		pi := s.paymentIntent(fe.entry.CardAuthorizationID)
		assert.Equal(t, stripe.PaymentIntentStatusCanceled, pi.Status, "the hold is released")
		assert.Zero(t, pi.AmountReceived, "the fan is not charged")
		assert.Empty(t, s.eventTickets(fe.fan), "the fan holds no Ticket for the event")
		s.assertNoOrder(fe.entry.ID)
	}
}

func TestLotteryStory_WithdrawAndReapply(t *testing.T) {
	// @spec stories/win-tickets-in-a-lottery "Withdraw and enter again"
	s := newLotteryStory(t)
	end := saleWindowStart.Add(3 * 24 * time.Hour)
	tt := s.createLottery("抽選", saleWindowStart, end, 8000, 100, 0)

	s.now = saleWindowStart.Add(time.Hour)
	fan := s.newFan()
	first := s.enter(tt.ID, fan, 2)
	require.NoError(t, s.lottery.Withdraw(s.ctx, first.ID, fan))
	assert.Equal(t, stripe.PaymentIntentStatusCanceled, s.paymentIntent(first.CardAuthorizationID).Status,
		"the first hold is released")

	s.now = s.now.Add(time.Hour)
	second := s.enter(tt.ID, fan, 2)
	require.NotEqual(t, first.ID, second.ID)
	require.NotEqual(t, first.CardAuthorizationID, second.CardAuthorizationID,
		"the new entry has a new hold")

	// -- only the new entry takes part in the draw --
	s.closeAndDraw(end)
	assert.Equal(t, entity.LotteryEntryStateWithdrawn, s.ownEntry(tt.ID, fan, first.ID).State)
	assert.Nil(t, s.ownEntry(tt.ID, fan, first.ID).DrawPosition, "the withdrawn entry is not drawn")
	assert.Equal(t, entity.LotteryEntryStateWon, s.ownEntry(tt.ID, fan, second.ID).State)
	assert.Equal(t, stripe.PaymentIntentStatusCanceled, s.paymentIntent(first.CardAuthorizationID).Status)
	assert.Equal(t, stripe.PaymentIntentStatusSucceeded, s.paymentIntent(second.CardAuthorizationID).Status)

	s.issueWins(second)
	assert.Len(t, s.eventTickets(fan), 2, "only the new entry's tickets are issued")
	s.assertNoOrder(first.ID)
}

func TestLotteryStory_EntryLeftUnconfirmed(t *testing.T) {
	// @spec stories/win-tickets-in-a-lottery "Entry left unconfirmed"
	s := newLotteryStory(t)
	end := saleWindowStart.Add(3 * 24 * time.Hour)
	tt := s.createLottery("抽選", saleWindowStart, end, 8000, 100, 0)

	s.now = saleWindowStart.Add(time.Hour)
	fan := s.newFan()
	draft := s.authorize(tt.ID, fan, 2) // the fan leaves before confirming
	require.Equal(t, entity.LotteryEntryStateDraft, draft.State)

	s.closeAndDraw(end)
	s.issueWins()
	assert.Equal(t, entity.LotteryEntryStateWithdrawn, s.ownEntry(tt.ID, fan, draft.ID).State)
	pi := s.paymentIntent(draft.CardAuthorizationID)
	assert.Equal(t, stripe.PaymentIntentStatusCanceled, pi.Status, "the hold is released at the draw")
	assert.Zero(t, pi.AmountReceived, "the fan is never charged")
	assert.Empty(t, s.eventTickets(fan))
	s.assertNoOrder(draft.ID)
}

func TestLotteryStory_PresaleWinnerAtTheLimit(t *testing.T) {
	// @spec stories/win-tickets-in-a-lottery "Presale winner at the limit"
	s := newLotteryStory(t)
	fan := s.newFan()
	presaleEnd := s.winPresale(fan, 4)

	generalStart := presaleEnd.Add(24 * time.Hour)
	general := s.createLottery("一般抽選", generalStart, generalStart.Add(3*24*time.Hour), 8000, 100, 4)
	s.now = generalStart.Add(time.Hour)

	_, err := s.lottery.Start(s.ctx, general.ID, fan, 1)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "the entry into the general sale is refused")
	entries, err := s.lottery.ListByTicketType(s.ctx, general.ID, fan)
	require.NoError(t, err)
	assert.Empty(t, entries, "no entry is made in the general sale")
	assert.Len(t, s.eventTickets(fan), 4, "the fan still holds 4 Tickets")
}

func TestLotteryStory_PresaleWinnerBelowTheLimit(t *testing.T) {
	// @spec stories/win-tickets-in-a-lottery "Presale winner below the limit"
	s := newLotteryStory(t)
	fan := s.newFan()
	presaleEnd := s.winPresale(fan, 2)

	generalStart := presaleEnd.Add(24 * time.Hour)
	general := s.createLottery("一般抽選", generalStart, generalStart.Add(3*24*time.Hour), 8000, 100, 4)
	s.now = generalStart.Add(time.Hour)

	entered := s.enter(general.ID, fan, 2)
	assert.Equal(t, entity.LotteryEntryStateEntered, s.ownEntry(general.ID, fan, entered.ID).State, "the entry is made")
}
