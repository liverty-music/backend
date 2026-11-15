package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestNormalizeVenueName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "unprefixed name is unchanged", in: "フェスティバルホール", want: "フェスティバルホール"},
		{
			// @spec components/entity/venue "Already normalized name is unchanged"
			name: "already normalized name is unchanged",
			in:   "日本武道館",
			want: "日本武道館",
		},
		{
			// @spec components/entity/venue "Prefecture prefix removed"
			name: "leading admin-area dot prefix is stripped",
			in:   "大阪・フェスティバルホール",
			want: "フェスティバルホール",
		},
		{
			name: "full-form prefecture dot prefix is stripped",
			in:   "大阪府・フェスティバルホール",
			want: "フェスティバルホール",
		},
		{
			// A ≤6-rune FACILITY name before a middle dot must be preserved — only
			// prefecture tokens are stripped, not any short token.
			// @spec components/entity/venue "Middle dot after a non-prefecture word is kept"
			name: "short non-prefecture facility name before a dot is preserved",
			in:   "東京文化会館・大ホール",
			want: "東京文化会館・大ホール",
		},
		{
			name: "another short facility name before a dot is preserved",
			in:   "杉並公会堂・大ホール",
			want: "杉並公会堂・大ホール",
		},
		{
			// @spec components/entity/venue "Performance-city prefix removed"
			name: "full-width performance prefix is stripped",
			in:   "大阪公演 ＠フェスティバルホール",
			want: "フェスティバルホール",
		},
		{
			name: "half-width performance prefix is stripped",
			in:   "東京公演@Zepp Tokyo",
			want: "Zepp Tokyo",
		},
		{
			name: "full-width ASCII folds to half-width",
			in:   "Ｚｅｐｐ　Ｔｏｋｙｏ",
			want: "Zepp Tokyo",
		},
		{
			name: "whitespace is collapsed and trimmed",
			in:   "  フェスティバル　　ホール  ",
			want: "フェスティバル ホール",
		},
		{
			// @spec components/entity/venue "Whitespace-only name is missing"
			name: "whitespace-only input, full-width space included, is missing",
			in:   "\u3000 ",
			want: "",
		},
		{name: "blank input returns empty", in: "   ", want: ""},
		{
			name: "long token before a middle dot is not treated as a prefix",
			in:   "サントリーホール・大ホール",
			want: "サントリーホール・大ホール",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, entity.NormalizeVenueName(tt.in))
		})
	}
}

// TestNormalizeVenueName_DriftCollapsesButDistinctStays asserts the two
// observed prod-drift spellings collapse to one identity while genuinely
// different venues stay distinct.
func TestNormalizeVenueName_UnannouncedVenue(t *testing.T) {
	t.Parallel()

	// @spec components/entity/venue "Venue not yet announced is missing"
	for _, in := range []string{"TBA", "-STAY TUNED-", "未定", "横浜某所", "tbd", "To Be Announced", "Coming Soon!", "後日発表", "ＴＢＡ"} {
		t.Run("missing: "+in, func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, entity.NormalizeVenueName(in))
		})
	}

	// @spec components/entity/venue "A real name containing a marker word is kept"
	for _, in := range []string{"TBA Hall Osaka", "未定会館", "Zepp Haneda"} {
		t.Run("kept: "+in, func(t *testing.T) {
			t.Parallel()
			got := entity.NormalizeVenueName(in)
			assert.Equal(t, in, got)
			assert.Equal(t, got, entity.NormalizeVenueName(got), "normalizing again changes nothing")
		})
	}
}

func TestNormalizeVenueName_DriftCollapsesButDistinctStays(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		entity.NormalizeVenueName("フェスティバルホール"),
		entity.NormalizeVenueName("大阪・フェスティバルホール"),
		"prefixed and unprefixed drift must collapse to the same identity",
	)
	assert.NotEqual(t,
		entity.NormalizeVenueName("日本武道館"),
		entity.NormalizeVenueName("東京国際フォーラム"),
		"genuinely different venues must stay distinct",
	)
}

func TestSelectVenuePlace(t *testing.T) {
	t.Parallel()

	station := &entity.VenuePlace{ProviderUserID: "p-station", Name: "羽田空港第3ターミナル駅"}
	zepp := &entity.VenuePlace{ProviderUserID: "p-zepp", Name: "ZEPP HANEDA"}
	budokan := &entity.VenuePlace{ProviderUserID: "p-budokan", Name: "Nippon Budokan"}
	park := &entity.VenuePlace{ProviderUserID: "p-park", Name: "北の丸公園"}

	tests := []struct {
		name   string
		listed string
		places []*entity.VenuePlace
		want   *entity.VenuePlace
	}{
		{
			// @spec components/entity/venue/select-place "A less relevant place has the listed name"
			name:   "a less relevant place has the listed name",
			listed: "Zepp Haneda",
			places: []*entity.VenuePlace{station, zepp},
			want:   zepp,
		},
		{
			// @spec components/entity/venue/select-place "No place has the listed name"
			name:   "no place has the listed name",
			listed: "日本武道館",
			places: []*entity.VenuePlace{budokan, park},
			want:   budokan,
		},
		{
			// @spec components/entity/venue/select-place "No place"
			name:   "no place",
			listed: "日本武道館",
			places: nil,
			want:   nil,
		},
		{
			name:   "the first of several places with the listed name",
			listed: "Zepp Haneda",
			places: []*entity.VenuePlace{station, zepp, {ProviderUserID: "p-zepp-2", Name: "Zepp Haneda"}},
			want:   zepp,
		},
		{
			name:   "an unannounced listed name matches no place by name",
			listed: "TBA",
			places: []*entity.VenuePlace{budokan, {ProviderUserID: "p-tba", Name: "TBA"}},
			want:   budokan,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := entity.SelectVenuePlace(tt.listed, tt.places)

			assert.Same(t, tt.want, got)
		})
	}
}
