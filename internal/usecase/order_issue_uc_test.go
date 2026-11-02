package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- self-contained test doubles for ⑤ issuance ports ---
//
// Function-field stubs shared by the issuance, settlement, refund and webhook
// tests of this package.

type stubIssuanceRepo struct {
	issueFn func(ctx context.Context, order *entity.Order, tickets []*entity.Ticket, settlement *entity.Settlement) error
}

func (s *stubIssuanceRepo) Issue(ctx context.Context, order *entity.Order, tickets []*entity.Ticket, settlement *entity.Settlement) error {
	if s.issueFn != nil {
		return s.issueFn(ctx, order, tickets, settlement)
	}
	return nil
}

// stubEventOrganizerRepo is a self-contained test double for
// usecase.EventOrganizerRepository (same rationale as the other stubs above).
type stubEventOrganizerRepo struct {
	getOrganizerIDFn func(ctx context.Context, eventID string) (string, error)
}

func (s *stubEventOrganizerRepo) GetOrganizerID(ctx context.Context, eventID string) (string, error) {
	if s.getOrganizerIDFn != nil {
		return s.getOrganizerIDFn(ctx, eventID)
	}
	return "org-1", nil
}

type stubOrderRepo struct {
	entity.OrderRepository
	getFn                    func(ctx context.Context, id entity.OrderID) (*entity.Order, error)
	getByLotteryEntryIDFn    func(ctx context.Context, lotteryEntryID entity.LotteryEntryID) (*entity.Order, error)
	getByProviderPaymentIDFn func(ctx context.Context, providerPaymentID string) (*entity.Order, error)
	updateStatusFn           func(ctx context.Context, id entity.OrderID, status entity.OrderStatus) error
	getEventIDFn             func(ctx context.Context, id entity.OrderID) (string, error)
}

func (s *stubOrderRepo) GetEventID(ctx context.Context, id entity.OrderID) (string, error) {
	if s.getEventIDFn != nil {
		return s.getEventIDFn(ctx, id)
	}
	return "event-1", nil
}

func (s *stubOrderRepo) Get(ctx context.Context, id entity.OrderID) (*entity.Order, error) {
	if s.getFn != nil {
		return s.getFn(ctx, id)
	}
	return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
}

func (s *stubOrderRepo) GetByLotteryEntryID(ctx context.Context, lotteryEntryID entity.LotteryEntryID) (*entity.Order, error) {
	if s.getByLotteryEntryIDFn != nil {
		return s.getByLotteryEntryIDFn(ctx, lotteryEntryID)
	}
	return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
}

func (s *stubOrderRepo) GetByProviderPaymentID(ctx context.Context, providerPaymentID string) (*entity.Order, error) {
	if s.getByProviderPaymentIDFn != nil {
		return s.getByProviderPaymentIDFn(ctx, providerPaymentID)
	}
	return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
}

func (s *stubOrderRepo) UpdateStatus(ctx context.Context, id entity.OrderID, status entity.OrderStatus) error {
	if s.updateStatusFn != nil {
		return s.updateStatusFn(ctx, id, status)
	}
	return nil
}

func (s *stubOrderRepo) ListByUser(_ context.Context, _ entity.UserID) ([]*entity.Order, error) {
	return nil, nil
}

func (s *stubOrderRepo) GetByCheckoutID(_ context.Context, _ entity.CheckoutID) (*entity.Order, error) {
	return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
}

func (s *stubOrderRepo) MarkConfirmationSent(_ context.Context, _ entity.OrderID, _ time.Time) error {
	return nil
}

// stubCardAuthorizations is a CardAuthorizationRepository whose Get reports
// getFn's card authorization, by default a Captured 10000 yen Visa payment,
// and whose Capture reports captureFn's outcome, by default a success. It
// counts the captures.
type stubCardAuthorizations struct {
	entity.CardAuthorizationRepository
	getFn     func(ctx context.Context, id string) (*entity.CardAuthorization, error)
	captureFn func(ctx context.Context, id string) error
	captures  int
}

func (s *stubCardAuthorizations) Capture(ctx context.Context, id string) error {
	s.captures++
	if s.captureFn != nil {
		return s.captureFn(ctx, id)
	}
	return nil
}

func (s *stubCardAuthorizations) Get(ctx context.Context, id string) (*entity.CardAuthorization, error) {
	if s.getFn != nil {
		return s.getFn(ctx, id)
	}
	return capturedCardAuthorization(id, &entity.CapturedPayment{
		Provider:  entity.PaymentProviderStripe,
		AmountJPY: 10000,
		Currency:  "JPY",
		CardBrand: "visa",
		CardLast4: "4242",
	}), nil
}

