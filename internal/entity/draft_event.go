package entity

import (
	"time"
)

// DraftEvent is a performance being authored under a first-party DRAFT series.
// While the series is a DRAFT, its performances live here (not in the live
// "events" table) so the natural-key space (uq_events_natural_key) stays clean
// and no discovered slot is claimed before Publish. On Publish each DraftEvent
// is materialized into events via the natural-key upsert with the supersede /
// suppression / cross-organizer rules, then the draft rows are deleted.
type DraftEvent struct {
	// ID is the unique draft-event identifier (UUIDv7).
	ID string
	// SeriesID is the parent first-party series being authored.
	SeriesID string
	// VenueID is the venue resolved (Places get-or-create) at draft time.
	VenueID string
	// ListedVenueName is the raw venue name the organizer entered, preserved for
	// display and re-resolution.
	ListedVenueName *string
	// LocalDate is the calendar date of the performance (midnight UTC).
	LocalDate time.Time
	// StartTime is the performance start time (absolute), if set.
	StartTime *time.Time
	// OpenTime is the doors-open time (absolute), if set.
	OpenTime *time.Time
}

// ToDiscoveredEvent adapts a DraftEvent into a DiscoveredEvent so the shared
// discovery-pipeline helpers (suppression check, duplicate detection, insert)
// can operate on it without modification. The ListedVenueName is dereferenced
// safely; a nil pointer is mapped to the empty string, which the helpers treat
// as a venue-TBA event and skip.
func (d *DraftEvent) ToDiscoveredEvent() *DiscoveredEvent {
	name := ""
	if d.ListedVenueName != nil {
		name = *d.ListedVenueName
	}
	ev := &DiscoveredEvent{
		ListedVenueName: name,
		LocalDate:       d.LocalDate,
	}
	if d.StartTime != nil {
		ev.StartTime = *d.StartTime
	}
	if d.OpenTime != nil {
		ev.OpenTime = *d.OpenTime
	}
	return ev
}
