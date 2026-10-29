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
)

// ticketStockFixture holds the mocks of a TicketStockUseCase under test.
type ticketStockFixture struct {
	stocks         *entitymocks.MockTicketStockRepository
	seatTypes      *entitymocks.MockSeatTypeRepository
	events         *entitymocks.MockEventRepository
	series         *entitymocks.MockSeriesRepository
	eventOrganizer *stubEventOrganizerRepo
	uc             usecase.TicketStockUseCase
}

var stockNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func newTicketStockFixture(t *testing.T, owner string) *ticketStockFixture {
	t.Helper()
	f := &ticketStockFixture{
		stocks:    entitymocks.NewMockTicketStockRepository(t),
		seatTypes: entitymocks.NewMockSeatTypeRepository(t),
		events:    entitymocks.NewMockEventRepository(t),
		series:    entitymocks.NewMockSeriesRepository(t),
		eventOrganizer: &stubEventOrganizerRepo{getOrganizerIDFn: func(_ context.Context, eventID string) (string, error) {
			if eventID != "event-1" {
				return "", apperr.ErrNotFound
			}
			return owner, nil
		}},
	}
	f.uc = usecase.NewTicketStockUseCase(f.stocks, f.seatTypes, f.events, f.series, f.eventOrganizer, fixedClock(stockNow))
	return f
}

// ownEvent makes event-1 of series-1 readable, in the publish state, with
// series-1 owned by owner.
func (f *ticketStockFixture) ownEvent(owner string, state entity.PublishState) {
	f.events.EXPECT().Get(mock.Anything, "event-1").Return(&entity.Event{ID: "event-1", SeriesID: "series-1", PublishState: &state}, nil)
	f.series.EXPECT().GetAuthored(mock.Anything, "series-1").Return(&entity.Series{ID: "series-1", OrganizerID: &owner}, nil, nil)
}

// seriesSeatTypes makes series-1 have S席.
func (f *ticketStockFixture) seriesSeatTypes() {
	f.seatTypes.EXPECT().ListBySeries(mock.Anything, "series-1").
		Return([]*entity.SeatType{{ID: "seat-s", SeriesID: "series-1", Name: "S席", Price: 8000}}, nil)
}

func TestTicketStockUseCase_Create(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("two days at one venue", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-stock/create "Two days at one venue"
		f := newTicketStockFixture(t, "org-1")
		day2 := entity.PublishStatePublished
		f.events.EXPECT().Get(mock.Anything, "event-1").Return(&entity.Event{ID: "event-1", SeriesID: "series-1", PublishState: &day2}, nil)
		f.events.EXPECT().Get(mock.Anything, "event-2").Return(&entity.Event{ID: "event-2", SeriesID: "series-1", PublishState: &day2}, nil)
		owner := "org-1"
		f.series.EXPECT().GetAuthored(mock.Anything, "series-1").Return(&entity.Series{ID: "series-1", OrganizerID: &owner}, nil, nil)
		f.seatTypes.EXPECT().ListBySeries(mock.Anything, "series-1").
			Return([]*entity.SeatType{{ID: "seat-s", SeriesID: "series-1", Name: "S席", Price: 8000}}, nil)
		var stored []*entity.TicketStock
		f.stocks.EXPECT().Create(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, s *entity.TicketStock) (*entity.TicketStock, error) {
			stored = append(stored, s)
			return s, nil
		})

		_, err := f.uc.Create(ctx, "org-1", "event-1", "seat-s", 150)
		require.NoError(t, err)
		_, err = f.uc.Create(ctx, "org-1", "event-2", "seat-s", 150)
		require.NoError(t, err)

		require.Len(t, stored, 2)
		assert.Equal(t, "event-1", stored[0].EventID)
		assert.Equal(t, "event-2", stored[1].EventID)
		for _, s := range stored {
			assert.Equal(t, entity.SeatTypeID("seat-s"), s.SeatTypeID)
			assert.Equal(t, 150, s.Quantity)
		}
	})

	t.Run("seat type of another series", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-stock/create "Seat type of another series"
		f := newTicketStockFixture(t, "org-1")
		f.ownEvent("org-1", entity.PublishStatePublished)
		f.seriesSeatTypes()

		_, err := f.uc.Create(ctx, "org-1", "event-1", "seat-of-other-series", 150)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		f.stocks.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})

	t.Run("quantity of 0", func(t *testing.T) {
		t.Parallel()
		f := newTicketStockFixture(t, "org-1")
		f.ownEvent("org-1", entity.PublishStateDraft)
		f.seriesSeatTypes()

		_, err := f.uc.Create(ctx, "org-1", "event-1", "seat-s", 0)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("same seat type twice", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-stock/create "Same seat type twice"
		f := newTicketStockFixture(t, "org-1")
		f.ownEvent("org-1", entity.PublishStatePublished)
		f.seriesSeatTypes()
		f.stocks.EXPECT().Create(mock.Anything, mock.Anything).Return(nil, apperr.New(codes.AlreadyExists, "already stocked"))

		_, err := f.uc.Create(ctx, "org-1", "event-1", "seat-s", 150)

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
	})

	t.Run("cancelled event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-stock/create "Cancelled event"
		f := newTicketStockFixture(t, "org-1")
		f.ownEvent("org-1", entity.PublishStateCancelled)
		f.seriesSeatTypes()

		_, err := f.uc.Create(ctx, "org-1", "event-1", "seat-s", 150)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		f.stocks.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})

	t.Run("another organizer's event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-stock/create "Another Organizer's event"
		f := newTicketStockFixture(t, "org-2")
		f.ownEvent("org-2", entity.PublishStatePublished)
		f.events.EXPECT().Get(mock.Anything, "missing").Return(nil, apperr.ErrNotFound)

		_, err := f.uc.Create(ctx, "org-1", "event-1", "seat-s", 150)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		_, err = f.uc.Create(ctx, "org-1", "missing", "seat-s", 150)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied, "does not reveal whether it exists")
	})
}

