package rdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// FollowRepository implements entity.FollowRepository interface for PostgreSQL.
type FollowRepository struct {
	db     *Database
	outbox Announcer
}

const (
	// followInsertQuery passes the hype column explicitly so the Go domain
	// constant entity.DefaultHype is the source-of-truth on the write path
	// rather than the DB column DEFAULT. The DB DEFAULT remains as a safety
	// net for any future code path that omits the column.
	followInsertQuery = `
		INSERT INTO followed_artists (user_id, artist_id, hype)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`
	// followDeleteQuery removes the follow and returns its hype level, so the
	// removal is announced with the Follow as it was; no row when the user
	// does not follow the artist.
	followDeleteQuery = `
		DELETE FROM followed_artists
		WHERE user_id = $1 AND artist_id = $2
		RETURNING hype
	`
	// followSetHypeQuery changes the hype level only when it differs, so a
	// repeated level changes no row.
	followSetHypeQuery = `
		UPDATE followed_artists
		SET hype = $3
		WHERE user_id = $1 AND artist_id = $2 AND hype <> $3
	`
	followExistsQuery = `
		SELECT EXISTS (SELECT 1 FROM followed_artists WHERE user_id = $1 AND artist_id = $2)
	`
	followListByUserQuery = `
		SELECT a.id, a.name, COALESCE(a.mbid, ''), a.fanart, fa.hype
		FROM artists a
		JOIN followed_artists fa ON a.id = fa.artist_id
		WHERE fa.user_id = $1
	`
	followListAllQuery = `
		SELECT DISTINCT a.id, a.name, COALESCE(a.mbid, '')
		FROM artists a
		JOIN followed_artists fa ON a.id = fa.artist_id
	`
	followListFollowersQuery = `
		SELECT fa.user_id, fa.hype, COALESCE(h.level_1, ''), h.centroid_latitude, h.centroid_longitude, COALESCE(u.preferred_language, '')
		FROM followed_artists fa
		JOIN users u ON u.id = fa.user_id
		LEFT JOIN homes h ON h.id = u.home_id
		WHERE fa.artist_id = $1
	`
)

// NewFollowRepository creates a new follow repository instance that records
// its announcements through outbox.
func NewFollowRepository(db *Database, outbox Announcer) *FollowRepository {
	return &FollowRepository{db: db, outbox: outbox}
}

// Follow establishes a follow relationship between a user and an artist at
// entity.DefaultHype, the canonical domain-layer default (the DB column
// DEFAULT mirrors it but is not relied upon here). The insert uses ON
// CONFLICT DO NOTHING so a duplicate follow never touches the existing row;
// its rows affected tell a fresh insert from a duplicate, which fails with
// AlreadyExists.
//
// # Announces
//
// ARTIST.followed with the new Follow, in the transaction of the insert.
// Nothing on AlreadyExists or any other failure.
func (r *FollowRepository) Follow(ctx context.Context, userID, artistID string) error {
	attrs := []slog.Attr{slog.String("user_id", userID), slog.String("artist_id", artistID)}
	return r.inTx(ctx, "follow artist", attrs, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, followInsertQuery, userID, artistID, string(entity.DefaultHype))
		if err != nil {
			return toAppErr(err, "failed to follow artist", attrs...)
		}
		if tag.RowsAffected() == 0 {
			return apperr.New(codes.AlreadyExists, "already following")
		}
		follow := &entity.Follow{UserID: userID, ArtistID: artistID, Hype: entity.DefaultHype}
		return announce(ctx, r.outbox, tx, entity.SubjectArtistFollowed, messaging.Announcement{Payload: pb.FromFollow(follow)})
	})
}

// Unfollow removes a follow relationship. Removing one that does not exist
// succeeds.
//
// # Announces
//
// ARTIST.unfollowed with the Follow as it was, in the transaction of the
// removal. Nothing when the user did not follow the artist.
func (r *FollowRepository) Unfollow(ctx context.Context, userID, artistID string) error {
	attrs := []slog.Attr{slog.String("user_id", userID), slog.String("artist_id", artistID)}
	return r.inTx(ctx, "unfollow artist", attrs, func(tx pgx.Tx) error {
		var hype string
		err := tx.QueryRow(ctx, followDeleteQuery, userID, artistID).Scan(&hype)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return toAppErr(err, "failed to unfollow artist", attrs...)
		}
		follow := &entity.Follow{UserID: userID, ArtistID: artistID, Hype: entity.Hype(hype)}
		return announce(ctx, r.outbox, tx, entity.SubjectArtistUnfollowed, messaging.Announcement{Payload: pb.FromFollow(follow)})
	})
}

