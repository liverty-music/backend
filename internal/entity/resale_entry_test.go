package entity_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
)

// novJST returns the time on the given day of November 2026, Japan time.
func novJST(day, hour int) time.Time {
	return time.Date(2026, 11, day, hour, 0, 0, 0, jst)
}

// entered returns an Entered entry of a Pending Order for count tickets whose
// card authorization expires at expire.
func entered(count int, expire time.Time) *entity.ResaleEntry {
	order := &entity.Order{ID: entity.OrderID(entity.NewID()), TicketCount: count, Status: entity.OrderStatusPending, CardAuthorizationID: entity.NewID()}
	e := entity.NewResaleEntry(order, novJST(1, 10))
	e.CardAuthorizationExpireTime = &expire
	return e
}

func TestNewResaleEntry(t *testing.T) {
	t.Parallel()
	// @spec components/entity/resale-entry "New entry"
	order := &entity.Order{ID: entity.OrderID(entity.NewID()), TicketCount: 2, Status: entity.OrderStatusPending, CardAuthorizationID: entity.NewID()}

	e := entity.NewResaleEntry(order, novJST(5, 15))

	assert.Equal(t, entity.ResaleEntryStateEntered, e.State)
	assert.Equal(t, entity.OrderStatusPending, e.Order.Status)
	assert.NotEmpty(t, e.Order.CardAuthorizationID)
	assert.Nil(t, e.DrawTime)
	assert.Equal(t, e.ID, order.ResaleEntryID, "the entry names its Order as its source")
	assert.NoError(t, order.ValidateSource())
}

func TestResaleDrawAt(t *testing.T) {
	t.Parallel()

	t.Run("day before the event", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-entry "Day before the event"
		draw, last := entity.ResaleDrawAt(novJST(9, 12), novJST(10, 19))
		assert.True(t, draw)
		assert.False(t, last)
	})

	t.Run("evening show", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-entry "Evening show"
		draw, last := entity.ResaleDrawAt(novJST(10, 12), novJST(10, 19))
		assert.True(t, draw)
		assert.True(t, last)
	})

	t.Run("matinee", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-entry "Matinee"
		draw, last := entity.ResaleDrawAt(novJST(10, 11), novJST(10, 14))
		assert.True(t, draw)
		assert.True(t, last)
		draw, _ = entity.ResaleDrawAt(novJST(10, 12), novJST(10, 14))
		assert.False(t, draw, "no draw at 12:00 that day")
	})

	t.Run("other times", func(t *testing.T) {
		t.Parallel()
		draw, _ := entity.ResaleDrawAt(novJST(9, 13), novJST(10, 19))
		assert.False(t, draw)
		draw, _ = entity.ResaleDrawAt(novJST(11, 12), novJST(10, 19))
		assert.False(t, draw, "after the last draw")
	})
}

func TestNextResaleDrawTime(t *testing.T) {
	t.Parallel()

	next, ok := entity.NextResaleDrawTime(time.Date(2026, 11, 5, 15, 0, 0, 0, jst), novJST(10, 19))
	require.True(t, ok)
	assert.True(t, next.Equal(novJST(6, 12)))

	next, ok = entity.NextResaleDrawTime(time.Date(2026, 11, 10, 9, 0, 0, 0, jst), novJST(10, 14))
	require.True(t, ok)
	assert.True(t, next.Equal(novJST(10, 11)), "the matinee's last draw")

	_, ok = entity.NextResaleDrawTime(novJST(10, 11), novJST(10, 14))
	assert.False(t, ok, "resale closed")
}

func TestResaleEntry_TakesPartInDrawAt(t *testing.T) {
	t.Parallel()

	t.Run("card authorization valid for weeks", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-entry "Card authorization valid for weeks"
		assert.True(t, entered(1, novJST(30, 0)).TakesPartInDrawAt(novJST(10, 12)))
	})

	t.Run("card authorization about to expire", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-entry "Card authorization about to expire"
		e := entered(1, novJST(11, 9))
		assert.False(t, e.TakesPartInDrawAt(novJST(10, 12)))
		out := entity.DrawResale([]*entity.ResaleEntry{e}, []entity.ResaleListingID{"l1"}, novJST(10, 12), false, false)
		assert.Equal(t, []*entity.ResaleEntry{e}, out.Lapsed, "it lapses at that draw")
		assert.Empty(t, out.Winners)
	})
}

