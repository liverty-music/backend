package entity

import (
	"context"
	"encoding/json"
	"time"
)

// TicketEmailType classifies the kind of ticket-related email imported.
type TicketEmailType int16

const (
	// TicketEmailTypeLotteryInfo is a lottery announcement email with dates and application URL.
	TicketEmailTypeLotteryInfo TicketEmailType = 1
	// TicketEmailTypeLotteryResult is a lottery result email indicating win/loss.
	TicketEmailTypeLotteryResult TicketEmailType = 2
)

// IsValid reports whether t is a recognized TicketEmailType value.
func (t TicketEmailType) IsValid() bool {
	return t >= TicketEmailTypeLotteryInfo && t <= TicketEmailTypeLotteryResult
}

// LotteryResult indicates whether the user won or lost a ticket lottery.
type LotteryResult int16

const (
	// LotteryResultWon indicates the user won the lottery.
	LotteryResultWon LotteryResult = 1
	// LotteryResultLost indicates the user lost the lottery.
	LotteryResultLost LotteryResult = 2
)

// IsValid reports whether r is a recognized LotteryResult value.
func (r LotteryResult) IsValid() bool {
	return r >= LotteryResultWon && r <= LotteryResultLost
}

// PaymentStatus indicates the payment state for a won lottery ticket.
type PaymentStatus int16

const (
	// PaymentStatusUnpaid indicates payment is pending.
	PaymentStatusUnpaid PaymentStatus = 1
	// PaymentStatusPaid indicates payment has been completed.
	PaymentStatusPaid PaymentStatus = 2
)

// IsValid reports whether s is a recognized PaymentStatus value.
func (s PaymentStatus) IsValid() bool {
	return s >= PaymentStatusUnpaid && s <= PaymentStatusPaid
}

// TicketEmail represents an imported ticket-related email parsed by Gemini Flash.
type TicketEmail struct {
	// ID is the unique identifier (UUIDv7).
	ID string
	// UserID is the internal UUID of the fan who imported this email.
	UserID string
	// EventID is the ID of the event associated with this email.
	EventID string
	// EmailType classifies the kind of ticket email.
	EmailType TicketEmailType
	// RawBody is the email text as provided (and optionally redacted) by the user.
	RawBody string
	// ParsedData stores the structured output from Gemini Flash parsing.
	ParsedData json.RawMessage
	// PaymentDeadlineTime is the date by which payment must be completed. Nil if not applicable.
	PaymentDeadlineTime *time.Time
	// LotteryStartTime is the start of the lottery application period. Nil if not applicable.
	LotteryStartTime *time.Time
	// LotteryEndTime is the end of the lottery application period. Nil if not applicable.
	LotteryEndTime *time.Time
	// ApplicationURL is the URL for lottery application. Empty if not applicable.
	ApplicationURL string
	// LotteryResult indicates win or loss. Nil if not applicable.
	LotteryResult *LotteryResult
	// PaymentStatus indicates whether payment has been completed. Nil if not applicable.
	PaymentStatus *PaymentStatus
}

// NewTicketEmail contains the fields required to create a new TicketEmail record.
type NewTicketEmail struct {
	UserID              string
	EventID             string
	EmailType           TicketEmailType
	RawBody             string
	ParsedData          json.RawMessage
	PaymentDeadlineTime *time.Time
	LotteryStartTime    *time.Time
	LotteryEndTime      *time.Time
	ApplicationURL      string
	LotteryResult       *LotteryResult
	PaymentStatus       *PaymentStatus
}

// UpdateTicketEmail contains the fields that can be corrected by the user.
type UpdateTicketEmail struct {
	PaymentDeadlineTime *time.Time
	LotteryStartTime    *time.Time
	LotteryEndTime      *time.Time
	ApplicationURL      *string
	LotteryResult       *LotteryResult
	PaymentStatus       *PaymentStatus
}

// TicketEmailRepository defines the persistence layer operations for ticket emails.
type TicketEmailRepository interface {
	// Create persists a new ticket email record and returns it with the generated ID.
	//
	// # Possible errors:
	//
	//   - Internal: database execution failure.
	Create(ctx context.Context, params *NewTicketEmail) (*TicketEmail, error)

	// Update applies user corrections to an existing ticket email record.
	//
	// # Possible errors:
	//
	//   - NotFound: record does not exist.
	//   - Internal: database execution failure.
	Update(ctx context.Context, id string, params *UpdateTicketEmail) (*TicketEmail, error)

	// GetByID retrieves a ticket email by its unique identifier.
	//
	// # Possible errors:
	//
	//   - NotFound: record does not exist.
	//   - Internal: database query failure.
	GetByID(ctx context.Context, id string) (*TicketEmail, error)

	// ListByUserAndEvent retrieves all ticket emails for a given user and event.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	ListByUserAndEvent(ctx context.Context, userID, eventID string) ([]*TicketEmail, error)
}
