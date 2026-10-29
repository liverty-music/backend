package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizerticketsalev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/ticket_sale/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// TicketSaleToProto maps a TicketSale with the events it names to Protobuf.
func TicketSaleToProto(s *entity.TicketSale) *entityv1.TicketSale {
	if s == nil {
		return nil
	}
	pb := &entityv1.TicketSale{
		Id:                      &entityv1.TicketSaleId{Value: string(s.ID)},
		SeriesId:                &entityv1.SeriesId{Value: s.SeriesID},
		Name:                    &entityv1.TicketSaleName{Value: s.Name},
		Method:                  ticketSaleMethodToProto(s.Method),
		SaleStartTime:           timestamppb.New(s.SaleStartTime),
		SaleEndTime:             timestamppb.New(s.SaleEndTime),
		VerificationRequirement: verificationRequirementToProto(s.VerificationRequirement),
		EventIds:                make([]*entityv1.EventId, 0, len(s.EventIDs)),
	}
	for _, id := range s.EventIDs {
		pb.EventIds = append(pb.EventIds, &entityv1.EventId{Value: id})
	}
	return pb
}

// TicketSalesToProto maps the sales of a list to Protobuf; see
// [TicketSaleToProto].
func TicketSalesToProto(sales []*entity.TicketSale) []*entityv1.TicketSale {
	out := make([]*entityv1.TicketSale, 0, len(sales))
	for _, s := range sales {
		if pb := TicketSaleToProto(s); pb != nil {
			out = append(out, pb)
		}
	}
	return out
}

// TicketSaleMethodFromProto maps a Protobuf sale method to the domain type;
// UNSPECIFIED and unknown values map to Unspecified.
func TicketSaleMethodFromProto(m entityv1.TicketSaleMethod) entity.TicketSaleMethod {
	switch m {
	case entityv1.TicketSaleMethod_TICKET_SALE_METHOD_FIRST_COME:
		return entity.TicketSaleMethodFirstCome
	case entityv1.TicketSaleMethod_TICKET_SALE_METHOD_LOTTERY:
		return entity.TicketSaleMethodLottery
	default:
		return entity.TicketSaleMethodUnspecified
	}
}

func ticketSaleMethodToProto(m entity.TicketSaleMethod) entityv1.TicketSaleMethod {
	switch m {
	case entity.TicketSaleMethodFirstCome:
		return entityv1.TicketSaleMethod_TICKET_SALE_METHOD_FIRST_COME
	case entity.TicketSaleMethodLottery:
		return entityv1.TicketSaleMethod_TICKET_SALE_METHOD_LOTTERY
	default:
		return entityv1.TicketSaleMethod_TICKET_SALE_METHOD_UNSPECIFIED
	}
}

// verificationRequirementToProto maps a domain VerificationRequirement to
// Protobuf; None is UNSPECIFIED.
func verificationRequirementToProto(r entity.VerificationRequirement) entityv1.VerificationRequirement {
	switch r {
	case entity.VerificationRequirementVerifiedAny:
		return entityv1.VerificationRequirement_VERIFICATION_REQUIREMENT_VERIFIED_ANY
	case entity.VerificationRequirementJPKIOnly:
		return entityv1.VerificationRequirement_VERIFICATION_REQUIREMENT_JPKI_ONLY
	default:
		return entityv1.VerificationRequirement_VERIFICATION_REQUIREMENT_UNSPECIFIED
	}
}

// VerificationRequirementFromProto maps a Protobuf VerificationRequirement to
// the domain type; UNSPECIFIED is None.
func VerificationRequirementFromProto(r entityv1.VerificationRequirement) entity.VerificationRequirement {
	switch r {
	case entityv1.VerificationRequirement_VERIFICATION_REQUIREMENT_VERIFIED_ANY:
		return entity.VerificationRequirementVerifiedAny
	case entityv1.VerificationRequirement_VERIFICATION_REQUIREMENT_JPKI_ONLY:
		return entity.VerificationRequirementJPKIOnly
	default:
		return entity.VerificationRequirementNone
	}
}

// CreateTicketSaleInputFromProto maps an organizer Create request to the use
// case input.
func CreateTicketSaleInputFromProto(req *organizerticketsalev1.CreateRequest) usecase.CreateTicketSaleInput {
	in := usecase.CreateTicketSaleInput{
		SeriesID:                req.GetSeriesId().GetValue(),
		Name:                    req.GetName().GetValue(),
		Method:                  TicketSaleMethodFromProto(req.GetMethod()),
		SaleStart:               req.GetSaleStartTime().AsTime(),
		VerificationRequirement: VerificationRequirementFromProto(req.GetVerificationRequirement()),
		EventIDs:                make([]string, 0, len(req.GetEventIds())),
	}
	if req.GetSaleEndTime() != nil {
		in.SaleEnd = req.GetSaleEndTime().AsTime()
	}
	for _, id := range req.GetEventIds() {
		in.EventIDs = append(in.EventIDs, id.GetValue())
	}
	return in
}
