package entity

import "context"

// TicketJourneyStatus represents where a fan stands in the ticket acquisition process.
type TicketJourneyStatus int16

const (
	// TicketJourneyStatusTracking indicates the fan is monitoring for ticket sales information.
	TicketJourneyStatusTracking TicketJourneyStatus = 1
	// TicketJourneyStatusApplied indicates the fan has entered a ticket lottery.
	TicketJourneyStatusApplied TicketJourneyStatus = 2
	// TicketJourneyStatusLost indicates the fan lost a lottery or missed a payment deadline.
	TicketJourneyStatusLost TicketJourneyStatus = 3
	// TicketJourneyStatusUnpaid indicates the fan is awaiting payment completion.
	TicketJourneyStatusUnpaid TicketJourneyStatus = 4
	// TicketJourneyStatusPaid indicates the fan has completed payment and secured the ticket.
	TicketJourneyStatusPaid TicketJourneyStatus = 5
)

// String returns the lowercase name of the status for use in analytics event
// properties. The zero value (no prior journey) returns "UNSPECIFIED" as the
// sentinel used by the ticket.journey.status.changed event's from_status field.
func (s TicketJourneyStatus) String() string {
	switch s {
	case TicketJourneyStatusTracking:
		return "TRACKING"
	case TicketJourneyStatusApplied:
		return "APPLIED"
	case TicketJourneyStatusLost:
		return "LOST"
	case TicketJourneyStatusUnpaid:
		return "UNPAID"
	case TicketJourneyStatusPaid:
		return "PAID"
	default:
		return "UNSPECIFIED"
	}
}

// IsValid reports whether s is a recognized TicketJourneyStatus value.
func (s TicketJourneyStatus) IsValid() bool {
	return s >= TicketJourneyStatusTracking && s <= TicketJourneyStatusPaid
}

// TicketJourneySetBy says who set a ticket journey's status.
type TicketJourneySetBy string

const (
	// TicketJourneySetByFan is a status the fan chose.
	TicketJourneySetByFan TicketJourneySetBy = "fan"
	// TicketJourneySetByPlatform is a fact the platform recorded: Applied for a
	// lottery entry, Paid for a paid Order on Liverty Music.
	TicketJourneySetByPlatform TicketJourneySetBy = "platform"
	// TicketJourneySetByInference is a status the app inferred from the fan's
	// own action: Tracking after a calendar add.
	TicketJourneySetByInference TicketJourneySetBy = "inference"
)

// IsValid reports whether s is a recognized TicketJourneySetBy value.
func (s TicketJourneySetBy) IsValid() bool {
	switch s {
	case TicketJourneySetByFan, TicketJourneySetByPlatform, TicketJourneySetByInference:
		return true
	default:
		return false
	}
}

// TicketJourney represents a fan's personal ticket acquisition status for a specific event.
type TicketJourney struct {
	// UserID is the internal UUID of the fan.
	UserID string
	// EventID is the ID of the event being tracked.
	EventID string
	// Status is the current position in the ticket acquisition process.
	Status TicketJourneyStatus
	// StatusSetBy says who set Status.
	StatusSetBy TicketJourneySetBy
}

// ReplaceableBy reports whether the platform or the app may replace j with
// status, set by setBy. No journey (a nil j) is replaceable by anything. A
// status set by inference replaces no journey. Applied set by the platform
// replaces a journey at Tracking, Applied or Lost, whoever set it, but not one
// at Unpaid or Paid, where the fan already has a ticket. Any other status set
// by the platform replaces any journey. A status the fan sets is stored with
// [TicketJourneyRepository.Upsert] and never asks this.
func (j *TicketJourney) ReplaceableBy(status TicketJourneyStatus, setBy TicketJourneySetBy) bool {
	if j == nil {
		return true
	}
	switch setBy {
	case TicketJourneySetByInference:
		return false
	case TicketJourneySetByPlatform:
		if status == TicketJourneyStatusApplied {
			return j.Status != TicketJourneyStatusUnpaid && j.Status != TicketJourneyStatusPaid
		}
		return true
	default:
		return true
	}
}

