package mapper

import (
	proto "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/entity"
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
	}
}

// NewUserFromProto converts protobuf User to domain NewUser for creation.
func NewUserFromProto(protoUser *proto.User) *entity.NewUser {
	if protoUser == nil {
		return nil
	}

	newUser := &entity.NewUser{}

	if protoUser.Email != nil {
		newUser.Email = protoUser.Email.Value
	}

	return newUser
}
