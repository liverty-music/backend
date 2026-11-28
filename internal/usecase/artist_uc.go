package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/analyticslog"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

// ArtistUseCase defines the interface for artist-related business logic and orchestration.
type ArtistUseCase interface {
	// Create registers an artist under the music catalog's name for its MBID
	// (the given name when the catalog lookup fails), with the MBID as both
	// its MBID and its listening MBID, its MBID Verified (the caller, an
	// admin, names it) and the activity status the lookup returned (false
	// when it failed). It returns the registered artist, which is the already
	// registered one when the MBID is known.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: the MBID is not 36 characters long.
	//   - Internal: unexpected failure during creation.
	Create(ctx context.Context, artist *entity.Artist) (*entity.Artist, error)

	// List retrieves all artists registered in the system database.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	List(ctx context.Context) ([]*entity.Artist, error)

	// Get returns one artist by its id.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: the id is not a UUID.
	//   - NotFound: no artist has the id.
	//   - Internal: database query failure.
	Get(ctx context.Context, id string) (*entity.Artist, error)

	// GetOfficialSite retrieves the primary official website for an artist.
	//
	// # Possible errors:
	//
	//   - NotFound: the artist exists but has no official site.
	//   - Internal: query failure.
	GetOfficialSite(ctx context.Context, artistID string) (*entity.OfficialSite, error)

	// Search finds the listening service's matches for the query and returns
	// them as registered artists, only the ones offered to fans (Verified,
	// activity not ended). A match not registered yet has its MBID decided
	// as VerifyMBID decides it before it is registered; a match whose decision
	// fails is registered Unverified and not returned.
	//
	// # Possible errors:
	//
	//   - NotFound: no matching artists found.
	//   - Unavailable: external discovery service failure.
	//   - ResourceExhausted: external discovery service rate limit exceeded.
	//   - DeadlineExceeded: external discovery service request timed out.
	//   - Internal: unexpected failure while persisting discovered artists.
	Search(ctx context.Context, query string) ([]*entity.Artist, error)

	// ListSimilar returns the artists the listening service lists as similar
	// to the target artist (looked up by its listening MBID), only the ones
	// offered to fans. A result without an MBID stands for the registered
	// artist, other than the target, that alone is known by its name; it
	// registers nothing. When limit is greater than zero, the result is capped
	// to that many entries; otherwise every offered artist of the 50-artist
	// lookup is returned. Artists it registers are Unverified.
	//
	// # Possible errors:
	//
	//   - NotFound: target artist not found in local or external catalogs.
	//   - Internal: service failure.
	ListSimilar(ctx context.Context, artistID string, limit int32) ([]*entity.Artist, error)

	// ListTop returns the chart's artists, optionally filtered by country or
	// genre tag, only the ones offered to fans. When limit is greater than
	// zero, the chart lookup is capped to that many entries; otherwise the
	// external service's default is used. Artists it registers are
	// Unverified.
	//
	// # Possible errors:
	//
	//   - Unavailable: external chart service failure.
	ListTop(ctx context.Context, country string, tag string, limit int32) ([]*entity.Artist, error)

	// ListByOrganizer returns the Artists the Organizer currently represents,
	// whatever its status. It checks no ownership: the admin names any
	// Organizer, and the organizer console passes the caller's own.
	//
	// # Possible errors
	//
	//   - NotFound: no Organizer has the id.
	ListByOrganizer(ctx context.Context, organizerID string) ([]*entity.Artist, error)

	// Refresh reads the artist again from the music catalog by its MBID. It
	// adopts the catalog's name when it is not empty and differs from
	// currentName, and then stores the catalog's known names of the artist and
	// whether it marks the artist's activity as ended. These are stored only
	// while the artist still has that MBID. An MBID the catalog does not know
	// changes nothing and succeeds, since reading it again cannot succeed.
	//
	// # Possible errors:
	//
	//   - Unavailable: the catalog is down or rate-limited.
	//   - NotFound: the artist does not exist.
	//   - Internal: unexpected failure.
	Refresh(ctx context.Context, artistID, mbid, currentName string) error

	// CorrectMBID gives an artist the MBID an admin chose: it looks the MBID
	// up in the music catalog and stores, with one write, the canonical MBID
	// the catalog returns for it with the catalog's name (the current name
	// when the catalog gives none) and activity status, marking the MBID
	// Verified and keeping the listening MBID. It returns the corrected
	// artist. It reads the catalog before storing anything, so every failure
	// leaves the artist unchanged.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: mbid is not 36 characters long.
	//   - NotFound: the artist does not exist, or the catalog knows no artist
	//     with the MBID.
	//   - AlreadyExists: another artist has the MBID.
	//   - Unavailable: the catalog is down or rate-limited.
	//   - Internal: unexpected failure.
	CorrectMBID(ctx context.Context, artistID, mbid string) (*entity.Artist, error)

	// VerifyMBID checks an Unverified artist's MBID against the music
	// catalog's artists of the same name (components/usecase/artist/
	// verify-mbid): it marks the artist Verified with the catalog's name and
	// activity status, corrects it to the one namesake with an official
	// homepage, or marks it Ambiguous and logs an error so the owner is
	// alerted. An artist that is not Unverified is left unchanged. A failed
	// catalog read leaves the artist Unverified and unchanged.
	//
	// # Possible errors:
	//
	//   - NotFound: the artist does not exist.
	//   - Unavailable: the catalog is down or rate-limited.
	//   - Internal: unexpected failure.
	VerifyMBID(ctx context.Context, artistID string) error

	// ListByMBIDStatus returns the artists whose MBID is in the status, the
	// one registered first listed first.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	ListByMBIDStatus(ctx context.Context, status entity.MBIDStatus) ([]*entity.Artist, error)

	// RefreshDue runs one daily refresh. It first verifies, one at a time and
	// oldest first, the MBID of every Unverified artist; then it refreshes,
	// one at a time, every artist in today's slot of a 30-day rotation (the
	// days from 1970-01-01 to today in Japan time, modulo 30), so every
	// artist is read again once every 30 days. A failed artist does not stop
	// either part, except that each part stops after 3 consecutive failures;
	// both stop when ctx is done.
	//
	// # Possible errors:
	//
	//   - Internal: listing the Unverified artists or the slot's artists failed.
	RefreshDue(ctx context.Context) (ArtistRefreshSummary, error)
}

