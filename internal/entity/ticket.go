package entity

import (
	"context"
	"time"
)

// TicketID is the opaque identifier for a single issued Ticket.
//
// Mirrors liverty_music.entity.v1.TicketId.value (a UUID string); mapped to the
// proto wrapper at the handler boundary.
type TicketID string

// TicketStatus is the lifecycle of an issued Ticket as owned by ⑤. ⑤ issues a
// ticket as Issued and voids it when its Order is refunded. ⑥
// ticket-wallet-and-checkin appends wallet / check-in states on top of this.
// Values mirror the proto enum liverty_music.entity.v1.TicketStatus.
type TicketStatus int16

const (
	// TicketStatusUnspecified is the zero value and is never persisted.
	TicketStatusUnspecified TicketStatus = 0
	// TicketStatusIssued is the normal state on issuance from a captured win.
	TicketStatusIssued TicketStatus = 1
	// TicketStatusVoided means the ticket's Order was refunded (cancellation /
	// postponement refund window / dispute), so it is no longer valid for entry.
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

// Ticket is a Web2, account-bound admission right issued from a captured
// lottery win. ⑤ DEFINES this entity; ⑥ ticket-wallet-and-checkin later adds
// wallet / rotating-QR / check-in behavior on top of it.
//
// Every issued Ticket is a covered ticket (特定興行入場券) carrying ALL THREE legal
// conditions:
//   - (i)   the face states resale without the organizer's consent is prohibited
//     (ResaleWithoutConsentProhibited, always true);
//   - (ii)  the face specifies date/venue (via EventID) + the eligible person
//     (the bound HolderIdentity; the lottery is a common pool with no seat map,
//     so admission is general and the eligible person is the holder);
//   - (iii) the holder's 本人確認 is captured and bound to the account
//     (HolderIdentity, with VerifiedIdentityID set where the phase required
//     identity verification — then the verified identity is authoritative).
//
// Mirrors liverty_music.entity.v1.Ticket.
type Ticket struct {
	// ID is the surrogate primary key (UUIDv7).
	ID TicketID
	// OrderID is the Order that issued this ticket. One Order issues the N
	// tickets of the winning companion group.
	OrderID OrderID
	// HolderID is the account the ticket is bound to — the current holder. At
	// issuance this is the winning buyer; ⑦ official resale reassigns it.
	HolderID UserID
	// EventID is the event this ticket admits to. Supplies the covered-ticket
	// face's date/venue (condition ii).
	EventID string
	// HolderIdentity is the holder's 本人確認 (name + contact) noted on the face
	// and bound to the account — condition (iii) and the eligible person of
	// condition (ii). Where the phase required identity verification this matches
	// the verified 本人確認 (see VerifiedIdentityID).
	HolderIdentity ApplicantIdentity
	// VerifiedIdentityID is the authoritative verified identity this ticket is
	// bound to, set only when the phase required identity verification. Empty
	// when the phase required no verification (then HolderIdentity is ④'s
	// self-declared name + contact).
	VerifiedIdentityID string
	// ResaleWithoutConsentProhibited is covered-ticket condition (i): the face
	// states resale without the organizer's consent is prohibited. Always true —
	// every issued ticket asserts this so it qualifies as a 特定興行入場券.
	ResaleWithoutConsentProhibited bool
	// Status is the ticket's lifecycle status (Issued on issuance; Voided on
	// refund).
	Status TicketStatus
	// IssuedTime is when the ticket was issued (= the Order's capture/issuance
	// time).
	IssuedTime time.Time
}

// TicketRepository defines the persistence contract for [Ticket] records.
// Implementations live in internal/infrastructure/database/rdb/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type TicketRepository interface {
	// ListByOrder returns the tickets issued under the given Order.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	ListByOrder(ctx context.Context, orderID OrderID) ([]*Ticket, error)

	// ListByHolder returns all tickets currently bound to the given account — the
	// buyer's "my tickets" view.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	ListByHolder(ctx context.Context, holderID UserID) ([]*Ticket, error)

	// VoidByOrder marks every ticket of the given Order as Voided (on a refund).
	// Idempotent: voiding already-voided tickets is a no-op.
	//
	// # Possible errors
	//
	//  - Internal: database execution failure.
	VoidByOrder(ctx context.Context, orderID OrderID) error
}
