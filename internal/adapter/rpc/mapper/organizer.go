package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/entity"
)

// OrganizerToProto maps a domain Organizer to its Protobuf message for the
// admin and organizer consoles: its id, name, short name (when it has one),
// status, business details and platform fee rate. The internal
// provider_organizer_id and owner email are backend-only and intentionally not
// exposed.
func OrganizerToProto(o *entity.Organizer) *entityv1.Organizer {
	if o == nil {
		return nil
	}
	pb := PublicOrganizerToProto(o)
	if o.ShortName != "" {
		pb.ShortName = &entityv1.OrganizerShortName{Value: o.ShortName}
	}
	pb.Status = organizerStatusToProto(o.Status)
	pb.PlatformFeeRateBps = int32(o.PlatformFeeRateBps)
	return pb
}

// organizerStatusToProto maps a domain OrganizerStatus to its Protobuf enum.
// OrganizerStatusUnspecified, and any value the proto does not define, maps to
// ORGANIZER_STATUS_UNSPECIFIED.
func organizerStatusToProto(s entity.OrganizerStatus) entityv1.OrganizerStatus {
	switch s {
	case entity.OrganizerStatusProvisioning:
		return entityv1.OrganizerStatus_ORGANIZER_STATUS_PROVISIONING
	case entity.OrganizerStatusActive:
		return entityv1.OrganizerStatus_ORGANIZER_STATUS_ACTIVE
	case entity.OrganizerStatusDeactivated:
		return entityv1.OrganizerStatus_ORGANIZER_STATUS_DEACTIVATED
	default:
		return entityv1.OrganizerStatus_ORGANIZER_STATUS_UNSPECIFIED
	}
}

// PublicOrganizerToProto maps a domain Organizer to the Protobuf message a fan
// sees: its id, name and, when present, business details, which the checkout
// shows as the 特商法 disclosure. The short name, the status and the platform
// fee rate are the Organizer's business with the platform and are never set.
func PublicOrganizerToProto(o *entity.Organizer) *entityv1.Organizer {
	if o == nil {
		return nil
	}
	pb := &entityv1.Organizer{
		Id:   &entityv1.OrganizerId{Value: o.ID},
		Name: &entityv1.OrganizerName{Value: o.Name},
	}
	if d := o.BusinessDetails; d != nil {
		pb.LegalName = &entityv1.LegalName{Value: d.LegalName}
		pb.RepresentativeName = &entityv1.RepresentativeName{Value: d.RepresentativeName}
		pb.BusinessAddress = &entityv1.BusinessAddress{Value: d.BusinessAddress}
		pb.BusinessPhoneNumber = &entityv1.PhoneNumber{Value: d.BusinessPhoneNumber}
		pb.ContactEmail = &entityv1.Email{Value: d.ContactEmail}
	}
	return pb
}

// BusinessDetailsFromSetRequest maps the five business details of an admin
// SetBusinessDetails request to the domain value.
func BusinessDetailsFromSetRequest(
	legalName *entityv1.LegalName,
	representativeName *entityv1.RepresentativeName,
	businessAddress *entityv1.BusinessAddress,
	businessPhoneNumber *entityv1.PhoneNumber,
	contactEmail *entityv1.Email,
) entity.BusinessDetails {
	return entity.BusinessDetails{
		LegalName:           legalName.GetValue(),
		RepresentativeName:  representativeName.GetValue(),
		BusinessAddress:     businessAddress.GetValue(),
		BusinessPhoneNumber: businessPhoneNumber.GetValue(),
		ContactEmail:        contactEmail.GetValue(),
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
