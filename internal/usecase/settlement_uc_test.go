package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// Self-contained stubs (same rationale as issuance_uc_test.go: mockery v2.53.6
// cannot load internal/entity; function-field stubs verify business logic).
// ─────────────────────────────────────────────────────────────────────────────

type stubSettlementRepo struct {
	upsertFn       func(ctx context.Context, s *entity.Settlement) (*entity.Settlement, error)
	getFn          func(ctx context.Context, id entity.SettlementID) (*entity.Settlement, error)
	getByOrderIDFn func(ctx context.Context, orderID entity.OrderID) (*entity.Settlement, error)
	listHeldFn     func(ctx context.Context) ([]*entity.Settlement, error)
	markReleasedFn func(ctx context.Context, id entity.SettlementID, chargeRef string, releasedAt time.Time, splits []entity.SettlementSplit) error
}

func (s *stubSettlementRepo) Upsert(ctx context.Context, settlement *entity.Settlement) (*entity.Settlement, error) {
	if s.upsertFn != nil {
		return s.upsertFn(ctx, settlement)
	}
	return settlement, nil
}
func (s *stubSettlementRepo) Get(ctx context.Context, id entity.SettlementID) (*entity.Settlement, error) {
	if s.getFn != nil {
		return s.getFn(ctx, id)
	}
	return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
}
func (s *stubSettlementRepo) GetByOrderID(ctx context.Context, orderID entity.OrderID) (*entity.Settlement, error) {
	if s.getByOrderIDFn != nil {
		return s.getByOrderIDFn(ctx, orderID)
	}
	return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
}
func (s *stubSettlementRepo) ListHeld(ctx context.Context) ([]*entity.Settlement, error) {
	if s.listHeldFn != nil {
		return s.listHeldFn(ctx)
	}
	return nil, nil
}
func (s *stubSettlementRepo) MarkReleased(ctx context.Context, id entity.SettlementID, chargeRef string, releasedAt time.Time, splits []entity.SettlementSplit) error {
	if s.markReleasedFn != nil {
		return s.markReleasedFn(ctx, id, chargeRef, releasedAt, splits)
	}
	return nil
}

type stubConnectedAccountRepo struct {
	getByOrganizerIDFn func(ctx context.Context, organizerID string) (*entity.OrganizerConnectedAccount, error)
	upsertFn           func(ctx context.Context, a *entity.OrganizerConnectedAccount) error
	updateStatusFn     func(ctx context.Context, organizerID string, status entity.PayoutOnboardingStatus) error
}

func (s *stubConnectedAccountRepo) GetByOrganizerID(ctx context.Context, organizerID string) (*entity.OrganizerConnectedAccount, error) {
	if s.getByOrganizerIDFn != nil {
		return s.getByOrganizerIDFn(ctx, organizerID)
	}
	return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
}
func (s *stubConnectedAccountRepo) Upsert(ctx context.Context, a *entity.OrganizerConnectedAccount) error {
	if s.upsertFn != nil {
		return s.upsertFn(ctx, a)
	}
	return nil
}
func (s *stubConnectedAccountRepo) UpdateStatus(ctx context.Context, organizerID string, status entity.PayoutOnboardingStatus) error {
	if s.updateStatusFn != nil {
		return s.updateStatusFn(ctx, organizerID, status)
	}
	return nil
}

type stubEventStartTimeRepo struct {
	getFn func(ctx context.Context, eventID string) (*time.Time, error)
}

func (s *stubEventStartTimeRepo) GetEventStartTime(ctx context.Context, eventID string) (*time.Time, error) {
	if s.getFn != nil {
		return s.getFn(ctx, eventID)
	}
	return nil, nil
}

