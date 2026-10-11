package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/liverty-music/backend/internal/entity"
)

// TicketToProto converts a domain Ticket to the wire-format entityv1.Ticket.
func TicketToProto(ticket *entity.Ticket) *entityv1.Ticket {
	if ticket == nil {
		return nil
	}
	t := &entityv1.Ticket{
		Id:                             &entityv1.TicketId{Value: string(ticket.ID)},
		OrderId:                        &entityv1.OrderId{Value: string(ticket.OrderID)},
		UserId:                         &entityv1.UserId{Value: string(ticket.UserID)},
		EventId:                        &entityv1.EventId{Value: ticket.EventID},
		ResaleWithoutConsentProhibited: ticket.ResaleWithoutConsentProhibited,
		Status:                         ticketStatusToProto(ticket.Status),
		IssueTime:                      timestamppb.New(ticket.IssuedTime),
	}
	// The Kyc binding is present only when the sale required verification.
	if ticket.KycID != "" {
		t.IdentityVerificationId = &entityv1.IdentityVerificationId{Value: ticket.KycID}
	}
	// The admitted time is present only once the ticket was admitted.
	if ticket.AdmittedTime != nil {
		t.AdmitTime = timestamppb.New(*ticket.AdmittedTime)
	}
	return t
}

// TicketsToProto converts a slice of domain Tickets to wire format.
func TicketsToProto(tickets []*entity.Ticket) []*entityv1.Ticket {
	if tickets == nil {
		return nil
	}
	out := make([]*entityv1.Ticket, 0, len(tickets))
	for _, t := range tickets {
		out = append(out, TicketToProto(t))
	}
	return out
}

// ticketStatusToProto maps a domain TicketStatus to the proto enum.
func ticketStatusToProto(s entity.TicketStatus) entityv1.TicketStatus {
	switch s {
	case entity.TicketStatusIssued:
		return entityv1.TicketStatus_TICKET_STATUS_ISSUED
	case entity.TicketStatusVoided:
		return entityv1.TicketStatus_TICKET_STATUS_VOIDED
	default:
		return entityv1.TicketStatus_TICKET_STATUS_UNSPECIFIED
	}
}