// ArtistRefreshSummary reports what one daily RefreshDue run did.
type ArtistRefreshSummary struct {
	// Unverified is the number of Unverified artists listed for verification.
	Unverified int
	// VerifyAttempted is the number of artists the run tried to verify.
	VerifyAttempted int
	// Verified is the number of artists the run left Verified.
	Verified int
	// Ambiguous is the number of artists the run left Ambiguous.
	Ambiguous int
	// VerifyFailed is the number of artists whose verification failed; they
	// stay Unverified.
	VerifyFailed int
	// VerifyCircuitBroken reports whether verification stopped after
	// consecutive failures.
	VerifyCircuitBroken bool
	// Slot is the rotation slot the run refreshed.
	Slot int
	// Listed is the number of artists in the slot.
	Listed int
	// Attempted is the number of artists the run tried to refresh.
	Attempted int
	// Ended is the number of refreshed artists the catalog marks as ended.
	Ended int
	// Renamed is the number of refreshed artists whose name changed.
	Renamed int
	// Failed is the number of attempted artists whose refresh failed.
	Failed int
	// CircuitBroken reports whether the run stopped after consecutive failures.
	CircuitBroken bool
}

// ArtistCacheTTL is how long ArtistUseCase reuses a catalog result (Search,
// ListSimilar, ListTop) before consulting the catalog again. Charts and
// catalog matches do not change materially within hours.
const ArtistCacheTTL = 6 * time.Hour

// similarLookupLimit is the number of similar artists ListSimilar asks the
// catalog for on a cache miss, whatever limit the caller requested. One
// cached lookup per artist then serves every limit up to it.
const similarLookupLimit = 50

// artistUseCase implements the ArtistUseCase interface.
type artistUseCase struct {
	artistRepo     entity.ArtistRepository
	organizerRepo  entity.OrganizerRepository
	artistSearcher entity.ArtistSearcher
	idManager      entity.ArtistIdentityManager
	officialSites  entity.OfficialSiteCatalog
	namesakes      entity.NamesakeCatalog
	cache          entity.Cache
	clock          Clock
	logger         *logging.Logger
}

// Compile-time interface compliance check
var _ ArtistUseCase = (*artistUseCase)(nil)

