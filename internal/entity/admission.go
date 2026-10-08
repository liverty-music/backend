package entity

import (
	"context"
	"errors"
	"time"
)

// Admission is the permanent note that a Ticket was let in at the venue:
// which Ticket, through which ReceptionLink and when. It is stored only by
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
	// ReceptionLinkID is the ReceptionLink that scanned the Ticket.
	ReceptionLinkID ReceptionLinkID
	// ReceptionLinkNumber is the number of that ReceptionLink within its
	// event, read together with the Admission for staff (受付1 ...).
	ReceptionLinkNumber int
	// AdmittedTime is when the Ticket was let in; equal to the Ticket's
	// admitted time.
	AdmittedTime time.Time
}

// ErrReceptionLinkNotUsable is the cause of the PermissionDenied that
// [TicketRepository.Admit] returns when the reception link was revoked
// before the ticket could be admitted through it.
var ErrReceptionLinkNotUsable = errors.New("reception link is not usable")

// AdmissionRepository reads [Admission]s. Admissions are written only by
// [TicketRepository.Admit].
type AdmissionRepository interface {
	// GetByTicket returns the Admission of the Ticket together with the number
	// of its ReceptionLink.
	//
	// # Possible errors
	//
	//  - NotFound: the Ticket has no Admission.
	//  - Internal: database failure.
	GetByTicket(ctx context.Context, ticketID TicketID) (*Admission, error)
}
