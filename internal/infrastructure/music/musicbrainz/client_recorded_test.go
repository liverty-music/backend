package musicbrainz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
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

// TestClient_ListNamesakes_RecordedResponses reads namesakes from artist
// searches MusicBrainz answered. The files in testdata are the responses of
// `curl 'https://musicbrainz.org/ws/2/artist/?query=artist%3A%22<name>%22&limit=100&fmt=json'`,
// recorded on 2026-11-28 and indented.
func TestClient_ListNamesakes_RecordedResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		query     string
		file      string
		wantMBIDs []string
		check     func(t *testing.T, got []*entity.CatalogArtist)
	}{
		{
			// @spec components/entity/artist/list-namesakes "Two artists share the name"
			name:  "Penthouse",
			query: "Penthouse",
			file:  "search-artist-penthouse.json",
			wantMBIDs: []string{
				"c94eaf81-baae-465d-8197-9951c2ef52cd",
				"9604bb00-65c5-4b67-a174-c46c063ba794",
				"7ca3c12a-637c-4868-a986-a97e2212917c",
				"d0f7ee2a-2d4f-4c81-be7a-87d31ba24a79",
				"0a427acb-c9f4-4ba6-a043-4ca854134dc0",
			},
			check: func(t *testing.T, got []*entity.CatalogArtist) {
				uk, jp := got[0], got[1]
				assert.Equal(t, "Penthouse", uk.Name)
				require.NotNil(t, uk.Country)
				assert.Equal(t, "GB", *uk.Country)
				assert.Equal(t, &entity.ActivityDate{Year: 1995, Month: 10}, uk.Begin)
				assert.Equal(t, &entity.ActivityDate{Year: 2002, Month: 11}, uk.End)
				assert.True(t, uk.ActivityEnded)
				require.NotNil(t, uk.Disambiguation)
				assert.Contains(t, *uk.Disambiguation, "UK indie rock band")
				assert.Nil(t, uk.OfficialSiteURL, "ListNamesakes reads no official site")

				assert.Nil(t, jp.Country, "the catalog records no country for the Japanese band")
				assert.Equal(t, &entity.ActivityDate{Year: 2018}, jp.Begin)
				assert.Nil(t, jp.End)
				assert.False(t, jp.ActivityEnded)
				require.NotNil(t, jp.Disambiguation)
				assert.Equal(t, "Japanese city soul band", *jp.Disambiguation)

				belgian := got[3]
				assert.Nil(t, belgian.End, "the Belgian band has no end date")
				assert.True(t, belgian.ActivityEnded, "the catalog marks it ended without an end date")
			},
		},
		{
			// @spec components/entity/artist/list-namesakes "Different letter case"
			name:      "sumika",
			query:     "sumika",
			file:      "search-artist-sumika.json",
			wantMBIDs: []string{"bc731e96-0c06-4adf-b94b-ca636f48998a", "8cc2e134-28ed-4e8d-bb81-88fb94c35be6"},
			check: func(t *testing.T, got []*entity.CatalogArtist) {
				assert.Equal(t, "Sumika", got[1].Name, "an artist recorded as \"Sumika\" matches \"sumika\"")
			},
		},
		{
			// @spec components/entity/artist/list-namesakes "Similar name only"
			name:  "Ado",
			query: "Ado",
			file:  "search-artist-ado.json",
			check: func(t *testing.T, got []*entity.CatalogArtist) {
				require.NotEmpty(t, got)
				for _, a := range got {
					assert.True(t, strings.EqualFold(a.Name, "Ado"), "%q only resembles Ado", a.Name)
				}
				assert.Len(t, got, 6, "the recording has six artists named Ado or ADO among 40 results")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body, err := os.ReadFile(filepath.Join("testdata", tt.file))
			require.NoError(t, err)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("query") != `artist:"`+tt.query+`"` {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
			}))
			t.Cleanup(server.Close)
			client := musicbrainz.NewClient(server.Client(), testLogger(t))
			client.SetBaseURL(server.URL + "/")

			got, err := client.ListNamesakes(context.Background(), tt.query)

			require.NoError(t, err)
			if tt.wantMBIDs != nil {
				mbids := make([]string, len(got))
				for i, a := range got {
					mbids[i] = a.MBID
				}
				assert.Equal(t, tt.wantMBIDs, mbids)
			}
			tt.check(t, got)
		})
	}
}
