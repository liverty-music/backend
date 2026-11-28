package rdb_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	mbidA = "aaaaaaaa-0000-0000-0000-00000000000a"
	mbidB = "bbbbbbbb-0000-0000-0000-00000000000b"
	mbidC = "cccccccc-0000-0000-0000-00000000000c"
	mbidD = "dddddddd-0000-0000-0000-00000000000d"
)

// countHolders returns how many artists have mbid as their MBID or their
// listening MBID.
func countHolders(t *testing.T, db *rdb.Database, mbid string) int {
	t.Helper()
	var n int
	require.NoError(t, db.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM artists WHERE mbid = $1 OR listening_mbid = $1`, mbid).Scan(&n))
	return n
}

// countAnnouncements returns the number of announcements in the outbox.
func countAnnouncements(t *testing.T, db *rdb.Database) int {
	t.Helper()
	var n int
	require.NoError(t, db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM watermill_events_outbox`).Scan(&n))
	return n
}

// setMBIDStatus sets an artist's MBID status directly, as the setup of a test.
func setMBIDStatus(t *testing.T, db *rdb.Database, id string, status entity.MBIDStatus) {
	t.Helper()
	_, err := db.Pool.Exec(context.Background(), `UPDATE artists SET mbid_status = $2 WHERE id = $1`, id, string(status))
	require.NoError(t, err)
}

// giveDetails gives the artist images, an official site, an official site
// check time and an ended activity.
func giveDetails(t *testing.T, db *rdb.Database, repo *rdb.ArtistRepository, id string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, repo.UpdateFanart(ctx, id, artistMBID(t, db, id), &entity.Fanart{ArtistThumb: []entity.FanartImage{{ID: "1", URL: "https://example.com/1.jpg"}}}, time.Now()))
	require.NoError(t, repo.UpdateActivityEnded(ctx, id, true))
	require.NoError(t, repo.CreateOfficialSite(ctx, entity.NewOfficialSite(id, "https://namesake.example/")))
	require.NoError(t, repo.MarkOfficialSiteChecked(ctx, id, time.Now()))
}