type stubPaymentSettlementPort struct {
	resolveChargeRefFn       func(ctx context.Context, piRef string) (string, error)
	createTransferFn         func(ctx context.Context, params usecase.TransferParams) (string, error)
	createConnectedAccountFn func(ctx context.Context, organizerID string) (string, error)
	getAccountStatusFn       func(ctx context.Context, accountRef string) (entity.PayoutOnboardingStatus, error)
	createOnboardingLinkFn   func(ctx context.Context, accountRef, returnURL string) (string, error)
	createRefundFn           func(ctx context.Context, params usecase.RefundParams) (string, error)
	reverseTransferFn        func(ctx context.Context, params usecase.ReverseTransferParams) (string, error)
}

func (s *stubPaymentSettlementPort) ResolveChargeRef(ctx context.Context, piRef string) (string, error) {
	if s.resolveChargeRefFn != nil {
		return s.resolveChargeRefFn(ctx, piRef)
	}
	return "ch_test", nil
}
func (s *stubPaymentSettlementPort) CreateTransfer(ctx context.Context, params usecase.TransferParams) (string, error) {
	if s.createTransferFn != nil {
		return s.createTransferFn(ctx, params)
	}
	return "tr_test", nil
}
func (s *stubPaymentSettlementPort) CreateConnectedAccount(ctx context.Context, organizerID string) (string, error) {
	if s.createConnectedAccountFn != nil {
		return s.createConnectedAccountFn(ctx, organizerID)
	}
	return "acct_test", nil
}
func (s *stubPaymentSettlementPort) GetAccountStatus(ctx context.Context, accountRef string) (entity.PayoutOnboardingStatus, error) {
	if s.getAccountStatusFn != nil {
		return s.getAccountStatusFn(ctx, accountRef)
	}
	return entity.PayoutOnboardingStatusActive, nil
}
func (s *stubPaymentSettlementPort) CreateOnboardingLink(ctx context.Context, accountRef, returnURL string) (string, error) {
	if s.createOnboardingLinkFn != nil {
		return s.createOnboardingLinkFn(ctx, accountRef, returnURL)
	}
	return "https://connect.stripe.com/onboarding/test", nil
}
func (s *stubPaymentSettlementPort) CreateRefund(ctx context.Context, params usecase.RefundParams) (string, error) {
	if s.createRefundFn != nil {
		return s.createRefundFn(ctx, params)
	}
	return "re_test", nil
}
func (s *stubPaymentSettlementPort) ReverseTransfer(ctx context.Context, params usecase.ReverseTransferParams) (string, error) {
	if s.reverseTransferFn != nil {
		return s.reverseTransferFn(ctx, params)
	}
	return "trr_test", nil
}

// ─────────────────────────────────────────────────────────────────────────────
// IsReleaseEligible — pure function tests
// ─────────────────────────────────────────────────────────────────────────────

