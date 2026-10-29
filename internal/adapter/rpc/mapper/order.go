package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/liverty-music/backend/internal/entity"
)

// OrderToProto maps a domain Order to Protobuf.
func OrderToProto(order *entity.Order) *entityv1.Order {
	if order == nil {
		return nil
	}
	pb := &entityv1.Order{
		Id:            &entityv1.OrderId{Value: string(order.ID)},
		UserId:        &entityv1.UserId{Value: string(order.UserID)},
		TicketStockId: &entityv1.TicketStockId{Value: string(order.TicketStockID)},
		TicketCount:   int32(order.TicketCount),
		Payment: &entityv1.Payment{
			Provider:  paymentProviderToProto(order.Payment.Provider),
			CardBrand: order.Payment.CardBrand,
			CardLast4: order.Payment.CardLast4,
		},
		Status:   orderStatusToProto(order.Status),
		Amount:   order.Amount,
		Currency: order.Currency,
	}
	if order.ReferralCode != nil {
		pb.ReferralCode = &entityv1.ReferralCode{Value: string(*order.ReferralCode)}
	}
	if order.CardAuthorizationID != "" {
		pb.CardAuthorizationId = &entityv1.CardAuthorizationId{Value: order.CardAuthorizationID}
	}
	if order.Payment.ProviderPaymentID != "" {
		pb.Payment.ProviderPaymentId = &entityv1.Payment_ProviderPaymentId{Value: order.Payment.ProviderPaymentID}
	}
	if order.PayTime != nil {
		pb.PayTime = timestamppb.New(*order.PayTime)
	}
	if order.Payment.ProviderPaymentMethodID != "" {
		pb.Payment.ProviderPaymentMethodId = &entityv1.Payment_ProviderPaymentMethodId{Value: order.Payment.ProviderPaymentMethodID}
	}
	if order.ProviderRefundID != "" {
		pb.ProviderRefundId = &entityv1.Order_ProviderRefundId{Value: order.ProviderRefundID}
	}
	if order.ConfirmationSendTime != nil {
		pb.ConfirmationSendTime = timestamppb.New(*order.ConfirmationSendTime)
	}
	// The source is exactly one of the lottery entry and the checkout.
	if order.CheckoutID != "" {
		pb.Source = &entityv1.Order_CheckoutId{CheckoutId: &entityv1.CheckoutId{Value: string(order.CheckoutID)}}
	} else {
		pb.Source = &entityv1.Order_LotteryEntryId{LotteryEntryId: &entityv1.LotteryEntryId{Value: string(order.LotteryEntryID)}}
	}
	return pb
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
	case entity.OrderStatusPending:
		return entityv1.OrderStatus_ORDER_STATUS_PENDING
	case entity.OrderStatusVoid:
		return entityv1.OrderStatus_ORDER_STATUS_VOID
	default:
		return entityv1.OrderStatus_ORDER_STATUS_UNSPECIFIED
	}
}
