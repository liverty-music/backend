package rdb_test

import (
	"context"
	"testing"
	"time"

	"uuid"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Story test for stories/review-concert-candidates. Concerts are found by
// CreateFromDiscovered (the batch the daily search hands over; the search
// itself is not run) over the real repositories, with places it knows
// standing in for Google Places, and reviewed through the
// ConcertCandidateUseCase an admin calls.

// reviewStory runs discovery and review over one test database.
type reviewStory struct {
	t        *testing.T
	ctx      context.Context
	db       *rdb.Database
	places   knownPlaces
	creation usecase.ConcertCreationUseCase
	review   usecase.ConcertCandidateUseCase
}

func newReviewStory(t *testing.T, db *rdb.Database) *reviewStory {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	concertRepo := rdb.NewConcertRepository(db, testOutbox)
	venueRepo := rdb.NewVenueRepository(db)
	seriesRepo := rdb.NewSeriesRepository(db, testOutbox)
	candidateRepo := rdb.NewConcertCandidateRepository(db)
	s := &reviewStory{t: t, ctx: context.Background(), db: db, places: knownPlaces{}}
	s.creation = usecase.NewConcertCreationUseCase(candidateRepo, venueRepo, concertRepo, seriesRepo, s.places, nil, time.Now, logger)
	concerts := usecase.NewConcertUseCase(rdb.NewArtistRepository(db, testOutbox), concertRepo, venueRepo, seriesRepo, nil,
		nil, candidateRepo, rdb.NewRejectedConcertRepository(db), nil, rdb.NewAreaRepository(db),
		nil, nil, nil, 0, 0, time.Now, logger)
	s.review = usecase.NewConcertCandidateUseCase(concerts, nil)
	return s
}

// place makes the listed venue names resolve to one new place.
func (s *reviewStory) place(names ...string) {
	p := &entity.VenuePlace{ProviderUserID: "ChIJ-" + uuid.NewV7().String(), Coordinates: &entity.Coordinates{Latitude: 34.67, Longitude: 135.47}}
	for _, n := range names {
		named := *p
		named.Name = n
		s.places[n] = &named
	}
}

// discover runs CreateFromDiscovered for one show of the artist.
func (s *reviewStory) discover(artistID, title, listed string, date time.Time, start *entity.TimeOfDay) {
	s.t.Helper()
	require.NoError(s.t, s.creation.CreateFromDiscovered(s.ctx, entity.ConcertDiscoveredData{ArtistID: artistID, Series: []*entity.DiscoveredSeries{{
		Title: title, Type: entity.SeriesTypeSingle, SourceURL: "https://example.com/" + uuid.NewV7().String(),
		Events: []*entity.DiscoveredEvent{{ListedVenueName: listed, AdminArea: new("JP-27"), TimeZone: new("Asia/Tokyo"), LocalDate: date, StartTime: start}},
	}}}))
}

// queue returns the artist's ConcertCandidates in the review queue.
func (s *reviewStory) queue(artistID string) []*entity.ConcertCandidate {
	s.t.Helper()
	reviews, err := s.review.List(s.ctx)
	require.NoError(s.t, err)
	var out []*entity.ConcertCandidate
	for _, r := range reviews {
		if r.ConcertCandidate.ArtistID == artistID {
			out = append(out, r.ConcertCandidate)
		}
	}
	return out
}

// catalog returns the artist's Concerts in the catalog.
func (s *reviewStory) catalog(artistID string) []*entity.Concert {
	s.t.Helper()
	concerts, err := rdb.NewConcertRepository(s.db, testOutbox).ListCatalogByArtist(s.ctx, artistID)
	require.NoError(s.t, err)
	return concerts
}

func (s *reviewStory) approve(id string, resolution usecase.ApproveResolution) *usecase.ApproveResult {
	s.t.Helper()
	result, err := s.review.Approve(s.ctx, id, resolution, nil, nil, "admin-1")
	require.NoError(s.t, err)
	return result
}

func TestStory_ReviewConcertCandidates(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	date := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 40)
	at17, at18 := &entity.TimeOfDay{Hour: 17}, &entity.TimeOfDay{Hour: 18}

	// @spec stories/review-concert-candidates "Approve publishes"
	t.Run("Approve publishes", func(t *testing.T) {
		s := newReviewStory(t, db)
		artist := seedArtist(t, db, "Unplaced Band", uuid.NewV4().String())
		// No place is known for the name, so the concert is staged.
		s.discover(artist, "Hidden Live", "Unknown Hall "+uuid.NewV7().String()[30:], date, at18)
		queued := s.queue(artist)
		require.Len(t, queued, 1)

		result := s.approve(queued[0].ID, usecase.ApproveResolutionUnspecified)

		assert.Nil(t, result.Conflict)
		assert.Empty(t, s.queue(artist), "it leaves the queue")
		concerts := s.catalog(artist)
		require.Len(t, concerts, 1)
		assert.Equal(t, queued[0].SeriesID, concerts[0].SeriesID, "a Concert in the catalog under its Series")
	})

	// @spec stories/review-concert-candidates "Duplicate reconciled"
	t.Run("Duplicate reconciled", func(t *testing.T) {
		s := newReviewStory(t, db)
		artist := seedArtist(t, db, "Dome Band", uuid.NewV4().String())
		s.place("京セラドーム大阪", "Kyocera Dome Osaka")
		s.discover(artist, "Dome Live", "京セラドーム大阪", date, at17)
		s.discover(artist, "Dome Live", "Kyocera Dome Osaka", date, at17)
		queued := s.queue(artist)
		require.Len(t, queued, 1, "the same show under another name is staged")

		conflict := s.approve(queued[0].ID, usecase.ApproveResolutionUnspecified).Conflict
		require.NotNil(t, conflict, "the admin sees the conflict")
		result := s.approve(queued[0].ID, usecase.ApproveResolutionKeepExisting)

		assert.Nil(t, result.Conflict)
		assert.Empty(t, s.queue(artist), "it leaves the queue")
		concerts := s.catalog(artist)
		require.Len(t, concerts, 1)
		require.NotNil(t, concerts[0].ListedVenueName)
		assert.Equal(t, "京セラドーム大阪", *concerts[0].ListedVenueName, "the existing Concert keeps its venue name")
		assert.Equal(t, at17, concerts[0].StartTime, "and its times")
		var rejected int
		require.NoError(t, db.Pool.QueryRow(s.ctx, `SELECT count(*) FROM rejected_concerts WHERE artist_id = $1 AND reason LIKE 'duplicate of existing event %'`, artist).Scan(&rejected))
		assert.Equal(t, 1, rejected, "a RejectedConcert entry records it")

		s.discover(artist, "Dome Live", "Kyocera Dome Osaka", date, at17)
		assert.Empty(t, s.queue(artist), "the next discovery under the candidate's venue name stages nothing")
		assert.Len(t, s.catalog(artist), 1)
	})

	// @spec stories/review-concert-candidates "Shared bill reconciled"
	t.Run("Shared bill reconciled", func(t *testing.T) {
		s := newReviewStory(t, db)
		horumon := seedArtist(t, db, "マキシマム ザ ホルモン", uuid.NewV4().String())
		sim := seedArtist(t, db, "SiM", uuid.NewV4().String())
		s.place("Zepp Osaka Bayside", "ゼップ大阪ベイサイド")
		s.discover(horumon, "Split Tour", "Zepp Osaka Bayside", date, at18)
		s.discover(sim, "Split Tour", "ゼップ大阪ベイサイド", date, at18)
		queued := s.queue(sim)
		require.Len(t, queued, 1, "the other Artist's concert under another name is staged")
		mark := outboxMark(t, db)

		require.NotNil(t, s.approve(queued[0].ID, usecase.ApproveResolutionUnspecified).Conflict)
		result := s.approve(queued[0].ID, usecase.ApproveResolutionKeepExisting)

		assert.Nil(t, result.Conflict)
		assert.Empty(t, s.queue(sim), "it leaves the queue")
		concerts := s.catalog(horumon)
		require.Len(t, concerts, 1)
		assert.ElementsMatch(t, []string{horumon, sim}, concerts[0].ArtistIDs(), "the existing Concert lists both Artists")
		var toldSiM int
		for _, msg := range announcedMessages(t, db, mark, entity.SubjectConcertCreated) {
			if msg.Metadata.Get("ce_subject") == sim {
				toldSiM++
			}
		}
		assert.Equal(t, 1, toldSiM, "the candidate's Artist's followers are told about it")
	})
}
