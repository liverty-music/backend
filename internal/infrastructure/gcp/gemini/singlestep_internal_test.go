package gemini

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSingleStepJSON(t *testing.T) {
	t.Parallel()

	raw := "```json\n" + `{
  "tours": [{"title": "TOUR 2027", "source_url": "https://example.com/tour", "events": [
    {"venue": "大阪城ホール", "country": "JP", "admin_area": "大阪府", "local_date": "2027-10-02", "open_time": "2027-10-02T17:00:00+09:00", "start_time": "2027-10-02T18:00:00+09:00"},
    {"venue": "Taipei Arena", "country": "TW", "admin_area": "", "local_date": "2027-10-31", "open_time": "", "start_time": "2027-10-31T19:00:00+08:00"}
  ]}],
  "standalones": [{"title": "武道館", "source_url": "https://example.com/news/1", "events": [
    {"venue": "日本武道館", "country": "JP", "admin_area": "東京都", "local_date": "2027-12-26", "open_time": "", "start_time": ""}
  ]}]
}` + "\n```"

	drafts, coerced, err := parseSingleStepJSON(raw)
	require.NoError(t, err)

	require.Len(t, drafts, 3)
	assert.Equal(t, EventDraft{
		Title: "TOUR 2027", SourceURL: "https://example.com/tour", Venue: "大阪城ホール", Country: "JP",
		LocalDate: "2027-10-02", StartTime: "2027-10-02T18:00:00+09:00", OpenTime: "2027-10-02T17:00:00+09:00",
		IsTour: true, TourGroup: 1,
	}, drafts[0])
	assert.True(t, drafts[1].IsTour)
	assert.Equal(t, 1, drafts[1].TourGroup)
	assert.False(t, drafts[2].IsTour)
	assert.Equal(t, 0, drafts[2].TourGroup)

	var resp step2Response
	require.NoError(t, json.Unmarshal([]byte(coerced), &resp))
	require.Len(t, resp.Events, 3)
	for i, ev := range resp.Events {
		assert.Equal(t, i, ev.Index)
	}
	assert.Equal(t, "大阪府", resp.Events[0].AdminArea)
	assert.Equal(t, "2027-10-31T19:00:00+08:00", resp.Events[1].StartTime)
	assert.Equal(t, "2027-12-26", resp.Events[2].LocalDate)
}

func TestParseSingleStepJSON_Invalid(t *testing.T) {
	t.Parallel()

	_, _, err := parseSingleStepJSON(`{"tours": [`)
	assert.Error(t, err)
}
