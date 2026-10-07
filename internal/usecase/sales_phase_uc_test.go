package usecase_test

import (
	"context"
	"errors"
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
	concertRepo   *entitymocks.MockConcertRepository
	artistRepo    *entitymocks.MockArtistRepository
	salesRepo     *entitymocks.MockSalesPhaseRepository
	searchLogRepo *entitymocks.MockSalesPhaseSearchLogRepository
	journeyRepo   *entitymocks.MockTicketJourneyRepository
	searcher      *entitymocks.MockSalesPhaseSearcher
	pub           *ucmocks.MockEventPublisher
}

func newDiscoveryMocks(t *testing.T) *discoveryMocks {
	t.Helper()
	return &discoveryMocks{
		concertRepo:   entitymocks.NewMockConcertRepository(t),
		artistRepo:    entitymocks.NewMockArtistRepository(t),
		salesRepo:     entitymocks.NewMockSalesPhaseRepository(t),
		searchLogRepo: entitymocks.NewMockSalesPhaseSearchLogRepository(t),
		journeyRepo:   entitymocks.NewMockTicketJourneyRepository(t),
		searcher:      entitymocks.NewMockSalesPhaseSearcher(t),
		pub:           ucmocks.NewMockEventPublisher(t),
	}
}

const (
	discoveryArtistID    = "artist-001"
	discoveryOfficialURL = "https://official.example"
)

var discoveryArtist = &entity.Artist{ID: discoveryArtistID, Name: "TestArtist"}

func upcomingConcert(seriesID, title string, date time.Time) *entity.Concert {
	return &entity.Concert{
		ID:        "event-" + seriesID + "-" + date.Format(time.DateOnly),
		SeriesID:  seriesID,
		LocalDate: date,
		Series:    &entity.Series{ID: seriesID, Title: title},
	}
}

func (m *discoveryMocks) concerts(cs ...*entity.Concert) {
	m.concertRepo.EXPECT().ListByArtist(mock.Anything, discoveryArtistID, true).Return(cs, nil).Once()
}

func (m *discoveryMocks) tracked(seriesID string, tracked bool) {
	var trackers []*entity.SeriesTracker
	if tracked {
		trackers = []*entity.SeriesTracker{{UserID: "fan-1", EventID: "event-1"}}
	}
	m.journeyRepo.EXPECT().ListUserIDsTrackingSeries(mock.Anything, seriesID).Return(trackers, nil).Once()
}

func (m *discoveryMocks) phases(seriesID string, phases ...*entity.SalesPhase) {
	m.salesRepo.EXPECT().GetBySeries(mock.Anything, seriesID).Return(phases, nil).Once()
}

func (m *discoveryMocks) searchLogs(seriesIDs []string, logs ...*entity.SalesPhaseSearchLog) {
	m.searchLogRepo.EXPECT().ListBySeries(mock.Anything, seriesIDs).Return(logs, nil).Once()
}

func (m *discoveryMocks) officialSite() {
	m.artistRepo.EXPECT().GetOfficialSite(mock.Anything, discoveryArtistID).
		Return(&entity.OfficialSite{ArtistID: discoveryArtistID, URL: discoveryOfficialURL}, nil).Once()
}

// search expects one search over exactly the given series and returns result.
func (m *discoveryMocks) search(seriesIDs []string, result []*entity.SalesPhaseCandidate, err error) {
	m.searcher.EXPECT().SearchSalesPhases(mock.Anything, mock.MatchedBy(func(in *entity.SalesPhaseSearchInput) bool {
		if in.ArtistName != discoveryArtist.Name || in.OfficialSiteURL != discoveryOfficialURL || len(in.Series) != len(seriesIDs) {
			return false
		}
		for i, ref := range in.Series {
			if ref.SeriesID != seriesIDs[i] || len(ref.EventDates) == 0 {
				return false
			}
		}
		return true
	})).Return(result, err).Once()
}

func (m *discoveryMocks) record(seriesIDs []string, err error) {
	m.searchLogRepo.EXPECT().Record(mock.Anything, seriesIDs, mock.AnythingOfType("time.Time")).Return(err).Once()
}

