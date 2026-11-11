package rdb_test

import (
	"context"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
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

// orderRefundRows counts the stored refund records of the order.
func orderRefundRows(t *testing.T, id entity.OrderID) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.Pool.QueryRow(context.Background(), `SELECT count(*) FROM order_refunds WHERE order_id = $1`, string(id)).Scan(&n))
	return n
}

// refundedOf returns the ORDER.refunded announcements of the order recorded
// after mark.
func refundedOf(t *testing.T, mark int64, id entity.OrderID) []*entityv1.Order {
	t.Helper()
	return announcedSince(t, mark, entity.SubjectOrderRefunded, func() *entityv1.Order { return &entityv1.Order{} },
		func(o *entityv1.Order) bool { return o.GetId().GetValue() == string(id) })
}

func TestRefundRepository_CommitRefund(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	ctx := context.Background()
	refunds := rdb.NewRefundRepository(testDB, testOutbox)
	orders := rdb.NewOrderRepository(testDB)
	settlements := rdb.NewSettlementRepository(testDB)
	at := saleWindowStart.Add(24 * time.Hour)
	refundAt := at.Add(time.Hour).UTC()

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
	cancellation := func(p *refundablePurchase, amount int64, providerRefundID string) entity.RefundCommit {
		return entity.RefundCommit{
			OrderID: p.orderID, Reason: entity.RefundReasonCancellation, Amount: amount,
			ProviderRefundID: providerRefundID, RefundTime: refundAt,
		}
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
		commit := cancellation(p, 6000, "re_1")
		commit.SettlementID, commit.ReversedSplits = p.settlement, reversed

		require.NoError(t, refunds.CommitRefund(ctx, commit))

		got := order(t, p)
		assert.Equal(t, entity.OrderStatusRefunded, got.Status)
		require.NotNil(t, got.Refund)
		assert.Equal(t, entity.RefundReasonCancellation, got.Refund.Reason)
		assert.Equal(t, int64(6000), got.Refund.Amount)
		assert.Equal(t, "re_1", got.Refund.ProviderRefundID)
		assert.True(t, refundAt.Equal(got.Refund.RefundTime))
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
		commit := cancellation(p, 6000, "re_2")
		commit.SettlementID = p.settlement

		require.NoError(t, refunds.CommitRefund(ctx, commit))

		assert.Equal(t, entity.TicketStatusVoided, ticketStatus(t, p.tickets[0]))
		assert.Equal(t, entity.TicketStatusVoided, ticketStatus(t, p.tickets[1]))
		assert.Equal(t, entity.ResaleListingStatusCancelled, resaleListingStatus(t, listed))
	})

	t.Run("ticket resold earlier", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Ticket resold earlier"
		p := seedRefundablePurchase(t, 2)
		sold := p.list(t, 0, entity.ResaleListingStatusSold)
		voidTickets(t, p.tickets[0])
		commit := cancellation(p, 3000, "re_3")
		commit.SettlementID = p.settlement

		require.NoError(t, refunds.CommitRefund(ctx, commit))

		assert.Equal(t, entity.TicketStatusVoided, ticketStatus(t, p.tickets[1]))
		assert.Equal(t, entity.TicketStatusVoided, ticketStatus(t, p.tickets[0]))
		assert.Equal(t, entity.ResaleListingStatusSold, resaleListingStatus(t, sold))
	})

	t.Run("no settlement", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "No settlement"
		p := seedRefundablePurchase(t, 1)

		require.NoError(t, refunds.CommitRefund(ctx, cancellation(p, 3000, "re_4")))

		assert.Equal(t, entity.OrderStatusRefunded, order(t, p).Status)
		assert.Equal(t, entity.TicketStatusVoided, ticketStatus(t, p.tickets[0]))
		assert.Equal(t, entity.SettlementStatusHeld, settlement(t, p).Status)
	})

	t.Run("dispute recorded", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Dispute recorded"
		p := seedRefundablePurchase(t, 1)

		require.NoError(t, refunds.CommitRefund(ctx, entity.RefundCommit{
			OrderID: p.orderID, Reason: entity.RefundReasonDispute, Amount: 0, RefundTime: refundAt,
		}))

		got := order(t, p)
		assert.Equal(t, entity.OrderStatusRefunded, got.Status)
		require.NotNil(t, got.Refund)
		assert.Equal(t, entity.RefundReasonDispute, got.Refund.Reason)
		assert.Equal(t, int64(0), got.Refund.Amount)
		assert.Empty(t, got.Refund.ProviderRefundID)
	})

	t.Run("reason missing", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Reason missing"
		p := seedRefundablePurchase(t, 1)

		err := refunds.CommitRefund(ctx, entity.RefundCommit{OrderID: p.orderID, Amount: 3000, ProviderRefundID: "re_7", RefundTime: refundAt})

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Equal(t, entity.OrderStatusPaid, order(t, p).Status)
		assert.Equal(t, entity.TicketStatusIssued, ticketStatus(t, p.tickets[0]))
		assert.Zero(t, orderRefundRows(t, p.orderID))
	})

	t.Run("amount below zero", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Amount below zero"
		p := seedRefundablePurchase(t, 1)

		err := refunds.CommitRefund(ctx, cancellation(p, -1, "re_9"))

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Equal(t, entity.OrderStatusPaid, order(t, p).Status)
		assert.Equal(t, entity.TicketStatusIssued, ticketStatus(t, p.tickets[0]))
		assert.Zero(t, orderRefundRows(t, p.orderID))
	})

	t.Run("order already refunded", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Order already refunded"
		p := seedRefundablePurchase(t, 1)
		require.NoError(t, refunds.CommitRefund(ctx, cancellation(p, 3000, "re_8")))

		mark := outboxMark(t)

		again := cancellation(p, 1000, "re_other")
		again.RefundTime = refundAt.Add(time.Hour)
		require.NoError(t, refunds.CommitRefund(ctx, again))

		assert.Empty(t, refundedOf(t, mark, p.orderID), "nothing is announced")
		got := order(t, p)
		assert.Equal(t, entity.OrderStatusRefunded, got.Status)
		require.NotNil(t, got.Refund)
		assert.Equal(t, entity.RefundReasonCancellation, got.Refund.Reason)
		assert.Equal(t, int64(3000), got.Refund.Amount)
		assert.Equal(t, "re_8", got.Refund.ProviderRefundID)
		assert.True(t, refundAt.Equal(got.Refund.RefundTime))
		assert.Equal(t, 1, orderRefundRows(t, p.orderID))
	})

	for _, status := range []entity.OrderStatus{entity.OrderStatusPending, entity.OrderStatusVoid} {
		t.Run("order never paid: "+status.String(), func(t *testing.T) {
			// @spec components/entity/order/commit-refund "Order never paid"
			p := seedRefundablePurchase(t, 2)
			// The Order was never paid; its Tickets and Settlement stand in
			// for rows that a refund must leave as they are.
			_, err := testDB.Pool.Exec(ctx, `UPDATE orders SET status = $2, provider_payment_id = NULL, pay_at = NULL WHERE id = $1`,
				string(p.orderID), int16(status))
			require.NoError(t, err)
			mark := outboxMark(t)

			err = refunds.CommitRefund(ctx, cancellation(p, 3000, "re_never_paid"))

			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
			assert.Equal(t, status, order(t, p).Status)
			assert.Nil(t, order(t, p).Refund)
			assert.Zero(t, orderRefundRows(t, p.orderID))
			for _, id := range p.tickets {
				assert.Equal(t, entity.TicketStatusIssued, ticketStatus(t, id))
			}
			assert.Equal(t, entity.SettlementStatusHeld, settlement(t, p).Status)
			assert.Empty(t, refundedOf(t, mark, p.orderID), "nothing is announced")
		})
	}

	t.Run("unknown order", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Unknown order"
		unknown := entity.OrderID(entity.NewID())
		mark := outboxMark(t)

		err := refunds.CommitRefund(ctx, entity.RefundCommit{
			OrderID: unknown, Reason: entity.RefundReasonCancellation, Amount: 3000,
			ProviderRefundID: "re_unknown", RefundTime: refundAt,
		})

		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Zero(t, orderRefundRows(t, unknown))
		assert.Empty(t, refundedOf(t, mark, unknown), "nothing is announced")
	})

	t.Run("settlement already reversed", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Settlement already reversed"
		p := seedRefundablePurchase(t, 1)
		listed := p.list(t, 0, entity.ResaleListingStatusListed)
		_, err := testDB.Pool.Exec(ctx, `UPDATE settlements SET status = 3 WHERE id = $1`, string(p.settlement))
		require.NoError(t, err)
		commit := cancellation(p, 3000, "re_5")
		commit.SettlementID = p.settlement

		err = refunds.CommitRefund(ctx, commit)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Equal(t, entity.OrderStatusPaid, order(t, p).Status)
		assert.Equal(t, entity.TicketStatusIssued, ticketStatus(t, p.tickets[0]))
		assert.Equal(t, entity.ResaleListingStatusListed, resaleListingStatus(t, listed))
		assert.Zero(t, orderRefundRows(t, p.orderID))
	})

	t.Run("failure changes nothing", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Failure changes nothing"
		p := seedRefundablePurchase(t, 2)
		listed := p.list(t, 0, entity.ResaleListingStatusListed)
		// Make the order update fail for this order only.
		_, err := testDB.Pool.Exec(ctx, `ALTER TABLE orders ADD CONSTRAINT chk_test_refund_fails CHECK (status <> 2 OR id <> '`+string(p.orderID)+`'::uuid) NOT VALID`)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = testDB.Pool.Exec(context.Background(), `ALTER TABLE orders DROP CONSTRAINT IF EXISTS chk_test_refund_fails`)
		})
		commit := cancellation(p, 6000, "re_6")
		commit.SettlementID = p.settlement

		err = refunds.CommitRefund(ctx, commit)

		require.Error(t, err)
		assert.Equal(t, entity.OrderStatusPaid, order(t, p).Status)
		assert.Equal(t, entity.TicketStatusIssued, ticketStatus(t, p.tickets[0]))
		assert.Equal(t, entity.TicketStatusIssued, ticketStatus(t, p.tickets[1]))
		assert.Equal(t, entity.ResaleListingStatusListed, resaleListingStatus(t, listed))
		assert.Equal(t, entity.SettlementStatusHeld, settlement(t, p).Status)
		assert.Zero(t, orderRefundRows(t, p.orderID))
	})
}

