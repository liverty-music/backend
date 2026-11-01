package rdb_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

func TestTicketRepository_AdmitAnnounces(t *testing.T) {
	ctx := context.Background()
	repo := rdb.NewTicketRepository(testDB, testOutbox)

	// admittedOf returns the admission announcements of the ticket recorded
	// after mark.
	admittedOf := func(t *testing.T, mark int64, ticket entity.TicketID) []*entityv1.Ticket {
		t.Helper()
		return announcedSince(t, mark, entity.SubjectTicketAdmitted, func() *entityv1.Ticket { return &entityv1.Ticket{} },
			func(got *entityv1.Ticket) bool { return got.GetId().GetValue() == string(ticket) })
	}
	// newTicket seeds an Issued ticket of its own event and a Scanner for it.
	newTicket := func(t *testing.T) (entity.TicketID, *entity.Scanner) {
		t.Helper()
		eventID := seedAdmissionEvent(t)
		fan := seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
		return seedIssuedTickets(t, eventID, fan, 1)[0], seedScanner(t, eventID)
	}

	t.Run("first admission announced", func(t *testing.T) {
		// @spec components/entity/ticket/admit "First admission announced"
		ticket, scanner := newTicket(t)
		mark := outboxMark(t)

		_, err := repo.Admit(ctx, ticket, scanner.ID, nov20(18, 32))
		require.NoError(t, err)

		got := admittedOf(t, mark, ticket)
		require.Len(t, got, 1)
		assert.True(t, got[0].GetAdmitTime().AsTime().Equal(nov20(18, 32)))
	})

	t.Run("second admission", func(t *testing.T) {
		// @spec components/entity/ticket/admit "Second admission"
		ticket, scanner := newTicket(t)
		_, err := repo.Admit(ctx, ticket, scanner.ID, nov20(18, 32))
		require.NoError(t, err)
		mark := outboxMark(t)

		got, err := repo.Admit(ctx, ticket, scanner.ID, nov20(18, 40))
		require.NoError(t, err)
		assert.Equal(t, entity.AdmitOutcomeAlreadyAdmitted, got.Outcome)
		assert.Empty(t, admittedOf(t, mark, ticket))
	})

	t.Run("voided ticket", func(t *testing.T) {
		// @spec components/entity/ticket/admit "Voided ticket"
		ticket, scanner := newTicket(t)
		_, err := testDB.Pool.Exec(ctx, `UPDATE tickets SET status = 2 WHERE id = $1`, string(ticket))
		require.NoError(t, err)
		mark := outboxMark(t)

		got, err := repo.Admit(ctx, ticket, scanner.ID, nov20(18, 32))
		require.NoError(t, err)
		assert.Equal(t, entity.AdmitOutcomeVoided, got.Outcome)
		assert.Empty(t, admittedOf(t, mark, ticket))
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/ticket/admit "Announcement cannot be recorded"
		ticket, scanner := newTicket(t)

		_, err := rdb.NewTicketRepository(testDB, failingOutbox{}).Admit(ctx, ticket, scanner.ID, nov20(18, 32))
		assert.ErrorIs(t, err, apperr.ErrUnavailable)

		var admitted, admissions int
		require.NoError(t, testDB.Pool.QueryRow(ctx,
			`SELECT (SELECT count(*) FROM tickets WHERE id = $1 AND admit_at IS NOT NULL), (SELECT count(*) FROM admissions WHERE ticket_id = $1)`,
			string(ticket)).Scan(&admitted, &admissions))
		assert.Zero(t, admitted, "the Ticket has no admit time")
		assert.Zero(t, admissions, "no Admission is stored")
	})
}
