package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/entity"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TicketToProto converts a domain Ticket entity to a protobuf Ticket message.
func TicketToProto(t *entity.Ticket) *entityv1.Ticket {
	if t == nil {
		return nil
	}

	return &entityv1.Ticket{
		Id:       &entityv1.TicketId{Value: t.ID},
		EventId:  &entityv1.EventId{Value: t.EventID},
		UserId:   &entityv1.UserId{Value: t.UserID},
		TokenId:  &entityv1.TokenId{Value: t.TokenID},
		TxHash:   t.TxHash,
		MintTime: timestamppb.New(t.MintTime),
	}
}

// TicketsToProto converts a slice of domain Ticket entities to protobuf messages.
func TicketsToProto(tickets []*entity.Ticket) []*entityv1.Ticket {
	result := make([]*entityv1.Ticket, 0, len(tickets))
	for _, t := range tickets {
		result = append(result, TicketToProto(t))
	}

	return result
}
