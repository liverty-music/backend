package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strings"
)

// Area kinds, as stored in areas.kind.
const (
	kindSubdivision = "subdivision"
	kindCountry     = "country"
	kindRegion      = "region"
)

// Reference point sources, as stored in areas.reference_point_source, in the
// order they are tried.
const (
	sourceLegacy     = "legacy_prefecture_table"
	sourceNELabel    = "natural_earth_label"
	sourceNEChildren = "natural_earth_children"
	sourceManual     = "manual"
)

// Decisions of manual_replacements.csv.
const (
	decisionReplace  = "replace"
	decisionOmit     = "omit"
	decisionDistinct = "distinct"
)

var (
	territoryRe   = regexp.MustCompile(`^[A-Z]{2}$`)
	cldrSubRe     = regexp.MustCompile(`^[a-z]{2}[a-z0-9]{1,3}$`)
	subdivisionRe = regexp.MustCompile(`^[A-Z]{2}-[A-Z0-9]{1,3}$`)
	regionRe      = regexp.MustCompile(`^[A-Z]{2}-[A-Z]{4,}$`)
)

// Point is a WGS 84 position.
type Point struct {
	Lat, Lng float64
}

// Alias is one CLDR subdivisionAlias: a code and its replacements.
type Alias struct {
	Code         string   // CLDR form, e.g. "cn91"
	Replacements []string // CLDR forms, e.g. ["HK"] or ["frnaq"]
}

// ZoneEntry is one line of the IANA zone.tab.
type ZoneEntry struct {
	Country, Zone string
}

// ZoneName overrides the display names of a time zone.
type ZoneName struct {
	EN, JA string
}

// NEPoint is the label point of one Natural Earth admin-1 feature.
type NEPoint struct {
	Code  string // ISO 3166-2, e.g. "FR-33"
	Point Point
}

// Decision is one row of manual_replacements.csv.
type Decision struct {
	Code, Decision, Target string
}

// Region is one row of regions.csv.
type Region struct {
	Code, NameEN, NameJA string
	Members              []string
}

// Inputs is everything the generator reads, already parsed.
type Inputs struct {
	// Subgroups maps a CLDR container (a territory "FR" or a subdivision
	// "frnaq") to the CLDR codes it directly contains.
	Subgroups map[string][]string
	Aliases   []Alias
	// SubdivisionNames maps a language ("en", "ja") to CLDR code to name.
	SubdivisionNames map[string]map[string]string
	// TerritoryNames maps a language to territory code to name.
	TerritoryNames map[string]map[string]string
	ZoneTab        []ZoneEntry
	// ExemplarCities maps a language to IANA zone to CLDR exemplar city.
	ExemplarCities  map[string]map[string]string
	NaturalEarth    []NEPoint
	Legacy          map[string]Point
	Decisions       []Decision
	ManualPoints    map[string]Point
	ManualAreaZones map[string]string
	ManualZoneNames map[string]ZoneName
	Regions         []Region
}

// Area is one row of the areas table.
type Area struct {
	Code           string
	Kind           string
	CountryCode    string
	ParentCode     string // empty for none
	ReplacedByCode string // empty for none
	NameEN         string
	NameJA         string // empty for none
	Reference      *Point
	ReferenceFrom  string
	TimeZone       string // empty for none
}

// FirstLevel reports what areas.is_first_level computes.
func (a Area) FirstLevel() bool {
	return a.Kind != kindRegion && a.ParentCode == "" && a.ReplacedByCode == ""
}

// TimeZone is one row of the time_zones table.
type TimeZone struct {
	Name, NameEN, NameJA string
}

// Seed is the generated reference data.
type Seed struct {
	Areas        []Area     // ordered so that every parent and replacement comes first
	TimeZones    []TimeZone // by name
	CountryZones []ZoneEntry
	Memberships  [][2]string // region code, member code
}

// Snapshot is what a previous run generated, kept to refuse a run that would
// lose a referenced value and to write only what changed.
type Snapshot struct {
	// Areas maps every area code to whether it was first-level.
	Areas     map[string]bool `json:"areas"`
	TimeZones []string        `json:"time_zones"`
	// Rows maps every row the seed wrote ("area JP-13", "zone Asia/Tokyo",
	// "country_zone JP Asia/Tokyo", "membership JP-KANTO JP-13") to a hash
	// of its values.
	Rows map[string]string `json:"rows"`
}

