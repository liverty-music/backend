package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Pinned releases. Changing one is the upgrade; the weekly check in
// .github/workflows/areas-release-check.yml opens an issue when a newer one
// exists.
const (
	cldrTag         = "release-49"
	tzdataVersion   = "2026f"
	naturalEarthTag = "v5.1.2"
)

var cldrFiles = []string{
	"common/supplemental/subdivisions.xml",
	"common/supplemental/supplementalMetadata.xml",
	"common/subdivisions/en.xml",
	"common/subdivisions/ja.xml",
	"common/main/en.xml",
	"common/main/ja.xml",
}

const naturalEarthAdmin1 = "10m_cultural/ne_10m_admin_1_states_provinces.dbf"

// fetch downloads the pinned sources into dir, keeping files already there.
func fetch(dir string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	get := func(url, dest string) error {
		if _, err := os.Stat(dest); err == nil {
			return nil
		}
		resp, err := client.Get(url)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("GET %s: %s", url, resp.Status)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		tmp := dest + ".part"
		f, err := os.Create(tmp)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, resp.Body); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		return os.Rename(tmp, dest)
	}
	for _, f := range cldrFiles {
		if err := get("https://raw.githubusercontent.com/unicode-org/cldr/"+cldrTag+"/"+f, filepath.Join(dir, "cldr", f)); err != nil {
			return err
		}
	}
	if err := get("https://data.iana.org/time-zones/releases/tzdata"+tzdataVersion+".tar.gz", filepath.Join(dir, "tzdata.tar.gz")); err != nil {
		return err
	}
	return get("https://raw.githubusercontent.com/nvkelso/natural-earth-vector/"+naturalEarthTag+"/"+naturalEarthAdmin1, filepath.Join(dir, "admin1.dbf"))
}

// readInputs parses the downloaded sources in srcDir and the hand-kept files
// in manualDir.
func readInputs(srcDir, manualDir string) (Inputs, error) {
	var in Inputs
	var err error
	cldr := func(f string) string { return filepath.Join(srcDir, "cldr", f) }

	if in.Subgroups, err = readSubgroups(cldr("common/supplemental/subdivisions.xml")); err != nil {
		return in, err
	}
	if in.Aliases, err = readAliases(cldr("common/supplemental/supplementalMetadata.xml")); err != nil {
		return in, err
	}
	in.SubdivisionNames = map[string]map[string]string{}
	in.TerritoryNames = map[string]map[string]string{}
	in.ExemplarCities = map[string]map[string]string{}
	for _, lang := range []string{"en", "ja"} {
		if in.SubdivisionNames[lang], err = readSubdivisionNames(cldr("common/subdivisions/" + lang + ".xml")); err != nil {
			return in, err
		}
		if in.TerritoryNames[lang], in.ExemplarCities[lang], err = readLocale(cldr("common/main/" + lang + ".xml")); err != nil {
			return in, err
		}
	}
	if in.ZoneTab, err = readZoneTab(filepath.Join(srcDir, "tzdata.tar.gz")); err != nil {
		return in, err
	}
	if in.NaturalEarth, err = readNaturalEarth(filepath.Join(srcDir, "admin1.dbf")); err != nil {
		return in, err
	}
	return in, readManual(&in, manualDir)
}

