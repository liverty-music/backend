package mapper

import (
	"context"
	"errors"

	proto "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
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

// NewUserFromCreateRequest converts JWT claims and optional home to domain NewUser.
// Security note: All identity fields (external_id, email, name) are extracted from
// validated JWT claims to prevent client-side identity tampering.
// The home field is the only client-provided data (selected during onboarding).
func NewUserFromCreateRequest(claims *auth.Claims, home *proto.Home) *entity.NewUser {
	if claims == nil {
		return nil
	}

	return &entity.NewUser{
		ExternalID: claims.Sub,
		Email:      claims.Email,
		Name:       claims.Name,
		Home:       ProtoHomeToEntity(home),
	}
}