func TestIsReleaseEligible(t *testing.T) {
	t.Parallel()

	const buffer = 7 * 24 * time.Hour
	eventStart := time.Date(2026, 9, 1, 18, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		now       time.Time
		startTime time.Time // zero value = not published
		want      bool
	}{
		{
			name:      "return false when event has not yet started",
			now:       eventStart.Add(-1 * time.Hour),
			startTime: eventStart,
			want:      false,
		},
		{
			name:      "return false when event just started (buffer not elapsed)",
			now:       eventStart.Add(1 * time.Hour),
			startTime: eventStart,
			want:      false,
		},
		{
			name:      "return false when event started but buffer not fully elapsed",
			now:       eventStart.Add(buffer - time.Second),
			startTime: eventStart,
			want:      false,
		},
		{
			name:      "return true when event started and full buffer has elapsed",
			now:       eventStart.Add(buffer + time.Second),
			startTime: eventStart,
			want:      true,
		},
		{
			name:      "return false when start_time is zero (event time not yet published)",
			now:       time.Now().Add(30 * 24 * time.Hour),
			startTime: time.Time{},
			want:      false,
		},
		{
			name: "return false after postponement resets clock to a future date",
			// Originally 2026-09-01; postponed to 2026-11-01.
			// now = 2026-09-15 (would have been eligible for the old date).
			now:       time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC),
			startTime: time.Date(2026, 11, 1, 18, 0, 0, 0, time.UTC),
			want:      false,
		},
		{
			name:      "return true after the postponed date plus buffer has elapsed",
			now:       time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC),
			startTime: time.Date(2026, 11, 1, 18, 0, 0, 0, time.UTC),
			want:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := entity.IsReleaseEligible(tc.now, tc.startTime, buffer)
			assert.Equal(t, tc.want, got)
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Splits engine (tested via ReleaseDueSettlements)
// ─────────────────────────────────────────────────────────────────────────────

func TestPayoutSweeper_SplitsEngine(t *testing.T) {
	t.Parallel()

	const (
		chargeAmount = int64(10000)
		orgID        = "org-1"
		eventID      = "event-1"
		piRef        = "pi_test"
		acctRef      = "acct_test"
	)

	pastStartVal := time.Now().Add(-30 * 24 * time.Hour)
	pastStart := &pastStartVal

	baseOrder := &entity.Order{
		ID:       "order-1",
		Amount:   chargeAmount,
		Currency: "JPY",
		Payment:  entity.Payment{PaymentIntentRef: piRef},
	}

	tests := []struct {
		name             string
		splits           []entity.SettlementSplit
		wantMarkReleased bool
	}{
		{
			name: "single organizer split within charge — MVP happy path",
			splits: []entity.SettlementSplit{
				{PayeeOrganizerID: orgID, Amount: 9000},
			},
			wantMarkReleased: true,
		},
		{
			name: "split exactly equal to charge amount is allowed",
			splits: []entity.SettlementSplit{
				{PayeeOrganizerID: orgID, Amount: chargeAmount},
			},
			wantMarkReleased: true,
		},
		{
			name: "sum of splits exceeds charge — skipped (logged, not returned)",
			splits: []entity.SettlementSplit{
				{PayeeOrganizerID: orgID, Amount: chargeAmount + 1},
			},
			wantMarkReleased: false,
		},
		{
			name:             "no splits — skipped",
			splits:           nil,
			wantMarkReleased: false,
		},
		{
			name: "split with zero amount — skipped",
			splits: []entity.SettlementSplit{
				{PayeeOrganizerID: orgID, Amount: 0},
			},
			wantMarkReleased: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			settlement := &entity.Settlement{
				ID:          "settle-1",
				OrderID:     baseOrder.ID,
				OrganizerID: orgID,
				EventID:     eventID,
				Splits:      tc.splits,
				Status:      entity.SettlementStatusHeld,
			}

			markReleasedCalled := false

			uc := usecase.NewPayoutSweeperUseCase(
				&stubSettlementRepo{
					listHeldFn: func(_ context.Context) ([]*entity.Settlement, error) {
						return []*entity.Settlement{settlement}, nil
					},
					markReleasedFn: func(_ context.Context, _ entity.SettlementID, _ string, _ time.Time, _ []entity.SettlementSplit) error {
						markReleasedCalled = true
						return nil
					},
				},
				&stubOrderRepo{
					getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) {
						return baseOrder, nil
					},
				},
				&stubConnectedAccountRepo{
					getByOrganizerIDFn: func(_ context.Context, _ string) (*entity.OrganizerConnectedAccount, error) {
						return &entity.OrganizerConnectedAccount{
							OrganizerID: orgID,
							AccountRef:  acctRef,
							Status:      entity.PayoutOnboardingStatusActive,
						}, nil
					},
				},
				&stubEventStartTimeRepo{
					getFn: func(_ context.Context, _ string) (*time.Time, error) { return pastStart, nil },
				},
				&stubPaymentSettlementPort{},
				30*24*time.Hour,
				func() time.Time { return time.Now() },
				newTestLogger(t),
			)

			// Sweep always returns nil (per-settlement errors are logged+skipped).
			require.NoError(t, uc.ReleaseDueSettlements(ctx))
			assert.Equal(t, tc.wantMarkReleased, markReleasedCalled,
				"MarkReleased call mismatch")
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Happy path: held → released (ch_ resolved, tr_ recorded)
// ─────────────────────────────────────────────────────────────────────────────

func TestPayoutSweeper_HappyPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pastStartVal := time.Now().Add(-30 * 24 * time.Hour)
	pastStart := &pastStartVal

	const (
		settleID = entity.SettlementID("settle-happy")
		orderID  = entity.OrderID("order-happy")
		orgID    = "org-happy"
		eventID  = "event-happy"
		piRef    = "pi_happy"
		chRef    = "ch_happy"
		trRef    = "tr_happy"
		acctRef  = "acct_happy"
	)

	settlement := &entity.Settlement{
		ID:          settleID,
		OrderID:     orderID,
		OrganizerID: orgID,
		EventID:     eventID,
		Splits:      []entity.SettlementSplit{{PayeeOrganizerID: orgID, Amount: 9000}},
		Status:      entity.SettlementStatusHeld,
	}

	var (
		gotChargeRef   string
		gotTransferRef string
		markCalled     bool
	)

	uc := usecase.NewPayoutSweeperUseCase(
		&stubSettlementRepo{
			listHeldFn: func(_ context.Context) ([]*entity.Settlement, error) {
				return []*entity.Settlement{settlement}, nil
			},
			markReleasedFn: func(_ context.Context, id entity.SettlementID, chargeRef string, _ time.Time, splits []entity.SettlementSplit) error {
				assert.Equal(t, settleID, id)
				gotChargeRef = chargeRef
				if len(splits) > 0 {
					gotTransferRef = splits[0].TransferRef
				}
				markCalled = true
				return nil
			},
		},
		&stubOrderRepo{
			getFn: func(_ context.Context, id entity.OrderID) (*entity.Order, error) {
				require.Equal(t, orderID, id)
				return &entity.Order{
					ID: orderID, Amount: 10000, Currency: "JPY",
					Payment: entity.Payment{PaymentIntentRef: piRef},
				}, nil
			},
		},
		&stubConnectedAccountRepo{
			getByOrganizerIDFn: func(_ context.Context, _ string) (*entity.OrganizerConnectedAccount, error) {
				return &entity.OrganizerConnectedAccount{
					OrganizerID: orgID, AccountRef: acctRef,
					Status: entity.PayoutOnboardingStatusActive,
				}, nil
			},
		},
		&stubEventStartTimeRepo{
			getFn: func(_ context.Context, _ string) (*time.Time, error) { return pastStart, nil },
		},
		&stubPaymentSettlementPort{
			resolveChargeRefFn: func(_ context.Context, _ string) (string, error) { return chRef, nil },
			createTransferFn: func(_ context.Context, p usecase.TransferParams) (string, error) {
				assert.Equal(t, chRef, p.SourceTransactionRef)
				assert.Equal(t, acctRef, p.PayeeAccountRef)
				assert.Equal(t, int64(9000), p.Amount)
				return trRef, nil
			},
			getAccountStatusFn: func(_ context.Context, _ string) (entity.PayoutOnboardingStatus, error) {
				return entity.PayoutOnboardingStatusActive, nil
			},
		},
		30*24*time.Hour,
		func() time.Time { return time.Now() },
		newTestLogger(t),
	)

	require.NoError(t, uc.ReleaseDueSettlements(ctx))
	assert.True(t, markCalled, "MarkReleased must be called")
	assert.Equal(t, chRef, gotChargeRef)
	assert.Equal(t, trRef, gotTransferRef)
}

// ─────────────────────────────────────────────────────────────────────────────
// Organizer account not Active → payout withheld (not failed)
// ─────────────────────────────────────────────────────────────────────────────

func TestPayoutSweeper_OrganizerNotActive_PayoutWithheld(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pastStartVal := time.Now().Add(-30 * 24 * time.Hour)
	pastStart := &pastStartVal

	settlement := &entity.Settlement{
		ID: "settle-pending", OrderID: "order-pending",
		OrganizerID: "org-pending", EventID: "event-pending",
		Splits: []entity.SettlementSplit{{PayeeOrganizerID: "org-pending", Amount: 9000}},
		Status: entity.SettlementStatusHeld,
	}

	transferCalled := false
	markCalled := false

	uc := usecase.NewPayoutSweeperUseCase(
		&stubSettlementRepo{
			listHeldFn: func(_ context.Context) ([]*entity.Settlement, error) {
				return []*entity.Settlement{settlement}, nil
			},
			markReleasedFn: func(_ context.Context, _ entity.SettlementID, _ string, _ time.Time, _ []entity.SettlementSplit) error {
				markCalled = true
				return nil
			},
		},
		&stubOrderRepo{
			getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) {
				return &entity.Order{ID: "order-pending", Amount: 10000, Currency: "JPY",
					Payment: entity.Payment{PaymentIntentRef: "pi_pending"}}, nil
			},
		},
		&stubConnectedAccountRepo{
			getByOrganizerIDFn: func(_ context.Context, _ string) (*entity.OrganizerConnectedAccount, error) {
				return &entity.OrganizerConnectedAccount{
					OrganizerID: "org-pending", AccountRef: "acct_pending",
					Status: entity.PayoutOnboardingStatusPending,
				}, nil
			},
		},
		&stubEventStartTimeRepo{
			getFn: func(_ context.Context, _ string) (*time.Time, error) { return pastStart, nil },
		},
		&stubPaymentSettlementPort{
			getAccountStatusFn: func(_ context.Context, _ string) (entity.PayoutOnboardingStatus, error) {
				return entity.PayoutOnboardingStatusPending, nil
			},
			createTransferFn: func(_ context.Context, _ usecase.TransferParams) (string, error) {
				transferCalled = true
				return "tr_should_not_happen", nil
			},
		},
		30*24*time.Hour,
		func() time.Time { return time.Now() },
		newTestLogger(t),
	)

	require.NoError(t, uc.ReleaseDueSettlements(ctx))
	assert.False(t, transferCalled, "Transfer must NOT be created when account is Pending")
	assert.False(t, markCalled, "MarkReleased must NOT be called when payout is withheld")
}

// ─────────────────────────────────────────────────────────────────────────────
// Release gate not passed → no transfer
// ─────────────────────────────────────────────────────────────────────────────

func TestPayoutSweeper_GateNotPassed_NoTransfer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	futureStartVal := time.Now().Add(30 * 24 * time.Hour)
	futureStart := &futureStartVal

	settlement := &entity.Settlement{
		ID: "settle-future", OrderID: "order-future",
		OrganizerID: "org-future", EventID: "event-future",
		Splits: []entity.SettlementSplit{{PayeeOrganizerID: "org-future", Amount: 9000}},
		Status: entity.SettlementStatusHeld,
	}

	transferCalled := false
	markCalled := false

	uc := usecase.NewPayoutSweeperUseCase(
		&stubSettlementRepo{
			listHeldFn: func(_ context.Context) ([]*entity.Settlement, error) {
				return []*entity.Settlement{settlement}, nil
			},
			markReleasedFn: func(_ context.Context, _ entity.SettlementID, _ string, _ time.Time, _ []entity.SettlementSplit) error {
				markCalled = true
				return nil
			},
		},
		&stubOrderRepo{
			getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) {
				return &entity.Order{ID: "order-future", Amount: 10000, Currency: "JPY",
					Payment: entity.Payment{PaymentIntentRef: "pi_future"}}, nil
			},
		},
		&stubConnectedAccountRepo{
			getByOrganizerIDFn: func(_ context.Context, _ string) (*entity.OrganizerConnectedAccount, error) {
				return &entity.OrganizerConnectedAccount{
					OrganizerID: "org-future", AccountRef: "acct_future",
					Status: entity.PayoutOnboardingStatusActive,
				}, nil
			},
		},
		&stubEventStartTimeRepo{
			getFn: func(_ context.Context, _ string) (*time.Time, error) { return futureStart, nil },
		},
		&stubPaymentSettlementPort{
			createTransferFn: func(_ context.Context, _ usecase.TransferParams) (string, error) {
				transferCalled = true
				return "tr_should_not_happen", nil
			},
		},
		7*24*time.Hour,
		func() time.Time { return time.Now() },
		newTestLogger(t),
	)

	require.NoError(t, uc.ReleaseDueSettlements(ctx))
	assert.False(t, transferCalled, "Transfer must NOT be created before gate passes")
	assert.False(t, markCalled, "MarkReleased must NOT be called before gate passes")
}

