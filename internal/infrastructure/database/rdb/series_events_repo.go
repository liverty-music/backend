package rdb

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// The lifecycle of a first-party series' events: create, edit, publish and
// cancel (OpenSpec change restructure-event-publishing, design D5). Each
// operation runs in one transaction and is the only writer of
// events.publish_state.
const (
	// lockFirstPartySeriesQuery locks the series row so concurrent edits of
	// one series run one after another, and reads its organizer.
	lockFirstPartySeriesQuery = `
		SELECT organizer_id FROM series WHERE id = $1 FOR UPDATE
	`

	// seriesShareStateQuery reads what decides whether a series may be given
	// a share token. Run after lockFirstPartySeriesQuery, so neither value
	// can change before the transaction ends.
	seriesShareStateQuery = `
		SELECT visibility::text, unlisted_token FROM series WHERE id = $1
	`

	// lockSeriesEventsQuery reads and locks every event of the series with
	// the fields a lifecycle write decides on.
	lockSeriesEventsQuery = `
		SELECT id, venue_id, local_event_date, start_at, open_at, publish_state, per_account_limit
		FROM events
		WHERE series_id = $1
		FOR UPDATE
	`

	insertAuthoredEventQuery = `
		INSERT INTO events (id, series_id, venue_id, listed_venue_name, local_event_date, start_at, open_at, publish_state, per_account_limit)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'DRAFT', $8)
	`

	insertConcertRowQuery = `
		INSERT INTO concerts (event_id) VALUES ($1)
	`

	// addPerformersQuery attaches the given artists to one concert.
	addPerformersQuery = `
		INSERT INTO concert_artists (event_id, artist_id)
		SELECT $1, a FROM unnest($2::uuid[]) AS a
		ON CONFLICT DO NOTHING
	`

	// dropOtherPerformersQuery removes the concert's performers that are not
	// among the given artists.
	dropOtherPerformersQuery = `
		DELETE FROM concert_artists WHERE event_id = $1 AND artist_id <> ALL($2::uuid[])
	`

	updateAuthoredSeriesQuery = `
		UPDATE series
		SET title = $2, type = $3, source_url = $4, description = $5, visibility = $6
		WHERE id = $1
	`

	updateDraftEventQuery = `
		UPDATE events
		SET venue_id = $2, listed_venue_name = $3, local_event_date = $4, start_at = $5, open_at = $6,
		    per_account_limit = COALESCE($7, per_account_limit)
		WHERE id = $1 AND publish_state = 'DRAFT'
	`

	updatePublishedEventTimesQuery = `
		UPDATE events
		SET start_at = $2, open_at = $3
		WHERE id = $1 AND publish_state = 'PUBLISHED'
	`

	deleteDraftEventsQuery = `
		DELETE FROM events WHERE id = ANY($1::uuid[]) AND publish_state = 'DRAFT'
	`

	// eventAtSlotQuery finds the event that holds a slot: the one event at
	// the venue, date and start time that is not DRAFT (uq_events_natural_key),
	// other than the event $4 itself, with its series' organizer.
	eventAtSlotQuery = `
		SELECT e.id, e.series_id, e.publish_state, s.organizer_id
		FROM events e
		JOIN series s ON s.id = e.series_id
		WHERE e.venue_id = $1
		  AND e.local_event_date = $2
		  AND e.start_at IS NOT DISTINCT FROM $3
		  AND e.id <> $4
		  AND ` + catalogEventCondition + `
		FOR UPDATE OF e
	`

	// moveTicketJourneysQuery moves the fans tracking event $1 onto event $2;
	// a fan who already tracks $2 keeps that journey.
	moveTicketJourneysQuery = `
		INSERT INTO ticket_journeys (user_id, event_id, status)
		SELECT user_id, $2, status FROM ticket_journeys WHERE event_id = $1
		ON CONFLICT DO NOTHING
	`

	// copyPerformersQuery adds the performers of concert $1 to concert $2.
	copyPerformersQuery = `
		INSERT INTO concert_artists (event_id, artist_id)
		SELECT $2, artist_id FROM concert_artists WHERE event_id = $1
		ON CONFLICT DO NOTHING
	`

	deleteEventByIDQuery = `
		DELETE FROM events WHERE id = $1
	`

	publishEventQuery = `
		UPDATE events SET publish_state = 'PUBLISHED' WHERE id = $1 AND publish_state = 'DRAFT'
	`

	// claimEventQuery moves a discovered event into the series as PUBLISHED.
	// It has no per-account limit, so it takes the draft's ($3).
	claimEventQuery = `
		UPDATE events SET series_id = $2, publish_state = 'PUBLISHED', per_account_limit = COALESCE(per_account_limit, $3)
		WHERE id = $1
	`

	cancelEventsQuery = `
		UPDATE events SET publish_state = 'CANCELLED'
		WHERE id = ANY($1::uuid[]) AND publish_state = 'PUBLISHED'
	`

	deleteEventlessSeriesQuery = `
		DELETE FROM series s
		WHERE s.id = $1 AND NOT EXISTS (SELECT 1 FROM events e WHERE e.series_id = s.id)
	`
)

