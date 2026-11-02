package rdb_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

func TestLotteryEntryRepository_CreateAnnounces(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewLotteryEntryRepository(testDB, testOutbox)
	enteredOf := func(t *testing.T, mark int64, userID entity.UserID) []*entityv1.Order {
		t.Helper()
		return announcedSince(t, mark, entity.SubjectLotteryEntryEntered, func() *entityv1.Order { return &entityv1.Order{} },
			func(o *entityv1.Order) bool { return o.GetUserId().GetValue() == string(userID) })
	}

	t.Run("entry stored", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Entry stored"
		f := openLotteryStock(t)
		userID := newUserID(t)
		mark := outboxMark(t)

		got, err := repo.Create(ctx, newEntry(f, f.SaleID, userID, 2, nil))

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
		f := openLotteryStock(t)
		userID := newUserID(t)
		entry := newEntry(f, f.SaleID, userID, 2, nil)
		_, err := repo.Create(ctx, entry)
		require.NoError(t, err)
		mark := outboxMark(t)

		again := newEntry(f, f.SaleID, userID, 2, nil)
		again.Order.CardAuthorizationID = entry.Order.CardAuthorizationID
		_, err = repo.Create(ctx, again)

		require.NoError(t, err)
		assert.Empty(t, enteredOf(t, mark, userID))
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Announcement cannot be recorded"
		f := openLotteryStock(t)
		userID := newUserID(t)

		_, err := rdb.NewLotteryEntryRepository(testDB, failingOutbox{}).Create(ctx, newEntry(f, f.SaleID, userID, 2, nil))

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Zero(t, countOrdersOfUser(t, userID), "no entry or Order is stored")
	})
}