func TestSalesPhaseDiscoveryUseCase_DiscoverForArtist(t *testing.T) {
	t.Parallel()

	now := time.Now()
	nextMonth := now.AddDate(0, 1, 0)
	candidate := &entity.SalesPhaseCandidate{
		SeriesID:       "series-a",
		Method:         entity.SalesMethodLottery,
		ApplyStartTime: now.AddDate(0, 0, 7),
		ApplyEndTime:   now.AddDate(0, 0, 14),
	}
	candidate2 := &entity.SalesPhaseCandidate{
		SeriesID:       "series-a",
		Method:         entity.SalesMethodFirstCome,
		ApplyStartTime: now.AddDate(0, 0, 20),
	}
	candidate3 := &entity.SalesPhaseCandidate{
		SeriesID:       "series-a",
		Method:         entity.SalesMethodFirstCome,
		ApplyStartTime: now.AddDate(0, 0, 25),
	}
	discovered := func(phaseID string, c *entity.SalesPhaseCandidate) entity.SalesPhaseDiscoveredData {
		return entity.SalesPhaseDiscoveredData{
			PhaseID: phaseID, SeriesID: c.SeriesID, Method: int16(c.Method), ApplyStartTime: c.ApplyStartTime,
		}
	}
	// searchedAgo returns a log of series-a last searched d ago.
	searchedAgo := func(d time.Duration) *entity.SalesPhaseSearchLog {
		return &entity.SalesPhaseSearchLog{SeriesID: "series-a", SearchedTime: now.Add(-d)}
	}
	const day = 24 * time.Hour
	searchErr := apperr.New(codes.ResourceExhausted, "spend cap reached")

	tests := []struct {
		name       string
		setup      func(m *discoveryMocks)
		wantCount  int
		wantErr    error
		wantErrMsg string
	}{
		{
			// @spec components/usecase/sales-phase/discover-for-artist "Tracked series with nothing pending"
			// @spec components/usecase/sales-phase/discover-for-artist "New phase"
			name: "Tracked series with nothing pending",
			setup: func(m *discoveryMocks) {
				m.concerts(upcomingConcert("series-a", "Tour A", nextMonth))
				m.tracked("series-a", true)
				m.phases("series-a")
				m.searchLogs([]string{"series-a"}, searchedAgo(12*day))
				m.officialSite()
				m.search([]string{"series-a"}, []*entity.SalesPhaseCandidate{candidate}, nil)
				m.salesRepo.EXPECT().Upsert(mock.Anything, candidate).Return("phase-1", entity.UpsertOutcomeInserted, nil).Once()
				m.pub.EXPECT().PublishEvent(mock.Anything, entity.SubjectSalesPhaseDiscovered, discovered("phase-1", candidate)).Return(nil).Once()
				m.record([]string{"series-a"}, nil)
			},
			wantCount: 1,
		},
		{
			// @spec components/usecase/sales-phase/discover-for-artist "Nobody tracks the series"
			name: "Nobody tracks the series",
			setup: func(m *discoveryMocks) {
				m.concerts(upcomingConcert("series-a", "Tour A", nextMonth))
				m.tracked("series-a", false)
			},
		},
		{
			// @spec components/usecase/sales-phase/discover-for-artist "A phase is still open"
			name: "A phase is still open",
			setup: func(m *discoveryMocks) {
				m.concerts(upcomingConcert("series-a", "Tour A", nextMonth))
				m.tracked("series-a", true)
				m.phases("series-a", &entity.SalesPhase{
					SeriesID: "series-a", Method: entity.SalesMethodLottery,
					ApplyStartTime: now.AddDate(0, 0, -3), ApplyEndTime: now.AddDate(0, 0, 7),
				})
			},
		},
		{
			// @spec components/usecase/sales-phase/discover-for-artist "First-come sale until sold out has opened"
			name: "First-come sale until sold out has opened",
			setup: func(m *discoveryMocks) {
				m.concerts(upcomingConcert("series-a", "Tour A", nextMonth))
				m.tracked("series-a", true)
				m.phases("series-a", &entity.SalesPhase{
					SeriesID: "series-a", Method: entity.SalesMethodFirstCome, ApplyStartTime: now.AddDate(0, 0, -1),
				})
				m.searchLogs([]string{"series-a"}, searchedAgo(11*day))
				m.officialSite()
				m.search([]string{"series-a"}, nil, nil)
				m.record([]string{"series-a"}, nil)
			},
		},
		{
			// @spec components/usecase/sales-phase/discover-for-artist "Searched recently"
			name: "Searched recently",
			setup: func(m *discoveryMocks) {
				m.concerts(upcomingConcert("series-a", "Tour A", nextMonth))
				m.tracked("series-a", true)
				m.phases("series-a")
				m.searchLogs([]string{"series-a"}, searchedAgo(5*day))
			},
		},
		{
			// @spec components/usecase/sales-phase/discover-for-artist "Events far ahead"
			name: "Events far ahead",
			setup: func(m *discoveryMocks) {
				m.concerts(upcomingConcert("series-a", "Tour A", now.AddDate(0, 10, 0)))
				m.tracked("series-a", true)
				m.phases("series-a")
				m.searchLogs([]string{"series-a"})
				m.officialSite()
				m.search([]string{"series-a"}, nil, nil)
				m.record([]string{"series-a"}, nil)
			},
		},
		{
			// @spec components/usecase/sales-phase/discover-for-artist "Two tracked series"
			name: "Two tracked series",
			setup: func(m *discoveryMocks) {
				m.concerts(
					upcomingConcert("series-a", "Tour A", nextMonth),
					upcomingConcert("series-b", "Tour B", nextMonth.AddDate(0, 0, 3)),
					upcomingConcert("series-a", "Tour A", nextMonth.AddDate(0, 0, 7)),
				)
				m.tracked("series-a", true)
				m.phases("series-a")
				m.tracked("series-b", true)
				m.phases("series-b")
				m.searchLogs([]string{"series-a", "series-b"})
				m.officialSite()
				m.search([]string{"series-a", "series-b"}, nil, nil)
				m.record([]string{"series-a", "series-b"}, nil)
			},
		},
		{
			// @spec components/usecase/sales-phase/discover-for-artist "No official site"
			name: "No official site",
			setup: func(m *discoveryMocks) {
				m.concerts(upcomingConcert("series-a", "Tour A", nextMonth))
				m.tracked("series-a", true)
				m.phases("series-a")
				m.searchLogs([]string{"series-a"})
				m.artistRepo.EXPECT().GetOfficialSite(mock.Anything, discoveryArtistID).
					Return(nil, apperr.New(codes.NotFound, "no official site")).Once()
			},
		},
		{
			name: "empty official site URL",
			setup: func(m *discoveryMocks) {
				m.concerts(upcomingConcert("series-a", "Tour A", nextMonth))
				m.tracked("series-a", true)
				m.phases("series-a")
				m.searchLogs([]string{"series-a"})
				m.artistRepo.EXPECT().GetOfficialSite(mock.Anything, discoveryArtistID).
					Return(&entity.OfficialSite{ArtistID: discoveryArtistID}, nil).Once()
			},
		},
		{
			name: "no upcoming concerts",
			setup: func(m *discoveryMocks) {
				m.concerts()
			},
		},
		{
			// @spec components/usecase/sales-phase/discover-for-artist "Search finds nothing"
			name: "Search finds nothing",
			setup: func(m *discoveryMocks) {
				m.concerts(upcomingConcert("series-a", "Tour A", nextMonth))
				m.tracked("series-a", true)
				m.phases("series-a")
				m.searchLogs([]string{"series-a"})
				m.officialSite()
				m.search([]string{"series-a"}, nil, nil)
				m.record([]string{"series-a"}, nil)
			},
		},
		{
			// @spec components/usecase/sales-phase/discover-for-artist "Search fails"
			name: "Search fails",
			setup: func(m *discoveryMocks) {
				m.concerts(upcomingConcert("series-a", "Tour A", nextMonth))
				m.tracked("series-a", true)
				m.phases("series-a")
				m.searchLogs([]string{"series-a"})
				m.officialSite()
				m.search([]string{"series-a"}, nil, searchErr)
				// No Record: the next daily run searches the series again.
			},
			wantErr: searchErr,
		},
		{
			name: "recording fails after the phases are stored",
			setup: func(m *discoveryMocks) {
				m.concerts(upcomingConcert("series-a", "Tour A", nextMonth))
				m.tracked("series-a", true)
				m.phases("series-a")
				m.searchLogs([]string{"series-a"})
				m.officialSite()
				m.search([]string{"series-a"}, []*entity.SalesPhaseCandidate{candidate}, nil)
				m.salesRepo.EXPECT().Upsert(mock.Anything, candidate).Return("phase-1", entity.UpsertOutcomeInserted, nil).Once()
				m.pub.EXPECT().PublishEvent(mock.Anything, entity.SubjectSalesPhaseDiscovered, discovered("phase-1", candidate)).Return(nil).Once()
				m.record([]string{"series-a"}, errors.New("db down"))
			},
			wantCount:  1,
			wantErrMsg: "db down",
		},
		{
			name: "trackers cannot be read",
			setup: func(m *discoveryMocks) {
				m.concerts(upcomingConcert("series-a", "Tour A", nextMonth))
				m.journeyRepo.EXPECT().ListUserIDsTrackingSeries(mock.Anything, "series-a").Return(nil, errors.New("db down")).Once()
			},
			wantErrMsg: "db down",
		},
		{
			// @spec components/usecase/sales-phase/discover-for-artist "Known phase discovered again"
			name: "Known phase discovered again",
			setup: func(m *discoveryMocks) {
				m.concerts(upcomingConcert("series-a", "Tour A", nextMonth))
				m.tracked("series-a", true)
				m.phases("series-a")
				m.searchLogs([]string{"series-a"})
				m.officialSite()
				m.search([]string{"series-a"}, []*entity.SalesPhaseCandidate{candidate}, nil)
				m.salesRepo.EXPECT().Upsert(mock.Anything, candidate).Return("phase-1", entity.UpsertOutcomeUpdated, nil).Once()
				m.record([]string{"series-a"}, nil)
			},
		},
		{
			// @spec components/usecase/sales-phase/discover-for-artist "One phase cannot be stored"
			name: "One phase cannot be stored",
			setup: func(m *discoveryMocks) {
				m.concerts(upcomingConcert("series-a", "Tour A", nextMonth))
				m.tracked("series-a", true)
				m.phases("series-a")
				m.searchLogs([]string{"series-a"})
				m.officialSite()
				m.search([]string{"series-a"}, []*entity.SalesPhaseCandidate{candidate, candidate2, candidate3}, nil)
				m.salesRepo.EXPECT().Upsert(mock.Anything, candidate).Return("phase-1", entity.UpsertOutcomeInserted, nil).Once()
				m.salesRepo.EXPECT().Upsert(mock.Anything, candidate2).Return("", entity.UpsertOutcomeSkipped, errors.New("db down")).Once()
				m.salesRepo.EXPECT().Upsert(mock.Anything, candidate3).Return("phase-3", entity.UpsertOutcomeInserted, nil).Once()
				m.pub.EXPECT().PublishEvent(mock.Anything, entity.SubjectSalesPhaseDiscovered, discovered("phase-1", candidate)).Return(nil).Once()
				m.pub.EXPECT().PublishEvent(mock.Anything, entity.SubjectSalesPhaseDiscovered, discovered("phase-3", candidate3)).Return(nil).Once()
				m.record([]string{"series-a"}, nil)
			},
			wantCount: 2,
		},
		{
			// @spec components/usecase/sales-phase/discover-for-artist "Announcement request fails"
			name: "Announcement request fails",
			setup: func(m *discoveryMocks) {
				m.concerts(upcomingConcert("series-a", "Tour A", nextMonth))
				m.tracked("series-a", true)
				m.phases("series-a")
				m.searchLogs([]string{"series-a"})
				m.officialSite()
				m.search([]string{"series-a"}, []*entity.SalesPhaseCandidate{candidate}, nil)
				m.salesRepo.EXPECT().Upsert(mock.Anything, candidate).Return("phase-1", entity.UpsertOutcomeInserted, nil).Once()
				m.pub.EXPECT().PublishEvent(mock.Anything, entity.SubjectSalesPhaseDiscovered, discovered("phase-1", candidate)).
					Return(errors.New("nats down")).Once()
				m.record([]string{"series-a"}, nil)
			},
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger, err := logging.New()
			require.NoError(t, err)
			m := newDiscoveryMocks(t)
			tt.setup(m)

			uc := usecase.NewSalesPhaseDiscoveryUseCase(
				m.concertRepo, m.artistRepo, m.salesRepo, m.searchLogRepo, m.journeyRepo,
				m.searcher, m.pub, logger,
			)
			got, err := uc.DiscoverForArtist(context.Background(), discoveryArtist)

			switch {
			case tt.wantErr != nil:
				require.ErrorIs(t, err, tt.wantErr)
			case tt.wantErrMsg != "":
				require.ErrorContains(t, err, tt.wantErrMsg)
			default:
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantCount, got)
		})
	}
}

func TestSalesPhaseSearchDue(t *testing.T) {
	t.Parallel()

	jst := time.FixedZone("JST", 9*60*60)
	searched := time.Date(2026, 10, 7, 21, 0, 30, 0, jst)

	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{
			// @spec components/usecase/sales-phase/discover-for-artist "Ten days counted by date"
			name: "Ten days counted by date",
			now:  time.Date(2026, 10, 17, 21, 0, 0, 0, jst),
			want: true,
		},
		{
			// @spec components/usecase/sales-phase/discover-for-artist "Nine days counted by date"
			name: "Nine days counted by date",
			now:  time.Date(2026, 10, 16, 23, 59, 0, 0, jst),
			want: false,
		},
		{
			name: "dates are taken in Japan time, not UTC",
			// 17 October 08:59 JST is still 16 October in UTC.
			now:  time.Date(2026, 10, 16, 23, 59, 0, 0, time.UTC),
			want: true,
		},
		{
			name: "same day",
			now:  time.Date(2026, 10, 7, 23, 0, 0, 0, jst),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, usecase.ExportedSalesPhaseSearchDue(searched, tt.now))
		})
	}
}
