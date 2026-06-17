package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSalesPhaseDiscoveryUseCase_DiscoverForArtist(t *testing.T) {
	t.Parallel()

	logger, _ := logging.New()
	ctx := context.Background()
	window := 90 * 24 * time.Hour

	artist := &entity.Artist{ID: "artist-001", Name: "TestArtist"}
	seriesID := "series-aaa"
	eventID := "event-001"
	t0 := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	upcomingConcert := &entity.Concert{
		Event: entity.Event{
			ID:        eventID,
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

	tests := []struct {
		name         string
		setupMocks   func(concertRepo *entitymocks.MockConcertRepository, repo *entitymocks.MockSalesPhaseRepository, searcher *entitymocks.MockSalesPhaseSearcher, pub *ucmocks.MockEventPublisher)
		wantNewCount int
		wantErr      error
	}{
		{
			name: "new phase discovered → published once",
			setupMocks: func(concertRepo *entitymocks.MockConcertRepository, repo *entitymocks.MockSalesPhaseRepository, searcher *entitymocks.MockSalesPhaseSearcher, pub *ucmocks.MockEventPublisher) {
				concertRepo.On("ListByArtist", ctx, artist.ID, true).Return([]*entity.Concert{upcomingConcert}, nil)
				searcher.On("SearchSalesPhases", ctx, artist.Name, "TestTour 2026", seriesID).
					Return([]*entity.SalesPhaseCandidate{candidate}, nil)
				repo.On("Upsert", ctx, candidate).Return("phase-111", entity.UpsertOutcomeInserted, nil)
				pub.On("PublishEvent", ctx, entity.SubjectSalesPhaseDiscovered, entity.SalesPhaseDiscoveredData{
					PhaseID:  "phase-111",
					SeriesID: seriesID,
				}).Return(nil)
			},
			wantNewCount: 1,
		},
		{
			name: "re-discovery → update, not re-announced",
			setupMocks: func(concertRepo *entitymocks.MockConcertRepository, repo *entitymocks.MockSalesPhaseRepository, searcher *entitymocks.MockSalesPhaseSearcher, pub *ucmocks.MockEventPublisher) {
				concertRepo.On("ListByArtist", ctx, artist.ID, true).Return([]*entity.Concert{upcomingConcert}, nil)
				searcher.On("SearchSalesPhases", ctx, artist.Name, "TestTour 2026", seriesID).
					Return([]*entity.SalesPhaseCandidate{candidate}, nil)
				// UpsertOutcomeUpdated → no publish
				repo.On("Upsert", ctx, candidate).Return("phase-111", entity.UpsertOutcomeUpdated, nil)
				// pub.PublishEvent must NOT be called
			},
			wantNewCount: 0,
		},
		{
			name: "no upcoming concerts → zero series, zero new phases",
			setupMocks: func(concertRepo *entitymocks.MockConcertRepository, repo *entitymocks.MockSalesPhaseRepository, searcher *entitymocks.MockSalesPhaseSearcher, pub *ucmocks.MockEventPublisher) {
				concertRepo.On("ListByArtist", ctx, artist.ID, true).Return([]*entity.Concert{}, nil)
			},
			wantNewCount: 0,
		},
		{
			name: "searcher returns empty → nothing upserted",
			setupMocks: func(concertRepo *entitymocks.MockConcertRepository, repo *entitymocks.MockSalesPhaseRepository, searcher *entitymocks.MockSalesPhaseSearcher, pub *ucmocks.MockEventPublisher) {
				concertRepo.On("ListByArtist", ctx, artist.ID, true).Return([]*entity.Concert{upcomingConcert}, nil)
				searcher.On("SearchSalesPhases", ctx, artist.Name, "TestTour 2026", seriesID).
					Return([]*entity.SalesPhaseCandidate{}, nil)
			},
			wantNewCount: 0,
		},
		{
			name: "upsert guard drops candidate → skipped outcome, nothing published",
			setupMocks: func(concertRepo *entitymocks.MockConcertRepository, repo *entitymocks.MockSalesPhaseRepository, searcher *entitymocks.MockSalesPhaseSearcher, pub *ucmocks.MockEventPublisher) {
				concertRepo.On("ListByArtist", ctx, artist.ID, true).Return([]*entity.Concert{upcomingConcert}, nil)
				searcher.On("SearchSalesPhases", ctx, artist.Name, "TestTour 2026", seriesID).
					Return([]*entity.SalesPhaseCandidate{candidate}, nil)
				repo.On("Upsert", ctx, candidate).Return("", entity.UpsertOutcomeSkipped, nil)
			},
			wantNewCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			concertRepo := entitymocks.NewMockConcertRepository(t)
			repo := entitymocks.NewMockSalesPhaseRepository(t)
			searcher := entitymocks.NewMockSalesPhaseSearcher(t)
			pub := ucmocks.NewMockEventPublisher(t)

			tt.setupMocks(concertRepo, repo, searcher, pub)

			uc := usecase.NewSalesPhaseDiscoveryUseCase(concertRepo, repo, searcher, pub, window, logger)
			got, err := uc.DiscoverForArtist(ctx, artist)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantNewCount, got)
		})
	}
}