// Snapshot returns what a later run compares itself with.
func (s *Seed) Snapshot() Snapshot {
	snap := Snapshot{Areas: map[string]bool{}, Rows: map[string]string{}}
	for _, a := range s.Areas {
		snap.Areas[a.Code] = a.FirstLevel()
		snap.Rows["area "+a.Code] = rowHash(areaValues(a))
	}
	for _, z := range s.TimeZones {
		snap.TimeZones = append(snap.TimeZones, z.Name)
		snap.Rows["zone "+z.Name] = rowHash(zoneValues(z))
	}
	for _, z := range s.CountryZones {
		snap.Rows["country_zone "+z.Country+" "+z.Zone] = ""
	}
	for _, m := range s.Memberships {
		snap.Rows["membership "+m[0]+" "+m[1]] = ""
	}
	return snap
}

// Since returns the rows of s that are new or differ from prev, in the same
// order. Without a previous snapshot every row is new.
func (s *Seed) Since(prev *Snapshot) *Seed {
	if prev == nil || prev.Rows == nil {
		return s
	}
	changed := func(key, hash string) bool {
		old, ok := prev.Rows[key]
		return !ok || old != hash
	}
	out := &Seed{}
	for _, a := range s.Areas {
		if changed("area "+a.Code, rowHash(areaValues(a))) {
			out.Areas = append(out.Areas, a)
		}
	}
	for _, z := range s.TimeZones {
		if changed("zone "+z.Name, rowHash(zoneValues(z))) {
			out.TimeZones = append(out.TimeZones, z)
		}
	}
	for _, z := range s.CountryZones {
		if changed("country_zone "+z.Country+" "+z.Zone, "") {
			out.CountryZones = append(out.CountryZones, z)
		}
	}
	for _, m := range s.Memberships {
		if changed("membership "+m[0]+" "+m[1], "") {
			out.Memberships = append(out.Memberships, m)
		}
	}
	return out
}

// Empty reports whether the seed writes nothing.
func (s *Seed) Empty() bool {
	return len(s.Areas) == 0 && len(s.TimeZones) == 0 && len(s.CountryZones) == 0 && len(s.Memberships) == 0
}

func rowHash(values string) string {
	sum := sha256.Sum256([]byte(values))
	return hex.EncodeToString(sum[:8])
}

// cldrCode turns a CLDR code into an area code: "fr33" → "FR-33",
// "FR" → "FR".
func cldrCode(c string) string {
	if territoryRe.MatchString(c) {
		return c
	}
	return strings.ToUpper(c[:2]) + "-" + strings.ToUpper(c[2:])
}

// Generate builds the seed from the inputs and checks it against the
// previous snapshot. It returns every problem it finds, not only the first.
func Generate(in Inputs, prev *Snapshot) (*Seed, error) {
	g := &generator{in: in, areas: map[string]*Area{}}
	g.build()
	if len(g.errs) > 0 {
		return nil, errors.Join(g.errs...)
	}
	seed := g.seed()
	if prev != nil {
		g.compare(seed, *prev)
	}
	if len(g.errs) > 0 {
		return nil, errors.Join(g.errs...)
	}
	return seed, nil
}

type generator struct {
	in    Inputs
	areas map[string]*Area
	// current holds the codes CLDR lists in its hierarchy (not aliases).
	current map[string]bool
	// countryZones maps a country to its zones, in zone.tab order.
	countryZones map[string][]string
	errs         []error
}

func (g *generator) fail(format string, args ...any) {
	g.errs = append(g.errs, fmt.Errorf(format, args...))
}

func (g *generator) build() {
	g.current = map[string]bool{}
	g.countryZones = map[string][]string{}
	for _, z := range g.in.ZoneTab {
		g.countryZones[z.Country] = append(g.countryZones[z.Country], z.Zone)
	}

	g.addHierarchy()
	g.addCountries()
	decided := g.decisions()
	g.addAliases(decided)
	g.applyDecisions()
	g.checkNameClashes(decided)
	g.checkChains()
	g.addRegions()
	g.setTimeZones()
	g.setReferencePoints()
}

// addHierarchy adds every subdivision CLDR lists: a code a territory contains
// directly is first-level, a code a subdivision contains has that parent.
func (g *generator) addHierarchy() {
	var walk func(container string, parent string)
	walk = func(container, parent string) {
		for _, child := range g.in.Subgroups[container] {
			code := cldrCode(child)
			if !subdivisionRe.MatchString(code) {
				g.fail("CLDR subdivision %q gives the malformed code %q", child, code)
				continue
			}
			g.current[code] = true
			g.areas[code] = &Area{
				Code:        code,
				Kind:        kindSubdivision,
				CountryCode: code[:2],
				ParentCode:  parent,
				NameEN:      g.in.SubdivisionNames["en"][child],
				NameJA:      g.in.SubdivisionNames["ja"][child],
			}
			walk(child, code)
		}
	}
	for container := range g.in.Subgroups {
		if territoryRe.MatchString(container) {
			walk(container, "")
		}
	}
}

