package rdb_test

import (
	"sync"
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

// The story tests of stories/buy-tickets-first-come run the fan's first-come
// purchase against a real local Postgres and the real Stripe test API: the
// Organizer stocks a SeatType for the event and creates a FirstCome sale
// naming it, the fan holds tickets with CheckoutUseCase.Start, authorizes
// their card with CheckoutUseCase.AuthorizeCard (card authentication stands
// in as a server-side confirmation with a non-3DS test card), and places the
// order with OrderUseCase.IssueFromCheckout; Stripe's
// payment_intent.succeeded then announces ORDER.paid, which the ticket journey
// and the confirmation email consume. They build on the lottery story's
// wiring, so a presale and a general sale of one event share one stock.

// firstComeStory adds the first-come checkout and the resale listings to the
// lottery story's use cases, over the same repositories and clock.
type firstComeStory struct {
	*lotteryStory

	checkouts usecase.CheckoutUseCase
	resale    usecase.ResaleListingUseCase
	stockRepo *rdb.TicketStockRepository
}

// newFirstComeStory skips unless the Stripe test API and the local database
// are available, and seeds the Organizer's event as newLotteryStory does.
func newFirstComeStory(t *testing.T) *firstComeStory {
	t.Helper()
	s := &firstComeStory{lotteryStory: newLotteryStory(t), stockRepo: rdb.NewTicketStockRepository(testDB)}
	logger, err := logging.New()
	require.NoError(t, err)
	clock := usecase.Clock(func() time.Time { return s.now })
	eventState := rdb.NewEventPublishStateRepository(testDB)
	eventRepo := rdb.NewEventRepository(testDB)
	s.checkouts = usecase.NewCheckoutUseCase(usecase.CheckoutDeps{
		CheckoutRepo:             rdb.NewCheckoutRepository(testDB, testOutbox),
		SaleRepo:                 rdb.NewTicketSaleRepository(testDB, testOutbox),
		StockRepo:                s.stockRepo,
		EntryRepo:                rdb.NewLotteryEntryRepository(testDB, testOutbox),
		OrderRepo:                s.orderRepo,
		UserRepo:                 s.userRepo,
		IdentityVerificationRepo: rdb.NewIdentityVerificationRepository(testDB),
		EventState:               eventState,
		EventRepo:                eventRepo,
		CardAuthorizations:       payment.NewStripeCardAuthorizationRepository(s.key, payment.CheckoutCardAuthorizationPolicy, logger),
		Publisher:                s.published,
		Clock:                    clock,
		Logger:                   logger,
	})
	s.resale = usecase.NewResaleListingUseCase(usecase.ResaleListingDeps{
		Listings:   rdb.NewResaleListingRepository(testDB, testOutbox),
		Tickets:    rdb.NewTicketRepository(testDB, testOutbox),
		Orders:     s.orderRepo,
		Stocks:     s.stockRepo,
		EventState: eventState,
		Events:     eventRepo,
		Publisher:  s.published,
		Clock:      clock,
		Logger:     logger,
	})
	return s
}

// createFirstCome has the Organizer create a FirstCome sale naming the event,
// open from start to end, and returns it.
func (s *firstComeStory) createFirstCome(name string, start, end time.Time) entity.TicketSaleID {
	s.t.Helper()
	sale, err := s.sales.Create(s.ctx, s.organizerID, usecase.CreateTicketSaleInput{
		SeriesID:  s.seriesID,
		Name:      name,
		Method:    entity.TicketSaleMethodFirstCome,
		SaleStart: start,
		SaleEnd:   end,
		EventIDs:  []string{s.eventID},
	})
	require.NoError(s.t, err)
	return sale.ID
}

// openFirstCome has the Organizer create a FirstCome sale opening at
// storyWindowStart for 7 days, and moves the clock an hour into it.
func (s *firstComeStory) openFirstCome() entity.TicketSaleID {
	s.t.Helper()
	sale := s.createFirstCome("一般発売", storyWindowStart, storyWindowStart.Add(7*24*time.Hour))
	s.now = storyWindowStart.Add(time.Hour)
	return sale
}

// hold starts the fan's checkout for count tickets of the stock.
func (s *firstComeStory) hold(stockID entity.TicketStockID, fan entity.UserID, count int, code *entity.ReferralCode) *entity.Checkout {
	s.t.Helper()
	checkout, err := s.checkouts.Start(s.ctx, fan, stockID, count, code)
	require.NoError(s.t, err)
	return checkout
}

// authorizeCheckout gives the fan's name and phone number, opens the card
// authorization of the checkout's Order, completes card authentication and
// returns the card authorization's id.
func (s *firstComeStory) authorizeCheckout(checkout *entity.Checkout, fan entity.UserID) string {
	s.t.Helper()
	secret, err := s.checkouts.AuthorizeCard(s.ctx, fan, checkout.ID, storyFanName, storyFanPhone)
	require.NoError(s.t, err)
	require.NotEmpty(s.t, secret)
	got, err := s.checkouts.Get(s.ctx, fan, checkout.ID)
	require.NoError(s.t, err)
	id := got.Order.CardAuthorizationID
	require.NotEmpty(s.t, id)
	confirmCardAuthorization(s.t, s.key, id)
	return id
}

// place places the order of the authorized checkout, delivers Stripe's
// payment_intent.succeeded for its charge, and hands every ORDER.paid
// announcement to the ticket journey as its consumer does. It returns the
// Order and the ORDER.paid announcements made for it.
func (s *firstComeStory) place(checkout *entity.Checkout, fan entity.UserID, cardAuthorizationID string) (*entity.Order, []entity.OrderPaidData) {
	s.t.Helper()
	order, err := s.issuance.IssueFromCheckout(s.ctx, checkout.ID, &fan)
	require.NoError(s.t, err)
	require.NoError(s.t, s.webhooks.HandleEvent(s.ctx, usecase.StripeWebhookEvent{
		ProviderEventID:   "evt_" + uuid.NewV7().String(),
		Type:              usecase.StripeEventPaymentIntentSucceeded,
		ProviderPaymentID: cardAuthorizationID,
		ReceivedAt:        s.now,
	}))
	var announced []entity.OrderPaidData
	for _, paid := range s.published.paid {
		require.NoError(s.t, s.journeys.MarkPaid(s.ctx, paid))
		if paid.OrderID == string(order.ID) {
			announced = append(announced, paid)
		}
	}
	s.published.paid = nil
	return order, announced
}

// buy runs the fan's whole first-come purchase of count tickets of the stock:
// hold, card authorization and placing the order.
func (s *firstComeStory) buy(stockID entity.TicketStockID, fan entity.UserID, count int, code *entity.ReferralCode) *entity.Order {
	s.t.Helper()
	checkout := s.hold(stockID, fan, count, code)
	order, _ := s.place(checkout, fan, s.authorizeCheckout(checkout, fan))
	return order
}

// stockAt returns the stock with its sold and held counts at the story's clock.
func (s *firstComeStory) stockAt(id entity.TicketStockID) *entity.TicketStock {
	s.t.Helper()
	stock, err := s.stockRepo.Get(s.ctx, id, s.now)
	require.NoError(s.t, err)
	return stock
}

// assertTicketsOf asserts the fan holds count Issued Tickets for the event,
// all of the stock.
func (s *firstComeStory) assertTicketsOf(fan entity.UserID, stockID entity.TicketStockID, count int) {
	s.t.Helper()
	tickets := s.eventTickets(fan)
	require.Len(s.t, tickets, count)
	for _, tk := range tickets {
		assert.Equal(s.t, entity.TicketStatusIssued, tk.Status)
		order, err := s.orderRepo.Get(s.ctx, tk.OrderID)
		require.NoError(s.t, err)
		assert.Equal(s.t, stockID, order.TicketStockID)
	}
}

func TestFirstComeStory_TwoTicketsBought(t *testing.T) {
	// @spec stories/buy-tickets-first-come "Two tickets bought"
	s := newFirstComeStory(t)
	stock := s.stock("S席", 3000, 150)
	s.openFirstCome()

	fan := s.newFan()
	checkout := s.hold(stock.ID, fan, 2, nil)
	authorization := s.authorizeCheckout(checkout, fan)
	order, announced := s.place(checkout, fan, authorization)

	// -- charged 6000 yen --
	pi := s.paymentIntent(authorization)
	assert.Equal(t, stripe.PaymentIntentStatusSucceeded, pi.Status)
	assert.Equal(t, int64(6000), pi.AmountReceived, "6000 yen is charged")
	assert.Equal(t, entity.OrderStatusPaid, order.Status)
	assert.Equal(t, int64(6000), order.Amount)

	// -- 2 Issued Tickets of that SeatType for the event --
	s.assertTicketsOf(fan, stock.ID, 2)
	assert.Equal(t, stock.SeatTypeID, s.stockAt(stock.ID).SeatTypeID)

	// -- the ticket journey is Paid --
	journey, err := s.journeyRepo.Get(s.ctx, string(fan), s.eventID)
	require.NoError(t, err)
	assert.Equal(t, entity.TicketJourneyStatusPaid, journey.Status)

	// -- one confirmation email: the Order is announced as paid once, and the
	//    consumer sends the email at most once per Order --
	assert.Len(t, announced, 1, "the Order is announced as paid once")
}

func TestFirstComeStory_LastTicketTwoFans(t *testing.T) {
	// @spec stories/buy-tickets-first-come "Last ticket, two fans"
	s := newFirstComeStory(t)
	stock := s.stock("S席", 3000, 1)
	s.openFirstCome()

	fans := [2]entity.UserID{s.newFan(), s.newFan()}
	var (
		wg        sync.WaitGroup
		checkouts [2]*entity.Checkout
		errs      [2]error
	)
	for i := range fans {
		wg.Go(func() {
			checkouts[i], errs[i] = s.checkouts.Start(s.ctx, fans[i], stock.ID, 1, nil)
		})
	}
	wg.Wait()

	winner, loser := 0, 1
	if errs[0] != nil {
		winner, loser = 1, 0
	}
	require.NoError(t, errs[winner], "one fan holds the last ticket")
	assert.ErrorIs(t, errs[loser], apperr.ErrResourceExhausted, "the other is told it is sold out for now")

	// -- the fan holding it buys it --
	authorization := s.authorizeCheckout(checkouts[winner], fans[winner])
	s.place(checkouts[winner], fans[winner], authorization)
	s.assertTicketsOf(fans[winner], stock.ID, 1)
	assert.Equal(t, int64(3000), s.paymentIntent(authorization).AmountReceived)

	// -- the other fan is not charged: no Order, no Ticket, nothing held --
	orders, err := s.orderRepo.ListByUser(s.ctx, fans[loser])
	require.NoError(t, err)
	assert.Empty(t, orders, "the other fan has no Order to charge")
	assert.Empty(t, s.eventTickets(fans[loser]))
	_, err = s.checkouts.Start(s.ctx, fans[loser], stock.ID, 1, nil)
	assert.ErrorIs(t, err, apperr.ErrResourceExhausted, "the stock stays sold out")
}

func TestFirstComeStory_PresaleWinnersLeaveTheRest(t *testing.T) {
	// @spec stories/buy-tickets-first-come "Presale winners leave the rest"
	s := newFirstComeStory(t)
	stock := s.stock("S席", 3000, 150)

	// -- a lottery presale of the event in which 120 tickets were won: Paid
	//    Orders of Won entries stand in for the drawn and issued wins --
	presaleEnd := storyWindowStart.Add(2 * 24 * time.Hour)
	presale := s.createLottery("先行抽選", storyWindowStart, presaleEnd)
	setStockSold(t, &stockFixture{ID: stock.ID, SaleID: presale, EventID: s.eventID,
		SeatType: &entity.SeatType{ID: stock.SeatTypeID, Price: 3000}, Quantity: 150}, 120)

	// -- the first-come general sale opens a day after the presale --
	generalStart := presaleEnd.Add(24 * time.Hour)
	s.createFirstCome("一般発売", generalStart, generalStart.Add(7*24*time.Hour))
	s.now = generalStart.Add(time.Hour)

	// 30 tickets are held by 8 fans within the per-account limit of 4 ...
	type held struct {
		fan      entity.UserID
		checkout *entity.Checkout
	}
	var holds []held
	for _, n := range []int{4, 4, 4, 4, 4, 4, 4, 2} {
		fan := s.newFan()
		holds = append(holds, held{fan: fan, checkout: s.hold(stock.ID, fan, n, nil)})
	}
	// ... and the 31st ticket is refused.
	_, err := s.checkouts.Start(s.ctx, s.newFan(), stock.ID, 1, nil)
	assert.ErrorIs(t, err, apperr.ErrResourceExhausted, "the general sale sells at most 30 tickets")

	// One holder buys: the stock never passes its quantity.
	first := holds[0]
	s.place(first.checkout, first.fan, s.authorizeCheckout(first.checkout, first.fan))
	got := s.stockAt(stock.ID)
	assert.Equal(t, 124, got.SoldCount)
	assert.Equal(t, 26, got.HeldCount)
	assert.Zero(t, got.Remaining())
	_, err = s.checkouts.Start(s.ctx, s.newFan(), stock.ID, 1, nil)
	assert.ErrorIs(t, err, apperr.ErrResourceExhausted)
}

func TestFirstComeStory_HoldRanOut(t *testing.T) {
	// @spec stories/buy-tickets-first-come "Hold ran out"
	s := newFirstComeStory(t)
	stock := s.stock("S席", 3000, 150)
	s.openFirstCome()

	fan := s.newFan()
	checkout := s.hold(stock.ID, fan, 2, nil)
	authorization := s.authorizeCheckout(checkout, fan)

	// -- the fan places the order 1 minute after the hold expired --
	s.now = checkout.HoldExpireTime.Add(time.Minute)
	_, err := s.issuance.IssueFromCheckout(s.ctx, checkout.ID, &fan)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "the purchase is refused")
	pi := s.paymentIntent(authorization)
	assert.Equal(t, stripe.PaymentIntentStatusRequiresCapture, pi.Status, "nothing is charged")
	assert.Zero(t, pi.AmountReceived)

	// -- the release job, running every minute, releases the card
	//    authorization within 2 minutes --
	require.NoError(t, s.checkouts.ReleaseExpired(s.ctx))
	s.now = s.now.Add(time.Minute)
	require.NoError(t, s.checkouts.ReleaseExpired(s.ctx))
	pi = s.paymentIntent(authorization)
	assert.Equal(t, stripe.PaymentIntentStatusCanceled, pi.Status, "the card authorization is released")
	assert.Zero(t, pi.AmountReceived, "the fan is not charged")
	assert.Equal(t, entity.OrderStatusVoid, orderStatusOf(t, checkout.OrderID))
	assert.Empty(t, s.eventTickets(fan))
}

