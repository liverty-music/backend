package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// eventOrganizer returns an EventOrganizerRepository that resolves every
// event to organizerID.
func eventOrganizer(t *testing.T, organizerID string) *ucmocks.MockEventOrganizerRepository {
	t.Helper()
	owners := ucmocks.NewMockEventOrganizerRepository(t)
	owners.EXPECT().GetOrganizerID(mock.Anything, mock.Anything).Return(organizerID, nil).Maybe()
	return owners
}

// capturedCards returns a CardAuthorizationRepository whose every card
// authorization reads back Captured with payment. No capture is expected;
// a test that charges the card adds the Capture expectation.
func capturedCards(t *testing.T, payment *entity.CapturedPayment) *entitymocks.MockCardAuthorizationRepository {
	t.Helper()
	cards := entitymocks.NewMockCardAuthorizationRepository(t)
	cards.EXPECT().Get(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, id string) (*entity.CardAuthorization, error) {
			return capturedCardAuthorization(id, payment), nil
		}).Maybe()
	return cards
}

// visa10000 is a captured 10000 yen Visa payment.
func visa10000() *entity.CapturedPayment {
	return &entity.CapturedPayment{Provider: entity.PaymentProviderStripe, AmountJPY: 10000, Currency: "JPY", CardBrand: "visa", CardLast4: "4242"}
}

// capturedCardAuthorization is the Captured card authorization id that took
// the payment.
func capturedCardAuthorization(id string, payment *entity.CapturedPayment) *entity.CardAuthorization {
	if payment.ProviderPaymentID == "" {
		// The fixtures name the card authorization as the provider names
		// the payment it captured.
		copied := *payment
		copied.ProviderPaymentID = id
		payment = &copied
	}
	return &entity.CardAuthorization{ID: id, Amount: payment.AmountJPY, Status: entity.CardAuthorizationStatusCaptured, CapturedPayment: payment}
}

// lotteryIssuanceDeps are the lottery-side dependencies of an issuance test.
// A nil field gets a default: the entry repository returns [wonEntry], the
// sale repository a Lottery sale without a verification requirement, the
// order repository the entry's Pending Order, the stock repository stock-1
// of event-1, the issuance repository stores nothing and expects no call,
// the card authorization reads back a captured 10000 yen Visa payment and
// expects no capture, and the Organizer resolves to "org-1".
type lotteryIssuanceDeps struct {
	stockRepo                entity.TicketStockRepository
	issuanceRepo             entity.IssuanceRepository
	orderRepo                entity.OrderRepository
	entryRepo                entity.LotteryEntryRepository
	saleRepo                 entity.TicketSaleRepository
	identityVerificationRepo entity.IdentityVerificationRepository
	eventOrganizerRepo       usecase.EventOrganizerRepository
	cardAuthorizations       entity.CardAuthorizationRepository
	clock                    usecase.Clock
	// published reports, at each check, whether event-1 is PUBLISHED; nil
	// means always.
	published func() bool
}

// newLotteryIssuanceUC builds an OrderUseCase for the lottery path, whose
// Organizer is at the 5% pilot rate.
func newLotteryIssuanceUC(t *testing.T, d lotteryIssuanceDeps) usecase.OrderUseCase {
	t.Helper()
	organizers := entitymocks.NewMockOrganizerRepository(t)
	organizers.EXPECT().Get(mock.Anything, mock.Anything).
		Return(&entity.Organizer{ID: "organizer-1", PlatformFeeRateBps: 500}, nil).Maybe()
	if d.issuanceRepo == nil {
		d.issuanceRepo = entitymocks.NewMockIssuanceRepository(t)
	}
	if d.orderRepo == nil {
		orders := entitymocks.NewMockOrderRepository(t)
		orders.EXPECT().Get(mock.Anything, wonEntry().OrderID).
			RunAndReturn(func(context.Context, entity.OrderID) (*entity.Order, error) { return wonEntry().Order, nil }).Maybe()
		d.orderRepo = orders
	}
	if d.stockRepo == nil {
		stocks := entitymocks.NewMockTicketStockRepository(t)
		stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-1"), mock.Anything).
			Return(&entity.TicketStock{ID: "stock-1", EventID: "event-1", Quantity: 100}, nil).Maybe()
		d.stockRepo = stocks
	}
	if d.entryRepo == nil {
		entries := entitymocks.NewMockLotteryEntryRepository(t)
		entries.EXPECT().Get(mock.Anything, mock.Anything).Return(wonEntry(), nil).Maybe()
		d.entryRepo = entries
	}
	if d.saleRepo == nil {
		d.saleRepo = lotterySaleRepo(t, entity.VerificationRequirementNone)
	}
	if d.identityVerificationRepo == nil {
		d.identityVerificationRepo = entitymocks.NewMockIdentityVerificationRepository(t)
	}
	if d.eventOrganizerRepo == nil {
		d.eventOrganizerRepo = eventOrganizer(t, "org-1")
	}
	if d.cardAuthorizations == nil {
		d.cardAuthorizations = capturedCards(t, visa10000())
	}
	if d.published == nil {
		d.published = func() bool { return true }
	}
	eventState := ucmocks.NewMockEventPublishStatePort(t)
	eventState.EXPECT().IsEventPublished(mock.Anything, "event-1").
		RunAndReturn(func(context.Context, string) (bool, error) { return d.published(), nil }).Maybe()
	return usecase.NewOrderUseCase(usecase.OrderDeps{
		IssuanceRepo:             d.issuanceRepo,
		OrderRepo:                d.orderRepo,
		EntryRepo:                d.entryRepo,
		EventOrganizerRepo:       d.eventOrganizerRepo,
		OrganizerRepo:            organizers,
		IdentityVerificationRepo: d.identityVerificationRepo,
		CardAuthorizations:       d.cardAuthorizations,
		EventState:               eventState,
		TicketSaleRepo:           d.saleRepo,
		StockRepo:                d.stockRepo,
		Clock:                    d.clock,
		Logger:                   newTestLogger(t),
	})
}

