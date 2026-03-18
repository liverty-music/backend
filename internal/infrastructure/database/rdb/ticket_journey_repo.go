package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
)

// TicketJourneyRepository implements entity.TicketJourneyRepository for PostgreSQL.
type TicketJourneyRepository struct {
	db *Database
}

const (
	ticketJourneyUpsertQuery = `
		INSERT INTO ticket_journeys (user_id, event_id, status)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, event_id) DO UPDATE SET status = $3
	`
	ticketJourneyDeleteQuery = `
		DELETE FROM ticket_journeys
		WHERE user_id = $1 AND event_id = $2
	`
	ticketJourneyListByUserQuery = `
		SELECT event_id, status
		FROM ticket_journeys
		WHERE user_id = $1
	`
)

// NewTicketJourneyRepository creates a new ticket journey repository instance.
func NewTicketJourneyRepository(db *Database) *TicketJourneyRepository {
	return &TicketJourneyRepository{db: db}
}

// Upsert creates or updates a ticket journey for the given user and event.
func (r *TicketJourneyRepository) Upsert(ctx context.Context, journey *entity.TicketJourney) error {
	_, err := r.db.Pool.Exec(ctx, ticketJourneyUpsertQuery, journey.UserID, journey.EventID, journey.Status)
	if err != nil {
		return toAppErr(err, "failed to upsert ticket journey",
			slog.String("user_id", journey.UserID),
			slog.String("event_id", journey.EventID),
		)
	}

	r.db.logger.Info(ctx, "ticket journey upserted",
		slog.String("entityType", "ticket_journeys"),
		slog.String("userID", journey.UserID),
		slog.String("eventID", journey.EventID),
		slog.Int("status", int(journey.Status)),
	)
	return nil
}

// Delete removes a ticket journey for the given user and event.
func (r *TicketJourneyRepository) Delete(ctx context.Context, userID, eventID string) error {
	_, err := r.db.Pool.Exec(ctx, ticketJourneyDeleteQuery, userID, eventID)
	if err != nil {
		return toAppErr(err, "failed to delete ticket journey",
			slog.String("user_id", userID),
			slog.String("event_id", eventID),
		)
	}

	r.db.logger.Info(ctx, "ticket journey deleted",
		slog.String("entityType", "ticket_journeys"),
		slog.String("userID", userID),
		slog.String("eventID", eventID),
	)
	return nil
}

// ListByUser retrieves all ticket journeys for a given user.
func (r *TicketJourneyRepository) ListByUser(ctx context.Context, userID string) ([]*entity.TicketJourney, error) {
	rows, err := r.db.Pool.Query(ctx, ticketJourneyListByUserQuery, userID)
	if err != nil {
		return nil, toAppErr(err, "failed to list ticket journeys", slog.String("user_id", userID))
	}
	defer rows.Close()

	var journeys []*entity.TicketJourney
	for rows.Next() {
		var eventID string
		var status int16
		if err := rows.Scan(&eventID, &status); err != nil {
			return nil, toAppErr(err, "failed to scan ticket journey")
		}
		journeys = append(journeys, &entity.TicketJourney{
			UserID:  userID,
			EventID: eventID,
			Status:  entity.TicketJourneyStatus(status),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating ticket journey rows")
	}
	return journeys, nil
}
