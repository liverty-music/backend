package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// discoveryMocks bundles the mocks for the per-artist discovery pipeline.
type discoveryMocks struct {
	concertRepo *entitymocks.MockConcertRepository
	artistRepo  *entitymocks.MockArtistRepository
	salesRepo   *entitymocks.MockSalesPhaseRepository
	searcher    *entitymocks.MockSalesPhaseSearcher
	pub         *ucmocks.MockEventPublisher
}

func TestSalesPhaseDiscoveryUseCase_DiscoverForArtist(t *testing.T) {
	t.Parallel()

	logger, _ := logging.New()
	ctx := context.Background()
	window := 90 * 24 * time.Hour

	artist := &entity.Artist{ID: "artist-001", Name: "TestArtist"}
	seriesID := "series-aaa"
	officialURL := "https://official.example"
	t0 := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	upcomingConcert := &entity.Concert{
		Event: entity.Event{
			ID:        "event-001",
			SeriesID:  seriesID,
			LocalDate: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		},
		Series: &entity.Series{ID: seriesID, Title: "TestTour 2026"},
	}

	candidate := &entity.SalesPhaseCandidate{
		SeriesID:       seriesID,
		Method:         entity.SalesMethodLottery,
		Channel:        entity.SalesChannelFanClub,
		ApplyStartTime: t0,
	}

	// matchArtistInput verifies the per-artist search input carries the artist,
	// the seeded official-site URL, and the series ref.
	matchArtistInput := mock.MatchedBy(func(in *entity.SalesPhaseSearchInput) bool {
		return in != nil && in.ArtistName == artist.Name && in.OfficialSiteURL == officialURL &&
			len(in.Series) == 1 && in.Series[0].SeriesID == seriesID
	})

	// withOfficialSite wires the grounding-seed URL lookup for cases that reach
	// the searcher.
	withOfficialSite := func(m *discoveryMocks) {
		m.artistRepo.On("GetOfficialSite", ctx, artist.ID).Return(&entity.OfficialSite{ArtistID: artist.ID, URL: officialURL}, nil)
	}

	tests := []struct {
		name         string
		setupMocks   func(m *discoveryMocks)
		wantNewCount int
	}{
		{
			name: "new phase discovered → published once",
			setupMocks: func(m *discoveryMocks) {
				m.concertRepo.On("ListByArtist", ctx, artist.ID, true).Return([]*entity.Concert{upcomingConcert}, nil)
				withOfficialSite(m)
				m.searcher.On("SearchSalesPhases", ctx, matchArtistInput).Return([]*entity.SalesPhaseCandidate{candidate}, nil)
				m.salesRepo.On("Upsert", ctx, candidate).Return("phase-111", entity.UpsertOutcomeInserted, nil)
				m.pub.On("PublishEvent", ctx, entity.SubjectSalesPhaseDiscovered, entity.SalesPhaseDiscoveredData{
					PhaseID:  "phase-111",
					SeriesID: seriesID,
				}).Return(nil)
			},
			wantNewCount: 1,
		},
		{
			name: "re-discovery → update, not re-announced",
			setupMocks: func(m *discoveryMocks) {
				m.concertRepo.On("ListByArtist", ctx, artist.ID, true).Return([]*entity.Concert{upcomingConcert}, nil)
				withOfficialSite(m)
				m.searcher.On("SearchSalesPhases", ctx, matchArtistInput).Return([]*entity.SalesPhaseCandidate{candidate}, nil)
				m.salesRepo.On("Upsert", ctx, candidate).Return("phase-111", entity.UpsertOutcomeUpdated, nil)
			},
			wantNewCount: 0,
		},
		{
			name: "no upcoming concerts → zero new phases (no search)",
			setupMocks: func(m *discoveryMocks) {
				m.concertRepo.On("ListByArtist", ctx, artist.ID, true).Return([]*entity.Concert{}, nil)
			},
			wantNewCount: 0,
		},
		{
			name: "no official site → benign skip, searcher not called",
			setupMocks: func(m *discoveryMocks) {
				m.concertRepo.On("ListByArtist", ctx, artist.ID, true).Return([]*entity.Concert{upcomingConcert}, nil)
				m.artistRepo.On("GetOfficialSite", ctx, artist.ID).Return(nil, apperr.New(codes.NotFound, "no official site"))
			},
			wantNewCount: 0,
		},
		{
			name: "empty official site URL → benign skip, searcher not called",
			setupMocks: func(m *discoveryMocks) {
				m.concertRepo.On("ListByArtist", ctx, artist.ID, true).Return([]*entity.Concert{upcomingConcert}, nil)
				m.artistRepo.On("GetOfficialSite", ctx, artist.ID).Return(&entity.OfficialSite{ArtistID: artist.ID, URL: ""}, nil)
			},
			wantNewCount: 0,
		},
		{
			name: "searcher returns empty → nothing upserted",
			setupMocks: func(m *discoveryMocks) {
				m.concertRepo.On("ListByArtist", ctx, artist.ID, true).Return([]*entity.Concert{upcomingConcert}, nil)
				withOfficialSite(m)
				m.searcher.On("SearchSalesPhases", ctx, matchArtistInput).Return([]*entity.SalesPhaseCandidate{}, nil)
			},
			wantNewCount: 0,
		},
		{
			name: "upsert guard drops candidate → skipped outcome, nothing published",
			setupMocks: func(m *discoveryMocks) {
				m.concertRepo.On("ListByArtist", ctx, artist.ID, true).Return([]*entity.Concert{upcomingConcert}, nil)
				withOfficialSite(m)
				m.searcher.On("SearchSalesPhases", ctx, matchArtistInput).Return([]*entity.SalesPhaseCandidate{candidate}, nil)
				m.salesRepo.On("Upsert", ctx, candidate).Return("", entity.UpsertOutcomeSkipped, nil)
			},
			wantNewCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := &discoveryMocks{
				concertRepo: entitymocks.NewMockConcertRepository(t),
				artistRepo:  entitymocks.NewMockArtistRepository(t),
				salesRepo:   entitymocks.NewMockSalesPhaseRepository(t),
				searcher:    entitymocks.NewMockSalesPhaseSearcher(t),
				pub:         ucmocks.NewMockEventPublisher(t),
			}
			tt.setupMocks(m)

			uc := usecase.NewSalesPhaseDiscoveryUseCase(
				m.concertRepo, m.artistRepo, m.salesRepo,
				m.searcher, m.pub, window, logger,
			)
			got, err := uc.DiscoverForArtist(ctx, artist)

			require.NoError(t, err)
			assert.Equal(t, tt.wantNewCount, got)
		})
	}
}
