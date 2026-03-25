package usecase

import (
	"context"
	"encoding/json"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// TicketEmailUseCase defines the interface for ticket email business logic.
type TicketEmailUseCase interface {
	// Create parses a ticket email body, persists the results, and returns the created records.
	// One record is created per eventID.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: missing or invalid fields.
	//   - Internal: Gemini parsing or database failure.
	Create(ctx context.Context, userID string, eventIDs []string, emailType entity.TicketEmailType, rawBody string) ([]*entity.TicketEmail, error)

	// Update applies user corrections and triggers TicketJourney status updates.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: missing ticket email ID.
	//   - NotFound: record does not exist or belongs to another user.
	//   - Internal: unexpected failure.
	Update(ctx context.Context, userID, ticketEmailID string, params *entity.UpdateTicketEmail) (*entity.TicketEmail, error)
}

// ticketEmailUseCase implements the TicketEmailUseCase interface.
type ticketEmailUseCase struct {
	emailRepo   entity.TicketEmailRepository
	journeyRepo entity.TicketJourneyRepository
	parser      entity.TicketEmailParser
	logger      *logging.Logger
}

// NewTicketEmailUseCase creates a new ticket email use case.
func NewTicketEmailUseCase(
	emailRepo entity.TicketEmailRepository,
	journeyRepo entity.TicketJourneyRepository,
	parser entity.TicketEmailParser,
	logger *logging.Logger,
) TicketEmailUseCase {
	return &ticketEmailUseCase{
		emailRepo:   emailRepo,
		journeyRepo: journeyRepo,
		parser:      parser,
		logger:      logger,
	}
}

// Create parses a ticket email and persists one record per event.
func (uc *ticketEmailUseCase) Create(ctx context.Context, userID string, eventIDs []string, emailType entity.TicketEmailType, rawBody string) ([]*entity.TicketEmail, error) {
	if userID == "" {
		return nil, apperr.New(codes.InvalidArgument, "user_id is required")
	}
	if len(eventIDs) == 0 {
		return nil, apperr.New(codes.InvalidArgument, "at least one event_id is required")
	}
	if !emailType.IsValid() {
		return nil, apperr.New(codes.InvalidArgument, "invalid email type")
	}
	if rawBody == "" {
		return nil, apperr.New(codes.InvalidArgument, "raw_body is required")
	}

	parsed, err := uc.parser.Parse(ctx, rawBody, emailType)
	if err != nil {
		return nil, err
	}

	parsedJSON, err := json.Marshal(parsed)
	if err != nil {
		return nil, apperr.New(codes.Internal, "failed to marshal parsed data")
	}

	newEmail := uc.buildNewTicketEmail(userID, emailType, rawBody, parsedJSON, parsed)

	var results []*entity.TicketEmail
	for _, eventID := range eventIDs {
		newEmail.EventID = eventID
		created, err := uc.emailRepo.Create(ctx, newEmail)
		if err != nil {
			return nil, err
		}
		results = append(results, created)
	}

	return results, nil
}

// Update applies corrections and triggers TicketJourney status updates.
func (uc *ticketEmailUseCase) Update(ctx context.Context, userID, ticketEmailID string, params *entity.UpdateTicketEmail) (*entity.TicketEmail, error) {
	if ticketEmailID == "" {
		return nil, apperr.New(codes.InvalidArgument, "ticket_email_id is required")
	}

	existing, err := uc.emailRepo.GetByID(ctx, ticketEmailID)
	if err != nil {
		return nil, err
	}
	if existing.UserID != userID {
		return nil, apperr.New(codes.NotFound, "ticket email not found")
	}

	updated, err := uc.emailRepo.Update(ctx, ticketEmailID, params)
	if err != nil {
		return nil, err
	}

	status := uc.determineJourneyStatus(updated)
	if err := uc.journeyRepo.Upsert(ctx, &entity.TicketJourney{
		UserID:  userID,
		EventID: updated.EventID,
		Status:  status,
	}); err != nil {
		return nil, err
	}

	return updated, nil
}

// determineJourneyStatus returns the TicketJourney status stored in the email,
// falling back to TRACKING for lottery info emails or when no status is set.
func (uc *ticketEmailUseCase) determineJourneyStatus(te *entity.TicketEmail) entity.TicketJourneyStatus {
	if te.JourneyStatus != nil {
		return *te.JourneyStatus
	}
	return entity.TicketJourneyStatusTracking
}

// buildNewTicketEmail constructs a NewTicketEmail from parsed data.
func (uc *ticketEmailUseCase) buildNewTicketEmail(userID string, emailType entity.TicketEmailType, rawBody string, parsedJSON json.RawMessage, parsed *entity.ParsedEmailData) *entity.NewTicketEmail {
	ne := &entity.NewTicketEmail{
		UserID:     userID,
		EmailType:  emailType,
		RawBody:    rawBody,
		ParsedData: parsedJSON,
	}

	if parsed.ApplicationURL != nil {
		ne.ApplicationURL = *parsed.ApplicationURL
	}
	if parsed.LotteryStart != nil {
		if t, err := time.Parse(time.RFC3339, *parsed.LotteryStart); err == nil {
			ne.LotteryStartTime = &t
		}
	}
	if parsed.LotteryEnd != nil {
		if t, err := time.Parse(time.RFC3339, *parsed.LotteryEnd); err == nil {
			ne.LotteryEndTime = &t
		}
	}
	if parsed.PaymentDeadline != nil {
		if t, err := time.Parse(time.RFC3339, *parsed.PaymentDeadline); err == nil {
			ne.PaymentDeadlineTime = &t
		}
	}
	// Map raw Gemini output to a single JourneyStatus.
	ne.JourneyStatus = parsed.JourneyStatus(emailType)

	return ne
}