func TestRefundRepository_CommitRefund_Announces(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	ctx := context.Background()
	refunds := rdb.NewRefundRepository(testDB, testOutbox)
	orders := rdb.NewOrderRepository(testDB)
	refundAt := saleWindowStart.Add(48 * time.Hour)
	cancellation := func(p *refundablePurchase, amount int64) entity.RefundCommit {
		return entity.RefundCommit{
			OrderID: p.orderID, Reason: entity.RefundReasonCancellation, Amount: amount,
			ProviderRefundID: "re_" + string(p.orderID), RefundTime: refundAt,
		}
	}

	t.Run("refund announced", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Refund announced"
		p := seedRefundablePurchase(t, 2)
		mark := outboxMark(t)

		require.NoError(t, refunds.CommitRefund(ctx, cancellation(p, 6000)))

		got := refundedOf(t, mark, p.orderID)
		require.Len(t, got, 1)
		assert.Equal(t, entityv1.OrderStatus_ORDER_STATUS_REFUNDED, got[0].GetStatus())
		assert.Equal(t, "re_"+string(p.orderID), got[0].GetProviderRefundId().GetValue())
	})

	t.Run("recorded twice", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Recorded twice"
		p := seedRefundablePurchase(t, 1)
		require.NoError(t, refunds.CommitRefund(ctx, cancellation(p, 3000)))
		mark := outboxMark(t)

		require.NoError(t, refunds.CommitRefund(ctx, cancellation(p, 1000)))

		assert.Empty(t, refundedOf(t, mark, p.orderID))
		got, err := orders.Get(ctx, p.orderID)
		require.NoError(t, err)
		require.NotNil(t, got.Refund)
		assert.Equal(t, int64(3000), got.Refund.Amount)
		assert.True(t, refundAt.Equal(got.Refund.RefundTime))
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Announcement cannot be recorded"
		p := seedRefundablePurchase(t, 1)

		err := rdb.NewRefundRepository(testDB, failingOutbox{}).CommitRefund(ctx, cancellation(p, 3000))

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		got, err := orders.Get(ctx, p.orderID)
		require.NoError(t, err)
		assert.Equal(t, entity.OrderStatusPaid, got.Status)
		assert.Nil(t, got.Refund)
		assert.Equal(t, entity.TicketStatusIssued, ticketStatus(t, p.tickets[0]))
		assert.Zero(t, orderRefundRows(t, p.orderID))
	})
}
