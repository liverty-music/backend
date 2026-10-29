package entity

import (
	"context"
	"errors"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// Event represents a single occurrence on a specific date at a specific venue.
//
// Every Event belongs to a parent [Series] that owns metadata shared across multiple
// events of the same engagement (tour title, source URL, classification). Series-level
// fields are intentionally absent from Event to avoid duplication when one series owns
// several events. Event is generic: a kind of event embeds it and adds its own data,
// as [Concert] adds the performing artists (stored in concert_artists).
//
// See [EventProto] for the wire representation.
//
// [EventProto]: https://github.com/liverty-music/specification/blob/main/proto/liverty_music/entity/v1/event.proto
type Event struct {
	// ID is the unique identifier for the event (UUIDv7).
	ID string
	// SeriesID is the foreign key reference to the parent [Series].
	SeriesID string
	// VenueID is the ID of the venue where the event takes place.
	VenueID string
	// Venue is the resolved venue entity. Populated by the server on read operations.
	Venue *Venue
	// ListedVenueName is the raw venue name as listed in the source data.
	// It preserves the original scraped text separately from the normalized Venue.Name.
	// Nullable: legacy rows inserted before this field was added will have NULL.
	ListedVenueName *string
	// LocalDate represents the calendar date of the event in the local timezone.
	//
	// Specifications:
	// - Location MUST be set to time.UTC.
	// - Time components (Hour, Minute, Second, Nanosecond) MUST be zero (00:00:00).
	// This ensures that the date remains consistent when saved to a Postgres DATE type.
	// It avoids "date shifting" issues during timezone conversions.
	LocalDate time.Time
	// StartTime is the specific starting time of the event (optional).
	StartTime *time.Time
	// OpenTime is the time when doors open (optional).
	OpenTime *time.Time
	// PublishState is where the event is in the organizer publish lifecycle.
	// Set for an Event of a first-party series; nil for an Event of a
	// discovered series.
	PublishState *PublishState
	// PerAccountLimit is the most tickets one account may hold for the event,
	// across every TicketSale, SeatType and official resale of it: 1 to 10.
	// Set for an Event of a first-party series; nil for an Event of a
	// discovered series.
	PerAccountLimit *int
}

const (
	// EventDefaultPerAccountLimit is the per-account limit of an Event created
	// without one.
	EventDefaultPerAccountLimit = 4
	// EventMaxPerAccountLimit is the highest per-account limit: one
	// AdmissionCode presents at most 10 tickets, and a companion group enters
	// together with one code.
	EventMaxPerAccountLimit = 10
)

// HeldTickets is what one account holds for one event: the issued Tickets of
// its Paid Orders plus the ticket counts of its Pending Orders, and the
// SeatType they are all of. SeatTypeID is nil exactly when Count is 0.
type HeldTickets struct {
	// Count is the number of tickets held.
	Count int
	// SeatTypeID is the SeatType of every ticket held; nil when none is held.
	SeatTypeID *SeatTypeID
}

// NewAuthoredEvent returns a new Event of a first-party series. It starts
// DRAFT: it holds no slot and is not a [Concert] until it is published. A nil
// perAccountLimit means none was given, and the limit is
// [EventDefaultPerAccountLimit].
func NewAuthoredEvent(seriesID, venueID string, listedVenueName *string, localDate time.Time, startTime, openTime *time.Time, perAccountLimit *int) *Event {
	state := PublishStateDraft
	limit := EventDefaultPerAccountLimit
	if perAccountLimit != nil {
		limit = *perAccountLimit
	}
	return &Event{
		ID:              NewID(),
		SeriesID:        seriesID,
		VenueID:         venueID,
		ListedVenueName: listedVenueName,
		LocalDate:       localDate,
		StartTime:       startTime,
		OpenTime:        openTime,
		PublishState:    &state,
		PerAccountLimit: &limit,
	}
}

// ValidatePerAccountLimit reports whether the per-account limit, when set, is
// 1 to 10. The entity layer returns stdlib errors; callers wrap them with an
// apperr code.
func (e *Event) ValidatePerAccountLimit() error {
	if e.PerAccountLimit != nil && (*e.PerAccountLimit < 1 || *e.PerAccountLimit > EventMaxPerAccountLimit) {
		return errors.New("per-account limit must be 1 to 10")
	}
	return nil
}

// Limit returns the per-account limit, or [EventDefaultPerAccountLimit] for
// an Event that has none.
func (e *Event) Limit() int {
	if e.PerAccountLimit == nil {
		return EventDefaultPerAccountLimit
	}
	return *e.PerAccountLimit
}

// Fits reports whether an application by an account for count tickets of the
// seat type fits the event, given what the account holds for it: the held
// tickets plus count are at most the per-account limit, and every held ticket
// is of that seat type. An account that holds no ticket for the event may
// apply for any seat type.
func (e *Event) Fits(held HeldTickets, seatTypeID SeatTypeID, count int) bool {
	if held.SeatTypeID != nil && *held.SeatTypeID != seatTypeID {
		return false
	}
	return held.Count+count <= e.Limit()
}

// HasPublishState reports whether the event is in the given publish state. An
// Event of a discovered series has no publish state, so it is in none.
func (e *Event) HasPublishState(state PublishState) bool {
	return e.PublishState != nil && *e.PublishState == state
}

// SameSlot reports whether e and other are the same performance: neither is
// DRAFT, and they share the Venue, the local date and the start time, where an
// unknown start time matches only another unknown start time. A DRAFT event
// holds no slot, so it is the same performance as no other Event.
func (e *Event) SameSlot(other *Event) bool {
	if e.HasPublishState(PublishStateDraft) || other.HasPublishState(PublishStateDraft) {
		return false
	}
	return e.VenueID == other.VenueID &&
		e.LocalDate.Equal(other.LocalDate) &&
		sameOptionalTime(e.StartTime, other.StartTime)
}

// CanTransitionTo reports whether the event may move to next. The only
// transitions are DRAFT to PUBLISHED and PUBLISHED to CANCELLED: a draft that
// will not happen is removed, never cancelled, and CANCELLED is terminal. An
// Event of a discovered series has no transitions.
func (e *Event) CanTransitionTo(next PublishState) bool {
	switch {
	case e.HasPublishState(PublishStateDraft):
		return next == PublishStatePublished
	case e.HasPublishState(PublishStatePublished):
		return next == PublishStateCancelled
	default:
		return false
	}
}

// IsPubliclyVisible reports whether the event appears on fan-facing public and
// follower surfaces. An Event of a discovered series always does. An Event of a
// first-party series does only when it is PUBLISHED and the series is PUBLIC.
func (e *Event) IsPubliclyVisible(series *Series) bool {
	if !series.IsFirstParty() {
		return true
	}
	return e.HasPublishState(PublishStatePublished) && series.IsPublic()
}

// HasEventPage reports whether the event has a public event page
// (/events/:id): its series is first-party and PUBLIC, and the event is
// PUBLISHED or CANCELLED. Unlike [Event.IsPubliclyVisible], a CANCELLED event
// keeps its page so a shared link can still explain the cancellation.
func (e *Event) HasEventPage(series *Series) bool {
	if !series.IsFirstParty() || !series.IsPublic() {
		return false
	}
	return e.HasPublishState(PublishStatePublished) || e.HasPublishState(PublishStateCancelled)
}

// CheckChange returns FailedPrecondition when next would change what the
// event's publish state fixes. A DRAFT event may change freely. A PUBLISHED
// event keeps its Venue, local date and per-account limit; its open time may
// change, and its start time may change only while no ticket sale names it
// (onSale false). A CANCELLED event may not change at all.
func (e *Event) CheckChange(next *Event, onSale bool) error {
	switch {
	case e.HasPublishState(PublishStateCancelled):
		if next.VenueID != e.VenueID || !next.LocalDate.Equal(e.LocalDate) ||
			!sameOptionalTime(next.StartTime, e.StartTime) || !sameOptionalTime(next.OpenTime, e.OpenTime) ||
			next.Limit() != e.Limit() {
			return apperr.New(codes.FailedPrecondition, "a cancelled event cannot change")
		}
	case e.HasPublishState(PublishStatePublished):
		if next.VenueID != e.VenueID || !next.LocalDate.Equal(e.LocalDate) {
			return apperr.New(codes.FailedPrecondition, "the venue and date of a published event cannot change; cancel it and add a new date")
		}
		if next.Limit() != e.Limit() {
			return apperr.New(codes.FailedPrecondition, "the per-account limit of a published event cannot change")
		}
		if onSale && !sameOptionalTime(next.StartTime, e.StartTime) {
			return apperr.New(codes.FailedPrecondition, "the start time of an event on sale cannot change")
		}
	}
	return nil
}

// sameOptionalTime reports whether two optional instants are equal, where two
// unknown (nil) instants are equal and an unknown one equals no known one.
func sameOptionalTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// HasPassedAt reports whether the event's local date is before t's date in
// Japan time: the whole event day still counts as upcoming.
func (e *Event) HasPassedAt(t time.Time) bool {
	y, m, d := t.In(japanTime).Date()
	return e.LocalDate.Before(time.Date(y, m, d, 0, 0, 0, 0, time.UTC))
}

// EventRepository reads one [Event]'s current date, times and publish state.
// Implementations live in internal/infrastructure/database/rdb/.
type EventRepository interface {
	// Get returns the event's id, series, venue, current local date, open
	// time, start time and publish state, read afresh; an unknown time is nil,
	// and the publish state is nil for an event of a discovered series. Venue
	// is not resolved.
	//
	// # Possible errors
	//
	//  - NotFound: no event has the id.
	//  - Internal: database failure.
	Get(ctx context.Context, id string) (*Event, error)
}
