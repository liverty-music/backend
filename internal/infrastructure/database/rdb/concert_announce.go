package rdb

import (
	"context"
	"database/sql"
	"sort"

	"github.com/jackc/pgx/v5"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
)

// announcedConcertsQuery reads, inside the announcing transaction, the
// events to announce with their venues and performers.
const announcedConcertsQuery = `
	SELECT e.id, e.series_id, e.venue_id, e.listed_venue_name, e.local_event_date, e.start_at, e.open_at,
	       e.publish_state::text, e.per_account_limit,
	       v.name, v.admin_area,
	       COALESCE(ARRAY(SELECT ca.artist_id::text FROM concert_artists ca WHERE ca.event_id = e.id ORDER BY ca.artist_id), '{}')
	FROM events e
	JOIN venues v ON v.id = e.venue_id
	WHERE e.id = ANY($1::uuid[])
	ORDER BY e.local_event_date, e.id
`

const (
	// seriesVisibilityQuery reads a series' visibility; NULL for a
	// discovered series.
	seriesVisibilityQuery = `SELECT visibility::text FROM series WHERE id = $1`
	// performersOfEventsQuery reads the (artist, event) pairs of the events.
	performersOfEventsQuery = `
		SELECT artist_id::text, event_id::text FROM concert_artists
		WHERE event_id = ANY($1::uuid[])
		ORDER BY artist_id, event_id
	`
)

// announceNewConcerts records CONCERT.created on tx, once for each Artist in
// byArtist and each Series of the listed events, carrying the Concerts of
// those events with the Artist id as ce_subject. Nothing is recorded for an
// empty map.
func announceNewConcerts(ctx context.Context, out Announcer, tx pgx.Tx, byArtist map[string][]string) error {
	if len(byArtist) == 0 {
		return nil
	}
	var ids []string
	seen := map[string]bool{}
	for _, eventIDs := range byArtist {
		for _, id := range eventIDs {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	concerts, err := readAnnouncedConcerts(ctx, tx, ids)
	if err != nil {
		return err
	}

	artists := make([]string, 0, len(byArtist))
	for a := range byArtist {
		artists = append(artists, a)
	}
	sort.Strings(artists)
	var announcements []messaging.Announcement
	for _, artistID := range artists {
		bySeries := map[string]*entityv1.ConcertList{}
		var seriesOrder []string
		for _, id := range byArtist[artistID] {
			c, ok := concerts[id]
			if !ok {
				continue
			}
			list, ok := bySeries[c.SeriesID]
			if !ok {
				list = &entityv1.ConcertList{}
				bySeries[c.SeriesID] = list
				seriesOrder = append(seriesOrder, c.SeriesID)
			}
			list.Concerts = append(list.Concerts, pb.FromConcert(c))
		}
		for _, seriesID := range seriesOrder {
			announcements = append(announcements, messaging.Announcement{Payload: bySeries[seriesID], Subject: artistID})
		}
	}
	return announceAll(ctx, out, tx, entity.SubjectConcertCreated, announcements)
}

// readAnnouncedConcerts reads the Concerts of the event ids on tx, keyed by
// event id.
func readAnnouncedConcerts(ctx context.Context, tx pgx.Tx, ids []string) (map[string]*entity.Concert, error) {
	rows, err := tx.Query(ctx, announcedConcertsQuery, ids)
	if err != nil {
		return nil, toAppErr(err, "failed to read the announced concerts")
	}
	defer rows.Close()
	out := make(map[string]*entity.Concert, len(ids))
	for rows.Next() {
		var (
			c         entity.Concert
			state     sql.NullString
			limit     sql.NullInt32
			artistIDs []string
			venue     entity.Venue
		)
		if err := rows.Scan(&c.ID, &c.SeriesID, &c.VenueID, &c.ListedVenueName, &c.LocalDate, &c.StartTime, &c.OpenTime,
			&state, &limit, &venue.Name, &venue.AdminArea, &artistIDs); err != nil {
			return nil, toAppErr(err, "failed to scan an announced concert")
		}
		venue.ID = c.VenueID
		c.Venue = &venue
		c.PublishState = nullPublishState(state)
		if limit.Valid {
			v := int(limit.Int32)
			c.PerAccountLimit = &v
		}
		for _, id := range artistIDs {
			c.Artists = append(c.Artists, &entity.Artist{ID: id})
		}
		out[c.ID] = &c
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to read the announced concerts")
	}
	return out, nil
}
