package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

func TestTicketSaleRepository_UpsertDiscovered(t *testing.T) {
	// Subtests are NOT parallel: each calls cleanDatabase, which TRUNCATEs the
	// shared tables; running them concurrently deadlocks (matches the sequential
	// convention of the other repository tests in this package).
	if testDB == nil {
		t.Skip("no local database available")
	}

	repo := rdb.NewTicketSaleRepository(testDB)
	ctx := context.Background()

	jst := time.FixedZone("JST", 9*60*60)
	oct5At16 := time.Date(2026, 10, 5, 16, 0, 0, 0, jst)
	oct5At18 := time.Date(2026, 10, 5, 18, 0, 0, 0, jst)
	oct22Close := time.Date(2026, 10, 22, 23, 59, 0, 0, jst)

	lottery := func(seriesID string, start time.Time) *entity.DiscoveredTicketSale {
		return &entity.DiscoveredTicketSale{
			SeriesID:      seriesID,
			Name:          "先行抽選",
			Method:        entity.TicketSaleMethodLottery,
			SaleStartTime: start,
			SaleEndTime:   oct22Close,
		}
	}
	listOf := func(t *testing.T, seriesID string) []*entity.TicketSale {
		t.Helper()
		sales, err := repo.ListBySeries(ctx, seriesID)
		require.NoError(t, err)
		return sales
	}

	// @spec components/entity/ticket-sale/upsert-discovered "Re-discovery with a corrected time"
	t.Run("Re-discovery with a corrected time", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")

		firstID, outcome := upsertDiscovered(t, repo, ctx, lottery(seriesID, oct5At16))
		assert.Equal(t, entity.UpsertOutcomeInserted, outcome)

		id, outcome := upsertDiscovered(t, repo, ctx, lottery(seriesID, oct5At18))
		assert.Equal(t, entity.UpsertOutcomeUpdated, outcome)
		assert.Equal(t, firstID, id)

		sales := listOf(t, seriesID)
		require.Len(t, sales, 1)
		assert.True(t, oct5At18.Equal(sales[0].SaleStartTime))
		assert.True(t, oct22Close.Equal(sales[0].SaleEndTime))
		assert.True(t, sales[0].IsDiscovered())
		assert.Equal(t, "先行抽選", sales[0].Name)
		assert.Empty(t, sales[0].EventIDs)
		assert.NoError(t, sales[0].Validate(), "a stored discovered sale reads back valid")
	})

	// @spec components/entity/ticket-sale/upsert-discovered "Re-discovery with a new name"
	t.Run("Re-discovery with a new name", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")
		firstID, _ := upsertDiscovered(t, repo, ctx, lottery(seriesID, oct5At18))

		renamed := lottery(seriesID, oct5At16)
		renamed.Name = "オフィシャル先行"
		id, outcome := upsertDiscovered(t, repo, ctx, renamed)

		assert.Equal(t, entity.UpsertOutcomeUpdated, outcome)
		assert.Equal(t, firstID, id)
		sales := listOf(t, seriesID)
		require.Len(t, sales, 1)
		assert.Equal(t, "オフィシャル先行", sales[0].Name)
	})

	// @spec components/entity/ticket-sale/upsert-discovered "Different start dates stay separate"
	t.Run("Different start dates stay separate", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")

		firstID, _ := upsertDiscovered(t, repo, ctx, lottery(seriesID, oct5At18))

		nov10 := &entity.DiscoveredTicketSale{
			SeriesID:      seriesID,
			Name:          "二次先行",
			Method:        entity.TicketSaleMethodLottery,
			SaleStartTime: time.Date(2026, 11, 10, 12, 0, 0, 0, jst),
			SaleEndTime:   time.Date(2026, 11, 20, 23, 59, 0, 0, jst),
		}
		id, outcome := upsertDiscovered(t, repo, ctx, nov10)
		assert.Equal(t, entity.UpsertOutcomeInserted, outcome)
		assert.NotEqual(t, firstID, id)
		assert.Len(t, listOf(t, seriesID), 2)
	})

	// @spec components/entity/ticket-sale/upsert-discovered "Different methods on one day stay separate"
	t.Run("Different methods on one day stay separate", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")

		upsertDiscovered(t, repo, ctx, lottery(seriesID, oct5At18))

		firstCome := &entity.DiscoveredTicketSale{
			SeriesID:      seriesID,
			Name:          "一般発売",
			Method:        entity.TicketSaleMethodFirstCome,
			SaleStartTime: oct5At18,
			SaleEndTime:   oct22Close,
		}
		_, outcome := upsertDiscovered(t, repo, ctx, firstCome)
		assert.Equal(t, entity.UpsertOutcomeInserted, outcome)
		assert.Len(t, listOf(t, seriesID), 2)
	})

	t.Run("same instant on different Japan dates stays separate", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")

		// 23:30 and 00:30 JST are an hour apart but on different Japan dates.
		late := lottery(seriesID, time.Date(2026, 10, 5, 23, 30, 0, 0, jst))
		early := lottery(seriesID, time.Date(2026, 10, 6, 0, 30, 0, 0, jst))
		upsertDiscovered(t, repo, ctx, late)
		_, outcome := upsertDiscovered(t, repo, ctx, early)
		assert.Equal(t, entity.UpsertOutcomeInserted, outcome)
	})

	skipped := []struct {
		name string
		sale func(seriesID string) *entity.DiscoveredTicketSale
	}{
		{
			// @spec components/entity/ticket-sale/upsert-discovered "Unknown start"
			name: "Unknown start",
			sale: func(seriesID string) *entity.DiscoveredTicketSale {
				s := lottery(seriesID, time.Time{})
				return s
			},
		},
		{
			// @spec components/entity/ticket-sale/upsert-discovered "First-come sale until sold out"
			name: "First-come sale until sold out",
			sale: func(seriesID string) *entity.DiscoveredTicketSale {
				return &entity.DiscoveredTicketSale{
					SeriesID: seriesID, Name: "一般発売", Method: entity.TicketSaleMethodFirstCome, SaleStartTime: oct5At18,
				}
			},
		},
		{
			name: "Lottery without a close",
			sale: func(seriesID string) *entity.DiscoveredTicketSale {
				s := lottery(seriesID, oct5At18)
				s.SaleEndTime = time.Time{}
				return s
			},
		},
	}
	for _, tc := range skipped {
		t.Run(tc.name, func(t *testing.T) {
			cleanDatabase(t)
			seriesID := seedSeriesOnly(t, "Tour")

			saleID, outcome, err := repo.UpsertDiscovered(ctx, tc.sale(seriesID))

			require.NoError(t, err)
			assert.Equal(t, entity.UpsertOutcomeSkipped, outcome)
			assert.Empty(t, saleID)
			assert.Empty(t, listOf(t, seriesID))
		})
	}

	// @spec components/entity/ticket-sale/upsert-discovered "Sale no longer discovered"
	t.Run("Sale no longer discovered", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")

		upsertDiscovered(t, repo, ctx, lottery(seriesID, oct5At18))
		// A later discovery that returns no sales makes no UpsertDiscovered
		// call; the stored sale stays.
		assert.Len(t, listOf(t, seriesID), 1)
	})

	// @spec components/entity/ticket-sale/upsert-discovered "No series"
	t.Run("No series", func(t *testing.T) {
		cleanDatabase(t)

		_, _, err := repo.UpsertDiscovered(ctx, lottery("", oct5At18))
		assertAppErrCode(t, err, codes.InvalidArgument)
	})

	// @spec components/entity/ticket-sale/upsert-discovered "No name"
	t.Run("No name", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")
		unnamed := lottery(seriesID, oct5At18)
		unnamed.Name = ""

		_, _, err := repo.UpsertDiscovered(ctx, unnamed)
		assertAppErrCode(t, err, codes.InvalidArgument)
		assert.Empty(t, listOf(t, seriesID))
	})

	// @spec components/entity/ticket-sale/upsert-discovered "Unknown series"
	t.Run("Unknown series", func(t *testing.T) {
		cleanDatabase(t)

		_, _, err := repo.UpsertDiscovered(ctx, lottery(mustNewV7(), oct5At18))
		assertAppErrCode(t, err, codes.FailedPrecondition)
	})

	// @spec components/entity/ticket-sale/upsert-discovered "Series of an Organizer"
	t.Run("Series of an Organizer", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seriesOf(t, seedPublishedEvent(t))

		_, _, err := repo.UpsertDiscovered(ctx, lottery(seriesID, oct5At18))
		assertAppErrCode(t, err, codes.FailedPrecondition)
		assert.Empty(t, listOf(t, seriesID))
	})
}

