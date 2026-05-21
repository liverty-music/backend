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

// UserToProto converts domain User entity to protobuf User.
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
		ExternalId: &proto.UserExternalId{
			Value: user.ExternalID,
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

// RequireUserIDMatch verifies that the userID supplied by the client in the
// request body matches the caller's authenticated userID. It returns
// InvalidArgument when reqUserID is empty and PermissionDenied when the two
// values differ.
//
// Handlers for per-user RPCs that expose an explicit user_id field in their
// request message MUST invoke this helper before performing any business
// logic so that cross-user requests are rejected before they reach the
// persistence layer.
func RequireUserIDMatch(callerUserID, reqUserID string) error {
	if reqUserID == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("user_id is required"))
	}
	if reqUserID != callerUserID {
		return connect.NewError(connect.CodePermissionDenied, errors.New("user_id does not match authenticated user"))
	}
	return nil
}

// NewUserFromCreateRequest converts JWT claims and a CreateRequest to a domain NewUser.
//
// Security note: All identity fields (external_id, email, name) are extracted from
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
		ExternalID:        claims.Sub,
		Email:             claims.Email,
		Name:              claims.Name,
		Home:              ProtoHomeToEntity(req.GetHome()),
		PreferredLanguage: req.GetPreferredLanguage(),
	}
}
