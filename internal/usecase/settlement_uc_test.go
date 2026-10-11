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
	getFn          func(ctx context.Context, id entity.SettlementID) (*entity.Settlement, error)
	getByOrderIDFn func(ctx context.Context, orderID entity.OrderID) (*entity.Settlement, error)
	listHeldFn     func(ctx context.Context) ([]*entity.Settlement, error)
	markReleasedFn func(ctx context.Context, id entity.SettlementID, providerChargeID string, releasedAt time.Time, splits []entity.SettlementSplit) error
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
func (s *stubSettlementRepo) MarkReleased(ctx context.Context, id entity.SettlementID, providerChargeID string, releasedAt time.Time, splits []entity.SettlementSplit) error {
	if s.markReleasedFn != nil {
		return s.markReleasedFn(ctx, id, providerChargeID, releasedAt, splits)
	}
	return nil
}

type stubConnectedAccountRepo struct {
	getByOrganizerIDFn func(ctx context.Context, organizerID string) (*entity.PayoutAccount, error)
	upsertFn           func(ctx context.Context, a *entity.PayoutAccount) error
	updateStatusFn     func(ctx context.Context, organizerID string, status entity.PayoutOnboardingStatus) error
}

func (s *stubConnectedAccountRepo) GetByOrganizerID(ctx context.Context, organizerID string) (*entity.PayoutAccount, error) {
	if s.getByOrganizerIDFn != nil {
		return s.getByOrganizerIDFn(ctx, organizerID)
	}
	return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
}
func (s *stubConnectedAccountRepo) Upsert(ctx context.Context, a *entity.PayoutAccount) error {
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

type stubSettlementPayoutRepository struct {
	resolveProviderChargeIDFn func(ctx context.Context, piRef string) (string, error)
	createTransferFn          func(ctx context.Context, params entity.TransferParams) (string, error)
	createConnectedAccountFn  func(ctx context.Context, organizerID string) (string, error)
	getAccountStatusFn        func(ctx context.Context, providerAccountID string) (entity.PayoutOnboardingStatus, error)
	createOnboardingLinkFn    func(ctx context.Context, providerAccountID, returnURL string) (string, error)
	createRefundFn            func(ctx context.Context, params entity.RefundParams) (string, error)
	reverseTransferFn         func(ctx context.Context, params entity.ReverseTransferParams) (string, error)
}

func (s *stubSettlementPayoutRepository) GetProviderChargeID(ctx context.Context, piRef string) (string, error) {
	if s.resolveProviderChargeIDFn != nil {
		return s.resolveProviderChargeIDFn(ctx, piRef)
	}
	return "ch_test", nil
}
func (s *stubSettlementPayoutRepository) CreatePayout(ctx context.Context, params entity.TransferParams) (string, error) {
	if s.createTransferFn != nil {
		return s.createTransferFn(ctx, params)
	}
	return "tr_test", nil
}
func (s *stubSettlementPayoutRepository) CreatePayoutAccount(ctx context.Context, organizerID string, _ string) (string, error) {
	if s.createConnectedAccountFn != nil {
		return s.createConnectedAccountFn(ctx, organizerID)
	}
	return "acct_test", nil
}
func (s *stubSettlementPayoutRepository) GetPayoutAccountStatus(ctx context.Context, providerAccountID string) (entity.PayoutOnboardingStatus, error) {
	if s.getAccountStatusFn != nil {
		return s.getAccountStatusFn(ctx, providerAccountID)
	}
	return entity.PayoutOnboardingStatusActive, nil
}
func (s *stubSettlementPayoutRepository) CreateOnboardingLink(ctx context.Context, providerAccountID, returnURL string) (string, error) {
	if s.createOnboardingLinkFn != nil {
		return s.createOnboardingLinkFn(ctx, providerAccountID, returnURL)
	}
	return "https://connect.stripe.com/onboarding/test", nil
}
func (s *stubSettlementPayoutRepository) CreateRefund(ctx context.Context, params entity.RefundParams) (string, error) {
	if s.createRefundFn != nil {
		return s.createRefundFn(ctx, params)
	}
	return "re_test", nil
}
func (s *stubSettlementPayoutRepository) ReversePayout(ctx context.Context, params entity.ReverseTransferParams) (string, error) {
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
		Payment:  entity.Payment{ProviderPaymentID: piRef},
	}

	tests := []struct {
		name             string
		splits           []entity.SettlementSplit
		wantMarkReleased bool
	}{
		{
			// @spec components/entity/settlement "Single Organizer split"
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
			// @spec components/entity/settlement "Splits exceed the order"
			name: "sum of splits exceeds charge — skipped (logged, not returned)",
			splits: []entity.SettlementSplit{
				{PayeeOrganizerID: orgID, Amount: chargeAmount + 1},
			},
			wantMarkReleased: false,
		},
		{
			// @spec components/entity/settlement "No split"
			name:             "no splits — skipped",
			splits:           nil,
			wantMarkReleased: false,
		},
		{
			// @spec components/entity/settlement "Non-positive split"
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

			uc := usecase.NewSettlementUseCase(
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
					getByOrganizerIDFn: func(_ context.Context, _ string) (*entity.PayoutAccount, error) {
						return &entity.PayoutAccount{
							OrganizerID:       orgID,
							ProviderAccountID: acctRef,
							Status:            entity.PayoutOnboardingStatusActive,
						}, nil
					},
				},
				&stubEventStartTimeRepo{
					getFn: func(_ context.Context, _ string) (*time.Time, error) { return pastStart, nil },
				},
				&stubSettlementPayoutRepository{},
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
		gotProviderChargeID string
		gotProviderPayoutID string
		markCalled          bool
	)

	uc := usecase.NewSettlementUseCase(
		&stubSettlementRepo{
			listHeldFn: func(_ context.Context) ([]*entity.Settlement, error) {
				return []*entity.Settlement{settlement}, nil
			},
			markReleasedFn: func(_ context.Context, id entity.SettlementID, providerChargeID string, _ time.Time, splits []entity.SettlementSplit) error {
				assert.Equal(t, settleID, id)
				gotProviderChargeID = providerChargeID
				if len(splits) > 0 {
					gotProviderPayoutID = splits[0].ProviderPayoutID
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
					Payment: entity.Payment{ProviderPaymentID: piRef},
				}, nil
			},
		},
		&stubConnectedAccountRepo{
			getByOrganizerIDFn: func(_ context.Context, _ string) (*entity.PayoutAccount, error) {
				return &entity.PayoutAccount{
					OrganizerID: orgID, ProviderAccountID: acctRef,
					Status: entity.PayoutOnboardingStatusActive,
				}, nil
			},
		},
		&stubEventStartTimeRepo{
			getFn: func(_ context.Context, _ string) (*time.Time, error) { return pastStart, nil },
		},
		&stubSettlementPayoutRepository{
			resolveProviderChargeIDFn: func(_ context.Context, _ string) (string, error) { return chRef, nil },
			createTransferFn: func(_ context.Context, p entity.TransferParams) (string, error) {
				assert.Equal(t, chRef, p.SourceTransactionRef)
				assert.Equal(t, acctRef, p.PayeeProviderAccountID)
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
	assert.Equal(t, chRef, gotProviderChargeID)
	assert.Equal(t, trRef, gotProviderPayoutID)
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

	uc := usecase.NewSettlementUseCase(
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
					Payment: entity.Payment{ProviderPaymentID: "pi_pending"}}, nil
			},
		},
		&stubConnectedAccountRepo{
			getByOrganizerIDFn: func(_ context.Context, _ string) (*entity.PayoutAccount, error) {
				return &entity.PayoutAccount{
					OrganizerID: "org-pending", ProviderAccountID: "acct_pending",
					Status: entity.PayoutOnboardingStatusPending,
				}, nil
			},
		},
		&stubEventStartTimeRepo{
			getFn: func(_ context.Context, _ string) (*time.Time, error) { return pastStart, nil },
		},
		&stubSettlementPayoutRepository{
			getAccountStatusFn: func(_ context.Context, _ string) (entity.PayoutOnboardingStatus, error) {
				return entity.PayoutOnboardingStatusPending, nil
			},
			createTransferFn: func(_ context.Context, _ entity.TransferParams) (string, error) {
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

	uc := usecase.NewSettlementUseCase(
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
					Payment: entity.Payment{ProviderPaymentID: "pi_future"}}, nil
			},
		},
		&stubConnectedAccountRepo{
			getByOrganizerIDFn: func(_ context.Context, _ string) (*entity.PayoutAccount, error) {
				return &entity.PayoutAccount{
					OrganizerID: "org-future", ProviderAccountID: "acct_future",
					Status: entity.PayoutOnboardingStatusActive,
				}, nil
			},
		},
		&stubEventStartTimeRepo{
			getFn: func(_ context.Context, _ string) (*time.Time, error) { return futureStart, nil },
		},
		&stubSettlementPayoutRepository{
			createTransferFn: func(_ context.Context, _ entity.TransferParams) (string, error) {
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
// Release gate: the event's current start time is read on every run
// ─────────────────────────────────────────────────────────────────────────────

func TestPayoutSweeper_ReleaseGate(t *testing.T) {
	t.Parallel()

	const buffer = 7 * 24 * time.Hour
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	ptr := func(t time.Time) *time.Time { return &t }

	tests := []struct {
		name string
		// startTimes is the event start time returned on each successive run;
		// nil means the event has no start time yet.
		startTimes []*time.Time
		// wantReleased is whether the Settlement is released on each run.
		wantReleased []bool
	}{
		{
			// @spec components/usecase/settlement/release-due-settlements "Before the event plus 7 days"
			name:         "keep the settlement held when the event started 3 days ago",
			startTimes:   []*time.Time{ptr(now.Add(-3 * 24 * time.Hour))},
			wantReleased: []bool{false},
		},
		{
			// @spec components/usecase/settlement/release-due-settlements "Event start unknown"
			name:         "keep the settlement held when the event has no start time",
			startTimes:   []*time.Time{nil},
			wantReleased: []bool{false},
		},
		{
			// @spec components/usecase/settlement/release-due-settlements "Start time filled in after the purchase"
			name:         "release on the next run once a start time 8 days ago is filled in",
			startTimes:   []*time.Time{nil, ptr(now.Add(-8 * 24 * time.Hour))},
			wantReleased: []bool{false, true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			settlement := &entity.Settlement{
				ID: "settle-gate", OrderID: "order-gate",
				OrganizerID: "org-gate", EventID: "event-gate",
				Splits: []entity.SettlementSplit{{PayeeOrganizerID: "org-gate", Amount: 9000}},
				Status: entity.SettlementStatusHeld,
			}

			run := 0
			released := false

			uc := usecase.NewSettlementUseCase(
				&stubSettlementRepo{
					listHeldFn: func(_ context.Context) ([]*entity.Settlement, error) {
						return []*entity.Settlement{settlement}, nil
					},
					markReleasedFn: func(_ context.Context, _ entity.SettlementID, _ string, _ time.Time, _ []entity.SettlementSplit) error {
						released = true
						return nil
					},
				},
				&stubOrderRepo{
					getFn: func(_ context.Context, _ entity.OrderID) (*entity.Order, error) {
						return &entity.Order{ID: "order-gate", Amount: 10000, Currency: "JPY",
							Payment: entity.Payment{ProviderPaymentID: "pi_gate"}}, nil
					},
				},
				&stubConnectedAccountRepo{
					getByOrganizerIDFn: func(_ context.Context, _ string) (*entity.PayoutAccount, error) {
						return &entity.PayoutAccount{
							OrganizerID: "org-gate", ProviderAccountID: "acct_gate",
							Status: entity.PayoutOnboardingStatusActive,
						}, nil
					},
				},
				&stubEventStartTimeRepo{
					getFn: func(_ context.Context, _ string) (*time.Time, error) { return tc.startTimes[run], nil },
				},
				&stubSettlementPayoutRepository{
					resolveProviderChargeIDFn: func(_ context.Context, _ string) (string, error) { return "ch_gate", nil },
					createTransferFn: func(_ context.Context, _ entity.TransferParams) (string, error) {
						return "tr_gate", nil
					},
					getAccountStatusFn: func(_ context.Context, _ string) (entity.PayoutOnboardingStatus, error) {
						return entity.PayoutOnboardingStatusActive, nil
					},
				},
				buffer,
				func() time.Time { return now },
				newTestLogger(t),
			)

			for ; run < len(tc.startTimes); run++ {
				released = false
				require.NoError(t, uc.ReleaseDueSettlements(context.Background()))
				assert.Equal(t, tc.wantReleased[run], released, "run %d", run+1)
			}
		})
	}
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

	uc := usecase.NewSettlementUseCase(
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
					Payment: entity.Payment{ProviderPaymentID: "pi_idem"}}, nil
			},
		},
		&stubConnectedAccountRepo{
			getByOrganizerIDFn: func(_ context.Context, _ string) (*entity.PayoutAccount, error) {
				return &entity.PayoutAccount{
					OrganizerID: "org-idem", ProviderAccountID: "acct_idem",
					Status: entity.PayoutOnboardingStatusActive,
				}, nil
			},
		},
		&stubEventStartTimeRepo{
			getFn: func(_ context.Context, _ string) (*time.Time, error) { return pastStart, nil },
		},
		&stubSettlementPayoutRepository{
			createTransferFn: func(_ context.Context, _ entity.TransferParams) (string, error) {
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
