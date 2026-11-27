package usecase

import (
	"time"

	"github.com/liverty-music/backend/internal/entity"
)

// sameConcertOutcome is what decideFoundEvent concludes about a found event.
type sameConcertOutcome int

const (
	// outcomeNothing: the Artist already has the event (decision.event);
	// nothing is stored.
	outcomeNothing sameConcertOutcome = iota
	// outcomeFill: the found start time is filled onto the Artist's
	// unknown-start event (decision.event).
	outcomeFill
	// outcomeMergeInto: the Artist's unknown-start event (decision.source) is
	// merged into another Artist's event holding the found start time
	// (decision.event).
	outcomeMergeInto
	// outcomeAddPerformer: the Artist is added as a performer of another
	// Artist's event at the found slot (decision.event).
	outcomeAddPerformer
	// outcomeStage: the found event collides with decision.event and an
	// admin decides.
	outcomeStage
	// outcomePublish: the found event is a new event.
	outcomePublish
)

// String names the outcome for logs.
func (o sameConcertOutcome) String() string {
	return [...]string{"nothing", "fill", "merge_into", "add_performer", "stage", "publish"}[o]
}

// foundEvent is an event discovery found, or a ConcertCandidate an admin
// approves, once its Venue is known.
type foundEvent struct {
	artistID        string
	listedVenueName string
	venueID         string
	localDate       time.Time
	startTime       *entity.TimeOfDay
}

// sameConcertDecision is the outcome of decideFoundEvent and the events it
// names.
type sameConcertDecision struct {
	outcome sameConcertOutcome
	// event is the stored event the outcome is about: the event the Artist
	// already has (Nothing), the event to fill (Fill), the target of a merge
	// (MergeInto), the event to add the Artist to (AddPerformer) or the event
	// collided with (Stage). Nil for Publish.
	event *entity.Concert
	// source is the Artist's event merged away (MergeInto only).
	source *entity.Concert
}

// decideFoundEvent decides whether a found event is a concert already
// stored, the one decision discovery (CreateFromDiscovered) and approval
// (Approve) share. artistEvents are the Artist's events (at any Venue; those
// on other dates than the found one are ignored), venueEvents the events at the found Venue and date (of any
// Artist), and claimed the unknown-start events this batch already filled.
// The rules are tried in order (create-from-discovered spec, "Re-discovery
// of the artist's own event", "Start time held by another Event", "Same
// Artist at another venue at the same time", "Collision is staged, anything
// else is published"):
//
//  1. an event of the Artist at the Venue with the same start time (or both
//     unknown) listed under the found name → Nothing;
//  2. with no found start, a timed event of the Artist at the Venue listed
//     under the found name → Nothing;
//  3. with no found start, a timed event of the Artist at the Venue → Stage;
//  4. an event of the Artist at the Venue with the same start time (or both
//     unknown) → Stage;
//  5. with a found start, an event of the Artist on the date with that start
//     time at another Venue → Stage, since one Artist cannot play two
//     venues at once (checked before a fill, so an approval reports it as a
//     collision and a discovery never fills such a time);
//  6. with a found start, an unknown-start event of the Artist at the Venue
//     not yet claimed → Fill (for an Organizer's event: Nothing when it is
//     listed under the found name, Stage otherwise), unless
//     another event holds the slot: MergeInto
//     it when it is listed under the found name and neither has a publish
//     state, Stage otherwise;
//  7. another Artist's event at the Venue with no publish state, the same
//     known start time and listed under the found name → AddPerformer;
//  8. another Artist's event at the Venue with the same start time (or both
//     unknown), or, with no found start, with a start time → Stage;
//  9. otherwise → Publish.
//
// An event with a publish state (an Organizer's event) is never a target of
// AddPerformer or MergeInto, nor a source of MergeInto.
func decideFoundEvent(found foundEvent, artistEvents, venueEvents []*entity.Concert, claimed map[string]bool) sameConcertDecision {
	var own, others []*entity.Concert
	for _, e := range venueEvents {
		if e.HasPerformer(found.artistID) {
			own = append(own, e)
		} else {
			others = append(others, e)
		}
	}
	if d, ok := decideAgainstOwn(found, own); ok {
		return d
	}
	if found.startTime != nil {
		if e := firstOf(artistEvents, func(e *entity.Concert) bool {
			return e.VenueID != found.venueID && e.LocalDate.Equal(found.localDate) && e.HasStartTime(found.startTime)
		}); e != nil {
			return sameConcertDecision{outcome: outcomeStage, event: e}
		}
	}
	if d, ok := decideFill(found, own, others, claimed); ok {
		return d
	}
	return decideAgainstOthers(found, others)
}

