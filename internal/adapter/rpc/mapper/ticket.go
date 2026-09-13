package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/entity"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// OrderToProto converts a domain Order to the wire-format entityv1.Order.
func OrderToProto(order *entity.Order) *entityv1.Order {
	if order == nil {
		return nil
	}
	return &entityv1.Order{
		Id:            &entityv1.OrderId{Value: string(order.ID)},
		BuyerId:       &entityv1.UserId{Value: string(order.BuyerID)},
		ApplicationId: &entityv1.TicketApplicationId{Value: string(order.ApplicationID)},
		Payment: &entityv1.Payment{
			Provider:         paymentProviderToProto(order.Payment.Provider),
			PaymentIntentRef: order.Payment.PaymentIntentRef,
			PaymentMethodRef: order.Payment.PaymentMethodRef,
			CardBrand:        order.Payment.CardBrand,
			CardLast4:        order.Payment.CardLast4,
		},
		Status:   orderStatusToProto(order.Status),
		Amount:   order.Amount,
		Currency: order.Currency,
		PaidAt:   timestamppb.New(order.PaidTime),
	}
}

// TicketToProto converts a domain Ticket to the wire-format entityv1.Ticket.
func TicketToProto(ticket *entity.Ticket) *entityv1.Ticket {
	if ticket == nil {
		return nil
	}
	t := &entityv1.Ticket{
		Id:                             &entityv1.TicketId{Value: string(ticket.ID)},
		OrderId:                        &entityv1.OrderId{Value: string(ticket.OrderID)},
		HolderId:                       &entityv1.UserId{Value: string(ticket.HolderID)},
		EventId:                        &entityv1.EventId{Value: ticket.EventID},
		HolderIdentity:                 applicantIdentityToProto(ticket.HolderIdentity),
		ResaleWithoutConsentProhibited: ticket.ResaleWithoutConsentProhibited,
		Status:                         ticketStatusToProto(ticket.Status),
		IssuedAt:                       timestamppb.New(ticket.IssuedTime),
	}
	// The verified-identity binding is present only when the phase required
	// identity verification.
	if ticket.VerifiedIdentityID != "" {
		t.VerifiedIdentityRef = &entityv1.VerifiedIdentityId{Value: ticket.VerifiedIdentityID}
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

// paymentProviderToProto maps a domain PaymentProvider to the proto enum.
func paymentProviderToProto(p entity.PaymentProvider) entityv1.PaymentProvider {
	switch p {
	case entity.PaymentProviderStripe:
		return entityv1.PaymentProvider_PAYMENT_PROVIDER_STRIPE
	case entity.PaymentProviderKOMOJU:
		return entityv1.PaymentProvider_PAYMENT_PROVIDER_KOMOJU
	default:
		return entityv1.PaymentProvider_PAYMENT_PROVIDER_UNSPECIFIED
	}
}

// orderStatusToProto maps a domain OrderStatus to the proto enum.
func orderStatusToProto(s entity.OrderStatus) entityv1.OrderStatus {
	switch s {
	case entity.OrderStatusPaid:
		return entityv1.OrderStatus_ORDER_STATUS_PAID
	case entity.OrderStatusRefunded:
		return entityv1.OrderStatus_ORDER_STATUS_REFUNDED
	case entity.OrderStatusFailed:
		return entityv1.OrderStatus_ORDER_STATUS_FAILED
	default:
		return entityv1.OrderStatus_ORDER_STATUS_UNSPECIFIED
	}
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