// TestTicketSaleRepository_UpsertDiscovered_DiscoveredTime proves the
// discovered time is set on insert and never changed by a re-discovery.
//
// @spec components/entity/ticket-sale "Re-discovery keeps the discovered time"
func TestTicketSaleRepository_UpsertDiscovered_DiscoveredTime(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	repo := rdb.NewTicketSaleRepository(testDB)
	ctx := context.Background()
	t0 := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

	cleanDatabase(t)
	seriesID := seedSeriesOnly(t, "TourCA")

	before := time.Now().UTC().Add(-time.Second)
	upsertDiscovered(t, repo, ctx, &entity.DiscoveredTicketSale{
		SeriesID: seriesID, Name: "先行抽選", Method: entity.TicketSaleMethodLottery,
		SaleStartTime: t0, SaleEndTime: t0.Add(72 * time.Hour),
	})
	after := time.Now().UTC().Add(time.Second)

	sales, err := repo.ListBySeries(ctx, seriesID)
	require.NoError(t, err)
	require.Len(t, sales, 1)
	require.NotNil(t, sales[0].DiscoveredTime)
	assert.True(t, sales[0].DiscoveredTime.After(before) && sales[0].DiscoveredTime.Before(after),
		"the discovered time is the time of the first store")
	first := *sales[0].DiscoveredTime

	upsertDiscovered(t, repo, ctx, &entity.DiscoveredTicketSale{
		SeriesID: seriesID, Name: "先行抽選", Method: entity.TicketSaleMethodLottery,
		SaleStartTime: t0, SaleEndTime: t0.Add(96 * time.Hour),
	})

	sales, err = repo.ListBySeries(ctx, seriesID)
	require.NoError(t, err)
	require.Len(t, sales, 1)
	assert.Equal(t, first.UTC(), sales[0].DiscoveredTime.UTC(), "the discovered time does not change on update")
}

