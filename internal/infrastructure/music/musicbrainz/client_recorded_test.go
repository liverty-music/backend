package musicbrainz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClient_GetArtist_RecordedResponses reads artists from responses that
// MusicBrainz returned, not from JSON the test builds: a field the client
// reads under a name MusicBrainz does not use fails here, and the live API,
// which resets connections in CI, is not called.
//
// The files in testdata are the responses of
// `curl 'https://musicbrainz.org/ws/2/artist/<mbid>?fmt=json'`, recorded on
// 2026-11-21 and indented.
func TestClient_GetArtist_RecordedResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		mbid          string
		wantName      string
		activityEnded bool
	}{
		// @spec components/entity/artist/get-artist "Known MBID"
		{name: "Radiohead", mbid: "a74b1b7f-71a5-4011-9441-d0b5e4122711", wantName: "Radiohead"},
		// @spec components/entity/artist/get-artist "Activity not ended"
		{name: "UVERworld", mbid: "a107bff6-58da-4302-83ad-317e86a1811c", wantName: "UVERworld"},
		// @spec components/entity/artist/get-artist "Ended activity"
		{name: "The Beatles", mbid: "b10bbbfc-cf9e-42e0-be17-e2c3e1d2600d", wantName: "The Beatles", activityEnded: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body, err := os.ReadFile(filepath.Join("testdata", "artist-"+tt.mbid+".json"))
			require.NoError(t, err)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.TrimPrefix(r.URL.Path, "/") != tt.mbid {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
			}))
			t.Cleanup(server.Close)
			client := musicbrainz.NewClient(server.Client(), testLogger(t))
			client.SetBaseURL(server.URL + "/")

			artist, err := client.GetArtist(context.Background(), tt.mbid)

			require.NoError(t, err)
			assert.Equal(t, tt.wantName, artist.Name)
			assert.Equal(t, tt.mbid, artist.MBID)
			assert.Equal(t, tt.activityEnded, artist.ActivityEnded)
		})
	}
}
