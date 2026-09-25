package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- self-contained test doubles for ⑤ issuance ports ---
//
// Same rationale as lottery_uc_test.go: mockery v2.53.6 cannot load
// internal/entity (the `image without types` bug), so these function-field
// stubs verify the issuance business logic. stubPhaseRepo, stubAppRepo, and
// stubVerifiedIdentityRepo are reused from lottery_uc_test.go (same package).

type stubIssuanceRepo struct {
	issueFn        func(ctx context.Context, order *entity.Order, tickets []*entity.Ticket, settlement *entity.Settlement) error
	listAwaitingFn func(ctx context.Context) ([]entity.TicketApplicationID, error)
}

func (s *stubIssuanceRepo) Issue(ctx context.Context, order *entity.Order, tickets []*entity.Ticket, settlement *entity.Settlement) error {
	if s.issueFn != nil {
		return s.issueFn(ctx, order, tickets, settlement)
	}
	return nil
}

func (s *stubIssuanceRepo) ListApplicationIDsAwaitingIssuance(ctx context.Context) ([]entity.TicketApplicationID, error) {
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
	getFn                   func(ctx context.Context, id entity.OrderID) (*entity.Order, error)
	getByApplicationIDFn    func(ctx context.Context, applicationID entity.TicketApplicationID) (*entity.Order, error)
	getByPaymentIntentRefFn func(ctx context.Context, piRef string) (*entity.Order, error)
	updateStatusFn          func(ctx context.Context, id entity.OrderID, status entity.OrderStatus) error
}

func (s *stubOrderRepo) Get(ctx context.Context, id entity.OrderID) (*entity.Order, error) {
	if s.getFn != nil {
		return s.getFn(ctx, id)
	}
	return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
}

func (s *stubOrderRepo) GetByApplicationID(ctx context.Context, applicationID entity.TicketApplicationID) (*entity.Order, error) {
	if s.getByApplicationIDFn != nil {
		return s.getByApplicationIDFn(ctx, applicationID)
	}
	return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
}

func (s *stubOrderRepo) GetByPaymentIntentRef(ctx context.Context, piRef string) (*entity.Order, error) {
	if s.getByPaymentIntentRefFn != nil {
		return s.getByPaymentIntentRefFn(ctx, piRef)
	}
	return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
}

func (s *stubOrderRepo) UpdateStatus(ctx context.Context, id entity.OrderID, status entity.OrderStatus) error {
	if s.updateStatusFn != nil {
		return s.updateStatusFn(ctx, id, status)
	}
	return nil
}

type stubJourneyRepo struct {
	upsertFn func(ctx context.Context, journey *entity.TicketJourney) error
}

func (s *stubJourneyRepo) Get(ctx context.Context, userID, eventID string) (*entity.TicketJourney, error) {
	return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
}

func (s *stubJourneyRepo) Upsert(ctx context.Context, journey *entity.TicketJourney) error {
	if s.upsertFn != nil {
		return s.upsertFn(ctx, journey)
	}
	return nil
}

func (s *stubJourneyRepo) Delete(ctx context.Context, userID, eventID string) error { return nil }

func (s *stubJourneyRepo) ListByUser(ctx context.Context, userID string) ([]*entity.TicketJourney, error) {
	return nil, nil
}

func (s *stubJourneyRepo) ListUserIDsTrackingSeries(ctx context.Context, seriesID string) ([]string, error) {
	return nil, nil
}

type stubCapturePort struct {
	getCapturedPaymentFn func(ctx context.Context, paymentIntentRef string) (*usecase.CapturedPayment, error)
}

func (s *stubCapturePort) GetCapturedPayment(ctx context.Context, paymentIntentRef string) (*usecase.CapturedPayment, error) {
	if s.getCapturedPaymentFn != nil {
		return s.getCapturedPaymentFn(ctx, paymentIntentRef)
	}
	return &usecase.CapturedPayment{
		Provider:  entity.PaymentProviderStripe,
		AmountJPY: 10000,
		Currency:  "JPY",
		CardBrand: "visa",
		CardLast4: "4242",
	}, nil
}