func TestTicketSaleRepository_ListBySeries(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	repo := rdb.NewTicketSaleRepository(testDB)
	ctx := context.Background()
	jst := time.FixedZone("JST", 9*60*60)

	// @spec components/entity/ticket-sale/list-by-series "Series with two sales"
	t.Run("Series with two sales", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")

		nov10 := time.Date(2026, 11, 10, 12, 0, 0, 0, jst)
		oct5 := time.Date(2026, 10, 5, 18, 0, 0, 0, jst)
		// Insert the later one first so the order comes from the query.
		upsertDiscovered(t, repo, ctx, &entity.DiscoveredTicketSale{
			SeriesID: seriesID, Name: "二次先行", Method: entity.TicketSaleMethodLottery,
			SaleStartTime: nov10, SaleEndTime: nov10.Add(72 * time.Hour),
		})
		upsertDiscovered(t, repo, ctx, &entity.DiscoveredTicketSale{
			SeriesID: seriesID, Name: "先行抽選", Method: entity.TicketSaleMethodLottery,
			SaleStartTime: oct5, SaleEndTime: oct5.Add(72 * time.Hour),
		})

		sales, err := repo.ListBySeries(ctx, seriesID)
		require.NoError(t, err)
		require.Len(t, sales, 2)
		assert.True(t, oct5.Equal(sales[0].SaleStartTime))
		assert.True(t, nov10.Equal(sales[1].SaleStartTime))
	})

	t.Run("an Organizer's sale comes with its events", func(t *testing.T) {
		cleanDatabase(t)
		eventID := seedPublishedEvent(t)
		created := createSale(t, newSaleFor(t, entity.TicketSaleMethodLottery, saleWindowStart, saleWindowStart.Add(5*24*time.Hour), eventID))

		sales, err := repo.ListBySeries(ctx, seriesOf(t, eventID))
		require.NoError(t, err)
		require.Len(t, sales, 1)
		assert.Equal(t, created.ID, sales[0].ID)
		assert.False(t, sales[0].IsDiscovered())
		assert.Equal(t, []string{eventID}, sales[0].EventIDs)
	})

	// @spec components/entity/ticket-sale/list-by-series "Series with no sale"
	t.Run("Series with no sale", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "Tour")

		sales, err := repo.ListBySeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Empty(t, sales)
	})

	// @spec components/entity/ticket-sale/list-by-series "No series given"
	t.Run("No series given", func(t *testing.T) {
		_, err := repo.ListBySeries(ctx, "")
		assertAppErrCode(t, err, codes.InvalidArgument)
	})
}

