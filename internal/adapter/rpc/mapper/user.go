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

	return &proto.User{
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

// NewUserFromCreateRequest converts JWT claims to domain NewUser.
// Security note: All identity fields (external_id, email, name) are extracted from
// validated JWT claims to prevent client-side identity tampering.
func NewUserFromCreateRequest(claims *auth.Claims) *entity.NewUser {
	if claims == nil {
		return nil
	}

	return &entity.NewUser{
		ExternalID: claims.Sub,
		Email:      claims.Email,
		Name:       claims.Name,
	}
}
