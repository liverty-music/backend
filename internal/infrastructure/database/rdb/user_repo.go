package rdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// UserRepository implements entity.UserRepository interface.
type UserRepository struct {
	db     *Database
	outbox Announcer
}

const (
	userColumns = `u.id, u.provider_user_id, u.email, u.name, u.preferred_language, u.time_zone, u.full_name, u.phone_number, u.last_login_time`

	// homeColumns are the user's home Area and, from the Area, its country
	// and reference point; homeJoin reaches them.
	homeColumns = `uh.area_code, ha.country_code, ha.reference_latitude, ha.reference_longitude`
	homeJoin    = `LEFT JOIN user_home_areas uh ON uh.user_id = u.id LEFT JOIN areas ha ON ha.code = uh.area_code`

	getUserQuery = `
		SELECT ` + userColumns + `, ` + homeColumns + `
		FROM users u
		` + homeJoin + `
		WHERE u.id = $1
	`

	// lockUserRowQuery locks the user's row until the transaction ends, so a
	// write that decides on what it reads next (UpdateHome: insert a Home or
	// update the stored one) runs after any concurrent one for the same user.
	// It is its own statement on purpose: the read that follows takes a new
	// snapshot and sees what the other transaction stored. FOR UPDATE on
	// getUserQuery itself would not: after the lock wait PostgreSQL re-reads
	// only the locked users row, and joins it to user_home_areas as of the statement's
	// start, where the Home just stored does not exist yet.
	lockUserRowQuery = `SELECT id FROM users WHERE id = $1 FOR UPDATE`

	getUserByProviderUserIDQuery = `
		SELECT ` + userColumns + `, ` + homeColumns + `
		FROM users u
		` + homeJoin + `
		WHERE u.provider_user_id = $1
	`

	// getUsersByEmailQuery matches the address with letter case ignored
	// (served by uq_users_email_lower, which also keeps two rows from
	// matching). LIMIT 2 still tells one match from several, so a breach of
	// that index would be reported instead of answered with the first row.
	getUsersByEmailQuery = `
		SELECT ` + userColumns + `, ` + homeColumns + `
		FROM users u
		` + homeJoin + `
		WHERE lower(u.email) = lower($1)
		LIMIT 2
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
		` + homeJoin + `
	`

	// updatePersonalDetailsQuery sets both personal details columns in one
	// atomic UPDATE + SELECT, like updatePreferredLanguageQuery. A NULL
	// parameter stores the value as absent.
	updatePersonalDetailsQuery = `
		WITH updated AS (
			UPDATE users SET full_name = $2, phone_number = $3
			WHERE id = $1
			RETURNING *
		)
		SELECT ` + userColumns + `, ` + homeColumns + `
		FROM updated u
		` + homeJoin + `
	`

	// upsertUserHomeQuery stores the user's home Area; the foreign key
	// refuses a code that is no first-level Area.
	upsertUserHomeQuery = `
		INSERT INTO user_home_areas (user_id, area_code) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET area_code = EXCLUDED.area_code
	`

	getHomeQuery = `
		SELECT a.code, a.country_code, a.reference_latitude, a.reference_longitude
		FROM areas a WHERE a.code = $1
	`

	insertUserQuery = `
		INSERT INTO users (id, provider_user_id, email, name, preferred_language, time_zone)
		VALUES ($1, $2, $3, $4, $5, $6)
	`

	// deleteUserQuery removes the user and records its identity as removed, in
	// one statement, so both happen or neither does.
	deleteUserQuery = `
		WITH removed AS (
			DELETE FROM users WHERE id = $1 RETURNING provider_user_id
		)
		INSERT INTO deleted_user_identities (provider_user_id)
		SELECT provider_user_id FROM removed
	`

	identityRemovedQuery = `SELECT EXISTS (SELECT 1 FROM deleted_user_identities WHERE provider_user_id = $1)`

	// userDeletionLockQuery and userCreateLockQuery take the user's
	// transaction-scoped advisory lock, exclusive for a deletion and shared
	// for a create of a record that blocks one; both are released at commit
	// or rollback, and when the session ends.
	userDeletionLockQuery = `SELECT pg_advisory_xact_lock(hashtextextended('user:' || $1, 0))`
	userCreateLockQuery   = `SELECT pg_advisory_xact_lock_shared(hashtextextended('user:' || $1, 0))`
	userExistsQuery       = `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`
	// recordLoginQuery moves the last login time forward only.
	recordLoginQuery = `
		UPDATE users SET last_login_time = $2
		WHERE id = $1 AND (last_login_time IS NULL OR last_login_time < $2)
	`
)