func TestTicketSaleRepository_ListWithPendingMilestones(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	repo := rdb.NewTicketSaleRepository(testDB)
	ctx := context.Background()
	now := time.Now()
	lookahead := 7 * 24 * time.Hour
	lookback := 2 * time.Hour
	const day = 24 * time.Hour

	discovered := func(t *testing.T, method entity.TicketSaleMethod, start, end time.Time) entity.TicketSaleID {
		t.Helper()
		id, _ := upsertDiscovered(t, repo, ctx, &entity.DiscoveredTicketSale{
			SeriesID: seedSeriesOnly(t, "Tour"), Name: "オフィシャル先行", Method: method,
			SaleStartTime: start, SaleEndTime: end,
		})
		return id
	}

	// @spec components/entity/ticket-sale/list-with-pending-milestones "Opened days ago, closes tomorrow"
	t.Run("Opened days ago, closes tomorrow", func(t *testing.T) {
		cleanDatabase(t)
		id := discovered(t, entity.TicketSaleMethodLottery, now.Add(-10*day), now.Add(day))

		sales, err := repo.ListWithPendingMilestones(ctx, lookahead, lookback)
		require.NoError(t, err)
		assert.Equal(t, []entity.TicketSaleID{id}, saleIDs(sales))
	})

	// @spec components/entity/ticket-sale/list-with-pending-milestones "Opens beyond the lookahead"
	t.Run("Opens beyond the lookahead", func(t *testing.T) {
		cleanDatabase(t)
		discovered(t, entity.TicketSaleMethodFirstCome, now.Add(10*day), now.Add(12*day))

		sales, err := repo.ListWithPendingMilestones(ctx, lookahead, lookback)
		require.NoError(t, err)
		assert.Empty(t, sales)
	})

	// @spec components/entity/ticket-sale/list-with-pending-milestones "All milestones long past"
	t.Run("All milestones long past", func(t *testing.T) {
		cleanDatabase(t)
		discovered(t, entity.TicketSaleMethodFirstCome, now.Add(-5*time.Hour), now.Add(-3*time.Hour))

		sales, err := repo.ListWithPendingMilestones(ctx, lookahead, lookback)
		require.NoError(t, err)
		assert.Empty(t, sales)
	})

	// @spec components/entity/ticket-sale/list-with-pending-milestones "Latest milestone just past"
	t.Run("Latest milestone just past", func(t *testing.T) {
		cleanDatabase(t)
		id := discovered(t, entity.TicketSaleMethodFirstCome, now.Add(-5*time.Hour), now.Add(-time.Hour))

		sales, err := repo.ListWithPendingMilestones(ctx, lookahead, lookback)
		require.NoError(t, err)
		assert.Equal(t, []entity.TicketSaleID{id}, saleIDs(sales))
	})

	// @spec components/entity/ticket-sale/list-with-pending-milestones "Organizer's sale opening tomorrow"
	t.Run("Organizer's sale opening tomorrow", func(t *testing.T) {
		cleanDatabase(t)
		eventID := seedPublishedEvent(t)
		sale := createSale(t, newSaleFor(t, entity.TicketSaleMethodLottery, now.Add(day), now.Add(6*day), eventID))

		sales, err := repo.ListWithPendingMilestones(ctx, lookahead, lookback)
		require.NoError(t, err)
		require.Equal(t, []entity.TicketSaleID{sale.ID}, saleIDs(sales))
		assert.Equal(t, []string{eventID}, sales[0].EventIDs, "the sale comes with its events")
	})

	// @spec components/entity/ticket-sale/list-with-pending-milestones "Organizer's sale of a cancelled Series"
	t.Run("Organizer's sale of a cancelled Series", func(t *testing.T) {
		cleanDatabase(t)
		eventID := seedFirstPartyEvents(t, "CANCELLED")[0]
		createSale(t, newSaleFor(t, entity.TicketSaleMethodLottery, now.Add(day), now.Add(6*day), eventID))

		sales, err := repo.ListWithPendingMilestones(ctx, lookahead, lookback)
		require.NoError(t, err)
		assert.Empty(t, sales)
	})

	// @spec components/entity/ticket-sale/list-with-pending-milestones "Nothing pending"
	t.Run("Nothing pending", func(t *testing.T) {
		cleanDatabase(t)

		sales, err := repo.ListWithPendingMilestones(ctx, lookahead, lookback)
		require.NoError(t, err)
		assert.Empty(t, sales)
	})

	// @spec components/entity/ticket-sale/list-with-pending-milestones "Zero lookahead"
	t.Run("Zero lookahead", func(t *testing.T) {
		_, err := repo.ListWithPendingMilestones(ctx, 0, lookback)
		assertAppErrCode(t, err, codes.InvalidArgument)
	})

	// @spec components/entity/ticket-sale/list-with-pending-milestones "Negative lookback"
	t.Run("Negative lookback", func(t *testing.T) {
		_, err := repo.ListWithPendingMilestones(ctx, lookahead, -time.Hour)
		assertAppErrCode(t, err, codes.InvalidArgument)
	})
}

