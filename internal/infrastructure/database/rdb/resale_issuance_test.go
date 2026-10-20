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

// chargedResaleCheckout starts, commits and charges a resale checkout of a
// new fan for count tickets of s's ticket type at at.
func chargedResaleCheckout(t *testing.T, s *soldOutType, count int, at time.Time) *entity.Checkout {
	t.Helper()
	ctx := context.Background()
	repo := rdb.NewCheckoutRepository(testDB)
	c, err := repo.GetOrCreateHeld(ctx, s.tt.ID, nextFan(t), count, at, &resaleDeadline)
	require.NoError(t, err)
	require.NoError(t, repo.SetCardAuthorization(ctx, c.ID, "pi_"+string(c.ID)))
	_, err = repo.Commit(ctx, c.ID, at.Add(time.Minute))
	require.NoError(t, err)
	require.NoError(t, repo.RecordCapture(ctx, c.ID, at.Add(2*time.Minute)))
	return c
}

// sellerSettlement returns the Settlement of the seller's original order.
func sellerSettlement(t *testing.T, s *soldOutType) *entity.Settlement {
	t.Helper()
	got, err := rdb.NewSettlementRepository(testDB).GetByOrderID(context.Background(), s.tickets[0].OrderID)
	require.NoError(t, err)
	return got
}

// setSellerSplit overwrites the split of the seller's original settlement.
func setSellerSplit(t *testing.T, s *soldOutType, amount int64) {
	t.Helper()
	_, err := testDB.Pool.Exec(context.Background(), `
		UPDATE settlement_splits SET amount = $2
		WHERE settlement_id = (SELECT id FROM settlements WHERE order_id = $1)
	`, string(s.tickets[0].OrderID), amount)
	require.NoError(t, err)
}

// ticketStatus returns the stored status of the ticket.
func ticketStatus(t *testing.T, id entity.TicketID) entity.TicketStatus {
	t.Helper()
	var status int16
	require.NoError(t, testDB.Pool.QueryRow(context.Background(), `SELECT status FROM tickets WHERE id = $1`, string(id)).Scan(&status))
	return entity.TicketStatus(status)
}

