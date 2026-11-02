package pb

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/entity"
)

// ticketJourneyStatusToProto maps a domain TicketJourneyStatus to its Protobuf enum value.
var ticketJourneyStatusToProto = map[entity.TicketJourneyStatus]entityv1.TicketJourneyStatus{
	entity.TicketJourneyStatusTracking: entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING,
	entity.TicketJourneyStatusApplied:  entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_APPLIED,
	entity.TicketJourneyStatusLost:     entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_LOST,
	entity.TicketJourneyStatusUnpaid:   entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_UNPAID,
	entity.TicketJourneyStatusPaid:     entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_PAID,
}

// TicketJourneyStatusFromProto maps a Protobuf TicketJourneyStatus enum to its domain value.
var TicketJourneyStatusFromProto = map[entityv1.TicketJourneyStatus]entity.TicketJourneyStatus{
	entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING: entity.TicketJourneyStatusTracking,
	entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_APPLIED:  entity.TicketJourneyStatusApplied,
	entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_LOST:     entity.TicketJourneyStatusLost,
	entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_UNPAID:   entity.TicketJourneyStatusUnpaid,
	entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_PAID:     entity.TicketJourneyStatusPaid,
}

// ticketJourneySetByToProto maps a domain TicketJourneySetBy value to its
// Protobuf enum value.
var ticketJourneySetByToProto = map[entity.TicketJourneySetBy]entityv1.TicketJourneyStatusSetBy{
	entity.TicketJourneySetByFan:       entityv1.TicketJourneyStatusSetBy_TICKET_JOURNEY_STATUS_SET_BY_FAN,
	entity.TicketJourneySetByPlatform:  entityv1.TicketJourneyStatusSetBy_TICKET_JOURNEY_STATUS_SET_BY_PLATFORM,
	entity.TicketJourneySetByInference: entityv1.TicketJourneyStatusSetBy_TICKET_JOURNEY_STATUS_SET_BY_INFERENCE,
}

// TicketJourneySetByFromProto maps a Protobuf TicketJourneyStatusSetBy enum to
// its domain value.
var TicketJourneySetByFromProto = map[entityv1.TicketJourneyStatusSetBy]entity.TicketJourneySetBy{
	entityv1.TicketJourneyStatusSetBy_TICKET_JOURNEY_STATUS_SET_BY_FAN:       entity.TicketJourneySetByFan,
	entityv1.TicketJourneyStatusSetBy_TICKET_JOURNEY_STATUS_SET_BY_PLATFORM:  entity.TicketJourneySetByPlatform,
	entityv1.TicketJourneyStatusSetBy_TICKET_JOURNEY_STATUS_SET_BY_INFERENCE: entity.TicketJourneySetByInference,
}

// FromTicketJourney maps a domain TicketJourney to its Protobuf wire representation.
func FromTicketJourney(tj *entity.TicketJourney) *entityv1.TicketJourney {
	if tj == nil {
		return nil
	}
	return &entityv1.TicketJourney{
		UserId:      &entityv1.UserId{Value: tj.UserID},
		EventId:     &entityv1.EventId{Value: tj.EventID},
		Status:      ticketJourneyStatusToProto[tj.Status],
		StatusSetBy: ticketJourneySetByToProto[tj.StatusSetBy],
	}
}

// FromTicketJourneys maps a collection of domain TicketJourney entities to Protobuf messages.
func FromTicketJourneys(journeys []*entity.TicketJourney) []*entityv1.TicketJourney {
	result := make([]*entityv1.TicketJourney, 0, len(journeys))
	for _, tj := range journeys {
		result = append(result, FromTicketJourney(tj))
	}
	return result
}
