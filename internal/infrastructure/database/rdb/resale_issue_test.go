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

// resoldSeller is a fan's Paid Order of 2 Tickets with a Held split of 9500
// yen, one Ticket of which is listed.
type resoldSeller struct {
	orderID      entity.OrderID
	settlementID string
	listedTicket string
	listing      entity.ResaleListingID
}

func seedResoldSeller(t *testing.T, f *stockFixture) resoldSeller {
	t.Helper()
	ctx := context.Background()
	seller := newUserID(t)
	s := resoldSeller{orderID: seedPaidOrder(t, f, string(seller), 2, issuedTicketsTime)}
	exec := func(q string, args ...any) {
		t.Helper()
		_, err := testDB.Pool.Exec(ctx, q, args...)
		require.NoError(t, err)
	}
	tickets := []string{entity.NewID(), entity.NewID()}
	for _, id := range tickets {
		exec(`INSERT INTO tickets (id, order_id, user_id, event_id, status, issue_at) VALUES ($1, $2, $3, $4, 1, $5)`,
			id, string(s.orderID), string(seller), f.EventID, issuedTicketsTime)
	}
	s.listedTicket = tickets[0]
	organizerID := seedOrganizer(t)
	s.settlementID = entity.NewID()
	exec(`INSERT INTO settlements (id, order_id, organizer_id, event_id, status, created_at) VALUES ($1, $2, $3, $4, 1, $5)`,
		s.settlementID, string(s.orderID), organizerID, f.EventID, issuedTicketsTime)
	exec(`INSERT INTO settlement_splits (settlement_id, payee_organizer_id, amount) VALUES ($1, $2, 9500)`, s.settlementID, organizerID)
	s.listing = entity.ResaleListingID(entity.NewID())
	exec(`INSERT INTO resale_listings (id, ticket_id, user_id, ticket_stock_id, price, status, created_at) VALUES ($1, $2, $3, $4, 3000, 1, $5)`,
		string(s.listing), s.listedTicket, string(seller), string(f.ID), resaleApplyTime)
	return s
}

