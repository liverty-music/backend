package rdb

import (
	"context"
	"database/sql"
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
	userColumns = `u.id, u.external_id, u.email, u.name, u.preferred_language, u.country, u.time_zone, COALESCE(u.safe_address, ''), u.is_active`

	homeColumns = `h.id, h.country_code, h.level_1, h.level_2`

	getUserQuery = `
		SELECT ` + userColumns + `, ` + homeColumns + `
		FROM users u
		LEFT JOIN homes h ON h.user_id = u.id
		WHERE u.id = $1
	`

	getUserByExternalIDQuery = `
		SELECT ` + userColumns + `, ` + homeColumns + `
		FROM users u
		LEFT JOIN homes h ON h.user_id = u.id
		WHERE u.external_id = $1
	`

	getUserByEmailQuery = `
		SELECT ` + userColumns + `, ` + homeColumns + `
		FROM users u
		LEFT JOIN homes h ON h.user_id = u.id
		WHERE u.email = $1
	`

	updateUserQuery = `
		WITH updated AS (
			UPDATE users SET external_id = $2, email = $3, name = $4, preferred_language = $5, country = $6, time_zone = $7
			WHERE id = $1
			RETURNING *
		)
		SELECT ` + `u.id, u.external_id, u.email, u.name, u.preferred_language, u.country, u.time_zone, COALESCE(u.safe_address, ''), u.is_active` + `, ` + homeColumns + `
		FROM updated u
		LEFT JOIN homes h ON h.user_id = u.id
	`

	listUsersQuery = `
		SELECT ` + userColumns + `, ` + homeColumns + `
		FROM users u
		LEFT JOIN homes h ON h.user_id = u.id
		ORDER BY u.id
		LIMIT $1 OFFSET $2
	`

	updateSafeAddressQuery = `
		UPDATE users SET safe_address = $2 WHERE id = $1
	`

	upsertHomeQuery = `
		INSERT INTO homes (user_id, country_code, level_1, level_2)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id) DO UPDATE SET
			country_code = EXCLUDED.country_code,
			level_1 = EXCLUDED.level_1,
			level_2 = EXCLUDED.level_2,
			updated_at = now()
		RETURNING id
	`

	insertUserQuery = `
		INSERT INTO users (external_id, email, name, preferred_language, country, time_zone, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`

	deleteUserQuery = `
		DELETE FROM users WHERE id = $1
	`
)

// NewUserRepository creates a new user repository instance.
func NewUserRepository(db *Database) *UserRepository {
	return &UserRepository{db: db}
}

// scanUser scans a user row with optional home columns from a LEFT JOIN.
func scanUser(scanner interface{ Scan(dest ...any) error }) (*entity.User, error) {
	user := &entity.User{}
	var homeID, countryCode, level1, level2 sql.NullString

	err := scanner.Scan(
		&user.ID, &user.ExternalID, &user.Email, &user.Name,
		&user.PreferredLanguage, &user.Country, &user.TimeZone,
		&user.SafeAddress, &user.IsActive,
		&homeID, &countryCode, &level1, &level2,
	)
	if err != nil {
		return nil, err
	}

	if homeID.Valid {
		user.Home = &entity.Home{
			ID:          homeID.String,
			CountryCode: countryCode.String,
			Level1:      level1.String,
		}
		if level2.Valid {
			user.Home.Level2 = &level2.String
		}
	}

	return user, nil
}