// issueEntry runs the issue workflow of entry-1 in one go and returns its
// result.
func issueEntry(uc usecase.OrderUseCase) (usecase.IssueResult, error) {
	return uc.Issue(context.Background(), &inlineFlow{}, usecase.IssueRequest{LotteryEntryID: "entry-1"})
}

// lotterySaleRepo returns a sale repository whose drawn Lottery sale
// "sale-lottery" of event-1 has the given verification requirement.
func lotterySaleRepo(t *testing.T, req entity.VerificationRequirement) *entitymocks.MockTicketSaleRepository {
	t.Helper()
	sale := &entity.TicketSale{
		ID: "sale-lottery", SeriesID: "series-1", Name: "抽選", Method: entity.TicketSaleMethodLottery,
		SaleStartTime:           time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC),
		SaleEndTime:             time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC),
		VerificationRequirement: req,
		EventIDs:                []string{"event-1"},
	}
	sales := entitymocks.NewMockTicketSaleRepository(t)
	sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("sale-lottery")).Return(sale, nil).Maybe()
	return sales
}

// wonEntry returns user-1's Won entry "entry-1" of sale-lottery with its
// Pending Order for 2 tickets of stock-1, paid by pi_won_1.
func wonEntry() *entity.LotteryEntry {
	return &entity.LotteryEntry{
		ID: "entry-1", OrderID: "order-entry-1", TicketSaleID: "sale-lottery", State: entity.LotteryEntryStateWon,
		Order: &entity.Order{
			ID: "order-entry-1", UserID: "user-1", TicketStockID: "stock-1", TicketCount: 2, Amount: 16000,
			Currency: "JPY", CardAuthorizationID: "pi_won_1", Status: entity.OrderStatusPending,
			Payment: entity.Payment{Provider: entity.PaymentProviderStripe}, LotteryEntryID: "entry-1",
		},
	}
}

// issuanceRecorder returns an IssuanceRepository that expects one Issue and
// records what it stores into the non-nil pointers.
func issuanceRecorder(t *testing.T, order **entity.Order, tickets *[]*entity.Ticket, settlement **entity.Settlement) *entitymocks.MockIssuanceRepository {
	t.Helper()
	issuances := entitymocks.NewMockIssuanceRepository(t)
	issuances.EXPECT().Issue(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, o *entity.Order, ts []*entity.Ticket, s *entity.Settlement) error {
			if order != nil {
				*order = o
			}
			if tickets != nil {
				*tickets = ts
			}
			if settlement != nil {
				*settlement = s
			}
			return nil
		}).Once()
	return issuances
}