func TestDrawResale(t *testing.T) {
	t.Parallel()
	far := novJST(30, 0)

	t.Run("group of two, one listing left", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-entry "Group of two, one listing left"
		e := entered(2, far)
		out := entity.DrawResale([]*entity.ResaleEntry{e}, []entity.ResaleListingID{"l1"}, novJST(9, 12), false, false)
		assert.Empty(t, out.Winners)
		assert.Empty(t, out.Lost, "stays Entered for the next draw")
		assert.Empty(t, out.Lapsed)

		last := entity.DrawResale([]*entity.ResaleEntry{e}, []entity.ResaleListingID{"l1"}, novJST(10, 12), true, false)
		assert.Equal(t, []*entity.ResaleEntry{e}, last.Lost, "at the last draw it is Lost")
		assert.Equal(t, entity.ResaleEntryLostReasonNoTicketLeft, last.LostReason)
	})

	t.Run("group of two, two listings left", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-entry "Group of two, two listings left"
		e := entered(2, far)
		out := entity.DrawResale([]*entity.ResaleEntry{e}, []entity.ResaleListingID{"l1", "l2", "l3"}, novJST(9, 12), false, false)
		require.Len(t, out.Winners, 1)
		assert.Equal(t, []entity.ResaleListingID{"l1", "l2"}, out.Winners[0].Listings, "the two oldest; one stays free")
	})

	t.Run("in the given order, all or nothing", func(t *testing.T) {
		t.Parallel()
		first, second, third := entered(2, far), entered(1, far), entered(1, far)
		out := entity.DrawResale([]*entity.ResaleEntry{first, second, third}, []entity.ResaleListingID{"l1", "l2"}, novJST(9, 12), false, false)
		require.Len(t, out.Winners, 1)
		assert.Same(t, first, out.Winners[0].Entry)
		assert.Empty(t, out.Lost, "the others wait for the next draw")
	})

	t.Run("concert cancelled", func(t *testing.T) {
		t.Parallel()
		a, b := entered(1, far), entered(1, novJST(10, 13))
		out := entity.DrawResale([]*entity.ResaleEntry{a, b}, []entity.ResaleListingID{"l1", "l2"}, novJST(9, 12), false, true)
		assert.Empty(t, out.Winners)
		assert.Equal(t, []*entity.ResaleEntry{a, b}, out.Lost)
		assert.Equal(t, entity.ResaleEntryLostReasonConcertCancelled, out.LostReason)
	})
}

func TestResaleEntry_RevertWin(t *testing.T) {
	t.Parallel()

	t.Run("card declined at capture", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-entry "Card declined at capture"
		e := entered(1, novJST(30, 0))
		e.State = entity.ResaleEntryStateWon
		listing := &entity.ResaleListing{Status: entity.ResaleListingStatusListed, ResaleEntryID: e.ID, ResaleEntryState: e.State}
		require.False(t, listing.IsFreeForDrawAt(novJST(9, 13), novJST(10, 12)))

		require.NoError(t, e.RevertWin(entity.ResaleEntryLostReasonCardNotCharged))

		assert.Equal(t, entity.ResaleEntryStateLost, e.State)
		assert.Equal(t, entity.ResaleEntryLostReasonCardNotCharged, e.LostReason)
		assert.Equal(t, entity.OrderStatusVoid, e.Order.Status)
		listing.ResaleEntryState = e.State
		assert.True(t, listing.IsFreeForDrawAt(novJST(9, 13), novJST(10, 12)), "its listings are free again")
	})

	t.Run("already lost", func(t *testing.T) {
		t.Parallel()
		e := entered(1, novJST(30, 0))
		e.State = entity.ResaleEntryStateLost
		assert.NoError(t, e.RevertWin(entity.ResaleEntryLostReasonCardNotCharged))
	})

	t.Run("not a pending win", func(t *testing.T) {
		t.Parallel()
		e := entered(1, novJST(30, 0))
		assert.ErrorIs(t, e.RevertWin(entity.ResaleEntryLostReasonCardNotCharged), entity.ErrResaleWinNotRevertible)
		e.State, e.Order.Status = entity.ResaleEntryStateWon, entity.OrderStatusPaid
		assert.ErrorIs(t, e.RevertWin(entity.ResaleEntryLostReasonCardNotCharged), entity.ErrResaleWinNotRevertible)
	})
}

func TestResaleEntry_IsWithdrawable(t *testing.T) {
	t.Parallel()

	t.Run("waiting for the draw", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-entry "Waiting for the draw"
		assert.True(t, entered(1, novJST(30, 0)).IsWithdrawable())
	})

	t.Run("already won", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-entry "Already won"
		e := entered(1, novJST(30, 0))
		e.State = entity.ResaleEntryStateWon
		assert.False(t, e.IsWithdrawable())
	})
}

func TestResaleListing_IsFreeForDrawAt(t *testing.T) {
	t.Parallel()
	at, deadline := novJST(9, 12), novJST(10, 12)

	t.Run("never allocated", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-listing "Never allocated"
		l := &entity.ResaleListing{Status: entity.ResaleListingStatusListed}
		assert.True(t, l.IsFreeForDrawAt(at, deadline))
	})

	t.Run("won by an entry being charged", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-listing "Won by an entry being charged"
		l := &entity.ResaleListing{Status: entity.ResaleListingStatusListed, ResaleEntryID: "e1", ResaleEntryState: entity.ResaleEntryStateWon}
		assert.False(t, l.IsFreeForDrawAt(at, deadline))
		assert.False(t, l.IsWithdrawableAt(at, deadline), "a listing being bought cannot be withdrawn")
	})

	t.Run("winner could not be charged", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-listing "Winner could not be charged"
		l := &entity.ResaleListing{Status: entity.ResaleListingStatusListed, ResaleEntryID: "e1", ResaleEntryState: entity.ResaleEntryStateLost}
		assert.True(t, l.IsFreeForDrawAt(at, deadline))
	})

	t.Run("after the deadline", func(t *testing.T) {
		t.Parallel()
		l := &entity.ResaleListing{Status: entity.ResaleListingStatusListed}
		assert.False(t, l.IsFreeForDrawAt(deadline, deadline))
	})
}
