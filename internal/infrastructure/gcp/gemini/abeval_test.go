package gemini_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadGroundTruth_FixtureWellFormed(t *testing.T) {
	t.Parallel()

	gt, err := gemini.LoadGroundTruth()
	require.NoError(t, err)

	assert.Equal(t, "2026-05-20", gt.EvaluationFrom)
	assert.NotEmpty(t, gt.CapturedAt)
	require.Len(t, gt.Artists, 3, "fixture must cover 3 artists")

	wantArtists := map[string]string{
		"UVERworld":    "019e302a-01ec-7b8e-80e4-2111c2e4fe0a",
		"Vaundy":       "019e3029-8044-75f7-ad29-c1748b5a46a6",
		"SUPER BEAVER": "019e302a-3ea8-7a7e-b077-8cf6068fdb4b",
	}

	for _, a := range gt.Artists {
		t.Run(a.Name, func(t *testing.T) {
			wantID, ok := wantArtists[a.Name]
			require.True(t, ok, "unexpected artist: %s", a.Name)
			assert.Equal(t, wantID, a.ID)

			_, err := uuid.Parse(a.ID)
			assert.NoError(t, err, "artist id must be a well-formed UUID")

			assert.NotEmpty(t, a.OfficialSiteURL)
			assert.NotEmpty(t, a.Events, "artist must have at least one event")

			for i, ev := range a.Events {
				assert.NotEmpty(t, ev.EventName, "event %d: event_name", i)
				assert.NotEmpty(t, ev.LocalDate, "event %d: local_date", i)
				assert.NotEmpty(t, ev.SourceURL, "event %d: source_url", i)
				assert.Contains(t, []string{"confirmed", "tentative"}, ev.Confidence,
					"event %d: confidence must be confirmed/tentative", i)
				assert.Contains(t, []string{"public", "members-only"}, ev.Visibility,
					"event %d: visibility must be public/members-only", i)
			}
		})
	}
}
