package usecase_test

import (
	"bytes"
	"context"
	"slices"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
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
	events      *entitymocks.MockEventRepository
	starts      *ucmocks.MockEventStartTimeRepository
	uc          usecase.SettlementUseCase
}

func newSettlementFixture(t *testing.T, disputeBuffer time.Duration, now func() time.Time) *settlementFixture {
	t.Helper()
	f := &settlementFixture{
		settlements: entitymocks.NewMockSettlementRepository(t),
		orders:      entitymocks.NewMockOrderRepository(t),
		accounts:    entitymocks.NewMockPayoutAccountRepository(t),
		events:      entitymocks.NewMockEventRepository(t),
		starts:      ucmocks.NewMockEventStartTimeRepository(t),
	}
	f.uc = usecase.NewSettlementUseCase(f.settlements, f.orders, f.accounts, f.events, f.starts, disputeBuffer, now, newTestLogger(t))
	return f
}

// held makes s the only Held Settlement and order the Order it is for, on
// every run.
func (f *settlementFixture) held(s *entity.Settlement, order *entity.Order) {
	f.settlements.EXPECT().ListHeld(mock.Anything).Return([]*entity.Settlement{s}, nil)
	f.orders.EXPECT().Get(mock.Anything, s.OrderID).Return(order, nil)
}

// publishedEvent makes eventID a PUBLISHED event on every run.
func (f *settlementFixture) publishedEvent(eventID string) {
	f.events.EXPECT().Get(mock.Anything, eventID).Return(eventInState(eventID, entity.PublishStatePublished), nil)
}

// eventInState returns the event eventID in the publish state.
func eventInState(eventID string, state entity.PublishState) *entity.Event {
	return &entity.Event{ID: eventID, PublishState: &state}
}