// capturedCardAuthorization is the Captured card authorization id that took
// the payment.
func capturedCardAuthorization(id string, payment *entity.CapturedPayment) *entity.CardAuthorization {
	return &entity.CardAuthorization{ID: id, Amount: payment.AmountJPY, Status: entity.CardAuthorizationStatusCaptured, CapturedPayment: payment}
}

// lotteryIssuanceDeps are the lottery-side dependencies of an issuance test.
// A nil field gets a default: the entry repository returns [wonEntry], the
// sale repository a Lottery sale without a verification requirement, the
// order repository the entry's Pending Order, the stock repository stock-1
// of event-1, the card authorization a captured 10000 yen Visa payment and
// the Organizer resolves to "org-1".
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
}

// newLotteryIssuanceUC builds an OrderUseCase for the lottery path, whose
// Organizer is at the 5% pilot rate.
func newLotteryIssuanceUC(t *testing.T, d lotteryIssuanceDeps) usecase.OrderUseCase {
	t.Helper()
	organizers := entitymocks.NewMockOrganizerRepository(t)
	organizers.EXPECT().Get(mock.Anything, mock.Anything).
		Return(&entity.Organizer{ID: "organizer-1", PlatformFeeRateBps: 500}, nil).Maybe()
	if d.issuanceRepo == nil {
		d.issuanceRepo = &stubIssuanceRepo{}
	}
	if d.orderRepo == nil {
		d.orderRepo = &stubOrderRepo{getFn: func(context.Context, entity.OrderID) (*entity.Order, error) { return wonEntry().Order, nil }}
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
		d.eventOrganizerRepo = &stubEventOrganizerRepo{}
	}
	if d.cardAuthorizations == nil {
		d.cardAuthorizations = &stubCardAuthorizations{}
	}
	return usecase.NewOrderUseCase(usecase.OrderDeps{
		IssuanceRepo:             d.issuanceRepo,
		OrderRepo:                d.orderRepo,
		EntryRepo:                d.entryRepo,
		EventOrganizerRepo:       d.eventOrganizerRepo,
		OrganizerRepo:            organizers,
		IdentityVerificationRepo: d.identityVerificationRepo,
		CardAuthorizations:       d.cardAuthorizations,
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

// storedOrPending returns an OrderRepository whose Get returns the Order Issue
// stored, and entry-1's Pending Order before that.
func storedOrPending(stored **entity.Order) *stubOrderRepo {
	return &stubOrderRepo{getFn: func(_ context.Context, id entity.OrderID) (*entity.Order, error) {
		if *stored != nil && (*stored).ID == id {
			return *stored, nil
		}
		if pending := wonEntry().Order; pending.ID == id {
			return pending, nil
		}
		return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
	}}
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

// recordingIssuanceRepo returns an IssuanceRepository stub that records what
// Issue stores, and a pointer to whether Issue was called.
func recordingIssuanceRepo(order **entity.Order, tickets *[]*entity.Ticket, settlement **entity.Settlement) (*stubIssuanceRepo, *bool) {
	called := false
	return &stubIssuanceRepo{issueFn: func(_ context.Context, o *entity.Order, ts []*entity.Ticket, s *entity.Settlement) error {
		called = true
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
	}}, &called
}

// TestOrderUseCase_ThroughSettlementRelease follows an Order end to end:
// issuance creates the Held Settlement (backend#468), and the payout sweeper
// — reusing the settlement_uc_test.go stubs, since both live in package
// usecase_test — later releases it once the event has started and the
// dispute buffer has passed. This is the regression test for the bug: before
// this change, SettlementUseCase.ListHeld was always empty because nothing
// ever created a Settlement.
func TestOrderUseCase_ThroughSettlementRelease(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

	// In-memory stand-in for the atomic DB transaction that
	// IssuanceRepository.Issue performs: what Issue "wrote" is what the
	// settlement/order repos below "read back".
	var storedOrder *entity.Order
	var storedSettlement *entity.Settlement

	issuanceRepo := &stubIssuanceRepo{issueFn: func(_ context.Context, o *entity.Order, _ []*entity.Ticket, s *entity.Settlement) error {
		storedOrder, storedSettlement = o, s
		return nil
	}}
	orderRepo := storedOrPending(&storedOrder)
	organizerRepo := &stubEventOrganizerRepo{getOrganizerIDFn: func(_ context.Context, _ string) (string, error) {
		return "organizer-1", nil
	}}

	issuanceUC := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, orderRepo: orderRepo, eventOrganizerRepo: organizerRepo, clock: fixedClock(now)})

	_, err := issueEntry(issuanceUC)
	require.NoError(t, err)
	order := storedOrder
	require.NotNil(t, storedSettlement, "issuance must create a Held settlement so the payout sweeper has something to release")
	assert.Equal(t, entity.SettlementStatusHeld, storedSettlement.Status)
	assert.Equal(t, order.ID, storedSettlement.OrderID)

	// -- 8 days later, the event has started and the 7-day dispute buffer has
	//    passed, and the Organizer's payout account is Active: the sweeper
	//    releases the settlement issuance created above. --
	var released *entity.Settlement
	var releasedSplits []entity.SettlementSplit
	settlementRepo := &stubSettlementRepo{
		listHeldFn: func(_ context.Context) ([]*entity.Settlement, error) {
			return []*entity.Settlement{storedSettlement}, nil
		},
		markReleasedFn: func(_ context.Context, id entity.SettlementID, _ string, _ time.Time, splits []entity.SettlementSplit) error {
			require.Equal(t, storedSettlement.ID, id)
			released = storedSettlement
			releasedSplits = splits
			return nil
		},
	}
	eventStarted := now.Add(-8 * 24 * time.Hour)
	eventStartTimeRepo := &stubEventStartTimeRepo{getFn: func(_ context.Context, _ string) (*time.Time, error) {
		return &eventStarted, nil
	}}
	payoutAccountRepo := &stubPayoutAccountRepo{getByOrganizerIDFn: func(_ context.Context, organizerID string) (*entity.PayoutAccount, error) {
		return &entity.PayoutAccount{OrganizerID: organizerID, Status: entity.PayoutOnboardingStatusActive}, nil
	}}

	sweeperUC := usecase.NewSettlementUseCase(withSettlementPayouts(settlementRepo, &stubPaymentProvider{}), withOrderPayments(orderRepo, &stubPaymentProvider{}), withPayoutAccountProvider(payoutAccountRepo, &stubPaymentProvider{}), eventStartTimeRepo, 7*24*time.Hour, fixedClock(now), newTestLogger(t))

	require.NoError(t, sweeperUC.ReleaseDueSettlements(context.Background()))
	require.NotNil(t, released, "the settlement issuance created must be released once due")
	require.Len(t, releasedSplits, 1)
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

	issuanceRepo := &stubIssuanceRepo{issueFn: func(_ context.Context, o *entity.Order, ts []*entity.Ticket, s *entity.Settlement) error {
		storedOrder, storedTickets, storedSettlement = o, ts, s
		return nil
	}}
	orderRepo := storedOrPending(&storedOrder)
	organizerRepo := &stubEventOrganizerRepo{getOrganizerIDFn: func(_ context.Context, _ string) (string, error) {
		return "organizer-pending", nil
	}}

	issuanceUC := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, orderRepo: orderRepo, eventOrganizerRepo: organizerRepo, clock: fixedClock(now)})

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
	transferCalled := false
	markReleasedCalled := false
	settlementRepo := &stubSettlementRepo{
		listHeldFn: func(_ context.Context) ([]*entity.Settlement, error) {
			return []*entity.Settlement{storedSettlement}, nil
		},
		markReleasedFn: func(_ context.Context, _ entity.SettlementID, _ string, _ time.Time, _ []entity.SettlementSplit) error {
			markReleasedCalled = true
			return nil
		},
	}
	eventStarted := now.Add(-8 * 24 * time.Hour)
	eventStartTimeRepo := &stubEventStartTimeRepo{getFn: func(_ context.Context, _ string) (*time.Time, error) {
		return &eventStarted, nil
	}}
	payoutAccountRepo := &stubPayoutAccountRepo{getByOrganizerIDFn: func(_ context.Context, organizerID string) (*entity.PayoutAccount, error) {
		return &entity.PayoutAccount{OrganizerID: organizerID, Status: entity.PayoutOnboardingStatusPending}, nil
	}}
	payments := &stubPaymentProvider{
		getAccountStatusFn: func(_ context.Context, _ string) (entity.PayoutOnboardingStatus, error) {
			return entity.PayoutOnboardingStatusPending, nil
		},
		createTransferFn: func(_ context.Context, _ entity.PayoutParams) (string, error) {
			transferCalled = true
			return "tr_should_not_happen", nil
		},
	}

	sweeperUC := usecase.NewSettlementUseCase(withSettlementPayouts(settlementRepo, payments), withOrderPayments(orderRepo, payments), withPayoutAccountProvider(payoutAccountRepo, payments), eventStartTimeRepo, 7*24*time.Hour, fixedClock(now), newTestLogger(t))

	require.NoError(t, sweeperUC.ReleaseDueSettlements(context.Background()))
	assert.False(t, transferCalled, "the Organizer's payout must stay Held while the account is Pending")
	assert.False(t, markReleasedCalled, "the settlement must not be marked Released while the account is Pending")
	assert.Equal(t, entity.SettlementStatusHeld, storedSettlement.Status, "the settlement itself is untouched")
}