// NewArtistUseCase creates a new instance of the artist business logic handler.
// officialSites and namesakes are the music catalog's reads VerifyMBID and
// Search decide MBIDs with; clock gives the day RefreshDue refreshes.
func NewArtistUseCase(
	artistRepo entity.ArtistRepository,
	organizerRepo entity.OrganizerRepository,
	artistSearcher entity.ArtistSearcher,
	idManager entity.ArtistIdentityManager,
	officialSites entity.OfficialSiteCatalog,
	namesakes entity.NamesakeCatalog,
	cache entity.Cache,
	clock Clock,
	logger *logging.Logger,
) ArtistUseCase {
	return &artistUseCase{
		artistRepo:     artistRepo,
		organizerRepo:  organizerRepo,
		artistSearcher: artistSearcher,
		idManager:      idManager,
		officialSites:  officialSites,
		namesakes:      namesakes,
		cache:          cache,
		clock:          clock,
		logger:         logger,
	}
}

// Create creates a new artist.
func (uc *artistUseCase) Create(ctx context.Context, artist *entity.Artist) (*entity.Artist, error) {
	if err := entity.ValidateMBID(artist.MBID); err != nil {
		return nil, err
	}
	// Normalize artist name using MBID
	artist.ActivityEnded = false
	// Without the catalog's names the artist is known by its name only, which
	// Artist.Create adds.
	artist.KnownNames = nil
	mbArtist, err := uc.idManager.GetArtist(ctx, artist.MBID)
	if err != nil {
		// Log warning but proceed with provided name if normalization fails
		uc.logger.Warn(ctx, "failed to normalize artist name from MBID", slog.String("mbid", artist.MBID), slog.Any("error", err))
	} else {
		artist.ActivityEnded = mbArtist.ActivityEnded
		artist.KnownNames = mbArtist.KnownNames
		if artist.Name != mbArtist.Name {
			// Update name to canonical name from MusicBrainz
			artist.Name = mbArtist.Name
			artist.MBID = mbArtist.MBID // Ensure MBID is set from canonical source
		}
	}

	// The caller (an admin, the only one offered Create) names the MBID, so
	// it is not checked against namesakes; its announcement verifies nothing.
	artist.ListeningMBID = artist.MBID
	artist.MBIDStatus = entity.MBIDStatusVerified

	created, err := uc.artistRepo.Create(ctx, artist)
	if err != nil {
		return nil, err
	}

	if len(created) == 0 {
		return nil, apperr.New(codes.Internal, "artist creation returned no results")
	}

	uc.logger.Info(ctx, "Artist created successfully", slog.String("artist_id", created[0].ID), slog.String("mbid", created[0].MBID))

	return created[0], nil
}

// List returns all artists.
func (uc *artistUseCase) List(ctx context.Context) ([]*entity.Artist, error) {
	artists, err := uc.artistRepo.List(ctx)
	if err != nil {
		return nil, err
	}

	return artists, nil
}

// GetOfficialSite retrieves the official site for an artist.
func (uc *artistUseCase) GetOfficialSite(ctx context.Context, artistID string) (*entity.OfficialSite, error) {
	site, err := uc.artistRepo.GetOfficialSite(ctx, artistID)
	if err != nil {
		return nil, err
	}

	return site, nil
}

// Search finds artists matching the query using the primary external discovery service.
// Results are cached to reduce external API calls.
func (uc *artistUseCase) Search(ctx context.Context, query string) ([]*entity.Artist, error) {
	// Check cache first
	cacheKey := fmt.Sprintf("search:%s", hashString(query))
	if cached := uc.cache.Get(cacheKey); cached != nil {
		if artists, ok := cached.([]*entity.Artist); ok {
			return uc.offeredNow(ctx, artists)
		}
	}

	// Cache miss - fetch from external API. Propagate the port's error as-is
	// (mirroring ListSimilar/ListTop) so its code (NotFound, Unavailable,
	// ResourceExhausted, DeadlineExceeded, ...) survives to the caller instead
	// of being collapsed into Internal.
	artists, err := uc.artistSearcher.Search(ctx, query)
	if err != nil {
		return nil, err
	}

	// Filter out entries with empty MBID and dedup by MBID keeping first occurrence.
	filtered := entity.FilterArtistsByMBID(artists)

	if len(filtered) == 0 {
		return nil, apperr.New(codes.NotFound, "no artists found")
	}

	// Persist artists to ensure stable database IDs, deciding the MBID of
	// each new match first so a first search returns verified artists.
	persisted, err := uc.persistArtists(ctx, filtered, uc.decideBeforeRegistering)
	if err != nil {
		return nil, err
	}

	// The cache keeps the artists not offered to fans: a later call reads
	// their status again.
	uc.cache.Set(cacheKey, persisted)

	return offered(persisted), nil
}