func TestTicketStockUseCase_Delete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	stock := &entity.TicketStock{ID: "stock-1", EventID: "event-1", SeatTypeID: "seat-s", Quantity: 150}

	t.Run("mistaken quantity before the sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-stock/delete "Mistaken quantity before the sale"
		f := newTicketStockFixture(t, "org-1")
		f.stocks.EXPECT().Get(mock.Anything, stock.ID, stockNow).Return(stock, nil)
		f.ownEvent("org-1", entity.PublishStatePublished)
		f.stocks.EXPECT().Delete(mock.Anything, stock.ID).Return(nil)

		require.NoError(t, f.uc.Delete(ctx, "org-1", stock.ID))
	})

	t.Run("fans already applied", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-stock/delete "Fans already applied"
		// TicketStock.Delete refuses the delete; the usecase returns it
		// unchanged.
		f := newTicketStockFixture(t, "org-1")
		f.stocks.EXPECT().Get(mock.Anything, stock.ID, stockNow).Return(stock, nil)
		f.ownEvent("org-1", entity.PublishStatePublished)
		f.stocks.EXPECT().Delete(mock.Anything, stock.ID).Return(apperr.New(codes.FailedPrecondition, "an order names the stock"))

		assert.ErrorIs(t, f.uc.Delete(ctx, "org-1", stock.ID), apperr.ErrFailedPrecondition)
	})

	t.Run("another organizer's stock", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-stock/delete "Another Organizer's stock"
		f := newTicketStockFixture(t, "org-2")
		f.stocks.EXPECT().Get(mock.Anything, stock.ID, stockNow).Return(stock, nil)
		f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("missing"), stockNow).Return(nil, apperr.ErrNotFound)
		f.ownEvent("org-2", entity.PublishStatePublished)

		assert.ErrorIs(t, f.uc.Delete(ctx, "org-1", stock.ID), apperr.ErrPermissionDenied)
		assert.ErrorIs(t, f.uc.Delete(ctx, "org-1", "missing"), apperr.ErrPermissionDenied, "does not reveal whether it exists")
		f.stocks.AssertNotCalled(t, "Delete", mock.Anything, mock.Anything)
	})
}

func TestTicketStockUseCase_ListByEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("stocked event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-stock/list-by-event "Stocked event"
		f := newTicketStockFixture(t, "org-1")
		stock := &entity.TicketStock{ID: "stock-1", EventID: "event-1", SeatTypeID: "seat-s", Quantity: 150, SoldCount: 100, HeldCount: 6}
		f.stocks.EXPECT().ListByEvent(mock.Anything, "event-1", stockNow).Return([]*entity.TicketStock{stock}, nil)

		got, err := f.uc.ListByEvent(ctx, "org-1", "event-1")

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, 150, got[0].Quantity)
		assert.Equal(t, 100, got[0].SoldCount)
		assert.Equal(t, 6, got[0].HeldCount)
	})

	t.Run("not stocked yet", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-stock/list-by-event "Not stocked yet"
		f := newTicketStockFixture(t, "org-1")
		f.stocks.EXPECT().ListByEvent(mock.Anything, "event-1", stockNow).Return([]*entity.TicketStock{}, nil)

		got, err := f.uc.ListByEvent(ctx, "org-1", "event-1")

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("another organizer's event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-stock/list-by-event "Another Organizer's event"
		f := newTicketStockFixture(t, "org-2")

		_, err := f.uc.ListByEvent(ctx, "org-1", "event-1")
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		_, err = f.uc.ListByEvent(ctx, "org-1", "missing")
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied, "does not reveal whether it exists")
	})
}
