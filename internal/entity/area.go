package entity

import (
	"regexp"
	"slices"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// AreaKind is what an Area stands for.
type AreaKind string

const (
	// AreaKindSubdivision is a subdivision from the ISO 3166-2 list (JP-13).
	AreaKindSubdivision AreaKind = "subdivision"
	// AreaKindCountry is a country or territory with no subdivision of its
	// own, named by its ISO 3166-1 code (HK).
	AreaKindCountry AreaKind = "country"
	// AreaKindRegion groups first-level Areas of one country (JP-KANTO).
	AreaKindRegion AreaKind = "region"
)

// ReferencePointSource says how an Area's reference point was obtained.
type ReferencePointSource string

const (
	// ReferencePointLegacy is the former prefecture table.
	ReferencePointLegacy ReferencePointSource = "legacy_prefecture_table"
	// ReferencePointLabel is a published map dataset's label point for the Area.
	ReferencePointLabel ReferencePointSource = "natural_earth_label"
	// ReferencePointChildren is the mean of the label points of the Area's
	// lower-level Areas.
	ReferencePointChildren ReferencePointSource = "natural_earth_children"
	// ReferencePointManual is set by hand.
	ReferencePointManual ReferencePointSource = "manual"
)

var (
	areaSubdivisionCode = regexp.MustCompile(`^[A-Z]{2}-[A-Z0-9]{1,3}$`)
	areaCountryCode     = regexp.MustCompile(`^[A-Z]{2}$`)
	areaRegionCode      = regexp.MustCompile(`^[A-Z]{2}-[A-Z]{4,}$`)
)

// Area is a named part of the world that Venues, Homes and reference areas
// name by its code: a subdivision, a country or territory without
// subdivisions, or a region grouping first-level Areas of a country. Areas
// are reference data generated from published releases; no operation
// creates one.
//
// Corresponds to liverty_music.entity.v1.Area.
type Area struct {
	// Code is the Area's identity: JP-13, HK or JP-KANTO.
	Code string
	// Kind is what the Area stands for.
	Kind AreaKind
	// CountryCode is the ISO 3166-1 alpha-2 code of the Area's country, the
	// first two letters of Code.
	CountryCode string
	// ParentCode is the subdivision a lower-level subdivision lies in; nil
	// for a first-level subdivision, a country and a region.
	ParentCode *string
	// ReplacedByCode is the Area that took over a withdrawn code; nil while
	// the code is current.
	ReplacedByCode *string
	// NameEN is the English name.
	NameEN string
	// NameJA is the Japanese name; nil when the published data has none.
	NameJA *string
	// ReferencePoint is where NEARBY distances are measured from; nil when
	// none is known.
	ReferencePoint *Coordinates
	// ReferencePointSource says how ReferencePoint was obtained; set exactly
	// when ReferencePoint is.
	ReferencePointSource *ReferencePointSource
	// TimeZone is the IANA zone of the whole Area; nil when the Area spans
	// zones or none is known.
	TimeZone *string
	// RegionCodes are the regions that group this first-level Area.
	RegionCodes []string
}

// IsFirstLevel reports whether the Area can be a Venue's admin area or a
// Home: it is not a region and has neither a parent nor a replacement.
func (a *Area) IsFirstLevel() bool {
	return a.Kind != AreaKindRegion && a.ParentCode == nil && a.ReplacedByCode == nil
}

// Validate checks the rules every Area holds: its code matches its kind and
// its country, a region has no parent, a replacement is never set on a
// region, and the reference point comes with its source.
//
// # Possible errors
//
//   - InvalidArgument: a rule does not hold.
func (a *Area) Validate() error {
	if len(a.Code) < 2 || a.Code[:2] != a.CountryCode || !areaCountryCode.MatchString(a.CountryCode) {
		return apperr.New(codes.InvalidArgument, "area code must start with its country code")
	}
	var codeRe *regexp.Regexp
	switch a.Kind {
	case AreaKindSubdivision:
		codeRe = areaSubdivisionCode
	case AreaKindCountry:
		codeRe = areaCountryCode
	case AreaKindRegion:
		codeRe = areaRegionCode
		if a.ParentCode != nil || a.ReplacedByCode != nil {
			return apperr.New(codes.InvalidArgument, "a region has no parent and no replacement")
		}
	default:
		return apperr.New(codes.InvalidArgument, "unknown area kind")
	}
	if !codeRe.MatchString(a.Code) {
		return apperr.New(codes.InvalidArgument, "area code does not match its kind")
	}
	if a.NameEN == "" {
		return apperr.New(codes.InvalidArgument, "area must have an English name")
	}
	if (a.ReferencePoint == nil) != (a.ReferencePointSource == nil) {
		return apperr.New(codes.InvalidArgument, "an area's reference point and its source go together")
	}
	return nil
}

// ValidateTimeZone checks the Area's time zone against the zones of its
// country: a zone must be one of them, and an Area of a country with exactly
// one zone must have it.
//
// # Possible errors
//
//   - InvalidArgument: the zone is not one of the country's, or a one-zone
//     country's Area has another or none.
func (a *Area) ValidateTimeZone(countryZones []string) error {
	if a.TimeZone != nil && !slices.Contains(countryZones, *a.TimeZone) {
		return apperr.New(codes.InvalidArgument, "an area's time zone must be one of its country's")
	}
	if a.Kind != AreaKindRegion && len(countryZones) == 1 && (a.TimeZone == nil || *a.TimeZone != countryZones[0]) {
		return apperr.New(codes.InvalidArgument, "an area of a one-zone country has that zone")
	}
	return nil
}

// Group adds member to the region a. Only a first-level Area of the region's
// country can be grouped, and grouping does not make the region its parent.
//
// # Possible errors
//
//   - InvalidArgument: a is not a region, or member is not a first-level
//     Area of the region's country.
func (a *Area) Group(member *Area) error {
	if a.Kind != AreaKindRegion {
		return apperr.New(codes.InvalidArgument, "only a region groups areas")
	}
	if !member.IsFirstLevel() {
		return apperr.New(codes.InvalidArgument, "a region groups only first-level areas")
	}
	if member.CountryCode != a.CountryCode {
		return apperr.New(codes.InvalidArgument, "a region groups only areas of its own country")
	}
	if !slices.Contains(member.RegionCodes, a.Code) {
		member.RegionCodes = append(member.RegionCodes, a.Code)
	}
	return nil
}

// Name returns the Area's name for a viewer of the language: the Japanese
// name for "ja" when there is one, the English name otherwise.
func (a *Area) Name(language string) string {
	return localName(language, a.NameEN, a.NameJA)
}

// AreaTimeZone is a time zone some part of a country lies in, with the
// names a screen shows for it.
//
// Corresponds to liverty_music.entity.v1.TimeZoneOption.
type AreaTimeZone struct {
	// Name is the IANA name: Asia/Seoul.
	Name string
	// NameEN is the English name, the zone's exemplar city (Seoul), except
	// Japan for Asia/Tokyo.
	NameEN string
	// NameJA is the Japanese name (ソウル, 日本 for Asia/Tokyo); nil when
	// the published data has none.
	NameJA *string
}

// DisplayName returns the zone's name for a viewer of the language.
func (z *AreaTimeZone) DisplayName(language string) string {
	return localName(language, z.NameEN, z.NameJA)
}

func localName(language, en string, ja *string) string {
	if language == "ja" && ja != nil && *ja != "" {
		return *ja
	}
	return en
}