func TestFirstComeStory_DoubleTapEverywhere(t *testing.T) {
	// @spec stories/buy-tickets-first-come "Double tap everywhere"
	s := newFirstComeStory(t)
	stock := s.stock("S席", 3000, 150)
	s.openFirstCome()

	// -- continue tapped twice: one hold --
	fan := s.newFan()
	first := s.hold(stock.ID, fan, 2, nil)
	second := s.hold(stock.ID, fan, 2, nil)
	assert.Equal(t, first.ID, second.ID, "the second tap resumes the hold")
	assert.Equal(t, 2, s.stockAt(stock.ID).HeldCount, "the tickets are held once")

	// The card step repeated (a reload) opens the same card authorization.
	authorization := s.authorizeCheckout(first, fan)
	_, err := s.checkouts.AuthorizeCard(s.ctx, fan, first.ID, storyFanName, storyFanPhone)
	require.NoError(t, err)
	got, err := s.checkouts.Get(s.ctx, fan, first.ID)
	require.NoError(t, err)
	assert.Equal(t, authorization, got.Order.CardAuthorizationID, "one card authorization")

	// -- place order tapped twice at once: one charge, one Order --
	var (
		wg     sync.WaitGroup
		orders [2]*entity.Order
		errs   [2]error
	)
	for i := range orders {
		wg.Go(func() {
			orders[i], errs[i] = s.issuance.IssueFromCheckout(s.ctx, first.ID, &fan)
		})
	}
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	assert.Equal(t, orders[0].ID, orders[1].ID, "both taps return the same Order")
	_, announced := s.place(first, fan, authorization) // the webhook, and a third tap
	assert.Len(t, announced, 1, "the Order is announced as paid once")

	pi := s.paymentIntent(authorization)
	assert.Equal(t, int64(6000), pi.AmountReceived, "charged once")
	all, err := s.orderRepo.ListByUser(s.ctx, fan)
	require.NoError(t, err)
	require.Len(t, all, 1, "one Order")
	assert.Equal(t, entity.OrderStatusPaid, all[0].Status)
	s.assertTicketsOf(fan, stock.ID, 2)
}

