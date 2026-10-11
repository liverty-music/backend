package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/liverty-music/backend/internal/entity"
)

// IdentityVerificationToProto converts a domain IdentityVerification to its Protobuf message. Only the result
// fields are included — never raw certificate data.
func IdentityVerificationToProto(vi *entity.IdentityVerification) *entityv1.IdentityVerification {
	if vi == nil {
		return nil
	}
	return &entityv1.IdentityVerification{
		Id:             &entityv1.IdentityVerificationId{Value: vi.ID},
		UserId:         &entityv1.UserId{Value: vi.UserID},
		Method:         verificationMethodToProto(vi.Method),
		ProviderUserId: &entityv1.IdentityVerification_ProviderUserId{Value: vi.ProviderUserID},
		DedupeStrength: dedupeStrengthToProto(vi.DedupeStrength),
		VerifyTime:     timestamppb.New(vi.VerifyTime),
		Status:         verificationStatusToProto(vi.Status),
	}
}

// VerificationMethodFromProto converts the proto VerificationMethod to the
// domain VerificationMethod.
func VerificationMethodFromProto(m entityv1.VerificationMethod) entity.VerificationMethod {
	switch m {
	case entityv1.VerificationMethod_VERIFICATION_METHOD_JPKI:
		return entity.VerificationMethodJPKI
	case entityv1.VerificationMethod_VERIFICATION_METHOD_DRIVER_LICENCE:
		return entity.VerificationMethodDriverLicence
	default:
		return entity.VerificationMethodUnspecified
	}
}

// VerificationStatusToProto converts the domain VerificationStatus to its proto
// enum value.
func VerificationStatusToProto(s entity.VerificationStatus) entityv1.VerificationStatus {
	return verificationStatusToProto(s)
}

// verificationMethodToProto converts the domain VerificationMethod to the proto
// enum value.
func verificationMethodToProto(m entity.VerificationMethod) entityv1.VerificationMethod {
	switch m {
	case entity.VerificationMethodJPKI:
		return entityv1.VerificationMethod_VERIFICATION_METHOD_JPKI
	case entity.VerificationMethodDriverLicence:
		return entityv1.VerificationMethod_VERIFICATION_METHOD_DRIVER_LICENCE
	default:
		return entityv1.VerificationMethod_VERIFICATION_METHOD_UNSPECIFIED
	}
}

// dedupeStrengthToProto converts the domain DedupeStrength to the proto enum
// value.
func dedupeStrengthToProto(s entity.DedupeStrength) entityv1.DedupeStrength {
	switch s {
	case entity.DedupeStrengthStrong:
		return entityv1.DedupeStrength_DEDUPE_STRENGTH_STRONG
	case entity.DedupeStrengthWeak:
		return entityv1.DedupeStrength_DEDUPE_STRENGTH_WEAK
	default:
		return entityv1.DedupeStrength_DEDUPE_STRENGTH_UNSPECIFIED
	}
}

// verificationStatusToProto converts the domain VerificationStatus to the proto
// enum value.
func verificationStatusToProto(s entity.VerificationStatus) entityv1.VerificationStatus {
	switch s {
	case entity.VerificationStatusActive:
		return entityv1.VerificationStatus_VERIFICATION_STATUS_ACTIVE
	case entity.VerificationStatusNeedsReverification:
		return entityv1.VerificationStatus_VERIFICATION_STATUS_NEEDS_REVERIFICATION
	default:
		return entityv1.VerificationStatus_VERIFICATION_STATUS_UNSPECIFIED
	}
}
