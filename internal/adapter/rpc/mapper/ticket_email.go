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

// journeyStatusToProto maps a domain TicketJourneyStatus to its Protobuf enum value.
var journeyStatusToProto = map[entity.TicketJourneyStatus]entityv1.TicketJourneyStatus{
	entity.TicketJourneyStatusTracking: entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING,
	entity.TicketJourneyStatusApplied:  entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_APPLIED,
	entity.TicketJourneyStatusLost:     entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_LOST,
	entity.TicketJourneyStatusUnpaid:   entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_UNPAID,
	entity.TicketJourneyStatusPaid:     entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_PAID,
}

// JourneyStatusFromProto maps a Protobuf TicketJourneyStatus to its domain value.
var JourneyStatusFromProto = map[entityv1.TicketJourneyStatus]entity.TicketJourneyStatus{
	entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING: entity.TicketJourneyStatusTracking,
	entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_APPLIED:  entity.TicketJourneyStatusApplied,
	entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_LOST:     entity.TicketJourneyStatusLost,
	entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_UNPAID:   entity.TicketJourneyStatusUnpaid,
	entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_PAID:     entity.TicketJourneyStatusPaid,
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
	if te.JourneyStatus != nil {
		v := journeyStatusToProto[*te.JourneyStatus]
		pb.JourneyStatus = &v
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
