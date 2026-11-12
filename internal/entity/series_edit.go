package entity

import (
	"log/slog"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// SlotHolder is the Event that holds a slot (the same Venue, local date and
// start time), with its Series' organizer, nil for a discovered Series.
type SlotHolder struct {
	// EventID is the holder's id.
	EventID string
	// SeriesID is the id of the holder's Series.
	SeriesID string
	// State is the holder's publish state; nil for a discovered Event.
	State *PublishState
	// OrganizerID is the organizer of the holder's Series; nil for a
	// discovered Series.
	OrganizerID *string
}

// SlotMergePlan decides what a PUBLISHED event does when its start time moves
// onto the slot that holder holds (nil when the slot is free). It returns the
// discovered holder to merge into the event: its fans' ticket journeys move to
// the event and its performers join, and it is removed. A free slot merges
// nothing (nil). A slot held by an Event of a first-party Series fails with
// FailedPrecondition.
func (e *Event) SlotMergePlan(holder *SlotHolder) (*SlotHolder, error) {
	if holder == nil {
		return nil, nil
	}
	if holder.OrganizerID != nil {
		return nil, apperr.New(codes.FailedPrecondition, "the new start time lands on an event of another first-party series",
			slog.String("event_id", e.ID), slog.String("holder_event_id", holder.EventID))
	}
	return holder, nil
}

// SlotClaim is what publishing a DRAFT event does with the slot it names.
type SlotClaim int8

const (
	// SlotClaimPublish means the slot is free: the DRAFT event becomes
	// PUBLISHED in place.
	SlotClaimPublish SlotClaim = iota + 1
	// SlotClaimExisting means a PUBLISHED event of the same Series already
	// holds the slot: the DRAFT is removed and nothing is published.
	SlotClaimExisting
	// SlotClaimDiscovered means a discovered event holds the slot: it is
	// claimed for the Series, keeping its id, the DRAFT's performers join it
	// and the DRAFT is removed.
	SlotClaimDiscovered
)

// ClaimSlotPlan decides what publishing the DRAFT event of the Series does with
// the slot that holder holds (nil when free). A slot held by a cancelled event
// or by an Event of another first-party Series fails with FailedPrecondition;
// such an Event is never moved, its sales, seat types and visibility stay with
// its Series.
func (e *Event) ClaimSlotPlan(seriesID string, holder *SlotHolder) (SlotClaim, error) {
	switch {
	case holder == nil:
		return SlotClaimPublish, nil
	case holder.State != nil && *holder.State == PublishStateCancelled:
		return 0, apperr.New(codes.FailedPrecondition, "the event's slot is held by a cancelled event",
			slog.String("event_id", e.ID), slog.String("holder_event_id", holder.EventID))
	case holder.SeriesID == seriesID:
		return SlotClaimExisting, nil
	case holder.OrganizerID != nil:
		return 0, apperr.New(codes.FailedPrecondition, "the event's slot is held by another series' event",
			slog.String("event_id", e.ID), slog.String("holder_event_id", holder.EventID))
	}
	return SlotClaimDiscovered, nil
}

// EventEditKind is what an organizer's edit does to one event of a Series.
type EventEditKind int8

const (
	// EventEditAdd adds the given event as a new DRAFT.
	EventEditAdd EventEditKind = iota + 1
	// EventEditDraft rewrites a DRAFT event: venue, date, times and limit.
	EventEditDraft
	// EventEditPublishedTimes changes the start and open time of a PUBLISHED
	// event, and nothing else.
	EventEditPublishedTimes
	// EventEditNone leaves a CANCELLED event, its performers included, as it is.
	EventEditNone
)

// EventEdit is one step of a Series update.
type EventEdit struct {
	// Kind is what the step does.
	Kind EventEditKind
	// Given is the event as the organizer gave it.
	Given *Event
	// Stored is the event as stored; nil for an added one.
	Stored *Event
	// MovesSlot is true when a PUBLISHED event's start time changes, so it
	// takes the slot of its new start time ([Event.SlotMergePlan]).
	MovesSlot bool
}

// SeriesUpdatePlan is the steps of a Series update, in the order the events
// were given, and the DRAFT events the organizer left out, which are removed.
type SeriesUpdatePlan struct {
	// Edits are the steps, one per given event.
	Edits []EventEdit
	// Removed are the ids of the stored DRAFT events that were not given.
	Removed []string
}

// PlanSeriesUpdate decides, from the Series' stored events by id and the events
// the organizer gave, what an update does: a given event without an id is
// added, one with an id is edited by its state, and a stored DRAFT event that
// was not given is removed. A given id that is not an event of the Series fails
// with FailedPrecondition.
func PlanSeriesUpdate(stored map[string]*Event, given []*Event) (SeriesUpdatePlan, error) {
	var plan SeriesUpdatePlan
	for _, e := range given {
		if e.ID != "" && stored[e.ID] == nil {
			return SeriesUpdatePlan{}, apperr.New(codes.FailedPrecondition, "a given event is not an event of the series",
				slog.String("event_id", e.ID))
		}
	}
	seen := make(map[string]bool, len(given))
	for _, e := range given {
		if e.ID == "" {
			plan.Edits = append(plan.Edits, EventEdit{Kind: EventEditAdd, Given: e})
			continue
		}
		seen[e.ID] = true
		current := stored[e.ID]
		edit := EventEdit{Given: e, Stored: current}
		switch {
		case current.HasPublishState(PublishStateDraft):
			edit.Kind = EventEditDraft
		case current.HasPublishState(PublishStatePublished):
			edit.Kind = EventEditPublishedTimes
			edit.MovesSlot = !sameOptionalTime(current.StartTime, e.StartTime)
		default:
			edit.Kind = EventEditNone
		}
		plan.Edits = append(plan.Edits, edit)
	}
	for id, e := range stored {
		if !seen[id] && e.HasPublishState(PublishStateDraft) {
			plan.Removed = append(plan.Removed, id)
		}
	}
	return plan, nil
}

// CheckPublishable fails with FailedPrecondition unless every id names a DRAFT
// event among the Series' stored events.
func CheckPublishable(stored map[string]*Event, ids []string) error {
	for _, id := range ids {
		if e := stored[id]; e == nil || !e.HasPublishState(PublishStateDraft) {
			return apperr.New(codes.FailedPrecondition, "a given event is not a DRAFT event of the series",
				slog.String("event_id", id))
		}
	}
	return nil
}

// CancelPlan is what cancelling events of a Series does.
type CancelPlan struct {
	// Cancel are the PUBLISHED events that become CANCELLED.
	Cancel []string
	// Remove are the DRAFT events, which are removed instead: a draft that
	// will not happen is never cancelled.
	Remove []string
}

// PlanCancel decides, from the Series' stored events by id, what cancelling the
// given ids does: a PUBLISHED event is cancelled ([Event.CanTransitionTo]) and a
// DRAFT one removed. An id that is not an event of the Series, or one already
// cancelled, fails with FailedPrecondition.
func PlanCancel(stored map[string]*Event, ids []string) (CancelPlan, error) {
	var plan CancelPlan
	for _, id := range ids {
		e := stored[id]
		switch {
		case e == nil:
			return CancelPlan{}, apperr.New(codes.FailedPrecondition, "a given event is not an event of the series",
				slog.String("event_id", id))
		case e.CanTransitionTo(PublishStateCancelled):
			plan.Cancel = append(plan.Cancel, id)
		case e.HasPublishState(PublishStateDraft):
			plan.Remove = append(plan.Remove, id)
		default:
			return CancelPlan{}, apperr.New(codes.FailedPrecondition, "a given event is already cancelled",
				slog.String("event_id", id))
		}
	}
	return plan, nil
}
