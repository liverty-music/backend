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

// countHolders returns how many artists have mbid as their MBID or among
// their other MBIDs.
func countHolders(t *testing.T, db *rdb.Database, mbid string) int {
	t.Helper()
	var n int
	require.NoError(t, db.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM artists WHERE mbid = $1 OR other_mbids @> ARRAY[$1::text]`, mbid).Scan(&n))
	return n
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

		require.NoError(t, repo.UpdateMBID(ctx, id, mbidB))

		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, mbidB, got.MBID)
		assert.Equal(t, []string{mbidA}, got.OtherMBIDs)
	})

	// @spec components/entity/artist/update-mbid "Second correction"
	t.Run("Second correction", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		require.NoError(t, repo.UpdateMBID(ctx, id, mbidB))

		require.NoError(t, repo.UpdateMBID(ctx, id, mbidC))

		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, mbidC, got.MBID)
		assert.Equal(t, []string{mbidA, mbidB}, got.OtherMBIDs)
	})

	// @spec components/entity/artist/update-mbid "Back to an earlier MBID"
	t.Run("Back to an earlier MBID", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		require.NoError(t, repo.UpdateMBID(ctx, id, mbidB))

		require.NoError(t, repo.UpdateMBID(ctx, id, mbidA))

		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, mbidA, got.MBID)
		assert.Equal(t, []string{mbidB}, got.OtherMBIDs)
	})

	// @spec components/entity/artist/update-mbid "Own MBID"
	t.Run("Own MBID", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		require.NoError(t, repo.UpdateFanart(ctx, id, &entity.Fanart{ArtistThumb: []entity.FanartImage{{ID: "1", URL: "https://example.com/1.jpg"}}}, time.Now()))
		require.NoError(t, repo.UpdateActivityEnded(ctx, id, true))
		require.NoError(t, repo.CreateOfficialSite(ctx, entity.NewOfficialSite(id, "https://example.com/")))
		require.NoError(t, repo.MarkOfficialSiteChecked(ctx, id, time.Now()))
		before, err := repo.Get(ctx, id)
		require.NoError(t, err)

		require.NoError(t, repo.UpdateMBID(ctx, id, mbidA))

		after, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, before, after)
		_, err = repo.GetOfficialSite(ctx, id)
		assert.NoError(t, err)
	})

	// @spec components/entity/artist/update-mbid "MBID of another artist"
	t.Run("MBID of another artist", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		other := seedArtist(t, db, "Other", mbidB)
		require.NoError(t, repo.UpdateMBID(ctx, other, mbidC)) // mbidB is now an other MBID of "Other"

		for _, taken := range []string{mbidC, mbidB} { // as MBID, then among other MBIDs
			err := repo.UpdateMBID(ctx, id, taken)
			assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
		}

		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, mbidA, got.MBID)
		assert.Empty(t, got.OtherMBIDs)
	})

	// @spec components/entity/artist/update-mbid "Unknown artist"
	t.Run("Unknown artist", func(t *testing.T) {
		cleanTables(t, db)

		err := repo.UpdateMBID(ctx, entity.NewID(), mbidA)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	// @spec components/entity/artist/update-mbid "Malformed MBID"
	t.Run("Malformed MBID", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)

		err := repo.UpdateMBID(ctx, id, "too-short")

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, mbidA, got.MBID)
	})

	// @spec components/entity/artist/update-mbid "Namesake's details removed"
	t.Run("Namesake's details removed", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		require.NoError(t, repo.UpdateFanart(ctx, id, &entity.Fanart{ArtistThumb: []entity.FanartImage{{ID: "1", URL: "https://example.com/1.jpg"}}}, time.Now()))
		require.NoError(t, repo.UpdateActivityEnded(ctx, id, true))
		require.NoError(t, repo.CreateOfficialSite(ctx, entity.NewOfficialSite(id, "https://namesake.example/")))
		require.NoError(t, repo.MarkOfficialSiteChecked(ctx, id, time.Now()))

		require.NoError(t, repo.UpdateMBID(ctx, id, mbidB))

		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Nil(t, got.Fanart)
		assert.Nil(t, got.FanartSyncTime)
		assert.Nil(t, got.OfficialSiteCheckTime)
		assert.False(t, got.ActivityEnded)
		_, err = repo.GetOfficialSite(ctx, id)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
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
		go func() { done <- repo.UpdateMBID(ctx, id, mbidB) }()

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
			var createErr, updateErr error
			go func() {
				defer wg.Done()
				_, createErr = repo.Create(ctx, entity.NewArtist("Penthouse", mbidA))
			}()
			go func() {
				defer wg.Done()
				updateErr = repo.UpdateMBID(ctx, id, mbidB)
			}()
			wg.Wait()

			require.NoError(t, createErr, "round %d", i)
			require.NoError(t, updateErr, "round %d", i)
			assert.Equal(t, 1, countHolders(t, db, mbidA), "round %d: one artist holds the replaced MBID", i)
			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, []string{mbidA}, got.OtherMBIDs)
		}
	})
}

func TestArtistRepository_ListByMBIDs_OtherMBIDs(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewArtistRepository(db, testOutbox)
	ctx := context.Background()

	// @spec components/entity/artist/list-by-mbids "Replaced MBID"
	t.Run("Replaced MBID", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		require.NoError(t, repo.UpdateMBID(ctx, id, mbidB))

		got, err := repo.ListByMBIDs(ctx, []string{mbidA})

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, id, got[0].ID)
	})

	// @spec components/entity/artist/list-by-mbids "Two MBIDs of one artist"
	t.Run("Two MBIDs of one artist", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		require.NoError(t, repo.UpdateMBID(ctx, id, mbidB))
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
		require.NoError(t, repo.UpdateMBID(ctx, id, mbidB))

		byOld, err := repo.ListByMBIDs(ctx, []string{mbidA})
		require.NoError(t, err)
		byNew, err := repo.ListByMBIDs(ctx, []string{mbidB})
		require.NoError(t, err)

		require.Len(t, byOld, 1)
		require.Len(t, byNew, 1)
		assert.Equal(t, id, byOld[0].ID)
		assert.Equal(t, id, byNew[0].ID)
	})

	// @spec components/entity/artist "One owner per MBID"
	t.Run("One owner per MBID", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		require.NoError(t, repo.UpdateMBID(ctx, id, mbidB))
		stranger := seedArtist(t, db, "Stranger", mbidC)

		require.ErrorIs(t, repo.UpdateMBID(ctx, stranger, mbidA), apperr.ErrAlreadyExists)
		_, err := repo.Create(ctx, entity.NewArtist("Namesake", mbidA))
		require.NoError(t, err)

		assert.Equal(t, 1, countHolders(t, db, mbidA))
	})
}

func TestArtistRepository_Create_OtherMBIDs(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewArtistRepository(db, testOutbox)
	ctx := context.Background()

	// @spec components/entity/artist/create "Replaced MBID"
	t.Run("Replaced MBID", func(t *testing.T) {
		cleanTables(t, db)
		id := seedArtist(t, db, "Penthouse", mbidA)
		require.NoError(t, repo.UpdateMBID(ctx, id, mbidB))
		var outboxBefore int
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM watermill_events_outbox`).Scan(&outboxBefore))

		got, err := repo.Create(ctx, entity.NewArtist("Penthouse", mbidA), entity.NewArtist("New one", mbidD))

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, id, got[0].ID)
		assert.Equal(t, mbidB, got[0].MBID)
		assert.Equal(t, "New one", got[1].Name)
		assert.Equal(t, 1, countHolders(t, db, mbidA))
		var outboxAfter int
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM watermill_events_outbox`).Scan(&outboxAfter))
		assert.Equal(t, outboxBefore+1, outboxAfter, "only the new artist is announced")
	})
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
