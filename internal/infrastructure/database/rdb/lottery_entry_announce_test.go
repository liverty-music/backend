package rdb_test

import (
	"context"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

func TestLotteryEntryRepository_CreateAnnounces(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	repo := rdb.NewLotteryEntryRepository(db, testOutbox)
	enteredOf := func(t *testing.T, mark int64, userID entity.UserID) []*entityv1.Order {
		t.Helper()
		return announcedSince(t, db, mark, entity.SubjectLotteryEntryEntered, func() *entityv1.Order { return &entityv1.Order{} },
			func(o *entityv1.Order) bool { return o.GetUserId().GetValue() == string(userID) })
	}

	t.Run("entry stored", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Entry stored"
		f := openLotteryStock(t, db)
		userID := newUserID(t, db)
		mark := outboxMark(t, db)

		got, err := repo.Create(ctx, newEntry(db, f, f.SaleID, userID, 2, nil), time.Now())

		require.NoError(t, err)
		announced := enteredOf(t, mark, userID)
		require.Len(t, announced, 1)
		assert.Equal(t, string(got.ID), announced[0].GetLotteryEntryId().GetValue())
		assert.Equal(t, string(got.Order.ID), announced[0].GetId().GetValue())
		assert.Equal(t, string(f.ID), announced[0].GetTicketStockId().GetValue())
		assert.Equal(t, entityv1.OrderStatus_ORDER_STATUS_PENDING, announced[0].GetStatus())
	})

	t.Run("same card authorization again", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Same card authorization again"
		f := openLotteryStock(t, db)
		userID := newUserID(t, db)
		entry := newEntry(db, f, f.SaleID, userID, 2, nil)
		_, err := repo.Create(ctx, entry, time.Now())
		require.NoError(t, err)
		mark := outboxMark(t, db)

		again := newEntry(db, f, f.SaleID, userID, 2, nil)
		again.Order.CardAuthorizationID = entry.Order.CardAuthorizationID
		_, err = repo.Create(ctx, again, time.Now())

		require.NoError(t, err)
		assert.Empty(t, enteredOf(t, mark, userID))
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Announcement cannot be recorded"
		f := openLotteryStock(t, db)
		userID := newUserID(t, db)

		_, err := rdb.NewLotteryEntryRepository(db, failingOutbox{}).Create(ctx, newEntry(db, f, f.SaleID, userID, 2, nil), time.Now())

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Zero(t, countOrdersOfUser(t, db, userID), "no entry or Order is stored")
	})
}
