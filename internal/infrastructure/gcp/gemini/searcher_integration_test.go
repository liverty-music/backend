//go:build integration

package gemini_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/pannpers/go-logging/logging"
)

func TestConcertSearcher_Search_Real(t *testing.T) {
	t.Skip("Skipping GCP integration test - requires real API access and credentials")

	// This test requires GCP credentials (ADC) and real API access.
	ctx := context.Background()
	cfg := gemini.Config{
		ProjectID:   "liverty-music-dev",
		Location:    "global",
		ModelName:   "gemini-3-flash-preview",
		DataStoreID: "projects/liverty-music-dev/locations/global/collections/default_collection/dataStores/official-artist-site",
	}

	logger, _ := logging.New(
		logging.WithLevel(slog.LevelDebug),
		logging.WithFormat(logging.FormatJSON),
	)
	s, _ := gemini.NewConcertSearcher(ctx, cfg, nil, logger) // Pass nil for Real test

	artist := &entity.Artist{ID: "artist-uverworld", Name: "UVERworld"}
	officialSite := &entity.OfficialSite{URL: "https://www.uverworld.jp/"}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	discovered, _ := s.Search(ctx, artist, officialSite, from)

	t.Logf("Found %d concerts for %s (with official site)", len(discovered), artist.Name)
	for _, c := range discovered {
		st := "nil"
		if c.StartTime != nil {
			st = c.StartTime.Format(time.RFC3339)
		}
		t.Logf("  - %s (%s) @ %s [Source: %s]", c.Title, c.LocalEventDate.Format("2006-01-02"), st, c.SourceURL)
	}

	// Nil-site path: no known URL, Gemini searches by artist name only.
	discoveredNoSite, _ := s.Search(ctx, artist, nil, from)

	t.Logf("Found %d concerts for %s (no official site)", len(discoveredNoSite), artist.Name)
	for _, c := range discoveredNoSite {
		st := "nil"
		if c.StartTime != nil {
			st = c.StartTime.Format(time.RFC3339)
		}
		t.Logf("  - %s (%s) @ %s [Source: %s]", c.Title, c.LocalEventDate.Format("2006-01-02"), st, c.SourceURL)
	}
}
