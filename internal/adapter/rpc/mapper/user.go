package mapper

import (
	"time"

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

// UserArtistSubscriptionToProto converts domain UserArtistSubscription entity to protobuf.
// func UserArtistSubscriptionToProto(subscription *entity.UserArtistSubscription) *proto.UserArtistSubscription {
// 	if subscription == nil {
// 		return nil
// 	}

// 	return &proto.UserArtistSubscription{
// 		Id: &proto.UserArtistSubscriptionId{
// 			Value: subscription.ID,
// 		},
// 		UserId: &proto.UserId{
// 			Value: subscription.UserID,
// 		},
// 		ArtistId: &proto.ArtistId{
// 			Value: subscription.ArtistID,
// 		},
// 	}
// }
