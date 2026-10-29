package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seatTypeNames returns the names of the seat types, in order.
func seatTypeNames(types []*entity.SeatType) []string {
	names := make([]string, len(types))
	for i, st := range types {
		names[i] = st.Name
	}
	return names
}

func TestSeatTypeRepository_Create(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewSeatTypeRepository(testDB)
	ctx := context.Background()

	listed := func(t *testing.T, seriesID string) []*entity.SeatType {
		t.Helper()
		got, err := repo.ListBySeries(ctx, seriesID)
		require.NoError(t, err)
		return got
	}

	t.Run("seat type stored", func(t *testing.T) {
		// @spec components/entity/seat-type/create "Seat type stored"
		seriesID := seriesOf(t, seedPublishedEvent(t))
		st := entity.NewSeatType(seriesID, "S席", 8000)

		got, err := repo.Create(ctx, st)

		require.NoError(t, err)
		assert.Equal(t, st, got)
		stored, err := repo.Get(ctx, st.ID)
		require.NoError(t, err)
		assert.Equal(t, st, stored)
	})

	t.Run("same name twice", func(t *testing.T) {
		// @spec components/entity/seat-type/create "Same name twice"
		seriesID := seriesOf(t, seedPublishedEvent(t))
		first, err := repo.Create(ctx, entity.NewSeatType(seriesID, "S席", 8000))
		require.NoError(t, err)

		_, err = repo.Create(ctx, entity.NewSeatType(seriesID, "S席", 9000))

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
		assert.Equal(t, []*entity.SeatType{first}, listed(t, seriesID))
	})

	t.Run("same name in another series", func(t *testing.T) {
		_, err := repo.Create(ctx, entity.NewSeatType(seriesOf(t, seedPublishedEvent(t)), "S席", 8000))
		require.NoError(t, err)

		_, err = repo.Create(ctx, entity.NewSeatType(seriesOf(t, seedPublishedEvent(t)), "S席", 8000))

		assert.NoError(t, err)
	})

	t.Run("discovered series", func(t *testing.T) {
		// @spec components/entity/seat-type/create "Discovered series"
		seriesID := seedSeriesOnly(t, "Found Tour")

		_, err := repo.Create(ctx, entity.NewSeatType(seriesID, "S席", 8000))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Empty(t, listed(t, seriesID))
	})

	t.Run("unknown series", func(t *testing.T) {
		_, err := repo.Create(ctx, entity.NewSeatType(entity.NewID(), "S席", 8000))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("invalid price", func(t *testing.T) {
		// @spec components/entity/seat-type/create "Invalid price"
		seriesID := seriesOf(t, seedPublishedEvent(t))

		_, err := repo.Create(ctx, entity.NewSeatType(seriesID, "S席", 0))

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Empty(t, listed(t, seriesID))
	})
}

func TestSeatTypeRepository_Get(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewSeatTypeRepository(testDB)
	ctx := context.Background()

	t.Run("seat type found", func(t *testing.T) {
		// @spec components/entity/seat-type/get "Seat type found"
		seriesID := seriesOf(t, seedPublishedEvent(t))
		st, err := repo.Create(ctx, entity.NewSeatType(seriesID, "S席", 8000))
		require.NoError(t, err)

		got, err := repo.Get(ctx, st.ID)

		require.NoError(t, err)
		assert.Equal(t, seriesID, got.SeriesID)
		assert.Equal(t, "S席", got.Name)
		assert.Equal(t, int64(8000), got.Price)
	})

	t.Run("unknown seat type", func(t *testing.T) {
		// @spec components/entity/seat-type/get "Unknown seat type"
		_, err := repo.Get(ctx, entity.SeatTypeID(entity.NewID()))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestSeatTypeRepository_ListBySeries(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewSeatTypeRepository(testDB)
	ctx := context.Background()

	t.Run("three seat types", func(t *testing.T) {
		// @spec components/entity/seat-type/list-by-series "Three seat types"
		seriesID := seriesOf(t, seedPublishedEvent(t))
		for _, st := range []*entity.SeatType{
			entity.NewSeatType(seriesID, "B席", 7000),
			entity.NewSeatType(seriesID, "S席", 9000),
			entity.NewSeatType(seriesID, "A席", 7000),
		} {
			_, err := repo.Create(ctx, st)
			require.NoError(t, err)
		}
		_, err := repo.Create(ctx, entity.NewSeatType(seriesOf(t, seedPublishedEvent(t)), "SS席", 20000))
		require.NoError(t, err)

		got, err := repo.ListBySeries(ctx, seriesID)

		require.NoError(t, err)
		assert.Equal(t, []string{"S席", "A席", "B席"}, seatTypeNames(got))
	})

	t.Run("no seat type", func(t *testing.T) {
		// @spec components/entity/seat-type/list-by-series "No seat type"
		got, err := repo.ListBySeries(ctx, seriesOf(t, seedPublishedEvent(t)))

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("unknown series", func(t *testing.T) {
		got, err := repo.ListBySeries(ctx, entity.NewID())

		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestSeatTypeRepository_Delete(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewSeatTypeRepository(testDB)
	ctx := context.Background()

	t.Run("unused seat type", func(t *testing.T) {
		// @spec components/entity/seat-type/delete "Unused seat type"
		st := seatTypeOf(t, seedPublishedEvent(t), "S席", 8000)

		require.NoError(t, repo.Delete(ctx, st.ID))

		_, err := repo.Get(ctx, st.ID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("stocked seat type", func(t *testing.T) {
		// @spec components/entity/seat-type/delete "Stocked seat type"
		f := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 4)

		err := repo.Delete(ctx, f.SeatType.ID)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, err = repo.Get(ctx, f.SeatType.ID)
		assert.NoError(t, err, "the seat type remains")
	})

	t.Run("stock created while the delete runs", func(t *testing.T) {
		eventID := seedPublishedEvent(t)
		st := seatTypeOf(t, eventID, "S席", 8000)
		// The stock insert takes a key-share lock on the seat type row and
		// commits only once the delete waits for it.
		create, err := testDB.Pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = create.Rollback(ctx) }()
		_, err = create.Exec(ctx, `INSERT INTO ticket_stocks (id, event_id, seat_type_id, quantity) VALUES ($1, $2, $3, 10)`,
			entity.NewID(), eventID, string(st.ID))
		require.NoError(t, err)
		var pid uint32
		require.NoError(t, create.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid))

		deleted := make(chan error, 1)
		go func() { deleted <- repo.Delete(ctx, st.ID) }()
		require.Eventually(t, backendBlocking(t, pid), 5*time.Second, 20*time.Millisecond, "the delete waits for the stock insert")
		require.NoError(t, create.Commit(ctx))

		assert.ErrorIs(t, <-deleted, apperr.ErrFailedPrecondition)
		_, err = repo.Get(ctx, st.ID)
		assert.NoError(t, err, "the seat type remains")
	})

	t.Run("unknown id", func(t *testing.T) {
		// @spec components/entity/seat-type/delete "Unknown id"
		err := repo.Delete(ctx, entity.SeatTypeID(entity.NewID()))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
