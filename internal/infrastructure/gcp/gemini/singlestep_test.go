package gemini_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSingleStepJSON(t *testing.T) {
	t.Parallel()

	raw := "```json\n" + `{
  "series": [
    {"title": "TOUR 2027", "source_url": "https://example.com/tour", "events": [
      {"venue": "大阪城ホール", "country": "JP", "admin_area": "JP-27", "local_date": "2027-10-02", "open_time": "2027-10-02T17:00:00+09:00", "start_time": "2027-10-02T18:00:00+09:00"},
      {"venue": "Taipei Arena", "country": "TW", "admin_area": "TW-TPE", "local_date": "2027-10-31", "open_time": "", "start_time": "2027-10-31T19:00:00+08:00"}
    ]},
    {"title": "武道館", "source_url": "https://example.com/news/1", "events": [
      {"venue": "日本武道館", "country": "JP", "admin_area": "JP-13", "local_date": "2027-12-26", "open_time": "", "start_time": ""}
    ]}
  ]
}` + "\n```"

	drafts, err := gemini.ParseSingleStepJSON(raw)
	require.NoError(t, err)

	require.Len(t, drafts, 3)
	assert.Equal(t, gemini.EventDraft{
		Title: "TOUR 2027", SourceURL: "https://example.com/tour", Venue: "大阪城ホール", Country: "JP", AdminArea: "JP-27",
		LocalDate: "2027-10-02", StartTime: "2027-10-02T18:00:00+09:00", OpenTime: "2027-10-02T17:00:00+09:00",
		Group: 1,
	}, drafts[0])
	assert.Equal(t, 1, drafts[1].Group)
	assert.Equal(t, "TW-TPE", drafts[1].AdminArea)
	assert.Equal(t, 2, drafts[2].Group, "each series entry gets its own group")
	assert.Equal(t, "2027-12-26", drafts[2].LocalDate)
}

func TestParseSingleStepJSON_Invalid(t *testing.T) {
	t.Parallel()

	_, err := gemini.ParseSingleStepJSON(`{"series": [`)
	assert.ErrorIs(t, err, gemini.ErrInvalidJSON)

	_, err = gemini.ParseSingleStepJSON(`{"series": "not an array"}`)
	assert.ErrorIs(t, err, gemini.ErrInvalidJSON)
}
