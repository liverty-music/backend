package linkpreview_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/adapter/linkpreview"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	eventID     = "019a0000-0000-7000-8000-0000000000e1"
	day2ID      = "019a0000-0000-7000-8000-0000000000e2"
	seriesID    = "019a0000-0000-7000-8000-0000000000a1"
	organizerID = "019a0000-0000-7000-8000-0000000000f1"
	mediaID     = "019a0000-0000-7000-8000-0000000000c1"
	siteBaseURL = "https://liverty-music.app"
	cdnBaseURL  = "https://cdn.example.com"
)

// jstTime returns an instant at the given Japan-time hour on 2026-11-20.
func jstTime(hour int) *time.Time {
	t := time.Date(2026, 11, 20, hour, 0, 0, 0, time.FixedZone("JST", 9*60*60))
	return &t
}

// concertOpts customise the fixture Concert.
type concertOpts struct {
	title string
	state entity.SeriesPublishState
	cover bool
	desc  string
}

func newConcert(id string, date time.Time, o concertOpts) *entity.Concert {
	if o.title == "" {
		o.title = "ONE MAN LIVE"
	}
	if o.state == "" {
		o.state = entity.SeriesPublishStatePublished
	}
	org := organizerID
	visibility := entity.SeriesVisibilityPublic
	state := o.state
	area := "JP-13"
	s := &entity.Series{
		ID: seriesID, Title: o.title, Type: entity.SeriesTypeSingle,
		OrganizerID: &org, Visibility: &visibility, PublishState: &state,
	}
	if o.desc != "" {
		d := o.desc
		s.Description = &d
	}
	if o.cover {
		s.CoverMedia = &entity.Media{ID: mediaID, OrganizerID: organizerID, Kind: entity.MediaKindImage}
	}
	return &entity.Concert{
		ID: id, SeriesID: seriesID, LocalDate: date,
		OpenTime: jstTime(18), StartTime: jstTime(19),
		Venue:      &entity.Venue{Name: "Shibuya WWW", AdminArea: &area},
		Series:     s,
		Performers: []*entity.Artist{{Name: "The Band"}},
	}
}

var (
	nov20 = time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	nov21 = time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
)

type fixture struct {
	uc      *mocks.MockConcertUseCase
	handler *linkpreview.Handler
	server  *httptest.Server
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	uc := mocks.NewMockConcertUseCase(t)
	h := linkpreview.NewHandler(uc, linkpreview.NewTagBuilder(siteBaseURL, mapper.NewMediaURLBuilder(cdnBaseURL)), logger)
	mux := http.NewServeMux()
	mux.Handle(linkpreview.Pattern, h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &fixture{uc: uc, handler: h, server: srv}
}

// get requests path and returns the status and body.
func (f *fixture) get(t *testing.T, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(f.server.URL + path)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

func metaContent(t *testing.T, body, key string) string {
	t.Helper()
	for _, attr := range []string{`property="` + key + `"`, `name="` + key + `"`} {
		_, after, ok := strings.Cut(body, attr)
		if !ok {
			continue
		}
		rest := after
		const open = ` content="`
		j := strings.Index(rest, open)
		require.GreaterOrEqual(t, j, 0)
		rest = rest[j+len(open):]
		return rest[:strings.Index(rest, `"`)]
	}
	t.Fatalf("meta %q not found in %q", key, body)
	return ""
}

func TestHandler_SharedOnX(t *testing.T) {
	// @spec components/infrastructure/fan/web/global/link-preview "Shared on X"
	t.Parallel()
	f := newFixture(t)
	day1 := newConcert(eventID, nov20, concertOpts{cover: true, desc: "Two nights at Shibuya WWW."})
	f.uc.EXPECT().Get(mock.Anything, eventID).Return(day1, nil).Once()
	f.uc.EXPECT().ListBySeries(mock.Anything, seriesID).Return([]*entity.Concert{
		day1, newConcert(day2ID, nov21, concertOpts{cover: true}),
	}, nil).Once()

	status, body := f.get(t, "/link-preview/events/"+eventID)

	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "ONE MAN LIVE | The Band", metaContent(t, body, "og:title"))
	assert.Equal(t,
		"2026年11月20日(金) 開場18:00 開演19:00 Shibuya WWW（東京都） 全2公演 11/20(金)・11/21(土) Two nights at Shibuya WWW.",
		metaContent(t, body, "og:description"))
	assert.Equal(t, cdnBaseURL+"/cdn/"+organizerID+"/"+mediaID+"/large.webp", metaContent(t, body, "og:image"))
	assert.Equal(t, siteBaseURL+"/events/"+eventID, metaContent(t, body, "og:url"))
	assert.Equal(t, "website", metaContent(t, body, "og:type"))
	assert.Equal(t, "Liverty Music", metaContent(t, body, "og:site_name"))
	assert.Equal(t, "ja_JP", metaContent(t, body, "og:locale"))
	assert.Equal(t, "en_US", metaContent(t, body, "og:locale:alternate"))
	assert.Equal(t, "summary_large_image", metaContent(t, body, "twitter:card"))
}

func TestHandler_SingleEventWithoutCover(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := newConcert(eventID, nov20, concertOpts{})
	f.uc.EXPECT().Get(mock.Anything, eventID).Return(c, nil).Once()
	f.uc.EXPECT().ListBySeries(mock.Anything, seriesID).Return([]*entity.Concert{c}, nil).Once()

	_, body := f.get(t, "/link-preview/events/"+eventID)

	assert.Equal(t, "2026年11月20日(金) 開場18:00 開演19:00 Shibuya WWW（東京都）", metaContent(t, body, "og:description"))
	assert.Equal(t, siteBaseURL+"/og-default.png", metaContent(t, body, "og:image"))
}

func TestHandler_LinkWithReferralCode(t *testing.T) {
	// @spec components/infrastructure/fan/web/global/link-preview "Link with a referral code"
	t.Parallel()
	f := newFixture(t)
	c := newConcert(eventID, nov20, concertOpts{})
	f.uc.EXPECT().Get(mock.Anything, eventID).Return(c, nil).Once()
	f.uc.EXPECT().ListBySeries(mock.Anything, seriesID).Return([]*entity.Concert{c}, nil).Once()

	_, body := f.get(t, "/link-preview/events/"+eventID+"?ref=member-a")

	assert.Equal(t, siteBaseURL+"/events/"+eventID, metaContent(t, body, "og:url"))
	assert.NotContains(t, body, "member-a")
}

func TestHandler_CancelledEvent(t *testing.T) {
	// @spec components/infrastructure/fan/web/global/link-preview "Cancelled event"
	t.Parallel()
	f := newFixture(t)
	c := newConcert(eventID, nov20, concertOpts{state: entity.SeriesPublishStateCancelled})
	f.uc.EXPECT().Get(mock.Anything, eventID).Return(c, nil).Once()
	f.uc.EXPECT().ListBySeries(mock.Anything, seriesID).Return([]*entity.Concert{c}, nil).Once()

	_, body := f.get(t, "/link-preview/events/"+eventID)

	assert.Equal(t, "【公演中止】ONE MAN LIVE | The Band", metaContent(t, body, "og:title"))
}

func TestHandler_SiteDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		err  error
	}{
		{
			// @spec components/infrastructure/fan/web/global/link-preview "Unlisted event link"
			name: "return 200 with an empty body when the event has no page",
			path: "/link-preview/events/" + eventID,
			err:  apperr.New(codes.NotFound, "event page not found"),
		},
		{
			// @spec components/infrastructure/fan/web/global/link-preview "Event read fails"
			name: "return 200 with an empty body when the event cannot be read",
			path: "/link-preview/events/" + eventID,
			err:  apperr.New(codes.Unavailable, "db down"),
		},
		{
			name: "return 200 with an empty body for an id that is not a UUID",
			path: "/link-preview/events/not-a-uuid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			if tt.err != nil {
				f.uc.EXPECT().Get(mock.Anything, eventID).Return(nil, tt.err).Once()
			}

			status, body := f.get(t, tt.path)

			assert.Equal(t, http.StatusOK, status)
			assert.Empty(t, body)
		})
	}
}