// SearchDecisionBudget is how long Search spends deciding the MBIDs of new
// matches. The music catalog answers 1 request per second and each decision
// takes 2 or more, so a name with many new matches would otherwise outlast
// the request's own deadline and register nothing. Matches left when the
// budget runs out are registered Unverified, and VerifyMBID decides them from
// their announcement (design "Risks", the search latency fallback).
const SearchDecisionBudget = 15 * time.Second

// decideBeforeRegistering decides the MBID of each artist about to be
// registered by Search, in order, within SearchDecisionBudget: a Verified
// artist takes the decided MBID and the catalog's name and activity status,
// keeping its listening MBID; an Ambiguous one is logged; one whose decision
// fails or is not reached within the budget stays Unverified.
func (uc *artistUseCase) decideBeforeRegistering(ctx context.Context, artists []*entity.Artist) {
	decideCtx, cancel := context.WithTimeout(ctx, SearchDecisionBudget)
	defer cancel()
	for i, a := range artists {
		if decideCtx.Err() != nil {
			uc.logger.Warn(ctx, "search decision budget spent; registering the remaining new matches unverified",
				slog.Int("undecided", len(artists)-i),
			)
			return
		}
		decision, err := uc.decideMBID(decideCtx, a.Name, a.MBID)
		if err != nil {
			uc.logger.Warn(ctx, "could not verify the MBID of a new search match; registering it unverified",
				slog.String("artist_name", a.Name),
				slog.String("mbid", a.MBID),
				slog.Any("error", err),
			)
			continue
		}
		a.MBIDStatus = decision.status
		if decision.status == entity.MBIDStatusAmbiguous {
			uc.logAmbiguous(ctx, a.ID, a.Name, decision)
			continue
		}
		a.MBID = decision.mbid
		a.Name = decision.name
		a.ActivityEnded = decision.activityEnded
		a.KnownNames = decision.knownNames
	}
}

// ListSimilar retrieves artists similar to a specified artist.
// The catalog is asked for similarLookupLimit artists once per artist and the
// result is cached under the artist id alone, so calls with any limit share it.
// Each call leaves out the artists not offered to fans and returns the first
// limit of the rest, or all of them when limit is 0.
// Fetched artists with an MBID are auto-persisted to ensure valid database IDs;
// those without one are matched to registered artists by name.
func (uc *artistUseCase) ListSimilar(ctx context.Context, artistID string, limit int32) ([]*entity.Artist, error) {
	cacheKey := fmt.Sprintf("similar:%s", artistID)
	if cached := uc.cache.Get(cacheKey); cached != nil {
		if artists, ok := cached.([]*entity.Artist); ok {
			active, err := uc.offeredNow(ctx, artists)
			if err != nil {
				return nil, err
			}
			return firstN(active, limit), nil
		}
	}

	// Cache miss - fetch artist and get similar artists
	artist, err := uc.artistRepo.Get(ctx, artistID)
	if err != nil {
		return nil, err
	}

	artists, err := uc.artistSearcher.ListSimilar(ctx, artist, similarLookupLimit)
	if err != nil {
		return nil, err
	}

	// Register the entries with an MBID, then match the others by name.
	byMBID, err := uc.registerArtists(ctx, entity.FilterArtistsByMBID(artists), nil)
	if err != nil {
		return nil, err
	}
	matched, err := uc.matchByName(ctx, artist.ID, artists)
	if err != nil {
		return nil, err
	}

	similar := listedOnce(artists, func(a *entity.Artist) *entity.Artist {
		if a.MBID == "" {
			return matched[a]
		}
		return registeredAs(byMBID, a)
	})

	// Cache the full lookup so a later call with a larger limit is served too.
	// The cache keeps the artists not offered to fans: a later call reads
	// their status again.
	uc.cache.Set(cacheKey, similar)

	return firstN(offered(similar), limit), nil
}

