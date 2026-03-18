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

// IsValid reports whether s is a recognized TicketJourneyStatus value.
func (s TicketJourneyStatus) IsValid() bool {
	return s >= TicketJourneyStatusTracking && s <= TicketJourneyStatusPaid
}

// TicketJourney represents a fan's personal ticket acquisition status for a specific event.
type TicketJourney struct {
	// UserID is the internal UUID of the fan.
	UserID string
	// EventID is the ID of the event being tracked.
	EventID string
	// Status is the current position in the ticket acquisition process.
	Status TicketJourneyStatus
}

// TicketJourneyRepository defines the persistence layer operations for ticket journeys.
type TicketJourneyRepository interface {
	// Upsert creates or updates a ticket journey for the given user and event.
	//
	// # Possible errors:
	//
	//   - Internal: database execution failure.
	Upsert(ctx context.Context, journey *TicketJourney) error

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
}
