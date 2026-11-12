package rdb_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

// TestOrderCountsParity checks that the queries behind the per-account limit
// (GetHeldForEvent) and the stock's sold and held counts count exactly what
// the entity predicates Order.CountsTowardLimitAt, IsSold and IsHeldAt accept.
// It stores one Order of 3 tickets for every combination of the Order's
// status and the state of its source (a Checkout, a LotteryEntry or a
// ResaleEntry), each for a fan and a stock of its own, and compares what the
// queries return for it with what the predicates say. A Paid Order has 2
// Issued and 1 Voided ticket, so it counts the Issued ones.
func TestOrderCountsParity(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	eventID := seedPublishedEvent(t, db)
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	const tickets, issued = 3, 2

	statuses := []entity.OrderStatus{entity.OrderStatusPending, entity.OrderStatusPaid, entity.OrderStatusRefunded, entity.OrderStatusVoid}
	type combo struct {
		name   string
		method entity.TicketSaleMethod
		source func(t *testing.T, f *stockFixture, orderID entity.OrderID) entity.OrderSourceState
	}
	var combos []combo
	checkoutCases := []struct {
		name   string
		status entity.CheckoutStatus
		expire time.Time
	}{
		{"holding", entity.CheckoutStatusHeld, now.Add(time.Minute)},
		{"lapsed", entity.CheckoutStatusHeld, now.Add(-time.Minute)},
		{"committed", entity.CheckoutStatusCommitted, now.Add(-time.Minute)},
		{"completed", entity.CheckoutStatusCompleted, now.Add(-time.Minute)},
		{"expired", entity.CheckoutStatusExpired, now.Add(-time.Minute)},
		{"released", entity.CheckoutStatusReleased, now.Add(-time.Minute)},
	}
	for _, cc := range checkoutCases {
		combos = append(combos, combo{"checkout " + cc.name, entity.TicketSaleMethodFirstCome, func(t *testing.T, f *stockFixture, orderID entity.OrderID) entity.OrderSourceState {
			var commitAt, captureAt any
			switch cc.status {
			case entity.CheckoutStatusCommitted:
				commitAt = cc.expire
			case entity.CheckoutStatusCompleted:
				commitAt, captureAt = cc.expire, cc.expire
			}
			id := entity.NewID()
			_, err := db.Pool.Exec(ctx, `
				INSERT INTO checkouts (id, order_id, ticket_sale_id, status, hold_expire_at, commit_at, capture_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				id, string(orderID), string(f.SaleID), int16(cc.status), cc.expire, commitAt, captureAt)
			require.NoError(t, err)
			return entity.OrderSourceState{Checkout: &entity.Checkout{ID: entity.CheckoutID(id), Status: cc.status, HoldExpireTime: cc.expire}}
		}})
	}
	for _, state := range []entity.LotteryEntryState{entity.LotteryEntryStateEntered, entity.LotteryEntryStateWon, entity.LotteryEntryStateLost} {
		combos = append(combos, combo{"lottery entry " + state.String(), entity.TicketSaleMethodLottery, func(t *testing.T, f *stockFixture, orderID entity.OrderID) entity.OrderSourceState {
			var lostReason any
			if state == entity.LotteryEntryStateLost {
				lostReason = int16(entity.LotteryEntryLostReasonNotDrawn)
			}
			_, err := db.Pool.Exec(ctx, `INSERT INTO lottery_entries (id, state, order_id, ticket_sale_id, lost_reason) VALUES ($1, $2, $3, $4, $5)`,
				entity.NewID(), int16(state), string(orderID), string(f.SaleID), lostReason)
			require.NoError(t, err)
			return entity.OrderSourceState{LotteryEntry: &entity.LotteryEntry{State: state}}
		}})
	}
	for _, state := range []entity.ResaleEntryState{entity.ResaleEntryStateEntered, entity.ResaleEntryStateWon, entity.ResaleEntryStateLost, entity.ResaleEntryStateLapsed} {
		combos = append(combos, combo{"resale entry " + state.String(), entity.TicketSaleMethodLottery, func(t *testing.T, f *stockFixture, orderID entity.OrderID) entity.OrderSourceState {
			id := entity.NewID()
			var drawAt, lostReason any
			if state != entity.ResaleEntryStateEntered {
				drawAt = now.Add(-time.Hour)
			}
			if state == entity.ResaleEntryStateLost {
				lostReason = int16(entity.ResaleEntryLostReasonNoTicketLeft)
			}
			// The Order's status is the combination's own, not the one the
			// entry's state would give it.
			_, err := db.Pool.Exec(ctx, `INSERT INTO resale_entries (id, order_id, state, draw_at, lost_reason, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
				id, string(orderID), int16(state), drawAt, lostReason, now.Add(-2*time.Hour))
			require.NoError(t, err)
			return entity.OrderSourceState{ResaleEntry: &entity.ResaleEntry{State: state}}
		}})
	}

	checked := 0
	for _, c := range combos {
		for _, status := range statuses {
			name := fmt.Sprintf("%s, order %s", c.name, status)
			f := seedStockForEvent(t, db, eventID, c.method, 100, 10)
			user := newUserID(t, db)
			orderID := insertOrder(t, db, f, orderSeed{user: user, count: tickets, status: status})
			src := c.source(t, f, orderID)
			if status == entity.OrderStatusPaid {
				for i := range tickets {
					ticketStatus := 1
					if i >= issued {
						ticketStatus = 2
					}
					_, err := db.Pool.Exec(ctx, `INSERT INTO tickets (id, order_id, user_id, event_id, status, issue_at) VALUES ($1, $2, $3, $4, $5, $6)`,
						entity.NewID(), string(orderID), string(user), eventID, ticketStatus, issuedTicketsTime)
					require.NoError(t, err, name)
				}
			}
			order := &entity.Order{ID: orderID, Status: status, TicketCount: tickets}

			wantHeld := 0
			if order.CountsTowardLimitAt(now, src) {
				wantHeld = tickets
				if status == entity.OrderStatusPaid {
					wantHeld = issued
				}
			}
			got, err := rdb.NewOrderRepository(db).GetHeldForEvent(ctx, user, eventID, nil, now)
			require.NoError(t, err, name)
			assert.Equal(t, wantHeld, got.Count, "per-account limit count: "+name)

			sold, held := stockCounts(t, db, f.ID, now)
			wantSold, wantStockHeld := 0, 0
			if order.IsSold(src) {
				wantSold = tickets
			}
			if order.IsHeldAt(now, src) {
				wantStockHeld = tickets
			}
			assert.Equal(t, wantSold, sold, "stock sold count: "+name)
			assert.Equal(t, wantStockHeld, held, "stock held count: "+name)
			checked++
		}
	}
	assert.Equal(t, len(combos)*len(statuses), checked)
}