// decideAgainstOwn applies rules 1 to 4 of decideFoundEvent to the Artist's
// events at the Venue; ok is false when none applies.
func decideAgainstOwn(found foundEvent, own []*entity.Concert) (sameConcertDecision, bool) {
	sameOrTimed := func(e *entity.Concert) bool {
		return e.HasStartTime(found.startTime) || (found.startTime == nil && e.StartTime != nil)
	}
	if e := firstOf(own, func(e *entity.Concert) bool { return sameOrTimed(e) && e.ListedUnder(found.listedVenueName) }); e != nil {
		return sameConcertDecision{outcome: outcomeNothing, event: e}, true
	}
	if e := firstOf(own, sameOrTimed); e != nil {
		return sameConcertDecision{outcome: outcomeStage, event: e}, true
	}
	return sameConcertDecision{}, false
}

// decideFill applies rule 6 of decideFoundEvent: a found start time for the
// Artist's unknown-start event at the Venue; ok is false when it does not
// apply.
func decideFill(found foundEvent, own, others []*entity.Concert, claimed map[string]bool) (sameConcertDecision, bool) {
	if found.startTime == nil {
		return sameConcertDecision{}, false
	}
	unknown := firstOf(own, func(e *entity.Concert) bool { return e.StartTime == nil && !claimed[e.ID] })
	if unknown == nil {
		return sameConcertDecision{}, false
	}
	holder := firstOf(others, func(e *entity.Concert) bool { return e.HasStartTime(found.startTime) })
	switch {
	case holder == nil && unknown.PublishState != nil && unknown.ListedUnder(found.listedVenueName):
		// An Organizer's event is never filled by discovery or approval; once
		// it is listed under the found name (an admin kept it), the found
		// event is that event and nothing is staged again.
		return sameConcertDecision{outcome: outcomeNothing, event: unknown}, true
	case holder == nil && unknown.PublishState != nil:
		return sameConcertDecision{outcome: outcomeStage, event: unknown}, true
	case holder == nil:
		return sameConcertDecision{outcome: outcomeFill, event: unknown}, true
	case holder.ListedUnder(found.listedVenueName) && holder.PublishState == nil && unknown.PublishState == nil:
		return sameConcertDecision{outcome: outcomeMergeInto, event: holder, source: unknown}, true
	default:
		return sameConcertDecision{outcome: outcomeStage, event: holder}, true
	}
}

// decideAgainstOthers applies rules 7 to 9 of decideFoundEvent to the other
// Artists' events at the Venue.
func decideAgainstOthers(found foundEvent, others []*entity.Concert) sameConcertDecision {
	if found.startTime != nil {
		if e := firstOf(others, func(e *entity.Concert) bool {
			return e.PublishState == nil && e.HasStartTime(found.startTime) && e.ListedUnder(found.listedVenueName)
		}); e != nil {
			return sameConcertDecision{outcome: outcomeAddPerformer, event: e}
		}
	}
	if e := firstOf(others, func(e *entity.Concert) bool {
		return e.HasStartTime(found.startTime) || (found.startTime == nil && e.StartTime != nil)
	}); e != nil {
		return sameConcertDecision{outcome: outcomeStage, event: e}
	}
	return sameConcertDecision{outcome: outcomePublish}
}

// firstOf returns the first event that matches, or nil.
func firstOf(events []*entity.Concert, match func(*entity.Concert) bool) *entity.Concert {
	for _, e := range events {
		if match(e) {
			return e
		}
	}
	return nil
}
