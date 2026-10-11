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
		Id:     &entityv1.OrderId{Value: string(order.ID)},
		UserId: &entityv1.UserId{Value: string(order.UserID)},
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
		PayTime:  timestamppb.New(order.PaidTime),
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
	case entity.OrderStatusFailed:
		return entityv1.OrderStatus_ORDER_STATUS_FAILED
	default:
		return entityv1.OrderStatus_ORDER_STATUS_UNSPECIFIED
	}
}
