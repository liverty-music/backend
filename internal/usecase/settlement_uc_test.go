package usecase_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

// settlementFixture holds the mocks of a SettlementUseCase under test.
type settlementFixture struct {
	settlements *entitymocks.MockSettlementRepository
	orders      *entitymocks.MockOrderRepository
	accounts    *entitymocks.MockPayoutAccountRepository
	starts      *ucmocks.MockEventStartTimeRepository
	uc          usecase.SettlementUseCase
}

func newSettlementFixture(t *testing.T, disputeBuffer time.Duration, now func() time.Time) *settlementFixture {
	t.Helper()
	f := &settlementFixture{
		settlements: entitymocks.NewMockSettlementRepository(t),
		orders:      entitymocks.NewMockOrderRepository(t),
		accounts:    entitymocks.NewMockPayoutAccountRepository(t),
		starts:      ucmocks.NewMockEventStartTimeRepository(t),
	}
	f.uc = usecase.NewSettlementUseCase(f.settlements, f.orders, f.accounts, f.starts, disputeBuffer, now, newTestLogger(t))
	return f
}

// held makes s the only Held Settlement and order the Order it is for, on
// every run.
func (f *settlementFixture) held(s *entity.Settlement, order *entity.Order) {
	f.settlements.EXPECT().ListHeld(mock.Anything).Return([]*entity.Settlement{s}, nil)
	f.orders.EXPECT().Get(mock.Anything, s.OrderID).Return(order, nil)
}

// startsAt makes the start time of eventID start on every run.
func (f *settlementFixture) startsAt(eventID string, start *time.Time) {
	f.starts.EXPECT().GetEventStartTime(mock.Anything, eventID).Return(start, nil)
}

// account makes the Organizer's payout account a, whose status the payment
// provider reports as fresh.
func (f *settlementFixture) account(a *entity.PayoutAccount, fresh entity.PayoutOnboardingStatus) {
	f.accounts.EXPECT().GetByOrganizerID(mock.Anything, a.OrganizerID).Return(a, nil)
	f.accounts.EXPECT().GetStatus(mock.Anything, a.ProviderAccountID).Return(fresh, nil)
}

// paysOut expects the charge of providerPaymentID to be resolved to
// providerChargeID and one payout to be created as providerPayoutID, once.
func (f *settlementFixture) paysOut(providerPaymentID, providerChargeID, providerPayoutID string) {
	f.orders.EXPECT().GetProviderChargeID(mock.Anything, providerPaymentID).Return(providerChargeID, nil).Once()
	f.settlements.EXPECT().CreatePayout(mock.Anything, mock.Anything).Return(providerPayoutID, nil).Once()
}

