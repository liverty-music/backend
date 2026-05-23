package rdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	infrageo "github.com/liverty-music/backend/internal/infrastructure/geo"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// UserRepository implements entity.UserRepository interface.
type UserRepository struct {
	db *Database
}

const (
	userColumns = `u.id, u.external_id, u.email, u.name, u.preferred_language, u.country, u.time_zone, COALESCE(u.safe_address, ''), u.is_active`

	homeColumns = `h.id, h.country_code, h.level_1, h.level_2, h.centroid_latitude, h.centroid_longitude`

	getUserQuery = `
		SELECT ` + userColumns + `, ` + homeColumns + `
		FROM users u
		LEFT JOIN homes h ON u.home_id = h.id
		WHERE u.id = $1
	`

	getUserByExternalIDQuery = `
		SELECT ` + userColumns + `, ` + homeColumns + `
		FROM users u
		LEFT JOIN homes h ON u.home_id = h.id
		WHERE u.external_id = $1
	`

	getUserByEmailQuery = `
		SELECT ` + userColumns + `, ` + homeColumns + `
		FROM users u
		LEFT JOIN homes h ON u.home_id = h.id
		WHERE u.email = $1
	`

	// updateUserQuery intentionally does NOT include preferred_language.
	// The column is owned by UpdatePreferredLanguage; a generic Update
	// would otherwise risk silently NULLing the row whenever a caller
	// passes a NewUser with an empty PreferredLanguage field (e.g. a
	// partial update built by a caller that forgot to copy the existing
	// value first).
	updateUserQuery = `
		WITH updated AS (
			UPDATE users SET external_id = $2, email = $3, name = $4, country = $5, time_zone = $6
			WHERE id = $1
			RETURNING *
		)
		SELECT ` + userColumns + `, ` + homeColumns + `
		FROM updated u
		LEFT JOIN homes h ON u.home_id = h.id
	`

	listUsersQuery = `
		SELECT ` + userColumns + `, ` + homeColumns + `
		FROM users u
		LEFT JOIN homes h ON u.home_id = h.id
		ORDER BY u.id
		LIMIT $1 OFFSET $2
	`

	updateSafeAddressQuery = `
		UPDATE users SET safe_address = $2 WHERE id = $1
	`

	// Atomic UPDATE + SELECT in a single statement so the read-after-write
	// can't race with a concurrent DELETE on the same user. The CTE
	// returns 0 rows if no row matches the WHERE, which scanUser surfaces
	// as ErrNoRows and the caller maps to NotFound.
	updatePreferredLanguageQuery = `
		WITH updated AS (
			UPDATE users SET preferred_language = $2
			WHERE id = $1
			RETURNING *
		)
		SELECT ` + userColumns + `, ` + homeColumns + `
		FROM updated u
		LEFT JOIN homes h ON u.home_id = h.id
	`

	insertHomeQuery = `
		INSERT INTO homes (id, country_code, level_1, level_2, centroid_latitude, centroid_longitude)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`

	updateHomeQuery = `
		UPDATE homes SET
			country_code = $2,
			level_1 = $3,
			level_2 = $4,
			centroid_latitude = $5,
			centroid_longitude = $6
		WHERE id = $1
		RETURNING id
	`

	setUserHomeIDQuery = `
		UPDATE users SET home_id = $2 WHERE id = $1
	`

	insertUserQuery = `
		INSERT INTO users (id, external_id, email, name, preferred_language, country, time_zone, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	deleteUserQuery = `
		DELETE FROM users WHERE id = $1
	`
)

// NewUserRepository creates a new user repository instance.
func NewUserRepository(db *Database) *UserRepository {
	return &UserRepository{db: db}
}

// nullStringFromEmpty maps Go's zero value (empty string) to a NULL
// SQL value, and any non-empty string to a present value. Used at the
// write boundary so columns whose semantics distinguish "absent" from
// "explicitly empty" (e.g. users.preferred_language) preserve that
// distinction in the database.
func nullStringFromEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}

// scanUser scans a user row with optional home columns from a LEFT JOIN.
func scanUser(scanner interface{ Scan(dest ...any) error }) (*entity.User, error) {
	user := &entity.User{}
	var preferredLanguage sql.NullString
	var homeID, countryCode, level1, level2 sql.NullString
	var centroidLat, centroidLng sql.NullFloat64

	err := scanner.Scan(
		&user.ID, &user.ExternalID, &user.Email, &user.Name,
		&preferredLanguage, &user.Country, &user.TimeZone,
		&user.SafeAddress, &user.IsActive,
		&homeID, &countryCode, &level1, &level2, &centroidLat, &centroidLng,
	)
	if err != nil {
		return nil, err
	}
	if preferredLanguage.Valid {
		user.PreferredLanguage = preferredLanguage.String
	}

	if homeID.Valid {
		user.Home = &entity.Home{
			ID:          homeID.String,
			CountryCode: countryCode.String,
			Level1:      level1.String,
		}
		if centroidLat.Valid && centroidLng.Valid {
			user.Home.Centroid = &entity.Coordinates{
				Latitude:  centroidLat.Float64,
				Longitude: centroidLng.Float64,
			}
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

	// Create returns the in-memory entity built from params (not a row
	// re-fetched after INSERT). This is intentional and currently safe:
	// every column we write here round-trips identically through pgx (no
	// triggers, generated columns, or server-side normalization on
	// users.* writes). If that ever changes — e.g., a DB-side normalizer
	// is added to preferred_language, or a trigger rewrites name — switch
	// Create to the CTE RETURNING + scanUser pattern that Update,
	// UpdateHome, and UpdatePreferredLanguage already use, so the caller
	// observes DB truth instead of a stale snapshot of params.
	user := entity.CreateUser(params)

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin transaction")
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Preserve the NULL = "client has not yet asserted a preference"
	// invariant on insert. Old clients that pre-date the
	// CreateRequest.preferred_language field send no value, which arrives
	// here as an empty string; mapping that to sql.NullString{Valid:false}
	// keeps such rows distinguishable from "client explicitly chose 'en'"
	// and feeds them into the same hydration-backfill path that legacy
	// rows already use.
	_, err = tx.Exec(ctx, insertUserQuery,
		user.ID, params.ExternalID, params.Email, params.Name,
		nullStringFromEmpty(params.PreferredLanguage),
		params.Country, params.TimeZone, true,
	)
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
		home = entity.NewHome(params.Home.CountryCode, params.Home.Level1, params.Home.Level2)
		var centroidLat, centroidLng *float64
		if c, ok := infrageo.ResolveCentroid(params.Home.Level1); ok {
			centroidLat = &c.Latitude
			centroidLng = &c.Longitude
			home.Centroid = &entity.Coordinates{
				Latitude:  c.Latitude,
				Longitude: c.Longitude,
			}
		}

		var homeID string
		err = tx.QueryRow(ctx, insertHomeQuery,
			home.ID, home.CountryCode, home.Level1, home.Level2,
			centroidLat, centroidLng,
		).Scan(&homeID)
		if err != nil {
			return nil, toAppErr(err, "failed to create home", slog.String("user_id", user.ID))
		}
		home.ID = homeID

		_, err = tx.Exec(ctx, setUserHomeIDQuery, user.ID, homeID)
		if err != nil {
			return nil, toAppErr(err, "failed to set user home_id", slog.String("user_id", user.ID))
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit transaction")
	}

	r.db.logger.Info(ctx, "user created",
		slog.String("entityType", "user"),
		slog.String("userID", user.ID),
	)

	user.Home = home
	return user, nil
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

	// Note: preferred_language is intentionally omitted — see the
	// updateUserQuery comment. Use UpdatePreferredLanguage instead.
	user, err := scanUser(r.db.Pool.QueryRow(ctx, updateUserQuery,
		id, params.ExternalID, params.Email, params.Name,
		params.Country, params.TimeZone,
	))
	if err != nil {
		// CTE RETURNING returns 0 rows when no row matches the WHERE.
		// QueryRow surfaces that as pgx.ErrNoRows; map to NotFound with
		// a clear message instead of the generic toAppErr "failed to
		// update user" (which reads like a query execution failure).
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.Wrap(apperr.ErrNotFound, codes.NotFound, fmt.Sprintf("user with ID %s not found", id))
		}
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

// UpdatePreferredLanguage sets the user's preferred display language.
// It performs a focused UPDATE on the preferred_language column and returns
// the refreshed user entity via the standard SELECT query.
func (r *UserRepository) UpdatePreferredLanguage(ctx context.Context, id, lang string) (*entity.User, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}
	// UpdatePreferredLanguage's RPC contract requires a non-empty
	// ISO 639-1 code (protovalidate enforces min_len: 2 + pattern). An
	// empty value reaching the repository is a programmer error; reject
	// loudly rather than write a NULL that downstream code would
	// misinterpret as "client has not yet asserted a preference".
	if lang == "" {
		return nil, apperr.New(codes.InvalidArgument, "preferred language cannot be empty")
	}

	// Atomic UPDATE + SELECT via CTE — no race window with a concurrent
	// DELETE on this user. If no row matches, scanUser returns ErrNoRows
	// which we wrap as NotFound.
	//
	// Belt-and-suspenders: pass via nullStringFromEmpty so that IF the
	// `lang == ""` guard above ever gets removed by a future refactor,
	// the query writes SQL NULL (preserving the "client has not yet
	// asserted" invariant and inviting a backfill on next hydration)
	// rather than an empty string (which would silently escape the
	// IS NULL invariant). The guard returning InvalidArgument is the
	// intended contract — this is the failure-mode safety net.
	user, err := scanUser(r.db.Pool.QueryRow(ctx, updatePreferredLanguageQuery, id, nullStringFromEmpty(lang)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.Wrap(apperr.ErrNotFound, codes.NotFound, fmt.Sprintf("user with ID %s not found", id))
		}
		return nil, toAppErr(err, "failed to update preferred language", slog.String("user_id", id))
	}

	r.db.logger.Info(ctx, "user updated",
		slog.String("entityType", "user"),
		slog.String("userID", id),
		slog.String("field", "preferredLanguage"),
	)

	return user, nil
}

// UpdateHome sets or changes the user's home area.
// If the user already has a home record it is updated in place; otherwise a new
// home record is inserted and linked to the user via users.home_id.
func (r *UserRepository) UpdateHome(ctx context.Context, id string, home *entity.Home) (*entity.User, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin transaction")
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Fetch current user to determine whether a home record already exists.
	current, err := scanUser(tx.QueryRow(ctx, getUserQuery, id))
	if err != nil {
		return nil, toAppErr(err, "failed to get user for home update", slog.String("user_id", id))
	}

	var centroidLat, centroidLng *float64
	if c, ok := infrageo.ResolveCentroid(home.Level1); ok {
		centroidLat = &c.Latitude
		centroidLng = &c.Longitude
	}

	if current.Home != nil {
		// Update the existing home record.
		_, err = tx.Exec(ctx, updateHomeQuery,
			current.Home.ID, home.CountryCode, home.Level1, home.Level2,
			centroidLat, centroidLng,
		)
		if err != nil {
			return nil, toAppErr(err, "failed to update home", slog.String("user_id", id))
		}
	} else {
		// Insert a new home record and link it to the user.
		newHome := entity.NewHome(home.CountryCode, home.Level1, home.Level2)
		var homeID string
		err = tx.QueryRow(ctx, insertHomeQuery,
			newHome.ID, newHome.CountryCode, newHome.Level1, newHome.Level2,
			centroidLat, centroidLng,
		).Scan(&homeID)
		if err != nil {
			return nil, toAppErr(err, "failed to insert home", slog.String("user_id", id))
		}

		_, err = tx.Exec(ctx, setUserHomeIDQuery, id, homeID)
		if err != nil {
			return nil, toAppErr(err, "failed to set user home_id", slog.String("user_id", id))
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, toAppErr(err, "failed to commit transaction")
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
