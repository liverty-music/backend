package entity_test

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
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

	t.Run("start time with seconds", func(t *testing.T) {
		t.Parallel()
		// The schedule runs on whole minutes: a start at 14:00:30 has its last
		// draw at 11:00, not at 11:00:30, which no run would reach.
		start := novJST(10, 14).Add(30 * time.Second)
		draw, last := entity.ResaleDrawAt(novJST(10, 11), start)
		assert.True(t, draw)
		assert.True(t, last)
	})

	t.Run("other times", func(t *testing.T) {
		t.Parallel()
		draw, _ := entity.ResaleDrawAt(novJST(9, 13), novJST(10, 19))
		assert.False(t, draw)
		draw, _ = entity.ResaleDrawAt(novJST(11, 12), novJST(10, 19))
		assert.False(t, draw, "after the last draw")
	})

}

func TestResaleLastDrawMissed(t *testing.T) {
	t.Parallel()
	assert.False(t, entity.ResaleLastDrawMissed(novJST(10, 12), novJST(10, 19)), "at the deadline the last draw runs")
	assert.True(t, entity.ResaleLastDrawMissed(novJST(10, 12).Add(time.Minute), novJST(10, 19)))
	assert.False(t, entity.ResaleLastDrawMissed(novJST(9, 12), novJST(10, 19)))
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
		assert.ErrorIs(t, e.RevertWin(entity.ResaleEntryLostReasonCardNotCharged), apperr.ErrFailedPrecondition)
		e.State, e.Order.Status = entity.ResaleEntryStateWon, entity.OrderStatusPaid
		assert.ErrorIs(t, e.RevertWin(entity.ResaleEntryLostReasonCardNotCharged), apperr.ErrFailedPrecondition)
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

	t.Run("the last draw at the deadline", func(t *testing.T) {
		t.Parallel()
		l := &entity.ResaleListing{Status: entity.ResaleListingStatusListed}
		assert.True(t, l.IsFreeForDrawAt(deadline, deadline), "the last draw still allocates it")
		assert.False(t, l.IsWithdrawableAt(deadline, deadline), "but it can no longer be withdrawn")
		assert.False(t, l.IsFreeForDrawAt(deadline.Add(time.Minute), deadline))
	})
}

func TestResaleEntry_CheckChargeable(t *testing.T) {
	t.Parallel()
	for _, state := range []entity.ResaleEntryState{entity.ResaleEntryStateEntered, entity.ResaleEntryStateWon, entity.ResaleEntryStateLost} {
		for _, status := range []entity.OrderStatus{entity.OrderStatusPending, entity.OrderStatusPaid, entity.OrderStatusVoid} {
			e := &entity.ResaleEntry{State: state, Order: &entity.Order{Status: status}}
			err := e.CheckChargeable()
			if state == entity.ResaleEntryStateWon && status == entity.OrderStatusPending {
				assert.NoError(t, err)
				continue
			}
			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, state.String()+"/"+status.String())
		}
	}
}

func TestResaleCharge(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 12, 1, 18, 0, 0, 0, time.UTC)
	before, after := start.Add(-time.Minute), start.Add(time.Minute)

	t.Run("gate", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name      string
			published bool
			now       time.Time
			want      entity.ResaleChargeGate
		}{
			{"published before the start", true, before, entity.ResaleChargeGateOpen},
			{"published at the start", true, start, entity.ResaleChargeGateShowBegan},
			{"published after the start", true, after, entity.ResaleChargeGateShowBegan},
			{"stopped before the start", false, before, entity.ResaleChargeGateConcertStopped},
			{"stopped after the start: the concert comes first", false, after, entity.ResaleChargeGateConcertStopped},
		}
		for _, tt := range tests {
			assert.Equal(t, tt.want, entity.ResaleChargeGateAt(tt.published, tt.now, start), tt.name)
		}
	})

	t.Run("outcome and lost reason", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name       string
			gate       entity.ResaleChargeGate
			charged    bool
			wantResult entity.ResaleChargeOutcome
			wantReason entity.ResaleEntryLostReason
		}{
			{"charged while open", entity.ResaleChargeGateOpen, true, entity.ResaleChargeOutcomeCharged, entity.ResaleEntryLostReasonUnspecified},
			{"card cannot be charged", entity.ResaleChargeGateOpen, false, entity.ResaleChargeOutcomeCardNotCharged, entity.ResaleEntryLostReasonCardNotCharged},
			{"stopped, already captured", entity.ResaleChargeGateConcertStopped, true, entity.ResaleChargeOutcomeCharged, entity.ResaleEntryLostReasonUnspecified},
			{"stopped, nothing charged", entity.ResaleChargeGateConcertStopped, false, entity.ResaleChargeOutcomeConcertCancelled, entity.ResaleEntryLostReasonConcertCancelled},
			{"show began", entity.ResaleChargeGateShowBegan, false, entity.ResaleChargeOutcomeShowBegan, entity.ResaleEntryLostReasonPaymentNotCompletedBeforeShow},
			{"show began, whatever the charge says", entity.ResaleChargeGateShowBegan, true, entity.ResaleChargeOutcomeShowBegan, entity.ResaleEntryLostReasonPaymentNotCompletedBeforeShow},
		}
		for _, tt := range tests {
			got := entity.ResaleChargeOutcomeAt(tt.gate, tt.charged)
			assert.Equal(t, tt.wantResult, got, tt.name)
			assert.Equal(t, tt.wantReason, got.LostReason(), tt.name)
		}
	})
}