func TestArtistRepository_UpdateMBID(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewArtistRepository(db, testOutbox)
	ctx := context.Background()

	// @spec components/entity/artist/update-mbid "New MBID"
	t.Run("New MBID", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)

		require.NoError(t, repo.UpdateMBID(ctx, id, mbidB, "PENTHOUSE", true))

		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, mbidB, got.MBID)
		assert.Equal(t, mbidA, got.ListeningMBID)
		assert.Equal(t, "PENTHOUSE", got.Name)
		assert.True(t, got.ActivityEnded)
		assert.Equal(t, entity.MBIDStatusVerified, got.MBIDStatus)
	})

	// @spec components/entity/artist/update-mbid "Second correction"
	t.Run("Second correction", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		require.NoError(t, repo.UpdateMBID(ctx, id, mbidB, "Penthouse", false))

		require.NoError(t, repo.UpdateMBID(ctx, id, mbidC, "Penthouse", false))

		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, mbidC, got.MBID)
		assert.Equal(t, mbidA, got.ListeningMBID)
	})

	// @spec components/entity/artist/update-mbid "Back to an earlier MBID"
	t.Run("Back to an earlier MBID", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		require.NoError(t, repo.UpdateMBID(ctx, id, mbidB, "Penthouse", false))

		require.NoError(t, repo.UpdateMBID(ctx, id, mbidA, "Penthouse", false))

		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, mbidA, got.MBID)
		assert.Equal(t, mbidA, got.ListeningMBID)
	})

	// @spec components/entity/artist/update-mbid "Own MBID"
	t.Run("Own MBID", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		giveDetails(t, db, repo, id)
		setMBIDStatus(t, db, id, entity.MBIDStatusAmbiguous)
		before, err := repo.Get(ctx, id)
		require.NoError(t, err)

		require.NoError(t, repo.UpdateMBID(ctx, id, mbidA, "PENTHOUSE", false))

		after, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, entity.MBIDStatusVerified, after.MBIDStatus)
		assert.Equal(t, "PENTHOUSE", after.Name)
		assert.False(t, after.ActivityEnded)
		assert.Equal(t, before.Fanart, after.Fanart, "the images are kept")
		assert.Equal(t, before.OfficialSiteCheckTime, after.OfficialSiteCheckTime)
		_, err = repo.GetOfficialSite(ctx, id)
		assert.NoError(t, err, "the official site is kept")
	})

	// @spec components/entity/artist/update-mbid "MBID of another artist"
	t.Run("MBID of another artist", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		other := seedArtist(t, db, "Other", mbidB)
		require.NoError(t, repo.UpdateMBID(ctx, other, mbidC, "Other", false)) // Other: mbid C, listening_mbid B

		for _, taken := range []string{mbidC, mbidB} { // as mbid, then as listening_mbid
			err := repo.UpdateMBID(ctx, id, taken, "Penthouse", false)
			assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
		}

		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, mbidA, got.MBID)
		assert.Equal(t, entity.MBIDStatusUnverified, got.MBIDStatus)
	})

	// @spec components/entity/artist/update-mbid "Unknown artist"
	t.Run("Unknown artist", func(t *testing.T) {
		cleanTables(t, db)

		err := repo.UpdateMBID(ctx, entity.NewID(), mbidA, "Nobody", false)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	// @spec components/entity/artist/update-mbid "Malformed MBID"
	t.Run("Malformed MBID", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)

		err := repo.UpdateMBID(ctx, id, "too-short", "Penthouse", false)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, mbidA, got.MBID)
		assert.Equal(t, entity.MBIDStatusUnverified, got.MBIDStatus)
	})

	// @spec components/entity/artist/update-mbid "Namesake's details removed"
	t.Run("Namesake's details removed", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		giveDetails(t, db, repo, id)

		require.NoError(t, repo.UpdateMBID(ctx, id, mbidB, "Penthouse", false))

		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Nil(t, got.Fanart)
		assert.Nil(t, got.FanartSyncTime)
		assert.Nil(t, got.OfficialSiteCheckTime)
		assert.False(t, got.ActivityEnded)
		_, err = repo.GetOfficialSite(ctx, id)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("Images resolved for the replaced MBID are not stored", func(t *testing.T) {
		// @spec components/entity/artist/update-fanart "Images of a replaced MBID"
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		require.NoError(t, repo.UpdateMBID(ctx, id, mbidB, "Penthouse", false))

		// An image sync that resolved images for mbidA before the correction
		// finishes after it.
		err := repo.UpdateFanart(ctx, id, mbidA, &entity.Fanart{ArtistThumb: []entity.FanartImage{{ID: "1", URL: "https://namesake.example/1.jpg"}}}, time.Now())

		require.NoError(t, err)
		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Nil(t, got.Fanart, "the namesake's images are not stored")
		assert.Nil(t, got.FanartSyncTime, "the artist stays due for its first image check")
	})

	// @spec components/entity/artist/update-mbid "Registered at the same time"
	t.Run("Waits while a registration holds the lock", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		holder, err := db.Pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = holder.Rollback(ctx) }()
		// The lock Create takes before it registers.
		_, err = holder.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('artists.mbid', 0))`)
		require.NoError(t, err)

		done := make(chan error, 1)
		go func() { done <- repo.UpdateMBID(ctx, id, mbidB, "Penthouse", false) }()

		select {
		case err := <-done:
			t.Fatalf("UpdateMBID did not wait for the registration (returned %v)", err)
		case <-time.After(300 * time.Millisecond):
		}
		require.NoError(t, holder.Commit(ctx))
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("UpdateMBID did not finish after the registration ended")
		}
	})

	// @spec components/entity/artist/update-mbid "Registered at the same time"
	t.Run("Registered at the same time", func(t *testing.T) {
		for i := range 15 {
			cleanTables(t, db)
			id := seedArtist(t, db, "Penthouse", mbidA)

			var wg sync.WaitGroup
			wg.Add(2)
			var created []*entity.Artist
			var createErr, updateErr error
			go func() {
				defer wg.Done()
				created, createErr = repo.Create(ctx, entity.NewArtist("Penthouse", mbidA))
			}()
			go func() {
				defer wg.Done()
				updateErr = repo.UpdateMBID(ctx, id, mbidB, "Penthouse", false)
			}()
			wg.Wait()

			require.NoError(t, createErr, "round %d", i)
			require.NoError(t, updateErr, "round %d", i)
			require.Len(t, created, 1)
			assert.Equal(t, id, created[0].ID, "round %d: Create returns the corrected artist", i)
			assert.Equal(t, 1, countHolders(t, db, mbidA), "round %d: one artist holds the listening MBID", i)
		}
	})
}

func TestArtistRepository_MarkMBIDAmbiguous(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewArtistRepository(db, testOutbox)
	ctx := context.Background()

	// @spec components/entity/artist/mark-mbid-ambiguous "Unverified artist"
	t.Run("Unverified artist", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		before, err := repo.Get(ctx, id)
		require.NoError(t, err)
		announced := countAnnouncements(t, db)

		require.NoError(t, repo.MarkMBIDAmbiguous(ctx, id))

		after, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, entity.MBIDStatusAmbiguous, after.MBIDStatus)
		assert.Equal(t, before.MBID, after.MBID)
		assert.Equal(t, before.ListeningMBID, after.ListeningMBID)
		assert.Equal(t, before.Name, after.Name)
		assert.Equal(t, announced, countAnnouncements(t, db), "nothing is announced")
	})

	// @spec components/entity/artist/mark-mbid-ambiguous "Already ambiguous"
	t.Run("Already ambiguous", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		require.NoError(t, repo.MarkMBIDAmbiguous(ctx, id))
		before, err := repo.Get(ctx, id)
		require.NoError(t, err)

		require.NoError(t, repo.MarkMBIDAmbiguous(ctx, id))

		after, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, before, after)
	})

	// @spec components/entity/artist/mark-mbid-ambiguous "Unknown artist"
	t.Run("Unknown artist", func(t *testing.T) {
		cleanTables(t, db)

		assert.ErrorIs(t, repo.MarkMBIDAmbiguous(ctx, entity.NewID()), apperr.ErrNotFound)
	})
}

func TestArtistRepository_ListByMBIDStatus(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewArtistRepository(db, testOutbox)
	ctx := context.Background()

	// @spec components/entity/artist/list-by-mbid-status "Ambiguous artists"
	t.Run("Ambiguous artists", func(t *testing.T) {
		cleanTables(t, db)
		first := seedArtist(t, db, "First", mbidA)
		seedArtist(t, db, "Unverified", mbidB)
		second := seedArtist(t, db, "Second", mbidC)
		require.NoError(t, repo.MarkMBIDAmbiguous(ctx, second))
		require.NoError(t, repo.MarkMBIDAmbiguous(ctx, first))

		got, err := repo.ListByMBIDStatus(ctx, entity.MBIDStatusAmbiguous)

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, first, got[0].ID, "the one registered first is listed first")
		assert.Equal(t, second, got[1].ID)
	})

	// @spec components/entity/artist/list-by-mbid-status "No artist in the status"
	t.Run("No artist in the status", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Verified", mbidA)
		setMBIDStatus(t, db, id, entity.MBIDStatusVerified)

		got, err := repo.ListByMBIDStatus(ctx, entity.MBIDStatusUnverified)

		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})
}

func TestArtistRepository_ListByMBIDs_ListeningMBID(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewArtistRepository(db, testOutbox)
	ctx := context.Background()

	// @spec components/entity/artist/list-by-mbids "Replaced MBID"
	t.Run("Replaced MBID", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		require.NoError(t, repo.UpdateMBID(ctx, id, mbidB, "Penthouse", false))

		got, err := repo.ListByMBIDs(ctx, []string{mbidA})

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, id, got[0].ID)
		assert.Equal(t, entity.MBIDStatusVerified, got[0].MBIDStatus, "the artist comes with its MBID status")
	})

	// @spec components/entity/artist/list-by-mbids "Two MBIDs of one artist"
	t.Run("Two MBIDs of one artist", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		require.NoError(t, repo.UpdateMBID(ctx, id, mbidB, "Penthouse", false))
		seedArtist(t, db, "Someone else", mbidC)

		got, err := repo.ListByMBIDs(ctx, []string{mbidC, mbidA, mbidB})

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "Someone else", got[0].Name)
		assert.Equal(t, id, got[1].ID)
	})

	// @spec components/entity/artist "Corrected artist keeps its identity"
	t.Run("Corrected artist keeps its identity", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		require.NoError(t, repo.UpdateMBID(ctx, id, mbidB, "Penthouse", false))

		byListening, err := repo.ListByMBIDs(ctx, []string{mbidA})
		require.NoError(t, err)
		byMBID, err := repo.ListByMBIDs(ctx, []string{mbidB})
		require.NoError(t, err)

		require.Len(t, byListening, 1)
		require.Len(t, byMBID, 1)
		assert.Equal(t, id, byListening[0].ID)
		assert.Equal(t, id, byMBID[0].ID)
	})

	// @spec components/entity/artist "One owner per MBID"
	t.Run("One owner per MBID", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		require.NoError(t, repo.UpdateMBID(ctx, id, mbidB, "Penthouse", false))
		stranger := seedArtist(t, db, "Stranger", mbidC)

		require.ErrorIs(t, repo.UpdateMBID(ctx, stranger, mbidA, "Stranger", false), apperr.ErrAlreadyExists)
		_, err := repo.Create(ctx, entity.NewArtist("Namesake", mbidA))
		require.NoError(t, err)

		assert.Equal(t, 1, countHolders(t, db, mbidA))
	})
}

func TestArtistRepository_Create_ListeningMBID(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewArtistRepository(db, testOutbox)
	ctx := context.Background()

	// @spec components/entity/artist/create "Replaced MBID"
	t.Run("Replaced MBID", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		require.NoError(t, repo.UpdateMBID(ctx, id, mbidB, "Penthouse", false))
		before := countAnnouncements(t, db)

		got, err := repo.Create(ctx, entity.NewArtist("Penthouse", mbidA), entity.NewArtist("New one", mbidD))

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, id, got[0].ID)
		assert.Equal(t, mbidB, got[0].MBID)
		assert.Equal(t, "New one", got[1].Name)
		assert.Equal(t, 1, countHolders(t, db, mbidA))
		assert.Equal(t, before+1, countAnnouncements(t, db), "only the new artist is announced")
	})

	// @spec components/entity/artist/create "Corrected MBID already registered"
	t.Run("Corrected MBID already registered", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidB)
		before := countAnnouncements(t, db)
		corrected := entity.NewArtist("Penthouse", mbidA)
		corrected.MBID = mbidB

		got, err := repo.Create(ctx, corrected)

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, id, got[0].ID)
		assert.Zero(t, countHolders(t, db, mbidA), "listening MBID A is not registered")
		assert.Equal(t, before, countAnnouncements(t, db), "nothing is announced")
	})

	// @spec components/entity/artist/create "Registered with a verified MBID"
	// @spec components/entity/artist "New artist created verified"
	t.Run("Registered with a verified MBID", func(t *testing.T) {
		cleanTables(t, db)
		verified := entity.NewArtist("Penthouse", mbidA)
		verified.MBID = mbidB
		verified.MBIDStatus = entity.MBIDStatusVerified
		verified.ActivityEnded = true

		got, err := repo.Create(ctx, verified)

		require.NoError(t, err)
		require.Len(t, got, 1)
		stored, err := repo.Get(ctx, got[0].ID)
		require.NoError(t, err)
		assert.Equal(t, mbidA, stored.ListeningMBID)
		assert.Equal(t, mbidB, stored.MBID)
		assert.Equal(t, entity.MBIDStatusVerified, stored.MBIDStatus)
		assert.True(t, stored.ActivityEnded)
	})

	invalid := []struct {
		name   string
		artist func() *entity.Artist
	}{
		// @spec components/entity/artist/create "Artist without MBID"
		{name: "Artist without MBID", artist: func() *entity.Artist { return entity.NewArtist("No MBID", "") }},
		// @spec components/entity/artist/create "Malformed MBID"
		{name: "Malformed MBID", artist: func() *entity.Artist {
			a := entity.NewArtist("Short", mbidC)
			a.MBID = "too-short"
			return a
		}},
		// @spec components/entity/artist/create "Malformed listening MBID"
		{name: "Malformed listening MBID", artist: func() *entity.Artist {
			a := entity.NewArtist("Short", mbidC)
			a.ListeningMBID = "too-short"
			return a
		}},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			cleanTables(t, db)

			_, err := repo.Create(ctx, entity.NewArtist("Valid", mbidD), tt.artist())

			assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			assert.Zero(t, countHolders(t, db, mbidD), "neither artist is registered")
		})
	}
}

func TestArtistRepository_Create_WaitsForMBIDUpdate(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewArtistRepository(db, testOutbox)
	ctx := context.Background()
	cleanTables(t, db)

	holder, err := db.Pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = holder.Rollback(ctx) }()
	// The lock UpdateMBID takes before it checks and replaces an MBID.
	_, err = holder.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('artists.mbid', 0))`)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, err := repo.Create(ctx, entity.NewArtist("Penthouse", mbidA))
		done <- err
	}()

	// @spec components/entity/artist/update-mbid "Registered at the same time"
	select {
	case err := <-done:
		t.Fatalf("Create did not wait for the MBID update (returned %v)", err)
	case <-time.After(300 * time.Millisecond):
	}
	require.NoError(t, holder.Commit(ctx))
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Create did not finish after the MBID update ended")
	}
}
