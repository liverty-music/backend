package entity

import "context"

// ParsedEmailData represents the structured result of parsing a ticket-related email.
type ParsedEmailData struct {
	// LotteryStart is the start of the lottery application period.
	LotteryStart *string `json:"lottery_start,omitempty"`
	// LotteryEnd is the end of the lottery application period.
	LotteryEnd *string `json:"lottery_end,omitempty"`
	// ApplicationURL is the URL for lottery application.
	ApplicationURL *string `json:"application_url,omitempty"`
	// LotteryResult is "won" or "lost".
	LotteryResult *string `json:"lottery_result,omitempty"`
	// PaymentStatus is "unpaid" or "paid".
	PaymentStatus *string `json:"payment_status,omitempty"`
	// PaymentDeadline is the payment due date.
	PaymentDeadline *string `json:"payment_deadline,omitempty"`
}

// TicketEmailParser defines the interface for parsing ticket-related emails.
type TicketEmailParser interface {
	// Parse extracts structured data from a ticket email body.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: unsupported email type for parsing.
	//   - Internal: API call failure or unparseable response.
	Parse(ctx context.Context, emailBody string, emailType TicketEmailType) (*ParsedEmailData, error)
}
