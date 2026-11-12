package rdb_test

import (
	"context"
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

func TestApplyTransition(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	f := seedStock(t, db, entity.TicketSaleMethodLottery, 2, 4)
	entry := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 1, entity.LotteryEntryStateEntered)

	stateOf := func(t *testing.T) int16 {
		t.Helper()
		var state int16
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT state FROM lottery_entries WHERE id = $1`, string(entry.ID)).Scan(&state))
		return state
	}
	apply := func(t *testing.T, tr entity.Transition[entity.LotteryEntryState]) error {
		t.Helper()
		tx, err := db.Pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		if err := rdb.ApplyTransitionOfLotteryEntry(ctx, tx, "lottery_entries", "state", string(entry.ID), tr, "lost_reason = $4", int16(entity.LotteryEntryLostReasonCardNotCharged)); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	t.Run("a stale From affects no row", func(t *testing.T) {
		err := apply(t, entity.Transition[entity.LotteryEntryState]{From: []entity.LotteryEntryState{entity.LotteryEntryStateWon}, To: entity.LotteryEntryStateLost})

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Equal(t, int16(entity.LotteryEntryStateEntered), stateOf(t), "the row the transition did not expect is left as it is")
	})

	t.Run("a transition that changes nothing is not applied", func(t *testing.T) {
		require.NoError(t, apply(t, entity.Transition[entity.LotteryEntryState]{}))
		assert.Equal(t, int16(entity.LotteryEntryStateEntered), stateOf(t))
	})

	t.Run("the row is in one of the From states", func(t *testing.T) {
		err := apply(t, entity.Transition[entity.LotteryEntryState]{From: []entity.LotteryEntryState{entity.LotteryEntryStateWon, entity.LotteryEntryStateEntered}, To: entity.LotteryEntryStateLost})

		require.NoError(t, err)
		assert.Equal(t, int16(entity.LotteryEntryStateLost), stateOf(t))
		var reason int16
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT lost_reason FROM lottery_entries WHERE id = $1`, string(entry.ID)).Scan(&reason))
		assert.Equal(t, int16(entity.LotteryEntryLostReasonCardNotCharged), reason, "the extra column is set with the state")
	})
}