// matchByName returns, for each result without an MBID that stands for a
// registered artist, that artist: the only registered one known by the result's
// name, offered to fans or not, unless it is the seed. It looks all the names
// up with one call, registers nothing and logs how many results matched.
func (uc *artistUseCase) matchByName(ctx context.Context, seedID string, results []*entity.Artist) (map[*entity.Artist]*entity.Artist, error) {
	var unnamed []*entity.Artist
	var names []string
	for _, r := range results {
		if r.MBID == "" {
			unnamed = append(unnamed, r)
			names = append(names, r.Name)
		}
	}
	if len(unnamed) == 0 {
		return nil, nil
	}

	registered, err := uc.artistRepo.ListByKnownNames(ctx, names)
	if err != nil {
		return nil, err
	}

	matched := make(map[*entity.Artist]*entity.Artist, len(unnamed))
	var single, several int
	unmatched := []string{}
	for _, r := range unnamed {
		var known []*entity.Artist
		for _, a := range registered {
			if a.IsKnownAs(r.Name) {
				known = append(known, a)
			}
		}
		switch len(known) {
		case 0:
			unmatched = append(unmatched, r.Name)
		case 1:
			single++
			if known[0].ID != seedID {
				matched[r] = known[0]
			}
		default:
			several++
		}
	}

	// These counts decide whether looking unmatched names up in the music
	// catalog is worth building, so they are kept for analysis.
	uc.logger.Info(ctx, "similar artists without MBID", analyticslog.Attrs(analyticslog.TopicCatalogSync,
		slog.String("seed_artist_id", seedID),
		slog.Int("matched", single),
		slog.Int("matched_several", several),
		slog.Int("unmatched", len(unmatched)),
		slog.Any("unmatched_names", unmatched),
	)...)

	return matched, nil
}

// firstN returns the first n artists, or all of them when n is 0 or exceeds
// the length. The returned slice has its capacity clipped so a caller's append
// cannot write into the cached backing array.
func firstN(artists []*entity.Artist, n int32) []*entity.Artist {
	if n <= 0 || int(n) >= len(artists) {
		return artists[:len(artists):len(artists)]
	}
	return artists[:n:n]
}

// ListTop retrieves popular artists, leaving out those not offered to fans.
// Results are cached to reduce external API calls.
// Fetched artists are auto-persisted to ensure valid database IDs.
func (uc *artistUseCase) ListTop(ctx context.Context, country string, tag string, limit int32) ([]*entity.Artist, error) {
	// Check cache first
	cacheKey := fmt.Sprintf("top:%s:%s:%d", country, tag, limit)
	if cached := uc.cache.Get(cacheKey); cached != nil {
		if artists, ok := cached.([]*entity.Artist); ok {
			return uc.offeredNow(ctx, artists)
		}
	}

	// Cache miss - fetch from external API
	artists, err := uc.artistSearcher.ListTop(ctx, country, tag, limit)
	if err != nil {
		return nil, err
	}

	// Filter out entries with empty MBID.
	filtered := entity.FilterArtistsByMBID(artists)

	// Persist via read-then-write helper.
	persisted, err := uc.persistArtists(ctx, filtered, nil)
	if err != nil {
		return nil, err
	}

	// Store in cache. The cache keeps the artists not offered to fans: a
	// later call reads their status again.
	uc.cache.Set(cacheKey, persisted)

	return offered(persisted), nil
}

// offered returns the artists offered to fans (Verified, activity not ended),
// in order.
func offered(artists []*entity.Artist) []*entity.Artist {
	result := make([]*entity.Artist, 0, len(artists))
	for _, a := range artists {
		if a.IsOffered() {
			result = append(result, a)
		}
	}
	return result
}

// offeredNow returns the artists of an earlier result that are offered to
// fans by now, in order, as they are now: it reads them again with one
// ListByMBIDs call by their listening MBIDs, which never change.
func (uc *artistUseCase) offeredNow(ctx context.Context, artists []*entity.Artist) ([]*entity.Artist, error) {
	if len(artists) == 0 {
		return []*entity.Artist{}, nil
	}
	mbids := make([]string, len(artists))
	for i, a := range artists {
		mbids[i] = a.ListeningMBID
	}
	current, err := uc.artistRepo.ListByMBIDs(ctx, mbids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*entity.Artist, len(current))
	for _, a := range current {
		byID[a.ID] = a
	}
	result := make([]*entity.Artist, 0, len(artists))
	for _, a := range artists {
		if now, ok := byID[a.ID]; ok && now.IsOffered() {
			result = append(result, now)
		}
	}
	return result, nil
}

