package rdb_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// insertLotteryEntry stores an entry of the user for the ticket type in the
// given state, with the card authorization "pi_<id>".
func insertLotteryEntry(t *testing.T, ticketTypeID entity.TicketTypeID, userID entity.UserID, count int, state entity.LotteryEntryState) *entity.LotteryEntry {
	t.Helper()
	e := entity.NewLotteryEntry(ticketTypeID, userID, count, "", nil)
	e.CardAuthorizationID = "pi_" + string(e.ID)
	e.State = state
	_, err := testDB.Pool.Exec(context.Background(), `
		INSERT INTO lottery_entries (id, ticket_type_id, user_id, ticket_count, card_authorization_id, state)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, string(e.ID), string(e.TicketTypeID), string(e.UserID), e.TicketCount, e.CardAuthorizationID, int16(e.State))
	require.NoError(t, err)
	return e
}

// newUserID returns a fresh user id; lottery_entries.user_id has no foreign
// key.
func newUserID() entity.UserID {
	return entity.UserID(entity.NewID())
}

// seedLotterySaleOfTwoEvents stores a Lottery sale with ticket types for two
// events of one Series.
func seedLotterySaleOfTwoEvents(t *testing.T) *entity.TicketSale {
	t.Helper()
	first := seedPublishedEvent(t)
	return createSale(t, newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, saleWindowStart.Add(7*24*time.Hour),
		first, seedEventInSeriesOf(t, first)))
}

// setSaleEndByDatabaseClock moves the sale's window so that it opened an hour
// before the database's current time and ends after the given interval from
// it (negative: already ended). LotteryEntry.Create reads the database clock,
// so the tests that store an entry place the window around it.
func setSaleEndByDatabaseClock(t *testing.T, saleID entity.TicketSaleID, end time.Duration) {
	t.Helper()
	_, err := testDB.Pool.Exec(context.Background(), `
		UPDATE ticket_sales
		SET sale_start_at = now() - interval '1 hour' + least($2::interval, interval '0'),
		    sale_end_at = now() + $2::interval
		WHERE id = $1`, string(saleID), end)
	require.NoError(t, err)
}

// openLotteryTicketType seeds a Lottery ticket type whose sale is open by the
// database clock for another day.
func openLotteryTicketType(t *testing.T) *entity.TicketType {
	t.Helper()
	tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)
	setSaleEndByDatabaseClock(t, tt.TicketSaleID, 24*time.Hour)
	return tt
}

// newEntry returns a new Entered entry of the user for count tickets of the
// ticket type, made from a fresh card authorization.
func newEntry(ticketTypeID entity.TicketTypeID, userID entity.UserID, count int, code *entity.ReferralCode) *entity.LotteryEntry {
	return entity.NewLotteryEntry(ticketTypeID, userID, count, "pi_"+entity.NewID(), code)
}

func TestLotteryEntryRepository_Create(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewLotteryEntryRepository(testDB)
	ctx := context.Background()

	t.Run("entry stored", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Entry stored"
		tt := openLotteryTicketType(t)
		code := entity.ReferralCode("member-a")
		entry := newEntry(tt.ID, newUserID(), 2, &code)

		got, err := repo.Create(ctx, entry)

		require.NoError(t, err)
		assert.Equal(t, entity.LotteryEntryStateEntered, got.State)
		assert.Equal(t, 2, got.TicketCount)
		assert.Equal(t, entry.CardAuthorizationID, got.CardAuthorizationID)
		assert.Equal(t, &code, got.ReferralCode)
		assert.Nil(t, got.DrawPosition)
		stored, err := repo.Get(ctx, got.ID)
		require.NoError(t, err)
		assert.Equal(t, got, stored)
	})

	t.Run("second entry", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Second entry"
		for _, state := range []entity.LotteryEntryState{entity.LotteryEntryStateEntered, entity.LotteryEntryStateWon, entity.LotteryEntryStateLost} {
			tt := openLotteryTicketType(t)
			userID := newUserID()
			first := insertLotteryEntry(t, tt.ID, userID, 2, state)

			_, err := repo.Create(ctx, newEntry(tt.ID, userID, 3, nil))

			assert.ErrorIs(t, err, apperr.ErrAlreadyExists, state.String())
			all, err := repo.ListByTicketTypeAndUser(ctx, tt.ID, userID)
			require.NoError(t, err)
			assert.Equal(t, []*entity.LotteryEntry{first}, all, "nothing is stored")
		}
	})

	t.Run("double tap", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Double tap"
		tt := openLotteryTicketType(t)
		userID := newUserID()

		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range errs {
			wg.Go(func() {
				_, errs[i] = repo.Create(ctx, newEntry(tt.ID, userID, 2, nil))
			})
		}
		wg.Wait()

		var stored, refused int
		for _, err := range errs {
			switch {
			case err == nil:
				stored++
			case assert.ErrorIs(t, err, apperr.ErrAlreadyExists):
				refused++
			}
		}
		assert.Equal(t, 1, stored)
		assert.Equal(t, 1, refused)
		all, err := repo.ListByTicketTypeAndUser(ctx, tt.ID, userID)
		require.NoError(t, err)
		assert.Len(t, all, 1)
	})

	t.Run("same card authorization again", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Same card authorization again"
		tt := openLotteryTicketType(t)
		entry := newEntry(tt.ID, newUserID(), 2, nil)
		first, err := repo.Create(ctx, entry)
		require.NoError(t, err)

		// Before the end time: the same entry, nothing else stored.
		again, err := repo.Create(ctx, entity.NewLotteryEntry(tt.ID, entry.UserID, 2, entry.CardAuthorizationID, nil))
		require.NoError(t, err)
		assert.Equal(t, first, again)

		// After the end time: still the same entry.
		setSaleEndByDatabaseClock(t, tt.TicketSaleID, -time.Minute)
		late, err := repo.Create(ctx, entity.NewLotteryEntry(tt.ID, entry.UserID, 2, entry.CardAuthorizationID, nil))
		require.NoError(t, err)
		assert.Equal(t, first, late)

		// Twice at the same time: one entry, returned to both.
		other := openLotteryTicketType(t)
		cardAuthorizationID := "pi_" + entity.NewID()
		var wg sync.WaitGroup
		got := make([]*entity.LotteryEntry, 2)
		errs := make([]error, 2)
		for i := range got {
			wg.Go(func() {
				got[i], errs[i] = repo.Create(ctx, entity.NewLotteryEntry(other.ID, entry.UserID, 2, cardAuthorizationID, nil))
			})
		}
		wg.Wait()
		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		assert.Equal(t, got[0], got[1])
		all, err := repo.ListByTicketTypeAndUser(ctx, other.ID, entry.UserID)
		require.NoError(t, err)
		assert.Len(t, all, 1)
	})

	t.Run("entry for another event of the same sale", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Entry for another event of the same sale"
		sale := seedLotterySaleOfTwoEvents(t)
		setSaleEndByDatabaseClock(t, sale.ID, 24*time.Hour)
		userID := newUserID()
		insertLotteryEntry(t, sale.TicketTypes[0].ID, userID, 2, entity.LotteryEntryStateEntered)

		got, err := repo.Create(ctx, newEntry(sale.TicketTypes[1].ID, userID, 2, nil))

		require.NoError(t, err)
		assert.Equal(t, sale.TicketTypes[1].ID, got.TicketTypeID)
		assert.Equal(t, entity.LotteryEntryStateEntered, got.State)
	})

	t.Run("after the end time", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "After the end time"
		tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)
		setSaleEndByDatabaseClock(t, tt.TicketSaleID, -time.Second)
		userID := newUserID()

		_, err := repo.Create(ctx, newEntry(tt.ID, userID, 2, nil))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		all, err := repo.ListByTicketTypeAndUser(ctx, tt.ID, userID)
		require.NoError(t, err)
		assert.Empty(t, all)
	})

	t.Run("unknown ticket type", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Unknown ticket type"
		entry := newEntry(entity.TicketTypeID(entity.NewID()), newUserID(), 2, nil)

		_, err := repo.Create(ctx, entry)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, err = repo.GetByCardAuthorizationID(ctx, entry.CardAuthorizationID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestLotteryEntryRepository_Get(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewLotteryEntryRepository(testDB)
	ctx := context.Background()
	tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)

	t.Run("existing entry", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get "Existing entry"
		for _, state := range []entity.LotteryEntryState{entity.LotteryEntryStateEntered, entity.LotteryEntryStateWon, entity.LotteryEntryStateLost} {
			e := insertLotteryEntry(t, tt.ID, newUserID(), 3, state)

			got, err := repo.Get(ctx, e.ID)

			require.NoError(t, err)
			assert.Equal(t, e, got)
		}
	})

	t.Run("unknown id", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get "Unknown id"
		_, err := repo.Get(ctx, entity.LotteryEntryID(entity.NewID()))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestLotteryEntryRepository_GetByCardAuthorizationID(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewLotteryEntryRepository(testDB)
	ctx := context.Background()
	tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)

	t.Run("charged win", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-by-card-authorization-id "Charged win"
		won := insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateWon)

		got, err := repo.GetByCardAuthorizationID(ctx, won.CardAuthorizationID)

		require.NoError(t, err)
		assert.Equal(t, won, got)
	})

	t.Run("unknown authorization", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-by-card-authorization-id "Unknown card authorization"
		_, err := repo.GetByCardAuthorizationID(ctx, "pi_unknown_"+entity.NewID())

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestLotteryEntryRepository_ListByTicketTypeAndUser(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewLotteryEntryRepository(testDB)
	ctx := context.Background()

	t.Run("entry of the user", func(t *testing.T) {
		// @spec components/entity/lottery-entry/list-by-ticket-type-and-user "Entry of the user"
		sale := seedLotterySaleOfTwoEvents(t)
		tt, other := sale.TicketTypes[0], sale.TicketTypes[1]
		userID := newUserID()
		entered := insertLotteryEntry(t, tt.ID, userID, 2, entity.LotteryEntryStateEntered)
		insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateEntered) // another user
		insertLotteryEntry(t, other.ID, userID, 2, entity.LotteryEntryStateEntered)   // another ticket type

		got, err := repo.ListByTicketTypeAndUser(ctx, tt.ID, userID)

		require.NoError(t, err)
		assert.Equal(t, []*entity.LotteryEntry{entered}, got)
	})

	t.Run("never entered", func(t *testing.T) {
		// @spec components/entity/lottery-entry/list-by-ticket-type-and-user "Never entered"
		tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)

		got, err := repo.ListByTicketTypeAndUser(ctx, tt.ID, newUserID())

		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})
}

func TestLotteryEntryRepository_ListUndrawnForTicketType(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewLotteryEntryRepository(testDB)
	ctx := context.Background()

	t.Run("mixed states", func(t *testing.T) {
		// @spec components/entity/lottery-entry/list-undrawn-for-ticket-type "Mixed states"
		sale := seedLotterySaleOfTwoEvents(t)
		tt, other := sale.TicketTypes[0], sale.TicketTypes[1]
		a := insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateEntered)
		b := insertLotteryEntry(t, tt.ID, newUserID(), 1, entity.LotteryEntryStateEntered)
		insertLotteryEntry(t, tt.ID, newUserID(), 3, entity.LotteryEntryStateWon)
		insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateLost)
		insertLotteryEntry(t, other.ID, newUserID(), 2, entity.LotteryEntryStateEntered)

		got, err := repo.ListUndrawnForTicketType(ctx, tt.ID)

		require.NoError(t, err)
		assert.ElementsMatch(t, []*entity.LotteryEntry{a, b}, got)
	})

	t.Run("no candidates", func(t *testing.T) {
		// @spec components/entity/lottery-entry/list-undrawn-for-ticket-type "No candidates"
		tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)
		insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateWon)
		insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateLost)

		got, err := repo.ListUndrawnForTicketType(ctx, tt.ID)

		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})
}

func TestLotteryEntryRepository_GetStatsByTicketType(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewLotteryEntryRepository(testDB)
	ctx := context.Background()

	t.Run("after the draw", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-stats-by-ticket-type "After the draw"
		tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)
		insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateWon)
		insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateWon)
		insertLotteryEntry(t, tt.ID, newUserID(), 4, entity.LotteryEntryStateLost)

		got, err := repo.GetStatsByTicketType(ctx, tt.ID)

		require.NoError(t, err)
		assert.Equal(t, entity.LotteryEntryStats{
			EntryCount: 3, TicketCount: 8, WonEntryCount: 2, WonTicketCount: 4, LostEntryCount: 1,
		}, got)
	})

	t.Run("before the draw", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-stats-by-ticket-type "Before the draw"
		tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)
		insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateEntered)
		insertLotteryEntry(t, tt.ID, newUserID(), 3, entity.LotteryEntryStateEntered)

		got, err := repo.GetStatsByTicketType(ctx, tt.ID)

		require.NoError(t, err)
		assert.Equal(t, entity.LotteryEntryStats{EntryCount: 2, TicketCount: 5}, got)
	})

	t.Run("no entries", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-stats-by-ticket-type "No entries"
		tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)

		got, err := repo.GetStatsByTicketType(ctx, tt.ID)

		require.NoError(t, err)
		assert.Equal(t, entity.LotteryEntryStats{}, got)
	})
}

func TestLotteryEntryRepository_PersistDrawOutcome(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewLotteryEntryRepository(testDB)
	sales := rdb.NewTicketSaleRepository(testDB)
	ctx := context.Background()

	// get reads an entry back.
	get := func(t *testing.T, id entity.LotteryEntryID) *entity.LotteryEntry {
		t.Helper()
		e, err := repo.Get(ctx, id)
		require.NoError(t, err)
		return e
	}
	// drawn reports whether the sale is drawn.
	drawn := func(t *testing.T, id entity.TicketSaleID) bool {
		t.Helper()
		s, err := sales.Get(ctx, id, saleWindowStart)
		require.NoError(t, err)
		return s.IsDrawn()
	}
	position := func(p int) *int { return &p }

	t.Run("outcome recorded", func(t *testing.T) {
		// @spec components/entity/lottery-entry/persist-draw-outcome "Outcome recorded"
		sale := seedLotterySaleOfTwoEvents(t)
		a, b := sale.TicketTypes[0], sale.TicketTypes[1]
		winA := insertLotteryEntry(t, a.ID, newUserID(), 2, entity.LotteryEntryStateEntered)
		loseA := insertLotteryEntry(t, a.ID, newUserID(), 4, entity.LotteryEntryStateEntered)
		winB := insertLotteryEntry(t, b.ID, newUserID(), 1, entity.LotteryEntryStateEntered)
		before := time.Now()

		err := repo.PersistDrawOutcome(ctx, sale.ID,
			[]entity.DrawWinnerRow{
				{LotteryEntryID: winA.ID, CardAuthorizationID: winA.CardAuthorizationID, DrawPosition: 1},
				{LotteryEntryID: winB.ID, CardAuthorizationID: winB.CardAuthorizationID, DrawPosition: 0},
			},
			[]entity.DrawLoserRow{{LotteryEntryID: loseA.ID, CardAuthorizationID: loseA.CardAuthorizationID, DrawPosition: 0}},
		)

		require.NoError(t, err)
		gotWinA, gotLoseA, gotWinB := get(t, winA.ID), get(t, loseA.ID), get(t, winB.ID)
		assert.Equal(t, entity.LotteryEntryStateWon, gotWinA.State)
		assert.Equal(t, position(1), gotWinA.DrawPosition)
		assert.Equal(t, entity.LotteryEntryStateLost, gotLoseA.State)
		assert.Equal(t, position(0), gotLoseA.DrawPosition)
		assert.Equal(t, entity.LotteryEntryStateWon, gotWinB.State)
		assert.Equal(t, position(0), gotWinB.DrawPosition)
		s, err := sales.Get(ctx, sale.ID, saleWindowStart)
		require.NoError(t, err)
		require.NotNil(t, s.DrawTime)
		assert.WithinDuration(t, before, *s.DrawTime, time.Minute)
	})

	t.Run("failure leaves nothing recorded", func(t *testing.T) {
		// @spec components/entity/lottery-entry/persist-draw-outcome "Failure leaves nothing recorded"
		sale := seedLotterySaleOfTwoEvents(t)
		winner := insertLotteryEntry(t, sale.TicketTypes[0].ID, newUserID(), 2, entity.LotteryEntryStateEntered)
		loser := insertLotteryEntry(t, sale.TicketTypes[1].ID, newUserID(), 2, entity.LotteryEntryStateEntered)
		// The last write fails: a FirstCome sale cannot be drawn
		// (chk_ticket_sales_draw_lottery_only).
		firstCome := seedTicketType(t, entity.TicketSaleMethodFirstCome, 100, 4)

		err := repo.PersistDrawOutcome(ctx, firstCome.TicketSaleID,
			[]entity.DrawWinnerRow{{LotteryEntryID: winner.ID, CardAuthorizationID: winner.CardAuthorizationID, DrawPosition: 0}},
			[]entity.DrawLoserRow{{LotteryEntryID: loser.ID, CardAuthorizationID: loser.CardAuthorizationID, DrawPosition: 1}},
		)

		require.Error(t, err)
		assert.Equal(t, winner, get(t, winner.ID))
		assert.Equal(t, loser, get(t, loser.ID))
		assert.False(t, drawn(t, sale.ID))
		assert.False(t, drawn(t, firstCome.TicketSaleID))
	})

	t.Run("unknown sale", func(t *testing.T) {
		sale := seedLotterySaleOfTwoEvents(t)
		winner := insertLotteryEntry(t, sale.TicketTypes[0].ID, newUserID(), 2, entity.LotteryEntryStateEntered)

		err := repo.PersistDrawOutcome(ctx, entity.TicketSaleID(entity.NewID()),
			[]entity.DrawWinnerRow{{LotteryEntryID: winner.ID, CardAuthorizationID: winner.CardAuthorizationID, DrawPosition: 0}},
			nil,
		)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Equal(t, winner, get(t, winner.ID))
	})

	t.Run("empty draw", func(t *testing.T) {
		// @spec components/entity/lottery-entry/persist-draw-outcome "Empty draw"
		sale := seedLotterySaleOfTwoEvents(t)
		bystander := insertLotteryEntry(t, sale.TicketTypes[0].ID, newUserID(), 2, entity.LotteryEntryStateEntered)

		require.NoError(t, repo.PersistDrawOutcome(ctx, sale.ID, nil, nil))

		assert.True(t, drawn(t, sale.ID))
		assert.Equal(t, bystander, get(t, bystander.ID))
	})

	t.Run("empty sale id", func(t *testing.T) {
		err := repo.PersistDrawOutcome(ctx, "", nil, nil)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}
