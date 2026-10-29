package usecase_test

import (
	"context"
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
)

// seatTypeFixture holds the mocks of a SeatTypeUseCase under test.
type seatTypeFixture struct {
	seatTypes *entitymocks.MockSeatTypeRepository
	series    *entitymocks.MockSeriesRepository
	uc        usecase.SeatTypeUseCase
}

func newSeatTypeFixture(t *testing.T) *seatTypeFixture {
	t.Helper()
	f := &seatTypeFixture{
		seatTypes: entitymocks.NewMockSeatTypeRepository(t),
		series:    entitymocks.NewMockSeriesRepository(t),
	}
	f.uc = usecase.NewSeatTypeUseCase(f.seatTypes, f.series)
	return f
}

// seriesOf makes series-1 owned by organizerID.
func (f *seatTypeFixture) seriesOf(organizerID string) {
	f.series.EXPECT().GetAuthored(mock.Anything, "series-1").
		Return(&entity.Series{ID: "series-1", OrganizerID: &organizerID}, nil, nil)
}

func TestSeatTypeUseCase_Create(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("owner adds S席", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/seat-type/create "Owner adds S席"
		f := newSeatTypeFixture(t)
		f.seriesOf("org-1")
		f.seatTypes.EXPECT().Create(mock.Anything, mock.MatchedBy(func(s *entity.SeatType) bool {
			return s.SeriesID == "series-1" && s.Name == "S席" && s.Price == 8000 && s.ID != ""
		})).RunAndReturn(func(_ context.Context, s *entity.SeatType) (*entity.SeatType, error) { return s, nil })

		got, err := f.uc.Create(ctx, "org-1", "series-1", "S席", 8000)

		require.NoError(t, err)
		assert.Equal(t, "S席", got.Name)
	})

	t.Run("another organizer's series", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/seat-type/create "Another Organizer's series"
		f := newSeatTypeFixture(t)
		f.seriesOf("org-2")

		_, err := f.uc.Create(ctx, "org-1", "series-1", "S席", 8000)

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		f.seatTypes.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})

	t.Run("unknown series is not revealed", func(t *testing.T) {
		t.Parallel()
		f := newSeatTypeFixture(t)
		f.series.EXPECT().GetAuthored(mock.Anything, "series-1").Return(nil, nil, apperr.ErrNotFound)

		_, err := f.uc.Create(ctx, "org-1", "series-1", "S席", 8000)

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("same name twice", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/seat-type/create "Same name twice"
		f := newSeatTypeFixture(t)
		f.seriesOf("org-1")
		f.seatTypes.EXPECT().Create(mock.Anything, mock.Anything).Return(nil, apperr.New(codes.AlreadyExists, "S席 exists"))

		_, err := f.uc.Create(ctx, "org-1", "series-1", "S席", 9000)

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
	})

	t.Run("price out of range", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/seat-type/create "Price out of range"
		f := newSeatTypeFixture(t)
		f.seriesOf("org-1")

		_, err := f.uc.Create(ctx, "org-1", "series-1", "S席", 0)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		f.seatTypes.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})
}

func TestSeatTypeUseCase_Delete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	seatS := &entity.SeatType{ID: "seat-s", SeriesID: "series-1", Name: "S席", Price: 8000}

	t.Run("mistaken seat type", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/seat-type/delete "Mistaken seat type"
		f := newSeatTypeFixture(t)
		f.seatTypes.EXPECT().Get(mock.Anything, seatS.ID).Return(seatS, nil)
		f.seriesOf("org-1")
		f.seatTypes.EXPECT().Delete(mock.Anything, seatS.ID).Return(nil)

		require.NoError(t, f.uc.Delete(ctx, "org-1", seatS.ID))
	})

	t.Run("seat type in stock", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/seat-type/delete "Seat type in stock"
		f := newSeatTypeFixture(t)
		f.seatTypes.EXPECT().Get(mock.Anything, seatS.ID).Return(seatS, nil)
		f.seriesOf("org-1")
		f.seatTypes.EXPECT().Delete(mock.Anything, seatS.ID).Return(apperr.New(codes.FailedPrecondition, "a stock uses it"))

		assert.ErrorIs(t, f.uc.Delete(ctx, "org-1", seatS.ID), apperr.ErrFailedPrecondition)
	})

	t.Run("another organizer's seat type", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/seat-type/delete "Another Organizer's seat type"
		f := newSeatTypeFixture(t)
		f.seatTypes.EXPECT().Get(mock.Anything, seatS.ID).Return(seatS, nil)
		f.seatTypes.EXPECT().Get(mock.Anything, entity.SeatTypeID("missing")).Return(nil, apperr.ErrNotFound)
		f.seriesOf("org-2")

		assert.ErrorIs(t, f.uc.Delete(ctx, "org-1", seatS.ID), apperr.ErrPermissionDenied)
		assert.ErrorIs(t, f.uc.Delete(ctx, "org-1", "missing"), apperr.ErrPermissionDenied, "does not reveal whether it exists")
		f.seatTypes.AssertNotCalled(t, "Delete", mock.Anything, mock.Anything)
	})
}

func TestSeatTypeUseCase_ListBySeries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("two seat types", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/seat-type/list-by-series "Two seat types"
		f := newSeatTypeFixture(t)
		f.seriesOf("org-1")
		types := []*entity.SeatType{
			{ID: "seat-s", SeriesID: "series-1", Name: "S席", Price: 8000},
			{ID: "seat-a", SeriesID: "series-1", Name: "A席", Price: 6000},
		}
		f.seatTypes.EXPECT().ListBySeries(mock.Anything, "series-1").Return(types, nil)

		got, err := f.uc.ListBySeries(ctx, "org-1", "series-1")

		require.NoError(t, err)
		assert.Equal(t, types, got)
	})

	t.Run("no seat type yet", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/seat-type/list-by-series "No seat type yet"
		f := newSeatTypeFixture(t)
		f.seriesOf("org-1")
		f.seatTypes.EXPECT().ListBySeries(mock.Anything, "series-1").Return([]*entity.SeatType{}, nil)

		got, err := f.uc.ListBySeries(ctx, "org-1", "series-1")

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("another organizer's series", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/seat-type/list-by-series "Another Organizer's series"
		f := newSeatTypeFixture(t)
		f.seriesOf("org-2")

		_, err := f.uc.ListBySeries(ctx, "org-1", "series-1")

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})
}
