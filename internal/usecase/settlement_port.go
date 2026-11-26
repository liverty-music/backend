package usecase

import (
	"context"

	"github.com/liverty-music/backend/internal/entity"
)

// ProcessedWebhookEventRepository stores the provider event ids that have
// already been applied so that duplicate webhook deliveries are no-ops.
// Interfaces are defined where consumed (AGENTS.md rule).
type ProcessedWebhookEventRepository interface {
	// IsProcessed returns true if the provider event id has already been
	// applied.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	IsProcessed(ctx context.Context, providerEventID string) (bool, error)

	// MarkProcessed records that the provider event id has been applied.
	// Idempotent: a duplicate insert is silently accepted (ON CONFLICT DO NOTHING).
	//
	// # Possible errors
	//
	//  - Internal: database execution failure.
	MarkProcessed(ctx context.Context, providerEventID string) error
}

// EventStartTimeRepository reads the event start time from the database. A
// minimal interface so the settlement use case does not depend on the full
// concert repository. Interfaces are defined where consumed (AGENTS.md rule).
type EventStartTimeRepository interface {
	// GetEventStartTime returns the current local date and start time of the
	// event identified by eventID with the time zone of its Venue. A nil
	// StartTime means the event's start time is not yet published; the
	// settlement sweeper withholds payout rather than erroring.
	//
	// # Possible errors
	//
	//  - NotFound: no event with the given id exists.
	//  - Internal: database query failure.
	GetEventStartTime(ctx context.Context, eventID string) (*entity.EventStart, error)
}