// NewUserRepository creates a new user repository instance.
func NewUserRepository(db *Database, outbox Announcer) *UserRepository {
	return &UserRepository{db: db, outbox: outbox}
}

// nullStringFromEmpty maps Go's zero value (empty string) to a NULL
// SQL value, and any non-empty string to a present value. Used at the
// write boundary so columns whose semantics distinguish "absent" from
// "explicitly empty" (e.g. users.time_zone) preserve that distinction in the
// database.
func nullStringFromEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}

// scanUser scans a user row with optional home columns from a LEFT JOIN.
//
// Every nullable users column (time_zone, full_name, phone_number) is scanned
// into a sql.NullString intermediate, then assigned to the entity field when
// .Valid: the time zone as its string, the full name and phone number as
// pointers that stay nil for NULL. pgx errors out when scanning SQL NULL into a
// non-nullable Go destination (a bare string), so the intermediate-local
// pattern protects every read path that shares this scanner (Get,
// GetByProviderUserID, GetByEmail, Update, UpdatePreferredLanguage,
// UpdateHome's re-fetch, List).
func scanUser(scanner interface{ Scan(dest ...any) error }) (*entity.User, error) {
	user := &entity.User{}
	var timeZone sql.NullString
	var fullName, phoneNumber sql.NullString
	var lastLoginAt sql.NullTime
	var homeArea, homeCountry sql.NullString
	var homeLat, homeLng sql.NullFloat64

	err := scanner.Scan(
		&user.ID, &user.ProviderUserID, &user.Email, &user.Name,
		&user.PreferredLanguage, &timeZone,
		&fullName, &phoneNumber, &lastLoginAt,
		&homeArea, &homeCountry, &homeLat, &homeLng,
	)
	if err != nil {
		return nil, err
	}
	if timeZone.Valid {
		user.TimeZone = timeZone.String
	}
	if fullName.Valid {
		user.FullName = &fullName.String
	}
	if phoneNumber.Valid {
		user.PhoneNumber = &phoneNumber.String
	}
	if lastLoginAt.Valid {
		t := lastLoginAt.Time
		user.LastLoginTime = &t
	}

	if homeArea.Valid {
		user.Home = homeOf(homeArea.String, homeCountry.String, homeLat, homeLng)
	}

	return user, nil
}

// homeOf builds a Home from its Area's code, country and reference point.
func homeOf(area, country string, lat, lng sql.NullFloat64) *entity.Home {
	home := &entity.Home{CountryCode: country, Level1: area}
	if lat.Valid && lng.Valid {
		home.ReferencePoint = &entity.Coordinates{Latitude: lat.Float64, Longitude: lng.Float64}
	}
	return home
}

// writeUserHome stores the Area as the user's home and returns the Home with
// its country and reference point read from the Area.
func writeUserHome(ctx context.Context, tx pgx.Tx, userID, area string) (*entity.Home, error) {
	if _, err := tx.Exec(ctx, upsertUserHomeQuery, userID, area); err != nil {
		if IsForeignKeyViolation(err) {
			return nil, apperr.Wrap(err, codes.InvalidArgument, "the home is no first-level Area", slog.String("area", area))
		}
		return nil, toAppErr(err, "failed to store home", slog.String("user_id", userID), slog.String("area", area))
	}
	var code, country string
	var lat, lng sql.NullFloat64
	if err := tx.QueryRow(ctx, getHomeQuery, area).Scan(&code, &country, &lat, &lng); err != nil {
		return nil, toAppErr(err, "failed to read home area", slog.String("area", area))
	}
	return homeOf(code, country, lat, lng), nil
}

