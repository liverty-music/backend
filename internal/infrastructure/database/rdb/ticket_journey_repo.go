package rdb

import (
	"context"

	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// TicketJourneyRepository implements entity.TicketJourneyRepository for PostgreSQL.
type TicketJourneyRepository struct {
	db     *Database
	outbox Announcer
}

// Compile-time interface compliance check.
var _ entity.TicketJourneyRepository = (*TicketJourneyRepository)(nil)

const (
	ticketJourneyGetQuery = `
		SELECT event_id, status
		FROM ticket_journeys
		WHERE user_id = $1 AND event_id = $2
	`
	// ticketJourneyUpsertQuery creates the journey or changes its status; a
	// journey already at the status changes no row.
	ticketJourneyUpsertQuery = `
		INSERT INTO ticket_journeys (user_id, event_id, status)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, event_id) DO UPDATE SET status = $3
		WHERE ticket_journeys.status <> $3
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
	// ticketJourneyListUserIDsTrackingSeriesQuery returns each user with a
	// Tracking journey (status = 1) on any event of the given series, once,
	// with the user's linked event: the earliest upcoming tracked event
	// (past events sort after upcoming ones), otherwise the earliest tracked
	// event. e.id breaks ties between events on the same date.
	ticketJourneyListUserIDsTrackingSeriesQuery = `
		SELECT DISTINCT ON (tj.user_id) tj.user_id, tj.event_id
		FROM ticket_journeys tj
		JOIN events e ON e.id = tj.event_id
		WHERE e.series_id = $1
		  AND tj.status = 1
		ORDER BY tj.user_id, (e.local_event_date < current_date), e.local_event_date, e.id
	`

	// ticketJourneyListUserIDsTrackingEventsQuery is
	// ticketJourneyListUserIDsTrackingSeriesQuery over the given events
	// instead of a series, so the linked event is one of them.
	ticketJourneyListUserIDsTrackingEventsQuery = `
		SELECT DISTINCT ON (tj.user_id) tj.user_id, tj.event_id
		FROM ticket_journeys tj
		JOIN events e ON e.id = tj.event_id
		WHERE tj.event_id = ANY($1::uuid[])
		  AND tj.status = 1
		ORDER BY tj.user_id, (e.local_event_date < current_date), e.local_event_date, e.id
	`
)

// NewTicketJourneyRepository creates a new ticket journey repository instance.
func NewTicketJourneyRepository(db *Database, outbox Announcer) *TicketJourneyRepository {
	return &TicketJourneyRepository{db: db, outbox: outbox}
}

// Get retrieves a single ticket journey by user and event IDs.
// Returns apperr.ErrNotFound when no journey exists for the pair.
func (r *TicketJourneyRepository) Get(ctx context.Context, userID, eventID string) (*entity.TicketJourney, error) {
	row := r.db.Pool.QueryRow(ctx, ticketJourneyGetQuery, userID, eventID)
	var gotEventID string
	var status int16
	if err := row.Scan(&gotEventID, &status); err != nil {
		return nil, toAppErr(err, "failed to get ticket journey",
			slog.String("user_id", userID),
			slog.String("event_id", eventID),
		)
	}
	return &entity.TicketJourney{
		UserID:  userID,
		EventID: gotEventID,
		Status:  entity.TicketJourneyStatus(status),
	}, nil
}

// Upsert creates or updates a ticket journey for the given user and event.
// A journey already at the given status is left unchanged.
//
// # Announces
//
// TICKET_JOURNEY.status_changed with the journey at its new status, in the
// transaction of the change, when it created the journey or changed its
// status. Nothing when the status was already the given one.
func (r *TicketJourneyRepository) Upsert(ctx context.Context, journey *entity.TicketJourney) error {
	attrs := []slog.Attr{slog.String("user_id", journey.UserID), slog.String("event_id", journey.EventID)}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin ticket journey upsert", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, ticketJourneyUpsertQuery, journey.UserID, journey.EventID, journey.Status)
	if err != nil {
		return toAppErr(err, "failed to upsert ticket journey", attrs...)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if err := announce(ctx, r.outbox, tx, entity.SubjectTicketJourneyStatusChanged, messaging.Announcement{Payload: pb.FromTicketJourney(journey)}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit ticket journey upsert", attrs...)
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

// ListUserIDsTrackingSeries returns each user with a Tracking journey on any
// event of the given series, with the user's linked event.
func (r *TicketJourneyRepository) ListUserIDsTrackingSeries(ctx context.Context, seriesID string) ([]*entity.SeriesTracker, error) {
	if seriesID == "" {
		return nil, apperr.New(codes.InvalidArgument, "series ID must not be empty")
	}

	rows, err := r.db.Pool.Query(ctx, ticketJourneyListUserIDsTrackingSeriesQuery, seriesID)
	if err != nil {
		return nil, toAppErr(err, "failed to list users tracking series", slog.String("series_id", seriesID))
	}
	return scanSeriesTrackers(rows)
}

// ListUserIDsTrackingEvents returns each user with a Tracking journey on any
// of the given events, with the user's linked event among them.
func (r *TicketJourneyRepository) ListUserIDsTrackingEvents(ctx context.Context, eventIDs []string) ([]*entity.SeriesTracker, error) {
	if len(eventIDs) == 0 {
		return nil, nil
	}
	rows, err := r.db.Pool.Query(ctx, ticketJourneyListUserIDsTrackingEventsQuery, eventIDs)
	if err != nil {
		return nil, toAppErr(err, "failed to list users tracking events", slog.Int("event_count", len(eventIDs)))
	}
	return scanSeriesTrackers(rows)
}

// scanSeriesTrackers reads (user_id, event_id) rows and closes them.
func scanSeriesTrackers(rows pgx.Rows) ([]*entity.SeriesTracker, error) {
	defer rows.Close()

	var trackers []*entity.SeriesTracker
	for rows.Next() {
		var tr entity.SeriesTracker
		if err := rows.Scan(&tr.UserID, &tr.EventID); err != nil {
			return nil, toAppErr(err, "failed to scan series tracker")
		}
		trackers = append(trackers, &tr)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating series tracker rows")
	}
	return trackers, nil
}