// wonApplication returns a Won-captured application for 2 tickets on phase-1.
func wonApplication() *entity.TicketApplication {
	return &entity.TicketApplication{
		ID:                   "app-1",
		PhaseID:              "phase-1",
		ApplicantID:          "user-1",
		RequestedTicketCount: 2,
		Identity:             entity.ApplicantIdentity{FullName: "山田 太郎", PhoneNumber: "+818000000000"},
		Authorization:        entity.PaymentAuthorization{PaymentIntentRef: "pi_won_1"},
		State:                entity.TicketApplicationStateWon,
	}
}

func TestIssuanceUseCase_IssueFromCapturedWin(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

	// @spec components/usecase/order/issue-from-captured-win "Won application issued"
	t.Run("creates a paid Order, issues N covered tickets, and sets journey PAID", func(t *testing.T) {
		t.Parallel()

		var issuedOrder *entity.Order
		var issuedTickets []*entity.Ticket
		var issuedSettlement *entity.Settlement
		var journeyUpsert *entity.TicketJourney

		issuanceRepo := &stubIssuanceRepo{issueFn: func(_ context.Context, o *entity.Order, ts []*entity.Ticket, s *entity.Settlement) error {
			issuedOrder, issuedTickets, issuedSettlement = o, ts, s
			return nil
		}}
		journeyRepo := &stubJourneyRepo{upsertFn: func(_ context.Context, j *entity.TicketJourney) error {
			journeyUpsert = j
			return nil
		}}
		appRepo := &stubAppRepo{getFn: func(_ context.Context, _ entity.TicketApplicationID) (*entity.TicketApplication, error) {
			return wonApplication(), nil
		}}
		phaseRepo := &stubPhaseRepo{getFn: func(_ context.Context, _ entity.LotteryPhaseID) (*entity.LotterySalesPhase, error) {
			return basePhase(now.Add(-48*time.Hour), now.Add(-24*time.Hour)), nil
		}}
		organizerRepo := &stubEventOrganizerRepo{getOrganizerIDFn: func(_ context.Context, eventID string) (string, error) {
			assert.Equal(t, "event-1", eventID)
			return "organizer-1", nil
		}}

		uc := usecase.NewIssuanceUseCase(issuanceRepo, &stubOrderRepo{}, appRepo, phaseRepo, organizerRepo,
			&stubVerifiedIdentityRepo{}, journeyRepo, &stubCapturePort{}, fixedClock(now), newTestLogger(t))

		order, err := uc.IssueFromCapturedWin(context.Background(), "app-1")
		require.NoError(t, err)

		// Order is paid, references the captured payment, carries facets, never card data.
		require.NotNil(t, order)
		assert.Equal(t, entity.OrderStatusPaid, order.Status)
		assert.Equal(t, entity.UserID("user-1"), order.BuyerID)
		assert.Equal(t, entity.TicketApplicationID("app-1"), order.ApplicationID)
		assert.Equal(t, "pi_won_1", order.Payment.PaymentIntentRef)
		assert.Equal(t, entity.PaymentProviderStripe, order.Payment.Provider)
		assert.Equal(t, "visa", order.Payment.CardBrand)
		assert.Equal(t, "4242", order.Payment.CardLast4)
		assert.Equal(t, int64(10000), order.Amount)
		assert.Equal(t, "JPY", order.Currency)
		assert.Equal(t, now, order.PaidTime)
		assert.Same(t, issuedOrder, order)

		// Exactly N covered tickets, all bound to the buyer with the three conditions.
		require.Len(t, issuedTickets, 2)
		for _, tk := range issuedTickets {
			assert.Equal(t, order.ID, tk.OrderID)
			assert.Equal(t, entity.UserID("user-1"), tk.HolderID)
			assert.Equal(t, "event-1", tk.EventID)
			assert.True(t, tk.ResaleWithoutConsentProhibited)
			assert.Equal(t, entity.TicketStatusIssued, tk.Status)
			assert.Equal(t, "山田 太郎", tk.HolderIdentity.FullName)
			assert.Empty(t, tk.VerifiedIdentityID) // phase required no verification
		}

		// Journey set to PAID for the buyer + event.
		require.NotNil(t, journeyUpsert)
		assert.Equal(t, "user-1", journeyUpsert.UserID)
		assert.Equal(t, "event-1", journeyUpsert.EventID)
		assert.Equal(t, entity.TicketJourneyStatusPaid, journeyUpsert.Status)

		// A Held settlement for the event's Organizer, with one split for the
		// Order's full amount (backend#468: the payout sweeper needs this row
		// to have anything to release).
		require.NotNil(t, issuedSettlement)
		assert.Equal(t, order.ID, issuedSettlement.OrderID)
		assert.Equal(t, "organizer-1", issuedSettlement.OrganizerID)
		assert.Equal(t, "event-1", issuedSettlement.EventID)
		assert.Equal(t, entity.SettlementStatusHeld, issuedSettlement.Status)
		assert.Equal(t, now, issuedSettlement.CreatedTime)
		require.Len(t, issuedSettlement.Splits, 1)
		assert.Equal(t, "organizer-1", issuedSettlement.Splits[0].PayeeOrganizerID)
		assert.Equal(t, order.Amount, issuedSettlement.Splits[0].Amount)
	})

	// @spec components/usecase/order/issue-from-captured-win "Replayed issuance"
	t.Run("is idempotent: an existing Order is returned without re-issuing", func(t *testing.T) {
		t.Parallel()

		existing := &entity.Order{ID: "order-existing", ApplicationID: "app-1", Status: entity.OrderStatusPaid}
		orderRepo := &stubOrderRepo{getByApplicationIDFn: func(_ context.Context, _ entity.TicketApplicationID) (*entity.Order, error) {
			return existing, nil
		}}
		issueCalled := false
		issuanceRepo := &stubIssuanceRepo{issueFn: func(_ context.Context, _ *entity.Order, _ []*entity.Ticket, _ *entity.Settlement) error {
			issueCalled = true
			return nil
		}}
		journeyCalled := false
		journeyRepo := &stubJourneyRepo{upsertFn: func(_ context.Context, _ *entity.TicketJourney) error {
			journeyCalled = true
			return nil
		}}

		uc := usecase.NewIssuanceUseCase(issuanceRepo, orderRepo, &stubAppRepo{}, &stubPhaseRepo{}, &stubEventOrganizerRepo{},
			&stubVerifiedIdentityRepo{}, journeyRepo, &stubCapturePort{}, fixedClock(now), newTestLogger(t))

		order, err := uc.IssueFromCapturedWin(context.Background(), "app-1")
		require.NoError(t, err)
		assert.Same(t, existing, order)
		assert.False(t, issueCalled, "must not re-issue when an Order already exists")
		assert.False(t, journeyCalled, "must not re-write journey on an idempotent replay")
	})

	// @spec components/usecase/order/issue-from-captured-win "Application not won"
	t.Run("returns FailedPrecondition and creates no Order when the application is not Won-captured", func(t *testing.T) {
		t.Parallel()

		appRepo := &stubAppRepo{getFn: func(_ context.Context, _ entity.TicketApplicationID) (*entity.TicketApplication, error) {
			app := wonApplication()
			app.State = entity.TicketApplicationStateLost
			return app, nil
		}}
		issueCalled := false
		issuanceRepo := &stubIssuanceRepo{issueFn: func(_ context.Context, _ *entity.Order, _ []*entity.Ticket, _ *entity.Settlement) error {
			issueCalled = true
			return nil
		}}

		uc := usecase.NewIssuanceUseCase(issuanceRepo, &stubOrderRepo{}, appRepo, &stubPhaseRepo{}, &stubEventOrganizerRepo{},
			&stubVerifiedIdentityRepo{}, &stubJourneyRepo{}, &stubCapturePort{}, fixedClock(now), newTestLogger(t))

		_, err := uc.IssueFromCapturedWin(context.Background(), "app-1")
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.False(t, issueCalled)
	})

	t.Run("binds the verified identity when the phase required verification", func(t *testing.T) {
		t.Parallel()

		var issuedTickets []*entity.Ticket
		issuanceRepo := &stubIssuanceRepo{issueFn: func(_ context.Context, _ *entity.Order, ts []*entity.Ticket, _ *entity.Settlement) error {
			issuedTickets = ts
			return nil
		}}
		appRepo := &stubAppRepo{getFn: func(_ context.Context, _ entity.TicketApplicationID) (*entity.TicketApplication, error) {
			return wonApplication(), nil
		}}
		phaseRepo := &stubPhaseRepo{getFn: func(_ context.Context, _ entity.LotteryPhaseID) (*entity.LotterySalesPhase, error) {
			p := basePhase(now.Add(-48*time.Hour), now.Add(-24*time.Hour))
			p.VerificationRequirement = entity.VerificationRequirementJPKIOnly
			return p, nil
		}}
		viRepo := &stubVerifiedIdentityRepo{getByUserIDFn: func(_ context.Context, _ string) (*entity.VerifiedIdentity, error) {
			return &entity.VerifiedIdentity{ID: "vi-1", UserID: "user-1", Status: entity.VerificationStatusActive}, nil
		}}

		uc := usecase.NewIssuanceUseCase(issuanceRepo, &stubOrderRepo{}, appRepo, phaseRepo, &stubEventOrganizerRepo{},
			viRepo, &stubJourneyRepo{}, &stubCapturePort{}, fixedClock(now), newTestLogger(t))

		_, err := uc.IssueFromCapturedWin(context.Background(), "app-1")
		require.NoError(t, err)
		require.Len(t, issuedTickets, 2)
		for _, tk := range issuedTickets {
			assert.Equal(t, "vi-1", tk.VerifiedIdentityID)
		}
	})

	// @spec components/usecase/order/issue-from-captured-win "Concurrent issuance"
	t.Run("on a concurrent issuance race it re-reads and returns the winning Order", func(t *testing.T) {
		t.Parallel()

		raceOrder := &entity.Order{ID: "order-race", ApplicationID: "app-1", Status: entity.OrderStatusPaid}
		// First GetByApplicationID (idempotency check) → NotFound; second (after the
		// AlreadyExists race) → the order the concurrent writer created.
		calls := 0
		orderRepo := &stubOrderRepo{getByApplicationIDFn: func(_ context.Context, _ entity.TicketApplicationID) (*entity.Order, error) {
			calls++
			if calls == 1 {
				return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
			}
			return raceOrder, nil
		}}
		issuanceRepo := &stubIssuanceRepo{issueFn: func(_ context.Context, _ *entity.Order, _ []*entity.Ticket, _ *entity.Settlement) error {
			return apperr.New(apperr.ErrAlreadyExists.Code, "duplicate application")
		}}
		appRepo := &stubAppRepo{getFn: func(_ context.Context, _ entity.TicketApplicationID) (*entity.TicketApplication, error) {
			return wonApplication(), nil
		}}
		phaseRepo := &stubPhaseRepo{getFn: func(_ context.Context, _ entity.LotteryPhaseID) (*entity.LotterySalesPhase, error) {
			return basePhase(now.Add(-48*time.Hour), now.Add(-24*time.Hour)), nil
		}}

		uc := usecase.NewIssuanceUseCase(issuanceRepo, orderRepo, appRepo, phaseRepo, &stubEventOrganizerRepo{},
			&stubVerifiedIdentityRepo{}, &stubJourneyRepo{}, &stubCapturePort{}, fixedClock(now), newTestLogger(t))

		order, err := uc.IssueFromCapturedWin(context.Background(), "app-1")
		require.NoError(t, err)
		assert.Same(t, raceOrder, order)
	})

	// @spec components/usecase/order/issue-from-captured-win "Organizer unresolved"
	t.Run("fails with NotFound and creates nothing when the event's Organizer cannot be resolved", func(t *testing.T) {
		t.Parallel()

		issueCalled := false
		issuanceRepo := &stubIssuanceRepo{issueFn: func(_ context.Context, _ *entity.Order, _ []*entity.Ticket, _ *entity.Settlement) error {
			issueCalled = true
			return nil
		}}
		appRepo := &stubAppRepo{getFn: func(_ context.Context, _ entity.TicketApplicationID) (*entity.TicketApplication, error) {
			return wonApplication(), nil
		}}
		phaseRepo := &stubPhaseRepo{getFn: func(_ context.Context, _ entity.LotteryPhaseID) (*entity.LotterySalesPhase, error) {
			return basePhase(now.Add(-48*time.Hour), now.Add(-24*time.Hour)), nil
		}}
		organizerRepo := &stubEventOrganizerRepo{getOrganizerIDFn: func(_ context.Context, _ string) (string, error) {
			return "", apperr.New(apperr.ErrNotFound.Code, "event's series has no organizer")
		}}

		uc := usecase.NewIssuanceUseCase(issuanceRepo, &stubOrderRepo{}, appRepo, phaseRepo, organizerRepo,
			&stubVerifiedIdentityRepo{}, &stubJourneyRepo{}, &stubCapturePort{}, fixedClock(now), newTestLogger(t))

		_, err := uc.IssueFromCapturedWin(context.Background(), "app-1")
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.False(t, issueCalled, "must not issue when the Organizer cannot be resolved")
	})

	// @spec components/usecase/order/issue-from-captured-win "Payment not captured"
	t.Run("returns FailedPrecondition and creates nothing when the payment was not captured", func(t *testing.T) {
		t.Parallel()

		issueCalled := false
		issuanceRepo := &stubIssuanceRepo{issueFn: func(_ context.Context, _ *entity.Order, _ []*entity.Ticket, _ *entity.Settlement) error {
			issueCalled = true
			return nil
		}}
		appRepo := &stubAppRepo{getFn: func(_ context.Context, _ entity.TicketApplicationID) (*entity.TicketApplication, error) {
			return wonApplication(), nil
		}}
		phaseRepo := &stubPhaseRepo{getFn: func(_ context.Context, _ entity.LotteryPhaseID) (*entity.LotterySalesPhase, error) {
			return basePhase(now.Add(-48*time.Hour), now.Add(-24*time.Hour)), nil
		}}
		capturePort := &stubCapturePort{getCapturedPaymentFn: func(_ context.Context, _ string) (*usecase.CapturedPayment, error) {
			return nil, apperr.New(apperr.ErrFailedPrecondition.Code, "payment intent is not captured")
		}}

		uc := usecase.NewIssuanceUseCase(issuanceRepo, &stubOrderRepo{}, appRepo, phaseRepo, &stubEventOrganizerRepo{},
			&stubVerifiedIdentityRepo{}, &stubJourneyRepo{}, capturePort, fixedClock(now), newTestLogger(t))

		_, err := uc.IssueFromCapturedWin(context.Background(), "app-1")
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.False(t, issueCalled, "must not issue when the payment was not captured")
	})
}

