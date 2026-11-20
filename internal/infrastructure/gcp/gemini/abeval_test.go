package gemini_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

func TestLoadGroundTruth_FixtureWellFormed(t *testing.T) {
	t.Parallel()

	gt, err := gemini.LoadGroundTruth()
	require.NoError(t, err)

	assert.Equal(t, "2026-11-20", gt.EvaluationFrom)
	assert.NotEmpty(t, gt.CaptureTime)
	require.Len(t, gt.Artists, 7, "fixture must cover 7 artists")

	wantArtists := map[string]string{
		"UVERworld":      "019e302a-01ec-7b8e-80e4-2111c2e4fe0a",
		"Vaundy":         "019e3029-8044-75f7-ad29-c1748b5a46a6",
		"SUPER BEAVER":   "019e302a-3ea8-7a7e-b077-8cf6068fdb4b",
		"BRADIO":         "019e4d59-98fa-7d1d-9689-e02398fbbb86",
		"BIGMAMA":        "019fdb16-3136-7792-91ff-12fa6a6b7531",
		"go!go!vanillas": "019e302a-59a0-7b0b-bb84-f0cc77caf797",
		"[Alexandros]":   "019e302a-01ec-7b82-921d-49b3903dacb7",
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
