package mapper

import (
	"context"
	"errors"

	proto "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	userv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/user/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
)

// UserToProto converts domain User entity to protobuf User: its profile
// fields, and its full name and phone number when set. It never carries the
// home's centroid or a verification level.
func UserToProto(user *entity.User) *proto.User {
	if user == nil {
		return nil
	}

	pb := &proto.User{
		Id: &proto.UserId{
			Value: user.ID,
		},
		Email: &proto.UserEmail{
			Value: user.Email,
		},
		ProviderUserId: &proto.User_ProviderUserId{
			Value: user.ProviderUserID,
		},
		Name: user.Name,
	}
	if user.Home != nil {
		pb.Home = HomeToProto(user.Home)
	}
	if user.PreferredLanguage != "" {
		lang := user.PreferredLanguage
		pb.PreferredLanguage = &lang
	}
	if user.FullName != "" {
		pb.FullName = &proto.FullName{Value: user.FullName}
	}
	if user.PhoneNumber != "" {
		pb.PhoneNumber = &proto.PhoneNumber{Value: user.PhoneNumber}
	}
	return pb
}

// HomeToProto converts domain Home entity to protobuf Home.
func HomeToProto(home *entity.Home) *proto.Home {
	if home == nil {
		return nil
	}
	pb := &proto.Home{
		CountryCode: home.CountryCode,
		Level_1:     home.Level1,
	}
	if home.Level2 != nil {
		pb.Level_2 = home.Level2
	}
	return pb
}

// ProtoHomeToEntity converts protobuf Home to domain Home entity.
func ProtoHomeToEntity(pbHome *proto.Home) *entity.Home {
	if pbHome == nil {
		return nil
	}
	home := &entity.Home{
		CountryCode: pbHome.CountryCode,
		Level1:      pbHome.Level_1,
	}
	if pbHome.Level_2 != nil {
		home.Level2 = pbHome.Level_2
	}
	return home
}

// GetClaimsFromContext extracts JWT claims from the authenticated context.
// Returns an error if the context is not authenticated or claims are missing.
func GetClaimsFromContext(ctx context.Context) (*auth.Claims, error) {
	claims, ok := auth.GetClaims(ctx)
	if !ok || claims == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	return claims, nil
}

// GetExternalUserID extracts the external user ID (subject claim) from the
// authenticated context. Returns CodeUnauthenticated if claims are missing
// or the subject claim is empty.
func GetExternalUserID(ctx context.Context) (string, error) {
	claims, err := GetClaimsFromContext(ctx)
	if err != nil {
		return "", err
	}
	if claims.Sub == "" {
		return "", connect.NewError(connect.CodeUnauthenticated, errors.New("token missing subject claim"))
	}
	return claims.Sub, nil
}

// NewUserFromCreateRequest converts JWT claims and a CreateRequest to a domain NewUser.
//
// Security note: All identity fields (provider_user_id, email, name) are extracted from
// validated JWT claims to prevent client-side identity tampering. The home and
// preferred_language fields are client-provided data accepted at signup.
//
// preferred_language is optional in the proto; an absent field returns an empty string
// via GetPreferredLanguage(), which is passed through to the entity as-is. The
// repository treats empty string as a NULL preferred_language in the database.
func NewUserFromCreateRequest(claims *auth.Claims, req *userv1.CreateRequest) *entity.NewUser {
	if claims == nil {
		return nil
	}

	return &entity.NewUser{
		ProviderUserID:    claims.Sub,
		Email:             claims.Email,
		Name:              claims.Name,
		Home:              ProtoHomeToEntity(req.GetHome()),
		PreferredLanguage: req.GetPreferredLanguage(),
	}
}

// PersonalDetailsFromUpdateRequest returns the full name and phone number a
// UserService.Update request gives, nil for a value not given. It fails when
// neither is given, a given value is empty, or a given value breaks the User's
// personal details rule ([entity.ValidatePersonalDetails]); the caller wraps
// the error as InvalidArgument.
func PersonalDetailsFromUpdateRequest(req *userv1.UpdateRequest) (fullName, phoneNumber *string, err error) {
	if req.FullName == nil && req.PhoneNumber == nil {
		return nil, nil, errors.New("at least one of full_name and phone_number must be given")
	}
	if req.FullName != nil {
		v := req.GetFullName().GetValue()
		if v == "" {
			return nil, nil, errors.New("full_name must not be empty")
		}
		fullName = &v
	}
	if req.PhoneNumber != nil {
		v := req.GetPhoneNumber().GetValue()
		if v == "" {
			return nil, nil, errors.New("phone_number must not be empty")
		}
		phoneNumber = &v
	}
	var name, phone string
	if fullName != nil {
		name = *fullName
	}
	if phoneNumber != nil {
		phone = *phoneNumber
	}
	if err := entity.ValidatePersonalDetails(name, phone); err != nil {
		return nil, nil, err
	}
	return fullName, phoneNumber, nil
}
