// Package usecase contains business logic implementations for the application.
package usecase

import (
	"context"
	"log/slog"

	"github.com/pannpers/go-backend-scaffold/internal/entity"
	"github.com/pannpers/go-backend-scaffold/pkg/apperr"
	"github.com/pannpers/go-backend-scaffold/pkg/apperr/codes"
	"github.com/pannpers/go-backend-scaffold/pkg/logging"
)

// PostUseCase handles post business logic.
type PostUseCase struct {
	postRepo entity.PostRepository
	logger   *logging.Logger
}

// NewPostUseCase creates a new post use case.
func NewPostUseCase(postRepo entity.PostRepository, logger *logging.Logger) *PostUseCase {
	return &PostUseCase{
		postRepo: postRepo,
		logger:   logger,
	}
}

// CreatePost creates a new post.
func (uc *PostUseCase) CreatePost(ctx context.Context, params *entity.NewPost) (*entity.Post, error) {
	post, err := uc.postRepo.Create(ctx, params)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to create post", 
			slog.String("title", params.Title),
			slog.String("user_id", params.UserID),
		)
	}

	uc.logger.Info(ctx, "Post created successfully", slog.String("post_id", post.ID))

	return post, nil
}

// GetPost retrieves a post by ID.
func (uc *PostUseCase) GetPost(ctx context.Context, id string) (*entity.Post, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "post ID cannot be empty")
	}

	post, err := uc.postRepo.Get(ctx, id)
	if err != nil {
		return nil, apperr.Wrap(err, codes.NotFound, "failed to get post", 
			slog.String("post_id", id),
		)
	}

	return post, nil
}

// DeletePost deletes a post by ID.
func (uc *PostUseCase) DeletePost(ctx context.Context, id string) error {
	if id == "" {
		return apperr.New(codes.InvalidArgument, "post ID cannot be empty")
	}

	err := uc.postRepo.Delete(ctx, id)
	if err != nil {
		return apperr.Wrap(err, codes.Internal, "failed to delete post", 
			slog.String("post_id", id),
		)
	}

	uc.logger.Info(ctx, "Post deleted successfully", slog.String("post_id", id))

	return nil
}
