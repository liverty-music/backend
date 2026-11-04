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

var (
	reJST = time.FixedZone("JST", 9*60*60)
	// reNow is when the fixtures' fan applies: 15:00 on 5 November.
	reNow = time.Date(2026, 11, 5, 15, 0, 0, 0, reJST)
	// reStart is when the fixtures' event starts: 19:00 on 10 November.
	reStart = time.Date(2026, 11, 10, 19, 0, 0, 0, reJST)
	// reSeatS is the S席 seat type at 5000 yen.
	reSeatS = &entity.SeatType{ID: "seat-s", SeriesID: "series-1", Name: "S席", Price: 5000}
	reSeatA = &entity.SeatType{ID: "seat-a", SeriesID: "series-1", Name: "A席", Price: 3000}
)

// resaleEntryFixture holds the mocks of ResaleEntryUseCase under test.
type resaleEntryFixture struct {
	entries              *entitymocks.MockResaleEntryRepository
	stocks               *entitymocks.MockTicketStockRepository
	sales                *entitymocks.MockTicketSaleRepository
	orders               *entitymocks.MockOrderRepository
	events               *entitymocks.MockEventRepository
	eventState           *usecasemocks.MockEventPublishStatePort
	identityVerification *entitymocks.MockIdentityVerificationRepository
	users                *entitymocks.MockUserRepository
	cardAuthorizations   *entitymocks.MockCardAuthorizationRepository
	uc                   usecase.ResaleEntryUseCase
}

func newResaleEntryFixture(t *testing.T) *resaleEntryFixture {
	t.Helper()
	f := &resaleEntryFixture{
		entries:              entitymocks.NewMockResaleEntryRepository(t),
		stocks:               entitymocks.NewMockTicketStockRepository(t),
		sales:                entitymocks.NewMockTicketSaleRepository(t),
		orders:               entitymocks.NewMockOrderRepository(t),
		events:               entitymocks.NewMockEventRepository(t),
		eventState:           usecasemocks.NewMockEventPublishStatePort(t),
		identityVerification: entitymocks.NewMockIdentityVerificationRepository(t),
		users:                entitymocks.NewMockUserRepository(t),
		cardAuthorizations:   entitymocks.NewMockCardAuthorizationRepository(t),
	}
	f.uc = usecase.NewResaleEntryUseCase(usecase.ResaleEntryDeps{
		EntryRepo:                f.entries,
		StockRepo:                f.stocks,
		SaleRepo:                 f.sales,
		OrderRepo:                f.orders,
		EventRepo:                f.events,
		EventState:               f.eventState,
		IdentityVerificationRepo: f.identityVerification,
		UserRepo:                 f.users,
		CardAuthorizations:       f.cardAuthorizations,
		Clock:                    fixedClock(reNow),
		Logger:                   newTestLogger(t),
	})
	return f
}

// soldOutStock returns the stock of seat as the repository reads it: sold out
// when soldOut.
func soldOutStock(id entity.TicketStockID, seat *entity.SeatType, soldOut bool) *entity.TicketStock {
	sold := 9
	if soldOut {
		sold = 10
	}
	return &entity.TicketStock{ID: id, EventID: "event-1", SeatTypeID: seat.ID, SeatType: seat, Quantity: 10, SoldCount: sold}
}

func resaleEvent() *entity.Event {
	limit, start := 4, reStart
	return &entity.Event{ID: "event-1", PerAccountLimit: &limit, StartTime: &start}
}

