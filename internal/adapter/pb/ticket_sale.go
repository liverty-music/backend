package pb

import (
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizerticketsalev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/ticket_sale/v1"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"google.golang.org/genproto/googleapis/type/datetime"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// FromTicketSale maps a TicketSale with the events it names to Protobuf.
func FromTicketSale(s *entity.TicketSale) *entityv1.TicketSale {
	if s == nil {
		return nil
	}
	pb := &entityv1.TicketSale{
		Id:                      &entityv1.TicketSaleId{Value: string(s.ID)},
		SeriesId:                &entityv1.SeriesId{Value: s.SeriesID},
		Name:                    &entityv1.TicketSaleName{Value: s.Name},
		Method:                  ticketSaleMethodToProto(s.Method),
		SaleStartTime:           SaleTimeToProto(s.SaleStartTime, s.Location()),
		SaleEndTime:             SaleTimeToProto(s.SaleEndTime, s.Location()),
		VerificationRequirement: verificationRequirementToProto(s.VerificationRequirement),
		EventIds:                make([]*entityv1.EventId, 0, len(s.EventIDs)),
	}
	for _, id := range s.EventIDs {
		pb.EventIds = append(pb.EventIds, &entityv1.EventId{Value: id})
	}
	return pb
}

// FromTicketSales maps the sales of a list to Protobuf; see
// [FromTicketSale].
func FromTicketSales(sales []*entity.TicketSale) []*entityv1.TicketSale {
	out := make([]*entityv1.TicketSale, 0, len(sales))
	for _, s := range sales {
		if pb := FromTicketSale(s); pb != nil {
			out = append(out, pb)
		}
	}
	return out
}

// ToTicketSaleMethod maps a Protobuf sale method to the domain type;
// UNSPECIFIED and unknown values map to Unspecified.
func ToTicketSaleMethod(m entityv1.TicketSaleMethod) entity.TicketSaleMethod {
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

// ToVerificationRequirement maps a Protobuf VerificationRequirement to
// the domain type; UNSPECIFIED is None.
func ToVerificationRequirement(r entityv1.VerificationRequirement) entity.VerificationRequirement {
	switch r {
	case entityv1.VerificationRequirement_VERIFICATION_REQUIREMENT_VERIFIED_ANY:
		return entity.VerificationRequirementVerifiedAny
	case entityv1.VerificationRequirement_VERIFICATION_REQUIREMENT_JPKI_ONLY:
		return entity.VerificationRequirementJPKIOnly
	default:
		return entity.VerificationRequirementNone
	}
}

// SaleWindowFromProto reads a sale window from its start and end date-times:
// both name one IANA time zone, which is returned with the instants of the two
// times. It fails with InvalidArgument when either is invalid (see
// [SaleTimeFromProto]) or they name different zones. A missing end is a zero
// instant, which the use case refuses.
func SaleWindowFromProto(start, end *datetime.DateTime) (zone string, startInstant, endInstant time.Time, err error) {
	startInstant, zone, err = SaleTimeFromProto(start)
	if err != nil {
		return "", time.Time{}, time.Time{}, err
	}
	if end == nil {
		return zone, startInstant, time.Time{}, nil
	}
	var endZone string
	endInstant, endZone, err = SaleTimeFromProto(end)
	if err != nil {
		return "", time.Time{}, time.Time{}, err
	}
	if endZone != zone {
		return "", time.Time{}, time.Time{}, apperr.New(codes.InvalidArgument, "the start and end times of a sale must name the same time zone")
	}
	return zone, startInstant, endInstant, nil
}

// CreateTicketSaleInputFromProto maps an organizer Create request to the use
// case input. It fails with InvalidArgument when the window is invalid (see
// [SaleWindowFromProto]).
func CreateTicketSaleInputFromProto(req *organizerticketsalev1.CreateRequest) (usecase.CreateTicketSaleInput, error) {
	zone, start, end, err := SaleWindowFromProto(req.GetSaleStartTime(), req.GetSaleEndTime())
	if err != nil {
		return usecase.CreateTicketSaleInput{}, err
	}
	in := usecase.CreateTicketSaleInput{
		SeriesID:                req.GetSeriesId().GetValue(),
		Name:                    req.GetName().GetValue(),
		Method:                  ToTicketSaleMethod(req.GetMethod()),
		TimeZone:                zone,
		SaleStart:               start,
		SaleEnd:                 end,
		VerificationRequirement: ToVerificationRequirement(req.GetVerificationRequirement()),
		EventIDs:                make([]string, 0, len(req.GetEventIds())),
	}
	for _, id := range req.GetEventIds() {
		in.EventIDs = append(in.EventIDs, id.GetValue())
	}
	return in, nil
}
