package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/entity"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TicketEmailTypeFromProto maps a Protobuf TicketEmailType to its domain value.
var TicketEmailTypeFromProto = map[entityv1.TicketEmailType]entity.TicketEmailType{
	entityv1.TicketEmailType_TICKET_EMAIL_TYPE_LOTTERY_INFO:   entity.TicketEmailTypeLotteryInfo,
	entityv1.TicketEmailType_TICKET_EMAIL_TYPE_LOTTERY_RESULT: entity.TicketEmailTypeLotteryResult,
}

// lotteryResultToProto maps a domain LotteryResult to its Protobuf enum value.
var lotteryResultToProto = map[entity.LotteryResult]entityv1.LotteryResult{
	entity.LotteryResultWon:  entityv1.LotteryResult_LOTTERY_RESULT_WON,
	entity.LotteryResultLost: entityv1.LotteryResult_LOTTERY_RESULT_LOST,
}

// paymentStatusToProto maps a domain PaymentStatus to its Protobuf enum value.
var paymentStatusToProto = map[entity.PaymentStatus]entityv1.PaymentStatus{
	entity.PaymentStatusUnpaid: entityv1.PaymentStatus_PAYMENT_STATUS_UNPAID,
	entity.PaymentStatusPaid:   entityv1.PaymentStatus_PAYMENT_STATUS_PAID,
}

// LotteryResultFromProto maps a Protobuf LotteryResult to its domain value.
var LotteryResultFromProto = map[entityv1.LotteryResult]entity.LotteryResult{
	entityv1.LotteryResult_LOTTERY_RESULT_WON:  entity.LotteryResultWon,
	entityv1.LotteryResult_LOTTERY_RESULT_LOST: entity.LotteryResultLost,
}

// PaymentStatusFromProto maps a Protobuf PaymentStatus to its domain value.
var PaymentStatusFromProto = map[entityv1.PaymentStatus]entity.PaymentStatus{
	entityv1.PaymentStatus_PAYMENT_STATUS_UNPAID: entity.PaymentStatusUnpaid,
	entityv1.PaymentStatus_PAYMENT_STATUS_PAID:   entity.PaymentStatusPaid,
}

// TicketEmailToProto maps a domain TicketEmail to its Protobuf wire representation.
func TicketEmailToProto(te *entity.TicketEmail) *entityv1.TicketEmail {
	if te == nil {
		return nil
	}

	pb := &entityv1.TicketEmail{
		Id:      &entityv1.TicketEmailId{Value: te.ID},
		UserId:  &entityv1.UserId{Value: te.UserID},
		EventId: &entityv1.EventId{Value: te.EventID},
		RawBody: te.RawBody,
	}

	// email_type
	switch te.EmailType {
	case entity.TicketEmailTypeLotteryInfo:
		pb.EmailType = entityv1.TicketEmailType_TICKET_EMAIL_TYPE_LOTTERY_INFO
	case entity.TicketEmailTypeLotteryResult:
		pb.EmailType = entityv1.TicketEmailType_TICKET_EMAIL_TYPE_LOTTERY_RESULT
	}

	// parsed_data
	if te.ParsedData != nil {
		s := &structpb.Struct{}
		if err := s.UnmarshalJSON(te.ParsedData); err == nil {
			pb.ParsedData = s
		}
	}

	// optional timestamps
	if te.PaymentDeadlineTime != nil {
		pb.PaymentDeadline = timestamppb.New(*te.PaymentDeadlineTime)
	}
	if te.LotteryStartTime != nil {
		pb.LotteryStart = timestamppb.New(*te.LotteryStartTime)
	}
	if te.LotteryEndTime != nil {
		pb.LotteryEnd = timestamppb.New(*te.LotteryEndTime)
	}
	if te.ApplicationURL != "" {
		pb.ApplicationUrl = &te.ApplicationURL
	}
	if te.LotteryResult != nil {
		v := lotteryResultToProto[*te.LotteryResult]
		pb.LotteryResult = &v
	}
	if te.PaymentStatus != nil {
		v := paymentStatusToProto[*te.PaymentStatus]
		pb.PaymentStatus = &v
	}

	return pb
}

// TicketEmailsToProto maps a collection of domain TicketEmail entities to Protobuf messages.
func TicketEmailsToProto(emails []*entity.TicketEmail) []*entityv1.TicketEmail {
	result := make([]*entityv1.TicketEmail, 0, len(emails))
	for _, te := range emails {
		result = append(result, TicketEmailToProto(te))
	}
	return result
}