// ----- test seed helpers specific to discovered ticket sale tests -----

// upsertDiscovered calls repo.UpsertDiscovered and fails the test on error.
func upsertDiscovered(t *testing.T, repo *rdb.TicketSaleRepository, ctx context.Context, sale *entity.DiscoveredTicketSale) (entity.TicketSaleID, entity.UpsertOutcome) {
	t.Helper()
	id, outcome, err := repo.UpsertDiscovered(ctx, sale)
	require.NoError(t, err)
	return id, outcome
}

// seedSeriesOnly inserts a bare discovered series row (no Organizer) and
// returns its ID.
func seedSeriesOnly(t *testing.T, title string) string {
	t.Helper()
	ctx := context.Background()
	seriesID := mustNewV7()
	_, err := testDB.Pool.Exec(ctx,
		`INSERT INTO series (id, title, type) VALUES ($1, $2, 'SINGLE')`,
		seriesID, title,
	)
	require.NoError(t, err)
	return seriesID
}

// seedEventForSeries inserts an event belonging to the given series and links
// the artist via concert_artists. Returns the event ID.
func seedEventForSeries(t *testing.T, seriesID, venueID, artistID, date string) string {
	t.Helper()
	ctx := context.Background()
	eventID := mustNewV7()
	_, err := testDB.Pool.Exec(ctx,
		`INSERT INTO events (id, series_id, venue_id, local_event_date) VALUES ($1, $2, $3, $4)`,
		eventID, seriesID, venueID, date,
	)
	require.NoError(t, err)
	_, err = testDB.Pool.Exec(ctx, `INSERT INTO concerts (event_id) VALUES ($1)`, eventID)
	require.NoError(t, err)
	_, err = testDB.Pool.Exec(ctx,
		`INSERT INTO concert_artists (event_id, artist_id) VALUES ($1, $2)`,
		eventID, artistID,
	)
	require.NoError(t, err)
	return eventID
}

// mustNewV7 generates a new UUIDv7 string, panicking on entropy failure.
func mustNewV7() string {
	return uuid.NewV7().String()
}

// assertAppErrCode asserts err is an apperr carrying the given code.
func assertAppErrCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	require.Error(t, err)
	var ae *apperr.AppErr
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, want, ae.Code, "got error: %v", err)
}