// TestIssuanceUseCase_ThroughSettlementRelease follows an Order end to end:
// issuance creates the Held Settlement (backend#468), and the payout sweeper
// — reusing the settlement_uc_test.go stubs, since both live in package
// usecase_test — later releases it once the event has started and the
// dispute buffer has passed. This is the regression test for the bug: before
// this change, PayoutSweeperUseCase.ListHeld was always empty because nothing
// ever created a Settlement.
func TestIssuanceUseCase_ThroughSettlementRelease(t *testing.T) {
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
	appRepo := &stubAppRepo{getFn: func(_ context.Context, _ entity.TicketApplicationID) (*entity.TicketApplication, error) {
		return wonApplication(), nil
	}}
	phaseRepo := &stubPhaseRepo{getFn: func(_ context.Context, _ entity.LotteryPhaseID) (*entity.LotterySalesPhase, error) {
		return basePhase(now.Add(-48*time.Hour), now.Add(-24*time.Hour)), nil
	}}
	organizerRepo := &stubEventOrganizerRepo{getOrganizerIDFn: func(_ context.Context, _ string) (string, error) {
		return "organizer-1", nil
	}}

	issuanceUC := usecase.NewIssuanceUseCase(issuanceRepo, orderRepo, appRepo, phaseRepo, organizerRepo,
		&stubVerifiedIdentityRepo{}, &stubJourneyRepo{}, &stubCapturePort{}, fixedClock(now), newTestLogger(t))

	order, err := issuanceUC.IssueFromCapturedWin(context.Background(), "app-1")
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
	connectedAccountRepo := &stubConnectedAccountRepo{getByOrganizerIDFn: func(_ context.Context, organizerID string) (*entity.OrganizerConnectedAccount, error) {
		return &entity.OrganizerConnectedAccount{OrganizerID: organizerID, Status: entity.PayoutOnboardingStatusActive}, nil
	}}

	sweeperUC := usecase.NewPayoutSweeperUseCase(settlementRepo, orderRepo, connectedAccountRepo, eventStartTimeRepo,
		&stubPaymentSettlementPort{}, 7*24*time.Hour, fixedClock(now), newTestLogger(t))

	require.NoError(t, sweeperUC.ReleaseDueSettlements(context.Background()))
	require.NotNil(t, released, "the settlement issuance created must be released once due")
	require.Len(t, releasedSplits, 1)
	assert.Equal(t, "organizer-1", releasedSplits[0].PayeeOrganizerID)
	assert.Equal(t, order.Amount, releasedSplits[0].Amount)
}

