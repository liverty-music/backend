package rpc

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/pannpers/go-backend-scaffold/internal/adapter/rpc/mapper"
	"github.com/pannpers/go-backend-scaffold/internal/usecase"
	"github.com/pannpers/go-backend-scaffold/pkg/logging"
	api "buf.build/gen/go/pannpers/scaffold/protocolbuffers/go/pannpers/api/v1"
)

// PostHandler implements the PostService Connect interface.
type PostHandler struct {
	postUseCase *usecase.PostUseCase
	logger      *logging.Logger
}

// NewPostHandler creates a new post handler.
func NewPostHandler(postUseCase *usecase.PostUseCase, logger *logging.Logger) *PostHandler {
	return &PostHandler{
		postUseCase: postUseCase,
		logger:      logger,
	}
}

// GetPost retrieves a post by ID.
func (h *PostHandler) GetPost(ctx context.Context, req *connect.Request[api.GetPostRequest]) (*connect.Response[api.GetPostResponse], error) {
	if req == nil || req.Msg == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("request cannot be nil"))
	}

	if req.Msg.PostId == nil || req.Msg.PostId.GetValue() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("post_id is required"))
	}

	// Use the use case layer for business logic
	post, err := h.postUseCase.GetPost(ctx, req.Msg.PostId.GetValue())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.GetPostResponse{
		Post: mapper.PostToProto(post),
	}), nil
}

// CreatePost creates a new post.
func (h *PostHandler) CreatePost(ctx context.Context, req *connect.Request[api.CreatePostRequest]) (*connect.Response[api.CreatePostResponse], error) {
	if req == nil || req.Msg == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("request cannot be nil"))
	}

	if req.Msg.Title == nil || req.Msg.Title.GetValue() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("title is required"))
	}

	if req.Msg.AuthorId == nil || req.Msg.AuthorId.GetValue() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("author_id is required"))
	}

	// Convert protobuf to domain DTO
	newPost := mapper.NewPostFromCreateRequest(req.Msg.Title.GetValue(), req.Msg.AuthorId.GetValue())

	// Use the use case layer for business logic
	createdPost, err := h.postUseCase.CreatePost(ctx, newPost)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.CreatePostResponse{
		Post: mapper.PostToProto(createdPost),
	}), nil
}