// Create creates a new user in the database.
// If params.Home is non-nil, the home record is inserted atomically.
func (r *UserRepository) Create(ctx context.Context, params *entity.NewUser) (*entity.User, error) {
	if params == nil {
		return nil, apperr.New(codes.InvalidArgument, "params cannot be nil")
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin transaction")
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var userID string
	err = tx.QueryRow(ctx, insertUserQuery,
		params.ExternalID, params.Email, params.Name,
		params.PreferredLanguage, params.Country, params.TimeZone, true,
	).Scan(&userID)
	if err != nil {
		if IsUniqueViolation(err) {
			r.db.logger.Warn(ctx, "duplicate user",
				slog.String("entityType", "user"),
				slog.String("email", params.Email),
			)
		}
		return nil, toAppErr(err, "failed to create user", slog.String("email", params.Email))
	}

	var home *entity.Home
	if params.Home != nil {
		var homeID string
		err = tx.QueryRow(ctx, upsertHomeQuery,
			userID, params.Home.CountryCode, params.Home.Level1, params.Home.Level2,
		).Scan(&homeID)
		if err != nil {
			return nil, toAppErr(err, "failed to create home", slog.String("user_id", userID))
		}
		home = &entity.Home{
			ID:          homeID,
			CountryCode: params.Home.CountryCode,
			Level1:      params.Home.Level1,
			Level2:      params.Home.Level2,
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit transaction")
	}

	r.db.logger.Info(ctx, "user created",
		slog.String("entityType", "user"),
		slog.String("userID", userID),
	)

	return &entity.User{
		ID:                userID,
		ExternalID:        params.ExternalID,
		Email:             params.Email,
		Name:              params.Name,
		PreferredLanguage: params.PreferredLanguage,
		Country:           params.Country,
		TimeZone:          params.TimeZone,
		IsActive:          true,
		Home:              home,
	}, nil
}

// Get retrieves a user by ID from the database.
func (r *UserRepository) Get(ctx context.Context, id string) (*entity.User, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	user, err := scanUser(r.db.Pool.QueryRow(ctx, getUserQuery, id))
	if err != nil {
		return nil, toAppErr(err, "failed to get user", slog.String("user_id", id))
	}

	return user, nil
}

// GetByExternalID retrieves a user by identity provider ID from the database.
func (r *UserRepository) GetByExternalID(ctx context.Context, externalID string) (*entity.User, error) {
	if externalID == "" {
		return nil, apperr.New(codes.InvalidArgument, "external ID cannot be empty")
	}

	user, err := scanUser(r.db.Pool.QueryRow(ctx, getUserByExternalIDQuery, externalID))
	if err != nil {
		return nil, toAppErr(err, "failed to get user by external ID", slog.String("external_id", externalID))
	}

	return user, nil
}

// GetByEmail retrieves a user by email address from the database.
func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*entity.User, error) {
	if email == "" {
		return nil, apperr.New(codes.InvalidArgument, "email cannot be empty")
	}

	user, err := scanUser(r.db.Pool.QueryRow(ctx, getUserByEmailQuery, email))
	if err != nil {
		return nil, toAppErr(err, "failed to get user by email", slog.String("email", email))
	}

	return user, nil
}

// Update updates user information in the database.
func (r *UserRepository) Update(ctx context.Context, id string, params *entity.NewUser) (*entity.User, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}
	if params == nil {
		return nil, apperr.New(codes.InvalidArgument, "params cannot be nil")
	}

	user, err := scanUser(r.db.Pool.QueryRow(ctx, updateUserQuery,
		id, params.ExternalID, params.Email, params.Name,
		params.PreferredLanguage, params.Country, params.TimeZone,
	))
	if err != nil {
		return nil, toAppErr(err, "failed to update user", slog.String("user_id", id))
	}

	r.db.logger.Info(ctx, "user updated",
		slog.String("entityType", "user"),
		slog.String("userID", user.ID),
	)

	return user, nil
}

// List retrieves users with pagination from the database.
func (r *UserRepository) List(ctx context.Context, limit, offset int) ([]*entity.User, error) {
	rows, err := r.db.Pool.Query(ctx, listUsersQuery, limit, offset)
	if err != nil {
		return nil, toAppErr(err, "failed to list users")
	}
	defer rows.Close()

	var users []*entity.User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, toAppErr(err, "failed to scan user row")
		}
		users = append(users, user)
	}

	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to iterate user rows")
	}

	return users, nil
}

// UpdateSafeAddress sets the predicted Safe address for a user.
func (r *UserRepository) UpdateSafeAddress(ctx context.Context, id, safeAddress string) error {
	if id == "" {
		return apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	result, err := r.db.Pool.Exec(ctx, updateSafeAddressQuery, id, safeAddress)
	if err != nil {
		return toAppErr(err, "failed to update safe address", slog.String("user_id", id))
	}

	if result.RowsAffected() == 0 {
		return apperr.Wrap(apperr.ErrNotFound, codes.NotFound, fmt.Sprintf("user with ID %s not found", id))
	}

	r.db.logger.Info(ctx, "user updated",
		slog.String("entityType", "user"),
		slog.String("userID", id),
		slog.String("field", "safeAddress"),
	)

	return nil
}

// UpdateHome sets or changes the user's home area.
// Uses UPSERT on the homes table to create or update the home record.
func (r *UserRepository) UpdateHome(ctx context.Context, id string, home *entity.Home) (*entity.User, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	_, err := r.db.Pool.Exec(ctx, upsertHomeQuery,
		id, home.CountryCode, home.Level1, home.Level2,
	)
	if err != nil {
		return nil, toAppErr(err, "failed to upsert home", slog.String("user_id", id))
	}

	// Re-fetch the user with home JOIN to return the complete entity.
	user, err := scanUser(r.db.Pool.QueryRow(ctx, getUserQuery, id))
	if err != nil {
		return nil, toAppErr(err, "failed to get user after home update", slog.String("user_id", id))
	}

	r.db.logger.Info(ctx, "user updated",
		slog.String("entityType", "user"),
		slog.String("userID", id),
		slog.String("field", "home"),
	)

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