func TestResaleEntry_CheckCreate(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 12, 20, 9, 0, 0, 0, time.UTC) // 18:00 JST; the deadline is noon JST
	deadline := entity.ResaleDeadlineAt(start)
	soldOut := &entity.TicketStock{ID: "stock-1", SeatTypeID: "seat-1", Quantity: 4, SoldCount: 4}
	event := func(mutate func(*entity.Event)) *entity.Event {
		e := &entity.Event{StartTime: &start}
		if mutate != nil {
			mutate(e)
		}
		return e
	}
	entry := func(at time.Time, count int) *entity.ResaleEntry {
		return &entity.ResaleEntry{CreateTime: at, Order: &entity.Order{TicketCount: count}}
	}
	seat := entity.SeatTypeID("seat-1")
	other := entity.SeatTypeID("seat-2")

	tests := []struct {
		name     string
		entry    *entity.ResaleEntry
		snapshot entity.ResaleCreateSnapshot
		wantErr  error
	}{
		{"open", entry(deadline.Add(-time.Hour), 2), entity.ResaleCreateSnapshot{Stock: soldOut, Event: event(nil)}, nil},
		{"event without a start time", entry(deadline.Add(-time.Hour), 1), entity.ResaleCreateSnapshot{Stock: soldOut, Event: &entity.Event{}}, apperr.ErrInternal},
		{"at the deadline", entry(deadline, 1), entity.ResaleCreateSnapshot{Stock: soldOut, Event: event(nil)}, apperr.ErrFailedPrecondition},
		{"stock not sold out", entry(deadline.Add(-time.Hour), 1), entity.ResaleCreateSnapshot{Stock: &entity.TicketStock{Quantity: 4, SoldCount: 3}, Event: event(nil)}, apperr.ErrFailedPrecondition},
		{"cancelled event", entry(deadline.Add(-time.Hour), 1), entity.ResaleCreateSnapshot{Stock: soldOut, Event: event(func(e *entity.Event) {
			state := entity.PublishStateCancelled
			e.PublishState = &state
		})}, apperr.ErrFailedPrecondition},
		{"already entered", entry(deadline.Add(-time.Hour), 1), entity.ResaleCreateSnapshot{Stock: soldOut, Event: event(nil), EnteredForEvent: true}, apperr.ErrAlreadyExists},
		{"over the limit", entry(deadline.Add(-time.Hour), 2), entity.ResaleCreateSnapshot{Stock: soldOut, Event: event(nil), Held: entity.HeldTickets{Count: 3, SeatTypeID: &seat}}, apperr.ErrFailedPrecondition},
		{"another seat type held", entry(deadline.Add(-time.Hour), 1), entity.ResaleCreateSnapshot{Stock: soldOut, Event: event(nil), Held: entity.HeldTickets{Count: 1, SeatTypeID: &other}}, apperr.ErrFailedPrecondition},
		{"fits what is held", entry(deadline.Add(-time.Hour), 1), entity.ResaleCreateSnapshot{Stock: soldOut, Event: event(nil), Held: entity.HeldTickets{Count: 3, SeatTypeID: &seat}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.entry.CheckCreate(tt.snapshot)
			if tt.wantErr == nil {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestResaleEntry_Drawn(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 12, 19, 3, 0, 0, 0, time.UTC)
	entry := &entity.ResaleEntry{State: entity.ResaleEntryStateEntered, Order: &entity.Order{Status: entity.OrderStatusPending}}

	won := entry.Drawn(entity.ResaleEntryStateWon, entity.ResaleEntryLostReasonUnspecified, at)
	assert.Equal(t, entity.ResaleEntryStateWon, won.State)
	assert.Equal(t, entity.OrderStatusPending, won.Order.Status, "a Won entry's Order waits to be charged")

	lost := entry.Drawn(entity.ResaleEntryStateLost, entity.ResaleEntryLostReasonNoTicketLeft, at)
	assert.Equal(t, entity.ResaleEntryLostReasonNoTicketLeft, lost.LostReason)
	assert.Equal(t, entity.OrderStatusVoid, lost.Order.Status)

	lapsed := entry.Drawn(entity.ResaleEntryStateLapsed, entity.ResaleEntryLostReasonNoTicketLeft, at)
	assert.Equal(t, entity.ResaleEntryLostReasonUnspecified, lapsed.LostReason, "only a Lost entry has a reason")
	assert.Equal(t, entity.OrderStatusVoid, lapsed.Order.Status)

	for _, drawn := range []*entity.ResaleEntry{won, lost, lapsed} {
		require.NotNil(t, drawn.DrawTime)
		assert.Equal(t, at, *drawn.DrawTime)
	}
	assert.Equal(t, entity.ResaleEntryStateEntered, entry.State, "the entry itself is not changed")
	assert.Equal(t, entity.OrderStatusPending, entry.Order.Status)
}

func TestResaleDrawDue(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 12, 20, 9, 0, 0, 0, time.UTC) // 18:00 JST
	deadline := entity.ResaleDeadlineAt(start)
	noon := time.Date(2026, 12, 18, 12, 0, 0, 0, time.FixedZone("JST", 9*3600))
	tests := []struct {
		name              string
		at                time.Time
		wantDue, wantLast bool
		wantMissed        bool
	}{
		{"a daily draw at noon", noon, true, false, false},
		{"between draws", noon.Add(time.Minute), false, false, false},
		{"the last draw at the deadline", deadline, true, true, false},
		{"past the deadline: the last draw was missed", deadline.Add(time.Minute), true, false, true},
	}
	for _, tt := range tests {
		due, last, missed := entity.ResaleDrawDue(tt.at, start)
		assert.Equal(t, tt.wantDue, due, tt.name)
		assert.Equal(t, tt.wantLast, last, tt.name)
		assert.Equal(t, tt.wantMissed, missed, tt.name)
	}
}

func TestShuffleDrawOrder_Resale(t *testing.T) {
	t.Parallel()

	entries := []*entity.ResaleEntry{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	got := entity.ShuffleDrawOrder(entries, rand.New(rand.NewPCG(1, 2)))

	assert.ElementsMatch(t, entries, got, "every entry takes part once")
	assert.Equal(t, entity.ResaleEntryID("a"), entries[0].ID, "the input is left as it is")
	assert.Equal(t, got, entity.ShuffleDrawOrder(entries, rand.New(rand.NewPCG(1, 2))), "the same seed gives the same order")
}

func TestResaleEntry_NextDrawTimeAndEndedEmail(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 12, 20, 9, 0, 0, 0, time.UTC)
	now := time.Date(2026, 12, 10, 0, 0, 0, 0, time.UTC)
	wantNext, ok := entity.NextResaleDrawTime(now, start)
	require.True(t, ok)

	for state, want := range map[entity.ResaleEntryState]struct{ next, ended bool }{
		entity.ResaleEntryStateEntered:   {true, false},
		entity.ResaleEntryStateWon:       {false, false},
		entity.ResaleEntryStateLost:      {false, true},
		entity.ResaleEntryStateLapsed:    {false, true},
		entity.ResaleEntryStateWithdrawn: {false, false},
	} {
		e := &entity.ResaleEntry{State: state}
		next := e.NextDrawTime(now, &start)
		if want.next {
			require.NotNil(t, next, state.String())
			assert.Equal(t, wantNext, *next, state.String())
		} else {
			assert.Nil(t, next, state.String())
		}
		assert.Equal(t, want.ended, e.NeedsEndedEmail(), state.String())
	}

	entered := &entity.ResaleEntry{State: entity.ResaleEntryStateEntered}
	assert.Nil(t, entered.NextDrawTime(now, nil), "an event without a start time has no draw")
	assert.Nil(t, entered.NextDrawTime(start, &start), "the last draw has passed")
}
