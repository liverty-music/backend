package entity

import (
	"context"
	"errors"
	"time"
)

// TicketID is the opaque identifier for a single issued Ticket.
//
// Mirrors liverty_music.entity.v1.TicketId.value (a UUID string); mapped to the
// proto wrapper at the handler boundary.
type TicketID string

// TicketStatus is the lifecycle of an issued Ticket as owned by ⑤. ⑤ issues a
// ticket as Issued and voids it when its Order is refunded. Admission at the
// venue (⑥ ticket-wallet-and-checkin) is not a status: it is the ticket's
// AdmitTime, kept when the ticket is later voided.
// Values mirror the proto enum liverty_music.entity.v1.TicketStatus.
type TicketStatus int16

const (
	// TicketStatusUnspecified is the zero value and is never persisted.
	TicketStatusUnspecified TicketStatus = 0
	// TicketStatusIssued is the normal state on issuance from a captured win.
	TicketStatusIssued TicketStatus = 1
	// TicketStatusVoided means the ticket's Order was refunded (cancellation /
	// dispute), so it is no longer valid for entry.
	TicketStatusVoided TicketStatus = 2
)

// String returns the lowercase status name.
func (s TicketStatus) String() string {
	switch s {
	case TicketStatusIssued:
		return "issued"
	case TicketStatusVoided:
		return "voided"
	default:
		return "UNSPECIFIED"
	}
}

// IsValid reports whether s is a recognized, persistable status.
func (s TicketStatus) IsValid() bool {
	return s == TicketStatusIssued || s == TicketStatusVoided
}

// Ticket is a Web2, account-bound admission right issued from a won lottery
// entry or a completed checkout. ⑥ ticket-wallet-and-checkin adds admission at
// the venue on top of it (AdmitTime, [TicketRepository.Admit]).
//
// Every issued Ticket is a covered ticket (特定興行入場券) carrying all three
// conditions:
//   - (i)   its face states resale without the organizer's consent is
//     prohibited (ResaleWithoutConsentProhibited, always true);
//   - (ii)  it names the event (date and venue, via EventID) and the eligible
//     person, who is the Ticket's user, with no seat assigned;
//   - (iii) it shows the user's full name and phone number as the User
//     currently records them (本人確認), read when the face is shown
//     ([Ticket.Face]); nothing is copied onto the Ticket.
//
// Mirrors liverty_music.entity.v1.Ticket.
type Ticket struct {
	// ID is the surrogate primary key (UUIDv7).
	ID TicketID
	// OrderID is the Order that issued this ticket. One Order issues the N
	// tickets of the companion group.
	OrderID OrderID
	// UserID is the account the ticket is bound to: the Order's user. It never
	// changes; resale voids the ticket and issues a new one.
	UserID UserID
	// EventID is the event this ticket admits to. Supplies the covered-ticket
	// face's date/venue (condition ii).
	EventID string
	// IdentityVerificationID is the IdentityVerification this ticket is bound to, set only when the sale
	// required verification; empty otherwise.
	IdentityVerificationID string
	// ResaleWithoutConsentProhibited is covered-ticket condition (i): the face
	// states resale without the organizer's consent is prohibited. Always true —
	// every issued ticket asserts this so it qualifies as a 特定興行入場券.
	ResaleWithoutConsentProhibited bool
	// Status is the ticket's lifecycle status (Issued on issuance; Voided on
	// refund).
	Status TicketStatus
	// IssueTime is when the ticket was issued (= the Order's capture/issuance
	// time).
	IssueTime time.Time
	// AdmitTime is when the ticket was admitted at the venue; nil until
	// admitted. Set once, together with its [Admission], and kept when the
	// ticket is later Voided.
	AdmitTime *time.Time
}

// Validate reports whether the ticket is a covered ticket: its face states
// that resale without consent is prohibited.
func (t *Ticket) Validate() error {
	if !t.ResaleWithoutConsentProhibited {
		return errors.New("a ticket must state that resale without consent is prohibited")
	}
	return nil
}

