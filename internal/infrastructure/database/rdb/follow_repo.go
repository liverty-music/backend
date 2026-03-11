package rdb

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// FollowRepository implements entity.FollowRepository interface for PostgreSQL.
type FollowRepository struct {
	db *Database
}

const (
	followInsertQuery = `
		INSERT INTO followed_artists (user_id, artist_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`
	followDeleteQuery = `
		DELETE FROM followed_artists
		WHERE user_id = $1 AND artist_id = $2
	`
	followSetHypeQuery = `
		UPDATE followed_artists
		SET hype = $3
		WHERE user_id = $1 AND artist_id = $2
	`
	followListByUserQuery = `
		SELECT a.id, a.name, COALESCE(a.mbid, ''), fa.hype
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
		SELECT fa.user_id, fa.hype, COALESCE(h.level_1, '')
		FROM followed_artists fa
		JOIN users u ON u.id = fa.user_id
		LEFT JOIN homes h ON h.id = u.home_id
		WHERE fa.artist_id = $1
	`
)

// NewFollowRepository creates a new follow repository instance.
func NewFollowRepository(db *Database) *FollowRepository {
	return &FollowRepository{db: db}
}

// Follow establishes a follow relationship between a user and an artist.
func (r *FollowRepository) Follow(ctx context.Context, userID, artistID string) error {
	_, err := r.db.Pool.Exec(ctx, followInsertQuery, userID, artistID)
	if err != nil {
		return toAppErr(err, "failed to follow artist", slog.String("user_id", userID), slog.String("artist_id", artistID))
	}

	r.db.logger.Info(ctx, "artist followed",
		slog.String("entityType", "followed_artists"),
		slog.String("userID", userID),
		slog.String("artistID", artistID),
	)
	return nil
}

// Unfollow removes a follow relationship.
func (r *FollowRepository) Unfollow(ctx context.Context, userID, artistID string) error {
	_, err := r.db.Pool.Exec(ctx, followDeleteQuery, userID, artistID)
	if err != nil {
		return toAppErr(err, "failed to unfollow artist", slog.String("user_id", userID), slog.String("artist_id", artistID))
	}

	r.db.logger.Info(ctx, "artist unfollowed",
		slog.String("entityType", "followed_artists"),
		slog.String("userID", userID),
		slog.String("artistID", artistID),
	)
	return nil
}

// SetHype updates the enthusiasm tier for a followed artist.
func (r *FollowRepository) SetHype(ctx context.Context, userID, artistID string, hype entity.Hype) error {
	tag, err := r.db.Pool.Exec(ctx, followSetHypeQuery, userID, artistID, string(hype))
	if err != nil {
		return toAppErr(err, "failed to set hype", slog.String("user_id", userID), slog.String("artist_id", artistID))
	}
	if tag.RowsAffected() == 0 {
		return apperr.New(codes.NotFound, "follow relationship not found")
	}

	r.db.logger.Info(ctx, "hype updated",
		slog.String("entityType", "followed_artists"),
		slog.String("userID", userID),
		slog.String("artistID", artistID),
		slog.String("hype", string(hype)),
	)
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
		var hype string
		if err := rows.Scan(&a.ID, &a.Name, &a.MBID, &hype); err != nil {
			return nil, toAppErr(err, "failed to scan followed artist")
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
// User entities are partially populated with ID and Home for notification filtering.
func (r *FollowRepository) ListFollowers(ctx context.Context, artistID string) ([]*entity.Follower, error) {
	rows, err := r.db.Pool.Query(ctx, followListFollowersQuery, artistID)
	if err != nil {
		return nil, toAppErr(err, "failed to list followers", slog.String("artist_id", artistID))
	}
	defer rows.Close()

	var followers []*entity.Follower
	for rows.Next() {
		var userID, hype, homeLevel1 string
		if err := rows.Scan(&userID, &hype, &homeLevel1); err != nil {
			return nil, toAppErr(err, "failed to scan follower row")
		}
		user := &entity.User{ID: userID}
		if homeLevel1 != "" {
			user.Home = &entity.Home{Level1: homeLevel1}
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