func TestResaleEntryUseCase_AuthorizeCard(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("card authorized", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-entry/authorize-card "Card authorized"
		f := newResaleEntryFixture(t)
		f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-s"), reNow).Return(soldOutStock("stock-s", reSeatS, true), nil)
		f.events.EXPECT().Get(mock.Anything, "event-1").Return(resaleEvent(), nil)
		f.eventState.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.orders.EXPECT().GetHeldForEvent(mock.Anything, entity.UserID("fan-1"), "event-1", (*entity.OrderID)(nil), reNow).Return(entity.HeldTickets{}, nil)
		f.sales.EXPECT().ListByEvent(mock.Anything, "event-1").Return([]*entity.TicketSale{{ID: "sale-1"}}, nil)
		f.users.EXPECT().UpdatePersonalDetails(mock.Anything, "fan-1", "山田 太郎", "+819012345678").Return(&entity.User{}, nil)
		f.cardAuthorizations.EXPECT().Create(mock.Anything, entity.CardAuthorizationPurchase{
			Purpose: entity.CardAuthorizationPurposeResaleEntry, UserID: "fan-1", TicketStockID: "stock-s", EventID: "event-1", TicketCount: 2,
		}, int64(10000)).Return(&entity.CardAuthorization{ID: "ca-1", Amount: 10000}, nil)

		got, err := f.uc.AuthorizeCard(ctx, "stock-s", "fan-1", 2, "山田 太郎", "+819012345678", nil)

		require.NoError(t, err)
		assert.Equal(t, int64(10000), got.Amount)
	})

	t.Run("not sold out", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-entry/authorize-card "Not sold out"
		f := newResaleEntryFixture(t)
		f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-s"), reNow).Return(soldOutStock("stock-s", reSeatS, false), nil)
		f.events.EXPECT().Get(mock.Anything, "event-1").Return(resaleEvent(), nil)

		_, err := f.uc.AuthorizeCard(ctx, "stock-s", "fan-1", 2, "山田 太郎", "+819012345678", nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("another seat type held", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-entry/authorize-card "Another seat type held"
		f := newResaleEntryFixture(t)
		f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-a"), reNow).Return(soldOutStock("stock-a", reSeatA, true), nil)
		f.events.EXPECT().Get(mock.Anything, "event-1").Return(resaleEvent(), nil)
		f.eventState.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		seatS := reSeatS.ID
		f.orders.EXPECT().GetHeldForEvent(mock.Anything, entity.UserID("fan-1"), "event-1", (*entity.OrderID)(nil), reNow).
			Return(entity.HeldTickets{Count: 2, SeatTypeID: &seatS}, nil)

		_, err := f.uc.AuthorizeCard(ctx, "stock-a", "fan-1", 1, "山田 太郎", "+819012345678", nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("verification required", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-entry/authorize-card "Verification required"
		f := newResaleEntryFixture(t)
		f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-s"), reNow).Return(soldOutStock("stock-s", reSeatS, true), nil)
		f.events.EXPECT().Get(mock.Anything, "event-1").Return(resaleEvent(), nil)
		f.eventState.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.orders.EXPECT().GetHeldForEvent(mock.Anything, entity.UserID("fan-1"), "event-1", (*entity.OrderID)(nil), reNow).Return(entity.HeldTickets{}, nil)
		f.sales.EXPECT().ListByEvent(mock.Anything, "event-1").Return([]*entity.TicketSale{
			{ID: "sale-1"}, {ID: "sale-2", VerificationRequirement: entity.VerificationRequirementVerifiedAny},
		}, nil)
		f.identityVerification.EXPECT().GetByUserID(mock.Anything, "fan-1").Return(nil, apperr.New(codes.NotFound, "none"))

		_, err := f.uc.AuthorizeCard(ctx, "stock-s", "fan-1", 1, "山田 太郎", "+819012345678", nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("concert cancelled", func(t *testing.T) {
		t.Parallel()
		f := newResaleEntryFixture(t)
		f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-s"), reNow).Return(soldOutStock("stock-s", reSeatS, true), nil)
		f.events.EXPECT().Get(mock.Anything, "event-1").Return(resaleEvent(), nil)
		f.eventState.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(false, nil)

		_, err := f.uc.AuthorizeCard(ctx, "stock-s", "fan-1", 1, "山田 太郎", "+819012345678", nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})
}

// resaleAuthorization returns an Authorized resale card authorization of
// fan-1 for count S席 tickets.
func resaleAuthorization(count int, status entity.CardAuthorizationStatus) *entity.CardAuthorization {
	expire := reNow.Add(30 * 24 * time.Hour)
	return &entity.CardAuthorization{
		ID: "ca-1", Amount: reSeatS.AmountFor(count), Status: status, ExpireTime: &expire,
		Purchase: entity.CardAuthorizationPurchase{
			Purpose: entity.CardAuthorizationPurposeResaleEntry, UserID: "fan-1", TicketStockID: "stock-s", EventID: "event-1", TicketCount: count,
		},
	}
}

func TestResaleEntryUseCase_Confirm(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fan := entity.UserID("fan-1")

	t.Run("entry made", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-entry/confirm "Entry made"
		f := newResaleEntryFixture(t)
		ca := resaleAuthorization(2, entity.CardAuthorizationStatusAuthorized)
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "ca-1").Return(ca, nil)
		f.entries.EXPECT().GetByCardAuthorizationID(mock.Anything, "ca-1").Return(nil, apperr.New(codes.NotFound, "none"))
		f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-s"), reNow).Return(soldOutStock("stock-s", reSeatS, true), nil)
		f.entries.EXPECT().Create(mock.Anything, mock.MatchedBy(func(e *entity.ResaleEntry) bool {
			return e.State == entity.ResaleEntryStateEntered && e.Order.TicketCount == 2 && e.Order.CardAuthorizationID == "ca-1" &&
				e.CardAuthorizationExpireTime != nil && e.CardAuthorizationExpireTime.Equal(*ca.ExpireTime)
		})).RunAndReturn(func(_ context.Context, e *entity.ResaleEntry) (*entity.ResaleEntry, error) { return e, nil })

		got, err := f.uc.Confirm(ctx, "ca-1", &fan)

		require.NoError(t, err)
		assert.Equal(t, entity.ResaleEntryStateEntered, got.State)
		assert.Equal(t, 2, got.Order.TicketCount)
	})

	t.Run("stock no longer sold out", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-entry/confirm "Stock no longer sold out"
		f := newResaleEntryFixture(t)
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "ca-1").Return(resaleAuthorization(2, entity.CardAuthorizationStatusAuthorized), nil)
		f.entries.EXPECT().GetByCardAuthorizationID(mock.Anything, "ca-1").Return(nil, apperr.New(codes.NotFound, "none"))
		f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-s"), reNow).Return(soldOutStock("stock-s", reSeatS, false), nil)
		f.entries.EXPECT().Create(mock.Anything, mock.Anything).Return(nil, apperr.New(codes.FailedPrecondition, "resale is not open"))
		f.cardAuthorizations.EXPECT().Release(mock.Anything, "ca-1").Return(nil).Once()

		_, err := f.uc.Confirm(ctx, "ca-1", &fan)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("fan and notice both confirm", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-entry/confirm "Fan and notice both confirm"
		f := newResaleEntryFixture(t)
		existing := &entity.ResaleEntry{ID: "entry-1", State: entity.ResaleEntryStateEntered, Order: &entity.Order{CardAuthorizationID: "ca-1"}}
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "ca-1").Return(resaleAuthorization(2, entity.CardAuthorizationStatusAuthorized), nil).Twice()
		f.entries.EXPECT().GetByCardAuthorizationID(mock.Anything, "ca-1").Return(existing, nil).Twice()

		byFan, err := f.uc.Confirm(ctx, "ca-1", &fan)
		require.NoError(t, err)
		byNotice, err := f.uc.Confirm(ctx, "ca-1", nil)
		require.NoError(t, err)

		assert.Equal(t, existing.ID, byFan.ID)
		assert.Equal(t, existing.ID, byNotice.ID)
	})

	t.Run("card authentication not finished", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-entry/confirm "Card authentication not finished"
		f := newResaleEntryFixture(t)
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "ca-1").Return(resaleAuthorization(2, entity.CardAuthorizationStatusPending), nil)
		f.entries.EXPECT().GetByCardAuthorizationID(mock.Anything, "ca-1").Return(nil, apperr.New(codes.NotFound, "none"))

		_, err := f.uc.Confirm(ctx, "ca-1", &fan)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("second entry for the event", func(t *testing.T) {
		t.Parallel()
		f := newResaleEntryFixture(t)
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "ca-1").Return(resaleAuthorization(1, entity.CardAuthorizationStatusAuthorized), nil)
		f.entries.EXPECT().GetByCardAuthorizationID(mock.Anything, "ca-1").Return(nil, apperr.New(codes.NotFound, "none"))
		f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-s"), reNow).Return(soldOutStock("stock-s", reSeatS, true), nil)
		f.entries.EXPECT().Create(mock.Anything, mock.Anything).Return(nil, apperr.New(codes.AlreadyExists, "entered"))
		f.cardAuthorizations.EXPECT().Release(mock.Anything, "ca-1").Return(nil).Once()

		_, err := f.uc.Confirm(ctx, "ca-1", &fan)

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists, "returned unchanged")
	})

	t.Run("another fan's card authorization", func(t *testing.T) {
		t.Parallel()
		f := newResaleEntryFixture(t)
		other := entity.UserID("fan-2")
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "ca-1").Return(resaleAuthorization(1, entity.CardAuthorizationStatusAuthorized), nil)

		_, err := f.uc.Confirm(ctx, "ca-1", &other)

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})
}

