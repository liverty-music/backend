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
	issueFn        func(ctx context.Context, order *entity.Order, tickets []*entity.Ticket, settlement *entity.Settlement) error
	listAwaitingFn func(ctx context.Context) ([]entity.LotteryEntryID, error)
}

func (s *stubIssuanceRepo) Issue(ctx context.Context, order *entity.Order, tickets []*entity.Ticket, settlement *entity.Settlement) error {
	if s.issueFn != nil {
		return s.issueFn(ctx, order, tickets, settlement)
	}
	return nil
}

func (s *stubIssuanceRepo) ListLotteryEntryIDsAwaitingIssuance(ctx context.Context) ([]entity.LotteryEntryID, error) {
	if s.listAwaitingFn != nil {
		return s.listAwaitingFn(ctx)
	}
	return nil, nil
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
// getFn's card authorization, by default a Captured 10000 yen Visa payment.
// The lottery issuance path only reads.
type stubCardAuthorizations struct {
	entity.CardAuthorizationRepository
	getFn func(ctx context.Context, id string) (*entity.CardAuthorization, error)
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
// order repository nothing issued yet, the card authorization a captured 10000 yen
// Visa payment and the Organizer resolves to "org-1".
type lotteryIssuanceDeps struct {
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
		d.orderRepo = &stubOrderRepo{}
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
		Clock:                    d.clock,
		Logger:                   newTestLogger(t),
	})
}

// lotterySaleRepo returns a sale repository whose ticket type "tt-lottery"
// belongs to a drawn Lottery sale of event-1 with the given verification
// requirement.
func lotterySaleRepo(t *testing.T, req entity.VerificationRequirement) *entitymocks.MockTicketSaleRepository {
	t.Helper()
	drawTime := time.Date(2026, 9, 12, 12, 1, 0, 0, time.UTC)
	sale := &entity.TicketSale{
		ID: "sale-lottery", SeriesID: "series-1", Name: "抽選", Method: entity.TicketSaleMethodLottery,
		SaleStartTime:           time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC),
		SaleEndTime:             time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC),
		VerificationRequirement: req,
		DrawTime:                &drawTime,
	}
	tt := &entity.TicketType{ID: "tt-lottery", TicketSaleID: sale.ID, EventID: "event-1", Price: 5000, Quantity: 100, PerAccountLimit: 4}
	sale.TicketTypes = []*entity.TicketType{tt}
	sales := entitymocks.NewMockTicketSaleRepository(t)
	sales.EXPECT().GetTicketType(mock.Anything, entity.TicketTypeID("tt-lottery"), mock.Anything).Return(sale, tt, nil).Maybe()
	return sales
}

