package usecase_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestVenueAreaBackfill_Run(t *testing.T) {
	t.Parallel()

	venues := []*entity.VenueToBackfill{
		{VenueID: "v-tokyo", ListedName: "Zepp Haneda", SourceURL: "https://example.com/tour", Artists: "Band A", EventCount: 2, FirstEventDate: "2026-12-01"},
		{VenueID: "v-texas", ListedName: "Stubb's", AdminArea: "US-TX", SourceURL: "https://example.com/us", Artists: "Band B / Band C", EventCount: 1, FirstEventDate: "2026-10-11"},
		{VenueID: "v-wrongzone", ListedName: "Gocheok Sky Dome", SourceURL: "https://example.com/kr", EventCount: 1, FirstEventDate: "2027-01-05"},
		{VenueID: "v-unknown", ListedName: "Somewhere", SourceURL: "https://example.com/x", EventCount: 1, FirstEventDate: "2026-11-30"},
		{VenueID: "v-nosource", ListedName: "Legacy Hall", Artists: "Band D", EventCount: 3, FirstEventDate: "2026-09-01"},
		{VenueID: "v-unused", ListedName: "Unused Hall", SourceURL: "https://example.com/unused"},
		{VenueID: "v-down", ListedName: "Down Hall", SourceURL: "https://example.com/down", EventCount: 1, FirstEventDate: "2026-12-24"},
	}
	tokyo := &entity.Area{Code: "JP-13", Kind: entity.AreaKindSubdivision, CountryCode: "JP", TimeZone: new("Asia/Tokyo")}
	texas := &entity.Area{Code: "US-TX", Kind: entity.AreaKindSubdivision, CountryCode: "US"}
	seoul := &entity.Area{Code: "KR-11", Kind: entity.AreaKindSubdivision, CountryCode: "KR", TimeZone: new("Asia/Seoul")}

	repo := mocks.NewMockVenueRepository(t)
	repo.EXPECT().ListWithoutAreaOrZone(mock.Anything).Return(venues, nil).Once()
	areas := mocks.NewMockAreaRepository(t)
	areas.EXPECT().Resolve(mock.Anything, "東京都", (*string)(nil)).Return(tokyo, nil).Once()
	areas.EXPECT().Resolve(mock.Anything, "US-TX", (*string)(nil)).Return(texas, nil).Once()
	areas.EXPECT().Resolve(mock.Anything, "KR-11", (*string)(nil)).Return(seoul, nil).Once()
	areas.EXPECT().Resolve(mock.Anything, "Atlantis", (*string)(nil)).Return(nil, apperr.New(codes.NotFound, "no area")).Once()
	areas.EXPECT().ListByCountry(mock.Anything, "US").Return(&entity.CountryAreas{
		TimeZones: []*entity.AreaTimeZone{{Name: "America/Chicago"}, {Name: "America/Denver"}},
	}, nil).Once()
	asker := mocks.NewMockVenueAreaAsker(t)
	asker.EXPECT().AskVenueArea(mock.Anything, "Zepp Haneda", "https://example.com/tour").
		Return(&entity.VenueAreaAnswer{AdminArea: "東京都", TimeZone: "Asia/Tokyo"}, nil).Once()
	asker.EXPECT().AskVenueArea(mock.Anything, "Stubb's", "https://example.com/us").
		Return(&entity.VenueAreaAnswer{AdminArea: "US-TX", TimeZone: "America/Chicago"}, nil).Once()
	asker.EXPECT().AskVenueArea(mock.Anything, "Gocheok Sky Dome", "https://example.com/kr").
		Return(&entity.VenueAreaAnswer{AdminArea: "KR-11", TimeZone: "Asia/Tokyo"}, nil).Once()
	asker.EXPECT().AskVenueArea(mock.Anything, "Somewhere", "https://example.com/x").
		Return(&entity.VenueAreaAnswer{AdminArea: "Atlantis", TimeZone: "Europe/Atlantis"}, nil).Once()
	asker.EXPECT().AskVenueArea(mock.Anything, "Down Hall", "https://example.com/down").
		Return(nil, apperr.New(codes.Unavailable, "gemini down")).Once()
	logger, err := logging.New()
	require.NoError(t, err)

	var out bytes.Buffer
	// The repository mock expects no write: the backfill stores nothing.
	err = usecase.NewVenueAreaBackfill(repo, areas, asker, logger).Run(context.Background(), &out)

	// The failed call is reported after every row is written.
	assert.ErrorIs(t, err, apperr.ErrUnavailable)
	rows, readErr := csv.NewReader(&out).ReadAll()
	require.NoError(t, readErr)
	assert.Equal(t, [][]string{
		usecase.VenueAreaBackfillColumns,
		{"v-tokyo", "Zepp Haneda", "", "https://example.com/tour", "Band A", "2", "2026-12-01", "東京都", "JP-13", "Asia/Tokyo", "Asia/Tokyo"},
		{"v-texas", "Stubb's", "US-TX", "https://example.com/us", "Band B / Band C", "1", "2026-10-11", "US-TX", "US-TX", "America/Chicago", "America/Chicago"},
		{"v-wrongzone", "Gocheok Sky Dome", "", "https://example.com/kr", "", "1", "2027-01-05", "KR-11", "KR-11", "Asia/Tokyo", ""},
		{"v-unknown", "Somewhere", "", "https://example.com/x", "", "1", "2026-11-30", "Atlantis", "", "Europe/Atlantis", ""},
		// No source page: nothing to ask, and the reviewer fills it in.
		{"v-nosource", "Legacy Hall", "", "", "Band D", "3", "2026-09-01", "", "", "", ""},
		// No event: unused, so it is not asked about.
		{"v-unused", "Unused Hall", "", "https://example.com/unused", "", "0", "", "", "", "", ""},
		{"v-down", "Down Hall", "", "https://example.com/down", "", "1", "2026-12-24", "", "", "", ""},
	}, rows)
}

func TestVenueAreaBackfill_Run_StopsWithEveryRowWritten(t *testing.T) {
	t.Parallel()

	repo := mocks.NewMockVenueRepository(t)
	repo.EXPECT().ListWithoutAreaOrZone(mock.Anything).Return([]*entity.VenueToBackfill{
		{VenueID: "v-1", ListedName: "Legacy Hall"},
		{VenueID: "v-2", ListedName: "Zepp Haneda", SourceURL: "https://example.com/tour", EventCount: 1},
	}, nil).Once()
	areas := mocks.NewMockAreaRepository(t)
	areas.EXPECT().Resolve(mock.Anything, "東京都", (*string)(nil)).Return(nil, apperr.New(codes.Unavailable, "db down")).Once()
	asker := mocks.NewMockVenueAreaAsker(t)
	asker.EXPECT().AskVenueArea(mock.Anything, "Zepp Haneda", "https://example.com/tour").
		Return(&entity.VenueAreaAnswer{AdminArea: "東京都", TimeZone: "Asia/Tokyo"}, nil).Once()
	logger, err := logging.New()
	require.NoError(t, err)

	var out bytes.Buffer
	err = usecase.NewVenueAreaBackfill(repo, areas, asker, logger).Run(context.Background(), &out)

	assert.ErrorIs(t, err, apperr.ErrUnavailable)
	rows, readErr := csv.NewReader(&out).ReadAll()
	require.NoError(t, readErr)
	assert.Equal(t, [][]string{usecase.VenueAreaBackfillColumns, {"v-1", "Legacy Hall", "", "", "", "0", "", "", "", "", ""}}, rows,
		"the rows before the failure are flushed")
}
