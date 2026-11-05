package rdb_test

import (
	"context"
	"strings"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v87"
	"google.golang.org/protobuf/proto"

	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/liverty-music/backend/internal/usecase"
)

// The story tests of stories/resell-a-ticket that buy a listing run the whole
// resale draw over the first-come story's wiring: fans apply through the
// fan-facing ResaleEntryUseCase with card authorizations on the Stripe test
// API, the worker draws, issues, releases and refunds as workflows, and the
// story hands every announcement to the consumer that receives it.

var storyJST = time.FixedZone("JST", 9*60*60)

// resaleStory adds the fan's ResaleEntryUseCase and the consumers of the
// resale announcements to the first-come story.
type resaleStory struct {
	*firstComeStory

	entries  usecase.ResaleEntryUseCase
	changed  *event.ResaleEntryChangedConsumer
	sold     *event.ResaleListingSoldConsumer
	refunded *event.ResaleListingRefundedConsumer
	// mark is the outbox offset up to which the story handed resale
	// announcements to their consumers.
	mark int64
}

func newResaleStory(t *testing.T) *resaleStory {
	t.Helper()
	fc := newFirstComeStory(t)
	s := &resaleStory{firstComeStory: fc, mark: outboxMark(t)}
	clock := usecase.Clock(func() time.Time { return s.now })
	cards := rdb.NewCardAuthorizationRepository(testDB, payment.NewStripeCardAuthorizationProvider(fc.key, payment.LotteryCardAuthorizationPolicy, fc.logger))
	s.entries = usecase.NewResaleEntryUseCase(usecase.ResaleEntryDeps{
		EntryRepo:                rdb.NewResaleEntryRepository(testDB, testOutbox),
		StockRepo:                fc.stockRepo,
		SaleRepo:                 rdb.NewTicketSaleRepository(testDB, testOutbox),
		OrderRepo:                fc.orderRepo,
		EventRepo:                rdb.NewEventRepository(testDB),
		EventState:               rdb.NewEventPublishStateRepository(testDB),
		IdentityVerificationRepo: rdb.NewIdentityVerificationRepository(testDB),
		UserRepo:                 fc.userRepo,
		CardAuthorizations:       cards,
		Clock:                    clock,
		Logger:                   fc.logger,
	})
	// The consumers' use cases only start workflows, which the worker runs.
	s.changed = event.NewResaleEntryChangedConsumer(usecase.NewOrderUseCase(usecase.OrderDeps{
		Workflows: fc.client, Clock: clock, Logger: fc.logger,
	}), fc.notifications, fc.logger)
	s.sold = event.NewResaleListingSoldConsumer(usecase.NewResaleListingUseCase(usecase.ResaleListingDeps{
		Workflows: fc.client, Clock: clock, Logger: fc.logger,
	}), fc.logger)
	s.refunded = event.NewResaleListingRefundedConsumer(fc.notifications, fc.logger)
	return s
}

// soldOutTo has a fan buy every ticket of a new stock of count tickets of
// 5000 yen first come and returns the stock and the fan.
func (s *resaleStory) soldOutTo(count int) (*entity.TicketStock, entity.UserID) {
	s.t.Helper()
	stock := s.stock("S席", 5000, count)
	s.openFirstCome()
	fan := s.newFan()
	s.buy(stock.ID, fan, count, nil)
	require.Equal(s.t, entity.TicketStockStateSoldOut, s.stockAt(stock.ID).State())
	return stock, fan
}

// apply has the fan apply for count tickets of the stock with a card of the
// Stripe test payment method, authenticated in the browser, and returns the
// Entered entry.
func (s *resaleStory) apply(stockID entity.TicketStockID, fan entity.UserID, count int, paymentMethod string) *entity.ResaleEntry {
	s.t.Helper()
	ca, err := s.entries.AuthorizeCard(s.ctx, stockID, fan, count, storyFanName, storyFanPhone, nil)
	require.NoError(s.t, err)
	pi, err := stripe.NewClient(s.key).V1PaymentIntents.Confirm(s.ctx, ca.ProviderID, &stripe.PaymentIntentConfirmParams{
		PaymentMethod: stripe.String(paymentMethod),
	})
	require.NoError(s.t, err)
	require.Equal(s.t, stripe.PaymentIntentStatusRequiresCapture, pi.Status)
	view, err := s.entries.Confirm(s.ctx, ca.ID, &fan)
	require.NoError(s.t, err)
	require.Equal(s.t, entity.ResaleEntryStateEntered, view.Entry.State)
	return view.Entry
}

