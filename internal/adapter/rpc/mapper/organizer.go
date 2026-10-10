package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/entity"
)

// OrganizerToProto maps a domain Organizer to its Protobuf message. The internal
// zitadel_org_id and operational status are backend-only and intentionally not
// exposed on the consumer entity.
func OrganizerToProto(o *entity.Organizer) *entityv1.Organizer {
	if o == nil {
		return nil
	}
	return &entityv1.Organizer{
		Id:                 &entityv1.OrganizerId{Value: o.ID},
		Name:               &entityv1.OrganizerName{Value: o.Name},
		SellerDetails:      SellerDetailsToProto(o.SellerDetails),
		PlatformFeeRateBps: int32(o.PlatformFeeRateBps),
	}
}

// SellerDetailsToProto maps domain seller details to Protobuf, or nil.
func SellerDetailsToProto(d *entity.SellerDetails) *entityv1.SellerDetails {
	if d == nil {
		return nil
	}
	return &entityv1.SellerDetails{
		LegalName:          d.LegalName,
		RepresentativeName: d.RepresentativeName,
		Address:            d.Address,
		PhoneNumber:        d.PhoneNumber,
		ContactEmail:       d.ContactEmail,
	}
}

// SellerDetailsFromProto maps Protobuf seller details to the domain value.
func SellerDetailsFromProto(pb *entityv1.SellerDetails) entity.SellerDetails {
	return entity.SellerDetails{
		LegalName:          pb.GetLegalName(),
		RepresentativeName: pb.GetRepresentativeName(),
		Address:            pb.GetAddress(),
		PhoneNumber:        pb.GetPhoneNumber(),
		ContactEmail:       pb.GetContactEmail(),
	}
}

// OrganizersToProto maps a slice of domain Organizers to Protobuf messages.
func OrganizersToProto(organizers []*entity.Organizer) []*entityv1.Organizer {
	out := make([]*entityv1.Organizer, 0, len(organizers))
	for _, o := range organizers {
		out = append(out, OrganizerToProto(o))
	}
	return out
}
