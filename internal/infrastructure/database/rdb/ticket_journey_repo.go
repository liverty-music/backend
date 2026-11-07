package rdb

import (
	"context"
	"errors"
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
		SELECT event_id, status, status_set_by
		FROM ticket_journeys
		WHERE user_id = $1 AND event_id = $2
	`
	// ticketJourneyUpsertQuery creates the journey or changes its status and
	// who set it; a journey already at both changes no row.
	ticketJourneyUpsertQuery = `
		INSERT INTO ticket_journeys (user_id, event_id, status, status_set_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, event_id) DO UPDATE SET status = $3, status_set_by = $4
		WHERE ticket_journeys.status <> $3 OR ticket_journeys.status_set_by <> $4
	`
	// ticketJourneyInsertIfAbsentQuery creates the journey only when the fan
	// has none for the event; a conflict waits for a concurrent insert.
	ticketJourneyInsertIfAbsentQuery = `
		INSERT INTO ticket_journeys (user_id, event_id, status, status_set_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, event_id) DO NOTHING
	`
	// ticketJourneyLockQuery reads the journey under its row lock, which a
	// concurrent ticketJourneyUpsertQuery waits for (or holds first).
	ticketJourneyLockQuery = `
		SELECT status, status_set_by
		FROM ticket_journeys
		WHERE user_id = $1 AND event_id = $2
		FOR UPDATE
	`
	ticketJourneyUpdateQuery = `
		UPDATE ticket_journeys
		SET status = $3, status_set_by = $4
		WHERE user_id = $1 AND event_id = $2
	`
	ticketJourneyDeleteQuery = `
		DELETE FROM ticket_journeys
		WHERE user_id = $1 AND event_id = $2
	`
	ticketJourneyListByUserQuery = `
		SELECT event_id, status, status_set_by
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
	var gotEventID, setBy string
	var status int16
	if err := row.Scan(&gotEventID, &status, &setBy); err != nil {
		return nil, toAppErr(err, "failed to get ticket journey",
			slog.String("user_id", userID),
			slog.String("event_id", eventID),
		)
	}
	return &entity.TicketJourney{
		UserID:      userID,
		EventID:     gotEventID,
		Status:      entity.TicketJourneyStatus(status),
		StatusSetBy: entity.TicketJourneySetBy(setBy),
	}, nil
}

// Upsert creates or updates a ticket journey for the given user and event,
// with its status and who set it. A journey already at both is left
// unchanged.
//
// # Announces
//
// TICKET_JOURNEY.status_changed with the journey at its new status and set by,
// in the transaction of the change, when it created the journey or changed
// either. Nothing when the journey already had both.
func (r *TicketJourneyRepository) Upsert(ctx context.Context, journey *entity.TicketJourney) error {
	attrs := []slog.Attr{slog.String("user_id", journey.UserID), slog.String("event_id", journey.EventID)}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin ticket journey upsert", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, ticketJourneyUpsertQuery, journey.UserID, journey.EventID, journey.Status, string(journey.StatusSetBy))
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
		slog.String("statusSetBy", string(journey.StatusSetBy)),
	)
	return nil
}

// UpsertIfReplaceable stores journey when the fan has no journey for the event
// or [entity.TicketJourney.ReplaceableBy] allows replacing it, and reports
// whether it stored it. It first inserts with ON CONFLICT DO NOTHING; on a
// conflict it reads the journey under its row lock, so the check and the write
// are one step with the fan's Upsert of the same journey.
//
// # Announces
//
// TICKET_JOURNEY.status_changed with the journey at its new status and set by,
// in the transaction of the change. Nothing when it stored nothing or on
// failure.
func (r *TicketJourneyRepository) UpsertIfReplaceable(ctx context.Context, journey *entity.TicketJourney) (bool, error) {
	attrs := []slog.Attr{
		slog.String("user_id", journey.UserID),
		slog.String("event_id", journey.EventID),
		slog.Int("status", int(journey.Status)),
		slog.String("status_set_by", string(journey.StatusSetBy)),
	}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return false, toAppErr(err, "failed to begin ticket journey guarded upsert", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	stored, err := r.storeIfReplaceable(ctx, tx, journey, attrs)
	if err != nil || !stored {
		return false, err
	}
	if err := announce(ctx, r.outbox, tx, entity.SubjectTicketJourneyStatusChanged, messaging.Announcement{Payload: pb.FromTicketJourney(journey)}); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, toAppErr(err, "failed to commit ticket journey guarded upsert", attrs...)
	}
	r.db.logger.Info(ctx, "ticket journey stored over a replaceable journey", attrs...)
	return true, nil
}

// storeIfReplaceable writes journey in tx when the fan has no journey or the
// existing one is replaceable by it and differs from it. A journey deleted
// between the insert's conflict and the lock is inserted again.
func (r *TicketJourneyRepository) storeIfReplaceable(ctx context.Context, tx pgx.Tx, journey *entity.TicketJourney, attrs []slog.Attr) (bool, error) {
	for range 2 {
		tag, err := tx.Exec(ctx, ticketJourneyInsertIfAbsentQuery, journey.UserID, journey.EventID, journey.Status, string(journey.StatusSetBy))
		if err != nil {
			return false, toAppErr(err, "failed to insert ticket journey", attrs...)
		}
		if tag.RowsAffected() == 1 {
			return true, nil
		}
		var status int16
		var setBy string
		err = tx.QueryRow(ctx, ticketJourneyLockQuery, journey.UserID, journey.EventID).Scan(&status, &setBy)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, toAppErr(err, "failed to read ticket journey", attrs...)
		}
		current := &entity.TicketJourney{UserID: journey.UserID, EventID: journey.EventID, Status: entity.TicketJourneyStatus(status), StatusSetBy: entity.TicketJourneySetBy(setBy)}
		if (current.Status == journey.Status && current.StatusSetBy == journey.StatusSetBy) || !current.ReplaceableBy(journey.Status, journey.StatusSetBy) {
			return false, nil
		}
		if _, err := tx.Exec(ctx, ticketJourneyUpdateQuery, journey.UserID, journey.EventID, journey.Status, string(journey.StatusSetBy)); err != nil {
			return false, toAppErr(err, "failed to update ticket journey", attrs...)
		}
		return true, nil
	}
	return false, apperr.New(codes.Aborted, "ticket journey changed concurrently", attrs...)
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
		var eventID, setBy string
		var status int16
		if err := rows.Scan(&eventID, &status, &setBy); err != nil {
			return nil, toAppErr(err, "failed to scan ticket journey")
		}
		journeys = append(journeys, &entity.TicketJourney{
			UserID:      userID,
			EventID:     eventID,
			Status:      entity.TicketJourneyStatus(status),
			StatusSetBy: entity.TicketJourneySetBy(setBy),
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
