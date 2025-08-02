package mapper

import (
	"time"

	"github.com/pannpers/go-backend-scaffold/internal/entity"
	proto "buf.build/gen/go/pannpers/scaffold/protocolbuffers/go/pannpers/entity/v1"
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
		Name: &proto.UserName{
			Value: user.Name,
		},
		Email: &proto.UserEmail{
			Value: user.Email,
		},
	}
}

// UserFromProto converts protobuf User to domain User entity.
func UserFromProto(protoUser *proto.User) *entity.User {
	if protoUser == nil {
		return nil
	}

	user := &entity.User{
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if protoUser.Id != nil {
		user.ID = protoUser.Id.Value
	}

	if protoUser.Name != nil {
		user.Name = protoUser.Name.Value
	}

	if protoUser.Email != nil {
		user.Email = protoUser.Email.Value
	}

	return user
}

// NewUserFromProto converts protobuf User to domain NewUser for creation.
func NewUserFromProto(protoUser *proto.User) *entity.NewUser {
	if protoUser == nil {
		return nil
	}

	newUser := &entity.NewUser{}

	if protoUser.Name != nil {
		newUser.Name = protoUser.Name.Value
	}

	if protoUser.Email != nil {
		newUser.Email = protoUser.Email.Value
	}

	return newUser
}