func readXML(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false // the CLDR files carry a DOCTYPE with a relative DTD
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func readSubgroups(path string) (map[string][]string, error) {
	var doc struct {
		Subgroups []struct {
			Type     string `xml:"type,attr"`
			Contains string `xml:"contains,attr"`
		} `xml:"subdivisionContainment>subgroup"`
	}
	if err := readXML(path, &doc); err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, s := range doc.Subgroups {
		out[s.Type] = append(out[s.Type], strings.Fields(s.Contains)...)
	}
	return out, nil
}

func readAliases(path string) ([]Alias, error) {
	var doc struct {
		Aliases []struct {
			Type        string `xml:"type,attr"`
			Replacement string `xml:"replacement,attr"`
		} `xml:"metadata>alias>subdivisionAlias"`
	}
	if err := readXML(path, &doc); err != nil {
		return nil, err
	}
	out := make([]Alias, 0, len(doc.Aliases))
	for _, a := range doc.Aliases {
		out = append(out, Alias{Code: a.Type, Replacements: strings.Fields(a.Replacement)})
	}
	return out, nil
}

func readSubdivisionNames(path string) (map[string]string, error) {
	var doc struct {
		Names []struct {
			Type string `xml:"type,attr"`
			Name string `xml:",chardata"`
		} `xml:"localeDisplayNames>subdivisions>subdivision"`
	}
	if err := readXML(path, &doc); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, n := range doc.Names {
		out[n.Type] = strings.TrimRight(strings.TrimSpace(n.Name), superscripts)
	}
	return out, nil
}

// superscripts are the marks CLDR appends to tell apart two subdivisions of a
// country with the same name ("Île-de-France²" for the region, beside the
// former FR-J). They are not part of the name: Area.Resolve matches the name
// people type, and Resolve already treats a shared name as ambiguous unless
// both lead to the same first-level Area.
const superscripts = "⁰¹²³⁴⁵⁶⁷⁸⁹"

// readLocale reads territory names (the standard form, e.g. "Hong Kong SAR
// China", not CLDR's alt="short" "Hong Kong") and exemplar cities.
func readLocale(path string) (map[string]string, map[string]string, error) {
	var doc struct {
		Territories []struct {
			Type string `xml:"type,attr"`
			Alt  string `xml:"alt,attr"`
			Name string `xml:",chardata"`
		} `xml:"localeDisplayNames>territories>territory"`
		Zones []struct {
			Type string `xml:"type,attr"`
			City string `xml:"exemplarCity"`
		} `xml:"dates>timeZoneNames>zone"`
	}
	if err := readXML(path, &doc); err != nil {
		return nil, nil, err
	}
	territories := map[string]string{}
	for _, t := range doc.Territories {
		if t.Alt == "" {
			if _, ok := territories[t.Type]; !ok {
				territories[t.Type] = strings.TrimSpace(t.Name)
			}
		}
	}
	cities := map[string]string{}
	for _, z := range doc.Zones {
		if c := strings.TrimSpace(z.City); c != "" {
			cities[z.Type] = c
		}
	}
	return territories, cities, nil
}

// readZoneTab reads zone.tab from the tzdata archive: one line per country
// and zone, so a country keeps a zone named for it.
func readZoneTab(archive string) ([]ZoneEntry, error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("zone.tab is not in the tzdata archive")
		}
		if err != nil {
			return nil, err
		}
		if h.Name != "zone.tab" {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		return parseZoneTab(string(b)), nil
	}
}

func parseZoneTab(s string) []ZoneEntry {
	var out []ZoneEntry
	for line := range strings.SplitSeq(s, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) >= 3 {
			out = append(out, ZoneEntry{Country: cols[0], Zone: cols[2]})
		}
	}
	return out
}