// noonAfter returns the first 12:00 Japan time after t.
func noonAfter(t time.Time) time.Time {
	local := t.In(storyJST)
	noon := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, storyJST)
	if !noon.After(t) {
		noon = noon.AddDate(0, 0, 1)
	}
	return noon
}

// draw moves the clock to the draw time at, has the worker run draw-resale
// for it and each stock draw it starts, and hands every announcement that
// follows to its consumer until the work is done.
func (s *resaleStory) draw(at time.Time, stocks ...entity.TicketStockID) {
	s.t.Helper()
	s.now = at
	id := usecase.WorkflowDrawResale + ":" + at.UTC().Format(time.RFC3339)
	require.NoError(s.t, s.client.Start(s.ctx, usecase.WorkflowDrawResale, id, at))
	_, err := usecase.Await[int](s.ctx, s.client, id, storyWait)
	require.NoError(s.t, err)
	for _, stock := range stocks {
		// A stock without Entered entries at the time is not drawn.
		err := s.client.Await(s.ctx, usecase.DrawResaleStockWorkflowID(stock, at), storyWait, nil)
		if !errorsIsNotFound(err) {
			require.NoError(s.t, err, "draw of stock %s", stock)
		}
	}
	s.followUp()
}

// followUp hands the resale announcements recorded since the last hand-over
// to their consumers and awaits the workflows they start, until none is left.
func (s *resaleStory) followUp() {
	s.t.Helper()
	for {
		next := outboxMark(s.t)
		if next == s.mark {
			return
		}
		mark := s.mark
		s.mark = next
		for _, msg := range announcedMessages(s.t, mark, entity.SubjectResaleEntryChanged) {
			require.NoError(s.t, s.changed.HandleStartWorkflow(msg))
			require.NoError(s.t, s.changed.HandleSendResaleEntryEnded(msg))
			var e entityv1.ResaleEntry
			require.NoError(s.t, proto.Unmarshal(msg.Payload, &e))
			orderID := entity.OrderID(e.GetOrderId().GetValue())
			switch {
			case e.GetState() == entityv1.ResaleEntryState_RESALE_ENTRY_STATE_WON:
				s.await(usecase.IssueWorkflowID(orderID))
			case e.GetLostReason() == entityv1.ResaleEntryLostReason_RESALE_ENTRY_LOST_REASON_PAYMENT_NOT_COMPLETED_BEFORE_SHOW:
			default:
				s.await(usecase.ReleaseWorkflowID(orderID))
			}
		}
		for _, msg := range announcedMessages(s.t, mark, entity.SubjectResaleListingSold) {
			require.NoError(s.t, s.sold.HandleStartRefund(msg))
			var l entityv1.ResaleListing
			require.NoError(s.t, proto.Unmarshal(msg.Payload, &l))
			s.await(usecase.RefundSoldWorkflowID(entity.ResaleListingID(l.GetId().GetValue())))
		}
		for _, msg := range announcedMessages(s.t, mark, entity.SubjectResaleListingRefunded) {
			require.NoError(s.t, s.refunded.Handle(msg))
		}
		for _, paid := range s.takePaid() {
			require.NoError(s.t, s.notifications.SendOrderConfirmation(s.ctx, paid))
		}
	}
}

// entry reads the entry.
func (s *resaleStory) entry(id entity.ResaleEntryID) *entity.ResaleEntry {
	s.t.Helper()
	e, err := rdb.NewResaleEntryRepository(testDB, testOutbox).Get(s.ctx, id)
	require.NoError(s.t, err)
	return e
}

// intentStatus reads the PaymentIntent of the entry's card authorization.
func (s *resaleStory) intentStatus(e *entity.ResaleEntry) stripe.PaymentIntentStatus {
	s.t.Helper()
	return s.paymentIntent(e.Order.CardAuthorizationID).Status
}

// pushed reports whether a push of the type was started for the entry.
func (s *resaleStory) pushed(typ entity.NotificationType, id entity.ResaleEntryID) bool {
	s.pushes.mu.Lock()
	defer s.pushes.mu.Unlock()
	return s.pushes.ids["deliver:"+string(typ)+":"+string(id)]
}

// sumSplits sums the splits of the Held Settlements of the event's Orders.
func (s *resaleStory) sumSplits() int64 {
	s.t.Helper()
	var sum int64
	require.NoError(s.t, testDB.Pool.QueryRow(s.ctx, `
		SELECT COALESCE(SUM(sp.amount), 0)::bigint FROM settlement_splits sp
		JOIN settlements st ON st.id = sp.settlement_id
		WHERE st.event_id = $1 AND st.status = 1`, s.eventID).Scan(&sum))
	return sum
}

