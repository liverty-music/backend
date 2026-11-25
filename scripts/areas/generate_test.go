package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixture is a small world: Japan (one zone), France (regions containing
// departments), the United States (several zones, with Puerto Rico and
// Georgia), China with Hong Kong and Taiwan, the Netherlands and Belgium,
// Guadeloupe, and Georgia the country.
func fixture() Inputs {
	return Inputs{
		Subgroups: map[string][]string{
			"JP":    {"jp13", "jp14"},
			"FR":    {"frnaq", "fridf"},
			"frnaq": {"fr33"},
			"fridf": {"fr75c"},
			"US":    {"usca", "ustx", "usga", "uspr"},
			"CN":    {"cnbj", "cnhk", "cntw"},
			"TW":    {"twtpe"},
			"GE":    {"getb"},
		},
		Aliases: []Alias{
			{Code: "cn91", Replacements: []string{"HK"}},
			{Code: "uspr", Replacements: []string{"PR"}},
			{Code: "fr75", Replacements: []string{"fr75c"}},
			{Code: "lud", Replacements: []string{"lucl", "ludi"}},
		},
		SubdivisionNames: map[string]map[string]string{
			"en": {
				"jp13": "Tokyo", "jp14": "Kanagawa", "frnaq": "Nouvelle-Aquitaine", "fridf": "Île-de-France",
				"fr33": "Gironde", "fr75c": "Paris", "fr75": "Paris", "usca": "California", "ustx": "Texas",
				"usga": "Georgia", "uspr": "Puerto Rico", "cnbj": "Beijing", "cnhk": "Hong Kong SAR China",
				"cntw": "Taiwan", "twtpe": "Taipei", "getb": "Tbilisi", "cn91": "Hong Kong SAR China",
			},
			"ja": {"jp13": "東京都", "jp14": "神奈川県"},
		},
		TerritoryNames: map[string]map[string]string{
			"en": {"HK": "Hong Kong", "PR": "Puerto Rico", "NL": "Netherlands", "BE": "Belgium", "GP": "Guadeloupe", "GE": "Georgia", "VI": "U.S. Virgin Islands"},
			"ja": {"HK": "香港"},
		},
		ZoneTab: []ZoneEntry{
			{"JP", "Asia/Tokyo"}, {"FR", "Europe/Paris"},
			{"US", "America/Los_Angeles"}, {"US", "America/Chicago"}, {"US", "America/New_York"},
			{"CN", "Asia/Shanghai"}, {"CN", "Asia/Urumqi"}, {"HK", "Asia/Hong_Kong"}, {"TW", "Asia/Taipei"},
			{"PR", "America/Puerto_Rico"}, {"NL", "Europe/Amsterdam"}, {"BE", "Europe/Brussels"},
			{"GP", "America/Guadeloupe"}, {"GE", "Asia/Tbilisi"},
		},
		ExemplarCities: map[string]map[string]string{
			"en": {"Asia/Tokyo": "Tokyo"},
			"ja": {"Asia/Tokyo": "東京", "Asia/Seoul": "ソウル"},
		},
		NaturalEarth: []NEPoint{
			{Code: "FR-33", Point: Point{Lat: 44.8, Lng: -0.6}},
			{Code: "JP-13", Point: Point{Lat: 1, Lng: 1}},
			{Code: "US-CA", Point: Point{Lat: 36.7, Lng: -119.6}},
		},
		Legacy:          map[string]Point{"JP-13": {Lat: 35.6894, Lng: 139.6917}},
		ManualPoints:    map[string]Point{"HK": {Lat: 22.3, Lng: 114.2}},
		ManualAreaZones: map[string]string{"US-CA": "America/Los_Angeles"},
		ManualZoneNames: map[string]ZoneName{"Asia/Tokyo": {EN: "Japan", JA: "日本"}},
		Decisions: []Decision{
			{Code: "CN-HK", Decision: decisionReplace, Target: "HK"},
			{Code: "US-PR", Decision: decisionReplace, Target: "PR"},
			{Code: "CN-TW", Decision: decisionOmit},
		},
		Regions: []Region{{Code: "JP-KANTO", NameEN: "Kanto", NameJA: "関東", Members: []string{"JP-13", "JP-14"}}},
	}
}

func generate(t *testing.T, in Inputs, prev *Snapshot) (*Seed, map[string]Area) {
	t.Helper()
	seed, err := Generate(in, prev)
	require.NoError(t, err)
	byCode := map[string]Area{}
	for _, a := range seed.Areas {
		byCode[a.Code] = a
	}
	return seed, byCode
}

// leadsTo follows replacements, then parents, as Area.Resolve does.
func leadsTo(areas map[string]Area, code string) string {
	for range 6 {
		a := areas[code]
		switch {
		case a.ReplacedByCode != "":
			code = a.ReplacedByCode
		case a.ParentCode != "":
			code = a.ParentCode
		default:
			return code
		}
	}
	return ""
}

func TestGenerate_FirstLevelClassification(t *testing.T) {
	_, areas := generate(t, fixture(), nil)

	assert.True(t, areas["JP-13"].FirstLevel(), "a code a country contains directly is first-level")
	assert.True(t, areas["FR-NAQ"].FirstLevel())
	assert.False(t, areas["FR-33"].FirstLevel(), "a department is inside its region")
	assert.Equal(t, "FR-NAQ", areas["FR-33"].ParentCode)
	assert.Equal(t, kindCountry, areas["HK"].Kind, "a territory without subdivisions is a country row")
	assert.True(t, areas["HK"].FirstLevel())
	assert.False(t, areas["JP-KANTO"].FirstLevel(), "a region is never first-level")
}

func TestGenerate_ReplacementAndParentChains(t *testing.T) {
	_, areas := generate(t, fixture(), nil)

	assert.Equal(t, "FR-75C", areas["FR-75"].ReplacedByCode, "an alias to a subdivision is a replacement")
	assert.Equal(t, "FR-IDF", leadsTo(areas, "FR-75"), "the chain follows the replacement, then the parent")
	assert.Equal(t, "FR-NAQ", leadsTo(areas, "FR-33"))
}

func TestGenerate_AliasToTerritory(t *testing.T) {
	_, areas := generate(t, fixture(), nil)

	assert.Equal(t, "HK", areas["CN-91"].ReplacedByCode, "cn91 maps to the Hong Kong country row")
	assert.Equal(t, "HK", leadsTo(areas, "CN-HK"))
}

func TestGenerate_AliasWithSeveralReplacementsIsLeftOut(t *testing.T) {
	_, areas := generate(t, fixture(), nil)

	_, ok := areas["LU-D"]
	assert.False(t, ok)
}

func TestGenerate_TimeZones(t *testing.T) {
	seed, areas := generate(t, fixture(), nil)

	assert.Equal(t, "Asia/Tokyo", areas["JP-13"].TimeZone, "a one-zone country gives every area its zone")
	assert.Empty(t, areas["US-TX"].TimeZone, "an area of a several-zone country has no zone unless the manual file names one")
	assert.Equal(t, "America/Los_Angeles", areas["US-CA"].TimeZone, "from manual_area_time_zones.csv")
	assert.Equal(t, "Europe/Amsterdam", areas["NL"].TimeZone, "the Netherlands keep the zone named for them")
	assert.Contains(t, seed.CountryZones, ZoneEntry{"NL", "Europe/Amsterdam"})

	names := map[string]TimeZone{}
	for _, z := range seed.TimeZones {
		names[z.Name] = z
	}
	assert.Equal(t, TimeZone{Name: "Asia/Tokyo", NameEN: "Japan", NameJA: "日本"}, names["Asia/Tokyo"], "manual names win")
	assert.Equal(t, "Los Angeles", names["America/Los_Angeles"].NameEN, "without an exemplar city, the name comes from the zone id")
}

func TestGenerate_TerritoryListedAsSubdivision(t *testing.T) {
	_, areas := generate(t, fixture(), nil)

	assert.Equal(t, "PR", leadsTo(areas, "US-PR"), "US-PR resolves to the Puerto Rico country row")
	_, ok := areas["CN-TW"]
	assert.False(t, ok, "an omitted code gets no row")
	assert.True(t, areas["TW-TPE"].FirstLevel())
}

func TestGenerate_UndecidedNameClashFails(t *testing.T) {
	in := fixture()
	in.Subgroups["FR"] = append(in.Subgroups["FR"], "fr971")
	in.SubdivisionNames["en"]["fr971"] = "Guadeloupe"

	_, err := Generate(in, nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "FR-971 has the same name")
}

func TestGenerate_UndecidedAliasToTerritoryFails(t *testing.T) {
	in := fixture()
	in.Subgroups["US"] = append(in.Subgroups["US"], "usvi")
	in.SubdivisionNames["en"]["usvi"] = "Virgin Islands of the United States"
	in.Aliases = append(in.Aliases, Alias{Code: "usvi", Replacements: []string{"VI"}})
	in.ZoneTab = append(in.ZoneTab, ZoneEntry{"VI", "America/St_Thomas"})

	_, err := Generate(in, nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "US-VI is a current subdivision with a CLDR alias to the territory VI")
}

func TestGenerate_DistinctKeepsBoth(t *testing.T) {
	in := fixture()
	in.Subgroups["FR"] = append(in.Subgroups["FR"], "fr971")
	in.SubdivisionNames["en"]["fr971"] = "Guadeloupe"
	in.Decisions = append(in.Decisions, Decision{Code: "FR-971", Decision: decisionDistinct})

	_, areas := generate(t, in, nil)
	assert.True(t, areas["FR-971"].FirstLevel())
	assert.True(t, areas["GP"].FirstLevel())
}

func TestGenerate_SharedNameWithTerritoryWithSubdivisions(t *testing.T) {
	_, areas := generate(t, fixture(), nil)

	assert.True(t, areas["US-GA"].FirstLevel(), "Georgia the state is generated without a decision")
	_, ok := areas["GE"]
	assert.False(t, ok, "Georgia the country has subdivisions, so it has no country row")
	assert.True(t, areas["GE-TB"].FirstLevel())
}

func TestGenerate_ReferencePoints(t *testing.T) {
	_, areas := generate(t, fixture(), nil)

	assert.Equal(t, sourceLegacy, areas["JP-13"].ReferenceFrom, "the former prefecture table wins")
	assert.Equal(t, Point{Lat: 35.6894, Lng: 139.6917}, *areas["JP-13"].Reference)
	assert.Equal(t, sourceNELabel, areas["US-CA"].ReferenceFrom)
	assert.Equal(t, sourceNEChildren, areas["FR-NAQ"].ReferenceFrom, "a region gets the mean of its departments")
	assert.InDelta(t, 44.8, areas["FR-NAQ"].Reference.Lat, 1e-6)
	assert.Equal(t, sourceManual, areas["HK"].ReferenceFrom)
	assert.Nil(t, areas["US-TX"].Reference)
	assert.Nil(t, areas["FR-33"].Reference, "only first-level areas get a reference point")
}

func TestGenerate_PreviousSeed(t *testing.T) {
	t.Run("a code or zone missing from the previous seed fails", func(t *testing.T) {
		prev := &Snapshot{Areas: map[string]bool{"JP-13": true, "XX-1": true}, TimeZones: []string{"Asia/Tokyo", "Asia/Nowhere"}}
		_, err := Generate(fixture(), prev)
		require.Error(t, err)
		assert.ErrorContains(t, err, "XX-1 was in the previous seed")
		assert.ErrorContains(t, err, "time zone Asia/Nowhere was in the previous seed")
	})
	t.Run("a first-level code that stops being first-level fails", func(t *testing.T) {
		in := fixture()
		in.Decisions = append(in.Decisions, Decision{Code: "JP-14", Decision: decisionReplace, Target: "JP-13"})
		in.Regions = nil
		_, err := Generate(in, &Snapshot{Areas: map[string]bool{"JP-14": true}})
		require.Error(t, err)
		assert.ErrorContains(t, err, "JP-14 was first-level in the previous seed")
	})
	t.Run("the same sources against their own snapshot pass", func(t *testing.T) {
		seed, _ := generate(t, fixture(), nil)
		snap := seed.Snapshot()
		_, err := Generate(fixture(), &snap)
		assert.NoError(t, err)
	})
}

func TestGenerate_Regions(t *testing.T) {
	t.Run("members are first-level", func(t *testing.T) {
		seed, areas := generate(t, fixture(), nil)
		assert.Equal(t, kindRegion, areas["JP-KANTO"].Kind)
		assert.Contains(t, seed.Memberships, [2]string{"JP-KANTO", "JP-13"})
	})
	t.Run("a member that is not first-level fails", func(t *testing.T) {
		in := fixture()
		in.Regions = append(in.Regions, Region{Code: "FR-SUDOUEST", NameEN: "Sud-Ouest", Members: []string{"FR-33"}})
		_, err := Generate(in, nil)
		require.Error(t, err)
		assert.ErrorContains(t, err, "member FR-33 of FR-SUDOUEST is not a first-level area")
	})
	t.Run("a member of another country fails", func(t *testing.T) {
		in := fixture()
		in.Regions = append(in.Regions, Region{Code: "JP-ABROAD", NameEN: "Abroad", Members: []string{"US-CA"}})
		_, err := Generate(in, nil)
		require.Error(t, err)
		assert.ErrorContains(t, err, "member US-CA of JP-ABROAD is in another country")
	})
}

func TestSeed_SQLOrdersReferencesFirst(t *testing.T) {
	seed, _ := generate(t, fixture(), nil)
	pos := map[string]int{}
	for i, a := range seed.Areas {
		pos[a.Code] = i
	}
	for _, a := range seed.Areas {
		if a.ParentCode != "" {
			assert.Less(t, pos[a.ParentCode], pos[a.Code], "%s comes after its parent", a.Code)
		}
		if a.ReplacedByCode != "" {
			assert.Less(t, pos[a.ReplacedByCode], pos[a.Code], "%s comes after its replacement", a.Code)
		}
	}
	assert.Contains(t, seed.SQL(), "ON CONFLICT (code) DO UPDATE")
}

func TestSeed_SinceWritesOnlyChangedRows(t *testing.T) {
	seed, _ := generate(t, fixture(), nil)
	snap := seed.Snapshot()

	t.Run("an unchanged run writes nothing", func(t *testing.T) {
		again, _ := generate(t, fixture(), &snap)
		assert.True(t, again.Since(&snap).Empty())
	})
	t.Run("a second run writes only the rows that changed", func(t *testing.T) {
		in := fixture()
		in.ManualZoneNames["Asia/Taipei"] = ZoneName{EN: "Taipei", JA: "台北"}
		in.ManualAreaZones["US-TX"] = "America/Chicago"
		next, _ := generate(t, in, &snap)
		delta := next.Since(&snap)

		require.Len(t, delta.Areas, 1)
		assert.Equal(t, "US-TX", delta.Areas[0].Code)
		require.Len(t, delta.TimeZones, 1)
		assert.Equal(t, "Asia/Taipei", delta.TimeZones[0].Name)
		assert.Empty(t, delta.CountryZones)
		assert.Empty(t, delta.Memberships)
		assert.NotContains(t, delta.SQL(), "INSERT INTO country_time_zones")
	})
	t.Run("without a previous snapshot every row is written", func(t *testing.T) {
		assert.Equal(t, seed, seed.Since(nil))
	})
}
