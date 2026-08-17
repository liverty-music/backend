package entity

import (
	"context"
	"time"
)

// SuppressedConcert is the natural key of a published concert an operator
// deleted, recorded so a later discovery run does not auto-publish or re-stage
// the same physical slot.
//
// The key mirrors the events physical identity (venue, local date, start time)
// with NULLS NOT DISTINCT semantics: an unknown (nil) StartTime collapses onto
// the same slot the way the events unique key does. It is deliberately
// independent of the performing artist so a co-headliner's per-artist discovery
// cannot resurrect a slot the operator removed.
//
// Suppression is distinct from RejectedConcertLog: the rejection log is
// analysis-only and never suppresses, whereas a SuppressedConcert actively
// blocks re-creation until it is removed through the deliberate un-suppress path.
type SuppressedConcert struct {
	// ID is the primary key (UUIDv7, application-generated).
	ID string
	// VenueID is the resolved venue of the deleted event. It carries no foreign
	// key so the suppression survives independently of venue lifecycle.
	VenueID string
	// LocalEventDate is the local calendar date of the deleted event.
	LocalEventDate time.Time
	// StartTime is the start time of the deleted event. Nil when unknown; a nil
	// value collapses the slot under NULLS NOT DISTINCT.
	StartTime *time.Time
	// SuppressedTime is when the concert was suppressed (i.e. deleted).
	SuppressedTime time.Time
}

// SuppressedConcertRepository is the data access interface for the moderation
// suppression set consulted by discovery.
type SuppressedConcertRepository interface {
	// Insert records a suppression entry. It is idempotent on the natural key
	// (venue_id, local_event_date, start_at): re-inserting an already-suppressed
	// key is a no-op success.
	//
	// # Possible errors
	//
	//  - Internal: unexpected failure.
	Insert(ctx context.Context, sc *SuppressedConcert) error

	// Exists reports whether a suppression entry matches the given resolved
	// natural key. startTime is matched NULL-safely (IS NOT DISTINCT FROM), so a
	// nil startTime matches a suppressed unknown-start slot.
	//
	// # Possible errors
	//
	//  - Internal: unexpected failure.
	Exists(ctx context.Context, venueID string, localDate time.Time, startTime *time.Time) (bool, error)

	// Delete removes the suppression entry for the given natural key — the
	// deliberate un-suppress path that re-enables discovery for that slot. It is
	// idempotent: removing an absent entry is a no-op success. startTime is
	// matched NULL-safely.
	//
	// # Possible errors
	//
	//  - Internal: unexpected failure.
	Delete(ctx context.Context, venueID string, localDate time.Time, startTime *time.Time) error
}