// lockSeriesForWrite locks a series and returns its organizer, or NotFound
// when no series has the id. A discovered series returns a nil organizer.
func lockSeriesForWrite(ctx context.Context, tx pgx.Tx, seriesID string) (*string, error) {
	var organizerID sql.NullString
	if err := tx.QueryRow(ctx, lockFirstPartySeriesQuery, seriesID).Scan(&organizerID); err != nil {
		return nil, toAppErr(err, "failed to lock series", slog.String("series_id", seriesID))
	}
	if !organizerID.Valid {
		return nil, nil
	}
	return &organizerID.String, nil
}

// lockSeriesEvents reads and locks every event of the series, keyed by id.
func lockSeriesEvents(ctx context.Context, tx pgx.Tx, seriesID string) (map[string]*entity.Event, error) {
	rows, err := tx.Query(ctx, lockSeriesEventsQuery, seriesID)
	if err != nil {
		return nil, toAppErr(err, "failed to lock series events", slog.String("series_id", seriesID))
	}
	defer rows.Close()

	events := make(map[string]*entity.Event)
	for rows.Next() {
		var (
			e     entity.Event
			state sql.NullString
			limit sql.NullInt32
		)
		if err := rows.Scan(&e.ID, &e.VenueID, &e.LocalDate, &e.StartTime, &e.OpenTime, &state, &limit); err != nil {
			return nil, toAppErr(err, "failed to scan series event")
		}
		e.SeriesID = seriesID
		e.PublishState = nullPublishState(state)
		e.PerAccountLimit = optionalInt(limit)
		events[e.ID] = &e
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "series events iteration ended with error")
	}
	return events, nil
}

// insertDraftEvent stores one new DRAFT event of the series as a concert
// performed by the given artists.
func insertDraftEvent(ctx context.Context, tx pgx.Tx, seriesID string, e *entity.Event, performerIDs []string) error {
	limit := e.Limit()
	if _, err := tx.Exec(ctx, insertAuthoredEventQuery,
		e.ID, seriesID, e.VenueID, e.ListedVenueName, e.LocalDate, e.StartTime, e.OpenTime, limit,
	); err != nil {
		return toAppErr(err, "failed to insert draft event", slog.String("series_id", seriesID))
	}
	if _, err := tx.Exec(ctx, insertConcertRowQuery, e.ID); err != nil {
		return toAppErr(err, "failed to insert concert of draft event", slog.String("event_id", e.ID))
	}
	if _, err := tx.Exec(ctx, addPerformersQuery, e.ID, performerIDs); err != nil {
		return toAppErr(err, "failed to link performers to draft event", slog.String("event_id", e.ID))
	}
	return nil
}

