package rdb_test

import (
	"context"
	"sync"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

func TestArtistRepository_CreateAnnounces(t *testing.T) {
	ctx := context.Background()
	repo := rdb.NewArtistRepository(testDB, testOutbox)
	createdOf := func(t *testing.T, mark int64, mbid string) []*entityv1.Artist {
		t.Helper()
		return announcedSince(t, mark, entity.SubjectArtistCreated, func() *entityv1.Artist { return &entityv1.Artist{} },
			func(a *entityv1.Artist) bool { return a.GetMbid().GetValue() == mbid })
	}
	registered := func(t *testing.T, mbid string) int {
		t.Helper()
		var n int
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT count(*) FROM artists WHERE mbid = $1`, mbid).Scan(&n))
		return n
	}

	t.Run("new MBID", func(t *testing.T) {
		// @spec components/entity/artist/create "New MBID"
		mbid := uuid.NewV7().String()
		mark := outboxMark(t)

		got, err := repo.Create(ctx, &entity.Artist{Name: "New Band", MBID: mbid})

		require.NoError(t, err)
		announced := createdOf(t, mark, mbid)
		require.Len(t, announced, 1)
		assert.Equal(t, got[0].ID, announced[0].GetId().GetValue())
	})

	t.Run("known MBID", func(t *testing.T) {
		// @spec components/entity/artist/create "Known MBID"
		mbid := uuid.NewV7().String()
		first, err := repo.Create(ctx, &entity.Artist{Name: "Known Band", MBID: mbid})
		require.NoError(t, err)
		mark := outboxMark(t)

		got, err := repo.Create(ctx, &entity.Artist{Name: "Known Band", MBID: mbid})

		require.NoError(t, err)
		assert.Equal(t, first[0].ID, got[0].ID)
		assert.Empty(t, createdOf(t, mark, mbid))
	})

	t.Run("registered by a concurrent call", func(t *testing.T) {
		// @spec components/entity/artist/create "Registered by a concurrent call"
		mbid := uuid.NewV7().String()
		mark := outboxMark(t)

		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() {
				_, err := repo.Create(ctx, &entity.Artist{Name: "Raced Band", MBID: mbid})
				assert.NoError(t, err)
			})
		}
		wg.Wait()

		assert.Equal(t, 1, registered(t, mbid))
		assert.Len(t, createdOf(t, mark, mbid), 1)
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/artist/create "Announcement cannot be recorded"
		mbid := uuid.NewV7().String()

		_, err := rdb.NewArtistRepository(testDB, failingOutbox{}).Create(ctx, &entity.Artist{Name: "Lost Band", MBID: mbid})

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Zero(t, registered(t, mbid))
	})
}

func TestTicketSaleRepository_UpsertDiscoveredAnnounces(t *testing.T) {
	ctx := context.Background()
	repo := rdb.NewTicketSaleRepository(testDB, testOutbox)
	jst := time.FixedZone("JST", 9*60*60)
	oct5At16 := time.Date(2026, 10, 5, 16, 0, 0, 0, jst)
	oct5At18 := time.Date(2026, 10, 5, 18, 0, 0, 0, jst)
	lottery := func(seriesID string, start time.Time) *entity.DiscoveredTicketSale {
		return &entity.DiscoveredTicketSale{
			SeriesID: seriesID, Name: "先行抽選", Method: entity.TicketSaleMethodLottery,
			SaleStartTime: start, SaleEndTime: time.Date(2026, 10, 22, 23, 59, 0, 0, jst),
		}
	}
	listOf := func(t *testing.T, seriesID string) []*entity.TicketSale {
		t.Helper()
		sales, err := repo.ListBySeries(ctx, seriesID)
		require.NoError(t, err)
		return sales
	}
	discoveredOf := func(t *testing.T, mark int64, seriesID string) []*entityv1.TicketSale {
		t.Helper()
		return announcedSince(t, mark, entity.SubjectTicketSaleDiscovered, func() *entityv1.TicketSale { return &entityv1.TicketSale{} },
			func(s *entityv1.TicketSale) bool { return s.GetSeriesId().GetValue() == seriesID })
	}

	t.Run("new sale", func(t *testing.T) {
		// @spec components/entity/ticket-sale/upsert-discovered "New sale"
		seriesID := seedSeriesOnly(t, "Tour")
		mark := outboxMark(t)

		id, outcome, err := repo.UpsertDiscovered(ctx, lottery(seriesID, oct5At18))

		require.NoError(t, err)
		assert.Equal(t, entity.UpsertOutcomeInserted, outcome)
		got := discoveredOf(t, mark, seriesID)
		require.Len(t, got, 1)
		assert.Equal(t, string(id), got[0].GetId().GetValue())
	})

	t.Run("known sale discovered again", func(t *testing.T) {
		// @spec components/entity/ticket-sale/upsert-discovered "Known sale discovered again"
		seriesID := seedSeriesOnly(t, "Tour")
		_, _, err := repo.UpsertDiscovered(ctx, lottery(seriesID, oct5At18))
		require.NoError(t, err)
		mark := outboxMark(t)

		_, outcome, err := repo.UpsertDiscovered(ctx, lottery(seriesID, oct5At16))

		require.NoError(t, err)
		assert.Equal(t, entity.UpsertOutcomeUpdated, outcome)
		assert.Empty(t, discoveredOf(t, mark, seriesID))
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/ticket-sale/upsert-discovered "Announcement cannot be recorded"
		seriesID := seedSeriesOnly(t, "Tour")

		_, _, err := rdb.NewTicketSaleRepository(testDB, failingOutbox{}).UpsertDiscovered(ctx, lottery(seriesID, oct5At18))

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Empty(t, listOf(t, seriesID), "no sale is stored")
	})
}