// persistArtists registers the artists as registerArtists does and returns the
// registered artists in the original input order, each once.
func (uc *artistUseCase) persistArtists(ctx context.Context, artists []*entity.Artist, decide func(context.Context, []*entity.Artist)) ([]*entity.Artist, error) {
	byMBID, err := uc.registerArtists(ctx, artists, decide)
	if err != nil {
		return nil, err
	}
	return listedOnce(artists, func(a *entity.Artist) *entity.Artist {
		return registeredAs(byMBID, a)
	}), nil
}

// registerArtists looks up existing artists by MBID (an artist's MBID or its
// listening MBID), creates only missing ones, and returns the registered
// artists indexed by their MBIDs. When decide is not nil it is given the
// missing artists to settle their MBIDs before they are created. All input
// artists must have a non-empty listening MBID, the MBID the listening
// service returned (caller responsibility).
func (uc *artistUseCase) registerArtists(ctx context.Context, artists []*entity.Artist, decide func(context.Context, []*entity.Artist)) (map[string]*entity.Artist, error) {
	if len(artists) == 0 {
		return map[string]*entity.Artist{}, nil
	}

	ctx, span := otel.Tracer("usecase/artist").Start(ctx, "PersistArtists")
	defer span.End()
	span.SetAttributes(attribute.Int("persist.input_count", len(artists)))

	// Step 1: Collect MBIDs.
	// A match is keyed by its listening MBID, the MBID the listening
	// service returned, which deciding its MBID does not change.
	mbids := make([]string, len(artists))
	for i, a := range artists {
		mbids[i] = a.ListeningMBID
	}

	// Step 2: Read existing.
	existing, err := uc.artistRepo.ListByMBIDs(ctx, mbids)
	if err != nil {
		return nil, err
	}

	existingSet := make(map[string]*entity.Artist, len(existing))
	indexByMBIDs(existingSet, existing)

	// Step 3: Determine missing.
	var missing []*entity.Artist
	for _, a := range artists {
		if _, ok := existingSet[a.ListeningMBID]; !ok {
			missing = append(missing, a)
		}
	}

	// Step 4: Create missing.
	if len(missing) > 0 && decide != nil {
		decide(ctx, missing)
	}
	if len(missing) > 0 {
		created, err := uc.artistRepo.Create(ctx, missing...)
		if err != nil {
			return nil, err
		}
		// Artist.Create announces each artist it registered (ARTIST.created).
		indexByMBIDs(existingSet, created)
	}

	span.SetAttributes(attribute.Int("persist.created_count", len(missing)))

	return existingSet, nil
}

// registeredAs returns the registered artist a match stands for in index, or
// nil. A match whose MBID was corrected to one another artist already holds is
// that artist: Create returned it, indexed under its own MBIDs.
func registeredAs(index map[string]*entity.Artist, match *entity.Artist) *entity.Artist {
	if a, ok := index[match.ListeningMBID]; ok {
		return a
	}
	return index[match.MBID]
}

// listedOnce returns the artist resolve gives for each result, in the results'
// order. A result it gives none for is left out, and an artist is listed once,
// at its first position, even when several results resolve to it.
func listedOnce(results []*entity.Artist, resolve func(*entity.Artist) *entity.Artist) []*entity.Artist {
	artists := make([]*entity.Artist, 0, len(results))
	listed := make(map[string]bool, len(results))
	for _, r := range results {
		a := resolve(r)
		if a != nil && !listed[a.ID] {
			listed[a.ID] = true
			artists = append(artists, a)
		}
	}
	return artists
}

// indexByMBIDs adds each artist to index under its MBID and its listening
// MBID, so a match under either finds it.
func indexByMBIDs(index map[string]*entity.Artist, artists []*entity.Artist) {
	for _, a := range artists {
		index[a.MBID] = a
		index[a.ListeningMBID] = a
	}
}

