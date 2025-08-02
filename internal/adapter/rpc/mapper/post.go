package mapper

import (
	"time"

	"github.com/pannpers/go-backend-scaffold/internal/entity"
	proto "buf.build/gen/go/pannpers/scaffold/protocolbuffers/go/pannpers/entity/v1"
)

// PostToProto converts domain Post entity to protobuf Post.
func PostToProto(post *entity.Post) *proto.Post {
	if post == nil {
		return nil
	}

	return &proto.Post{
		Id: &proto.PostId{
			Value: post.ID,
		},
		Title: &proto.PostTitle{
			Value: post.Title,
		},
	}
}

// PostFromProto converts protobuf Post to domain Post entity.
func PostFromProto(protoPost *proto.Post) *entity.Post {
	if protoPost == nil {
		return nil
	}

	post := &entity.Post{
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if protoPost.Id != nil {
		post.ID = protoPost.Id.Value
	}

	if protoPost.Title != nil {
		post.Title = protoPost.Title.Value
	}

	return post
}

// NewPostFromProto converts protobuf Post to domain NewPost for creation.
func NewPostFromProto(protoPost *proto.Post, userID string) *entity.NewPost {
	if protoPost == nil {
		return nil
	}

	newPost := &entity.NewPost{
		UserID: userID,
	}

	if protoPost.Title != nil {
		newPost.Title = protoPost.Title.Value
	}

	return newPost
}

// NewPostFromCreateRequest converts CreatePostRequest fields to domain NewPost for creation.
func NewPostFromCreateRequest(title, authorID string) *entity.NewPost {
	return &entity.NewPost{
		Title:  title,
		UserID: authorID,
	}
}
