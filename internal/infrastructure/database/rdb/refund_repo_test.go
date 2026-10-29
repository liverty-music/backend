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

// ticketStatus returns the stored status of the ticket.
func ticketStatus(t *testing.T, id entity.TicketID) entity.TicketStatus {
	t.Helper()
	var status int16
	require.NoError(t, testDB.Pool.QueryRow(context.Background(), `SELECT status FROM tickets WHERE id = $1`, string(id)).Scan(&status))
	return entity.TicketStatus(status)
}

// refundablePurchase is a Paid Order with its Issued Tickets and its Held
// Settlement of one split.
type refundablePurchase struct {
	stock      *stockFixture
	orderID    entity.OrderID
	tickets    []entity.TicketID
	settlement entity.SettlementID
}

// seedRefundablePurchase stores a Paid Order of n Issued Tickets of a new
// stock for a new fan, with its Held Settlement paying 2700 yen to one
// Organizer.
func seedRefundablePurchase(t *testing.T, n int) *refundablePurchase {
	t.Helper()
	ctx := context.Background()
	f := seedStock(t, entity.TicketSaleMethodFirstCome, 10, 4)
	fan := seedFanUser(t)
	tickets := seedIssuedTicketsOf(t, f, fan, n)
	var orderID string
	require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT order_id FROM tickets WHERE id = $1`, string(tickets[0])).Scan(&orderID))
	settlementID, organizerID := entity.NewID(), seedOrganizer(t)
	_, err := testDB.Pool.Exec(ctx, `
		INSERT INTO settlements (id, order_id, organizer_id, event_id, status, created_at) VALUES ($1, $2, $3, $4, 1, $5)`,
		settlementID, orderID, organizerID, f.EventID, issuedTicketsTime)
	require.NoError(t, err)
	insertSettlementSplit(t, entity.SettlementID(settlementID), organizerID, 2700)
	return &refundablePurchase{stock: f, orderID: entity.OrderID(orderID), tickets: tickets, settlement: entity.SettlementID(settlementID)}
}

// list stores a resale listing of the purchase's i-th ticket in the status,
// sold at the listing time when Sold, and returns its id.
func (p *refundablePurchase) list(t *testing.T, i int, status entity.ResaleListingStatus) entity.ResaleListingID {
	t.Helper()
	id := entity.NewID()
	listedAt := issuedTicketsTime.Add(time.Hour)
	var soldAt any
	if status == entity.ResaleListingStatusSold {
		soldAt = listedAt
	}
	_, err := testDB.Pool.Exec(context.Background(), `
		INSERT INTO resale_listings (id, ticket_id, user_id, price, status, created_at, sold_at, ticket_stock_id)
		SELECT $1, t.id, t.user_id, $3, $4, $5, $6, $7 FROM tickets t WHERE t.id = $2`,
		id, string(p.tickets[i]), p.stock.Price(), int16(status), listedAt, soldAt, string(p.stock.ID))
	require.NoError(t, err)
	return entity.ResaleListingID(id)
}

// resaleListingStatus returns the stored status of the listing.
func resaleListingStatus(t *testing.T, id entity.ResaleListingID) entity.ResaleListingStatus {
	t.Helper()
	var status int16
	require.NoError(t, testDB.Pool.QueryRow(context.Background(), `SELECT status FROM resale_listings WHERE id = $1`, string(id)).Scan(&status))
	return entity.ResaleListingStatus(status)
}

func TestRefundRepository_CommitRefund(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	ctx := context.Background()
	refunds := rdb.NewRefundRepository(testDB)
	orders := rdb.NewOrderRepository(testDB)
	settlements := rdb.NewSettlementRepository(testDB)
	at := saleWindowStart.Add(24 * time.Hour)

	order := func(t *testing.T, p *refundablePurchase) *entity.Order {
		t.Helper()
		o, err := orders.Get(ctx, p.orderID)
		require.NoError(t, err)
		return o
	}
	settlement := func(t *testing.T, p *refundablePurchase) *entity.Settlement {
		t.Helper()
		s, err := settlements.GetByOrderID(ctx, p.orderID)
		require.NoError(t, err)
		return s
	}

	t.Run("refund recorded", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Refund recorded"
		p := seedRefundablePurchase(t, 2)
		_, err := testDB.Pool.Exec(ctx, `UPDATE settlements SET status = 2, release_at = $2 WHERE id = $1`, string(p.settlement), at)
		require.NoError(t, err)
		_, err = testDB.Pool.Exec(ctx, `UPDATE settlement_splits SET provider_payout_id = 'tr_1' WHERE settlement_id = $1`, string(p.settlement))
		require.NoError(t, err)
		reversed := settlement(t, p).Splits
		reversed[0].ProviderPayoutReversalID = "trr_1"

		require.NoError(t, refunds.CommitRefund(ctx, entity.RefundCommit{
			OrderID: p.orderID, ProviderRefundID: "re_1", SettlementID: p.settlement, ReversedSplits: reversed,
		}))

		got := order(t, p)
		assert.Equal(t, entity.OrderStatusRefunded, got.Status)
		assert.Equal(t, "re_1", got.ProviderRefundID)
		assert.Equal(t, entity.TicketStatusVoided, ticketStatus(t, p.tickets[0]))
		assert.Equal(t, entity.TicketStatusVoided, ticketStatus(t, p.tickets[1]))
		after := settlement(t, p)
		assert.Equal(t, entity.SettlementStatusReversed, after.Status)
		assert.Equal(t, "trr_1", after.Splits[0].ProviderPayoutReversalID)
	})

	t.Run("ticket on resale", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Ticket on resale"
		p := seedRefundablePurchase(t, 2)
		listed := p.list(t, 0, entity.ResaleListingStatusListed)

		require.NoError(t, refunds.CommitRefund(ctx, entity.RefundCommit{
			OrderID: p.orderID, ProviderRefundID: "re_2", SettlementID: p.settlement,
		}))

		assert.Equal(t, entity.TicketStatusVoided, ticketStatus(t, p.tickets[0]))
		assert.Equal(t, entity.TicketStatusVoided, ticketStatus(t, p.tickets[1]))
		assert.Equal(t, entity.ResaleListingStatusCancelled, resaleListingStatus(t, listed))
	})

	t.Run("ticket resold earlier", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Ticket resold earlier"
		p := seedRefundablePurchase(t, 2)
		sold := p.list(t, 0, entity.ResaleListingStatusSold)
		voidTickets(t, p.tickets[0])

		require.NoError(t, refunds.CommitRefund(ctx, entity.RefundCommit{
			OrderID: p.orderID, ProviderRefundID: "re_3", SettlementID: p.settlement,
		}))

		assert.Equal(t, entity.TicketStatusVoided, ticketStatus(t, p.tickets[1]))
		assert.Equal(t, entity.TicketStatusVoided, ticketStatus(t, p.tickets[0]))
		assert.Equal(t, entity.ResaleListingStatusSold, resaleListingStatus(t, sold))
	})

	t.Run("no settlement", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "No settlement"
		p := seedRefundablePurchase(t, 1)

		require.NoError(t, refunds.CommitRefund(ctx, entity.RefundCommit{OrderID: p.orderID, ProviderRefundID: "re_4"}))

		assert.Equal(t, entity.OrderStatusRefunded, order(t, p).Status)
		assert.Equal(t, entity.TicketStatusVoided, ticketStatus(t, p.tickets[0]))
		assert.Equal(t, entity.SettlementStatusHeld, settlement(t, p).Status)
	})

	t.Run("settlement already reversed", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Settlement already reversed"
		p := seedRefundablePurchase(t, 1)
		listed := p.list(t, 0, entity.ResaleListingStatusListed)
		_, err := testDB.Pool.Exec(ctx, `UPDATE settlements SET status = 3 WHERE id = $1`, string(p.settlement))
		require.NoError(t, err)

		err = refunds.CommitRefund(ctx, entity.RefundCommit{
			OrderID: p.orderID, ProviderRefundID: "re_5", SettlementID: p.settlement,
		})

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Equal(t, entity.OrderStatusPaid, order(t, p).Status)
		assert.Equal(t, entity.TicketStatusIssued, ticketStatus(t, p.tickets[0]))
		assert.Equal(t, entity.ResaleListingStatusListed, resaleListingStatus(t, listed))
	})

	t.Run("failure changes nothing", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Failure changes nothing"
		p := seedRefundablePurchase(t, 2)
		listed := p.list(t, 0, entity.ResaleListingStatusListed)
		// Make the last step, the order update, fail for this order only.
		_, err := testDB.Pool.Exec(ctx, `ALTER TABLE orders ADD CONSTRAINT chk_test_refund_fails CHECK (status <> 2 OR id <> '`+string(p.orderID)+`'::uuid) NOT VALID`)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = testDB.Pool.Exec(context.Background(), `ALTER TABLE orders DROP CONSTRAINT IF EXISTS chk_test_refund_fails`)
		})

		err = refunds.CommitRefund(ctx, entity.RefundCommit{
			OrderID: p.orderID, ProviderRefundID: "re_6", SettlementID: p.settlement,
		})

		require.Error(t, err)
		assert.Equal(t, entity.OrderStatusPaid, order(t, p).Status)
		assert.Equal(t, entity.TicketStatusIssued, ticketStatus(t, p.tickets[0]))
		assert.Equal(t, entity.TicketStatusIssued, ticketStatus(t, p.tickets[1]))
		assert.Equal(t, entity.ResaleListingStatusListed, resaleListingStatus(t, listed))
		assert.Equal(t, entity.SettlementStatusHeld, settlement(t, p).Status)
	})
}
