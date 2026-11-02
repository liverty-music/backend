package rdb_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	guuid "github.com/google/uuid"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

// seedFan inserts a user and an artist of their own for one test, and
// returns their ids.
func seedFan(t *testing.T) (userID, artistID string) {
	t.Helper()
	tag := guuid.NewString()
	userID = seedUser(t, "Fan "+tag, tag+"@example.com", "ext-"+tag)
	artistID = seedArtist(t, "Artist "+tag, guuid.NewString())
	return userID, artistID
}

// followsOf returns the Follow announcements of subject recorded after mark
// for the user.
func followsOf(t *testing.T, mark int64, subject, userID string) []*entityv1.Follow {
	t.Helper()
	return announcedSince(t, mark, subject, func() *entityv1.Follow { return &entityv1.Follow{} },
		func(f *entityv1.Follow) bool { return f.GetUserId().GetValue() == userID })
}

// storedHype returns the user's hype level for the artist, or "" when the
// user does not follow it.
func storedHype(t *testing.T, userID, artistID string) string {
	t.Helper()
	var hype string
	err := testDB.Pool.QueryRow(context.Background(),
		`SELECT COALESCE((SELECT hype FROM followed_artists WHERE user_id = $1 AND artist_id = $2), '')`,
		userID, artistID).Scan(&hype)
	require.NoError(t, err)
	return hype
}

func TestFollowRepository_FollowAnnounces(t *testing.T) {
	ctx := context.Background()
	repo := rdb.NewFollowRepository(testDB, testOutbox)

	t.Run("first follow announced", func(t *testing.T) {
		// @spec components/entity/follow/follow "First follow announced"
		userID, artistID := seedFan(t)
		mark := outboxMark(t)

		require.NoError(t, repo.Follow(ctx, userID, artistID))

		got := followsOf(t, mark, entity.SubjectArtistFollowed, userID)
		require.Len(t, got, 1)
		assert.Equal(t, artistID, got[0].GetArtistId().GetValue())
		assert.Equal(t, entityv1.HypeType_HYPE_TYPE_NEARBY, got[0].GetHype())
	})

	t.Run("repeat follow", func(t *testing.T) {
		// @spec components/entity/follow/follow "Repeat follow"
		// @spec components/infrastructure/backend/process/event-delivery "Change already made"
		userID, artistID := seedFan(t)
		require.NoError(t, repo.Follow(ctx, userID, artistID))
		before := storedHype(t, userID, artistID)
		mark := outboxMark(t)

		assert.ErrorIs(t, repo.Follow(ctx, userID, artistID), apperr.ErrAlreadyExists)
		assert.Empty(t, followsOf(t, mark, entity.SubjectArtistFollowed, userID))
		assert.Equal(t, before, storedHype(t, userID, artistID), "the stored Follow is unchanged")
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/follow/follow "Announcement cannot be recorded"
		// @spec components/infrastructure/backend/process/event-delivery "Announcement cannot be recorded"
		userID, artistID := seedFan(t)

		err := rdb.NewFollowRepository(testDB, failingOutbox{}).Follow(ctx, userID, artistID)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Empty(t, storedHype(t, userID, artistID), "the fan still does not follow the artist")
	})
}

func TestFollowRepository_UnfollowAnnounces(t *testing.T) {
	ctx := context.Background()
	repo := rdb.NewFollowRepository(testDB, testOutbox)

	t.Run("followed artist unfollowed", func(t *testing.T) {
		// @spec components/entity/follow/unfollow "Followed artist unfollowed"
		userID, artistID := seedFan(t)
		require.NoError(t, repo.Follow(ctx, userID, artistID))
		require.NoError(t, repo.SetHype(ctx, userID, artistID, entity.HypeAway))
		mark := outboxMark(t)

		require.NoError(t, repo.Unfollow(ctx, userID, artistID))

		got := followsOf(t, mark, entity.SubjectArtistUnfollowed, userID)
		require.Len(t, got, 1)
		assert.Equal(t, entityv1.HypeType_HYPE_TYPE_AWAY, got[0].GetHype())
		assert.Empty(t, storedHype(t, userID, artistID))
	})

	t.Run("artist not followed", func(t *testing.T) {
		// @spec components/entity/follow/unfollow "Artist not followed"
		userID, artistID := seedFan(t)
		mark := outboxMark(t)

		require.NoError(t, repo.Unfollow(ctx, userID, artistID))
		assert.Empty(t, followsOf(t, mark, entity.SubjectArtistUnfollowed, userID))
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/follow/unfollow "Announcement cannot be recorded"
		userID, artistID := seedFan(t)
		require.NoError(t, repo.Follow(ctx, userID, artistID))

		err := rdb.NewFollowRepository(testDB, failingOutbox{}).Unfollow(ctx, userID, artistID)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Equal(t, string(entity.DefaultHype), storedHype(t, userID, artistID), "the Follow remains")
	})
}