// setPerformers makes the given artists the performers of one concert.
func setPerformers(ctx context.Context, tx pgx.Tx, eventID string, performerIDs []string) error {
	if _, err := tx.Exec(ctx, dropOtherPerformersQuery, eventID, performerIDs); err != nil {
		return toAppErr(err, "failed to drop performers", slog.String("event_id", eventID))
	}
	if _, err := tx.Exec(ctx, addPerformersQuery, eventID, performerIDs); err != nil {
		return toAppErr(err, "failed to add performers", slog.String("event_id", eventID))
	}
	return nil
}

// checkSlot returns the event that holds the slot, or nil when the slot is
// free. exceptID is the event being placed, which never holds its own slot. A
// suppressed slot does not matter here: suppression keeps only discovery away.
func checkSlot(ctx context.Context, tx pgx.Tx, e *entity.Event, exceptID string) (*entity.SlotHolder, error) {
	var (
		h           entity.SlotHolder
		state       sql.NullString
		organizerID sql.NullString
	)
	err := tx.QueryRow(ctx, eventAtSlotQuery, e.VenueID, e.LocalDate, e.StartTime, exceptID).
		Scan(&h.EventID, &h.SeriesID, &state, &organizerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, toAppErr(err, "failed to read the event at the slot")
	}
	h.State = nullPublishState(state)
	if organizerID.Valid {
		h.OrganizerID = &organizerID.String
	}
	return &h, nil
}

// CreateDraft persists a new first-party series and its DRAFT events in a
// single transaction.
func (r *SeriesRepository) CreateDraft(ctx context.Context, series *entity.Series, events []*entity.Event, performerArtistIDs []string) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, insertFirstPartySeriesQuery,
		series.ID, series.Title, string(series.Type), nullableString(series.SourceURL),
		series.Description, series.OrganizerID, visibilityString(series.Visibility),
	); err != nil {
		return toAppErr(err, "failed to insert first-party series", slog.String("series_id", series.ID))
	}
	for _, e := range events {
		if err := insertDraftEvent(ctx, tx, series.ID, e, performerArtistIDs); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit draft creation transaction")
	}
	return nil
}

// Update applies an organizer's edit to a first-party series in a single
// transaction.
func (r *SeriesRepository) Update(ctx context.Context, series *entity.Series, events []*entity.Event, performerArtistIDs []string) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	organizerID, err := lockSeriesForWrite(ctx, tx, series.ID)
	if errors.Is(err, apperr.ErrNotFound) || (err == nil && organizerID == nil) {
		return apperr.New(codes.FailedPrecondition, "no first-party series has the id", slog.String("series_id", series.ID))
	}
	if err != nil {
		return err
	}
	stored, err := lockSeriesEvents(ctx, tx, series.ID)
	if err != nil {
		return err
	}
	plan, err := entity.PlanSeriesUpdate(stored, events)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, updateAuthoredSeriesQuery,
		series.ID, series.Title, string(series.Type), nullableString(series.SourceURL),
		series.Description, visibilityString(series.Visibility),
	); err != nil {
		return toAppErr(err, "failed to update series", slog.String("series_id", series.ID))
	}

	for _, edit := range plan.Edits {
		e := edit.Given
		switch edit.Kind {
		case entity.EventEditAdd:
			added := *e
			added.ID = entity.NewID()
			if err := insertDraftEvent(ctx, tx, series.ID, &added, performerArtistIDs); err != nil {
				return err
			}
			continue
		case entity.EventEditDraft:
			if _, err := tx.Exec(ctx, updateDraftEventQuery,
				e.ID, e.VenueID, e.ListedVenueName, e.LocalDate, e.StartTime, e.OpenTime, e.PerAccountLimit,
			); err != nil {
				return toAppErr(err, "failed to update draft event", slog.String("event_id", e.ID))
			}
		case entity.EventEditPublishedTimes:
			if edit.MovesSlot {
				moved := *edit.Stored
				moved.StartTime = e.StartTime
				if err := takeNewSlot(ctx, tx, &moved); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, updatePublishedEventTimesQuery, e.ID, e.StartTime, e.OpenTime); err != nil {
				return toAppErr(err, "failed to update published event times", slog.String("event_id", e.ID))
			}
		default:
			// A CANCELLED event never changes, its performers included.
			continue
		}
		if err := setPerformers(ctx, tx, e.ID, performerArtistIDs); err != nil {
			return err
		}
	}

	if len(plan.Removed) > 0 {
		if _, err := tx.Exec(ctx, deleteDraftEventsQuery, plan.Removed); err != nil {
			return toAppErr(err, "failed to remove draft events", slog.String("series_id", series.ID))
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit series update transaction")
	}
	return nil
}