// hashString creates a simple hash of a string for cache key consistency.
func hashString(s string) string {
	h := sha256.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// Get implements [ArtistUseCase].
func (uc *artistUseCase) Get(ctx context.Context, id string) (*entity.Artist, error) {
	return uc.artistRepo.Get(ctx, id)
}

// ListByOrganizer implements [ArtistUseCase].
func (uc *artistUseCase) ListByOrganizer(ctx context.Context, organizerID string) ([]*entity.Artist, error) {
	if _, err := uc.organizerRepo.Get(ctx, organizerID); err != nil {
		return nil, err
	}
	return uc.organizerRepo.ListArtists(ctx, organizerID)
}

// artistRefreshOutcome is what one Refresh changed.
type artistRefreshOutcome struct {
	renamed bool
	ended   bool
}

// CorrectMBID implements [ArtistUseCase].
func (uc *artistUseCase) CorrectMBID(ctx context.Context, artistID, mbid string) (*entity.Artist, error) {
	if err := entity.ValidateMBID(mbid); err != nil {
		return nil, err
	}
	artist, err := uc.artistRepo.Get(ctx, artistID)
	if err != nil {
		return nil, err
	}
	// Look the MBID up before changing anything, so a mistyped MBID stores
	// nothing. The catalog answers a merged MBID with the surviving one; that
	// canonical MBID is what a later lookup returns, so it is the one stored.
	catalog, err := uc.idManager.GetArtist(ctx, mbid)
	if err != nil {
		return nil, err
	}
	if catalog.MBID == "" {
		return nil, apperr.New(codes.Internal, "the catalog returned no MBID", slog.String("mbid", mbid))
	}
	canonical := catalog.MBID
	name := catalog.Name
	if name == "" {
		name = artist.Name
	}
	if err := uc.artistRepo.UpdateMBID(ctx, artistID, canonical, name, catalog.ActivityEnded, catalog.KnownNames); err != nil {
		return nil, err
	}
	uc.logger.Info(ctx, "artist mbid corrected",
		slog.String("artist_id", artistID),
		slog.String("old_mbid", artist.MBID),
		slog.String("new_mbid", canonical),
	)
	return uc.artistRepo.Get(ctx, artistID)
}

// Refresh implements [ArtistUseCase].
func (uc *artistUseCase) Refresh(ctx context.Context, artistID, mbid, currentName string) error {
	_, err := uc.refresh(ctx, artistID, mbid, currentName)
	return err
}

// refresh reads the artist from the catalog and stores its name, known names
// and activity status, reporting what it found. Every write names the MBID it
// looked up, so it changes nothing when the MBID was replaced in the meantime.
func (uc *artistUseCase) refresh(ctx context.Context, artistID, mbid, currentName string) (artistRefreshOutcome, error) {
	var outcome artistRefreshOutcome

	catalog, err := uc.idManager.GetArtist(ctx, mbid)
	switch {
	case errors.Is(err, apperr.ErrNotFound):
		uc.logger.Warn(ctx, "catalog has no artist for the MBID; leaving the artist unchanged", analyticslog.Attrs(analyticslog.TopicCatalogSync,
			slog.String("artist_id", artistID),
			slog.String("mbid", mbid),
		)...)
		return outcome, nil
	case err != nil:
		return outcome, fmt.Errorf("get artist %s from the catalog: %w", artistID, err)
	}

	name := currentName
	switch {
	case catalog.Name == "":
		uc.logger.Warn(ctx, "catalog returned an empty name; keeping the current name",
			slog.String("artist_id", artistID),
			slog.String("mbid", mbid),
		)
	case catalog.Name != currentName:
		stored, err := uc.artistRepo.UpdateName(ctx, artistID, mbid, catalog.Name)
		if err != nil {
			return outcome, fmt.Errorf("update name of artist %s: %w", artistID, err)
		}
		name = catalog.Name
		// Not stored when the MBID was replaced since it was looked up.
		if stored {
			outcome.renamed = true
			uc.logger.Info(ctx, "artist name updated to canonical", analyticslog.Attrs(analyticslog.TopicCatalogSync,
				slog.String("artist_id", artistID),
				slog.String("old_name", currentName),
				slog.String("new_name", catalog.Name),
			)...)
		}
	}

	// Aliases change on their own, so the known names are stored also when the
	// name is unchanged.
	knownNames := entity.NewKnownNames(append([]string{name}, catalog.KnownNames...)...)
	if err := uc.artistRepo.UpdateKnownNames(ctx, artistID, mbid, knownNames); err != nil {
		return outcome, fmt.Errorf("update known names of artist %s: %w", artistID, err)
	}

	if err := uc.artistRepo.UpdateActivityEnded(ctx, artistID, mbid, catalog.ActivityEnded); err != nil {
		return outcome, fmt.Errorf("update activity status of artist %s: %w", artistID, err)
	}
	outcome.ended = catalog.ActivityEnded
	return outcome, nil
}

// verifyUnverified verifies, one at a time and oldest first, every Unverified
// artist, recording the outcome in summary. It stops after
// entity.ArtistRefreshMaxConsecutiveFailures failures in a row and when ctx is
// done; a failed artist stays Unverified for the next run.
func (uc *artistUseCase) verifyUnverified(ctx context.Context, summary *ArtistRefreshSummary) error {
	artists, err := uc.artistRepo.ListByMBIDStatus(ctx, entity.MBIDStatusUnverified)
	if err != nil {
		return fmt.Errorf("list unverified artists: %w", err)
	}
	summary.Unverified = len(artists)

	var consecutiveFailures int
	for _, artist := range artists {
		if ctx.Err() != nil {
			return nil
		}
		summary.VerifyAttempted++
		status, err := uc.verifyMBID(ctx, artist.ID)
		if err != nil {
			summary.VerifyFailed++
			consecutiveFailures++
			uc.logger.Error(ctx, "failed to verify artist MBID", err,
				slog.String("artist_id", artist.ID),
				slog.String("artist_name", artist.Name),
			)
			if consecutiveFailures >= entity.ArtistRefreshMaxConsecutiveFailures {
				summary.VerifyCircuitBroken = true
				uc.logger.Error(ctx, "circuit breaker activated: stopping MBID verification after consecutive failures", nil,
					slog.Int("consecutive_failures", consecutiveFailures),
				)
				return nil
			}
			continue
		}
		consecutiveFailures = 0
		switch status {
		case entity.MBIDStatusVerified:
			summary.Verified++
		case entity.MBIDStatusAmbiguous:
			summary.Ambiguous++
		}
	}
	return nil
}

// artistRefreshSlot returns the rotation slot of the day of now in Japan time:
// the whole days from 1970-01-01 to that day, modulo entity.ArtistRefreshSlots.
func artistRefreshSlot(now time.Time) int {
	japan, err := entity.LoadZone("Asia/Tokyo")
	if err != nil {
		panic(err) // the zone is embedded, so this cannot happen
	}
	days := entity.LocalDateIn(now, japan).Unix() / (24 * 60 * 60)
	return int(days % entity.ArtistRefreshSlots)
}

// RefreshDue implements [ArtistUseCase].
func (uc *artistUseCase) RefreshDue(ctx context.Context) (ArtistRefreshSummary, error) {
	summary := ArtistRefreshSummary{Slot: artistRefreshSlot(uc.clock())}

	if err := uc.verifyUnverified(ctx, &summary); err != nil {
		return summary, err
	}

	artists, err := uc.artistRepo.ListRefreshDue(ctx, entity.ArtistRefreshSlots, summary.Slot)
	if err != nil {
		return summary, fmt.Errorf("list artists of refresh slot %d: %w", summary.Slot, err)
	}
	summary.Listed = len(artists)

	var consecutiveFailures int
	for _, artist := range artists {
		if ctx.Err() != nil {
			break
		}

		summary.Attempted++
		outcome, err := uc.refresh(ctx, artist.ID, artist.MBID, artist.Name)
		if err != nil {
			summary.Failed++
			consecutiveFailures++
			uc.logger.Error(ctx, "failed to refresh artist", err,
				slog.String("artist_id", artist.ID),
				slog.String("artist_name", artist.Name),
			)
			if consecutiveFailures >= entity.ArtistRefreshMaxConsecutiveFailures {
				summary.CircuitBroken = true
				uc.logger.Error(ctx, "circuit breaker activated: stopping after consecutive failures", nil,
					slog.Int("consecutive_failures", consecutiveFailures),
				)
				break
			}
			continue
		}
		consecutiveFailures = 0
		if outcome.renamed {
			summary.Renamed++
		}
		if outcome.ended {
			summary.Ended++
		}
	}

	return summary, nil
}