func TestFollowRepository_SetHypeAnnounces(t *testing.T) {
	ctx := context.Background()
	repo := rdb.NewFollowRepository(testDB, testOutbox)

	t.Run("change the level", func(t *testing.T) {
		// @spec components/entity/follow/set-hype "Change the level"
		userID, artistID := seedFan(t)
		require.NoError(t, repo.Follow(ctx, userID, artistID))

		require.NoError(t, repo.SetHype(ctx, userID, artistID, entity.HypeAway))
		assert.Equal(t, string(entity.HypeAway), storedHype(t, userID, artistID))
	})

	t.Run("level changed", func(t *testing.T) {
		// @spec components/entity/follow/set-hype "Level changed"
		userID, artistID := seedFan(t)
		require.NoError(t, repo.Follow(ctx, userID, artistID))
		require.NoError(t, repo.SetHype(ctx, userID, artistID, entity.HypeWatch))
		mark := outboxMark(t)

		require.NoError(t, repo.SetHype(ctx, userID, artistID, entity.HypeAway))

		got := followsOf(t, mark, entity.SubjectArtistHypeChanged, userID)
		require.Len(t, got, 1)
		assert.Equal(t, entityv1.HypeType_HYPE_TYPE_AWAY, got[0].GetHype())
	})

	t.Run("same level again", func(t *testing.T) {
		// @spec components/entity/follow/set-hype "Same level again"
		userID, artistID := seedFan(t)
		require.NoError(t, repo.Follow(ctx, userID, artistID))
		require.NoError(t, repo.SetHype(ctx, userID, artistID, entity.HypeHome))
		mark := outboxMark(t)

		require.NoError(t, repo.SetHype(ctx, userID, artistID, entity.HypeHome))

		assert.Equal(t, string(entity.HypeHome), storedHype(t, userID, artistID))
		assert.Empty(t, followsOf(t, mark, entity.SubjectArtistHypeChanged, userID))
	})

	t.Run("artist not followed", func(t *testing.T) {
		// @spec components/entity/follow/set-hype "Artist not followed"
		userID, artistID := seedFan(t)

		assert.ErrorIs(t, repo.SetHype(ctx, userID, artistID, entity.HypeAway), apperr.ErrNotFound)
		assert.Empty(t, storedHype(t, userID, artistID), "no Follow is created")
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/follow/set-hype "Announcement cannot be recorded"
		userID, artistID := seedFan(t)
		require.NoError(t, repo.Follow(ctx, userID, artistID))
		require.NoError(t, repo.SetHype(ctx, userID, artistID, entity.HypeWatch))

		err := rdb.NewFollowRepository(testDB, failingOutbox{}).SetHype(ctx, userID, artistID, entity.HypeAway)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Equal(t, string(entity.HypeWatch), storedHype(t, userID, artistID), "the level stays Watch")
	})
}

// storedSetBy returns who set the user's hype level for the artist, or ""
// when the user does not follow it.
func storedSetBy(t *testing.T, userID, artistID string) entity.HypeSetBy {
	t.Helper()
	var setBy string
	err := testDB.Pool.QueryRow(context.Background(),
		`SELECT COALESCE((SELECT hype_set_by FROM followed_artists WHERE user_id = $1 AND artist_id = $2), '')`,
		userID, artistID).Scan(&setBy)
	require.NoError(t, err)
	return entity.HypeSetBy(setBy)
}