func TestIssuanceRepository_IssueFromResaleCheckout(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	ctx := context.Background()
	issuance := rdb.NewIssuanceRepository(testDB)
	listings := rdb.NewResaleListingRepository(testDB)
	orders := rdb.NewOrderRepository(testDB)
	at := saleWindowStart.Add(24 * time.Hour)

	t.Run("order issued from a resale checkout", func(t *testing.T) {
		// @spec components/entity/order/issue "Order issued from a resale checkout"
		s := seedSoldOutType(t, 2)
		setSellerSplit(t, s, 9500)
		listed := s.list(t, 0, at)
		c := chargedResaleCheckout(t, s, 1, at.Add(time.Hour))
		paid := at.Add(time.Hour + 3*time.Minute)
		order, tickets, settlement := checkoutOrder(t, c, paid)

		require.NoError(t, issuance.Issue(ctx, order, tickets, settlement))

		got, err := orders.Get(ctx, order.ID)
		require.NoError(t, err)
		assert.Equal(t, c.UserID, got.UserID)
		l, err := listings.Get(ctx, listed.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ResaleListingStatusSold, l.Status)
		require.NotNil(t, l.SoldTime)
		assert.True(t, l.SoldTime.Equal(paid))
		assert.Equal(t, entity.TicketStatusVoided, ticketStatus(t, s.tickets[0].ID))
		assert.Equal(t, entity.TicketStatusIssued, ticketStatus(t, s.tickets[1].ID))
		original := sellerSettlement(t, s)
		assert.Equal(t, entity.SettlementStatusHeld, original.Status)
		assert.Equal(t, int64(4750), original.Splits[0].Amount)
		assert.Equal(t, entity.TicketStatusIssued, ticketStatus(t, tickets[0].ID), "the purchasing fan's new ticket")
	})

	t.Run("last ticket of the original order resold", func(t *testing.T) {
		s := seedSoldOutType(t, 1)
		s.list(t, 0, at)
		c := chargedResaleCheckout(t, s, 1, at.Add(time.Hour))
		order, tickets, settlement := checkoutOrder(t, c, at.Add(time.Hour+3*time.Minute))

		require.NoError(t, issuance.Issue(ctx, order, tickets, settlement))

		assert.Equal(t, entity.SettlementStatusReversed, sellerSettlement(t, s).Status)
	})

	t.Run("two listings of one order in one checkout", func(t *testing.T) {
		s := seedSoldOutType(t, 3)
		setSellerSplit(t, s, 9200)
		s.list(t, 0, at)
		s.list(t, 1, at.Add(time.Minute))
		c := chargedResaleCheckout(t, s, 2, at.Add(time.Hour))
		order, tickets, settlement := checkoutOrder(t, c, at.Add(time.Hour+3*time.Minute))

		require.NoError(t, issuance.Issue(ctx, order, tickets, settlement))

		// floor(9200 × 2 ÷ 3) = 6133 taken off.
		assert.Equal(t, int64(3067), sellerSettlement(t, s).Splits[0].Amount)
	})

	t.Run("both tickets of one order resold at the same time", func(t *testing.T) {
		for range 5 {
			s := seedSoldOutType(t, 2)
			setSellerSplit(t, s, 9500)
			s.list(t, 0, at)
			s.list(t, 1, at.Add(time.Minute))
			first := chargedResaleCheckout(t, s, 1, at.Add(time.Hour))
			second := chargedResaleCheckout(t, s, 1, at.Add(time.Hour))
			o1, t1, st1 := checkoutOrder(t, first, at.Add(time.Hour+3*time.Minute))
			o2, t2, st2 := checkoutOrder(t, second, at.Add(time.Hour+3*time.Minute))

			var wg sync.WaitGroup
			var err1, err2 error
			wg.Go(func() { err1 = issuance.Issue(ctx, o1, t1, st1) })
			wg.Go(func() { err2 = issuance.Issue(ctx, o2, t2, st2) })
			wg.Wait()

			require.NoError(t, err1)
			require.NoError(t, err2)
			assert.Equal(t, entity.SettlementStatusReversed, sellerSettlement(t, s).Status,
				"nothing is left to pay out once both tickets are resold")
		}
	})

	t.Run("refund and resale issuance of one order at the same time", func(t *testing.T) {
		refunds := rdb.NewRefundRepository(testDB)
		for range 5 {
			s := seedSoldOutType(t, 2)
			s.list(t, 0, at)
			c := chargedResaleCheckout(t, s, 1, at.Add(time.Hour))
			order, tickets, settlement := checkoutOrder(t, c, at.Add(time.Hour+3*time.Minute))
			sellerSettlementID := sellerSettlement(t, s).ID

			var wg sync.WaitGroup
			var issueErr, refundErr error
			wg.Go(func() { issueErr = issuance.Issue(ctx, order, tickets, settlement) })
			wg.Go(func() {
				refundErr = refunds.CommitRefund(ctx, entity.RefundCommit{
					OrderID: s.tickets[0].OrderID, ProviderRefundID: "re_x", SettlementID: sellerSettlementID,
				})
			})
			wg.Wait()

			// Whichever runs second sees the other's result; neither deadlocks.
			for _, err := range []error{issueErr, refundErr} {
				if err != nil {
					assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
				}
			}
			assert.False(t, issueErr != nil && refundErr != nil, "one of them succeeds")
		}
	})

	t.Run("failure stores nothing", func(t *testing.T) {
		// @spec components/entity/order/issue "Failure stores nothing"
		s := seedSoldOutType(t, 2)
		setSellerSplit(t, s, 9500)
		listed := s.list(t, 0, at)
		c := chargedResaleCheckout(t, s, 1, at.Add(time.Hour))
		order, tickets, settlement := checkoutOrder(t, c, at.Add(time.Hour+3*time.Minute))
		tickets[0].EventID = entity.NewID() // violates the tickets.event_id foreign key

		require.Error(t, issuance.Issue(ctx, order, tickets, settlement))

		_, err := orders.Get(ctx, order.ID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Equal(t, entity.ResaleListingStatusListed, listingStatus(t, listed.ID))
		assert.Equal(t, entity.TicketStatusIssued, ticketStatus(t, s.tickets[0].ID))
		assert.Equal(t, int64(9500), sellerSettlement(t, s).Splits[0].Amount)
		got, err := rdb.NewCheckoutRepository(testDB).Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCommitted, got.Status)
	})

	t.Run("listing cancelled before issuance", func(t *testing.T) {
		s := seedSoldOutType(t, 1)
		listed := s.list(t, 0, at)
		c := chargedResaleCheckout(t, s, 1, at.Add(time.Hour))
		_, err := testDB.Pool.Exec(ctx, `UPDATE resale_listings SET status = 4 WHERE id = $1`, string(listed.ID))
		require.NoError(t, err)
		order, tickets, settlement := checkoutOrder(t, c, at.Add(time.Hour+3*time.Minute))

		err = issuance.Issue(ctx, order, tickets, settlement)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, err = orders.Get(ctx, order.ID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestRefundRepository_CommitRefund(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	ctx := context.Background()
	refunds := rdb.NewRefundRepository(testDB)
	orders := rdb.NewOrderRepository(testDB)
	at := saleWindowStart.Add(24 * time.Hour)

	orderStatus := func(t *testing.T, s *soldOutType) entity.OrderStatus {
		t.Helper()
		o, err := orders.Get(ctx, s.tickets[0].OrderID)
		require.NoError(t, err)
		return o.Status
	}

	t.Run("refund recorded", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Refund recorded"
		s := seedSoldOutType(t, 2)
		_, err := testDB.Pool.Exec(ctx, `
			UPDATE settlements SET status = 2, release_at = $2 WHERE order_id = $1;
		`, string(s.tickets[0].OrderID), at)
		require.NoError(t, err)
		_, err = testDB.Pool.Exec(ctx, `
			UPDATE settlement_splits SET provider_payout_id = 'tr_1'
			WHERE settlement_id = (SELECT id FROM settlements WHERE order_id = $1)
		`, string(s.tickets[0].OrderID))
		require.NoError(t, err)
		settlement := sellerSettlement(t, s)
		reversed := settlement.Splits
		reversed[0].ProviderPayoutReversalID = "trr_1"

		require.NoError(t, refunds.CommitRefund(ctx, entity.RefundCommit{
			OrderID: s.tickets[0].OrderID, ProviderRefundID: "re_1", SettlementID: settlement.ID, ReversedSplits: reversed,
		}))

		assert.Equal(t, entity.OrderStatusRefunded, orderStatus(t, s))
		assert.Equal(t, entity.TicketStatusVoided, ticketStatus(t, s.tickets[0].ID))
		assert.Equal(t, entity.TicketStatusVoided, ticketStatus(t, s.tickets[1].ID))
		after := sellerSettlement(t, s)
		assert.Equal(t, entity.SettlementStatusReversed, after.Status)
		assert.Equal(t, "trr_1", after.Splits[0].ProviderPayoutReversalID)
	})

	t.Run("ticket on resale", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Ticket on resale"
		s := seedSoldOutType(t, 2)
		listed := s.list(t, 0, at)

		require.NoError(t, refunds.CommitRefund(ctx, entity.RefundCommit{
			OrderID: s.tickets[0].OrderID, ProviderRefundID: "re_2", SettlementID: sellerSettlement(t, s).ID,
		}))

		assert.Equal(t, entity.TicketStatusVoided, ticketStatus(t, s.tickets[0].ID))
		assert.Equal(t, entity.TicketStatusVoided, ticketStatus(t, s.tickets[1].ID))
		assert.Equal(t, entity.ResaleListingStatusCancelled, listingStatus(t, listed.ID))
	})

	t.Run("ticket resold earlier", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Ticket resold earlier"
		s := seedSoldOutType(t, 2)
		listed := s.list(t, 0, at)
		c := chargedResaleCheckout(t, s, 1, at.Add(time.Hour))
		order, tickets, settlement := checkoutOrder(t, c, at.Add(time.Hour+3*time.Minute))
		require.NoError(t, rdb.NewIssuanceRepository(testDB).Issue(ctx, order, tickets, settlement))

		require.NoError(t, refunds.CommitRefund(ctx, entity.RefundCommit{
			OrderID: s.tickets[0].OrderID, ProviderRefundID: "re_3", SettlementID: sellerSettlement(t, s).ID,
		}))

		assert.Equal(t, entity.TicketStatusVoided, ticketStatus(t, s.tickets[1].ID))
		assert.Equal(t, entity.ResaleListingStatusSold, listingStatus(t, listed.ID))
		assert.Equal(t, entity.TicketStatusIssued, ticketStatus(t, tickets[0].ID), "the purchasing fan's ticket is another order's")
	})

	t.Run("no settlement", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "No settlement"
		s := seedSoldOutType(t, 1)

		require.NoError(t, refunds.CommitRefund(ctx, entity.RefundCommit{OrderID: s.tickets[0].OrderID, ProviderRefundID: "re_4"}))

		assert.Equal(t, entity.OrderStatusRefunded, orderStatus(t, s))
		assert.Equal(t, entity.TicketStatusVoided, ticketStatus(t, s.tickets[0].ID))
		assert.Equal(t, entity.SettlementStatusHeld, sellerSettlement(t, s).Status)
	})

	t.Run("settlement already reversed", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Settlement already reversed"
		s := seedSoldOutType(t, 1)
		listed := s.list(t, 0, at)
		_, err := testDB.Pool.Exec(ctx, `UPDATE settlements SET status = 3 WHERE order_id = $1`, string(s.tickets[0].OrderID))
		require.NoError(t, err)

		err = refunds.CommitRefund(ctx, entity.RefundCommit{
			OrderID: s.tickets[0].OrderID, ProviderRefundID: "re_5", SettlementID: sellerSettlement(t, s).ID,
		})

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Equal(t, entity.OrderStatusPaid, orderStatus(t, s))
		assert.Equal(t, entity.TicketStatusIssued, ticketStatus(t, s.tickets[0].ID))
		assert.Equal(t, entity.ResaleListingStatusListed, listingStatus(t, listed.ID))
	})

	t.Run("failure changes nothing", func(t *testing.T) {
		// @spec components/entity/order/commit-refund "Failure changes nothing"
		s := seedSoldOutType(t, 2)
		listed := s.list(t, 0, at)
		// Make the last step, the order update, fail for this order only.
		_, err := testDB.Pool.Exec(ctx, `ALTER TABLE orders ADD CONSTRAINT chk_test_refund_fails CHECK (status <> 2 OR id <> '`+string(s.tickets[0].OrderID)+`'::uuid) NOT VALID`)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = testDB.Pool.Exec(context.Background(), `ALTER TABLE orders DROP CONSTRAINT IF EXISTS chk_test_refund_fails`)
		})

		err = refunds.CommitRefund(ctx, entity.RefundCommit{
			OrderID: s.tickets[0].OrderID, ProviderRefundID: "re_6", SettlementID: sellerSettlement(t, s).ID,
		})

		require.Error(t, err)
		assert.Equal(t, entity.OrderStatusPaid, orderStatus(t, s))
		assert.Equal(t, entity.TicketStatusIssued, ticketStatus(t, s.tickets[0].ID))
		assert.Equal(t, entity.TicketStatusIssued, ticketStatus(t, s.tickets[1].ID))
		assert.Equal(t, entity.ResaleListingStatusListed, listingStatus(t, listed.ID))
		assert.Equal(t, entity.SettlementStatusHeld, sellerSettlement(t, s).Status)
	})
}
