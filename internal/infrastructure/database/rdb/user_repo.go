package rdb

import (
	"context"
	"fmt"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/apperr"
	"github.com/liverty-music/backend/pkg/apperr/codes"
)

// UserRepository implements entity.UserRepository interface.
type UserRepository struct {
	db *Database
}

// NewUserRepository creates a new user repository instance.
func NewUserRepository(db *Database) *UserRepository {
	return &UserRepository{db: db}
}

// Create creates a new user in the database.
func (r *UserRepository) Create(ctx context.Context, params *entity.NewUser) (*entity.User, error) {
	if params == nil {
		return nil, apperr.New(codes.InvalidArgument, "params cannot be nil")
	}

	row := FromNewUser(params)

	_, err := r.db.NewInsert().Model(row).Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	return row.ToEntity(), nil
}

// Get retrieves a user by ID from the database.
func (r *UserRepository) Get(ctx context.Context, id string) (*entity.User, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	row := &User{}
	err := r.db.NewSelect().Model(row).Where("id = ?", id).Scan(ctx)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, apperr.New(codes.NotFound, fmt.Sprintf("user with ID %s not found", id))
		}
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return row.ToEntity(), nil
}

// Delete removes a user from the database.
func (r *UserRepository) Delete(ctx context.Context, id string) error {
	if id == "" {
		return apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	result, err := r.db.NewDelete().Model((*User)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return apperr.New(codes.NotFound, fmt.Sprintf("user with ID %s not found", id))
	}

	return nil
}
