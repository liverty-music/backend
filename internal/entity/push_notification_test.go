package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConcertNotificationPayload(t *testing.T) {
	t.Parallel()

	artist := &entity.Artist{
		ID:   "artist-123",
		Name: "YOASOBI",
	}

	tests := []struct {
		name         string
		artist       *entity.Artist
		concertCount int
		wantTitle    string
		wantBody     string
		wantURL      string
		wantTag      string
	}{
		{
			name:         "single concert",
			artist:       artist,
			concertCount: 1,
			wantTitle:    "YOASOBI",
			wantBody:     "1 new concerts found",
			wantURL:      "/concerts?artist=artist-123",
			wantTag:      "concert-artist-123",
		},
		{
			name:         "multiple concerts",
			artist:       artist,
			concertCount: 5,
			wantTitle:    "YOASOBI",
			wantBody:     "5 new concerts found",
			wantURL:      "/concerts?artist=artist-123",
			wantTag:      "concert-artist-123",
		},
		{
			name:         "artist ID is embedded in URL and tag",
			artist:       &entity.Artist{ID: "abc-xyz", Name: "Test Artist"},
			concertCount: 2,
			wantTitle:    "Test Artist",
			wantBody:     "2 new concerts found",
			wantURL:      "/concerts?artist=abc-xyz",
			wantTag:      "concert-abc-xyz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := entity.NewConcertNotificationPayload(tt.artist, tt.concertCount)
			require.NotNil(t, got)
			assert.Equal(t, tt.wantTitle, got.Title)
			assert.Equal(t, tt.wantBody, got.Body)
			assert.Equal(t, tt.wantURL, got.URL)
			assert.Equal(t, tt.wantTag, got.Tag)
		})
	}
}
