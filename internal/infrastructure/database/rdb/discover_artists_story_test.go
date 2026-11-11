package rdb_test

import (
	"context"
	"testing"
	"time"

	"uuid"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/cache"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// Story tests for stories/discover-artists. They run the real ArtistUseCase,
// FollowUseCase (with ArtistUseCase.Refresh) and ArtistImageSyncUseCase over
// the real repositories and local Postgres, hand each recorded ARTIST.created
// announcement to the consumers the event consumer runs, and run the daily
// image run as cmd/job/artist-image-sync does. The artist catalog, the image
// catalog, MusicBrainz and the official site lookup are faked.

// Daily image run settings, as in cmd/job/artist-image-sync.
const (
	imageRunAge   = 7 * 24 * time.Hour
	imageRunLimit = 500
)

// storyImageCatalog returns one thumbnail for every artist and counts the
// lookups per MBID.
type storyImageCatalog struct {
	lookups map[string]int
}

func (c *storyImageCatalog) ResolveImages(_ context.Context, mbid string) (*entity.Fanart, error) {
	c.lookups[mbid]++
	return &entity.Fanart{ArtistThumb: []entity.FanartImage{
		{ID: mbid, URL: "https://images.example/" + mbid + ".jpg", Likes: 1, Lang: "en"},
	}}, nil
}

// storyArtistCatalog returns the artists a search or a chart lists.
type storyArtistCatalog struct {
	results []*entity.Artist
}

func (c *storyArtistCatalog) Search(context.Context, string) ([]*entity.Artist, error) {
	return c.results, nil
}

func (c *storyArtistCatalog) ListSimilar(context.Context, *entity.Artist, int32) ([]*entity.Artist, error) {
	return c.results, nil
}

func (c *storyArtistCatalog) ListTop(context.Context, string, string, int32) ([]*entity.Artist, error) {
	return c.results, nil
}

// storyMusicBrainz knows every artist under the name it was registered with,
// and marks the activity of the MBIDs in ended as ended.
type storyMusicBrainz struct {
	names map[string]string
	ended map[string]bool
}

func (m *storyMusicBrainz) GetArtist(_ context.Context, mbid string) (*entity.Artist, error) {
	return &entity.Artist{Name: m.names[mbid], MBID: mbid, ActivityEnded: m.ended[mbid]}, nil
}

// storyNoOfficialSite finds no official site for any artist.
type storyNoOfficialSite struct{}

func (storyNoOfficialSite) ResolveOfficialSiteURL(context.Context, string) (string, error) {
	return "", nil
}

type storyFollowMetrics struct{}

func (storyFollowMetrics) RecordFollow(context.Context, string) {}

type discoverStory struct {
	// db is the database of the test.
	db         *rdb.Database
	t          *testing.T
	ctx        context.Context
	artistRepo *rdb.ArtistRepository
	artists    usecase.ArtistUseCase
	follows    usecase.FollowUseCase
	imageSync  usecase.ArtistImageSyncUseCase
	logger     *logging.Logger
	found      *storyArtistCatalog
	catalog    *storyImageCatalog
	mb         *storyMusicBrainz
}

func newDiscoverStory(t *testing.T, db *rdb.Database) *discoverStory {
	t.Helper()
	cleanTables(t, db)
	logger, err := logging.New()
	require.NoError(t, err)

	s := &discoverStory{
		db:         db,
		t:          t,
		ctx:        context.Background(),
		artistRepo: rdb.NewArtistRepository(db, testOutbox),
		logger:     logger,
		found:      &storyArtistCatalog{},
		catalog:    &storyImageCatalog{lookups: map[string]int{}},
		mb:         &storyMusicBrainz{names: map[string]string{}, ended: map[string]bool{}},
	}
	discoveries := cache.NewMemoryCache(usecase.ArtistCacheTTL)
	t.Cleanup(func() { _ = discoveries.Close() })
	s.artists = usecase.NewArtistUseCase(s.artistRepo, nil, s.found, s.mb, discoveries, time.Now, logger)
	s.follows = usecase.NewFollowUseCase(rdb.NewFollowRepository(db, testOutbox), s.artistRepo, storyNoOfficialSite{},
		storyFollowMetrics{}, logger)
	s.imageSync = usecase.NewArtistImageSyncUseCase(s.artistRepo, s.catalog, time.Now, logger)
	return s
}

// createdSince returns the ARTIST.created announcements of the MBID recorded
// after mark.
func (s *discoverStory) createdSince(mark int64, mbid string) []*entityv1.Artist {
	s.t.Helper()
	return announcedSince(s.t, s.db, mark, entity.SubjectArtistCreated, func() *entityv1.Artist { return &entityv1.Artist{} },
		func(a *entityv1.Artist) bool { return a.GetMbid().GetValue() == mbid })
}

// handleCreated hands an ARTIST.created announcement to the refresh and image
// consumers, as the event consumer does.
func (s *discoverStory) handleCreated(announced *entityv1.Artist) {
	s.t.Helper()
	payload, err := proto.Marshal(announced)
	require.NoError(s.t, err)
	require.NoError(s.t, event.NewArtistRefreshConsumer(s.artists, s.logger).Handle(message.NewMessage(uuid.NewV7().String(), payload)))
	require.NoError(s.t, event.NewArtistImageConsumer(s.imageSync, s.logger).Handle(message.NewMessage(uuid.NewV7().String(), payload)))
}

// register registers an artist through ArtistUseCase.Create.
func (s *discoverStory) register(name string) *entity.Artist {
	s.t.Helper()
	mbid := uuid.NewV4().String()
	s.mb.names[mbid] = name
	artist, err := s.artists.Create(s.ctx, entity.NewArtist(name, mbid))
	require.NoError(s.t, err)
	return artist
}

// follow makes a new fan follow the artist.
func (s *discoverStory) follow(artistID string) {
	s.t.Helper()
	ext := uuid.NewV4().String()
	userID := seedUser(s.t, s.db, "Fan", ext+"@example.com", ext)
	require.NoError(s.t, s.follows.Follow(s.ctx, userID, artistID))
}

// runRefreshDays runs the daily artist refresh on the given number of
// consecutive days from start, as cmd/job/artist-refresh does.
func (s *discoverStory) runRefreshDays(start time.Time, days int) {
	s.t.Helper()
	day := start
	refresher := usecase.NewArtistUseCase(s.artistRepo, nil, nil, s.mb, nil, func() time.Time { return day }, s.logger)
	for range days {
		summary, err := refresher.RefreshDue(s.ctx)
		require.NoError(s.t, err)
		require.False(s.t, summary.CircuitBroken)
		day = day.AddDate(0, 0, 1)
	}
}

// requireActivityEnded asserts the stored activity status of the artist.
func (s *discoverStory) requireActivityEnded(artistID string, want bool) {
	s.t.Helper()
	got, err := s.artistRepo.Get(s.ctx, artistID)
	require.NoError(s.t, err)
	require.Equal(s.t, want, got.ActivityEnded)
}

// artistIDs returns the ids of the artists, in order.
func artistIDs(artists []*entity.Artist) []string {
	ids := make([]string, len(artists))
	for i, a := range artists {
		ids[i] = a.ID
	}
	return ids
}

// checkedAt sets the time of the artist's last image check, keeping its images.
func (s *discoverStory) checkedAt(artistID string, at time.Time) {
	s.t.Helper()
	artist, err := s.artistRepo.Get(s.ctx, artistID)
	require.NoError(s.t, err)
	require.NoError(s.t, s.artistRepo.UpdateFanart(s.ctx, artistID, artist.Fanart, at))
}

// runDaily runs the daily image run's steps as cmd/job/artist-image-sync does.
func (s *discoverStory) runDaily() {
	s.t.Helper()
	due, err := s.artistRepo.ListStaleOrMissingFanart(s.ctx, imageRunAge, imageRunLimit)
	require.NoError(s.t, err)
	for _, artist := range due {
		require.NoError(s.t, s.imageSync.SyncArtistImage(s.ctx, artist.ID, artist.MBID))
	}
}

// requireCheckedSince asserts that the artist's images were recorded at or
// after since.
func (s *discoverStory) requireCheckedSince(artistID string, since time.Time) {
	s.t.Helper()
	got, err := s.artistRepo.Get(s.ctx, artistID)
	require.NoError(s.t, err)
	require.NotNil(s.t, got.FanartSyncTime)
	assert.False(s.t, got.FanartSyncTime.Before(since.Truncate(time.Microsecond)), "last check %s is before %s", got.FanartSyncTime, since)
	require.NotNil(s.t, got.Fanart)
	assert.Len(s.t, got.Fanart.ArtistThumb, 1)
}

func daysBefore(n int) time.Time {
	return time.Now().Add(-time.Duration(n) * 24 * time.Hour)
}

func TestStory_DiscoverArtists_RegisteredAndEnriched(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	t.Run("a newly found artist is registered and gets its canonical name and images", func(t *testing.T) {
		// @spec stories/discover-artists "Newly discovered artist"
		s := newDiscoverStory(t, db)
		mbid := uuid.NewV4().String()
		s.mb.names[mbid] = "ヨルシカ"
		s.found.results = []*entity.Artist{{Name: "yorushika", MBID: mbid}}
		mark := outboxMark(t, db)

		got, err := s.artists.Search(s.ctx, "yorushika")

		require.NoError(t, err)
		require.Len(t, got, 1)
		registered, err := s.artistRepo.Get(s.ctx, got[0].ID)
		require.NoError(t, err, "the result is a registered artist")
		assert.Equal(t, mbid, registered.MBID)
		announced := s.createdSince(mark, mbid)
		require.Len(t, announced, 1, "the new artist is announced as created")
		assert.Equal(t, got[0].ID, announced[0].GetId().GetValue())

		s.handleCreated(announced[0])

		enriched, err := s.artistRepo.Get(s.ctx, got[0].ID)
		require.NoError(t, err)
		assert.Equal(t, "ヨルシカ", enriched.Name)
		assert.False(t, enriched.ActivityEnded, "the catalog records no end for the artist")
		require.NotNil(t, enriched.FanartSyncTime)
		require.NotNil(t, enriched.Fanart)
		assert.Len(t, enriched.Fanart.ArtistThumb, 1)
	})

	t.Run("a chart listing a registered artist returns it and announces nothing", func(t *testing.T) {
		// @spec stories/discover-artists "Already registered artist"
		s := newDiscoverStory(t, db)
		artist := s.register("Known Band")
		s.found.results = []*entity.Artist{{Name: "Known Band", MBID: artist.MBID}}
		mark := outboxMark(t, db)

		got, err := s.artists.ListTop(s.ctx, "JP", "", 10)

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, artist.ID, got[0].ID)
		assert.Empty(t, s.createdSince(mark, artist.MBID))
	})
}