// wonEntry returns user-1's Won entry "entry-1" for 2 tickets of tt-lottery.
func wonEntry() *entity.LotteryEntry {
	return &entity.LotteryEntry{
		ID:                  "entry-1",
		TicketTypeID:        "tt-lottery",
		UserID:              "user-1",
		TicketCount:         2,
		CardAuthorizationID: "pi_won_1",
		State:               entity.LotteryEntryStateWon,
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

func TestOrderUseCase_IssueFromCapturedWin(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

	// @spec components/usecase/order/issue-from-captured-win "Won application issued"
	t.Run("creates a paid Order, issues N covered tickets and a Held Settlement at the Organizer's rate", func(t *testing.T) {
		t.Parallel()

		var issuedOrder *entity.Order
		var issuedTickets []*entity.Ticket
		var issuedSettlement *entity.Settlement
		issuanceRepo, _ := recordingIssuanceRepo(&issuedOrder, &issuedTickets, &issuedSettlement)
		organizerRepo := &stubEventOrganizerRepo{getOrganizerIDFn: func(_ context.Context, eventID string) (string, error) {
			assert.Equal(t, "event-1", eventID)
			return "organizer-1", nil
		}}
		cardAuthorizations := &stubCardAuthorizations{getFn: func(_ context.Context, id string) (*entity.CardAuthorization, error) {
			assert.Equal(t, "pi_won_1", id)
			return capturedCardAuthorization(id, &entity.CapturedPayment{Provider: entity.PaymentProviderStripe, AmountJPY: 16000, Currency: "JPY", CardBrand: "visa", CardLast4: "4242"}), nil
		}}

		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, eventOrganizerRepo: organizerRepo, cardAuthorizations: cardAuthorizations, clock: fixedClock(now)})

		order, err := uc.IssueFromCapturedWin(context.Background(), "entry-1")
		require.NoError(t, err)

		// Order is paid, references the captured payment, carries facets, never card data.
		require.NotNil(t, order)
		assert.Same(t, issuedOrder, order)
		assert.Equal(t, entity.OrderStatusPaid, order.Status)
		assert.Equal(t, entity.UserID("user-1"), order.UserID)
		assert.Equal(t, entity.LotteryEntryID("entry-1"), order.LotteryEntryID)
		assert.Empty(t, order.CheckoutID)
		assert.Equal(t, "pi_won_1", order.Payment.ProviderPaymentID)
		assert.Equal(t, entity.PaymentProviderStripe, order.Payment.Provider)
		assert.Equal(t, "visa", order.Payment.CardBrand)
		assert.Equal(t, "4242", order.Payment.CardLast4)
		assert.Equal(t, int64(16000), order.Amount)
		assert.Equal(t, "JPY", order.Currency)
		assert.Equal(t, now, order.PayTime)

		// Exactly N covered tickets, all bound to the entry's user for the
		// ticket type's event; no IdentityVerification, since the sale required none.
		require.Len(t, issuedTickets, 2)
		for _, tk := range issuedTickets {
			assert.Equal(t, order.ID, tk.OrderID)
			assert.Equal(t, entity.UserID("user-1"), tk.UserID)
			assert.Equal(t, "event-1", tk.EventID)
			assert.True(t, tk.ResaleWithoutConsentProhibited)
			assert.Equal(t, entity.TicketStatusIssued, tk.Status)
			assert.Equal(t, now, tk.IssueTime)
			assert.Empty(t, tk.IdentityVerificationID)
		}

		// A Held settlement for the event's Organizer, with one split for the
		// Order's amount less the 5% platform fee: 16000 -> 15200 yen.
		require.NotNil(t, issuedSettlement)
		assert.Equal(t, order.ID, issuedSettlement.OrderID)
		assert.Equal(t, "organizer-1", issuedSettlement.OrganizerID)
		assert.Equal(t, "event-1", issuedSettlement.EventID)
		assert.Equal(t, entity.SettlementStatusHeld, issuedSettlement.Status)
		assert.Equal(t, now, issuedSettlement.CreateTime)
		require.Len(t, issuedSettlement.Splits, 1)
		assert.Equal(t, "organizer-1", issuedSettlement.Splits[0].PayeeOrganizerID)
		assert.Equal(t, int64(15200), issuedSettlement.Splits[0].Amount)
	})

	// @spec components/usecase/order/issue-from-captured-win "Replayed issuance"
	t.Run("is idempotent: an existing Order is returned without re-issuing", func(t *testing.T) {
		t.Parallel()

		existing := &entity.Order{ID: "order-existing", LotteryEntryID: "entry-1", Status: entity.OrderStatusPaid}
		orderRepo := &stubOrderRepo{getByLotteryEntryIDFn: func(_ context.Context, _ entity.LotteryEntryID) (*entity.Order, error) {
			return existing, nil
		}}
		issuanceRepo, issueCalled := recordingIssuanceRepo(nil, nil, nil)

		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, orderRepo: orderRepo, clock: fixedClock(now)})

		order, err := uc.IssueFromCapturedWin(context.Background(), "entry-1")
		require.NoError(t, err)
		assert.Same(t, existing, order)
		assert.False(t, *issueCalled, "must not re-issue when an Order already exists")
	})

	// @spec components/usecase/order/issue-from-captured-win "Concurrent issuance"
	t.Run("on a concurrent issuance race it re-reads and returns the winning Order", func(t *testing.T) {
		t.Parallel()

		raceOrder := &entity.Order{ID: "order-race", LotteryEntryID: "entry-1", Status: entity.OrderStatusPaid}
		// First lookup (idempotency check) → NotFound; second (after the
		// AlreadyExists race) → the order the concurrent writer created.
		calls := 0
		orderRepo := &stubOrderRepo{getByLotteryEntryIDFn: func(_ context.Context, _ entity.LotteryEntryID) (*entity.Order, error) {
			calls++
			if calls == 1 {
				return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
			}
			return raceOrder, nil
		}}
		issuanceRepo := &stubIssuanceRepo{issueFn: func(_ context.Context, _ *entity.Order, _ []*entity.Ticket, _ *entity.Settlement) error {
			return apperr.New(apperr.ErrAlreadyExists.Code, "duplicate lottery entry")
		}}

		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, orderRepo: orderRepo, clock: fixedClock(now)})

		order, err := uc.IssueFromCapturedWin(context.Background(), "entry-1")
		require.NoError(t, err)
		assert.Same(t, raceOrder, order)
	})

	// @spec components/usecase/order/issue-from-captured-win "Application not won"
	t.Run("fails with FailedPrecondition and creates nothing for an entry that is not Won", func(t *testing.T) {
		t.Parallel()

		for _, state := range []entity.LotteryEntryState{
			entity.LotteryEntryStateDraft, entity.LotteryEntryStateEntered,
			entity.LotteryEntryStateLost, entity.LotteryEntryStateWithdrawn,
		} {
			t.Run(state.String(), func(t *testing.T) {
				t.Parallel()
				entry := wonEntry()
				entry.State = state
				entries := entitymocks.NewMockLotteryEntryRepository(t)
				entries.EXPECT().Get(mock.Anything, entity.LotteryEntryID("entry-1")).Return(entry, nil)
				issuanceRepo, issueCalled := recordingIssuanceRepo(nil, nil, nil)

				uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, entryRepo: entries, clock: fixedClock(now)})

				_, err := uc.IssueFromCapturedWin(context.Background(), "entry-1")
				assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
				assert.False(t, *issueCalled)
			})
		}
	})

	t.Run("fails with NotFound for an unknown entry", func(t *testing.T) {
		t.Parallel()

		entries := entitymocks.NewMockLotteryEntryRepository(t)
		entries.EXPECT().Get(mock.Anything, entity.LotteryEntryID("entry-x")).Return(nil, apperr.ErrNotFound)

		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{entryRepo: entries, clock: fixedClock(now)})

		_, err := uc.IssueFromCapturedWin(context.Background(), "entry-x")
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	// @spec components/usecase/order/issue-from-captured-win "Payment not captured"
	t.Run("fails with FailedPrecondition and creates nothing when the payment was not captured", func(t *testing.T) {
		t.Parallel()

		issuanceRepo, issueCalled := recordingIssuanceRepo(nil, nil, nil)
		cardAuthorizations := &stubCardAuthorizations{getFn: func(_ context.Context, id string) (*entity.CardAuthorization, error) {
			return &entity.CardAuthorization{ID: id, Amount: 16000, CardBrand: "visa", Status: entity.CardAuthorizationStatusAuthorized}, nil
		}}

		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, cardAuthorizations: cardAuthorizations, clock: fixedClock(now)})

		_, err := uc.IssueFromCapturedWin(context.Background(), "entry-1")
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.False(t, *issueCalled, "must not issue when the payment was not captured")
	})

	// @spec components/usecase/order/issue-from-captured-win "Organizer unresolved"
	t.Run("fails with NotFound and creates nothing when the event's Organizer cannot be resolved", func(t *testing.T) {
		t.Parallel()

		issuanceRepo, issueCalled := recordingIssuanceRepo(nil, nil, nil)
		organizerRepo := &stubEventOrganizerRepo{getOrganizerIDFn: func(_ context.Context, _ string) (string, error) {
			return "", apperr.New(apperr.ErrNotFound.Code, "event's series has no organizer")
		}}

		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, eventOrganizerRepo: organizerRepo, clock: fixedClock(now)})

		_, err := uc.IssueFromCapturedWin(context.Background(), "entry-1")
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.False(t, *issueCalled, "must not issue when the Organizer cannot be resolved")
	})

	// @spec components/usecase/order/issue-from-captured-win "Phase required verification"
	t.Run("binds every ticket to the user's identity verification when the sale requires verification", func(t *testing.T) {
		t.Parallel()

		var issuedTickets []*entity.Ticket
		issuanceRepo, _ := recordingIssuanceRepo(nil, &issuedTickets, nil)
		identityVerifications := entitymocks.NewMockIdentityVerificationRepository(t)
		// Whatever its status: the IdentityVerification read at issuance only binds.
		identityVerifications.EXPECT().GetByUserID(mock.Anything, "user-1").
			Return(&entity.IdentityVerification{ID: "identity verification-1", UserID: "user-1", Status: entity.VerificationStatusNeedsReverification}, nil)

		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{
			issuanceRepo: issuanceRepo, identityVerificationRepo: identityVerifications,
			saleRepo: lotterySaleRepo(t, entity.VerificationRequirementJPKIOnly), clock: fixedClock(now),
		})

		_, err := uc.IssueFromCapturedWin(context.Background(), "entry-1")
		require.NoError(t, err)
		require.Len(t, issuedTickets, 2)
		for _, tk := range issuedTickets {
			assert.Equal(t, "identity verification-1", tk.IdentityVerificationID)
			// The face shows the user's own full name: nothing is copied onto
			// the Ticket.
			face := tk.Face(&entity.User{ID: "user-1", FullName: "山田 太郎", PhoneNumber: "+819012345678"})
			assert.Equal(t, "山田 太郎", face.FullName)
		}
	})

	// @spec components/usecase/order/issue-from-captured-win "Identity verification missing"
	t.Run("fails with NotFound and creates nothing when the user has no identity verification", func(t *testing.T) {
		t.Parallel()

		issuanceRepo, issueCalled := recordingIssuanceRepo(nil, nil, nil)
		identityVerifications := entitymocks.NewMockIdentityVerificationRepository(t)
		identityVerifications.EXPECT().GetByUserID(mock.Anything, "user-1").Return(nil, apperr.ErrNotFound)

		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{
			issuanceRepo: issuanceRepo, identityVerificationRepo: identityVerifications,
			saleRepo: lotterySaleRepo(t, entity.VerificationRequirementVerifiedAny), clock: fixedClock(now),
		})

		_, err := uc.IssueFromCapturedWin(context.Background(), "entry-1")
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.False(t, *issueCalled)
	})

	// @spec components/usecase/order/issue-from-captured-win "No requirement"
	t.Run("reads no identity verification and binds none when the sale has no requirement", func(t *testing.T) {
		t.Parallel()

		var issuedTickets []*entity.Ticket
		issuanceRepo, _ := recordingIssuanceRepo(nil, &issuedTickets, nil)
		identityVerifications := entitymocks.NewMockIdentityVerificationRepository(t) // no expectation: GetByUserID is never called

		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, identityVerificationRepo: identityVerifications, clock: fixedClock(now)})

		_, err := uc.IssueFromCapturedWin(context.Background(), "entry-1")
		require.NoError(t, err)
		require.Len(t, issuedTickets, 2)
		for _, tk := range issuedTickets {
			assert.Empty(t, tk.IdentityVerificationID)
		}
	})
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
	orderRepo := &stubOrderRepo{getFn: func(_ context.Context, id entity.OrderID) (*entity.Order, error) {
		if storedOrder != nil && storedOrder.ID == id {
			return storedOrder, nil
		}
		return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
	}}
	organizerRepo := &stubEventOrganizerRepo{getOrganizerIDFn: func(_ context.Context, _ string) (string, error) {
		return "organizer-1", nil
	}}

	issuanceUC := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, orderRepo: orderRepo, eventOrganizerRepo: organizerRepo, clock: fixedClock(now)})

	order, err := issuanceUC.IssueFromCapturedWin(context.Background(), "entry-1")
	require.NoError(t, err)
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

