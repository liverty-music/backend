package pb

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"

	"github.com/liverty-music/backend/internal/entity"
)

// areaKinds maps each AreaKind to its proto enum value.
var areaKinds = map[entity.AreaKind]entityv1.AreaKind{
	entity.AreaKindSubdivision: entityv1.AreaKind_AREA_KIND_SUBDIVISION,
	entity.AreaKindCountry:     entityv1.AreaKind_AREA_KIND_COUNTRY,
	entity.AreaKindRegion:      entityv1.AreaKind_AREA_KIND_REGION,
}

// FromArea converts an Area to its proto message, with its region codes and
// its time zone when it has one.
func FromArea(a *entity.Area) *entityv1.Area {
	if a == nil {
		return nil
	}
	out := &entityv1.Area{
		Code:        &entityv1.AreaCode{Value: a.Code},
		Kind:        areaKinds[a.Kind],
		CountryCode: &entityv1.CountryCode{Value: a.CountryCode},
		NameEn:      a.NameEN,
		NameJa:      a.NameJA,
	}
	for _, code := range a.RegionCodes {
		out.RegionCodes = append(out.RegionCodes, &entityv1.AreaCode{Value: code})
	}
	if a.TimeZone != nil {
		out.TimeZone = &entityv1.TimeZone{Value: *a.TimeZone}
	}
	return out
}

// FromAreas converts Areas to their proto messages, in order.
func FromAreas(areas []*entity.Area) []*entityv1.Area {
	out := make([]*entityv1.Area, 0, len(areas))
	for _, a := range areas {
		out = append(out, FromArea(a))
	}
	return out
}

// FromTimeZoneOptions converts a country's time zones to their proto
// messages, in order.
func FromTimeZoneOptions(zones []*entity.AreaTimeZone) []*entityv1.TimeZoneOption {
	out := make([]*entityv1.TimeZoneOption, 0, len(zones))
	for _, z := range zones {
		out = append(out, &entityv1.TimeZoneOption{
			TimeZone: &entityv1.TimeZone{Value: z.Name},
			NameEn:   z.NameEN,
			NameJa:   z.NameJA,
		})
	}
	return out
}

// FromCountries converts countries to their proto messages, in order.
func FromCountries(countries []*entity.Country) []*entityv1.Country {
	out := make([]*entityv1.Country, 0, len(countries))
	for _, c := range countries {
		out = append(out, &entityv1.Country{
			Code:   &entityv1.CountryCode{Value: c.Code},
			NameEn: c.NameEN,
			NameJa: c.NameJA,
		})
	}
	return out
}