func TestFollowRepository_HypeSetBy(t *testing.T) {
	ctx := context.Background()
	repo := rdb.NewFollowRepository(testDB, testOutbox)

	t.Run("first follow is set by default", func(t *testing.T) {
		// @spec components/entity/follow/follow "First follow"
		userID, artistID := seedFan(t)
		mark := outboxMark(t)

		require.NoError(t, repo.Follow(ctx, userID, artistID))

		assert.Equal(t, string(entity.HypeNearby), storedHype(t, userID, artistID))
		assert.Equal(t, entity.HypeSetByDefault, storedSetBy(t, userID, artistID))
		got := followsOf(t, mark, entity.SubjectArtistFollowed, userID)
		require.Len(t, got, 1)
		assert.Equal(t, entityv1.HypeSetBy_HYPE_SET_BY_DEFAULT, got[0].GetHypeSetBy())
	})

	t.Run("repeat follow keeps who set the level", func(t *testing.T) {
		// @spec components/entity/follow/follow "Repeat follow"
		userID, artistID := seedFan(t)
		require.NoError(t, repo.Follow(ctx, userID, artistID))
		require.NoError(t, repo.SetHype(ctx, userID, artistID, entity.HypeAway))

		assert.ErrorIs(t, repo.Follow(ctx, userID, artistID), apperr.ErrAlreadyExists)
		assert.Equal(t, string(entity.HypeAway), storedHype(t, userID, artistID))
		assert.Equal(t, entity.HypeSetByFan, storedSetBy(t, userID, artistID))
	})

	t.Run("set hype is set by the fan", func(t *testing.T) {
		// @spec components/entity/follow/set-hype "Change the level"
		// @spec components/entity/follow/set-hype "Level changed"
		userID, artistID := seedFan(t)
		require.NoError(t, repo.Follow(ctx, userID, artistID))
		mark := outboxMark(t)

		require.NoError(t, repo.SetHype(ctx, userID, artistID, entity.HypeAway))

		assert.Equal(t, entity.HypeSetByFan, storedSetBy(t, userID, artistID))
		got := followsOf(t, mark, entity.SubjectArtistHypeChanged, userID)
		require.Len(t, got, 1)
		assert.Equal(t, entityv1.HypeType_HYPE_TYPE_AWAY, got[0].GetHype())
		assert.Equal(t, entityv1.HypeSetBy_HYPE_SET_BY_FAN, got[0].GetHypeSetBy())
	})

	t.Run("fan picks the default level", func(t *testing.T) {
		// @spec components/entity/follow/set-hype "Fan picks the default level"
		userID, artistID := seedFan(t)
		require.NoError(t, repo.Follow(ctx, userID, artistID))
		mark := outboxMark(t)

		require.NoError(t, repo.SetHype(ctx, userID, artistID, entity.HypeNearby))

		assert.Equal(t, string(entity.HypeNearby), storedHype(t, userID, artistID))
		assert.Equal(t, entity.HypeSetByFan, storedSetBy(t, userID, artistID))
		got := followsOf(t, mark, entity.SubjectArtistHypeChanged, userID)
		require.Len(t, got, 1, "one outbox row")
		assert.Equal(t, entityv1.HypeType_HYPE_TYPE_NEARBY, got[0].GetHype())
		assert.Equal(t, entityv1.HypeSetBy_HYPE_SET_BY_FAN, got[0].GetHypeSetBy())
	})

	t.Run("list by user carries who set each level", func(t *testing.T) {
		// @spec components/entity/follow/list-by-user "Fan follows three artists"
		userID, watched := seedFan(t)
		defaulted := seedArtist(t, "Artist default "+userID, guuid.NewString())
		raised := seedArtist(t, "Artist raised "+userID, guuid.NewString())
		for _, a := range []string{watched, defaulted, raised} {
			require.NoError(t, repo.Follow(ctx, userID, a))
		}
		require.NoError(t, repo.SetHype(ctx, userID, watched, entity.HypeWatch))
		_, err := repo.RaiseHype(ctx, userID, raised, entity.HypeAway)
		require.NoError(t, err)

		got, err := repo.ListByUser(ctx, userID)

		require.NoError(t, err)
		byArtist := map[string]*entity.FollowedArtist{}
		for _, fa := range got {
			byArtist[fa.Artist.ID] = fa
		}
		require.Len(t, byArtist, 3)
		assert.Equal(t, entity.HypeWatch, byArtist[watched].Hype)
		assert.Equal(t, entity.HypeSetByFan, byArtist[watched].HypeSetBy)
		assert.Equal(t, entity.HypeNearby, byArtist[defaulted].Hype)
		assert.Equal(t, entity.HypeSetByDefault, byArtist[defaulted].HypeSetBy)
		assert.Equal(t, entity.HypeAway, byArtist[raised].Hype)
		assert.Equal(t, entity.HypeSetByInference, byArtist[raised].HypeSetBy)
	})
}