func TestIssuanceRepository_IssueResaleEntry(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	entries := rdb.NewResaleEntryRepository(testDB, testOutbox)
	issuance := rdb.NewIssuanceRepository(testDB, testOutbox)
	orders := rdb.NewOrderRepository(testDB)

	t.Run("order issued from a won resale entry", func(t *testing.T) {
		// @spec components/entity/order/issue "Order issued from a won resale entry"
		f := seedResaleStock(t, 4, 4)
		seller := seedResoldSeller(t, f)
		entry := enterResale(t, entries, f, 1)
		require.NoError(t, entries.PersistDrawOutcome(ctx, f.ID, resaleDrawTime, entity.ResaleDrawOutcome{
			Winners: []entity.ResaleWin{{Entry: entry, Listings: []entity.ResaleListingID{seller.listing}}},
		}))
		order, issued, settlement := issuanceOf(t, entry.Order, f.EventID)
		mark := outboxMark(t)

		require.NoError(t, issuance.Issue(ctx, order, issued, settlement))

		got, err := orders.Get(ctx, order.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.OrderStatusPaid, got.Status)
		var listingStatus, ticketStatus int16
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT status FROM resale_listings WHERE id = $1`, string(seller.listing)).Scan(&listingStatus))
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT status FROM tickets WHERE id = $1`, seller.listedTicket).Scan(&ticketStatus))
		assert.Equal(t, entity.ResaleListingStatusSold, entity.ResaleListingStatus(listingStatus))
		assert.Equal(t, entity.TicketStatusVoided, entity.TicketStatus(ticketStatus), "the other fan's Ticket is Voided")
		var split int64
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT amount FROM settlement_splits WHERE settlement_id = $1`, seller.settlementID).Scan(&split))
		assert.Equal(t, int64(4750), split, "their Order's split gives up the resold ticket's share")
		sold := announcedSince(t, mark, entity.SubjectResaleListingSold, func() *entityv1.ResaleListing { return &entityv1.ResaleListing{} },
			func(l *entityv1.ResaleListing) bool { return l.GetId().GetValue() == string(seller.listing) })
		require.Len(t, sold, 1)
		assert.Equal(t, entityv1.ResaleListingStatus_RESALE_LISTING_STATUS_SOLD, sold[0].GetStatus())
	})

	refunds := rdb.NewRefundRepository(testDB, testOutbox)
	listingStatus := func(t *testing.T, id entity.ResaleListingID) entity.ResaleListingStatus {
		t.Helper()
		var status int16
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT status FROM resale_listings WHERE id = $1`, string(id)).Scan(&status))
		return entity.ResaleListingStatus(status)
	}

	t.Run("listing fan's order refunded before the sell", func(t *testing.T) {
		f := seedResaleStock(t, 4, 4)
		seller := seedResoldSeller(t, f)
		entry := enterResale(t, entries, f, 1)
		require.NoError(t, entries.PersistDrawOutcome(ctx, f.ID, resaleDrawTime, entity.ResaleDrawOutcome{
			Winners: []entity.ResaleWin{{Entry: entry, Listings: []entity.ResaleListingID{seller.listing}}},
		}))
		require.NoError(t, refunds.CommitRefund(ctx, entity.RefundCommit{OrderID: seller.orderID, Reason: entity.RefundReasonCancellation, Amount: 3000, ProviderRefundID: "re_seller", RefundTime: saleWindowStart}))
		order, issued, settlement := issuanceOf(t, entry.Order, f.EventID)

		err := issuance.Issue(ctx, order, issued, settlement)

		require.ErrorIs(t, err, entity.ErrResaleListingCancelled)
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		got, err := orders.Get(ctx, order.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.OrderStatusPending, got.Status, "nothing changes")
		assert.Equal(t, entity.ResaleListingStatusCancelled, listingStatus(t, seller.listing))
	})

	t.Run("refund and sell run concurrently", func(t *testing.T) {
		// Whichever commits second sees the other's result: either the
		// listing is Sold and the Order Paid, or it is Cancelled and the sell
		// fails with ErrResaleListingCancelled. Never both, never neither.
		for range 5 {
			f := seedResaleStock(t, 4, 4)
			seller := seedResoldSeller(t, f)
			entry := enterResale(t, entries, f, 1)
			require.NoError(t, entries.PersistDrawOutcome(ctx, f.ID, resaleDrawTime, entity.ResaleDrawOutcome{
				Winners: []entity.ResaleWin{{Entry: entry, Listings: []entity.ResaleListingID{seller.listing}}},
			}))
			order, issued, settlement := issuanceOf(t, entry.Order, f.EventID)

			start := make(chan struct{})
			issueErr, refundErr := make(chan error, 1), make(chan error, 1)
			go func() {
				<-start
				issueErr <- issuance.Issue(ctx, order, issued, settlement)
			}()
			go func() {
				<-start
				refundErr <- refunds.CommitRefund(ctx, entity.RefundCommit{OrderID: seller.orderID, Reason: entity.RefundReasonCancellation, Amount: 3000, ProviderRefundID: "re_seller", RefundTime: saleWindowStart})
			}()
			close(start)

			require.NoError(t, <-refundErr, "the refund is never refused")
			err := <-issueErr
			got, gerr := orders.Get(ctx, order.ID)
			require.NoError(t, gerr)
			if err == nil {
				assert.Equal(t, entity.ResaleListingStatusSold, listingStatus(t, seller.listing), "the sell won")
				assert.Equal(t, entity.OrderStatusPaid, got.Status)
				continue
			}
			require.ErrorIs(t, err, entity.ErrResaleListingCancelled, "the refund won")
			assert.Equal(t, entity.ResaleListingStatusCancelled, listingStatus(t, seller.listing))
			assert.Equal(t, entity.OrderStatusPending, got.Status)
		}
	})

	t.Run("entry no longer won", func(t *testing.T) {
		// @spec components/entity/order/issue "Entry no longer won"
		f := seedResaleStock(t, 4, 4)
		seller := seedResoldSeller(t, f)
		entry := enterResale(t, entries, f, 1)
		require.NoError(t, entries.PersistDrawOutcome(ctx, f.ID, resaleDrawTime, entity.ResaleDrawOutcome{
			Winners: []entity.ResaleWin{{Entry: entry, Listings: []entity.ResaleListingID{seller.listing}}},
		}))
		_, err := testDB.Pool.Exec(ctx, `UPDATE resale_entries SET state = 3, lost_reason = 3 WHERE id = $1`, string(entry.ID))
		require.NoError(t, err)
		order, issued, settlement := issuanceOf(t, entry.Order, f.EventID)

		err = issuance.Issue(ctx, order, issued, settlement)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		got, err := orders.Get(ctx, order.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.OrderStatusPending, got.Status, "nothing changes")
		var listingStatus int16
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT status FROM resale_listings WHERE id = $1`, string(seller.listing)).Scan(&listingStatus))
		assert.Equal(t, entity.ResaleListingStatusListed, entity.ResaleListingStatus(listingStatus))
	})
}

func TestResaleEntryRepository_RevertWin(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewResaleEntryRepository(testDB, testOutbox)
	// wonWithTwo stores a Won entry for 2 tickets allocated two listings.
	wonWithTwo := func(t *testing.T) (*entity.ResaleEntry, []entity.ResaleListingID) {
		t.Helper()
		f := seedResaleStock(t, 4, 4)
		listings := []entity.ResaleListingID{seedListing(t, f), seedListing(t, f)}
		e := enterResale(t, repo, f, 2)
		require.NoError(t, repo.PersistDrawOutcome(ctx, f.ID, resaleDrawTime, entity.ResaleDrawOutcome{
			Winners: []entity.ResaleWin{{Entry: e, Listings: listings}},
		}))
		return e, listings
	}
	free := func(t *testing.T, id entity.ResaleListingID) bool {
		t.Helper()
		var ok bool
		require.NoError(t, testDB.Pool.QueryRow(ctx, `
			SELECT rl.status = 1 AND (rl.resale_entry_id IS NULL OR x.state = 3)
			FROM resale_listings rl LEFT JOIN resale_entries x ON x.id = rl.resale_entry_id WHERE rl.id = $1`, string(id)).Scan(&ok))
		return ok
	}

	t.Run("win reverted", func(t *testing.T) {
		// @spec components/entity/resale-entry/revert-win "Win reverted"
		e, listings := wonWithTwo(t)

		require.NoError(t, repo.RevertWin(ctx, e.ID, entity.ResaleEntryLostReasonCardNotCharged))

		got, err := repo.Get(ctx, e.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ResaleEntryStateLost, got.State)
		assert.Equal(t, entity.ResaleEntryLostReasonCardNotCharged, got.LostReason)
		assert.Equal(t, entity.OrderStatusVoid, got.Order.Status)
		for _, l := range listings {
			assert.True(t, free(t, l), "free for the next draw")
		}
	})

	t.Run("already reverted", func(t *testing.T) {
		// @spec components/entity/resale-entry/revert-win "Already reverted"
		e, _ := wonWithTwo(t)
		require.NoError(t, repo.RevertWin(ctx, e.ID, entity.ResaleEntryLostReasonCardNotCharged))
		mark := outboxMark(t)

		require.NoError(t, repo.RevertWin(ctx, e.ID, entity.ResaleEntryLostReasonCardNotCharged))

		assert.Empty(t, announcedSince(t, mark, entity.SubjectResaleEntryLost, func() *entityv1.ResaleEntry { return &entityv1.ResaleEntry{} },
			func(m *entityv1.ResaleEntry) bool { return m.GetId().GetValue() == string(e.ID) }), "nothing changes or is announced")
	})

	t.Run("win already issued", func(t *testing.T) {
		// @spec components/entity/resale-entry/revert-win "Win already issued"
		e, _ := wonWithTwo(t)
		_, err := testDB.Pool.Exec(ctx, `UPDATE orders SET status = 1, provider_payment_id = 'pi_x', pay_at = now() WHERE id = $1`, string(e.OrderID))
		require.NoError(t, err)

		assert.ErrorIs(t, repo.RevertWin(ctx, e.ID, entity.ResaleEntryLostReasonCardNotCharged), apperr.ErrFailedPrecondition)
		got, err := repo.Get(ctx, e.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ResaleEntryStateWon, got.State)
	})

	t.Run("unknown entry", func(t *testing.T) {
		// @spec components/entity/resale-entry/revert-win "Unknown entry"
		assert.ErrorIs(t, repo.RevertWin(ctx, entity.ResaleEntryID(entity.NewID()), entity.ResaleEntryLostReasonCardNotCharged), apperr.ErrNotFound)
	})

	t.Run("listing cancelled", func(t *testing.T) {
		// @spec components/entity/resale-entry/revert-win "Listing cancelled"
		e, listings := wonWithTwo(t)
		// The Order of the fan who listed the first ticket was refunded after
		// the draw, which cancels its Listed listing.
		_, err := testDB.Pool.Exec(ctx, `UPDATE resale_listings SET status = 4 WHERE id = $1`, string(listings[0]))
		require.NoError(t, err)

		require.NoError(t, repo.RevertWin(ctx, e.ID, entity.ResaleEntryLostReasonListingCancelled))

		got, err := repo.Get(ctx, e.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ResaleEntryStateLost, got.State)
		assert.Equal(t, entity.ResaleEntryLostReasonListingCancelled, got.LostReason)
		assert.Equal(t, entity.OrderStatusVoid, got.Order.Status)
		assert.True(t, free(t, listings[1]), "the Listed listing is free for the next draw")
		assert.False(t, free(t, listings[0]), "the Cancelled listing is not")
		var status int16
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT status FROM resale_listings WHERE id = $1`, string(listings[0])).Scan(&status))
		assert.Equal(t, entity.ResaleListingStatusCancelled, entity.ResaleListingStatus(status), "it stays Cancelled")
	})

	t.Run("win reverted and announced", func(t *testing.T) {
		// @spec components/entity/resale-entry/revert-win "Win reverted and announced"
		e, _ := wonWithTwo(t)
		mark := outboxMark(t)

		require.NoError(t, repo.RevertWin(ctx, e.ID, entity.ResaleEntryLostReasonPaymentNotCompletedBeforeShow))

		got := announcedSince(t, mark, entity.SubjectResaleEntryLost, func() *entityv1.ResaleEntry { return &entityv1.ResaleEntry{} },
			func(m *entityv1.ResaleEntry) bool { return m.GetId().GetValue() == string(e.ID) })
		require.Len(t, got, 1)
		assert.Equal(t, entityv1.ResaleEntryState_RESALE_ENTRY_STATE_LOST, got[0].GetState())
		assert.Equal(t, entityv1.ResaleEntryLostReason_RESALE_ENTRY_LOST_REASON_PAYMENT_NOT_COMPLETED_BEFORE_SHOW, got[0].GetLostReason())
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/resale-entry/revert-win "Announcement cannot be recorded"
		e, _ := wonWithTwo(t)

		err := rdb.NewResaleEntryRepository(testDB, failingOutbox{}).RevertWin(ctx, e.ID, entity.ResaleEntryLostReasonCardNotCharged)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		got, err := repo.Get(ctx, e.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ResaleEntryStateWon, got.State)
		assert.Equal(t, entity.OrderStatusPending, got.Order.Status)
	})
}
