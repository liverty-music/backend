package rdb_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

// seedOrganizerInStatus inserts an Organizer in the given status and returns
// its id.
func seedOrganizerInStatus(t *testing.T, status entity.OrganizerStatus) string {
	t.Helper()
	id := seedOrganizer(t)
	_, err := testDB.Pool.Exec(context.Background(), `UPDATE organizers SET status = $2 WHERE id = $1`, id, int16(status))
	require.NoError(t, err)
	return id
}

func organizerStatus(t *testing.T, id string) entity.OrganizerStatus {
	t.Helper()
	var status int16
	require.NoError(t, testDB.Pool.QueryRow(context.Background(), `SELECT status FROM organizers WHERE id = $1`, id).Scan(&status))
	return entity.OrganizerStatus(status)
}

func TestOrganizerRepository_CompareAndSetStatusAnnounces(t *testing.T) {
	ctx := context.Background()
	repo := rdb.NewOrganizerRepository(testDB, testOutbox)
	createdOf := func(t *testing.T, mark int64, id string) []*entityv1.Organizer {
		t.Helper()
		return announcedSince(t, mark, entity.SubjectOrganizerCreated, func() *entityv1.Organizer { return &entityv1.Organizer{} },
			func(o *entityv1.Organizer) bool { return o.GetId().GetValue() == id })
	}

	t.Run("organizer activated", func(t *testing.T) {
		// @spec components/entity/organizer/compare-and-set-status "Organizer activated"
		id := seedOrganizerInStatus(t, entity.OrganizerStatusProvisioning)
		mark := outboxMark(t)

		changed, err := repo.CompareAndSetStatus(ctx, id, entity.OrganizerStatusProvisioning, entity.OrganizerStatusActive)

		require.NoError(t, err)
		assert.True(t, changed)
		got := createdOf(t, mark, id)
		require.Len(t, got, 1)
		assert.Equal(t, entityv1.OrganizerStatus_ORGANIZER_STATUS_ACTIVE, got[0].GetStatus())
	})

	t.Run("status changed meanwhile", func(t *testing.T) {
		// @spec components/entity/organizer/compare-and-set-status "Status changed meanwhile"
		id := seedOrganizerInStatus(t, entity.OrganizerStatusDeactivated)
		mark := outboxMark(t)

		changed, err := repo.CompareAndSetStatus(ctx, id, entity.OrganizerStatusProvisioning, entity.OrganizerStatusActive)

		require.NoError(t, err)
		assert.False(t, changed)
		assert.Empty(t, createdOf(t, mark, id))
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/organizer/compare-and-set-status "Announcement cannot be recorded"
		id := seedOrganizerInStatus(t, entity.OrganizerStatusProvisioning)

		_, err := rdb.NewOrganizerRepository(testDB, failingOutbox{}).CompareAndSetStatus(ctx, id, entity.OrganizerStatusProvisioning, entity.OrganizerStatusActive)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Equal(t, entity.OrganizerStatusProvisioning, organizerStatus(t, id))
	})
}

func TestOrganizerRepository_AssociateArtistAnnounces(t *testing.T) {
	ctx := context.Background()
	repo := rdb.NewOrganizerRepository(testDB, testOutbox)
	associatedOf := func(t *testing.T, mark int64, artistID string) []*entityv1.Artist {
		t.Helper()
		return announcedSince(t, mark, entity.SubjectOrganizerArtistAssociated, func() *entityv1.Artist { return &entityv1.Artist{} },
			func(a *entityv1.Artist) bool { return a.GetId().GetValue() == artistID })
	}
	representedBy := func(t *testing.T, artistID string) string {
		t.Helper()
		var org string
		require.NoError(t, testDB.Pool.QueryRow(ctx,
			`SELECT COALESCE((SELECT organizer_id::text FROM organizer_artists WHERE artist_id = $1), '')`, artistID).Scan(&org))
		return org
	}
	newArtist := func(t *testing.T) string {
		t.Helper()
		return seedArtist(t, "artist "+uuid.NewV7().String(), uuid.NewV7().String())
	}

	t.Run("unrepresented artist linked", func(t *testing.T) {
		// @spec components/entity/organizer/associate-artist "Unrepresented Artist linked"
		org, artistID := seedOrganizer(t), newArtist(t)
		mark := outboxMark(t)

		require.NoError(t, repo.AssociateArtist(ctx, org, artistID))

		got := associatedOf(t, mark, artistID)
		require.Len(t, got, 1)
		assert.Equal(t, org, got[0].GetOrganizerId().GetValue())
	})

	t.Run("artist already represented", func(t *testing.T) {
		// @spec components/entity/organizer/associate-artist "Artist already represented"
		artistID := newArtist(t)
		require.NoError(t, repo.AssociateArtist(ctx, seedOrganizer(t), artistID))
		mark := outboxMark(t)

		err := repo.AssociateArtist(ctx, seedOrganizer(t), artistID)

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
		assert.Empty(t, associatedOf(t, mark, artistID))
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/organizer/associate-artist "Announcement cannot be recorded"
		org, artistID := seedOrganizer(t), newArtist(t)

		err := rdb.NewOrganizerRepository(testDB, failingOutbox{}).AssociateArtist(ctx, org, artistID)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Empty(t, representedBy(t, artistID), "the Artist is linked to no Organizer")
	})
}