func TestFirstComeStory_FanClosesTheTab(t *testing.T) {
	// @spec stories/buy-tickets-first-come "Fan closes the tab"
	s := newFirstComeStory(t)
	stock := s.stock("S席", 3000, 150)
	s.openFirstCome()

	// -- 18:00: the fan holds 2 tickets, authorizes their card and leaves --
	s.now = storyWindowStart.Add(9 * time.Hour) // the story's 18:00
	fan := s.newFan()
	checkout := s.hold(stock.ID, fan, 2, nil)
	authorization := s.authorizeCheckout(checkout, fan)
	assert.Equal(t, 148, s.stockAt(stock.ID).Remaining())
	holdTime := s.now

	// -- by 18:16 the 2 tickets are on sale again --
	s.now = holdTime.Add(16 * time.Minute)
	require.NoError(t, s.checkouts.ReleaseExpired(s.ctx))
	assert.Equal(t, 150, s.stockAt(stock.ID).Remaining(), "the tickets are on sale again")
	expired, err := s.checkouts.Get(s.ctx, fan, checkout.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.CheckoutStatusExpired, expired.Status)

	// -- by 18:17 the card authorization is released --
	s.now = holdTime.Add(17 * time.Minute)
	require.NoError(t, s.checkouts.ReleaseExpired(s.ctx))
	pi := s.paymentIntent(authorization)
	assert.Equal(t, stripe.PaymentIntentStatusCanceled, pi.Status, "the card authorization is released")
	assert.Zero(t, pi.AmountReceived)
}