func TestOrderUseCase_IssueDueWins(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

	// @spec components/usecase/order/issue-due-wins "Won application"
	// @spec components/usecase/order/issue-due-wins "Issuance fails"
	t.Run("issues every awaiting entry and continues past a single failure", func(t *testing.T) {
		t.Parallel()

		issued := map[entity.LotteryEntryID]bool{}
		issuanceRepo := &stubIssuanceRepo{
			listAwaitingFn: func(_ context.Context) ([]entity.LotteryEntryID, error) {
				return []entity.LotteryEntryID{"entry-1", "entry-bad", "entry-2"}, nil
			},
			issueFn: func(_ context.Context, o *entity.Order, _ []*entity.Ticket, _ *entity.Settlement) error {
				issued[o.LotteryEntryID] = true
				return nil
			},
		}
		entries := entitymocks.NewMockLotteryEntryRepository(t)
		entries.EXPECT().Get(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, id entity.LotteryEntryID) (*entity.LotteryEntry, error) {
			// entry-bad fails to load so its issuance errors; the sweep must continue.
			if id == "entry-bad" {
				return nil, apperr.New(apperr.ErrUnavailable.Code, "db down")
			}
			entry := wonEntry()
			entry.ID = id
			return entry, nil
		})

		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, entryRepo: entries, clock: fixedClock(now)})

		require.NoError(t, uc.IssueDueWins(context.Background()))
		assert.True(t, issued["entry-1"])
		assert.True(t, issued["entry-2"])
		assert.False(t, issued["entry-bad"], "a failed entry must not be issued")
	})

	t.Run("fails with the listing's error and issues nothing", func(t *testing.T) {
		t.Parallel()

		issuanceRepo := &stubIssuanceRepo{listAwaitingFn: func(_ context.Context) ([]entity.LotteryEntryID, error) {
			return nil, apperr.ErrInternal
		}}
		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, clock: fixedClock(now)})

		assert.ErrorIs(t, uc.IssueDueWins(context.Background()), apperr.ErrInternal)
	})
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
	orderRepo := &stubOrderRepo{getFn: func(_ context.Context, id entity.OrderID) (*entity.Order, error) {
		if storedOrder != nil && storedOrder.ID == id {
			return storedOrder, nil
		}
		return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
	}}
	organizerRepo := &stubEventOrganizerRepo{getOrganizerIDFn: func(_ context.Context, _ string) (string, error) {
		return "organizer-pending", nil
	}}

	issuanceUC := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, orderRepo: orderRepo, eventOrganizerRepo: organizerRepo, clock: fixedClock(now)})

	// -- the fan is charged and holds Issued Tickets regardless of the
	//    Organizer's payout readiness. --
	order, err := issuanceUC.IssueFromCapturedWin(context.Background(), "entry-1")
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusPaid, order.Status)
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
