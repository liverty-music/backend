package entity_test

import (
	"strings"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		assert.Equal(t, "a1b2c3d4-0000-0000-0000-000000000001", got.ListeningMBID)
		assert.Equal(t, entity.MBIDStatusUnverified, got.MBIDStatus)
		assert.Nil(t, got.Fanart)
		assert.Nil(t, got.FanartSyncTime)
		assert.Nil(t, got.OfficialSiteCheckTime)
		assert.False(t, got.ActivityEnded)
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
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestArtist_IsActive(t *testing.T) {
	t.Parallel()

	assert.True(t, (&entity.Artist{}).IsActive(), "no end recorded yet")
	assert.True(t, (&entity.Artist{ActivityEnded: false}).IsActive())
	assert.False(t, (&entity.Artist{ActivityEnded: true}).IsActive(), "an ended activity is not active")
}

func TestOfficialSiteRefreshPolicy(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 7*24*time.Hour, entity.OfficialSiteRefreshAge)
	assert.Equal(t, 7, entity.OfficialSiteRefreshCycleDays)
	assert.Equal(t, 3, entity.OfficialSiteRefreshMaxConsecutiveFailures)
}

func TestArtist_FanartDueAt(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	threshold := now.Add(-entity.FanartStaleAge)
	at := func(t time.Time) *entity.Artist { return &entity.Artist{FanartSyncTime: &t} }

	assert.True(t, (&entity.Artist{}).FanartDueAt(now), "never synced")
	assert.False(t, at(threshold).FanartDueAt(now), "exactly at the age is not yet due")
	assert.True(t, at(threshold.Add(-time.Nanosecond)).FanartDueAt(now), "just past the age is due")
	assert.False(t, at(threshold.Add(time.Nanosecond)).FanartDueAt(now), "just inside the age is not due")
	assert.False(t, at(now).FanartDueAt(now))
}

func TestArtist_CheckSearchable(t *testing.T) {
	t.Parallel()

	assert.NoError(t, (&entity.Artist{ID: "a", Name: "Band", MBID: "mbid"}).CheckSearchable())
	assert.ErrorIs(t, (&entity.Artist{ID: "a", MBID: "mbid"}).CheckSearchable(), apperr.ErrInternal, "no name")
	assert.ErrorIs(t, (&entity.Artist{ID: "a", Name: "Band"}).CheckSearchable(), apperr.ErrInternal, "no MBID")
}

func TestMergeArtistsByMBID(t *testing.T) {
	t.Parallel()

	const (
		mbid1 = "11111111-0000-0000-0000-000000000001"
		mbid2 = "22222222-0000-0000-0000-000000000002"
		mbid3 = "33333333-0000-0000-0000-000000000003"
	)
	first := &entity.Artist{ID: "a-1", Name: "First", MBID: mbid1, ListeningMBID: mbid1}
	second := &entity.Artist{ID: "a-2", Name: "Second", MBID: mbid1, ListeningMBID: mbid1}
	other := &entity.Artist{ID: "a-3", Name: "Other", MBID: mbid2, ListeningMBID: mbid2}
	// corrected is found by the listening service under mbid3 and was
	// corrected to mbid1, which first already has.
	corrected := &entity.Artist{ID: "a-4", Name: "Corrected", MBID: mbid1, ListeningMBID: mbid3}

	got, err := entity.MergeArtistsByMBID([]*entity.Artist{first, nil, second, other, corrected})
	require.NoError(t, err)
	assert.Equal(t, []*entity.Artist{first, other}, got, "no two artists share an MBID, the first given wins")

	_, err = entity.MergeArtistsByMBID([]*entity.Artist{first, {Name: "No MBID"}})
	assert.ErrorIs(t, err, apperr.ErrInvalidArgument)

	_, err = entity.MergeArtistsByMBID([]*entity.Artist{{Name: "No listening MBID", MBID: mbid2}})
	assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
}

func TestArtist_IsOffered(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status entity.MBIDStatus
		ended  bool
		want   bool
	}{
		// @spec components/entity/artist "Verified and active"
		{name: "verified and active", status: entity.MBIDStatusVerified, want: true},
		// @spec components/entity/artist "Not verified"
		{name: "unverified", status: entity.MBIDStatusUnverified, want: false},
		// @spec components/entity/artist "Not verified"
		{name: "unverified and ended", status: entity.MBIDStatusUnverified, ended: true, want: false},
		// @spec components/entity/artist "Not verified"
		{name: "ambiguous", status: entity.MBIDStatusAmbiguous, want: false},
		// @spec components/entity/artist "Not verified"
		{name: "ambiguous and ended", status: entity.MBIDStatusAmbiguous, ended: true, want: false},
		// @spec components/entity/artist "Activity ended"
		{name: "verified and ended", status: entity.MBIDStatusVerified, ended: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &entity.Artist{MBIDStatus: tt.status, ActivityEnded: tt.ended}

			assert.Equal(t, tt.want, a.IsOffered())
		})
	}
}

func TestCatalogArtist_Validate(t *testing.T) {
	t.Parallel()

	valid := entity.CatalogArtist{MBID: "c94eaf81-baae-465d-8197-9951c2ef52cd", Name: "Penthouse"}
	require.NoError(t, valid.Validate())

	// @spec components/entity/catalog-artist "Malformed MBID"
	malformed := valid
	malformed.MBID = "c94eaf81"
	assert.ErrorIs(t, malformed.Validate(), apperr.ErrInvalidArgument)

	// @spec components/entity/catalog-artist "Empty name"
	unnamed := valid
	unnamed.Name = ""
	assert.ErrorIs(t, unnamed.Validate(), apperr.ErrInvalidArgument)
}

func TestNewKnownNames(t *testing.T) {
	t.Parallel()

	t.Run("letter case and width are unified", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/artist "Letter case and width"
		a := &entity.Artist{KnownNames: entity.NewKnownNames("Ｓａｕｃｙ　Dog")}

		assert.Equal(t, []string{"saucy dog"}, a.KnownNames)
		assert.True(t, a.IsKnownAs("Saucy dog"))
	})

	t.Run("an alias is a known name", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/artist "Alias"
		a := &entity.Artist{Name: "ヨルシカ", KnownNames: entity.NewKnownNames("ヨルシカ", "Yorushika")}

		assert.True(t, a.IsKnownAs("yorushika"))
		assert.True(t, a.IsKnownAs("ヨルシカ"))
	})

	t.Run("the same name is held once", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/artist "Same name twice"
		assert.Equal(t, []string{"sumika"}, entity.NewKnownNames("sumika", "SUMIKA"))
	})

	t.Run("part of a name does not match", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/artist "Part of a name"
		a := &entity.Artist{KnownNames: entity.NewKnownNames("tomoo kosugi")}

		assert.False(t, a.IsKnownAs("tomoo"))
	})

	t.Run("white space is trimmed and collapsed", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{"back number"}, entity.NewKnownNames(" \tback \u3000 number\n", "  "))
	})

	t.Run("no name gives an empty list", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{}, entity.NewKnownNames())
	})
}