// Create creates a new user in the database.
// If params.Home is non-nil, the home record is inserted atomically.
//
// # Announces
//
// USER.created with the User without its full name, phone number and display
// name, in the transaction of the insert. Nothing on AlreadyExists.
func (r *UserRepository) Create(ctx context.Context, params *entity.NewUser) (*entity.User, error) {
	if params == nil {
		return nil, apperr.New(codes.InvalidArgument, "params cannot be nil")
	}
	if params.PreferredLanguage == "" {
		return nil, apperr.New(codes.InvalidArgument, "preferred language is required")
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

	// Keep NULL = "never asserted" for time_zone: callers carry it as a Go
	// string, so an unfilled field arrives here as the empty string.
	_, err = tx.Exec(ctx, insertUserQuery,
		user.ID, params.ProviderUserID, params.Email, params.Name,
		params.PreferredLanguage,
		nullStringFromEmpty(params.TimeZone),
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

	// Read after the insert: an insert that meets the uncommitted removal of
	// the same identity waits for it on the unique index, so this read, a new
	// statement of the transaction, sees its record.
	var removed bool
	if err := tx.QueryRow(ctx, identityRemovedQuery, params.ProviderUserID).Scan(&removed); err != nil {
		return nil, toAppErr(err, "failed to check the identity was not removed")
	}
	if removed {
		return nil, apperr.New(codes.FailedPrecondition, "the account of this identity was removed",
			slog.String("provider_user_id", params.ProviderUserID))
	}

	var home *entity.Home
	if params.Home != nil {
		if home, err = writeUserHome(ctx, tx, user.ID, params.Home.Level1); err != nil {
			return nil, err
		}
	}

	user.Home = home
	if err := announce(ctx, r.outbox, tx, entity.SubjectUserCreated, user); err != nil {
		return nil, err
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

// RecordLogin sets the user's last login time to at when it has none or an
// earlier one.
//
// # Announces
//
// USER.signed_in with the User and its new last login time, without its full
// name, phone number and display name, in the transaction of the change.
// Nothing when the stored time is the same or later.
func (r *UserRepository) RecordLogin(ctx context.Context, id string, at time.Time) error {
	attrs := []slog.Attr{slog.String("user_id", id)}
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin login record", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, recordLoginQuery, id, at)
	if err != nil {
		return toAppErr(err, "failed to record login", attrs...)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := tx.QueryRow(ctx, userExistsQuery, id).Scan(&exists); err != nil {
			return toAppErr(err, "failed to read the user", attrs...)
		}
		if !exists {
			return apperr.New(codes.NotFound, "user not found", attrs...)
		}
		return nil
	}
	user, err := scanUser(tx.QueryRow(ctx, getUserQuery, id))
	if err != nil {
		return toAppErr(err, "failed to read the signed-in user", attrs...)
	}
	if err := announce(ctx, r.outbox, tx, entity.SubjectUserSignedIn, user); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit login record", attrs...)
	}
	return nil
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

// GetByProviderUserID retrieves a user by identity provider ID from the database.
func (r *UserRepository) GetByProviderUserID(ctx context.Context, providerUserID string) (*entity.User, error) {
	if providerUserID == "" {
		return nil, apperr.New(codes.InvalidArgument, "provider user ID cannot be empty")
	}

	user, err := scanUser(r.db.Pool.QueryRow(ctx, getUserByProviderUserIDQuery, providerUserID))
	if err != nil {
		return nil, toAppErr(err, "failed to get user by provider user ID", slog.String("provider_user_id", providerUserID))
	}

	return user, nil
}

// GetByEmail retrieves the user whose email equals email when letter case is
// ignored. More than one match fails with Internal rather than picking one.
func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*entity.User, error) {
	if email == "" {
		return nil, apperr.New(codes.InvalidArgument, "email cannot be empty")
	}

	rows, err := r.db.Pool.Query(ctx, getUsersByEmailQuery, email)
	if err != nil {
		return nil, toAppErr(err, "failed to get user by email")
	}
	defer rows.Close()

	var users []*entity.User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, toAppErr(err, "failed to scan user by email")
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "failed to get user by email")
	}

	switch len(users) {
	case 0:
		return nil, apperr.New(codes.NotFound, "no user has the email")
	case 1:
		return users[0], nil
	default:
		return nil, apperr.New(codes.Internal, "more than one user has the email when letter case is ignored",
			slog.String("user_id", users[0].ID), slog.String("other_user_id", users[1].ID))
	}
}

// UpdatePersonalDetails sets the user's full name and phone number, storing
// an empty value as absent, and returns the refreshed user.
func (r *UserRepository) UpdatePersonalDetails(ctx context.Context, id string, fullName, phoneNumber *string) (*entity.User, error) {
	if err := entity.ValidatePersonalDetails(fullName, phoneNumber); err != nil {
		return nil, err
	}
	// A nil pointer is sent as NULL: the value is stored as absent.
	user, err := scanUser(r.db.Pool.QueryRow(ctx, updatePersonalDetailsQuery, id, fullName, phoneNumber))
	if err != nil {
		return nil, toAppErr(err, "failed to update personal details", slog.String("user_id", id))
	}
	return user, nil
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

// UpdateHome sets or changes the user's home Area.
func (r *UserRepository) UpdateHome(ctx context.Context, id string, home *entity.Home) (*entity.User, error) {
	if id == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID cannot be empty")
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, toAppErr(err, "failed to begin transaction")
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Lock the user's row, so a user deleted meanwhile is NotFound here
	// rather than a foreign key failure of the insert.
	var lockedID string
	if err := tx.QueryRow(ctx, lockUserRowQuery, id).Scan(&lockedID); err != nil {
		return nil, toAppErr(err, "failed to lock user for home update", slog.String("user_id", id))
	}
	if _, err := writeUserHome(ctx, tx, id, home.Level1); err != nil {
		return nil, err
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

// SerializeDeletion runs fn while holding the user's deletion lock
// exclusively, in a transaction that fn's own reads and writes run on too:
// the context passed to fn is bound to it, so a deletion holds one connection,
// not one for the lock and one for each query of fn. The transaction commits
// when fn succeeds, which is what makes fn's writes stick, and rolls back when
// fn fails. fn must run its queries one at a time.
func (r *UserRepository) SerializeDeletion(ctx context.Context, id string, fn func(ctx context.Context) error) error {
	attrs := slog.String("user_id", id)
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin user deletion lock", attrs)
	}
	// Rollback releases the lock when fn fails; after a commit it is a no-op.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, userDeletionLockQuery, id); err != nil {
		return toAppErr(err, "failed to lock user deletion", attrs)
	}
	if err := fn(withBoundTx(ctx, tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit user deletion", attrs)
	}
	return nil
}

// lockUserForCreate holds the user's deletion lock shared for the rest of tx
// and confirms the user still exists, so a record created in tx never
// outlives a deletion of its user (UserRepository.SerializeDeletion).
func lockUserForCreate(ctx context.Context, tx pgx.Tx, userID entity.UserID) error {
	attrs := slog.String("user_id", string(userID))
	if _, err := tx.Exec(ctx, userCreateLockQuery, string(userID)); err != nil {
		return toAppErr(err, "failed to lock the user against deletion", attrs)
	}
	var exists bool
	if err := tx.QueryRow(ctx, userExistsQuery, string(userID)).Scan(&exists); err != nil {
		return toAppErr(err, "failed to check the user exists", attrs)
	}
	if !exists {
		return apperr.New(codes.NotFound, "the user does not exist", attrs)
	}
	return nil
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