func TestResaleDrawStory_OneOfTwoTicketsResold(t *testing.T) {
	// @spec stories/resell-a-ticket "One of two tickets resold"
	// @spec stories/resell-a-ticket "Payout after a resale"
	// @spec components/usecase/order/issue "Won resale entry"
	s := newResaleStory(t)
	_, err := testDB.Pool.Exec(s.ctx, `UPDATE organizers SET platform_fee_rate_bps = 500 WHERE id = $1`, s.organizerID)
	require.NoError(t, err)
	stock, seller := s.soldOutTo(2)
	listed := s.listOne(seller)
	buyer := s.newFan()
	entry := s.apply(stock.ID, buyer, 1, "pm_card_visa")

	s.draw(noonAfter(s.now), stock.ID)

	won := s.entry(entry.ID)
	assert.Equal(t, entity.ResaleEntryStateWon, won.State)
	assert.Equal(t, entity.OrderStatusPaid, won.Order.Status, "the other fan is charged and issued")
	assert.Equal(t, stripe.PaymentIntentStatusSucceeded, s.intentStatus(won))
	assert.Equal(t, int64(5000), s.paymentIntent(won.Order.CardAuthorizationID).AmountReceived)
	s.assertValidForEntry(buyer, 1)

	sellerTickets := s.eventTickets(seller)
	statuses := map[entity.TicketStatus]int{}
	for _, tk := range sellerTickets {
		statuses[tk.Status]++
	}
	assert.Equal(t, 1, statuses[entity.TicketStatusIssued], "the first fan keeps one Issued Ticket")
	assert.Equal(t, 1, statuses[entity.TicketStatusVoided], "and the resold one is Voided")

	listings := s.ownListings(seller)
	require.Len(t, listings, 1)
	assert.Equal(t, listed.Listing.ID, listings[0].Listing.ID)
	assert.Equal(t, entity.ResaleListingStatusSold, listings[0].Status)
	require.NotNil(t, listings[0].Listing.RefundTime, "refunded")
	require.NotNil(t, listings[0].Listing.SoldTime)
	assert.LessOrEqual(t, listings[0].Listing.RefundTime.Sub(*listings[0].Listing.SoldTime), 2*time.Minute)
	sold := 0
	for _, m := range s.emailsTo(seller) {
		if strings.Contains(m.subject, "売れました") {
			sold++
		}
	}
	assert.Equal(t, 1, sold, "one sold email")

	// The Organizer is paid once per seat: 2 × 5000 yen less 5%.
	assert.Equal(t, int64(9500), s.sumSplits())
}

func TestResaleDrawStory_TwoFansOneListing(t *testing.T) {
	// @spec stories/resell-a-ticket "Two fans, one listing"
	s := newResaleStory(t)
	stock, seller := s.soldOutTo(2)
	s.listOne(seller)
	a := s.apply(stock.ID, s.newFan(), 1, "pm_card_visa")
	b := s.apply(stock.ID, s.newFan(), 1, "pm_card_visa")

	s.draw(noonAfter(s.now), stock.ID)

	states := map[entity.ResaleEntryState][]*entity.ResaleEntry{}
	for _, e := range []*entity.ResaleEntry{s.entry(a.ID), s.entry(b.ID)} {
		states[e.State] = append(states[e.State], e)
	}
	require.Len(t, states[entity.ResaleEntryStateWon], 1, "the draw allocates the listing to one of them")
	require.Len(t, states[entity.ResaleEntryStateEntered], 1, "the other waits for the next draw")
	assert.Equal(t, stripe.PaymentIntentStatusSucceeded, s.intentStatus(states[entity.ResaleEntryStateWon][0]))
	assert.Equal(t, stripe.PaymentIntentStatusRequiresCapture, s.intentStatus(states[entity.ResaleEntryStateEntered][0]), "and is not charged")
}

func TestResaleDrawStory_WithdrawnBeforeTheDraw(t *testing.T) {
	// @spec stories/resell-a-ticket "Withdrawn before the draw"
	s := newResaleStory(t)
	stock, seller := s.soldOutTo(2)
	listed := s.listOne(seller)
	entry := s.apply(stock.ID, s.newFan(), 1, "pm_card_visa")
	_, err := s.resale.Withdraw(s.ctx, seller, listed.Listing.ID)
	require.NoError(t, err)

	s.draw(noonAfter(s.now), stock.ID)

	assert.Equal(t, entity.ResaleEntryStateEntered, s.entry(entry.ID).State, "the draw allocates nothing from that listing")
	s.assertValidForEntry(seller, 2)
}

