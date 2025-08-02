package rdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/pannpers/go-backend-scaffold/internal/entity"
	"github.com/pannpers/go-backend-scaffold/pkg/apperr"
	"github.com/pannpers/go-backend-scaffold/pkg/apperr/codes"
)

// PostRepository implements entity.PostRepository interface.
type PostRepository struct {
	db *Database
}

// NewPostRepository creates a new post repository instance.
func NewPostRepository(db *Database) entity.PostRepository {
	return &PostRepository{db: db}
}

// Create creates a new post in the database.
func (r *PostRepository) Create(ctx context.Context, params *entity.NewPost) (*entity.Post, error) {
	if params == nil {
		return nil, apperr.New(codes.InvalidArgument, "params cannot be nil")
	}

	row := FromNewPost(params)

	_, err := r.db.NewInsert().Model(row).Exec(ctx)
	if err != nil {
		if isForeignKeyViolation(err) {
			return nil, apperr.New(codes.FailedPrecondition,
				fmt.Sprintf("user with ID %s does not exist", params.UserID),
			)
		}
		return nil, fmt.Errorf("failed to create post: %w", err)
	}

	// Convert back to domain entity
	return row.ToEntity(), nil
}

// Get retrieves a post by ID from the database.
func (r *PostRepository) Get(ctx context.Context, id string) (*entity.Post, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "post ID cannot be empty")
	}

	row := &Post{}
	err := r.db.NewSelect().Model(row).Where("id = ?", id).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperr.Wrap(err, codes.NotFound,
				fmt.Sprintf("post with ID %s not found", id),
			)
		}
		if isInvalidUUIDFormat(err) {
			return nil, apperr.Wrap(err, codes.InvalidArgument,
				fmt.Sprintf("invalid UUID format: %s", id),
			)
		}
		return nil, fmt.Errorf("failed to get post: %w", err)
	}

	return row.ToEntity(), nil
}

// Delete removes a post from the database.
func (r *PostRepository) Delete(ctx context.Context, id string) error {
	if id == "" {
		return apperr.New(codes.InvalidArgument, "post ID cannot be empty")
	}

	result, err := r.db.NewDelete().Model((*Post)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete post: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return apperr.New(codes.NotFound, fmt.Sprintf("post with ID %s not found", id))
	}

	return nil
}