// addCountries adds a country row for every territory with time zones that
// CLDR gives no subdivisions.
func (g *generator) addCountries() {
	for country := range g.countryZones {
		if len(g.in.Subgroups[country]) > 0 {
			continue
		}
		g.current[country] = true
		g.areas[country] = &Area{
			Code:        country,
			Kind:        kindCountry,
			CountryCode: country,
			NameEN:      g.in.TerritoryNames["en"][country],
			NameJA:      g.in.TerritoryNames["ja"][country],
		}
	}
}

func (g *generator) decisions() map[string]Decision {
	decided := map[string]Decision{}
	for _, d := range g.in.Decisions {
		decided[d.Code] = d
	}
	return decided
}

// addAliases interprets CLDR's subdivision aliases. An alias to a subdivision
// becomes a replacement; an alias to a territory maps to that territory's
// country row; an alias with several replacements is left out.
func (g *generator) addAliases(decided map[string]Decision) {
	for _, al := range g.in.Aliases {
		if !cldrSubRe.MatchString(al.Code) || len(al.Replacements) != 1 {
			continue
		}
		code := cldrCode(al.Code)
		if _, ok := decided[code]; ok {
			continue
		}
		repl := al.Replacements[0]
		toTerritory := territoryRe.MatchString(repl)
		target := cldrCode(repl)
		if g.current[code] {
			if toTerritory {
				g.fail("%s is a current subdivision with a CLDR alias to the territory %s: decide it in manual_replacements.csv", code, repl)
			}
			continue
		}
		if t, ok := g.areas[target]; !ok || (toTerritory && t.Kind != kindCountry) {
			continue
		}
		g.areas[code] = g.aliasRow(code, al.Code, target)
	}
}

// aliasRow is a row for a code that only exists to be replaced.
func (g *generator) aliasRow(code, cldr, target string) *Area {
	a := &Area{
		Code:           code,
		Kind:           kindSubdivision,
		CountryCode:    code[:2],
		ReplacedByCode: target,
		NameEN:         g.in.SubdivisionNames["en"][cldr],
		NameJA:         g.in.SubdivisionNames["ja"][cldr],
	}
	if t := g.areas[target]; t != nil && a.NameEN == "" {
		a.NameEN, a.NameJA = t.NameEN, t.NameJA
	}
	return a
}

func (g *generator) applyDecisions() {
	for _, d := range g.in.Decisions {
		switch d.Decision {
		case decisionReplace:
			g.replace(d)
		case decisionOmit:
			g.omit(d.Code)
		case decisionDistinct:
			if _, ok := g.areas[d.Code]; !ok {
				g.fail("manual_replacements.csv: %s is distinct but CLDR has no such code", d.Code)
			}
		default:
			g.fail("manual_replacements.csv: %s has the unknown decision %q", d.Code, d.Decision)
		}
	}
}

// replace makes d.Code a replacement of d.Target, adding a row for a code
// CLDR does not list.
func (g *generator) replace(d Decision) {
	target, ok := g.areas[d.Target]
	if !ok {
		g.fail("manual_replacements.csv: %s replaces unknown %s", d.Code, d.Target)
		return
	}
	a, ok := g.areas[d.Code]
	if !ok {
		if !subdivisionRe.MatchString(d.Code) {
			g.fail("manual_replacements.csv: %s is not a subdivision code", d.Code)
			return
		}
		cldr := strings.ToLower(strings.ReplaceAll(d.Code, "-", ""))
		a = g.aliasRow(d.Code, cldr, target.Code)
		g.areas[d.Code] = a
	}
	a.ParentCode = ""
	a.ReplacedByCode = target.Code
}

// omit removes a code, refusing when another row leads through it.
func (g *generator) omit(code string) {
	if _, ok := g.areas[code]; !ok {
		g.fail("manual_replacements.csv: %s is omitted but CLDR has no such code", code)
	}
	delete(g.areas, code)
	for _, a := range g.areas {
		if a.ParentCode == code || a.ReplacedByCode == code {
			g.fail("manual_replacements.csv: %s is omitted but %s refers to it", code, a.Code)
		}
	}
}

