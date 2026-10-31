package usecase

import (
	"context"

	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/entity"
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

	// List retrieves all ticket journeys for the authenticated user.
	//
	// # Possible errors:
	//
	//   - Internal: query failure.
	List(ctx context.Context, userID string) ([]*entity.TicketJourney, error)

	// MarkPaid sets the fan's ticket journey for the event to Paid for a paid
	// Order, from a checkout or a won lottery, replacing whatever status the fan
	// set. The change is not announced as a status change. Running it again for
	// the same Order leaves the journey Paid.
	//
	// # Possible errors:
	//
	//   - The upsert's failure, unchanged; the announcement is redelivered.
	MarkPaid(ctx context.Context, paid entity.OrderPaidData) error
}

// ticketJourneyUseCase implements the TicketJourneyUseCase interface.
type ticketJourneyUseCase struct {
	repo      entity.TicketJourneyRepository
	publisher EventPublisher
	logger    *logging.Logger
}

// Compile-time interface compliance check.
var _ TicketJourneyUseCase = (*ticketJourneyUseCase)(nil)

// NewTicketJourneyUseCase creates a new ticket journey use case.
func NewTicketJourneyUseCase(
	repo entity.TicketJourneyRepository,
	publisher EventPublisher,
	logger *logging.Logger,
) TicketJourneyUseCase {
	return &ticketJourneyUseCase{
		repo:      repo,
		publisher: publisher,
		logger:    logger,
	}
}

// SetStatus creates or updates a ticket journey through TicketJourney.Upsert,
// which announces a created journey or a changed status
// (TICKET_JOURNEY.status_changed) together with the change.
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

// List retrieves all ticket journeys for a user.
func (uc *ticketJourneyUseCase) List(ctx context.Context, userID string) ([]*entity.TicketJourney, error) {
	return uc.repo.ListByUser(ctx, userID)
}

// MarkPaid implements [TicketJourneyUseCase].
func (uc *ticketJourneyUseCase) MarkPaid(ctx context.Context, paid entity.OrderPaidData) error {
	return uc.repo.Upsert(ctx, &entity.TicketJourney{
		UserID:  paid.UserID,
		EventID: paid.EventID,
		Status:  entity.TicketJourneyStatusPaid,
	})
}
