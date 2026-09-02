package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/entity"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// LotterySalesPhaseToProto converts a domain LotterySalesPhase entity to the
// wire-format entityv1.LotterySalesPhase message.
func LotterySalesPhaseToProto(phase *entity.LotterySalesPhase) *entityv1.LotterySalesPhase {
	if phase == nil {
		return nil
	}
	return &entityv1.LotterySalesPhase{
		Id:                       &entityv1.LotterySalesPhaseId{Value: string(phase.ID)},
		EventId:                  &entityv1.EventId{Value: phase.EventID},
		OpenTime:                 timestamppb.New(phase.OpenTime),
		CloseTime:                timestamppb.New(phase.CloseTime),
		TicketCapacity:           int32(phase.TicketCapacity),
		MaxTicketsPerApplication: int32(phase.MaxTicketsPerApplication),
		TicketPrice:              phase.TicketPrice,
	}
}

// TicketApplicationToProto converts a domain TicketApplication entity to the
// wire-format entityv1.TicketApplication message.
func TicketApplicationToProto(app *entity.TicketApplication) *entityv1.TicketApplication {
	if app == nil {
		return nil
	}
	return &entityv1.TicketApplication{
		Id:                   &entityv1.TicketApplicationId{Value: string(app.ID)},
		PhaseId:              &entityv1.LotterySalesPhaseId{Value: string(app.PhaseID)},
		ApplicantId:          &entityv1.UserId{Value: string(app.ApplicantID)},
		RequestedTicketCount: int32(app.RequestedTicketCount),
		Identity:             applicantIdentityToProto(app.Identity),
		Authorization:        paymentAuthorizationToProto(app.Authorization),
		State:                ticketApplicationStateToProto(app.State),
		DrawSequence:         app.DrawSequence,
	}
}

// applicantIdentityToProto converts a domain ApplicantIdentity value to its
// proto counterpart.
func applicantIdentityToProto(id entity.ApplicantIdentity) *entityv1.ApplicantIdentity {
	return &entityv1.ApplicantIdentity{
		FullName:    id.FullName,
		PhoneNumber: id.PhoneNumber,
	}
}

// ApplicantIdentityFromProto converts a proto ApplicantIdentity message to the
// domain value object. Returns the zero value when pb is nil.
func ApplicantIdentityFromProto(pb *entityv1.ApplicantIdentity) entity.ApplicantIdentity {
	if pb == nil {
		return entity.ApplicantIdentity{}
	}
	return entity.ApplicantIdentity{
		FullName:    pb.GetFullName(),
		PhoneNumber: pb.GetPhoneNumber(),
	}
}

// paymentAuthorizationToProto converts a domain PaymentAuthorization value to
// its proto counterpart.
func paymentAuthorizationToProto(auth entity.PaymentAuthorization) *entityv1.PaymentAuthorization {
	return &entityv1.PaymentAuthorization{
		PaymentIntentRef: auth.PaymentIntentRef,
	}
}

// PaymentAuthorizationFromProto converts a proto PaymentAuthorization message
// to the domain value object. Returns the zero value when pb is nil.
func PaymentAuthorizationFromProto(pb *entityv1.PaymentAuthorization) entity.PaymentAuthorization {
	if pb == nil {
		return entity.PaymentAuthorization{}
	}
	return entity.PaymentAuthorization{
		PaymentIntentRef: pb.GetPaymentIntentRef(),
	}
}

// ticketApplicationStateToProto maps a domain TicketApplicationState to the
// corresponding proto enum value.
//
// Note: the domain defines Withdrawn as 5, but the proto uses 4 for
// WITHDRAWN. The mapping is explicit here to handle this discrepancy without
// relying on numeric equality.
func ticketApplicationStateToProto(s entity.TicketApplicationState) entityv1.TicketApplicationState {
	switch s {
	case entity.TicketApplicationStateApplied:
		return entityv1.TicketApplicationState_TICKET_APPLICATION_STATE_APPLIED
	case entity.TicketApplicationStateWon:
		return entityv1.TicketApplicationState_TICKET_APPLICATION_STATE_WON
	case entity.TicketApplicationStateLost:
		return entityv1.TicketApplicationState_TICKET_APPLICATION_STATE_LOST
	case entity.TicketApplicationStateWithdrawn:
		return entityv1.TicketApplicationState_TICKET_APPLICATION_STATE_WITHDRAWN
	default:
		return entityv1.TicketApplicationState_TICKET_APPLICATION_STATE_UNSPECIFIED
	}
}
