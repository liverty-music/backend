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

// ticketTypeFixture holds the mocks of a TicketTypeUseCase under test.
type ticketTypeFixture struct {
	sales  *entitymocks.MockTicketSaleRepository
	series *entitymocks.MockSeriesRepository
	uc     usecase.TicketTypeUseCase
}

func newTicketTypeFixture(t *testing.T, now time.Time) *ticketTypeFixture {
	t.Helper()
	f := &ticketTypeFixture{
		sales:  entitymocks.NewMockTicketSaleRepository(t),
		series: entitymocks.NewMockSeriesRepository(t),
	}
	f.uc = usecase.NewTicketTypeUseCase(f.sales, f.series, fixedClock(now))
	return f
}

// owns makes series-1 the given Organizer's.
func (f *ticketTypeFixture) owns(organizerID string) {
	f.series.EXPECT().GetAuthored(mock.Anything, "series-1").
		Return(&entity.Series{ID: "series-1", OrganizerID: &organizerID}, nil, nil, nil).Maybe()
}

// reads makes TicketType.Get return the sale's first TicketType.
func (f *ticketTypeFixture) reads(sale *entity.TicketSale) {
	f.sales.EXPECT().GetTicketType(mock.Anything, sale.TicketTypes[0].ID, mock.Anything).Return(sale, sale.TicketTypes[0], nil)
}

func TestTicketTypeUseCase_Update(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	lottery := func(quantity int) *entity.TicketSale {
		sale := entity.NewTicketSale("series-1", "ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 10, 0), saleTime(11, 10, 10, 0))
		entity.NewTicketType(sale, "event-1", 8000, quantity, 4)
		return sale
	}
	firstCome := func(sold, held int) *entity.TicketSale {
		sale := entity.NewTicketSale("series-1", "一般発売", entity.TicketSaleMethodFirstCome, saleTime(11, 1, 10, 0), saleTime(11, 20, 19, 0))
		tt := entity.NewTicketType(sale, "event-1", 3000, 150, 4)
		tt.SoldCount, tt.HeldCount = sold, held
		return sale
	}
	// updates makes TicketType.Update return the TicketType it is given.
	updates := func(f *ticketTypeFixture, sale *entity.TicketSale) {
		f.sales.EXPECT().UpdateTicketType(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
			func(_ context.Context, tt *entity.TicketType, _ time.Time) (*entity.TicketSale, *entity.TicketType, error) {
				return sale, tt, nil
			})
	}

	t.Run("another organizer's ticket type", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-type/update "Another Organizer's ticket type"
		f := newTicketTypeFixture(t, saleTime(10, 25, 12, 0))
		sale := lottery(100)
		f.reads(sale)
		f.owns("org-2")

		_, err := f.uc.Update(ctx, "org-1", usecase.UpdateTicketTypeInput{TicketTypeID: sale.TicketTypes[0].ID, Price: 8000, Quantity: 120, PerAccountLimit: 4})

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("unknown ticket type", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-type/update "Unknown ticket type"
		f := newTicketTypeFixture(t, saleTime(10, 25, 12, 0))
		f.sales.EXPECT().GetTicketType(mock.Anything, entity.TicketTypeID("missing"), mock.Anything).Return(nil, nil, apperr.ErrNotFound)

		_, err := f.uc.Update(ctx, "org-1", usecase.UpdateTicketTypeInput{TicketTypeID: "missing", Price: 8000, Quantity: 120, PerAccountLimit: 4})

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("lottery ticket type before the sale opens", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-type/update "Lottery ticket type before the sale opens"
		f := newTicketTypeFixture(t, saleTime(10, 25, 12, 0))
		sale := lottery(100)
		f.reads(sale)
		f.owns("org-1")
		updates(f, sale)

		got, err := f.uc.Update(ctx, "org-1", usecase.UpdateTicketTypeInput{TicketTypeID: sale.TicketTypes[0].ID, Price: 8000, Quantity: 120, PerAccountLimit: 4})

		require.NoError(t, err)
		assert.Equal(t, 120, got.TicketType.Quantity)
		assert.Equal(t, 100, sale.TicketTypes[0].Quantity, "the read TicketType is not changed in place")
	})

	t.Run("lottery sale already open", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-type/update "Lottery sale already open"
		f := newTicketTypeFixture(t, saleTime(11, 3, 12, 0))
		sale := lottery(100)
		f.reads(sale)
		f.owns("org-1")

		_, err := f.uc.Update(ctx, "org-1", usecase.UpdateTicketTypeInput{TicketTypeID: sale.TicketTypes[0].ID, Price: 8000, Quantity: 120, PerAccountLimit: 4})

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("more tickets later", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-type/update "More tickets later"
		f := newTicketTypeFixture(t, saleTime(11, 3, 12, 0))
		sale := firstCome(100, 6)
		f.reads(sale)
		f.owns("org-1")
		updates(f, sale)

		got, err := f.uc.Update(ctx, "org-1", usecase.UpdateTicketTypeInput{TicketTypeID: sale.TicketTypes[0].ID, Price: 3000, Quantity: 180, PerAccountLimit: 4})

		require.NoError(t, err)
		assert.Equal(t, 180, got.TicketType.Quantity)
		assert.Equal(t, entity.TicketTypeStateOnSale, got.State)
	})

	t.Run("price while tickets are held", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-type/update "Price while tickets are held"
		f := newTicketTypeFixture(t, saleTime(11, 3, 12, 0))
		sale := firstCome(0, 2)
		f.reads(sale)
		f.owns("org-1")
		locked := apperr.New(codes.FailedPrecondition, "the price cannot change while tickets are sold or held")
		f.sales.EXPECT().UpdateTicketType(mock.Anything, mock.MatchedBy(func(tt *entity.TicketType) bool { return tt.Price == 3500 }), mock.Anything).
			Return(nil, nil, locked)

		_, err := f.uc.Update(ctx, "org-1", usecase.UpdateTicketTypeInput{TicketTypeID: sale.TicketTypes[0].ID, Price: 3500, Quantity: 150, PerAccountLimit: 4})

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Equal(t, locked, err, "the error of TicketType.Update is returned unchanged")
	})
}