func TestStory_DiscoverArtists_ImagesKeptCurrent(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	t.Run("a followed artist's stale images are checked again", func(t *testing.T) {
		// @spec stories/discover-artists "Stale images"
		s := newDiscoverStory(t, db)
		artist := s.register("Stale Band")
		s.follow(artist.ID)
		s.runDaily()
		s.checkedAt(artist.ID, daysBefore(8))
		before := s.catalog.lookups[artist.MBID]

		start := time.Now()
		s.runDaily()

		assert.Equal(t, before+1, s.catalog.lookups[artist.MBID])
		s.requireCheckedSince(artist.ID, start)
	})

	t.Run("an artist nobody follows keeps the images of its last check", func(t *testing.T) {
		// @spec stories/discover-artists "Artist nobody follows"
		s := newDiscoverStory(t, db)
		artist := s.register("Searched Band")
		s.runDaily()
		require.Equal(t, 1, s.catalog.lookups[artist.MBID], "the first check happens whoever registered the artist")
		lastCheck := daysBefore(8)
		s.checkedAt(artist.ID, lastCheck)

		s.runDaily()

		assert.Equal(t, 1, s.catalog.lookups[artist.MBID])
		got, err := s.artistRepo.Get(s.ctx, artist.ID)
		require.NoError(t, err)
		require.NotNil(t, got.FanartSyncTime)
		assert.True(t, got.FanartSyncTime.Equal(lastCheck.Truncate(time.Microsecond)))
		require.NotNil(t, got.Fanart)
		assert.Len(t, got.Fanart.ArtistThumb, 1)
	})

	t.Run("an artist followed later is checked again by the next run", func(t *testing.T) {
		// @spec stories/discover-artists "Artist followed later"
		s := newDiscoverStory(t, db)
		artist := s.register("Later Band")
		s.runDaily()
		s.checkedAt(artist.ID, daysBefore(30))
		s.runDaily()
		require.Equal(t, 1, s.catalog.lookups[artist.MBID], "not checked again while nobody follows it")

		s.follow(artist.ID)
		start := time.Now()
		s.runDaily()

		assert.Equal(t, 2, s.catalog.lookups[artist.MBID])
		s.requireCheckedSince(artist.ID, start)
	})

	t.Run("an artist registered directly is first checked by the next run", func(t *testing.T) {
		// @spec stories/discover-artists "Artist registered directly"
		s := newDiscoverStory(t, db)
		artist := s.register("Direct Band")
		got, err := s.artistRepo.Get(s.ctx, artist.ID)
		require.NoError(t, err)
		require.Nil(t, got.FanartSyncTime, "registration makes no image check")

		start := time.Now()
		s.runDaily()

		assert.Equal(t, 1, s.catalog.lookups[artist.MBID])
		s.requireCheckedSince(artist.ID, start)
	})
}