// startsAt makes the start time of eventID start on every run.
func (f *settlementFixture) startsAt(eventID string, start *time.Time) {
	f.starts.EXPECT().GetEventStartTime(mock.Anything, eventID).Return(tokyoEventStart(start), nil)
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
// Invalid splits: the entity refuses the release, the sweep skips it
// ─────────────────────────────────────────────────────────────────────────────

func TestSettlementUseCase_ReleaseDueSettlements_InvalidSplitsAreSkipped(t *testing.T) {
	t.Parallel()

	const (
		orgID             = "org-1"
		eventID           = "event-1"
		providerAccountID = "acct_test"
	)
	pastStartVal := time.Now().Add(-30 * 24 * time.Hour)
	order := &entity.Order{ID: "order-1", Amount: 10000, Currency: "JPY", Payment: entity.Payment{ProviderPaymentID: "pi_test"}}
	// The rules of a valid split are the entity's (TestSettlement_ReleasePlan).
	settlement := &entity.Settlement{ID: "settle-1", OrderID: order.ID, OrganizerID: orgID, EventID: eventID, Status: entity.SettlementStatusHeld}

	f := newSettlementFixture(t, 30*24*time.Hour, time.Now)
	f.held(settlement, order)
	f.publishedEvent(eventID)
	f.startsAt(eventID, &pastStartVal)
	f.account(&entity.PayoutAccount{OrganizerID: orgID, ProviderAccountID: providerAccountID, Status: entity.PayoutOnboardingStatusActive}, entity.PayoutOnboardingStatusActive)

	// The sweep returns nil: a per-settlement error is logged and skipped.
	require.NoError(t, f.uc.ReleaseDueSettlements(context.Background()))
	f.assertNotPaidOut(t)
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
	f.publishedEvent(eventID)
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
	f.publishedEvent("event-pending")
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
	f.publishedEvent("event-future")
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
			f.publishedEvent("event-gate")
			// Each run reads the start time again, in order.
			for _, start := range tc.startTimes {
				f.starts.EXPECT().GetEventStartTime(mock.Anything, "event-gate").Return(tokyoEventStart(start), nil).Once()
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
	f.publishedEvent("event-idem")
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

// ─────────────────────────────────────────────────────────────────────────────
// Cancelled event: the publish state is read on every run, before any payment
// provider call
// ─────────────────────────────────────────────────────────────────────────────

func TestSettlementUseCase_ReleaseDueSettlements_CancelledEvent(t *testing.T) {
	t.Parallel()

	const buffer = 7 * 24 * time.Hour
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	started := now.Add(-8 * 24 * time.Hour)
	account := &entity.PayoutAccount{
		OrganizerID: "org-cancel", ProviderAccountID: "acct_cancel",
		Status: entity.PayoutOnboardingStatusActive,
	}
	// due returns a fixture whose one Held Settlement is of an event that
	// started 8 days ago.
	due := func(t *testing.T) (*settlementFixture, *entity.Settlement) {
		t.Helper()
		settlement := &entity.Settlement{
			ID: "settle-cancel", OrderID: "order-cancel",
			OrganizerID: "org-cancel", EventID: "event-cancel",
			Splits: []entity.SettlementSplit{{PayeeOrganizerID: "org-cancel", Amount: 9000}},
			Status: entity.SettlementStatusHeld,
		}
		f := newSettlementFixture(t, buffer, func() time.Time { return now })
		f.held(settlement, &entity.Order{ID: "order-cancel", Amount: 10000, Currency: "JPY",
			Payment: entity.Payment{ProviderPaymentID: "pi_cancel"}})
		f.startsAt("event-cancel", &started)
		return f, settlement
	}

	t.Run("pay nothing for a cancelled event past the buffer", func(t *testing.T) {
		// @spec components/usecase/settlement/release-due-settlements "Cancelled event past the buffer"
		t.Parallel()
		f, settlement := due(t)
		f.events.EXPECT().Get(mock.Anything, "event-cancel").
			Return(eventInState("event-cancel", entity.PublishStateCancelled), nil).Once()
		// The Organizer's payout account is Active, and the run never reads it:
		// the strict mocks fail on any payment provider call.
		var logs bytes.Buffer
		logger, err := logging.New(logging.WithWriter(&logs))
		require.NoError(t, err)
		uc := usecase.NewSettlementUseCase(f.settlements, f.orders, f.accounts, f.events, f.starts, buffer,
			func() time.Time { return now }, logger)

		require.NoError(t, uc.ReleaseDueSettlements(context.Background()))

		f.assertNotPaidOut(t)
		assert.Equal(t, entity.SettlementStatusHeld, settlement.Status)
		assert.Contains(t, logs.String(), "payout sweep: event cancelled; payout withheld")
		assert.NotContains(t, logs.String(), "failed to release settlement", "no error is raised for the Settlement")
	})

	t.Run("pay the split when the event is cancelled after the run read it", func(t *testing.T) {
		// @spec components/usecase/settlement/release-due-settlements "Cancelled after the run read the event"
		t.Parallel()
		f, settlement := due(t)
		state := entity.PublishStatePublished
		f.events.EXPECT().Get(mock.Anything, "event-cancel").
			RunAndReturn(func(context.Context, string) (*entity.Event, error) {
				return eventInState("event-cancel", state), nil
			}).Once()
		// The cancellation is stored once the run is past the read.
		f.accounts.EXPECT().GetByOrganizerID(mock.Anything, account.OrganizerID).
			RunAndReturn(func(context.Context, string) (*entity.PayoutAccount, error) {
				state = entity.PublishStateCancelled
				return account, nil
			}).Once()
		f.accounts.EXPECT().GetStatus(mock.Anything, account.ProviderAccountID).Return(entity.PayoutOnboardingStatusActive, nil).Once()
		f.paysOut("pi_cancel", "ch_cancel", "tr_cancel")
		f.settlements.EXPECT().MarkReleased(mock.Anything, settlement.ID, "ch_cancel", now, []entity.SettlementSplit{
			{PayeeOrganizerID: "org-cancel", Amount: 9000, ProviderPayoutID: "tr_cancel"},
		}).Return(nil).Once()

		require.NoError(t, f.uc.ReleaseDueSettlements(context.Background()))
		assert.Equal(t, entity.PublishStateCancelled, state, "the event was cancelled during the run")
	})

	t.Run("leave an unrecorded payout alone once the event is cancelled", func(t *testing.T) {
		// @spec components/usecase/settlement/release-due-settlements "Cancelled before a failed release is recorded"
		t.Parallel()
		f, settlement := due(t)
		f.events.EXPECT().Get(mock.Anything, "event-cancel").
			Return(eventInState("event-cancel", entity.PublishStatePublished), nil).Once()
		f.account(account, entity.PayoutOnboardingStatusActive)
		f.paysOut("pi_cancel", "ch_cancel", "tr_cancel")
		f.settlements.EXPECT().MarkReleased(mock.Anything, settlement.ID, "ch_cancel", now, mock.Anything).
			Return(apperr.New(codes.Unavailable, "database unavailable")).Once()
		require.NoError(t, f.uc.ReleaseDueSettlements(context.Background()))

		f.events.EXPECT().Get(mock.Anything, "event-cancel").
			Return(eventInState("event-cancel", entity.PublishStateCancelled), nil).Once()
		require.NoError(t, f.uc.ReleaseDueSettlements(context.Background()))

		f.settlements.AssertNumberOfCalls(t, "CreatePayout", 1)
		f.settlements.AssertNumberOfCalls(t, "MarkReleased", 1)
		assert.Equal(t, entity.SettlementStatusHeld, settlement.Status)
	})

	t.Run("skip a settlement whose event does not exist", func(t *testing.T) {
		t.Parallel()
		f := newSettlementFixture(t, buffer, func() time.Time { return now })
		f.held(&entity.Settlement{ID: "settle-gone", OrderID: "order-gone", EventID: "event-gone", Status: entity.SettlementStatusHeld},
			&entity.Order{ID: "order-gone"})
		f.events.EXPECT().Get(mock.Anything, "event-gone").Return(nil, apperr.New(codes.NotFound, "event not found")).Once()

		require.NoError(t, f.uc.ReleaseDueSettlements(context.Background()))
		f.assertNotPaidOut(t)
	})
}