// takeNewSlot prepares a PUBLISHED event to take the slot of its new start
// time (design D8): a discovered event there is merged into it, so its fans
// track the PUBLISHED event and its performers join; a first-party event there
// fails with FailedPrecondition.
func takeNewSlot(ctx context.Context, tx pgx.Tx, e *entity.Event) error {
	holder, err := checkSlot(ctx, tx, e, e.ID)
	if err != nil {
		return err
	}
	merge, err := e.SlotMergePlan(holder)
	if err != nil || merge == nil {
		return err
	}
	if _, err := tx.Exec(ctx, moveTicketJourneysQuery, merge.EventID, e.ID); err != nil {
		return toAppErr(err, "failed to move ticket journeys", slog.String("event_id", merge.EventID))
	}
	if _, err := tx.Exec(ctx, copyPerformersQuery, merge.EventID, e.ID); err != nil {
		return toAppErr(err, "failed to copy performers", slog.String("event_id", merge.EventID))
	}
	if _, err := tx.Exec(ctx, deleteEventByIDQuery, merge.EventID); err != nil {
		return toAppErr(err, "failed to remove the merged discovered event", slog.String("event_id", merge.EventID))
	}
	return nil
}

// GetAuthored retrieves the full authored concert for one series: one
// Concert per event, each with its own performers.
func (r *SeriesRepository) GetAuthored(ctx context.Context, seriesID string) (*entity.Series, []*entity.Concert, error) {
	s, err := scanSeries(r.db.Pool.QueryRow(ctx, getSeriesQuery, seriesID).Scan)
	if err != nil {
		return nil, nil, toAppErr(err, "failed to get authored series", slog.String("series_id", seriesID))
	}

	events, err := r.queryAuthoredEvents(ctx, r.db.Pool, seriesID)
	if err != nil {
		return nil, nil, err
	}
	concerts := make([]*entity.Concert, len(events))
	byEvent := make(map[string]*entity.Concert, len(events))
	for i, e := range events {
		concerts[i] = &entity.Concert{Event: *e, Series: s}
		byEvent[e.ID] = concerts[i]
	}

	perfRows, err := r.db.Pool.Query(ctx, getAuthoredSeriesPerformersQuery, seriesID)
	if err != nil {
		return nil, nil, toAppErr(err, "failed to load authored performers", slog.String("series_id", seriesID))
	}
	defer perfRows.Close()

	for perfRows.Next() {
		var (
			eventID string
			a       entity.Artist
		)
		if err := perfRows.Scan(&eventID, &a.ID, &a.Name, &a.MBID); err != nil {
			return nil, nil, toAppErr(err, "failed to scan authored performer")
		}
		if c := byEvent[eventID]; c != nil {
			c.Artists = append(c.Artists, &a)
		}
	}
	if err := perfRows.Err(); err != nil {
		return nil, nil, toAppErr(err, "performers iteration ended with error")
	}
	return s, concerts, nil
}