// assertNotPaidOut asserts that no payout was created and no Settlement was
// marked Released.
func (f *settlementFixture) assertNotPaidOut(t *testing.T) {
	t.Helper()
	f.settlements.AssertNotCalled(t, "CreatePayout", mock.Anything, mock.Anything)
	f.settlements.AssertNotCalled(t, "MarkReleased", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
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

func TestSettlementUseCase_ReleaseDueSettlements_SplitsEngine(t *testing.T) {
	t.Parallel()

	const (
		chargeAmount      = int64(10000)
		orgID             = "org-1"
		eventID           = "event-1"
		providerPaymentID = "pi_test"
		providerAccountID = "acct_test"
	)

	pastStartVal := time.Now().Add(-30 * 24 * time.Hour)
	pastStart := &pastStartVal

	baseOrder := &entity.Order{
		ID:       "order-1",
		Amount:   chargeAmount,
		Currency: "JPY",
		Payment:  entity.Payment{ProviderPaymentID: providerPaymentID},
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

			f := newSettlementFixture(t, 30*24*time.Hour, time.Now)
			f.held(settlement, baseOrder)
			f.startsAt(eventID, pastStart)
			f.account(&entity.PayoutAccount{
				OrganizerID:       orgID,
				ProviderAccountID: providerAccountID,
				Status:            entity.PayoutOnboardingStatusActive,
			}, entity.PayoutOnboardingStatusActive)
			if tc.wantMarkReleased {
				f.paysOut(providerPaymentID, "ch_test", "tr_test")
				f.settlements.EXPECT().MarkReleased(mock.Anything, settlement.ID, "ch_test", mock.Anything, mock.Anything).Return(nil).Once()
			}

			// Sweep always returns nil (per-settlement errors are logged+skipped).
			require.NoError(t, f.uc.ReleaseDueSettlements(ctx))
			if !tc.wantMarkReleased {
				f.assertNotPaidOut(t)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Happy path: held → released (ch_ resolved, tr_ recorded)
// ─────────────────────────────────────────────────────────────────────────────

func TestSettlementUseCase_ReleaseDueSettlements_HappyPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pastStartVal := time.Now().Add(-30 * 24 * time.Hour)
	pastStart := &pastStartVal

	const (
		settleID          = entity.SettlementID("settle-happy")
		orderID           = entity.OrderID("order-happy")
		orgID             = "org-happy"
		eventID           = "event-happy"
		providerPaymentID = "pi_happy"
		providerChargeID  = "ch_happy"
		providerPayoutID  = "tr_happy"
		providerAccountID = "acct_happy"
	)

	settlement := &entity.Settlement{
		ID:          settleID,
		OrderID:     orderID,
		OrganizerID: orgID,
		EventID:     eventID,
		Splits:      []entity.SettlementSplit{{PayeeOrganizerID: orgID, Amount: 9000}},
		Status:      entity.SettlementStatusHeld,
	}

	f := newSettlementFixture(t, 30*24*time.Hour, time.Now)
	f.held(settlement, &entity.Order{
		ID: orderID, Amount: 10000, Currency: "JPY",
		Payment: entity.Payment{ProviderPaymentID: providerPaymentID},
	})
	f.startsAt(eventID, pastStart)
	f.account(&entity.PayoutAccount{
		OrganizerID: orgID, ProviderAccountID: providerAccountID,
		Status: entity.PayoutOnboardingStatusActive,
	}, entity.PayoutOnboardingStatusActive)
	f.orders.EXPECT().GetProviderChargeID(mock.Anything, providerPaymentID).Return(providerChargeID, nil).Once()
	f.settlements.EXPECT().CreatePayout(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, p entity.PayoutParams) (string, error) {
			assert.Equal(t, providerChargeID, p.ProviderChargeID)
			assert.Equal(t, providerAccountID, p.PayeeProviderAccountID)
			assert.Equal(t, int64(9000), p.Amount)
			return providerPayoutID, nil
		}).Once()
	var releasedSplits []entity.SettlementSplit
	f.settlements.EXPECT().MarkReleased(mock.Anything, settleID, providerChargeID, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ entity.SettlementID, _ string, _ time.Time, splits []entity.SettlementSplit) error {
			releasedSplits = splits
			return nil
		}).Once()

	require.NoError(t, f.uc.ReleaseDueSettlements(ctx))
	require.Len(t, releasedSplits, 1)
	assert.Equal(t, providerPayoutID, releasedSplits[0].ProviderPayoutID)
}

// ─────────────────────────────────────────────────────────────────────────────
// Organizer account not Active → payout withheld (not failed)
// ─────────────────────────────────────────────────────────────────────────────

func TestSettlementUseCase_ReleaseDueSettlements_OrganizerNotActive_PayoutWithheld(t *testing.T) {
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

	f := newSettlementFixture(t, 30*24*time.Hour, time.Now)
	f.held(settlement, &entity.Order{ID: "order-pending", Amount: 10000, Currency: "JPY",
		Payment: entity.Payment{ProviderPaymentID: "pi_pending"}})
	f.startsAt("event-pending", pastStart)
	f.account(&entity.PayoutAccount{
		OrganizerID: "org-pending", ProviderAccountID: "acct_pending",
		Status: entity.PayoutOnboardingStatusPending,
	}, entity.PayoutOnboardingStatusPending)

	require.NoError(t, f.uc.ReleaseDueSettlements(ctx))
	// Transfer must NOT be created and MarkReleased must NOT be called when
	// the account is Pending.
	f.assertNotPaidOut(t)
}

// ─────────────────────────────────────────────────────────────────────────────
// Release gate not passed → no transfer
// ─────────────────────────────────────────────────────────────────────────────

func TestSettlementUseCase_ReleaseDueSettlements_GateNotPassed_NoTransfer(t *testing.T) {
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

	f := newSettlementFixture(t, 7*24*time.Hour, time.Now)
	f.held(settlement, &entity.Order{ID: "order-future", Amount: 10000, Currency: "JPY",
		Payment: entity.Payment{ProviderPaymentID: "pi_future"}})
	f.startsAt("event-future", futureStart)

	require.NoError(t, f.uc.ReleaseDueSettlements(ctx))
	// Transfer must NOT be created and MarkReleased must NOT be called before
	// the gate passes.
	f.assertNotPaidOut(t)
}

// ─────────────────────────────────────────────────────────────────────────────
// Release gate: the event's current start time is read on every run
// ─────────────────────────────────────────────────────────────────────────────

func TestSettlementUseCase_ReleaseDueSettlements_ReleaseGate(t *testing.T) {
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

			f := newSettlementFixture(t, buffer, func() time.Time { return now })
			f.held(settlement, &entity.Order{ID: "order-gate", Amount: 10000, Currency: "JPY",
				Payment: entity.Payment{ProviderPaymentID: "pi_gate"}})
			// Each run reads the start time again, in order.
			for _, start := range tc.startTimes {
				f.starts.EXPECT().GetEventStartTime(mock.Anything, "event-gate").Return(start, nil).Once()
			}
			released := false
			if slices.Contains(tc.wantReleased, true) {
				f.account(&entity.PayoutAccount{
					OrganizerID: "org-gate", ProviderAccountID: "acct_gate",
					Status: entity.PayoutOnboardingStatusActive,
				}, entity.PayoutOnboardingStatusActive)
				f.paysOut("pi_gate", "ch_gate", "tr_gate")
				f.settlements.EXPECT().MarkReleased(mock.Anything, settlement.ID, "ch_gate", now, mock.Anything).
					RunAndReturn(func(context.Context, entity.SettlementID, string, time.Time, []entity.SettlementSplit) error {
						released = true
						return nil
					}).Once()
			}

			for run := range tc.startTimes {
				released = false
				require.NoError(t, f.uc.ReleaseDueSettlements(context.Background()))
				assert.Equal(t, tc.wantReleased[run], released, "run %d", run+1)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Idempotent re-run: MarkReleased returns FailedPrecondition (concurrent
// release) → sweep returns nil, no double error
// ─────────────────────────────────────────────────────────────────────────────

func TestSettlementUseCase_ReleaseDueSettlements_IdempotentRerun_NoDoubleTransfer(t *testing.T) {
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

	f := newSettlementFixture(t, 30*24*time.Hour, time.Now)
	f.held(settlement, &entity.Order{ID: "order-idem", Amount: 10000, Currency: "JPY",
		Payment: entity.Payment{ProviderPaymentID: "pi_idem"}})
	f.startsAt("event-idem", pastStart)
	f.account(&entity.PayoutAccount{
		OrganizerID: "org-idem", ProviderAccountID: "acct_idem",
		Status: entity.PayoutOnboardingStatusActive,
	}, entity.PayoutOnboardingStatusActive)
	// Transfer is called (before MarkReleased) but the Stripe idempotency key
	// ensures no double charge at the provider level: exactly once per tick.
	f.paysOut("pi_idem", "ch_test", "tr_idem")
	// Simulate another sweep pod that already released this settlement.
	f.settlements.EXPECT().MarkReleased(mock.Anything, settlement.ID, mock.Anything, mock.Anything, mock.Anything).
		Return(apperr.New(codes.FailedPrecondition, "settlement already released")).Once()

	// Must not return an error even though MarkReleased signalled FailedPrecondition.
	require.NoError(t, f.uc.ReleaseDueSettlements(ctx))
}
