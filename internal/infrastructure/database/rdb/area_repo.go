package rdb

import (
	"context"
	"database/sql"
	"log/slog"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

// AreaRepository implements entity.AreaRepository for PostgreSQL.
type AreaRepository struct {
	db *Database
}

// NewAreaRepository creates an area repository.
func NewAreaRepository(db *Database) *AreaRepository {
	return &AreaRepository{db: db}
}

var (
	// areaCodeText is a text in the form of any Area code, in any case.
	areaCodeText = regexp.MustCompile(`^[A-Za-z]{2}(-[A-Za-z0-9]+)?$`)
	countryCode  = regexp.MustCompile(`^[A-Z]{2}$`)
)

const (
	// resolveAreaQuery finds the matching Areas and follows each one's
	// replacement, then its parents, to the first-level Area it leads to
	// (at most three steps in CLDR; six are allowed). It returns the
	// distinct first-level codes.
	//
	// $1 is the upper-cased text when it is code-like, else NULL; $2 the
	// trimmed text, matched as a name; $3 the country, which narrows names.
	resolveAreaQuery = `
		WITH RECURSIVE matched AS (
			SELECT code FROM areas
			WHERE kind <> 'region'
			  AND (
			    code = $1
			    OR (($3::text IS NULL OR country_code = $3)
			        AND (lower(name_en) = lower($2)
			             OR name_ja = $2
			             OR name_ja IN ($2 || '都', $2 || '道', $2 || '府', $2 || '県')))
			  )
		), chain AS (
			SELECT a.code, a.replaced_by_code, a.parent_code, a.is_first_level, 0 AS step
			FROM areas a JOIN matched m ON m.code = a.code
			UNION ALL
			SELECT a.code, a.replaced_by_code, a.parent_code, a.is_first_level, c.step + 1
			FROM chain c
			JOIN areas a ON a.code = COALESCE(c.replaced_by_code, c.parent_code)
			WHERE NOT c.is_first_level AND c.step < 6
		)
		SELECT DISTINCT code FROM chain WHERE is_first_level
		LIMIT 2
	`

	// areaColumns are the columns scanAreaRow reads, with the codes of the
	// regions that group the Area.
	areaColumns = `
		a.code, a.kind, a.country_code, a.parent_code, a.replaced_by_code, a.name_en, a.name_ja,
		a.reference_latitude, a.reference_longitude, a.reference_point_source, a.time_zone,
		COALESCE((SELECT array_agg(m.region_code ORDER BY m.region_code)
		          FROM area_memberships m WHERE m.member_code = a.code), '{}')
	`

	getAreaQuery = `SELECT ` + areaColumns + ` FROM areas a WHERE a.code = $1`

	listFirstLevelAreasByCountryQuery = `
		SELECT ` + areaColumns + `
		FROM areas a
		WHERE a.country_code = $1 AND a.is_first_level
		ORDER BY a.code
	`
	listRegionsByCountryQuery = `
		SELECT ` + areaColumns + `
		FROM areas a
		WHERE a.country_code = $1 AND a.kind = 'region'
		ORDER BY a.code
	`
	listTimeZonesByCountryQuery = `
		SELECT z.name, z.name_en, z.name_ja
		FROM country_time_zones c
		JOIN time_zones z ON z.name = c.time_zone
		WHERE c.country_code = $1
		ORDER BY z.name
	`
	listCountriesQuery = `SELECT code, name_en, name_ja FROM countries ORDER BY code`
)

// Resolve returns the one first-level Area a code or a name stands for.
func (r *AreaRepository) Resolve(ctx context.Context, text string, country *string) (*entity.Area, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, apperr.New(codes.InvalidArgument, "area text is empty")
	}
	if country != nil && !countryCode.MatchString(*country) {
		return nil, apperr.New(codes.InvalidArgument, "country code must be two uppercase letters")
	}
	// A text in the form of a code is also looked up as a name: a name can
	// have that form ("Al-Anbar"), and no code equals a name.
	var code *string
	if areaCodeText.MatchString(text) {
		upper := strings.ToUpper(text)
		code = &upper
	}
	name := &text

	rows, err := r.db.Pool.Query(ctx, resolveAreaQuery, code, name, country)
	if err != nil {
		return nil, toAppErr(err, "failed to resolve area", slog.String("text", text))
	}
	firstLevel, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, toAppErr(err, "failed to resolve area", slog.String("text", text))
	}
	if len(firstLevel) != 1 {
		return nil, apperr.New(codes.NotFound, "the text stands for no first-level area or for more than one",
			slog.String("text", text), slog.Int("first_level_matches", len(firstLevel)))
	}

	area, err := scanAreaRow(r.db.Pool.QueryRow(ctx, getAreaQuery, firstLevel[0]))
	if err != nil {
		return nil, toAppErr(err, "failed to read area", slog.String("code", firstLevel[0]))
	}
	return area, nil
}

