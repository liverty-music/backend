package entity

import (
	"context"
	"errors"
	"time"
)

// Admission is the permanent note that a Ticket was let in at the venue:
// which Ticket, through which Scanner and when. It is stored only by
// [TicketRepository.Admit], together with the Ticket's admitted time, a
// Ticket has at most one, and it is never changed or removed — not even when
// the Ticket is later voided — so it can serve as attendance evidence.
//
// Mirrors liverty_music.entity.v1.Admission.
type Admission struct {
	// TicketID is the Ticket let in; identifies the Admission.
	TicketID TicketID
	// EventID is the event of the Ticket.
	EventID string
	// ScannerID is the Scanner that scanned the Ticket.
	ScannerID ScannerID
	// ScannerNumber is the number of that Scanner within its
	// event, read together with the Admission for staff (受付1 ...).
	ScannerNumber int
	// AdmitTime is when the Ticket was let in; equal to the Ticket's
	// admitted time.
	AdmitTime time.Time
}

// ErrScannerNotUsable is the cause of the PermissionDenied that
// [TicketRepository.Admit] returns when the Scanner was revoked
// before the ticket could be admitted through it.
var ErrScannerNotUsable = errors.New("scanner is not usable")

// AdmissionRepository reads [Admission]s. Admissions are written only by
// [TicketRepository.Admit].
type AdmissionRepository interface {
	// GetByTicket returns the Admission of the Ticket together with the number
	// of its Scanner.
	//
	// # Possible errors
	//
	//  - NotFound: the Ticket has no Admission.
	//  - Internal: database failure.
	GetByTicket(ctx context.Context, ticketID TicketID) (*Admission, error)
}