// PublishEvents publishes the given DRAFT events of a first-party series in a
// single transaction, and stores the share token in it when one is given.
//
// # Announces
//
// CONCERT.created once per performing Artist, carrying the Concerts made
// PUBLISHED in place at which it performs, with the Artist id as ce_subject,
// in the transaction of the publish, when the series is PUBLIC. Nothing for
// claimed events, events published before, an UNLISTED series or a failure.
func (r *SeriesRepository) PublishEvents(ctx context.Context, seriesID string, eventIDs []string, token *string) ([]string, error) {
	if len(eventIDs) == 0 {
		return nil, apperr.New(codes.InvalidArgument, "at least one event must be given to publish")
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin publish transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := lockSeriesForWrite(ctx, tx, seriesID); err != nil {
		return nil, err
	}
	if token != nil {
		if err := setFirstShareToken(ctx, tx, seriesID, *token); err != nil {
			return nil, err
		}
	}
	stored, err := lockSeriesEvents(ctx, tx, seriesID)
	if err != nil {
		return nil, err
	}
	if err := entity.CheckPublishable(stored, eventIDs); err != nil {
		return nil, err
	}

	var published []string
	for _, id := range eventIDs {
		draft := stored[id]
		holder, err := checkSlot(ctx, tx, draft, draft.ID)
		if err != nil {
			return nil, err
		}
		claim, err := draft.ClaimSlotPlan(seriesID, holder)
		if err != nil {
			return nil, err
		}
		switch claim {
		case entity.SlotClaimPublish:
			if _, err := tx.Exec(ctx, publishEventQuery, draft.ID); err != nil {
				return nil, toAppErr(err, "failed to publish event", slog.String("event_id", draft.ID))
			}
			published = append(published, draft.ID)
			continue
		case entity.SlotClaimDiscovered:
			// Claim a discovered event, keeping its id (design D7).
			if _, err := tx.Exec(ctx, claimEventQuery, holder.EventID, seriesID, draft.Limit()); err != nil {
				return nil, toAppErr(err, "failed to claim event", slog.String("event_id", holder.EventID))
			}
			if _, err := tx.Exec(ctx, copyPerformersQuery, draft.ID, holder.EventID); err != nil {
				return nil, toAppErr(err, "failed to link performers to claimed event", slog.String("event_id", holder.EventID))
			}
		}
		// A PUBLISHED event of this series already holds the slot, or the
		// discovered one was claimed: the draft goes.
		if _, err := tx.Exec(ctx, deleteEventByIDQuery, draft.ID); err != nil {
			return nil, toAppErr(err, "failed to remove the draft of a held slot", slog.String("event_id", draft.ID))
		}
	}

	if _, err := tx.Exec(ctx, dropConcertCandidatesBySeriesQuery, seriesID); err != nil {
		return nil, toAppErr(err, "failed to drop concert candidates for series")
	}
	if err := r.announcePublished(ctx, tx, seriesID, published); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit publish transaction")
	}
	return published, nil
}

// setFirstShareToken gives a series its first share token inside the publish
// transaction. The caller holds the series row lock, which Update (a
// visibility change) and SetUnlistedToken (a rotation) also take, so the
// state checked here is the state the token is stored against.
// FailedPrecondition when the series is not UNLISTED or already has a token;
// AlreadyExists when another series holds the token.
func setFirstShareToken(ctx context.Context, tx pgx.Tx, seriesID, token string) error {
	attr := slog.String("series_id", seriesID)
	var visibility, current *string
	if err := tx.QueryRow(ctx, seriesShareStateQuery, seriesID).Scan(&visibility, &current); err != nil {
		return toAppErr(err, "failed to read the series share state", attr)
	}
	if visibility == nil || entity.SeriesVisibility(*visibility) != entity.SeriesVisibilityUnlisted {
		return apperr.New(codes.FailedPrecondition, "a share token is given only to an UNLISTED series", attr)
	}
	if current != nil {
		return apperr.New(codes.FailedPrecondition, "the series already has a share token", attr)
	}
	if _, err := tx.Exec(ctx, setUnlistedTokenQuery, seriesID, token); err != nil {
		return toAppErr(err, "failed to store the share token", attr)
	}
	return nil
}

