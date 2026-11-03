package usecase_test

import (
	"context"
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
	usecasemocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

// unavailable is a payment provider that cannot be reached.
var unavailable = apperr.New(codes.Unavailable, "stripe down")

func TestOrderUseCase_Issue(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

	// capturing16000 reads back a captured 16000 yen Visa payment.
	capturing16000 := func() *stubCardAuthorizations {
		return &stubCardAuthorizations{getFn: func(_ context.Context, id string) (*entity.CardAuthorization, error) {
			return capturedCardAuthorization(id, &entity.CapturedPayment{Provider: entity.PaymentProviderStripe, AmountJPY: 16000, Currency: "JPY", CardBrand: "visa", CardLast4: "4242"}), nil
		}}
	}

	t.Run("won entry issued", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Won entry issued"
		var issuedOrder *entity.Order
		var issuedTickets []*entity.Ticket
		var issuedSettlement *entity.Settlement
		issuanceRepo, _ := recordingIssuanceRepo(&issuedOrder, &issuedTickets, &issuedSettlement)
		organizerRepo := &stubEventOrganizerRepo{getOrganizerIDFn: func(_ context.Context, eventID string) (string, error) {
			assert.Equal(t, "event-1", eventID)
			return "organizer-1", nil
		}}
		cardAuthorizations := capturing16000()
		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, eventOrganizerRepo: organizerRepo, cardAuthorizations: cardAuthorizations, clock: fixedClock(now)})

		got, err := issueEntry(uc)

		require.NoError(t, err)
		assert.Equal(t, usecase.IssueResult{OrderID: "order-entry-1", Paid: true}, got)
		assert.Equal(t, 1, cardAuthorizations.captures, "the card is charged 16000 yen once")
		require.NotNil(t, issuedOrder)
		assert.Equal(t, entity.OrderStatusPaid, issuedOrder.Status)
		assert.Equal(t, entity.OrderID("order-entry-1"), issuedOrder.ID, "the Pending Order is the one made Paid")
		assert.Equal(t, "pi_won_1", issuedOrder.Payment.ProviderPaymentID)
		assert.Equal(t, entity.PaymentProviderStripe, issuedOrder.Payment.Provider)
		assert.Equal(t, "visa", issuedOrder.Payment.CardBrand)
		assert.Equal(t, "4242", issuedOrder.Payment.CardLast4)
		require.NotNil(t, issuedOrder.PayTime)
		assert.Equal(t, now, *issuedOrder.PayTime)
		require.Len(t, issuedTickets, 2)
		for _, tk := range issuedTickets {
			assert.Equal(t, issuedOrder.ID, tk.OrderID)
			assert.Equal(t, entity.UserID("user-1"), tk.UserID)
			assert.Equal(t, "event-1", tk.EventID)
			assert.Equal(t, entity.TicketStatusIssued, tk.Status)
			assert.Equal(t, now, tk.IssueTime)
		}
		// A Held settlement with one split for the Order's amount less the 5%
		// platform fee: 16000 -> 15200 yen.
		require.NotNil(t, issuedSettlement)
		assert.Equal(t, entity.SettlementStatusHeld, issuedSettlement.Status)
		assert.Equal(t, "organizer-1", issuedSettlement.OrganizerID)
		require.Len(t, issuedSettlement.Splits, 1)
		assert.Equal(t, int64(15200), issuedSettlement.Splits[0].Amount)
	})

	t.Run("stopped after the charge", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Stopped after the charge"
		var stored *entity.Order
		issued := 0
		issuanceRepo := &stubIssuanceRepo{issueFn: func(_ context.Context, o *entity.Order, _ []*entity.Ticket, _ *entity.Settlement) error {
			issued++
			stored = o
			return nil
		}}
		cardAuthorizations := capturing16000()
		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, orderRepo: storedOrPending(&stored), cardAuthorizations: cardAuthorizations, clock: fixedClock(now)})
		flow := newReplayFlow()
		flow.stopAt = "read captured payment"
		in := usecase.IssueRequest{LotteryEntryID: "entry-1"}

		_, err := uc.Issue(context.Background(), flow, in)
		require.ErrorIs(t, err, errStopped)
		require.Equal(t, 1, cardAuthorizations.captures)
		flow.stopAt = ""
		got, err := uc.Issue(context.Background(), flow, in)

		require.NoError(t, err)
		assert.True(t, got.Paid)
		assert.Equal(t, 1, cardAuthorizations.captures, "the card is charged once")
		assert.Equal(t, 1, issued, "the Order becomes Paid once")
		assert.Equal(t, entity.OrderStatusPaid, stored.Status)
	})

	t.Run("replayed issuance", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Replayed issuance"
		paid := wonEntry().Order
		paid.Status = entity.OrderStatusPaid
		issuanceRepo, issueCalled := recordingIssuanceRepo(nil, nil, nil)
		cardAuthorizations := &stubCardAuthorizations{}
		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{
			issuanceRepo: issuanceRepo, cardAuthorizations: cardAuthorizations, clock: fixedClock(now),
			orderRepo: &stubOrderRepo{getFn: func(context.Context, entity.OrderID) (*entity.Order, error) { return paid, nil }},
		})

		got, err := issueEntry(uc)

		require.NoError(t, err)
		assert.Equal(t, usecase.IssueResult{OrderID: paid.ID, Paid: true}, got)
		assert.Zero(t, cardAuthorizations.captures, "the card is not charged again")
		assert.False(t, *issueCalled, "no Ticket or Settlement is added")
	})

	t.Run("concurrent issuance", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Concurrent issuance"
		// The other run made the Order Paid between this run's read and its
		// issuance: Order.Issue reports AlreadyExists.
		issuanceRepo := &stubIssuanceRepo{issueFn: func(context.Context, *entity.Order, []*entity.Ticket, *entity.Settlement) error {
			return apperr.New(codes.AlreadyExists, "already issued")
		}}
		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, cardAuthorizations: capturing16000(), clock: fixedClock(now)})

		got, err := issueEntry(uc)

		require.NoError(t, err)
		assert.Equal(t, usecase.IssueResult{OrderID: "order-entry-1", Paid: true}, got, "the Order Paid by the other run")
	})

	t.Run("entry not won", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Entry not won"
		for _, state := range []entity.LotteryEntryState{entity.LotteryEntryStateEntered, entity.LotteryEntryStateLost} {
			t.Run(state.String(), func(t *testing.T) {
				t.Parallel()
				entry := wonEntry()
				entry.State = state
				entries := entitymocks.NewMockLotteryEntryRepository(t)
				entries.EXPECT().Get(mock.Anything, entity.LotteryEntryID("entry-1")).Return(entry, nil)
				issuanceRepo, issueCalled := recordingIssuanceRepo(nil, nil, nil)
				cardAuthorizations := &stubCardAuthorizations{}
				uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, entryRepo: entries, cardAuthorizations: cardAuthorizations, clock: fixedClock(now)})

				_, err := issueEntry(uc)

				assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
				assert.Zero(t, cardAuthorizations.captures, "nothing is charged")
				assert.False(t, *issueCalled, "nothing changes")
			})
		}
	})

	t.Run("payment provider unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Payment provider unavailable"
		// The capture step fails with Unavailable and the workflow runtime
		// runs it again; the second attempt succeeds.
		var stored *entity.Order
		cardAuthorizations := capturing16000()
		cardAuthorizations.captureFn = func(context.Context, string) error {
			if cardAuthorizations.captures == 1 {
				return unavailable
			}
			return nil
		}
		issuanceRepo, _ := recordingIssuanceRepo(&stored, nil, nil)
		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, cardAuthorizations: cardAuthorizations, clock: fixedClock(now)})
		flow := newReplayFlow()
		in := usecase.IssueRequest{LotteryEntryID: "entry-1"}

		_, err := uc.Issue(context.Background(), flow, in)
		require.ErrorIs(t, err, apperr.ErrUnavailable, "a step the runtime tries again")
		got, err := uc.Issue(context.Background(), flow, in)

		require.NoError(t, err)
		assert.True(t, got.Paid)
		assert.Equal(t, entity.OrderStatusPaid, stored.Status)
	})

	t.Run("payment provider unavailable for a day", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Payment provider unavailable for a day"
		// Every capture fails with Unavailable until the 24-hour deadline the
		// starter sets (StartDrawnEntryWorkflow); the runtime then stops the run
		// and reports it for an admin. Nothing is reverted or issued.
		entries := entitymocks.NewMockLotteryEntryRepository(t)
		entries.EXPECT().Get(mock.Anything, entity.LotteryEntryID("entry-1")).Return(wonEntry(), nil)
		cardAuthorizations := &stubCardAuthorizations{captureFn: func(context.Context, string) error { return unavailable }}
		issuanceRepo, issueCalled := recordingIssuanceRepo(nil, nil, nil)
		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, entryRepo: entries, cardAuthorizations: cardAuthorizations, clock: fixedClock(now)})

		_, err := issueEntry(uc)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.False(t, *issueCalled, "the Order stays Pending")
		entries.AssertNotCalled(t, "RevertWin", mock.Anything, mock.Anything)
	})

	t.Run("card authorization no longer chargeable", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Card authorization no longer chargeable"
		entries := entitymocks.NewMockLotteryEntryRepository(t)
		entries.EXPECT().Get(mock.Anything, entity.LotteryEntryID("entry-1")).Return(wonEntry(), nil)
		entries.EXPECT().RevertWin(mock.Anything, entity.LotteryEntryID("entry-1")).Return(nil).Once()
		cardAuthorizations := &stubCardAuthorizations{captureFn: func(context.Context, string) error {
			return apperr.New(codes.FailedPrecondition, "card closed")
		}}
		issuanceRepo, issueCalled := recordingIssuanceRepo(nil, nil, nil)
		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, entryRepo: entries, cardAuthorizations: cardAuthorizations, clock: fixedClock(now)})

		got, err := issueEntry(uc)

		require.NoError(t, err, "a card that can no longer be charged is a result, not an error")
		assert.Equal(t, usecase.IssueResult{OrderID: "order-entry-1", Paid: false}, got)
		assert.False(t, *issueCalled, "nothing is issued")
	})

	t.Run("organizer unresolved", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Organizer unresolved"
		issuanceRepo, issueCalled := recordingIssuanceRepo(nil, nil, nil)
		organizerRepo := &stubEventOrganizerRepo{getOrganizerIDFn: func(context.Context, string) (string, error) {
			return "", apperr.New(codes.NotFound, "event's series has no organizer")
		}}
		cardAuthorizations := &stubCardAuthorizations{}
		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, eventOrganizerRepo: organizerRepo, cardAuthorizations: cardAuthorizations, clock: fixedClock(now)})

		_, err := issueEntry(uc)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Zero(t, cardAuthorizations.captures, "nothing is charged")
		assert.False(t, *issueCalled, "nothing changes")
	})

	t.Run("unknown entry", func(t *testing.T) {
		t.Parallel()
		entries := entitymocks.NewMockLotteryEntryRepository(t)
		entries.EXPECT().Get(mock.Anything, entity.LotteryEntryID("entry-1")).Return(nil, apperr.ErrNotFound)
		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{entryRepo: entries, clock: fixedClock(now)})

		_, err := issueEntry(uc)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("sale required verification", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Sale required verification"
		var issuedTickets []*entity.Ticket
		issuanceRepo, _ := recordingIssuanceRepo(nil, &issuedTickets, nil)
		identityVerifications := entitymocks.NewMockIdentityVerificationRepository(t)
		// Whatever its status: the IdentityVerification read at issuance only binds.
		identityVerifications.EXPECT().GetByUserID(mock.Anything, "user-1").
			Return(&entity.IdentityVerification{ID: "identity-verification-1", UserID: "user-1", Status: entity.VerificationStatusNeedsReverification}, nil)
		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{
			issuanceRepo: issuanceRepo, identityVerificationRepo: identityVerifications,
			saleRepo: lotterySaleRepo(t, entity.VerificationRequirementJPKIOnly), clock: fixedClock(now),
		})

		_, err := issueEntry(uc)

		require.NoError(t, err)
		require.Len(t, issuedTickets, 2)
		for _, tk := range issuedTickets {
			assert.Equal(t, "identity-verification-1", tk.IdentityVerificationID)
			face := tk.Face(&entity.User{ID: "user-1", FullName: "山田 太郎", PhoneNumber: "+819012345678"})
			assert.Equal(t, "山田 太郎", face.FullName, "the face shows the user's own full name")
		}
	})

	t.Run("identity verification missing", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Identity verification missing"
		issuanceRepo, issueCalled := recordingIssuanceRepo(nil, nil, nil)
		identityVerifications := entitymocks.NewMockIdentityVerificationRepository(t)
		identityVerifications.EXPECT().GetByUserID(mock.Anything, "user-1").Return(nil, apperr.ErrNotFound)
		cardAuthorizations := &stubCardAuthorizations{}
		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{
			issuanceRepo: issuanceRepo, identityVerificationRepo: identityVerifications, cardAuthorizations: cardAuthorizations,
			saleRepo: lotterySaleRepo(t, entity.VerificationRequirementVerifiedAny), clock: fixedClock(now),
		})

		_, err := issueEntry(uc)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Zero(t, cardAuthorizations.captures, "nothing is charged")
		assert.False(t, *issueCalled)
	})

	t.Run("no requirement", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "No requirement"
		var issuedTickets []*entity.Ticket
		issuanceRepo, _ := recordingIssuanceRepo(nil, &issuedTickets, nil)
		identityVerifications := entitymocks.NewMockIdentityVerificationRepository(t) // no expectation: GetByUserID is never called
		uc := newLotteryIssuanceUC(t, lotteryIssuanceDeps{issuanceRepo: issuanceRepo, identityVerificationRepo: identityVerifications, clock: fixedClock(now)})

		_, err := issueEntry(uc)

		require.NoError(t, err)
		require.Len(t, issuedTickets, 2)
		for _, tk := range issuedTickets {
			assert.Empty(t, tk.IdentityVerificationID)
		}
	})
}