// TestFirstComeStory_PresaleWinnerTopsUpInTheGeneralSale checks the
// first-come side of "A presale and a general sale share the per-account
// limit" (stories/win-tickets-in-a-lottery): a fan who won 2 S席 tickets in
// the lottery presale buys 2 more S席 tickets first come in the general sale,
// up to the event's limit of 4, and a fifth is refused.
func TestFirstComeStory_PresaleWinnerTopsUpInTheGeneralSale(t *testing.T) {
	s := newFirstComeStory(t)
	stock := s.stock("S席", 3000, 150)
	fan := s.newFan()
	presaleEnd := s.winPresale(stock, fan, 2)

	generalStart := presaleEnd.Add(24 * time.Hour)
	s.createFirstCome("一般発売", generalStart, generalStart.Add(7*24*time.Hour))
	s.now = generalStart.Add(time.Hour)

	// -- 3 more would pass the limit of 4 --
	_, err := s.checkouts.Start(s.ctx, fan, stock.ID, 3, nil)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "the hold over the limit is refused")

	// -- 2 more are bought --
	order := s.buy(stock.ID, fan, 2, nil)
	assert.Equal(t, entity.OrderStatusPaid, order.Status)
	assert.Equal(t, int64(6000), order.Amount)
	s.assertTicketsOf(fan, stock.ID, 4)

	// -- at the limit nothing more is held --
	_, err = s.checkouts.Start(s.ctx, fan, stock.ID, 1, nil)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	assert.Equal(t, 4, s.stockAt(stock.ID).SoldCount, "the presale win and the purchase are sold")
}

// TestFirstComeStory_SecondSeatTypeRefused checks the first-come side of "One
// seat type per account and event" (stories/win-tickets-in-a-lottery): a fan
// who bought S席 first come cannot hold A席 of the same event, while another
// fan can.
func TestFirstComeStory_SecondSeatTypeRefused(t *testing.T) {
	s := newFirstComeStory(t)
	sSeat := s.stock("S席", 5000, 150)
	aSeat := s.stock("A席", 3000, 150)
	s.openFirstCome()

	fan := s.newFan()
	s.buy(sSeat.ID, fan, 2, nil)

	_, err := s.checkouts.Start(s.ctx, fan, aSeat.ID, 1, nil)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "the hold of A席 is refused")
	assert.Zero(t, s.stockAt(aSeat.ID).HeldCount, "nothing of A席 is held")
	s.assertTicketsOf(fan, sSeat.ID, 2)

	// More of the same seat type is still allowed.
	s.hold(sSeat.ID, fan, 2, nil)
	// Another fan picks A席.
	s.hold(aSeat.ID, s.newFan(), 1, nil)
}