// checkNameClashes refuses an undecided current subdivision that has the
// same English or Japanese name as a country row: the two are likely one
// place that CLDR lists twice.
func (g *generator) checkNameClashes(decided map[string]Decision) {
	countryByName := map[string]string{}
	for _, a := range g.areas {
		if a.Kind != kindCountry {
			continue
		}
		countryByName["en:"+a.NameEN] = a.Code
		if a.NameJA != "" {
			countryByName["ja:"+a.NameJA] = a.Code
		}
	}
	for _, a := range g.sorted() {
		if a.Kind != kindSubdivision || !g.current[a.Code] {
			continue
		}
		if _, ok := decided[a.Code]; ok {
			continue
		}
		for _, key := range []string{"en:" + a.NameEN, "ja:" + a.NameJA} {
			if key == "ja:" {
				continue
			}
			if c, ok := countryByName[key]; ok {
				g.fail("%s has the same name %q as the country row %s: decide it in manual_replacements.csv", a.Code, key[3:], c)
				break
			}
		}
	}
}

// firstLevel follows replacements and parents to the first-level area.
func (g *generator) firstLevel(code string) (string, bool) {
	for range 6 {
		a, ok := g.areas[code]
		if !ok {
			return "", false
		}
		switch {
		case a.ReplacedByCode != "":
			code = a.ReplacedByCode
		case a.ParentCode != "":
			code = a.ParentCode
		default:
			return code, a.Kind != kindRegion
		}
	}
	return "", false
}

func (g *generator) checkChains() {
	for _, a := range g.sorted() {
		if a.NameEN == "" {
			g.fail("%s has no English name", a.Code)
		}
		if _, ok := g.firstLevel(a.Code); !ok {
			g.fail("%s does not lead to a first-level area", a.Code)
		}
	}
}

func (g *generator) addRegions() {
	for _, r := range g.in.Regions {
		if !regionRe.MatchString(r.Code) {
			g.fail("regions.csv: %s is not a region code", r.Code)
			continue
		}
		for _, m := range r.Members {
			if a, ok := g.areas[m]; !ok || !a.FirstLevel() {
				g.fail("regions.csv: member %s of %s is not a first-level area", m, r.Code)
			} else if a.CountryCode != r.Code[:2] {
				g.fail("regions.csv: member %s of %s is in another country", m, r.Code)
			}
		}
		g.areas[r.Code] = &Area{Code: r.Code, Kind: kindRegion, CountryCode: r.Code[:2], NameEN: r.NameEN, NameJA: r.NameJA}
	}
}

// setTimeZones gives an area its country's zone when the country has one,
// and the zone of manual_area_time_zones.csv otherwise.
func (g *generator) setTimeZones() {
	for _, a := range g.areas {
		if a.Kind == kindRegion {
			continue
		}
		if zones := g.countryZones[a.CountryCode]; len(zones) == 1 {
			a.TimeZone = zones[0]
		}
	}
	for code, zone := range g.in.ManualAreaZones {
		a, ok := g.areas[code]
		if !ok || !a.FirstLevel() {
			g.fail("manual_area_time_zones.csv: %s is not a first-level area", code)
			continue
		}
		if !slices.Contains(g.countryZones[a.CountryCode], zone) {
			g.fail("manual_area_time_zones.csv: %s is not a zone of %s", zone, a.CountryCode)
			continue
		}
		a.TimeZone = zone
	}
}

// setReferencePoints gives each first-level area the first reference point
// available, in the order of the sources.
func (g *generator) setReferencePoints() {
	labels := map[string]Point{}
	children := map[string][]Point{}
	for _, p := range g.in.NaturalEarth {
		labels[p.Code] = p.Point
		if first, ok := g.firstLevel(p.Code); ok && first != p.Code {
			children[first] = append(children[first], p.Point)
		}
	}
	for code := range g.in.ManualPoints {
		if a, ok := g.areas[code]; !ok || !a.FirstLevel() {
			g.fail("manual_reference_points.csv: %s is not a first-level area", code)
		}
	}
	for _, a := range g.areas {
		if !a.FirstLevel() {
			continue
		}
		if p, ok := g.in.Legacy[a.Code]; ok {
			a.Reference, a.ReferenceFrom = &p, sourceLegacy
		} else if p, ok := labels[a.Code]; ok {
			a.Reference, a.ReferenceFrom = &p, sourceNELabel
		} else if ps := children[a.Code]; len(ps) > 0 {
			p := sphericalMean(ps)
			a.Reference, a.ReferenceFrom = &p, sourceNEChildren
		} else if p, ok := g.in.ManualPoints[a.Code]; ok {
			a.Reference, a.ReferenceFrom = &p, sourceManual
		}
	}
}

