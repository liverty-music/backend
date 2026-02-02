package rdb

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// UserRepository implements entity.UserRepository interface.
type UserRepository struct {
	db *Database
}

const (
	getUserQuery = `
		SELECT id, email, name, preferred_language, country, time_zone, is_active, created_at, updated_at
		FROM users
		WHERE id = $1
	`

	insertUserQuery = `
		INSERT INTO users (email, name, preferred_language, country, time_zone, is_active)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, updated_at
	`
	deleteUserQuery = `
		DELETE FROM users WHERE id = $1
	`
)

// NewUserRepository creates a new user repository instance.
func NewUserRepository(db *Database) *UserRepository {
	return &UserRepository{db: db}
}

// Create creates a new user in the database.
func (r *UserRepository) Create(ctx context.Context, params *entity.NewUser) (*entity.User, error) {
	if params == nil {
		return nil, apperr.New(codes.InvalidArgument, "params cannot be nil")
	}

	user := &entity.User{
		Email:             params.Email,
		Name:              params.Name,
		PreferredLanguage: params.PreferredLanguage,
		Country:           params.Country,
		TimeZone:          params.TimeZone,
		IsActive:          true,
	}

	err := r.db.Pool.QueryRow(ctx, insertUserQuery,
		user.Email, user.Name, user.PreferredLanguage, user.Country, user.TimeZone, user.IsActive,
	).Scan(&user.ID, &user.CreateTime, &user.UpdateTime)
	if err != nil {
		return nil, toAppErr(err, "failed to create user", slog.String("email", user.Email))
	}

	return user, nil
}

// Get retrieves a user by ID from the database.
func (r *UserRepository) Get(ctx context.Context, id string) (*entity.User, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	user := &entity.User{}
	err := r.db.Pool.QueryRow(ctx, getUserQuery, id).Scan(
		&user.ID, &user.Email, &user.Name, &user.PreferredLanguage, &user.Country, &user.TimeZone, &user.IsActive, &user.CreateTime, &user.UpdateTime,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to get user", slog.String("user_id", id))
	}

	return user, nil
}

// Delete removes a user from the database.
func (r *UserRepository) Delete(ctx context.Context, id string) error {
	if id == "" {
		return apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	result, err := r.db.Pool.Exec(ctx, deleteUserQuery, id)
	if err != nil {
		return toAppErr(err, "failed to delete user", slog.String("user_id", id))
	}

	if result.RowsAffected() == 0 {
		return apperr.Wrap(apperr.ErrNotFound, codes.NotFound, fmt.Sprintf("user with ID %s not found", id))
	}

	return nil
}
