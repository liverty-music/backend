package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// analyticsLabelsKey is the log field that carries the analytics mark.
const analyticsLabelsKey = "logging.googleapis.com/labels"

func TestParseMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr string
	}{
		{name: "no flag runs the daily search", args: nil, want: modeSearch},
		{name: "search", args: []string{"-mode=search"}, want: modeSearch},
		{name: "official page check", args: []string{"-mode=official-page-check"}, want: modeOfficialPageCheck},
		{name: "unknown mode names the allowed values", args: []string{"-mode=crawl"}, wantErr: `unknown -mode "crawl" (allowed: "search", "official-page-check")`},
		{name: "unknown flag", args: []string{"-dry-run"}, wantErr: "flag provided but not defined: -dry-run"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseMode(tt.args)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPageChecker_Run(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger, err := logging.New(logging.WithWriter(&logs), logging.WithFormat(logging.FormatJSON))
	require.NoError(t, err)

	artists := mocks.NewMockArtistRepository(t)
	organizers := mocks.NewMockOrganizerRepository(t)
	for _, id := range []string{"checked", "blocked", "no-site", "db-error", "represented"} {
		organizers.EXPECT().IsArtistRepresentedByActiveOrganizer(mock.Anything, id).Return(id == "represented", nil)
	}
	organizers.EXPECT().IsArtistRepresentedByActiveOrganizer(mock.Anything, "unknown-rep").
		Return(false, apperr.New(codes.Unavailable, "db down"))
	artists.EXPECT().GetOfficialSite(mock.Anything, "checked").Return(&entity.OfficialSite{URL: "https://checked.example/"}, nil)
	artists.EXPECT().GetOfficialSite(mock.Anything, "blocked").Return(&entity.OfficialSite{URL: "https://blocked.example/"}, nil)
	artists.EXPECT().GetOfficialSite(mock.Anything, "unknown-rep").Return(&entity.OfficialSite{URL: "https://unknown-rep.example/"}, nil)
	artists.EXPECT().GetOfficialSite(mock.Anything, "no-site").Return(nil, apperr.ErrNotFound)
	artists.EXPECT().GetOfficialSite(mock.Anything, "db-error").Return(nil, apperr.New(codes.Unavailable, "db down"))

	var calls atomic.Int32
	checker := &pageChecker{
		artists:    artists,
		organizers: organizers,
		check: func(_ context.Context, siteURL string) []gemini.OfficialPageCheck {
			calls.Add(1)
			switch siteURL {
			case "https://checked.example/", "https://unknown-rep.example/":
				return []gemini.OfficialPageCheck{
					{URL: siteURL, Kind: gemini.PageKindTop, Status: gemini.PageStatusOK, HTTPStatus: 200, TextSHA256: "aa", TextLength: 120},
					{URL: siteURL + "live", Kind: gemini.PageKindLinked, Status: gemini.PageStatusOK, HTTPStatus: 200, TextSHA256: "bb", TextLength: 80},
					{URL: siteURL + "gone", Kind: gemini.PageKindLinked, Status: gemini.PageStatusHTTPError, HTTPStatus: 404},
				}
			default:
				return []gemini.OfficialPageCheck{{URL: siteURL, Kind: gemini.PageKindTop, Status: gemini.PageStatusHTTPError, HTTPStatus: 403}}
			}
		},
		logger: logger,
	}

	followed := []*entity.Artist{
		{ID: "checked"}, {ID: "blocked"}, {ID: "no-site"}, {ID: "db-error"},
		{ID: "ended", ActivityEnded: true}, {ID: "represented"}, {ID: "unknown-rep"},
	}
	result := checker.run(context.Background(), followed)
	logPageCheckSummary(context.Background(), logger, len(followed), result)

	assert.Equal(t, int32(3), calls.Load(), "only artists with an official site the search would search are read")
	assert.Equal(t, 4, result.Artists, "checked, blocked, db-error and unknown-rep were reached")
	assert.Equal(t, 2, result.FailedTop, "blocked answered 403 and db-error has no readable site")
	assert.Equal(t, 7, result.Pages)
	assert.Equal(t, 3, result.FailedPages)

	pages := map[string]map[string]any{}
	var summary map[string]any
	for line := range bytes.Lines(logs.Bytes()) {
		var entry map[string]any
		require.NoError(t, json.Unmarshal(line, &entry))
		switch entry["msg"] {
		case "official page checked":
			pages[entry["page_url"].(string)] = entry
			assert.NotContains(t, entry, analyticsLabelsKey, "a page line stays in the operational logs")
		case "official page check complete":
			summary = entry
		}
	}
	require.Len(t, pages, 7)
	assert.Subset(t, pages["https://checked.example/"], map[string]any{
		"artist_id": "checked", "page_kind": "top", "status": "ok", "http_status": float64(200), "text_sha256": "aa", "text_length": float64(120),
	})
	assert.Subset(t, pages["https://checked.example/gone"], map[string]any{
		"artist_id": "checked", "page_kind": "linked", "status": "http_error", "http_status": float64(404), "text_sha256": "", "text_length": float64(0),
	})
	require.NotNil(t, summary)
	assert.Subset(t, summary, map[string]any{
		"followed_artists": float64(7), "artists_attempted": float64(4), "artists_succeeded": float64(2), "failures": float64(2),
		"circuit_broken": false, "pages_checked": float64(7), "pages_failed": float64(3),
	})
	assert.Equal(t, map[string]any{"sink": "analytics", "topic": "job_result"}, summary[analyticsLabelsKey])
}

func TestPageChecker_RunStopsOnCancel(t *testing.T) {
	t.Parallel()

	logger, err := logging.New(logging.WithWriter(&bytes.Buffer{}))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	checker := &pageChecker{
		artists:    mocks.NewMockArtistRepository(t),
		organizers: mocks.NewMockOrganizerRepository(t),
		check: func(context.Context, string) []gemini.OfficialPageCheck {
			t.Error("no site is read after cancellation")
			return nil
		},
		logger: logger,
	}

	result := checker.run(ctx, []*entity.Artist{{ID: "a"}})

	assert.Zero(t, result.Artists)
	assert.True(t, errors.Is(ctx.Err(), context.Canceled))
}
