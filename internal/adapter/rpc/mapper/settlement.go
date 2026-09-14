package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	adminv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/v1"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// OrganizerConnectedAccountToProto maps a domain OrganizerConnectedAccount to
// its Protobuf message. Raw Stripe status is never exposed; the platform's own
// PayoutOnboardingStatus enum is used.
func OrganizerConnectedAccountToProto(a *entity.OrganizerConnectedAccount) *entityv1.OrganizerConnectedAccount {
	if a == nil {
		return nil
	}
	return &entityv1.OrganizerConnectedAccount{
		OrganizerId: &entityv1.OrganizerId{Value: a.OrganizerID},
		AccountRef:  a.AccountRef,
		Status:      payoutOnboardingStatusToProto(a.Status),
	}
}

// payoutOnboardingStatusToProto maps entity.PayoutOnboardingStatus to the
// proto enum.
func payoutOnboardingStatusToProto(s entity.PayoutOnboardingStatus) entityv1.PayoutOnboardingStatus {
	switch s {
	case entity.PayoutOnboardingStatusPending:
		return entityv1.PayoutOnboardingStatus_PAYOUT_ONBOARDING_STATUS_PENDING
	case entity.PayoutOnboardingStatusActive:
		return entityv1.PayoutOnboardingStatus_PAYOUT_ONBOARDING_STATUS_ACTIVE
	case entity.PayoutOnboardingStatusRestricted:
		return entityv1.PayoutOnboardingStatus_PAYOUT_ONBOARDING_STATUS_RESTRICTED
	default:
		return entityv1.PayoutOnboardingStatus_PAYOUT_ONBOARDING_STATUS_UNSPECIFIED
	}
}

// SettlementToProto maps a domain Settlement to its Protobuf message.
func SettlementToProto(s *entity.Settlement) *entityv1.Settlement {
	if s == nil {
		return nil
	}
	proto := &entityv1.Settlement{
		Id:        &entityv1.SettlementId{Value: string(s.ID)},
		OrderId:   &entityv1.OrderId{Value: string(s.OrderID)},
		ChargeRef: s.ChargeRef,
		Splits:    settlementSplitsToProto(s.Splits),
		Status:    settlementStatusToProto(s.Status),
	}
	if !s.ReleasedTime.IsZero() {
		proto.ReleasedAt = timestamppb.New(s.ReleasedTime)
	}
	return proto
}

func settlementSplitsToProto(splits []entity.SettlementSplit) []*entityv1.SettlementSplit {
	if len(splits) == 0 {
		return nil
	}
	out := make([]*entityv1.SettlementSplit, 0, len(splits))
	for _, sp := range splits {
		out = append(out, &entityv1.SettlementSplit{
			PayeeOrganizerId:    &entityv1.OrganizerId{Value: sp.PayeeOrganizerID},
			Amount:              sp.Amount,
			TransferRef:         sp.TransferRef,
			TransferReversalRef: sp.TransferReversalRef,
		})
	}
	return out
}

func settlementStatusToProto(s entity.SettlementStatus) entityv1.SettlementStatus {
	switch s {
	case entity.SettlementStatusHeld:
		return entityv1.SettlementStatus_SETTLEMENT_STATUS_HELD
	case entity.SettlementStatusReleased:
		return entityv1.SettlementStatus_SETTLEMENT_STATUS_RELEASED
	case entity.SettlementStatusReversed:
		return entityv1.SettlementStatus_SETTLEMENT_STATUS_REVERSED
	default:
		return entityv1.SettlementStatus_SETTLEMENT_STATUS_UNSPECIFIED
	}
}

// ProtoRefundReasonToDomain converts the proto RefundReason enum to the domain
// RefundReason type. Kept in mapper/ so handlers remain free of conversion
// logic (AGENTS.md: handlers map Proto↔Entity via mapper/ and call a UseCase).
// UNSPECIFIED is passed through as-is; the use case validates it and returns
// InvalidArgument.
func ProtoRefundReasonToDomain(r adminv1.RefundReason) usecase.RefundReason {
	switch r {
	case adminv1.RefundReason_REFUND_REASON_CANCELLATION:
		return usecase.RefundReasonCancellation
	case adminv1.RefundReason_REFUND_REASON_POSTPONEMENT_WINDOW:
		return usecase.RefundReasonPostponementWindow
	case adminv1.RefundReason_REFUND_REASON_DISPUTE:
		return usecase.RefundReasonDispute
	default:
		return usecase.RefundReasonUnspecified
	}
}
