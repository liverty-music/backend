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
// given state. Entered, Won and Lost entries carry a card authorization "pi_<id>", as
// the schema requires; Drafts and Withdrawn entries carry none.
func insertLotteryEntry(t *testing.T, ticketTypeID entity.TicketTypeID, userID entity.UserID, count int, state entity.LotteryEntryState) *entity.LotteryEntry {
	t.Helper()
	e := entity.NewLotteryEntry(ticketTypeID, userID, count)
	e.State = state
	var ref any
	if state != entity.LotteryEntryStateDraft && state != entity.LotteryEntryStateWithdrawn {
		e.CardAuthorizationID = "pi_" + string(e.ID)
		ref = e.CardAuthorizationID
	}
	_, err := testDB.Pool.Exec(context.Background(), `
		INSERT INTO lottery_entries (id, ticket_type_id, user_id, ticket_count, card_authorization_id, state)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, string(e.ID), string(e.TicketTypeID), string(e.UserID), e.TicketCount, ref, int16(e.State))
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

// activeEntries returns the user's entries for the ticket type that are not
// Withdrawn.
func activeEntries(t *testing.T, repo *rdb.LotteryEntryRepository, ticketTypeID entity.TicketTypeID, userID entity.UserID) []*entity.LotteryEntry {
	t.Helper()
	all, err := repo.ListByTicketTypeAndUser(context.Background(), ticketTypeID, userID)
	require.NoError(t, err)
	var active []*entity.LotteryEntry
	for _, e := range all {
		if e.IsActive() {
			active = append(active, e)
		}
	}
	return active
}

func TestLotteryEntryRepository_GetOrCreateDraft(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewLotteryEntryRepository(testDB)
	ctx := context.Background()

	t.Run("first start", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-or-create-draft "First start"
		tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)
		userID := newUserID()

		got, err := repo.GetOrCreateDraft(ctx, tt.ID, userID, 2)

		require.NoError(t, err)
		assert.Equal(t, entity.LotteryEntryStateDraft, got.State)
		assert.Equal(t, 2, got.TicketCount)
		assert.False(t, got.HasCardAuthorization())
		assert.Nil(t, got.DrawPosition)
		stored, err := repo.Get(ctx, got.ID)
		require.NoError(t, err)
		assert.Equal(t, got, stored)
	})

	t.Run("reload with the same count", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-or-create-draft "Reload with the same count"
		tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)
		userID := newUserID()
		first, err := repo.GetOrCreateDraft(ctx, tt.ID, userID, 2)
		require.NoError(t, err)
		require.NoError(t, repo.SetCardAuthorization(ctx, first.ID, "pi_reload"))

		got, err := repo.GetOrCreateDraft(ctx, tt.ID, userID, 2)

		require.NoError(t, err)
		assert.Equal(t, first.ID, got.ID)
		assert.Equal(t, "pi_reload", got.CardAuthorizationID, "the draft is unchanged")
		all, err := repo.ListByTicketTypeAndUser(ctx, tt.ID, userID)
		require.NoError(t, err)
		assert.Len(t, all, 1)
	})

	t.Run("count changed", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-or-create-draft "Count changed"
		tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)
		userID := newUserID()
		old, err := repo.GetOrCreateDraft(ctx, tt.ID, userID, 2)
		require.NoError(t, err)

		got, err := repo.GetOrCreateDraft(ctx, tt.ID, userID, 3)

		require.NoError(t, err)
		assert.NotEqual(t, old.ID, got.ID)
		assert.Equal(t, entity.LotteryEntryStateDraft, got.State)
		assert.Equal(t, 3, got.TicketCount)
		withdrawn, err := repo.Get(ctx, old.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.LotteryEntryStateWithdrawn, withdrawn.State)
	})

	t.Run("double tap", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-or-create-draft "Double tap"
		for _, counts := range [][2]int{{2, 2}, {2, 3}} {
			tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)
			userID := newUserID()

			var wg sync.WaitGroup
			errs := make([]error, 2)
			for i, count := range counts {
				wg.Go(func() {
					_, errs[i] = repo.GetOrCreateDraft(ctx, tt.ID, userID, count)
				})
			}
			wg.Wait()

			assert.NoError(t, errs[0])
			assert.NoError(t, errs[1])
			active := activeEntries(t, repo, tt.ID, userID)
			require.Len(t, active, 1, "one active draft exists afterwards (counts %v)", counts)
			assert.Equal(t, entity.LotteryEntryStateDraft, active[0].State)
		}
	})

	t.Run("already entered", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-or-create-draft "Already entered"
		for _, state := range []entity.LotteryEntryState{
			entity.LotteryEntryStateEntered, entity.LotteryEntryStateWon, entity.LotteryEntryStateLost,
		} {
			tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)
			userID := newUserID()
			existing := insertLotteryEntry(t, tt.ID, userID, 2, state)

			_, err := repo.GetOrCreateDraft(ctx, tt.ID, userID, 3)

			assert.ErrorIs(t, err, apperr.ErrAlreadyExists, "state %s", state)
			all, err := repo.ListByTicketTypeAndUser(ctx, tt.ID, userID)
			require.NoError(t, err)
			assert.Equal(t, []*entity.LotteryEntry{existing}, all, "nothing changed")
		}
	})

	t.Run("entry for another event of the same sale", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-or-create-draft "Entry for another event of the same sale"
		sale := seedLotterySaleOfTwoEvents(t)
		userID := newUserID()
		insertLotteryEntry(t, sale.TicketTypes[0].ID, userID, 2, entity.LotteryEntryStateEntered)

		got, err := repo.GetOrCreateDraft(ctx, sale.TicketTypes[1].ID, userID, 2)

		require.NoError(t, err)
		assert.Equal(t, sale.TicketTypes[1].ID, got.TicketTypeID)
		assert.Equal(t, entity.LotteryEntryStateDraft, got.State)
	})

	t.Run("re-entry after withdrawal", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-or-create-draft "Re-entry after withdrawal"
		tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)
		userID := newUserID()
		withdrawn := insertLotteryEntry(t, tt.ID, userID, 2, entity.LotteryEntryStateWithdrawn)

		got, err := repo.GetOrCreateDraft(ctx, tt.ID, userID, 2)

		require.NoError(t, err)
		assert.NotEqual(t, withdrawn.ID, got.ID)
		all, err := repo.ListByTicketTypeAndUser(ctx, tt.ID, userID)
		require.NoError(t, err)
		assert.Len(t, all, 2, "the new draft is stored alongside the withdrawn entry")
	})

	t.Run("unknown ticket type", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-or-create-draft "Unknown ticket type"
		_, err := repo.GetOrCreateDraft(ctx, entity.TicketTypeID(entity.NewID()), newUserID(), 2)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})
}

func TestLotteryEntryRepository_SetCardAuthorization(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewLotteryEntryRepository(testDB)
	ctx := context.Background()
	tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)

	t.Run("card authorization recorded", func(t *testing.T) {
		// @spec components/entity/lottery-entry/set-card-authorization "Card authorization recorded"
		draft := insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateDraft)

		require.NoError(t, repo.SetCardAuthorization(ctx, draft.ID, "pi_hold"))

		got, err := repo.Get(ctx, draft.ID)
		require.NoError(t, err)
		want := *draft
		want.CardAuthorizationID = "pi_hold"
		assert.Equal(t, &want, got)
	})

	t.Run("same card authorization on retry", func(t *testing.T) {
		// @spec components/entity/lottery-entry/set-card-authorization "Same card authorization on retry"
		draft := insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateDraft)
		require.NoError(t, repo.SetCardAuthorization(ctx, draft.ID, "pi_retry"))

		require.NoError(t, repo.SetCardAuthorization(ctx, draft.ID, "pi_retry"))

		got, err := repo.Get(ctx, draft.ID)
		require.NoError(t, err)
		assert.Equal(t, "pi_retry", got.CardAuthorizationID)
	})

	t.Run("another card authorization", func(t *testing.T) {
		draft := insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateDraft)
		require.NoError(t, repo.SetCardAuthorization(ctx, draft.ID, "pi_first"))

		err := repo.SetCardAuthorization(ctx, draft.ID, "pi_second")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		got, err := repo.Get(ctx, draft.ID)
		require.NoError(t, err)
		assert.Equal(t, "pi_first", got.CardAuthorizationID, "nothing changes")
	})

	t.Run("not a draft", func(t *testing.T) {
		// @spec components/entity/lottery-entry/set-card-authorization "Not a draft"
		entered := insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateEntered)

		err := repo.SetCardAuthorization(ctx, entered.ID, "pi_other")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		got, err := repo.Get(ctx, entered.ID)
		require.NoError(t, err)
		assert.Equal(t, entered, got)
	})

	t.Run("unknown id", func(t *testing.T) {
		// @spec components/entity/lottery-entry/set-card-authorization "Unknown id"
		err := repo.SetCardAuthorization(ctx, entity.LotteryEntryID(entity.NewID()), "pi_none")

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
		for _, state := range []entity.LotteryEntryState{entity.LotteryEntryStateEntered, entity.LotteryEntryStateWithdrawn} {
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

	t.Run("active entry after a withdrawal", func(t *testing.T) {
		// @spec components/entity/lottery-entry/list-by-ticket-type-and-user "Active entry after a withdrawal"
		sale := seedLotterySaleOfTwoEvents(t)
		tt, other := sale.TicketTypes[0], sale.TicketTypes[1]
		userID := newUserID()
		withdrawn := insertLotteryEntry(t, tt.ID, userID, 2, entity.LotteryEntryStateWithdrawn)
		entered := insertLotteryEntry(t, tt.ID, userID, 2, entity.LotteryEntryStateEntered)
		insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateEntered) // another user
		insertLotteryEntry(t, other.ID, userID, 2, entity.LotteryEntryStateEntered)   // another ticket type

		got, err := repo.ListByTicketTypeAndUser(ctx, tt.ID, userID)

		require.NoError(t, err)
		assert.Equal(t, []*entity.LotteryEntry{entered, withdrawn}, got)
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
		draft := insertLotteryEntry(t, tt.ID, newUserID(), 3, entity.LotteryEntryStateDraft)
		insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateWithdrawn)
		insertLotteryEntry(t, other.ID, newUserID(), 2, entity.LotteryEntryStateEntered)

		got, err := repo.ListUndrawnForTicketType(ctx, tt.ID)

		require.NoError(t, err)
		assert.ElementsMatch(t, []*entity.LotteryEntry{a, b, draft}, got)
	})

	t.Run("no candidates", func(t *testing.T) {
		// @spec components/entity/lottery-entry/list-undrawn-for-ticket-type "No candidates"
		tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)
		insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateWon)
		insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateLost)
		insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateWithdrawn)

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

	t.Run("withdrawn and draft entries excluded", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-stats-by-ticket-type "Withdrawn and draft entries excluded"
		tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)
		insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateEntered)
		insertLotteryEntry(t, tt.ID, newUserID(), 3, entity.LotteryEntryStateDraft)
		insertLotteryEntry(t, tt.ID, newUserID(), 4, entity.LotteryEntryStateWithdrawn)

		got, err := repo.GetStatsByTicketType(ctx, tt.ID)

		require.NoError(t, err)
		assert.Equal(t, 1, got.EntryCount)
		assert.Equal(t, 2, got.TicketCount)
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
		draftB := insertLotteryEntry(t, b.ID, newUserID(), 2, entity.LotteryEntryStateDraft)
		before := time.Now()

		err := repo.PersistDrawOutcome(ctx, sale.ID,
			[]entity.DrawWinnerRow{
				{LotteryEntryID: winA.ID, CardAuthorizationID: winA.CardAuthorizationID, DrawPosition: 1},
				{LotteryEntryID: winB.ID, CardAuthorizationID: winB.CardAuthorizationID, DrawPosition: 0},
			},
			[]entity.DrawLoserRow{{LotteryEntryID: loseA.ID, CardAuthorizationID: loseA.CardAuthorizationID, DrawPosition: 0}},
			[]entity.LotteryEntryID{draftB.ID},
		)

		require.NoError(t, err)
		gotWinA, gotLoseA, gotWinB, gotDraftB := get(t, winA.ID), get(t, loseA.ID), get(t, winB.ID), get(t, draftB.ID)
		assert.Equal(t, entity.LotteryEntryStateWon, gotWinA.State)
		assert.Equal(t, position(1), gotWinA.DrawPosition)
		assert.Equal(t, entity.LotteryEntryStateLost, gotLoseA.State)
		assert.Equal(t, position(0), gotLoseA.DrawPosition)
		assert.Equal(t, entity.LotteryEntryStateWon, gotWinB.State)
		assert.Equal(t, position(0), gotWinB.DrawPosition)
		assert.Equal(t, entity.LotteryEntryStateWithdrawn, gotDraftB.State)
		assert.Nil(t, gotDraftB.DrawPosition)
		s, err := sales.Get(ctx, sale.ID, saleWindowStart)
		require.NoError(t, err)
		require.NotNil(t, s.DrawTime)
		assert.WithinDuration(t, before, *s.DrawTime, time.Minute)
	})

	t.Run("failure leaves nothing recorded", func(t *testing.T) {
		// @spec components/entity/lottery-entry/persist-draw-outcome "Failure leaves nothing recorded"
		sale := seedLotterySaleOfTwoEvents(t)
		winner := insertLotteryEntry(t, sale.TicketTypes[0].ID, newUserID(), 2, entity.LotteryEntryStateEntered)
		loser := insertLotteryEntry(t, sale.TicketTypes[0].ID, newUserID(), 2, entity.LotteryEntryStateEntered)
		draft := insertLotteryEntry(t, sale.TicketTypes[1].ID, newUserID(), 2, entity.LotteryEntryStateDraft)
		// The last write fails: a FirstCome sale cannot be drawn
		// (chk_ticket_sales_draw_lottery_only).
		firstCome := seedTicketType(t, entity.TicketSaleMethodFirstCome, 100, 4)

		err := repo.PersistDrawOutcome(ctx, firstCome.TicketSaleID,
			[]entity.DrawWinnerRow{{LotteryEntryID: winner.ID, CardAuthorizationID: winner.CardAuthorizationID, DrawPosition: 0}},
			[]entity.DrawLoserRow{{LotteryEntryID: loser.ID, CardAuthorizationID: loser.CardAuthorizationID, DrawPosition: 1}},
			[]entity.LotteryEntryID{draft.ID},
		)

		require.Error(t, err)
		assert.Equal(t, winner, get(t, winner.ID))
		assert.Equal(t, loser, get(t, loser.ID))
		assert.Equal(t, draft, get(t, draft.ID))
		assert.False(t, drawn(t, sale.ID))
		assert.False(t, drawn(t, firstCome.TicketSaleID))
	})

	t.Run("unknown sale", func(t *testing.T) {
		sale := seedLotterySaleOfTwoEvents(t)
		winner := insertLotteryEntry(t, sale.TicketTypes[0].ID, newUserID(), 2, entity.LotteryEntryStateEntered)

		err := repo.PersistDrawOutcome(ctx, entity.TicketSaleID(entity.NewID()),
			[]entity.DrawWinnerRow{{LotteryEntryID: winner.ID, CardAuthorizationID: winner.CardAuthorizationID, DrawPosition: 0}},
			nil, nil,
		)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Equal(t, winner, get(t, winner.ID))
	})

	t.Run("empty draw", func(t *testing.T) {
		// @spec components/entity/lottery-entry/persist-draw-outcome "Empty draw"
		sale := seedLotterySaleOfTwoEvents(t)
		bystander := insertLotteryEntry(t, sale.TicketTypes[0].ID, newUserID(), 2, entity.LotteryEntryStateEntered)

		require.NoError(t, repo.PersistDrawOutcome(ctx, sale.ID, nil, nil, nil))

		assert.True(t, drawn(t, sale.ID))
		assert.Equal(t, bystander, get(t, bystander.ID))
	})

	t.Run("empty sale id", func(t *testing.T) {
		err := repo.PersistDrawOutcome(ctx, "", nil, nil, nil)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestLotteryEntryRepository_UpdateState(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewLotteryEntryRepository(testDB)
	ctx := context.Background()
	tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 4)

	t.Run("state changed", func(t *testing.T) {
		// @spec components/entity/lottery-entry/update-state "State changed"
		entered := insertLotteryEntry(t, tt.ID, newUserID(), 2, entity.LotteryEntryStateEntered)

		require.NoError(t, repo.UpdateState(ctx, entered.ID, entity.LotteryEntryStateWithdrawn))

		got, err := repo.Get(ctx, entered.ID)
		require.NoError(t, err)
		want := *entered
		want.State = entity.LotteryEntryStateWithdrawn
		assert.Equal(t, &want, got)
	})

	t.Run("unknown id", func(t *testing.T) {
		// @spec components/entity/lottery-entry/update-state "Unknown id"
		err := repo.UpdateState(ctx, entity.LotteryEntryID(entity.NewID()), entity.LotteryEntryStateWithdrawn)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