func TestResaleDrawStory_NobodyWinsAtTheLastDraw(t *testing.T) {
	// @spec stories/resell-a-ticket "Nobody wins at the last draw"
	s := newResaleStory(t)
	// The show is 8 days after the first-come sale (7 days) opens, so the
	// card authorization still holds at the last draw.
	start := storyWindowStart.Add(8 * 24 * time.Hour)
	_, err := testDB.Pool.Exec(s.ctx, `UPDATE events SET start_at = $2, local_event_date = $3 WHERE id = $1`,
		s.eventID, start, start.In(storyJST).Format(time.DateOnly))
	require.NoError(t, err)
	stock, seller := s.soldOutTo(2)
	s.listOne(seller)
	fan := s.newFan()
	entry := s.apply(stock.ID, fan, 2, "pm_card_visa")

	s.draw(entity.ResaleDeadlineAt(start), stock.ID)

	lost := s.entry(entry.ID)
	assert.Equal(t, entity.ResaleEntryStateLost, lost.State)
	assert.Equal(t, entity.ResaleEntryLostReasonNoTicketLeft, lost.LostReason)
	assert.Equal(t, stripe.PaymentIntentStatusCanceled, s.intentStatus(lost), "not charged; the card authorization is released")
	assert.Len(t, s.emailsTo(fan), 1, "the loss email")
	assert.True(t, s.pushed(entity.NotificationTypeResaleEntryLost, entry.ID), "and push")
}

func TestResaleDrawStory_AuthorizationAboutToExpire(t *testing.T) {
	// @spec stories/resell-a-ticket "Authorization about to expire"
	s := newResaleStory(t)
	stock, seller := s.soldOutTo(2)
	s.listOne(seller)
	fan := s.newFan()
	// An American Express card authorization lasts about 7 days.
	entry := s.apply(stock.ID, fan, 1, "pm_card_amex")
	expire := s.entry(entry.ID).CardAuthorizationExpireTime
	require.NotNil(t, expire)

	s.draw(noonAfter(expire.Add(-24*time.Hour)), stock.ID)

	lapsed := s.entry(entry.ID)
	assert.Equal(t, entity.ResaleEntryStateLapsed, lapsed.State)
	assert.Equal(t, stripe.PaymentIntentStatusCanceled, s.intentStatus(lapsed))
	assert.Len(t, s.emailsTo(fan), 1, "the fan is told by email")
	assert.True(t, s.pushed(entity.NotificationTypeResaleEntryLapsed, entry.ID), "and push")
	_, err := s.entries.AuthorizeCard(s.ctx, stock.ID, fan, 1, storyFanName, storyFanPhone, nil)
	assert.NoError(t, err, "the fan can apply again")
}

func TestResaleDrawStory_PlansChanged(t *testing.T) {
	// @spec stories/resell-a-ticket "Plans changed"
	s := newResaleStory(t)
	stock, seller := s.soldOutTo(2)
	s.listOne(seller)
	fan := s.newFan()
	entry := s.apply(stock.ID, fan, 2, "pm_card_visa")

	_, err := s.entries.Withdraw(s.ctx, fan, entry.ID)
	require.NoError(t, err)
	s.followUp()
	s.draw(noonAfter(s.now), stock.ID)

	withdrawn := s.entry(entry.ID)
	assert.Equal(t, entity.ResaleEntryStateWithdrawn, withdrawn.State, "the draw allocates them nothing")
	assert.Equal(t, stripe.PaymentIntentStatusCanceled, s.intentStatus(withdrawn), "not charged; the hold is released")
	assert.Empty(t, s.emailsTo(fan), "no email for a withdrawal")
}

func TestResaleDrawStory_CancelledBeforeTheDraw(t *testing.T) {
	// @spec stories/resell-a-ticket "Cancelled before the draw"
	s := newResaleStory(t)
	stock, seller := s.soldOutTo(2)
	s.listOne(seller)
	fan := s.newFan()
	entry := s.apply(stock.ID, fan, 1, "pm_card_visa")
	_, err := testDB.Pool.Exec(context.Background(), `UPDATE events SET publish_state = 'CANCELLED' WHERE id = $1`, s.eventID)
	require.NoError(t, err)

	s.draw(noonAfter(s.now), stock.ID)

	lost := s.entry(entry.ID)
	assert.Equal(t, entity.ResaleEntryStateLost, lost.State)
	assert.Equal(t, entity.ResaleEntryLostReasonConcertCancelled, lost.LostReason)
	assert.Equal(t, stripe.PaymentIntentStatusCanceled, s.intentStatus(lost), "nothing is charged and the hold is released")
	assert.Len(t, s.emailsTo(fan), 1, "the fan is told")
	assert.True(t, s.pushed(entity.NotificationTypeResaleEntryLost, entry.ID))
}