// TicketFace is what a Ticket's face shows: the resale notice, the event, and
// the eligible person — the Ticket's user — with the full name and phone
// number the User records when the face is read. No seat is assigned.
type TicketFace struct {
	// ResaleWithoutConsentProhibited is the resale notice; always true for a
	// valid Ticket.
	ResaleWithoutConsentProhibited bool
	// EventID is the event the ticket admits to.
	EventID string
	// UserID is the eligible person's account.
	UserID UserID
	// FullName is the user's full name; empty when not given yet.
	FullName string
	// PhoneNumber is the user's phone number in E.164 form; empty when not
	// given yet.
	PhoneNumber string
}

// Face returns the ticket's face for user, the Ticket's user, with the full
// name and phone number user currently records.
func (t *Ticket) Face(user *User) TicketFace {
	return TicketFace{
		ResaleWithoutConsentProhibited: t.ResaleWithoutConsentProhibited,
		EventID:                        t.EventID,
		UserID:                         t.UserID,
		FullName:                       user.FullName,
		PhoneNumber:                    user.PhoneNumber,
	}
}

// IsAdmissible reports whether the ticket can be admitted: it is Issued and
// has no admitted time. A Voided ticket is never admissible, and an admitted
// ticket is never admissible again.
func (t *Ticket) IsAdmissible() bool {
	return t.Status == TicketStatusIssued && t.AdmitTime == nil
}

// AdmitOutcome is the outcome of [TicketRepository.Admit].
type AdmitOutcome int

const (
	// AdmitOutcomeAdmitted means the ticket was admitted by this call.
	AdmitOutcomeAdmitted AdmitOutcome = iota + 1
	// AdmitOutcomeAlreadyAdmitted means the ticket already had an admitted
	// time; nothing changed.
	AdmitOutcomeAlreadyAdmitted
	// AdmitOutcomeVoided means the ticket is Voided and was never admitted;
	// nothing changed.
	AdmitOutcomeVoided
)

// AdmitResult is what [TicketRepository.Admit] reports.
type AdmitResult struct {
	// Outcome is Admitted, AlreadyAdmitted or Voided.
	Outcome AdmitOutcome
	// AdmitTime is the ticket's admitted time: the given time when
	// Admitted, the earlier one when AlreadyAdmitted, zero when Voided.
	AdmitTime time.Time
}

// TicketRepository defines the persistence contract for [Ticket] records.
// Implementations live in internal/infrastructure/database/rdb/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type TicketRepository interface {

	// ListByUser returns every ticket whose user is the given User, Issued and
	// Voided alike, most recently issued first; empty when none.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	ListByUser(ctx context.Context, userID UserID) ([]*Ticket, error)

	// ListByUserAndEvent returns every ticket of the User for the event,
	// Issued and Voided alike, in the order they were issued; empty when none.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	ListByUserAndEvent(ctx context.Context, userID UserID, eventID string) ([]*Ticket, error)

	// Admit admits the ticket through the Scanner at admittedTime, at
	// most once. When the ticket is admissible it sets the admitted time and
	// stores the [Admission] in one indivisible step (a per-ticket conditional
	// update, never a table lock) and reports Admitted; of any number of
	// concurrent calls for one ticket exactly one reports Admitted. A ticket
	// with an admitted time reports AlreadyAdmitted with that time; a Voided
	// ticket without one reports Voided. The Scanner must still be usable
	// when the ticket is admitted, so a Scanner revoked mid-scan admits nothing
	// more.
	//
	// # Possible errors
	//
	//  - NotFound: no ticket has the id.
	//  - PermissionDenied (cause [ErrScannerNotUsable]): the Scanner is
	//    Revoked or does not exist; nothing changed.
	//  - Internal: database failure.
	//
	// # Announces
	//
	// ORDER.ticket_admitted with the Ticket and its admit time, together with the
	// admission, when the outcome is Admitted.
	Admit(ctx context.Context, ticketID TicketID, scannerID ScannerID, admittedTime time.Time) (AdmitResult, error)
}