// TicketJourneyRepository defines the persistence layer operations for ticket journeys.
type TicketJourneyRepository interface {
	// Get retrieves the ticket journey for the given user and event.
	//
	// # Possible errors:
	//
	//   - NotFound: no journey exists for the (userID, eventID) pair.
	//   - Internal: database query failure.
	Get(ctx context.Context, userID, eventID string) (*TicketJourney, error)

	// Upsert creates or updates a ticket journey for the given user and event,
	// with its status and who set it.
	//
	// # Possible errors:
	//
	//   - Unavailable: the announcement could not be recorded; nothing changed.
	//   - Internal: database execution failure.
	//
	// # Announces
	//
	// TICKET_JOURNEY.status_changed with the journey at its new status and set
	// by, together with the change. Nothing when the journey already had both.
	Upsert(ctx context.Context, journey *TicketJourney) error

	// UpsertIfReplaceable stores journey (its status set by the platform or by
	// inference) when the fan has no journey for the event or the fan's journey
	// is [TicketJourney.ReplaceableBy] it, and reports whether it stored it. A
	// journey that already has the status and set by is left unchanged. The
	// check and the write hold the journey's row (or the insert's conflict), so
	// a status the fan stores at the same moment is never replaced.
	//
	// # Possible errors:
	//
	//   - FailedPrecondition: the event or the fan does not exist.
	//   - Aborted: the journey was deleted and recreated by concurrent writes
	//     while it was being read; nothing changed, and a retry may succeed.
	//   - Unavailable: the announcement could not be recorded; nothing changed.
	//   - Internal: database execution failure.
	//
	// # Announces
	//
	// TICKET_JOURNEY.status_changed with the journey at its new status and set
	// by, together with the change. Nothing when it stored nothing.
	UpsertIfReplaceable(ctx context.Context, journey *TicketJourney) (bool, error)

	// Delete removes a ticket journey for the given user and event.
	// This is idempotent — deleting a non-existent journey succeeds silently.
	//
	// # Possible errors:
	//
	//   - Internal: database execution failure.
	Delete(ctx context.Context, userID, eventID string) error

	// ListByUser retrieves all ticket journeys for a given user.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	ListByUser(ctx context.Context, userID string) ([]*TicketJourney, error)

	// ListUserIDsTrackingSeries returns every user that has a Tracking ticket
	// journey ([TicketJourneyStatusTracking]) on any event of the given series,
	// once each, with the event to link that user to: the earliest upcoming
	// event (date today or later) the user tracks in the series, otherwise the
	// earliest event the user tracks there. It is the reverse-lookup the
	// discovered ticket sale announcement and reminder paths use to resolve their audience
	// from explicit fan intent (a Tracking journey is a "notify me about this
	// sale" signal) rather than follower proximity. Fans in later lifecycle
	// states (Applied/Paid/…) are intentionally excluded. The result has no
	// particular order.
	//
	// An empty result (no one tracking) returns (nil, nil).
	//
	// # Possible errors:
	//
	//   - InvalidArgument: If seriesID is empty.
	//   - Internal: database query failure.
	ListUserIDsTrackingSeries(ctx context.Context, seriesID string) ([]*SeriesTracker, error)

	// ListUserIDsAppliedSeries returns every user that has an Applied ticket
	// journey ([TicketJourneyStatusApplied]) on any event of the given series,
	// once each, with the event to link that user to: the earliest upcoming
	// event (date today or later) the user applied for in the series,
	// otherwise the earliest one. It is the audience of a discovered lottery's
	// RESULT_DAY reminder. Fans whose journeys on the series are all in
	// another status are excluded. The result has no particular order.
	//
	// An empty result (nobody applied) returns (nil, nil).
	//
	// # Possible errors:
	//
	//   - InvalidArgument: If seriesID is empty.
	//   - Internal: database query failure.
	ListUserIDsAppliedSeries(ctx context.Context, seriesID string) ([]*SeriesTracker, error)

	// ListUserIDsTrackingEvents returns every user that has a Tracking ticket
	// journey on at least one of the given events, once each, with the event
	// to link that user to, chosen among the given events the user tracks: the
	// earliest upcoming one (date today or later), otherwise the earliest. It
	// is the audience of an Organizer's ticket sale, which may offer only some
	// events of its Series. The result has no particular order; no event given
	// returns an empty result.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	ListUserIDsTrackingEvents(ctx context.Context, eventIDs []string) ([]*SeriesTracker, error)
}

// SeriesTracker is a user tracking a series, with the event a notification
// about that series links the user to.
type SeriesTracker struct {
	// UserID is the tracking user.
	UserID string
	// EventID is the user's linked event: the earliest upcoming event the user
	// tracks in the series, otherwise the earliest one the user tracks there.
	EventID string
}
