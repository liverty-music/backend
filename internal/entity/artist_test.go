package entity_test

import (
	"strings"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestFilterArtistsByMBID(t *testing.T) {
	t.Parallel()

	type args struct {
		artists []*entity.Artist
	}
	tests := []struct {
		name    string
		args    args
		wantLen int
		// wantMBIDs are the MBIDs expected in result order.
		wantMBIDs []string
	}{
		{
			name: "filter empty MBIDs and deduplicate keeping first occurrence",
			args: args{
				artists: []*entity.Artist{
					{ID: "1", Name: "Artist A", MBID: "mbid-a"},
					{ID: "2", Name: "Artist B", MBID: ""},
					{ID: "3", Name: "Artist A dup", MBID: "mbid-a"},
					{ID: "4", Name: "Artist C", MBID: "mbid-c"},
				},
			},
			wantLen:   2,
			wantMBIDs: []string{"mbid-a", "mbid-c"},
		},
		{
			name: "return empty slice when all MBIDs are empty",
			args: args{
				artists: []*entity.Artist{
					{ID: "1", Name: "Artist A", MBID: ""},
					{ID: "2", Name: "Artist B", MBID: ""},
				},
			},
			wantLen:   0,
			wantMBIDs: []string{},
		},
		{
			name: "return all artists when there are no duplicates",
			args: args{
				artists: []*entity.Artist{
					{ID: "1", Name: "Artist A", MBID: "mbid-a"},
					{ID: "2", Name: "Artist B", MBID: "mbid-b"},
				},
			},
			wantLen:   2,
			wantMBIDs: []string{"mbid-a", "mbid-b"},
		},
		{
			name: "return empty slice for empty input",
			args: args{
				artists: []*entity.Artist{},
			},
			wantLen:   0,
			wantMBIDs: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := entity.FilterArtistsByMBID(tt.args.artists)
			assert.Len(t, got, tt.wantLen)

			gotMBIDs := make([]string, len(got))
			for i, a := range got {
				gotMBIDs[i] = a.MBID
			}
			assert.Equal(t, tt.wantMBIDs, gotMBIDs)
		})
	}
}

func TestNewArtist(t *testing.T) {
	t.Parallel()

	// @spec components/entity/artist "New artist"
	t.Run("New artist", func(t *testing.T) {
		t.Parallel()

		got := entity.NewArtist("羊文学", "a1b2c3d4-0000-0000-0000-000000000001")

		assert.NotEmpty(t, got.ID)
		assert.Equal(t, "羊文学", got.Name)
		assert.Equal(t, "a1b2c3d4-0000-0000-0000-000000000001", got.MBID)
		assert.Nil(t, got.Fanart)
		assert.Nil(t, got.FanartSyncTime)
		assert.Nil(t, got.OfficialSiteCheckTime)
	})

	// @spec components/entity/artist "Two new artists"
	t.Run("Two new artists", func(t *testing.T) {
		t.Parallel()

		a := entity.NewArtist("羊文学", "a1b2c3d4-0000-0000-0000-000000000001")
		b := entity.NewArtist("羊文学", "a1b2c3d4-0000-0000-0000-000000000001")

		assert.NotEqual(t, a.ID, b.ID)
		assert.Nil(t, a.OfficialSiteCheckTime)
		assert.Nil(t, b.OfficialSiteCheckTime)
	})
}

func TestNewOfficialSite(t *testing.T) {
	t.Parallel()

	t.Run("set ID, ArtistID, and URL", func(t *testing.T) {
		t.Parallel()

		got := entity.NewOfficialSite("artist-1", "https://example.com")

		assert.NotEmpty(t, got.ID)
		assert.Equal(t, "artist-1", got.ArtistID)
		assert.Equal(t, "https://example.com", got.URL)
	})

	t.Run("generate different IDs on successive calls", func(t *testing.T) {
		t.Parallel()

		a := entity.NewOfficialSite("artist-1", "https://example.com")
		b := entity.NewOfficialSite("artist-1", "https://example.com")

		assert.NotEqual(t, a.ID, b.ID)
	})
}

func TestValidateOfficialSiteURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		url     string
		wantErr error
	}{
		{name: "valid https URL", url: "https://www.hitsujibungaku.info/", wantErr: nil},
		{name: "valid URL of exactly 2048 characters", url: "https://example.com/" + strings.Repeat("a", 2048-len("https://example.com/")), wantErr: nil},
		{name: "empty URL", url: "", wantErr: assert.AnError},
		{name: "URL longer than 2048 characters", url: "https://example.com/" + strings.Repeat("a", 2049), wantErr: assert.AnError},
		{name: "relative reference is not an absolute URI", url: "/artist/yorushika", wantErr: assert.AnError},
		{name: "malformed URI", url: "https://exa mple.com/%zz", wantErr: assert.AnError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := entity.ValidateOfficialSiteURL(tt.url)

			if tt.wantErr != nil {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