// SetHype sets the enthusiasm tier for a followed artist. Setting the level
// it already has changes nothing; a user who does not follow the artist
// fails with NotFound.
//
// # Announces
//
// ARTIST.hype_changed with the Follow at its new level, in the transaction of
// the change. Nothing when the level is unchanged or on failure.
func (r *FollowRepository) SetHype(ctx context.Context, userID, artistID string, hype entity.Hype) error {
	attrs := []slog.Attr{slog.String("user_id", userID), slog.String("artist_id", artistID), slog.String("hype", string(hype))}
	return r.inTx(ctx, "set hype", attrs, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, followSetHypeQuery, userID, artistID, string(hype))
		if err != nil {
			return toAppErr(err, "failed to set hype", attrs...)
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			if err := tx.QueryRow(ctx, followExistsQuery, userID, artistID).Scan(&exists); err != nil {
				return toAppErr(err, "failed to read the follow", attrs...)
			}
			if !exists {
				return apperr.New(codes.NotFound, "follow relationship not found", attrs...)
			}
			return nil
		}
		follow := &entity.Follow{UserID: userID, ArtistID: artistID, Hype: hype}
		return announce(ctx, r.outbox, tx, entity.SubjectArtistHypeChanged, messaging.Announcement{Payload: pb.FromFollow(follow)})
	})
}

// inTx runs fn in one transaction, committed when fn succeeds.
func (r *FollowRepository) inTx(ctx context.Context, op string, attrs []slog.Attr, fn func(pgx.Tx) error) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return toAppErr(err, "failed to begin "+op+" transaction", attrs...)
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return toAppErr(err, "failed to commit "+op+" transaction", attrs...)
	}
	return nil
}

// ListByUser retrieves the list of artists followed by a user, including hype level.
func (r *FollowRepository) ListByUser(ctx context.Context, userID string) ([]*entity.FollowedArtist, error) {
	rows, err := r.db.Pool.Query(ctx, followListByUserQuery, userID)
	if err != nil {
		return nil, toAppErr(err, "failed to list followed artists", slog.String("user_id", userID))
	}
	defer rows.Close()

	var followed []*entity.FollowedArtist
	for rows.Next() {
		var a entity.Artist
		var fanartJSON []byte
		var hype string
		if err := rows.Scan(&a.ID, &a.Name, &a.MBID, &fanartJSON, &hype); err != nil {
			return nil, toAppErr(err, "failed to scan followed artist")
		}
		if len(fanartJSON) > 0 {
			var f entity.Fanart
			if err := json.Unmarshal(fanartJSON, &f); err != nil {
				return nil, toAppErr(err, "failed to unmarshal fanart JSON")
			}
			a.Fanart = &f
		}
		followed = append(followed, &entity.FollowedArtist{
			UserID: userID,
			Artist: &a,
			Hype:   entity.Hype(hype),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating followed artist rows")
	}
	return followed, nil
}

// ListAll retrieves all distinct artists followed by any user.
func (r *FollowRepository) ListAll(ctx context.Context) ([]*entity.Artist, error) {
	rows, err := r.db.Pool.Query(ctx, followListAllQuery)
	if err != nil {
		return nil, toAppErr(err, "failed to list all followed artists")
	}
	defer rows.Close()

	var artists []*entity.Artist
	for rows.Next() {
		var a entity.Artist
		if err := rows.Scan(&a.ID, &a.Name, &a.MBID); err != nil {
			return nil, toAppErr(err, "failed to scan followed artist")
		}
		artists = append(artists, &a)
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating all followed artist rows")
	}
	return artists, nil
}

// ListFollowers retrieves all followers of an artist with their hype level and home area.
// User entities are partially populated with ID, Home (including the home area's
// Centroid, needed by Hype.MatchingConcerts to evaluate ProximityNearby), and
// PreferredLanguage for notification filtering and copy localization.
func (r *FollowRepository) ListFollowers(ctx context.Context, artistID string) ([]*entity.Follower, error) {
	rows, err := r.db.Pool.Query(ctx, followListFollowersQuery, artistID)
	if err != nil {
		return nil, toAppErr(err, "failed to list followers", slog.String("artist_id", artistID))
	}
	defer rows.Close()

	var followers []*entity.Follower
	for rows.Next() {
		var userID, hype, homeLevel1, prefLang string
		var centroidLat, centroidLng sql.NullFloat64
		if err := rows.Scan(&userID, &hype, &homeLevel1, &centroidLat, &centroidLng, &prefLang); err != nil {
			return nil, toAppErr(err, "failed to scan follower row")
		}
		user := &entity.User{ID: userID, PreferredLanguage: prefLang}
		if homeLevel1 != "" {
			user.Home = &entity.Home{Level1: homeLevel1}
			if centroidLat.Valid && centroidLng.Valid {
				user.Home.Centroid = &entity.Coordinates{
					Latitude:  centroidLat.Float64,
					Longitude: centroidLng.Float64,
				}
			}
		}
		followers = append(followers, &entity.Follower{
			ArtistID: artistID,
			User:     user,
			Hype:     entity.Hype(hype),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, toAppErr(err, "error iterating follower rows")
	}
	return followers, nil
}