// assertNothingIssued asserts that Order.Issue was not called.
func assertNothingIssued(t *testing.T, issuances *entitymocks.MockIssuanceRepository) {
	t.Helper()
	issuances.AssertNotCalled(t, "Issue", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// assertNotCharged asserts that the card was not charged.
func assertNotCharged(t *testing.T, cards *entitymocks.MockCardAuthorizationRepository) {
	t.Helper()
	cards.AssertNotCalled(t, "Capture", mock.Anything, mock.Anything)
}

// TestOrderUseCase_ThroughSettlementRelease follows an Order end to end:
// issuance creates the Held Settlement (backend#468), and the payout sweeper
// later releases it once the event has started and the dispute buffer has
// passed. This is the regression test for the bug: before this change,
// SettlementUseCase.ListHeld was always empty because nothing ever created a
// Settlement.
func TestOrderUseCase_ThroughSettlementRelease(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

	// What Issue stores in its atomic DB transaction is what the sweeper's
	// settlement and order reads below return.
	var storedOrder *entity.Order
	var storedSettlement *entity.Settlement
	issuanceRepo := issuanceRecorder(t, &storedOrder, nil, &storedSettlement)
	cards := capturedCards(t, visa10000())
	cards.EXPECT().Capture(mock.Anything, "pi_won_1").Return(nil).Once()

	issuanceUC := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, eventOrganizerRepo: eventOrganizer(t, "organizer-1"),
		cardAuthorizations: cards, clock: fixedClock(now)})

	_, err := issueEntry(issuanceUC)
	require.NoError(t, err)
	order := storedOrder
	require.NotNil(t, storedSettlement, "issuance must create a Held settlement so the payout sweeper has something to release")
	assert.Equal(t, entity.SettlementStatusHeld, storedSettlement.Status)
	assert.Equal(t, order.ID, storedSettlement.OrderID)

	// -- 8 days later, the event has started and the 7-day dispute buffer has
	//    passed, and the Organizer's payout account is Active: the sweeper
	//    releases the settlement issuance created above. --
	sweeper := newSettlementFixture(t, 7*24*time.Hour, fixedClock(now))
	sweeper.held(storedSettlement, storedOrder)
	sweeper.startsAt(storedSettlement.EventID, new(now.Add(-8*24*time.Hour)))
	sweeper.account(&entity.PayoutAccount{OrganizerID: "organizer-1", Status: entity.PayoutOnboardingStatusActive}, entity.PayoutOnboardingStatusActive)
	sweeper.paysOut(storedOrder.Payment.ProviderPaymentID, "ch_test", "tr_test")
	var releasedSplits []entity.SettlementSplit
	sweeper.settlements.EXPECT().MarkReleased(mock.Anything, storedSettlement.ID, "ch_test", now, mock.Anything).
		RunAndReturn(func(_ context.Context, _ entity.SettlementID, _ string, _ time.Time, splits []entity.SettlementSplit) error {
			releasedSplits = splits
			return nil
		}).Once()

	require.NoError(t, sweeper.uc.ReleaseDueSettlements(context.Background()))
	require.Len(t, releasedSplits, 1, "the settlement issuance created must be released once due")
	assert.Equal(t, "organizer-1", releasedSplits[0].PayeeOrganizerID)
	assert.Equal(t, order.Amount-entity.PlatformFee(order.Amount, 500), releasedSplits[0].Amount)
}

// TestOrderUseCase_OrganizerPayoutReadinessNeverBlocksSale follows the
// win-tickets-in-a-lottery story's payout-readiness requirement: an Organizer
// still completing identity checks never blocks the sale — the fan is still
// charged and issued Tickets — only the Organizer's own payout stays Held
// until the account is Active.
//
// @spec stories/win-tickets-in-a-lottery "Organizer still in identity check"
func TestOrderUseCase_OrganizerPayoutReadinessNeverBlocksSale(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

	var storedOrder *entity.Order
	var storedTickets []*entity.Ticket
	var storedSettlement *entity.Settlement
	issuanceRepo := issuanceRecorder(t, &storedOrder, &storedTickets, &storedSettlement)
	cards := capturedCards(t, visa10000())
	cards.EXPECT().Capture(mock.Anything, "pi_won_1").Return(nil).Once()

	issuanceUC := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, eventOrganizerRepo: eventOrganizer(t, "organizer-pending"),
		cardAuthorizations: cards, clock: fixedClock(now)})

	// -- the fan is charged and holds Issued Tickets regardless of the
	//    Organizer's payout readiness. --
	_, err := issueEntry(issuanceUC)
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusPaid, storedOrder.Status)
	require.Len(t, storedTickets, 2)
	for _, tk := range storedTickets {
		assert.Equal(t, entity.TicketStatusIssued, tk.Status)
	}
	require.NotNil(t, storedSettlement)
	assert.Equal(t, entity.SettlementStatusHeld, storedSettlement.Status)

	// -- the event started 8 days ago (past the dispute buffer), but the
	//    Organizer's payout account is still Pending (identity check in
	//    progress): the sweeper withholds the payout instead of failing. --
	sweeper := newSettlementFixture(t, 7*24*time.Hour, fixedClock(now))
	sweeper.held(storedSettlement, storedOrder)
	sweeper.startsAt(storedSettlement.EventID, new(now.Add(-8*24*time.Hour)))
	sweeper.account(&entity.PayoutAccount{OrganizerID: "organizer-pending", Status: entity.PayoutOnboardingStatusPending}, entity.PayoutOnboardingStatusPending)

	require.NoError(t, sweeper.uc.ReleaseDueSettlements(context.Background()))
	// The Organizer's payout must stay Held while the account is Pending: no
	// transfer, and the settlement is not marked Released.
	sweeper.assertNotPaidOut(t)
	assert.Equal(t, entity.SettlementStatusHeld, storedSettlement.Status, "the settlement itself is untouched")
}