// announcePublished records CONCERT.created, once per performing Artist, for
// the events made PUBLISHED in place, when the series is PUBLIC.
func (r *SeriesRepository) announcePublished(ctx context.Context, tx pgx.Tx, seriesID string, published []string) error {
	if len(published) == 0 {
		return nil
	}
	var visibility *string
	if err := tx.QueryRow(ctx, seriesVisibilityQuery, seriesID).Scan(&visibility); err != nil {
		return toAppErr(err, "failed to read the series visibility", slog.String("series_id", seriesID))
	}
	if visibility == nil || entity.SeriesVisibility(*visibility) != entity.SeriesVisibilityPublic {
		return nil
	}
	rows, err := tx.Query(ctx, performersOfEventsQuery, published)
	if err != nil {
		return toAppErr(err, "failed to read the performers of the published events", slog.String("series_id", seriesID))
	}
	byArtist := map[string][]string{}
	for rows.Next() {
		var artistID, eventID string
		if err := rows.Scan(&artistID, &eventID); err != nil {
			rows.Close()
			return toAppErr(err, "failed to scan a performer of a published event")
		}
		byArtist[artistID] = append(byArtist[artistID], eventID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return toAppErr(err, "failed to read the performers of the published events")
	}
	return announceNewConcerts(ctx, r.outbox, tx, byArtist)
}

// CancelEvents cancels the given events of a first-party series in a single
// transaction, and records EVENT.cancelled for each event it makes
// CANCELLED in it.
func (r *SeriesRepository) CancelEvents(ctx context.Context, seriesID string, eventIDs []string) ([]string, error) {
	if len(eventIDs) == 0 {
		return nil, apperr.New(codes.InvalidArgument, "at least one event must be given to cancel")
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin cancel transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := lockSeriesForWrite(ctx, tx, seriesID); err != nil {
		return nil, err
	}
	stored, err := lockSeriesEvents(ctx, tx, seriesID)
	if err != nil {
		return nil, err
	}

	plan, err := entity.PlanCancel(stored, eventIDs)
	if err != nil {
		return nil, err
	}
	cancelled, removed := plan.Cancel, plan.Remove

	if len(cancelled) > 0 {
		if _, err := tx.Exec(ctx, cancelEventsQuery, cancelled); err != nil {
			return nil, toAppErr(err, "failed to cancel events", slog.String("series_id", seriesID))
		}
		announcements := make([]*entity.Event, 0, len(cancelled))
		for _, id := range cancelled {
			e := *stored[id]
			state := entity.PublishStateCancelled
			e.PublishState = &state
			announcements = append(announcements, &e)
		}
		if err := announceAll(ctx, r.outbox, tx, entity.SubjectEventCancelled, announcements); err != nil {
			return nil, err
		}
	}
	if len(removed) > 0 {
		if _, err := tx.Exec(ctx, deleteDraftEventsQuery, removed); err != nil {
			return nil, toAppErr(err, "failed to remove draft events", slog.String("series_id", seriesID))
		}
	}
	// The cover image link cascades; the SeriesImage row and its objects stay,
	// as for any hard-deleted series (design D5).
	if _, err := tx.Exec(ctx, deleteEventlessSeriesQuery, seriesID); err != nil {
		return nil, toAppErr(err, "failed to remove the series left with no event", slog.String("series_id", seriesID))
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit cancel transaction")
	}
	return cancelled, nil
}

// nullableString maps an empty string to NULL.
func nullableString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// visibilityString maps a series visibility to its column value.
func visibilityString(v *entity.SeriesVisibility) *string {
	if v == nil {
		return nil
	}
	s := string(*v)
	return &s
}
