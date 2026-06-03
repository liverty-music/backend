package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const testMerchWindow = 60 * 24 * time.Hour

type merchTestDeps struct {
	seriesRepo *mocks.MockSeriesRepository
	searcher   *mocks.MockMerchSearcher
	checker    *mocks.MockMerchLivenessChecker
	uc         usecase.MerchDiscoveryUseCase
}

func newMerchTestDeps(t *testing.T) *merchTestDeps {
	t.Helper()
	d := &merchTestDeps{
		seriesRepo: mocks.NewMockSeriesRepository(t),
		searcher:   mocks.NewMockMerchSearcher(t),
		checker:    mocks.NewMockMerchLivenessChecker(t),
	}
	d.uc = usecase.NewMerchDiscoveryUseCase(d.seriesRepo, d.searcher, d.checker, testMerchWindow, newTestLogger(t))
	return d
}

func candidate(merchURL string) *entity.MerchCandidate {
	return &entity.MerchCandidate{
		SeriesID:    "01890000-0000-7000-8000-000000000001",
		SeriesTitle: "Summer Tour 2026",
		ArtistName:  "Test Artist",
		MerchURL:    merchURL,
	}
}

func TestMerchDiscoveryUseCase_ListCandidates(t *testing.T) {
	t.Parallel()

	d := newMerchTestDeps(t)
	want := []*entity.MerchCandidate{candidate("")}
	// The configured window is passed straight through to the repository.
	d.seriesRepo.EXPECT().ListSeriesInMerchWindow(mock.Anything, testMerchWindow).Return(want, nil)

	got, err := d.uc.ListCandidates(context.Background())
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestMerchDiscoveryUseCase_ResolveMerchURL(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const (
		officialURL = "https://artist.example.com/goods"
		socialURL   = "https://x.com/test_artist/status/123456789"
		deadURL     = "https://old.example.com/dead"
		liveURL     = "https://artist.example.com/live"
	)

	tests := []struct {
		name        string
		merchURL    string
		setup       func(d *merchTestDeps)
		wantOutcome usecase.MerchOutcome
		wantErr     bool
	}{
		{
			name:     "empty field, found official url, persisted",
			merchURL: "",
			setup: func(d *merchTestDeps) {
				d.searcher.EXPECT().SearchMerchURL(mock.Anything, "Test Artist", "Summer Tour 2026").Return(officialURL, nil)
				d.seriesRepo.EXPECT().SetMerchURL(mock.Anything, mock.Anything, officialURL).Return(nil)
			},
			wantOutcome: usecase.MerchOutcomeFilled,
		},
		{
			name:     "empty field, social-media post is acceptable",
			merchURL: "",
			setup: func(d *merchTestDeps) {
				d.searcher.EXPECT().SearchMerchURL(mock.Anything, mock.Anything, mock.Anything).Return(socialURL, nil)
				d.seriesRepo.EXPECT().SetMerchURL(mock.Anything, mock.Anything, socialURL).Return(nil)
			},
			wantOutcome: usecase.MerchOutcomeFilled,
		},
		{
			name:     "empty field, no confident source",
			merchURL: "",
			setup: func(d *merchTestDeps) {
				d.searcher.EXPECT().SearchMerchURL(mock.Anything, mock.Anything, mock.Anything).Return("", nil)
				// no SetMerchURL expected
			},
			wantOutcome: usecase.MerchOutcomeNoSource,
		},
		{
			name:     "empty field, invalid url discarded",
			merchURL: "",
			setup: func(d *merchTestDeps) {
				d.searcher.EXPECT().SearchMerchURL(mock.Anything, mock.Anything, mock.Anything).Return("not-a-url", nil)
				// no SetMerchURL expected
			},
			wantOutcome: usecase.MerchOutcomeInvalidDiscarded,
		},
		{
			name:     "non-empty field, still live, left unchanged",
			merchURL: liveURL,
			setup: func(d *merchTestDeps) {
				d.checker.EXPECT().IsDeadLink(mock.Anything, liveURL).Return(false)
				// no clear, no search, no set
			},
			wantOutcome: usecase.MerchOutcomeAlreadyLive,
		},
		{
			name:     "non-empty field, dead link cleared and re-searched",
			merchURL: deadURL,
			setup: func(d *merchTestDeps) {
				d.checker.EXPECT().IsDeadLink(mock.Anything, deadURL).Return(true)
				d.seriesRepo.EXPECT().ClearMerchURL(mock.Anything, mock.Anything).Return(nil)
				d.searcher.EXPECT().SearchMerchURL(mock.Anything, mock.Anything, mock.Anything).Return(officialURL, nil)
				d.seriesRepo.EXPECT().SetMerchURL(mock.Anything, mock.Anything, officialURL).Return(nil)
			},
			wantOutcome: usecase.MerchOutcomeFilled,
		},
		{
			name:     "non-empty field, dead link cleared but no new source",
			merchURL: deadURL,
			setup: func(d *merchTestDeps) {
				d.checker.EXPECT().IsDeadLink(mock.Anything, deadURL).Return(true)
				d.seriesRepo.EXPECT().ClearMerchURL(mock.Anything, mock.Anything).Return(nil)
				d.searcher.EXPECT().SearchMerchURL(mock.Anything, mock.Anything, mock.Anything).Return("", nil)
			},
			wantOutcome: usecase.MerchOutcomeNoSource,
		},
		{
			name:     "search failure surfaces as error",
			merchURL: "",
			setup: func(d *merchTestDeps) {
				d.searcher.EXPECT().SearchMerchURL(mock.Anything, mock.Anything, mock.Anything).Return("", errors.New("gemini down"))
			},
			wantErr: true,
		},
		{
			name:     "clear failure surfaces as error",
			merchURL: deadURL,
			setup: func(d *merchTestDeps) {
				d.checker.EXPECT().IsDeadLink(mock.Anything, deadURL).Return(true)
				d.seriesRepo.EXPECT().ClearMerchURL(mock.Anything, mock.Anything).Return(errors.New("db down"))
			},
			wantErr: true,
		},
		{
			name:     "persist failure surfaces as error",
			merchURL: "",
			setup: func(d *merchTestDeps) {
				d.searcher.EXPECT().SearchMerchURL(mock.Anything, mock.Anything, mock.Anything).Return(officialURL, nil)
				d.seriesRepo.EXPECT().SetMerchURL(mock.Anything, mock.Anything, officialURL).Return(errors.New("db down"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newMerchTestDeps(t)
			tt.setup(d)

			outcome, err := d.uc.ResolveMerchURL(ctx, candidate(tt.merchURL))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantOutcome, outcome)
		})
	}
}

func TestValidMerchURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"https official site", "https://artist.example.com/goods", true},
		{"http allowed", "http://artist.example.com/goods", true},
		{"social media post", "https://x.com/test_artist/status/123", true},
		{"empty", "", false},
		{"no scheme", "artist.example.com/goods", false},
		{"non-http scheme", "ftp://artist.example.com/file", false},
		{"scheme only, no host", "https://", false},
		{"too long", "https://example.com/" + strings.Repeat("a", 2048), false},
		{"loopback IP host (SSRF)", "http://127.0.0.1/goods", false},
		{"private IP host 10.x (SSRF)", "http://10.0.0.5/goods", false},
		{"private IP host 192.168 (SSRF)", "http://192.168.1.1/", false},
		{"cloud metadata IP (SSRF)", "http://169.254.169.254/latest/meta-data/", false},
		{"ipv6 loopback (SSRF)", "http://[::1]/goods", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, usecase.ExportedValidMerchURL(tt.url))
		})
	}
}
