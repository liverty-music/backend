package gemini_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeVenue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		// Prefecture prefix ("PREFECTURE・<venue>")
		{"prefix_osaka", "大阪府・Billborad Live OSAKA", "billborad live osaka"},
		{"prefix_tokyo", "東京都・Billborad Live TOKYO", "billborad live tokyo"},
		{"prefix_kyoto", "京都府・磔磔", "磔磔"},
		{"prefix_saitama", "埼玉県・HEAVEN'S ROCK さいたま新都心 VJ-3", "heaven's rock さいたま新都心 vj 3"},

		// Parenthesised prefecture suffix
		{"paren_chiba", "幕張メッセ 9・11ホール（千葉県）", "幕張メッセ 9 11ホール"},
		{"paren_ascii", "Zepp Haneda (東京都)", "zepp haneda"},

		// Both — prefix and inner paren (rare but possible)
		{"prefix_and_paren", "大阪府・京セラドーム大阪（大阪府）", "京セラドーム大阪"},

		// Prefecture embedded in venue name MUST NOT be stripped — only
		// matches the regex pattern (prefix `・` or parenthesised), so
		// "新潟県民会館" stays intact.
		{"embedded_safe", "新潟県民会館", "新潟県民会館"},

		// TBD markers collapse to empty
		{"tbd_empty", "", ""},
		{"tbd_dashed", "-STAY TUNED-", ""},
		{"tbd_japanese", "未定", ""},
		{"tbd_tba", "TBA", ""},
		{"tbd_coming_soon", "Coming Soon", ""},

		// Original punctuation / whitespace handling preserved
		{"basic_lowercase", "Zepp Tokyo", "zepp tokyo"},
		{"middle_dot_split", "幕張メッセ 9・11ホール", "幕張メッセ 9 11ホール"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := gemini.NormalizeVenue(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLoadGroundTruth_FixtureWellFormed(t *testing.T) {
	t.Parallel()

	gt, err := gemini.LoadGroundTruth()
	require.NoError(t, err)

	assert.Equal(t, "2026-05-20", gt.EvaluationFrom)
	assert.NotEmpty(t, gt.CapturedAt)
	require.Len(t, gt.Artists, 4, "fixture must cover 4 artists")

	wantArtists := map[string]string{
		"UVERworld":    "019e302a-01ec-7b8e-80e4-2111c2e4fe0a",
		"Vaundy":       "019e3029-8044-75f7-ad29-c1748b5a46a6",
		"SUPER BEAVER": "019e302a-3ea8-7a7e-b077-8cf6068fdb4b",
		"BRADIO":       "019e4d59-98fa-7d1d-9689-e02398fbbb86",
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