func TestResaleEntryUseCase_List(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("entered entry", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-entry/list "Entered entry"
		f := newResaleEntryFixture(t)
		entry := &entity.ResaleEntry{ID: "entry-1", State: entity.ResaleEntryStateEntered, Order: &entity.Order{TicketStockID: "stock-s", TicketCount: 2}}
		f.entries.EXPECT().ListByUser(mock.Anything, entity.UserID("fan-1")).Return([]*entity.ResaleEntry{entry}, nil)
		f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-s"), reNow).Return(soldOutStock("stock-s", reSeatS, true), nil)
		f.events.EXPECT().Get(mock.Anything, "event-1").Return(resaleEvent(), nil)

		got, err := f.uc.List(ctx, "fan-1")

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, reSeatS, got[0].Stock.SeatType)
		require.NotNil(t, got[0].NextDrawTime)
		assert.True(t, got[0].NextDrawTime.Equal(time.Date(2026, 11, 6, 12, 0, 0, 0, reJST)), "12:00 on 6 November")
	})

	t.Run("no entries", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-entry/list "No entries"
		f := newResaleEntryFixture(t)
		f.entries.EXPECT().ListByUser(mock.Anything, entity.UserID("fan-1")).Return([]*entity.ResaleEntry{}, nil)

		got, err := f.uc.List(ctx, "fan-1")

		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestResaleEntryUseCase_Withdraw(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mine := func(state entity.ResaleEntryState) *entity.ResaleEntry {
		return &entity.ResaleEntry{ID: "entry-1", State: state, Order: &entity.Order{ID: "order-1", UserID: "fan-1", TicketCount: 2, Status: entity.OrderStatusPending}}
	}

	t.Run("fan withdraws", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-entry/withdraw "Fan withdraws"
		f := newResaleEntryFixture(t)
		f.entries.EXPECT().Get(mock.Anything, entity.ResaleEntryID("entry-1")).Return(mine(entity.ResaleEntryStateEntered), nil)
		withdrawn := mine(entity.ResaleEntryStateWithdrawn)
		withdrawn.Order.Status = entity.OrderStatusVoid
		f.entries.EXPECT().Withdraw(mock.Anything, entity.ResaleEntryID("entry-1"), reNow).Return(withdrawn, nil)

		got, err := f.uc.Withdraw(ctx, "fan-1", "entry-1")

		require.NoError(t, err)
		assert.Equal(t, entity.ResaleEntryStateWithdrawn, got.State)
		assert.Equal(t, entity.OrderStatusVoid, got.Order.Status)
		// The card authorization is released from the withdrawal's
		// announcement (workflow starter), never by Withdraw itself.
		f.cardAuthorizations.AssertNotCalled(t, "Release", mock.Anything, mock.Anything)
	})

	t.Run("another fan's entry", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-entry/withdraw "Another fan's entry"
		f := newResaleEntryFixture(t)
		f.entries.EXPECT().Get(mock.Anything, entity.ResaleEntryID("entry-1")).Return(mine(entity.ResaleEntryStateEntered), nil)

		_, err := f.uc.Withdraw(ctx, "fan-2", "entry-1")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("already drawn", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-entry/withdraw "Already drawn"
		f := newResaleEntryFixture(t)
		f.entries.EXPECT().Get(mock.Anything, entity.ResaleEntryID("entry-1")).Return(mine(entity.ResaleEntryStateWon), nil)
		f.entries.EXPECT().Withdraw(mock.Anything, entity.ResaleEntryID("entry-1"), reNow).Return(nil, apperr.New(codes.FailedPrecondition, "drawn"))

		_, err := f.uc.Withdraw(ctx, "fan-1", "entry-1")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("payment provider unreachable", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-entry/withdraw "Payment provider unreachable"
		// Withdraw never calls the payment provider: the entry is Withdrawn
		// whether or not the release, started from the announcement, can
		// reach it, and that release retries (OrderUseCase.ReleaseCardAuthorization).
		f := newResaleEntryFixture(t)
		f.entries.EXPECT().Get(mock.Anything, entity.ResaleEntryID("entry-1")).Return(mine(entity.ResaleEntryStateEntered), nil)
		f.entries.EXPECT().Withdraw(mock.Anything, entity.ResaleEntryID("entry-1"), reNow).Return(mine(entity.ResaleEntryStateWithdrawn), nil)

		got, err := f.uc.Withdraw(ctx, "fan-1", "entry-1")

		require.NoError(t, err)
		assert.Equal(t, entity.ResaleEntryStateWithdrawn, got.State)
		f.cardAuthorizations.AssertNotCalled(t, "Release", mock.Anything, mock.Anything)
	})
}
