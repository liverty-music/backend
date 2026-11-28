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
	for _, in := range []string{"TBA", "-STAY TUNED-", "未定", "横浜某所", "tbd", "To Be Announced", "Coming Soon!", "後日発表", "ＴＢＡ", "PRIVATE", "Private Event"} {
		t.Run("missing: "+in, func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, entity.NormalizeVenueName(in))
		})
	}

	// @spec components/entity/venue "Venue to be announced later is missing"
	for _, in := range []string{"会場後日発表予定", "会場未定", "後日発表予定", "会場 後日発表予定"} {
		t.Run("missing: "+in, func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, entity.NormalizeVenueName(in))
		})
	}

	// @spec components/entity/venue "A real name containing a marker word is kept"
	for _, in := range []string{"TBA Hall Osaka", "未定会館", "会場未定ホール", "Zepp Haneda"} {
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

	tokyoArea := "JP-13"
	californiaArea := "US-CA"
	osaka := "JP-27"
	independentOsaka := &entity.VenuePlace{ProviderUserID: "p-osaka", Name: "The Independent", CountryCode: "JP", AdminArea: &osaka}
	independentSF := &entity.VenuePlace{ProviderUserID: "p-sf", Name: "The Independent", CountryCode: "US", AdminArea: &californiaArea}
	noRegionUS := &entity.VenuePlace{ProviderUserID: "p-us", Name: "Some Hall", CountryCode: "US"}
	unknown := &entity.VenuePlace{ProviderUserID: "p-unknown", Name: "The Independent"}

	tests := []struct {
		name   string
		listed string
		area   *string
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
			// @spec components/entity/venue/select-place "A place of the same name elsewhere"
			name:   "a place of the same name elsewhere is passed over",
			listed: "The Independent",
			area:   &californiaArea,
			places: []*entity.VenuePlace{independentOsaka, independentSF},
			want:   independentSF,
		},
		{
			// @spec components/entity/venue/select-place "Every place is elsewhere"
			name:   "every place is elsewhere",
			listed: "The Independent",
			area:   &tokyoArea,
			places: []*entity.VenuePlace{independentOsaka, independentSF},
			want:   nil,
		},
		{
			// @spec components/entity/venue/select-place "A place whose area is unknown counts by its country"
			name:   "a place whose area is unknown counts by its country",
			listed: "Some Hall",
			area:   &californiaArea,
			places: []*entity.VenuePlace{noRegionUS},
			want:   noRegionUS,
		},
		{
			// @spec components/entity/venue/select-place "A place in the area beats one known only by country"
			name:   "a place in the area beats an earlier one known only by country",
			listed: "The Independent",
			area:   &californiaArea,
			places: []*entity.VenuePlace{{ProviderUserID: "p-country", Name: "The Independent", CountryCode: "US"}, independentSF},
			want:   independentSF,
		},
		{
			// @spec components/entity/venue/select-place "A place whose country is unknown"
			name:   "a place whose country is unknown does not count",
			listed: "The Independent",
			area:   &californiaArea,
			places: []*entity.VenuePlace{unknown},
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

			got := entity.SelectVenuePlace(tt.listed, tt.area, tt.places)

			assert.Same(t, tt.want, got)
		})
	}
}