func TestHandler_SeriesCancelledWithinCacheTTL(t *testing.T) {
	// @spec components/infrastructure/fan/web/global/link-preview "Series cancelled"
	t.Parallel()
	f := newFixture(t)

	var (
		mu  sync.Mutex
		now = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	)
	f.handler.SetNow(func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	})
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}

	published := newConcert(eventID, nov20, concertOpts{})
	cancelled := newConcert(eventID, nov20, concertOpts{state: entity.SeriesPublishStateCancelled})
	f.uc.EXPECT().Get(mock.Anything, eventID).Return(published, nil).Once()
	f.uc.EXPECT().ListBySeries(mock.Anything, seriesID).Return([]*entity.Concert{published}, nil).Once()

	_, body := f.get(t, "/link-preview/events/"+eventID)
	assert.Equal(t, "ONE MAN LIVE | The Band", metaContent(t, body, "og:title"))

	// The organizer cancels; within the TTL the cached preview is served
	// without reading the usecase again.
	advance(30 * time.Second)
	_, body = f.get(t, "/link-preview/events/"+eventID)
	assert.Equal(t, "ONE MAN LIVE | The Band", metaContent(t, body, "og:title"))

	// 60 seconds after the first read the preview reflects the cancellation.
	advance(30 * time.Second)
	f.uc.EXPECT().Get(mock.Anything, eventID).Return(cancelled, nil).Once()
	f.uc.EXPECT().ListBySeries(mock.Anything, seriesID).Return([]*entity.Concert{cancelled}, nil).Once()
	_, body = f.get(t, "/link-preview/events/"+eventID)
	assert.True(t, strings.HasPrefix(metaContent(t, body, "og:title"), "【公演中止】"))
}

func TestHandler_EscapesOrganizerText(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := newConcert(eventID, nov20, concertOpts{
		title: `LIVE"><script>alert(1)</script>`,
		desc:  `<img src=x onerror=alert(1)> & more`,
	})
	f.uc.EXPECT().Get(mock.Anything, eventID).Return(c, nil).Once()
	f.uc.EXPECT().ListBySeries(mock.Anything, seriesID).Return([]*entity.Concert{c}, nil).Once()

	_, body := f.get(t, "/link-preview/events/"+eventID)

	assert.NotContains(t, body, "<script>")
	assert.NotContains(t, body, "<img")
	assert.Contains(t, body, "&lt;script&gt;")
	assert.Contains(t, body, "&amp; more")
	assert.Equal(t, 12, strings.Count(body, "<meta "), "no extra elements are injected")
}

func TestHandler_DescriptionIsTruncated(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := newConcert(eventID, nov20, concertOpts{desc: strings.Repeat("あ", 200)})
	f.uc.EXPECT().Get(mock.Anything, eventID).Return(c, nil).Once()
	f.uc.EXPECT().ListBySeries(mock.Anything, seriesID).Return([]*entity.Concert{c}, nil).Once()

	_, body := f.get(t, "/link-preview/events/"+eventID)

	desc := []rune(metaContent(t, body, "og:description"))
	assert.Len(t, desc, 100)
	assert.Equal(t, '…', desc[len(desc)-1])
}
