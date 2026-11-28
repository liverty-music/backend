package entity

import (
	"log/slog"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// CatalogArtist is an artist as the music catalog (MusicBrainz) records it,
// not a registered [Artist]: an admin compares the CatalogArtists that share a
// registered Artist's name to decide which one that Artist is. Its life span
// is Begin, End and ActivityEnded together.
//
// See [CatalogArtistProto] for the wire representation.
//
// [CatalogArtistProto]: https://github.com/liverty-music/specification/blob/main/proto/liverty_music/entity/v1/catalog_artist.proto
type CatalogArtist struct {
	// MBID is the artist's identifier in the catalog.
	MBID string
	// Name is the artist's name in the catalog.
	Name string
	// Country is the ISO 3166-1 alpha-2 code of the country the catalog
	// associates with the artist; nil when it records none.
	Country *string
	// Begin is when the artist's activity began, as precise as the catalog
	// records it; nil when it records no beginning.
	Begin *ActivityDate
	// End is when the artist's activity ended, as precise as the catalog
	// records it; nil while it records no end.
	End *ActivityDate
	// ActivityEnded reports whether the catalog marks the artist's activity
	// as ended. The catalog records it apart from End, so it can be true while
	// End is nil.
	ActivityEnded bool
	// Disambiguation is the catalog's short note that tells the artist apart
	// from others of the same name; nil when the catalog has none.
	Disambiguation *string
	// OfficialSiteURL is the artist's official homepage, chosen with
	// [SelectOfficialSite]; nil when the catalog has no active official
	// homepage link.
	OfficialSiteURL *string
}

// ActivityDate is a day an artist's activity began or ended, as precise as the
// music catalog records it: a year, a year and month (Day 0), or a full date.
type ActivityDate struct {
	// Year is always set.
	Year int
	// Month is 1 to 12, or 0 when only the year is known.
	Month int
	// Day is 1 to 31, or 0 when only the year and month are known.
	Day int
}

// Validate checks that the CatalogArtist has an MBID of 36 characters and a
// name.
//
// # Possible errors
//
//   - InvalidArgument: the MBID is not 36 characters long, or the name is
//     empty.
func (c *CatalogArtist) Validate() error {
	if err := ValidateMBID(c.MBID); err != nil {
		return err
	}
	if c.Name == "" {
		return apperr.New(codes.InvalidArgument, "catalog artist name must not be empty", slog.String("mbid", c.MBID))
	}
	return nil
}
