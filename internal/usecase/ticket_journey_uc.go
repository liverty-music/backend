package usecase

import (
	"context"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
)

// TicketJourneyUseCase defines the interface for ticket journey business logic.
type TicketJourneyUseCase interface {
	// SetStatus creates or updates a ticket journey for the authenticated user.
	//
	// # Possible errors:
	//
	//   - Internal: unexpected failure.
	SetStatus(ctx context.Context, userID, eventID string, status entity.TicketJourneyStatus) error

	// Delete removes a ticket journey for the authenticated user and event.
	// Idempotent — deleting a non-existent journey succeeds silently.
	//
	// # Possible errors:
	//
	//   - Internal: unexpected failure.
	Delete(ctx context.Context, userID, eventID string) error

	// ListByUser retrieves all ticket journeys for the authenticated user.
	//
	// # Possible errors:
	//
	//   - Internal: query failure.
	ListByUser(ctx context.Context, userID string) ([]*entity.TicketJourney, error)
}

// ticketJourneyUseCase implements the TicketJourneyUseCase interface.
type ticketJourneyUseCase struct {
	repo   entity.TicketJourneyRepository
	logger *logging.Logger
}

// NewTicketJourneyUseCase creates a new ticket journey use case.
func NewTicketJourneyUseCase(
	repo entity.TicketJourneyRepository,
	logger *logging.Logger,
) TicketJourneyUseCase {
	return &ticketJourneyUseCase{
		repo:   repo,
		logger: logger,
	}
}

// SetStatus creates or updates a ticket journey.
func (uc *ticketJourneyUseCase) SetStatus(ctx context.Context, userID, eventID string, status entity.TicketJourneyStatus) error {
	return uc.repo.Upsert(ctx, &entity.TicketJourney{
		UserID:  userID,
		EventID: eventID,
		Status:  status,
	})
}

// Delete removes a ticket journey.
func (uc *ticketJourneyUseCase) Delete(ctx context.Context, userID, eventID string) error {
	return uc.repo.Delete(ctx, userID, eventID)
}

// ListByUser retrieves all ticket journeys for a user.
func (uc *ticketJourneyUseCase) ListByUser(ctx context.Context, userID string) ([]*entity.TicketJourney, error) {
	return uc.repo.ListByUser(ctx, userID)
}