// releaseFixture is an OrderUseCase over mocked Order and card authorization
// repositories, for ReleaseCardAuthorization.
type releaseFixture struct {
	orders             *entitymocks.MockOrderRepository
	cardAuthorizations *entitymocks.MockCardAuthorizationRepository
	uc                 usecase.OrderUseCase
}

func newReleaseFixture(t *testing.T) *releaseFixture {
	t.Helper()
	f := &releaseFixture{
		orders:             entitymocks.NewMockOrderRepository(t),
		cardAuthorizations: entitymocks.NewMockCardAuthorizationRepository(t),
	}
	f.uc = usecase.NewOrderUseCase(usecase.OrderDeps{
		OrderRepo: f.orders, CardAuthorizations: f.cardAuthorizations,
		Clock: time.Now, Logger: newTestLogger(t),
	})
	return f
}

// order returns order-1 in the given status, held by card authorization ref.
func (f *releaseFixture) order(status entity.OrderStatus, ref string) {
	f.orders.EXPECT().Get(mock.Anything, entity.OrderID("order-1")).
		Return(&entity.Order{ID: "order-1", Status: status, CardAuthorizationID: ref}, nil)
}

func TestOrderUseCase_ReleaseCardAuthorization(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("loser released", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/release-card-authorization "Loser released"
		f := newReleaseFixture(t)
		f.order(entity.OrderStatusVoid, "pi_lost")
		f.cardAuthorizations.EXPECT().Release(mock.Anything, "pi_lost").Return(nil).Once()

		_, err := f.uc.ReleaseCardAuthorization(ctx, &inlineFlow{}, "order-1")

		require.NoError(t, err)
		f.cardAuthorizations.AssertNotCalled(t, "Capture", mock.Anything, mock.Anything)
	})

	t.Run("win reverted", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/release-card-authorization "Win reverted"
		// RevertWin made the Order Void and announced the entry Lost, which
		// starts this workflow for the Order (StartDrawnEntryWorkflow).
		f := newReleaseFixture(t)
		f.order(entity.OrderStatusVoid, "pi_reverted")
		f.cardAuthorizations.EXPECT().Release(mock.Anything, "pi_reverted").Return(nil).Once()

		_, err := f.uc.ReleaseCardAuthorization(ctx, &inlineFlow{}, "order-1")

		require.NoError(t, err)
	})

	t.Run("order not void", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/release-card-authorization "Order not Void"
		f := newReleaseFixture(t)
		f.order(entity.OrderStatusPending, "pi_pending")

		_, err := f.uc.ReleaseCardAuthorization(ctx, &inlineFlow{}, "order-1")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		f.cardAuthorizations.AssertNotCalled(t, "Release", mock.Anything, mock.Anything)
	})

	t.Run("payment provider unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/release-card-authorization "Payment provider unavailable"
		f := newReleaseFixture(t)
		f.order(entity.OrderStatusVoid, "pi_lost")
		f.cardAuthorizations.EXPECT().Release(mock.Anything, "pi_lost").Return(unavailable).Once()
		f.cardAuthorizations.EXPECT().Release(mock.Anything, "pi_lost").Return(nil).Once()
		flow := newReplayFlow()

		_, err := f.uc.ReleaseCardAuthorization(ctx, flow, "order-1")
		require.ErrorIs(t, err, apperr.ErrUnavailable, "a step the runtime tries again")
		_, err = f.uc.ReleaseCardAuthorization(ctx, flow, "order-1")

		require.NoError(t, err, "the card authorization is Released")
	})

	t.Run("payment provider unavailable for a day", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/release-card-authorization "Payment provider unavailable for a day"
		// Every release fails with Unavailable until the 24-hour deadline the
		// starter sets; the runtime then stops the run and reports it.
		f := newReleaseFixture(t)
		f.order(entity.OrderStatusVoid, "pi_lost")
		f.cardAuthorizations.EXPECT().Release(mock.Anything, "pi_lost").Return(unavailable)

		_, err := f.uc.ReleaseCardAuthorization(ctx, &inlineFlow{}, "order-1")

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	t.Run("charged card authorization", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/release-card-authorization "Charged card authorization"
		// The failure ends the workflow with an error, which the runtime
		// reports to an admin.
		f := newReleaseFixture(t)
		f.order(entity.OrderStatusVoid, "pi_captured")
		f.cardAuthorizations.EXPECT().Release(mock.Anything, "pi_captured").Return(apperr.New(codes.FailedPrecondition, "captured"))

		_, err := f.uc.ReleaseCardAuthorization(ctx, &inlineFlow{}, "order-1")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("order removed", func(t *testing.T) {
		t.Parallel()
		// A delete removed the Void Order while its release was due: the
		// workflow ends with NotFound, which the runtime reports to an admin,
		// and nothing is released.
		f := newReleaseFixture(t)
		f.orders.EXPECT().Get(mock.Anything, entity.OrderID("order-1")).Return(nil, apperr.New(codes.NotFound, "order removed"))

		_, err := f.uc.ReleaseCardAuthorization(ctx, &inlineFlow{}, "order-1")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
		f.cardAuthorizations.AssertNotCalled(t, "Release", mock.Anything, mock.Anything)
	})

	t.Run("no card authorization", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/release-card-authorization "No card authorization"
		f := newReleaseFixture(t)
		f.order(entity.OrderStatusVoid, "")

		_, err := f.uc.ReleaseCardAuthorization(ctx, &inlineFlow{}, "order-1")

		require.NoError(t, err)
		f.cardAuthorizations.AssertNotCalled(t, "Release", mock.Anything, mock.Anything)
	})
}