// sphericalMean is the normalized mean of the points as unit vectors.
func sphericalMean(ps []Point) Point {
	var x, y, z float64
	for _, p := range ps {
		lat, lng := p.Lat*math.Pi/180, p.Lng*math.Pi/180
		x += math.Cos(lat) * math.Cos(lng)
		y += math.Cos(lat) * math.Sin(lng)
		z += math.Sin(lat)
	}
	lat := math.Atan2(z, math.Hypot(x, y)) * 180 / math.Pi
	lng := math.Atan2(y, x) * 180 / math.Pi
	return Point{Lat: math.Round(lat*1e6) / 1e6, Lng: math.Round(lng*1e6) / 1e6}
}

// sorted returns the areas by code.
func (g *generator) sorted() []*Area {
	out := make([]*Area, 0, len(g.areas))
	for _, a := range g.areas {
		out = append(out, a)
	}
	slices.SortFunc(out, func(a, b *Area) int { return strings.Compare(a.Code, b.Code) })
	return out
}

// depth is how many parents and replacements lie above an area.
func (g *generator) depth(code string) int {
	d := 0
	for a := g.areas[code]; a != nil && d < 6; d++ {
		next := a.ReplacedByCode
		if next == "" {
			next = a.ParentCode
		}
		if next == "" {
			break
		}
		a = g.areas[next]
	}
	return d
}

func (g *generator) seed() *Seed {
	s := &Seed{}
	for _, a := range g.sorted() {
		s.Areas = append(s.Areas, *a)
	}
	slices.SortStableFunc(s.Areas, func(a, b Area) int { return g.depth(a.Code) - g.depth(b.Code) })

	zones := map[string]bool{}
	for _, z := range g.in.ZoneTab {
		zones[z.Zone] = true
	}
	for name := range zones {
		tz := TimeZone{
			Name:   name,
			NameEN: g.in.ExemplarCities["en"][name],
			NameJA: g.in.ExemplarCities["ja"][name],
		}
		if tz.NameEN == "" {
			tz.NameEN = strings.ReplaceAll(name[strings.LastIndex(name, "/")+1:], "_", " ")
		}
		if n, ok := g.in.ManualZoneNames[name]; ok {
			tz.NameEN, tz.NameJA = n.EN, n.JA
		}
		s.TimeZones = append(s.TimeZones, tz)
	}
	slices.SortFunc(s.TimeZones, func(a, b TimeZone) int { return strings.Compare(a.Name, b.Name) })

	s.CountryZones = slices.Clone(g.in.ZoneTab)
	slices.SortFunc(s.CountryZones, func(a, b ZoneEntry) int {
		return strings.Compare(a.Country+" "+a.Zone, b.Country+" "+b.Zone)
	})
	s.CountryZones = slices.Compact(s.CountryZones)

	for _, r := range g.in.Regions {
		for _, m := range r.Members {
			s.Memberships = append(s.Memberships, [2]string{r.Code, m})
		}
	}
	return s
}

// resolveByHand is what the upgrading change does when compare refuses.
const resolveByHand = "write a migration that remaps or deletes it, and in the same change remove it from scripts/areas/seed.json"

// compare refuses a seed that loses a code, a zone, a region membership or a
// country's zone of the previous one, or in which a first-level code stops
// being first-level: stored venues, homes and sales may reference them, and
// the seed never deletes, so the upgrading change must remap or delete them.
func (g *generator) compare(s *Seed, prev Snapshot) {
	now := s.Snapshot()
	for _, code := range slices.Sorted(maps.Keys(prev.Areas)) {
		first, ok := now.Areas[code]
		switch {
		case !ok:
			g.fail("%s was in the previous seed and is missing now: %s", code, resolveByHand)
		case prev.Areas[code] && !first:
			g.fail("%s was first-level in the previous seed and is not now: %s", code, resolveByHand)
		}
	}
	for _, z := range prev.TimeZones {
		if !slices.Contains(now.TimeZones, z) {
			g.fail("time zone %s was in the previous seed and is missing now: %s", z, resolveByHand)
		}
	}
	// The seed never deletes, so a region membership or a country's zone
	// that disappears would stay in the database; the upgrading change must
	// delete it by hand.
	for _, key := range slices.Sorted(maps.Keys(prev.Rows)) {
		if strings.HasPrefix(key, "membership ") || strings.HasPrefix(key, "country_zone ") {
			if _, ok := now.Rows[key]; !ok {
				g.fail("%s was in the previous seed and is missing now: %s", key, resolveByHand)
			}
		}
	}
}