func TestIssuanceUseCase_IssueDueWins(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

	t.Run("issues every awaiting application and continues past a single failure", func(t *testing.T) {
		t.Parallel()

		issued := map[entity.TicketApplicationID]bool{}
		issuanceRepo := &stubIssuanceRepo{
			listAwaitingFn: func(_ context.Context) ([]entity.TicketApplicationID, error) {
				return []entity.TicketApplicationID{"app-1", "app-bad", "app-2"}, nil
			},
			issueFn: func(_ context.Context, o *entity.Order, _ []*entity.Ticket, _ *entity.Settlement) error {
				issued[o.ApplicationID] = true
				return nil
			},
		}
		appRepo := &stubAppRepo{getFn: func(_ context.Context, id entity.TicketApplicationID) (*entity.TicketApplication, error) {
			// app-bad fails to load so its issuance errors; the sweep must continue.
			if id == "app-bad" {
				return nil, apperr.New(apperr.ErrNotFound.Code, "gone")
			}
			app := wonApplication()
			app.ID = id
			return app, nil
		}}
		phaseRepo := &stubPhaseRepo{getFn: func(_ context.Context, _ entity.LotteryPhaseID) (*entity.LotterySalesPhase, error) {
			return basePhase(now.Add(-48*time.Hour), now.Add(-24*time.Hour)), nil
		}}

		uc := usecase.NewIssuanceUseCase(issuanceRepo, &stubOrderRepo{}, appRepo, phaseRepo, &stubEventOrganizerRepo{},
			&stubVerifiedIdentityRepo{}, &stubJourneyRepo{}, &stubCapturePort{}, fixedClock(now), newTestLogger(t))

		err := uc.IssueDueWins(context.Background())
		require.NoError(t, err)
		assert.True(t, issued["app-1"])
		assert.True(t, issued["app-2"])
		assert.False(t, issued["app-bad"], "a failed application must not be issued")
	})
}