// ListByCountry returns the country's first-level Areas, regions and time
// zones.
func (r *AreaRepository) ListByCountry(ctx context.Context, country string) (*entity.CountryAreas, error) {
	if !countryCode.MatchString(country) {
		return nil, apperr.New(codes.InvalidArgument, "country code must be two uppercase letters")
	}
	out := &entity.CountryAreas{}
	var err error
	if out.Areas, err = r.listAreas(ctx, listFirstLevelAreasByCountryQuery, country); err != nil {
		return nil, err
	}
	if out.Regions, err = r.listAreas(ctx, listRegionsByCountryQuery, country); err != nil {
		return nil, err
	}
	rows, err := r.db.Pool.Query(ctx, listTimeZonesByCountryQuery, country)
	if err != nil {
		return nil, toAppErr(err, "failed to list time zones", slog.String("country", country))
	}
	out.TimeZones, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (*entity.AreaTimeZone, error) {
		z := &entity.AreaTimeZone{}
		var nameJA sql.NullString
		if err := row.Scan(&z.Name, &z.NameEN, &nameJA); err != nil {
			return nil, err
		}
		z.NameJA = nullString(nameJA)
		return z, nil
	})
	if err != nil {
		return nil, toAppErr(err, "failed to list time zones", slog.String("country", country))
	}
	return out, nil
}

// ListCountries returns every country that has first-level Areas.
func (r *AreaRepository) ListCountries(ctx context.Context) ([]*entity.Country, error) {
	rows, err := r.db.Pool.Query(ctx, listCountriesQuery)
	if err != nil {
		return nil, toAppErr(err, "failed to list countries")
	}
	countries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*entity.Country, error) {
		c := &entity.Country{}
		var nameJA sql.NullString
		if err := row.Scan(&c.Code, &c.NameEN, &nameJA); err != nil {
			return nil, err
		}
		c.NameJA = nullString(nameJA)
		return c, nil
	})
	if err != nil {
		return nil, toAppErr(err, "failed to list countries")
	}
	return countries, nil
}

func (r *AreaRepository) listAreas(ctx context.Context, query, country string) ([]*entity.Area, error) {
	rows, err := r.db.Pool.Query(ctx, query, country)
	if err != nil {
		return nil, toAppErr(err, "failed to list areas", slog.String("country", country))
	}
	areas, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*entity.Area, error) {
		return scanAreaRow(row)
	})
	if err != nil {
		return nil, toAppErr(err, "failed to list areas", slog.String("country", country))
	}
	return areas, nil
}

// scanAreaRow reads the columns of areaColumns.
func scanAreaRow(row pgx.Row) (*entity.Area, error) {
	var (
		a                                  entity.Area
		kind                               string
		parent, replacedBy, nameJA, source sql.NullString
		timeZone                           sql.NullString
		lat, lng                           sql.NullFloat64
	)
	if err := row.Scan(&a.Code, &kind, &a.CountryCode, &parent, &replacedBy, &a.NameEN, &nameJA,
		&lat, &lng, &source, &timeZone, &a.RegionCodes); err != nil {
		return nil, err
	}
	a.Kind = entity.AreaKind(kind)
	a.ParentCode = nullString(parent)
	a.ReplacedByCode = nullString(replacedBy)
	a.NameJA = nullString(nameJA)
	a.TimeZone = nullString(timeZone)
	if lat.Valid && lng.Valid && source.Valid {
		a.ReferencePoint = &entity.Coordinates{Latitude: lat.Float64, Longitude: lng.Float64}
		s := entity.ReferencePointSource(source.String)
		a.ReferencePointSource = &s
	}
	return &a, nil
}

// nullString is the string of a valid value, nil otherwise.
func nullString(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}
