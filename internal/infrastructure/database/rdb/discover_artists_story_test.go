package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

// Story test for stories/discover-artists "Artist images are kept current".
// It runs the real ArtistUseCase, FollowUseCase and ArtistImageSyncUseCase
// over the real repositories and local Postgres, and runs the daily image run
// as cmd/job/artist-image-sync does. The image catalog, MusicBrainz and the
// official site lookup are faked; messaging is replaced by storyPublisher.

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

// storyMusicBrainz knows every artist under the name it was registered with.
type storyMusicBrainz struct {
	names map[string]string
}

func (m *storyMusicBrainz) GetArtist(_ context.Context, mbid string) (*entity.Artist, error) {
	return &entity.Artist{Name: m.names[mbid], MBID: mbid}, nil
}

// storyNoOfficialSite finds no official site for any artist.
type storyNoOfficialSite struct{}

func (storyNoOfficialSite) ResolveOfficialSiteURL(context.Context, string) (string, error) {
	return "", nil
}

type storyFollowMetrics struct{}

func (storyFollowMetrics) RecordFollow(context.Context, string) {}

type discoverStory struct {
	t          *testing.T
	ctx        context.Context
	artistRepo *rdb.ArtistRepository
	artists    usecase.ArtistUseCase
	follows    usecase.FollowUseCase
	imageSync  usecase.ArtistImageSyncUseCase
	catalog    *storyImageCatalog
	mb         *storyMusicBrainz
}

func newDiscoverStory(t *testing.T) *discoverStory {
	t.Helper()
	cleanDatabase(t)
	logger, err := logging.New()
	require.NoError(t, err)

	s := &discoverStory{
		t:          t,
		ctx:        context.Background(),
		artistRepo: rdb.NewArtistRepository(testDB, testOutbox),
		catalog:    &storyImageCatalog{lookups: map[string]int{}},
		mb:         &storyMusicBrainz{names: map[string]string{}},
	}
	s.artists = usecase.NewArtistUseCase(s.artistRepo, nil, nil, s.mb, &storyPublisher{}, nil, logger)
	s.follows = usecase.NewFollowUseCase(rdb.NewFollowRepository(testDB, testOutbox), s.artistRepo, storyNoOfficialSite{},
		storyFollowMetrics{}, logger)
	// The catalog returns no logo, so the logo is never downloaded.
	s.imageSync = usecase.NewArtistImageSyncUseCase(s.artistRepo, s.catalog, nil, logger)
	return s
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
	userID := seedUser(s.t, "Fan", ext+"@example.com", ext)
	require.NoError(s.t, s.follows.Follow(s.ctx, userID, artistID))
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

func TestStory_DiscoverArtists_ImagesKeptCurrent(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	t.Run("a followed artist's stale images are checked again", func(t *testing.T) {
		// @spec stories/discover-artists "Stale images"
		s := newDiscoverStory(t)
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
		s := newDiscoverStory(t)
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
		s := newDiscoverStory(t)
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
		s := newDiscoverStory(t)
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