// ─────────────────────────────────────────────────────────────────────────────
// Idempotent re-run: MarkReleased returns FailedPrecondition (concurrent
// release) → sweep returns nil, no double error
// ─────────────────────────────────────────────────────────────────────────────

func TestPayoutSweeper_IdempotentRerun_NoDoubleTransfer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pastStartVal := time.Now().Add(-30 * 24 * time.Hour)
	pastStart := &pastStartVal

	settlement := &entity.Settlement{
		ID: "settle-idem", OrderID: "order-idem",
		OrganizerID: "org-idem", EventID: "event-idem",
		Splits: []entity.SettlementSplit{{PayeeOrganizerID: "org-idem", Amount: 9000}},
		Status: entity.SettlementStatusHeld,
	}

	transferCallCount := 0

	uc := usecase.NewPayoutSweeperUseCase(
		&stubSettlementRepo{
			listHeldFn: func(_ context.Context) ([]*entity.Settlement, error) {
				return []*entity.Settlement{settlement}, nil
			},
			// Simulate another sweep pod that already released this settlement.
			markReleasedFn: func(_ context.Context, _ entity.SettlementID, _ string, _ time.Time, _ []entity.SettlementSplit) error {
				return apperr.New(codes.FailedPrecondition, "settlement already released")
			},
		},
		&stubOrderRepo{
			getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) {
				return &entity.Order{ID: "order-idem", Amount: 10000, Currency: "JPY",
					Payment: entity.Payment{PaymentIntentRef: "pi_idem"}}, nil
			},
		},
		&stubConnectedAccountRepo{
			getByOrganizerIDFn: func(_ context.Context, _ string) (*entity.OrganizerConnectedAccount, error) {
				return &entity.OrganizerConnectedAccount{
					OrganizerID: "org-idem", AccountRef: "acct_idem",
					Status: entity.PayoutOnboardingStatusActive,
				}, nil
			},
		},
		&stubEventStartTimeRepo{
			getFn: func(_ context.Context, _ string) (*time.Time, error) { return pastStart, nil },
		},
		&stubPaymentSettlementPort{
			createTransferFn: func(_ context.Context, _ usecase.TransferParams) (string, error) {
				transferCallCount++
				return "tr_idem", nil
			},
			getAccountStatusFn: func(_ context.Context, _ string) (entity.PayoutOnboardingStatus, error) {
				return entity.PayoutOnboardingStatusActive, nil
			},
		},
		30*24*time.Hour,
		func() time.Time { return time.Now() },
		newTestLogger(t),
	)

	// Must not return an error even though MarkReleased signalled FailedPrecondition.
	require.NoError(t, uc.ReleaseDueSettlements(ctx))
	// Transfer is called (before MarkReleased) but the Stripe idempotency key
	// ensures no double charge at the provider level. We verify at most 1 call.
	assert.LessOrEqual(t, transferCallCount, 1, "Transfer must be called at most once per sweep tick")
}