func TestFollowRepository_RaiseHype(t *testing.T) {
	ctx := context.Background()
	repo := rdb.NewFollowRepository(testDB, testOutbox)

	t.Run("raisable follow", func(t *testing.T) {
		// @spec components/entity/follow/raise-hype "Raisable Follow"
		// @spec components/entity/follow/raise-hype "Level raised"
		userID, artistID := seedFan(t)
		require.NoError(t, repo.Follow(ctx, userID, artistID))
		mark := outboxMark(t)

		raised, err := repo.RaiseHype(ctx, userID, artistID, entity.HypeAway)

		require.NoError(t, err)
		assert.True(t, raised)
		assert.Equal(t, string(entity.HypeAway), storedHype(t, userID, artistID))
		assert.Equal(t, entity.HypeSetByInference, storedSetBy(t, userID, artistID))
		got := followsOf(t, mark, entity.SubjectArtistHypeChanged, userID)
		require.Len(t, got, 1)
		assert.Equal(t, entityv1.HypeType_HYPE_TYPE_AWAY, got[0].GetHype())
		assert.Equal(t, entityv1.HypeSetBy_HYPE_SET_BY_INFERENCE, got[0].GetHypeSetBy())
	})

	t.Run("level the fan set", func(t *testing.T) {
		// @spec components/entity/follow/raise-hype "Level the fan set"
		userID, artistID := seedFan(t)
		require.NoError(t, repo.Follow(ctx, userID, artistID))
		require.NoError(t, repo.SetHype(ctx, userID, artistID, entity.HypeHome))
		mark := outboxMark(t)

		raised, err := repo.RaiseHype(ctx, userID, artistID, entity.HypeAway)

		require.NoError(t, err)
		assert.False(t, raised)
		assert.Equal(t, string(entity.HypeHome), storedHype(t, userID, artistID))
		assert.Equal(t, entity.HypeSetByFan, storedSetBy(t, userID, artistID))
		assert.Empty(t, followsOf(t, mark, entity.SubjectArtistHypeChanged, userID))
	})

	t.Run("fan sets the level at the same time", func(t *testing.T) {
		// @spec components/entity/follow/raise-hype "Fan sets the level at the same time"
		userID, artistID := seedFan(t)
		require.NoError(t, repo.Follow(ctx, userID, artistID))

		// The fan's transaction holds the row lock when RaiseHype starts, so
		// RaiseHype waits and then reads the fan's level.
		fanTx, err := testDB.Pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = fanTx.Rollback(ctx) }()
		_, err = fanTx.Exec(ctx, `UPDATE followed_artists SET hype = 'home', hype_set_by = 'fan' WHERE user_id = $1 AND artist_id = $2`, userID, artistID)
		require.NoError(t, err)

		done := make(chan error, 1)
		var raised bool
		go func() {
			var err error
			raised, err = repo.RaiseHype(ctx, userID, artistID, entity.HypeAway)
			done <- err
		}()
		require.NoError(t, fanTx.Commit(ctx))
		require.NoError(t, <-done)

		assert.False(t, raised)
		assert.Equal(t, string(entity.HypeHome), storedHype(t, userID, artistID))
		assert.Equal(t, entity.HypeSetByFan, storedSetBy(t, userID, artistID))
	})

	t.Run("artist not followed", func(t *testing.T) {
		// @spec components/entity/follow/raise-hype "Artist not followed"
		userID, artistID := seedFan(t)

		_, err := repo.RaiseHype(ctx, userID, artistID, entity.HypeAway)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/follow/raise-hype "Announcement cannot be recorded"
		userID, artistID := seedFan(t)
		require.NoError(t, repo.Follow(ctx, userID, artistID))

		_, err := rdb.NewFollowRepository(testDB, failingOutbox{}).RaiseHype(ctx, userID, artistID, entity.HypeAway)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Equal(t, string(entity.HypeNearby), storedHype(t, userID, artistID))
		assert.Equal(t, entity.HypeSetByDefault, storedSetBy(t, userID, artistID))
	})
}
