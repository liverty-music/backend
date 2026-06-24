package usecase

import (
	"context"
	"errors"
	"log/slog"

	"github.com/pannpers/go-apperr/apperr"
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

	// ListByUser retrieves all ticket journeys for the authenticated user.
	//
	// # Possible errors:
	//
	//   - Internal: query failure.
	ListByUser(ctx context.Context, userID string) ([]*entity.TicketJourney, error)
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

// SetStatus creates or updates a ticket journey.
//
// Before the upsert, it reads the current stored status to detect whether a
// meaningful change is occurring. The TICKET_JOURNEY.status_changed analytics
// event is published only when the new status differs from the prior one (or
// when no prior journey existed). Publishing is non-fatal — a failure is
// logged but does not roll back the already-persisted upsert.
func (uc *ticketJourneyUseCase) SetStatus(ctx context.Context, userID, eventID string, status entity.TicketJourneyStatus) error {
	// Capture the current status before the upsert so we can detect a change.
	var fromStatus entity.TicketJourneyStatus // zero value = UNSPECIFIED sentinel
	existing, err := uc.repo.Get(ctx, userID, eventID)
	if err != nil && !errors.Is(err, apperr.ErrNotFound) {
		return err
	}
	if existing != nil {
		fromStatus = existing.Status
	}

	if err := uc.repo.Upsert(ctx, &entity.TicketJourney{
		UserID:  userID,
		EventID: eventID,
		Status:  status,
	}); err != nil {
		return err
	}

	// Publish only when status actually changed to avoid noise.
	if fromStatus == status {
		return nil
	}

	if err := uc.publisher.PublishEvent(ctx, entity.SubjectTicketJourneyStatusChanged, entity.TicketJourneyStatusChangedData{
		UserID:     userID,
		EventID:    eventID,
		FromStatus: fromStatus.String(),
		ToStatus:   status.String(),
	}); err != nil {
		uc.logger.Error(ctx, "failed to publish TICKET_JOURNEY.status_changed event", err,
			slog.String("user_id", userID),
			slog.String("event_id", eventID),
			slog.String("from_status", fromStatus.String()),
			slog.String("to_status", status.String()),
		)
		// Non-fatal: the upsert is already persisted.
	}

	return nil
}

// Delete removes a ticket journey.
func (uc *ticketJourneyUseCase) Delete(ctx context.Context, userID, eventID string) error {
	return uc.repo.Delete(ctx, userID, eventID)
}

// ListByUser retrieves all ticket journeys for a user.
func (uc *ticketJourneyUseCase) ListByUser(ctx context.Context, userID string) ([]*entity.TicketJourney, error) {
	return uc.repo.ListByUser(ctx, userID)
}