func TestStory_DiscoverArtists_EndedArtistsNotSuggested(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	// dissolve marks the artist as dissolved in the catalog and refreshes it,
	// as the announcement or the daily run does.
	dissolve := func(s *discoverStory, artist *entity.Artist) {
		s.t.Helper()
		s.mb.ended[artist.MBID] = true
		require.NoError(s.t, s.artists.Refresh(s.ctx, artist.ID, artist.MBID, artist.Name))
		s.requireActivityEnded(artist.ID, true)
	}

	t.Run("the chart leaves out a dissolved group", func(t *testing.T) {
		// @spec stories/discover-artists "Dissolved group in the chart"
		s := newDiscoverStory(t, db)
		active := s.register("Active Band")
		dissolved := s.register("SMAP")
		dissolve(s, dissolved)
		s.found.results = []*entity.Artist{
			{Name: "SMAP", MBID: dissolved.MBID},
			{Name: "Active Band", MBID: active.MBID},
		}

		got, err := s.artists.ListTop(s.ctx, "JP", "", 50)

		require.NoError(t, err)
		assert.Equal(t, []string{active.ID}, artistIDs(got))
	})

	t.Run("a name search still finds a dissolved group", func(t *testing.T) {
		// @spec stories/discover-artists "Name search for a dissolved group"
		s := newDiscoverStory(t, db)
		dissolved := s.register("SMAP")
		dissolve(s, dissolved)
		s.found.results = []*entity.Artist{{Name: "SMAP", MBID: dissolved.MBID}}

		got, err := s.artists.Search(s.ctx, "SMAP")

		require.NoError(t, err)
		assert.Equal(t, []string{dissolved.ID}, artistIDs(got))
	})

	t.Run("a group nobody follows is left out within 30 days of dissolving", func(t *testing.T) {
		// @spec stories/discover-artists "Group dissolves after it was registered"
		s := newDiscoverStory(t, db)
		seed := s.register("Seed Band")
		active := s.register("Active Band")
		group := s.register("BiSH")
		s.mb.ended[group.MBID] = true
		s.requireActivityEnded(group.ID, false)

		s.runRefreshDays(time.Now(), 30)

		s.requireActivityEnded(group.ID, true)
		s.found.results = []*entity.Artist{
			{Name: "BiSH", MBID: group.MBID},
			{Name: "Active Band", MBID: active.MBID},
		}
		top, err := s.artists.ListTop(s.ctx, "JP", "", 50)
		require.NoError(t, err)
		assert.Equal(t, []string{active.ID}, artistIDs(top))
		similar, err := s.artists.ListSimilar(s.ctx, seed.ID, 0)
		require.NoError(t, err)
		assert.Equal(t, []string{active.ID}, artistIDs(similar))
	})

	t.Run("an ended artist found for the first time is left out once refreshed", func(t *testing.T) {
		// @spec stories/discover-artists "Ended artist found for the first time"
		s := newDiscoverStory(t, db)
		seed := s.register("Seed Band")
		mbid := uuid.NewV4().String()
		s.mb.names[mbid] = "The Beatles"
		s.mb.ended[mbid] = true
		s.found.results = []*entity.Artist{{Name: "The Beatles", MBID: mbid}}
		mark := outboxMark(t, db)

		first, err := s.artists.ListSimilar(s.ctx, seed.ID, 0)

		require.NoError(t, err)
		require.Len(t, first, 1, "the call that registers the artist returns it")
		announced := s.createdSince(mark, mbid)
		require.Len(t, announced, 1)

		s.handleCreated(announced[0])

		s.requireActivityEnded(first[0].ID, true)
		second, err := s.artists.ListSimilar(s.ctx, seed.ID, 0)
		require.NoError(t, err)
		assert.Empty(t, second)
	})
}
