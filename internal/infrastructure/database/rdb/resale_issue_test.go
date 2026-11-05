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
