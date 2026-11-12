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

// TestTicketAdmissibleParity checks that the admit guard of
// TicketRepository.Admit and the listing check of ResaleListingRepository.Create
// accept exactly the tickets entity.Ticket.IsAdmissible accepts, for every
// combination of the ticket's status and admit time, and that the Scanner
// check of Admit accepts exactly the Scanners entity.Scanner.IsUsable accepts.
func TestTicketAdmissibleParity(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	tickets := rdb.NewTicketRepository(db, testOutbox)
	listings := rdb.NewResaleListingRepository(db, testOutbox)
	admitAt := time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)

	combos := []struct {
		name   string
		status entity.TicketStatus
		admit  *time.Time
	}{
		{"issued, not admitted", entity.TicketStatusIssued, nil},
		{"issued, admitted", entity.TicketStatusIssued, &admitAt},
		{"voided, not admitted", entity.TicketStatusVoided, nil},
		{"voided, admitted", entity.TicketStatusVoided, &admitAt},
	}
	s := seedSoldOutType(t, db, len(combos))
	scanner := seedScanner(t, db, s.f.EventID)
	listingTime := saleWindowStart.Add(24 * time.Hour)

	for i, c := range combos {
		_, err := db.Pool.Exec(ctx, `UPDATE tickets SET status = $2, admit_at = $3 WHERE id = $1`, string(s.tickets[i].ID), int16(c.status), c.admit)
		require.NoError(t, err, c.name)
		ticket := &entity.Ticket{Status: c.status, AdmitTime: c.admit}
		want := ticket.IsAdmissible()

		_, listErr := listings.Create(ctx, s.newListing(i, listingTime))
		assert.Equal(t, want, listErr == nil, "listing check: "+c.name)

		result, admitErr := tickets.Admit(ctx, s.tickets[i].ID, scanner.ID, admitAt.Add(time.Hour))
		require.NoError(t, admitErr, c.name)
		assert.Equal(t, want, result.Outcome == entity.AdmitOutcomeAdmitted, "admit guard: "+c.name)
	}

	for _, status := range []entity.ScannerStatus{entity.ScannerStatusUnused, entity.ScannerStatusInUse, entity.ScannerStatusRevoked} {
		t.Run("scanner "+fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()
			f := seedSoldOutType(t, db, 1)
			sc := seedScanner(t, db, f.f.EventID)
			switch status {
			case entity.ScannerStatusInUse:
				_, err := db.Pool.Exec(ctx, `UPDATE scanners SET status = 2, link_token = NULL, bound_public_key = decode('04' || repeat('00', 64), 'hex'), bound_at = now() WHERE id = $1`, string(sc.ID))
				require.NoError(t, err)
			case entity.ScannerStatusRevoked:
				_, err := db.Pool.Exec(ctx, `UPDATE scanners SET status = 3, link_token = NULL, revoked_at = now() WHERE id = $1`, string(sc.ID))
				require.NoError(t, err)
			}

			_, err := tickets.Admit(ctx, f.tickets[0].ID, sc.ID, admitAt)

			usable := (&entity.Scanner{Status: status}).IsUsable()
			assert.Equal(t, usable, err == nil, "a usable Scanner admits, any other is refused")
			if !usable {
				assert.ErrorContains(t, err, "scanner not usable")
			}
		})
	}
}