// readNaturalEarth reads the label point (latitude, longitude) and ISO 3166-2
// code of each admin-1 feature from the dBASE table of the shapefile.
func readNaturalEarth(path string) ([]NEPoint, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) < 32 {
		return nil, errors.New("admin1.dbf: short header")
	}
	records := int(binary.LittleEndian.Uint32(b[4:8]))
	headerLen := int(binary.LittleEndian.Uint16(b[8:10]))
	recordLen := int(binary.LittleEndian.Uint16(b[10:12]))
	type field struct {
		name        string
		offset, len int
	}
	fields := map[string]field{}
	offset := 1 // the deletion flag
	for p := 32; p+32 <= headerLen && b[p] != 0x0D; p += 32 {
		name, _, _ := bytes.Cut(b[p:p+11], []byte{0})
		l := int(b[p+16])
		fields[string(name)] = field{name: string(name), offset: offset, len: l}
		offset += l
	}
	get := func(rec []byte, name string) string {
		f, ok := fields[name]
		if !ok {
			return ""
		}
		return strings.Trim(string(rec[f.offset:f.offset+f.len]), " \x00")
	}
	for _, name := range []string{"iso_3166_2", "latitude", "longitude"} {
		if _, ok := fields[name]; !ok {
			return nil, fmt.Errorf("admin1.dbf: no %s field", name)
		}
	}
	var out []NEPoint
	for i := range records {
		start := headerLen + i*recordLen
		if start+recordLen > len(b) {
			return nil, errors.New("admin1.dbf: truncated")
		}
		rec := b[start : start+recordLen]
		if rec[0] == '*' {
			continue
		}
		code := get(rec, "iso_3166_2")
		lat, err1 := strconv.ParseFloat(get(rec, "latitude"), 64)
		lng, err2 := strconv.ParseFloat(get(rec, "longitude"), 64)
		if !subdivisionRe.MatchString(code) || err1 != nil || err2 != nil {
			continue
		}
		out = append(out, NEPoint{Code: code, Point: Point{Lat: lat, Lng: lng}})
	}
	return out, nil
}

// readCSV reads a hand-kept file: a header row, then records; lines starting
// with # are comments.
func readCSV(path string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	r := csv.NewReader(f)
	r.Comment = '#'
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%s: no header", path)
	}
	return rows[1:], nil
}

func readManual(in *Inputs, dir string) error {
	file := func(name string) string { return filepath.Join(dir, name) }
	var err error
	if in.Decisions, err = readDecisions(file("manual_replacements.csv")); err != nil {
		return err
	}
	if in.Legacy, err = readPoints(file("legacy_prefecture_points.csv")); err != nil {
		return err
	}
	if in.ManualPoints, err = readPoints(file("manual_reference_points.csv")); err != nil {
		return err
	}
	if in.ManualAreaZones, err = readAreaZones(file("manual_area_time_zones.csv")); err != nil {
		return err
	}
	if in.ManualZoneNames, err = readZoneNames(file("manual_time_zone_names.csv")); err != nil {
		return err
	}
	in.Regions, err = readRegions(file("regions.csv"))
	return err
}

// readRows reads a hand-kept file and checks that each record has at least
// the columns the header promises.
func readRows(path string, columns int) ([][]string, error) {
	rows, err := readCSV(path)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if len(r) < columns {
			return nil, fmt.Errorf("%s: want %d columns: %v", filepath.Base(path), columns, r)
		}
	}
	return rows, nil
}

func readDecisions(path string) ([]Decision, error) {
	rows, err := readRows(path, 3)
	out := make([]Decision, 0, len(rows))
	for _, r := range rows {
		out = append(out, Decision{Code: r[0], Decision: r[1], Target: r[2]})
	}
	return out, err
}

func readPoints(path string) (map[string]Point, error) {
	rows, err := readRows(path, 3)
	if err != nil {
		return nil, err
	}
	out := map[string]Point{}
	for _, r := range rows {
		lat, err1 := strconv.ParseFloat(r[1], 64)
		lng, err2 := strconv.ParseFloat(r[2], 64)
		if err := errors.Join(err1, err2); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		out[r[0]] = Point{Lat: lat, Lng: lng}
	}
	return out, nil
}

func readAreaZones(path string) (map[string]string, error) {
	rows, err := readRows(path, 2)
	out := map[string]string{}
	for _, r := range rows {
		out[r[0]] = r[1]
	}
	return out, err
}

func readZoneNames(path string) (map[string]ZoneName, error) {
	rows, err := readRows(path, 3)
	out := map[string]ZoneName{}
	for _, r := range rows {
		out[r[0]] = ZoneName{EN: r[1], JA: r[2]}
	}
	return out, err
}

func readRegions(path string) ([]Region, error) {
	rows, err := readRows(path, 4)
	out := make([]Region, 0, len(rows))
	for _, r := range rows {
		out = append(out, Region{Code: r[0], NameEN: r[1], NameJA: r[2], Members: strings.Fields(r[3])})
	}
	return out, err
}
