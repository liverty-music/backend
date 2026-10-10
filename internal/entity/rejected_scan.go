package entity

import (
	"context"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// RejectedScanReason is why a scan, or one Ticket presented in it, was
// refused at reception. Values mirror the proto enum
// liverty_music.entity.v1.RejectedScanReason, except that value 4 (the
// proto's NOT_HOLDER) is unused and invalid: a code presenting a Ticket the
// user does not hold for the event is rejected as Forged.
type RejectedScanReason int16

const (
	// RejectedScanReasonUnspecified is the zero value and is never persisted.
	RejectedScanReasonUnspecified RejectedScanReason = 0
	// RejectedScanReasonForged means the text is not an AdmissionCode, the
	// user has no wallet key, the signature does not verify, or the code
	// presents a Ticket the user does not hold for the event. Applies to the
	// whole scan; names no Ticket.
	RejectedScanReasonForged RejectedScanReason = 1
	// RejectedScanReasonExpired means the code is genuine but not fresh.
	// Applies to the whole scan.
	RejectedScanReasonExpired RejectedScanReason = 2
	// RejectedScanReasonOtherEvent means a genuine, fresh code for another
	// event. Applies to the whole scan.
	RejectedScanReasonOtherEvent RejectedScanReason = 3
	// RejectedScanReasonVoided means the presented Ticket was voided.
	RejectedScanReasonVoided RejectedScanReason = 5
	// RejectedScanReasonAlreadyAdmitted means the presented Ticket was let in
	// earlier.
	RejectedScanReasonAlreadyAdmitted RejectedScanReason = 6
)

// IsValid reports whether r is a recognized, persistable reason.
func (r RejectedScanReason) IsValid() bool {
	switch r {
	case RejectedScanReasonForged, RejectedScanReasonExpired, RejectedScanReasonOtherEvent,
		RejectedScanReasonVoided, RejectedScanReasonAlreadyAdmitted:
		return true
	default:
		return false
	}
}

// RejectedScan is the permanent note that a scan at the venue, or one Ticket
// in it, was refused: why, through which ReceptionLink and when. RejectedScans
// are append-only; unlike an Admission, losing one is acceptable.
//
// Mirrors liverty_music.entity.v1.RejectedScan.
type RejectedScan struct {
	// ID is the surrogate key (UUIDv7).
	ID string
	// EventID is the event of the ReceptionLink that scanned.
	EventID string
	// ReceptionLinkID is the ReceptionLink that scanned.
	ReceptionLinkID ReceptionLinkID
	// TicketID is the Ticket the refusal is about; empty exactly when the
	// reason is Forged.
	TicketID TicketID
	// Reason is why it was refused.
	Reason RejectedScanReason
	// ScannedTime is when the scan was decided.
	ScannedTime time.Time
}

// NewRejectedScan returns a RejectedScan with a fresh id.
func NewRejectedScan(eventID string, linkID ReceptionLinkID, ticketID TicketID, reason RejectedScanReason, scannedTime time.Time) *RejectedScan {
	return &RejectedScan{
		ID:              NewID(),
		EventID:         eventID,
		ReceptionLinkID: linkID,
		TicketID:        ticketID,
		Reason:          reason,
		ScannedTime:     scannedTime,
	}
}

// Validate reports InvalidArgument unless the RejectedScan names an event, a
// link, a known reason and a scanned time, and names a Ticket exactly when
// the reason is not Forged: a forged code proves nothing about the Tickets it
// lists, while any other reason follows a verified signature.
func (s *RejectedScan) Validate() error {
	if s.EventID == "" || s.ReceptionLinkID == "" || s.ScannedTime.IsZero() {
		return apperr.New(codes.InvalidArgument, "rejected scan needs an event, a reception link and a scanned time")
	}
	if !s.Reason.IsValid() {
		return apperr.New(codes.InvalidArgument, "rejected scan reason is not valid")
	}
	if (s.Reason == RejectedScanReasonForged) != (s.TicketID == "") {
		return apperr.New(codes.InvalidArgument, "rejected scan names a ticket exactly when the reason is not Forged")
	}
	return nil
}

// RejectedScanRepository appends [RejectedScan]s; nothing changes or removes
// a stored one.
type RejectedScanRepository interface {
	// Append stores all of scans or none of them.
	//
	// # Possible errors
	//
	//  - InvalidArgument: any scan is invalid (none is stored).
	//  - Internal: database failure.
	Append(ctx context.Context, scans []*RejectedScan) error
}
