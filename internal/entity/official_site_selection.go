package entity

import (
	"net/url"
	"strings"
)

// OfficialSiteRelation is one official homepage link an external catalog
// (MusicBrainz) records for an artist, as the catalog states it.
type OfficialSiteRelation struct {
	// URL is the linked address.
	URL string
	// SourceCredit is the name the link is credited to, "" when the catalog
	// credits no name.
	SourceCredit string
	// Ended reports whether the catalog marks the link as no longer in use.
	Ended bool
}

// OfficialSiteRelations is what an external catalog records about an
// artist's official homepages.
type OfficialSiteRelations struct {
	// ArtistName is the artist's name in the catalog.
	ArtistName string
	// Relations are the artist's official homepage links, in catalog order.
	Relations []OfficialSiteRelation
}

// SelectOfficialSite picks the artist's official site among the catalog's
// official homepage links and returns its URL, or "" when there is no link in
// use.
//
// Ended links are ignored. When any remaining link points at a site's top
// page, deeper links (such as /artist/<name> pages on label sites) are
// dropped. Among the rest, the first link credited to the artist's name
// (ignoring case) wins, then the first link credited to no name, then the
// first link.
func SelectOfficialSite(rels OfficialSiteRelations) string {
	var active, topPages []OfficialSiteRelation
	for _, r := range rels.Relations {
		if r.Ended {
			continue
		}
		active = append(active, r)
		if isTopPageURL(r.URL) {
			topPages = append(topPages, r)
		}
	}
	if len(topPages) > 0 {
		active = topPages
	}

	var uncredited, first string
	for _, r := range active {
		if strings.EqualFold(r.SourceCredit, rels.ArtistName) {
			return r.URL
		}
		if r.SourceCredit == "" && uncredited == "" {
			uncredited = r.URL
		}
		if first == "" {
			first = r.URL
		}
	}
	if uncredited != "" {
		return uncredited
	}
	return first
}

// isTopPageURL reports whether raw points at a site's top page: a URL with a
// host, no path other than "/", no query and no fragment.
func isTopPageURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	return (u.Path == "" || u.Path == "/") && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
}