// TestIssuanceUseCase_OrganizerPayoutReadinessNeverBlocksSale follows the
// win-tickets-in-a-lottery story's payout-readiness requirement: an Organizer
// still completing identity checks never blocks the sale — the fan is still
// charged and issued Tickets — only the Organizer's own payout stays Held
// until the account is Active.
//
// @spec stories/win-tickets-in-a-lottery "Organizer still in identity check"
func TestIssuanceUseCase_OrganizerPayoutReadinessNeverBlocksSale(t *testing.T) {
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
	appRepo := &stubAppRepo{getFn: func(_ context.Context, _ entity.TicketApplicationID) (*entity.TicketApplication, error) {
		return wonApplication(), nil
	}}
	phaseRepo := &stubPhaseRepo{getFn: func(_ context.Context, _ entity.LotteryPhaseID) (*entity.LotterySalesPhase, error) {
		return basePhase(now.Add(-48*time.Hour), now.Add(-24*time.Hour)), nil
	}}
	organizerRepo := &stubEventOrganizerRepo{getOrganizerIDFn: func(_ context.Context, _ string) (string, error) {
		return "organizer-pending", nil
	}}

	issuanceUC := usecase.NewIssuanceUseCase(issuanceRepo, orderRepo, appRepo, phaseRepo, organizerRepo,
		&stubVerifiedIdentityRepo{}, &stubJourneyRepo{}, &stubCapturePort{}, fixedClock(now), newTestLogger(t))

	// -- the fan is charged and holds Issued Tickets regardless of the
	//    Organizer's payout readiness. --
	order, err := issuanceUC.IssueFromCapturedWin(context.Background(), "app-1")
	require.NoError(t, err)
	assert.Equal(t, entity.OrderStatusPaid, order.Status)
	require.Len(t, storedTickets, 2)
	for _, tk := range storedTickets {
		assert.Equal(t, entity.TicketStatusIssued, tk.Status)
	}
	require.NotNil(t, storedSettlement)
	assert.Equal(t, entity.SettlementStatusHeld, storedSettlement.Status)

	// -- the event started 8 days ago (past the dispute buffer), but the
	//    Organizer's connected account is still Pending (identity check in
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
	connectedAccountRepo := &stubConnectedAccountRepo{getByOrganizerIDFn: func(_ context.Context, organizerID string) (*entity.OrganizerConnectedAccount, error) {
		return &entity.OrganizerConnectedAccount{OrganizerID: organizerID, Status: entity.PayoutOnboardingStatusPending}, nil
	}}
	settlementPort := &stubPaymentSettlementPort{
		getAccountStatusFn: func(_ context.Context, _ string) (entity.PayoutOnboardingStatus, error) {
			return entity.PayoutOnboardingStatusPending, nil
		},
		createTransferFn: func(_ context.Context, _ usecase.TransferParams) (string, error) {
			transferCalled = true
			return "tr_should_not_happen", nil
		},
	}

	sweeperUC := usecase.NewPayoutSweeperUseCase(settlementRepo, orderRepo, connectedAccountRepo, eventStartTimeRepo,
		settlementPort, 7*24*time.Hour, fixedClock(now), newTestLogger(t))

	require.NoError(t, sweeperUC.ReleaseDueSettlements(context.Background()))
	assert.False(t, transferCalled, "the Organizer's payout must stay Held while the account is Pending")
	assert.False(t, markReleasedCalled, "the settlement must not be marked Released while the account is Pending")
	assert.Equal(t, entity.SettlementStatusHeld, storedSettlement.Status, "the settlement itself is untouched")
}
