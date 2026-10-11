package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizerticketsalev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/ticket_sale/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// TicketSaleToProto maps a TicketSale, with its TicketTypes as the reader saw
// them, to Protobuf. withCounts sets each TicketType's quantity and, for a
// FirstCome sale, its sold and held counts, which only the owning Organizer
// may see.
func TicketSaleToProto(v *usecase.TicketSaleView, withCounts bool) *entityv1.TicketSale {
	if v == nil || v.Sale == nil {
		return nil
	}
	s := v.Sale
	pb := &entityv1.TicketSale{
		Id:                      &entityv1.TicketSaleId{Value: string(s.ID)},
		SeriesId:                &entityv1.SeriesId{Value: s.SeriesID},
		Name:                    &entityv1.TicketSaleName{Value: s.Name},
		Method:                  ticketSaleMethodToProto(s.Method),
		SaleStartTime:           timestamppb.New(s.SaleStartTime),
		SaleEndTime:             timestamppb.New(s.SaleEndTime),
		VerificationRequirement: verificationRequirementToProto(s.VerificationRequirement),
		TicketTypes:             make([]*entityv1.TicketType, 0, len(v.TicketTypes)),
	}
	if s.DrawnTime != nil {
		pb.DrawTime = timestamppb.New(*s.DrawnTime)
	}
	for _, tv := range v.TicketTypes {
		if tt := TicketTypeToProto(tv, withCounts); tt != nil {
			pb.TicketTypes = append(pb.TicketTypes, tt)
		}
	}
	return pb
}

// TicketSalesToProto maps the sales of a list to Protobuf; see
// [TicketSaleToProto].
func TicketSalesToProto(views []*usecase.TicketSaleView, withCounts bool) []*entityv1.TicketSale {
	out := make([]*entityv1.TicketSale, 0, len(views))
	for _, v := range views {
		if pb := TicketSaleToProto(v, withCounts); pb != nil {
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
// case input. A missing end time stays nil, and a missing per-account limit
// is 0, which the use case reads as the default.
func CreateTicketSaleInputFromProto(req *organizerticketsalev1.CreateRequest) usecase.CreateTicketSaleInput {
	in := usecase.CreateTicketSaleInput{
		SeriesID:                req.GetSeriesId().GetValue(),
		Name:                    req.GetName().GetValue(),
		Method:                  TicketSaleMethodFromProto(req.GetMethod()),
		SaleStart:               req.GetSaleStartTime().AsTime(),
		VerificationRequirement: VerificationRequirementFromProto(req.GetVerificationRequirement()),
		TicketTypes:             make([]usecase.TicketTypeInput, 0, len(req.GetTicketTypes())),
	}
	if req.GetSaleEndTime() != nil {
		end := req.GetSaleEndTime().AsTime()
		in.SaleEnd = &end
	}
	for _, d := range req.GetTicketTypes() {
		in.TicketTypes = append(in.TicketTypes, usecase.TicketTypeInput{
			EventID:         d.GetEventId().GetValue(),
			Price:           d.GetPrice().GetValue(),
			Quantity:        int(d.GetQuantity()),
			PerAccountLimit: int(d.GetPerAccountLimit()),
		})
	}
	return in
}