func TestOrderUseCase_StartDrawnEntryWorkflow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	newUC := func(t *testing.T) (usecase.OrderUseCase, *usecasemocks.MockWorkflows) {
		t.Helper()
		workflows := usecasemocks.NewMockWorkflows(t)
		return usecase.NewOrderUseCase(usecase.OrderDeps{Workflows: workflows, Clock: fixedClock(now), Logger: newTestLogger(t)}), workflows
	}
	// options decodes the start options a call was given.
	options := func(call mock.Call) usecase.StartOptions {
		var opts []usecase.StartOption
		for _, a := range call.Arguments[4:] {
			opts = append(opts, a.(usecase.StartOption))
		}
		return usecase.NewStartOptions(opts...)
	}
	entry := func(state entity.LotteryEntryState) *entity.LotteryEntry {
		return &entity.LotteryEntry{ID: "entry-1", OrderID: "order-1", TicketSaleID: "sale-l", State: state}
	}

	t.Run("won entry", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/issue "Won entry"
		uc, workflows := newUC(t)
		workflows.EXPECT().Start(mock.Anything, usecase.WorkflowIssue, "issue:order-1", usecase.IssueRequest{LotteryEntryID: "entry-1"}, mock.Anything, mock.Anything).Return(nil).Once()

		require.NoError(t, uc.StartDrawnEntryWorkflow(ctx, entry(entity.LotteryEntryStateWon)))

		opts := options(workflows.Calls[0])
		assert.Equal(t, usecase.PaymentQueue, opts.Queue)
		assert.Equal(t, now.Add(24*time.Hour), opts.Deadline, "it stops 24 hours after it started")
	})

	t.Run("lost entry", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/order/release-card-authorization "Loser released"
		uc, workflows := newUC(t)
		workflows.EXPECT().Start(mock.Anything, usecase.WorkflowReleaseCardAuthorization, "release:order-1", entity.OrderID("order-1"), mock.Anything, mock.Anything).Return(nil).Once()

		require.NoError(t, uc.StartDrawnEntryWorkflow(ctx, entry(entity.LotteryEntryStateLost)))

		opts := options(workflows.Calls[0])
		assert.Equal(t, usecase.PaymentQueue, opts.Queue)
		assert.Equal(t, now.Add(24*time.Hour), opts.Deadline)
	})

	t.Run("entered entry", func(t *testing.T) {
		t.Parallel()
		uc, _ := newUC(t)

		require.NoError(t, uc.StartDrawnEntryWorkflow(ctx, entry(entity.LotteryEntryStateEntered)))
	})

	t.Run("start fails", func(t *testing.T) {
		t.Parallel()
		uc, workflows := newUC(t)
		workflows.EXPECT().Start(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(unavailable)

		assert.ErrorIs(t, uc.StartDrawnEntryWorkflow(ctx, entry(entity.LotteryEntryStateWon)), apperr.ErrUnavailable)
	})
}
