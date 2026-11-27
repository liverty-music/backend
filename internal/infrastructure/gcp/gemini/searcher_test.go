package gemini_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/liverty-music/backend/internal/testutil"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rewriteTransport struct {
	URL string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(t.URL)
	req.URL.Scheme = u.Scheme
	req.URL.Host = u.Host
	return http.DefaultTransport.RoundTrip(req)
}

// geminiResponse builds a mock Gemini API JSON response with the given body text and finish reason.
func geminiResponse(bodyText, finishReason string) string {
	if finishReason == "" {
		finishReason = "STOP"
	}
	return fmt.Sprintf(`{
		"candidates": [{
			"content": {"parts": [{"text": %s}]},
			"finishReason": %q,
			"groundingMetadata": {"webSearchQueries": ["test"]}
		}],
		"usageMetadata": {
			"promptTokenCount": 10,
			"candidatesTokenCount": 10,
			"totalTokenCount": 20
		}
	}`, strconv.Quote(bodyText), finishReason)
}

// testAreas returns the Area reference data of a test database of the
// test's own, so the searcher resolves admin areas as in production.
func testAreas(t *testing.T) entity.AreaRepository {
	t.Helper()
	clone := testutil.NewDatabase(t)
	logger, err := logging.New()
	require.NoError(t, err)
	db, err := rdb.New(context.Background(), config.DatabaseConfig{
		Host: "localhost", Port: clone.Port, Name: clone.Name, User: "test-user", SSLMode: "disable",
		Schema: "app", MaxOpenConns: 2, MaxIdleConns: 1, ConnMaxLifetime: 1800, MaxConnIdleTime: 600, HealthCheckPeriod: 60,
	}, true, logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return rdb.NewAreaRepository(db)
}

// newTestSearcher starts a mock Gemini server that answers every request with
// respond, and returns a searcher pointed at it plus the request counter. The
// searcher has no official-page client, so it searches without linked pages.
func newTestSearcher(t *testing.T, cfg gemini.Config, logger *logging.Logger, respond func(n int32, body map[string]any) (int, string)) (*gemini.ConcertSearcher, *atomic.Int32) {
	t.Helper()
	return newTestSearcherWithPages(t, cfg, logger, nil, respond)
}

// newTestSearcherWithPages is newTestSearcher with pageClient fetching the
// official top page.
func newTestSearcherWithPages(t *testing.T, cfg gemini.Config, logger *logging.Logger, pageClient *http.Client, respond func(n int32, body map[string]any) (int, string)) (*gemini.ConcertSearcher, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		status, resp := respond(n, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(ts.Close)

	if cfg.APIKey == "" {
		cfg.APIKey = "test"
	}
	if cfg.Model == "" {
		cfg.Model = "gemini-test"
	}
	if logger == nil {
		var err error
		logger, err = logging.New()
		require.NoError(t, err)
	}
	s, err := gemini.NewConcertSearcher(context.Background(), cfg,
		&http.Client{Transport: &rewriteTransport{URL: ts.URL}}, pageClient, testAreas(t), logger)
	require.NoError(t, err)
	return s, &calls
}

// flatEvent is one returned event with its series fields, for readable
// expectations. Times are RFC3339 ("" when unknown).
type flatEvent struct {
	Title     string
	Type      entity.SeriesType
	SourceURL string
	Venue     string
	AdminArea string
	LocalDate string
	StartTime string
	OpenTime  string
}

func flatten(t *testing.T, series []*entity.DiscoveredSeries) []flatEvent {
	t.Helper()
	var out []flatEvent
	for _, s := range series {
		for _, ev := range s.Events {
			fe := flatEvent{
				Title: s.Title, Type: s.Type, SourceURL: s.SourceURL,
				Venue: ev.ListedVenueName, LocalDate: ev.LocalDate.Format("2006-01-02"),
			}
			if ev.AdminArea != nil {
				fe.AdminArea = *ev.AdminArea
			}
			if ev.StartTime != nil {
				fe.StartTime = ev.StartTime.String()
			}
			if ev.OpenTime != nil {
				fe.OpenTime = ev.OpenTime.String()
			}
			out = append(out, fe)
		}
	}
	return out
}

func TestConcertSearcher_Search(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
	site := &entity.OfficialSite{URL: "https://www.example.com/"}

	type want struct {
		events     []flatEvent
		seriesSize []int
	}
	tests := []struct {
		name string
		body string
		// at is the search time; zero means from.
		at   time.Time
		want want
	}{
		{
			// @spec components/entity/concert/search "Yesterday's show early in the morning"
			name: "Yesterday's show early in the morning",
			// 2026-11-10 23:30 UTC is 2026-11-11 08:30 at the Venue (Asia/Tokyo).
			at: time.Date(2026, 11, 10, 23, 30, 0, 0, time.UTC),
			body: `{"series": [
				{"title": "Yesterday", "source_url": "https://www.example.com/y", "events": [{"venue": "Hall A", "country": "JP", "admin_area": "JP-13", "local_date": "2026-11-10", "open_time": "", "start_time": ""}]},
				{"title": "Today", "source_url": "https://www.example.com/t", "events": [{"venue": "Hall B", "country": "JP", "admin_area": "JP-13", "local_date": "2026-11-11", "open_time": "", "start_time": ""}]}
			]}`,
			want: want{
				events:     []flatEvent{{Title: "Today", Type: entity.SeriesTypeSingle, SourceURL: "https://www.example.com/t", Venue: "Hall B", AdminArea: "JP-13", LocalDate: "2026-11-11"}},
				seriesSize: []int{1},
			},
		},
		{
			// @spec components/entity/concert/search "Time zone unknown, date possibly still today"
			name: "Time zone unknown, date possibly still today",
			// 2026-11-11 08:00 UTC is 2026-11-10 at UTC-12.
			at: time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC),
			body: `{"series": [
				{"title": "Maybe Today", "source_url": "https://www.example.com/m", "events": [{"venue": "Hall A", "country": "", "admin_area": "", "local_date": "2026-11-10", "open_time": "", "start_time": ""}]}
			]}`,
			want: want{
				events:     []flatEvent{{Title: "Maybe Today", Type: entity.SeriesTypeSingle, SourceURL: "https://www.example.com/m", Venue: "Hall A", LocalDate: "2026-11-10"}},
				seriesSize: []int{1},
			},
		},
		{
			// @spec components/entity/concert/search "Time zone unknown, date past everywhere"
			name: "Time zone unknown, date past everywhere",
			// 2026-11-11 13:00 UTC is 2026-11-11 at UTC-12.
			at: time.Date(2026, 11, 11, 13, 0, 0, 0, time.UTC),
			body: `{"series": [
				{"title": "Past Everywhere", "source_url": "https://www.example.com/p", "events": [{"venue": "Hall A", "country": "", "admin_area": "", "local_date": "2026-11-10", "open_time": "", "start_time": ""}]}
			]}`,
			want: want{},
		},
		{
			// @spec components/entity/concert/search "Same venue in another letter case"
			name: "Same venue in another letter case",
			body: `{"series": [{"title": "TOUR", "source_url": "https://www.example.com/tour", "events": [
				{"venue": "Zepp Haneda", "country": "JP", "admin_area": "JP-13", "local_date": "2026-05-20", "open_time": "", "start_time": "19:00"},
				{"venue": "ZEPP HANEDA", "country": "JP", "admin_area": "JP-13", "local_date": "2026-05-20", "open_time": "", "start_time": "19:00"}
			]}]}`,
			want: want{
				events:     []flatEvent{{Title: "TOUR", Type: entity.SeriesTypeSingle, SourceURL: "https://www.example.com/tour", Venue: "Zepp Haneda", AdminArea: "JP-13", LocalDate: "2026-05-20", StartTime: "19:00"}},
				seriesSize: []int{1},
			},
		},
		{
			// @spec components/entity/concert/search "Tour across venues"
			name: "Tour across venues",
			body: `{"series": [{"title": "TOUR 2026", "source_url": "https://www.example.com/tour", "events": [
				{"venue": "Zepp Nagoya", "country": "JP", "admin_area": "愛知県", "local_date": "2026-03-15", "open_time": "17:00", "start_time": "18:00"},
				{"venue": "Taipei Arena", "country": "TW", "admin_area": "tw-tpe", "local_date": "2026-04-01", "open_time": "", "start_time": "19:00"}
			]}]}`,
			want: want{
				events: []flatEvent{
					{Title: "TOUR 2026", Type: entity.SeriesTypeTour, SourceURL: "https://www.example.com/tour", Venue: "Zepp Nagoya", AdminArea: "JP-23", LocalDate: "2026-03-15", StartTime: "18:00", OpenTime: "17:00"},
					{Title: "TOUR 2026", Type: entity.SeriesTypeTour, SourceURL: "https://www.example.com/tour", Venue: "Taipei Arena", AdminArea: "TW-TPE", LocalDate: "2026-04-01", StartTime: "19:00"},
				},
				seriesSize: []int{2},
			},
		},
		{
			// @spec components/entity/concert/search "Past event left out"
			name: "Past event left out",
			body: `{"series": [
				{"title": "Old Show", "source_url": "https://www.example.com/old", "events": [{"venue": "Hall A", "country": "JP", "admin_area": "JP-13", "local_date": "2026-02-28", "open_time": "", "start_time": ""}]},
				{"title": "New Show", "source_url": "https://www.example.com/new", "events": [{"venue": "Hall B", "country": "JP", "admin_area": "JP-13", "local_date": "2026-03-01", "open_time": "", "start_time": ""}]}
			]}`,
			want: want{
				events:     []flatEvent{{Title: "New Show", Type: entity.SeriesTypeSingle, SourceURL: "https://www.example.com/new", Venue: "Hall B", AdminArea: "JP-13", LocalDate: "2026-03-01"}},
				seriesSize: []int{1},
			},
		},
		{
			// @spec components/entity/concert/search "Two standalone shows with one title"
			name: "Two standalone shows with one title",
			body: `{"series": [
				{"title": "Special Live", "source_url": "https://www.example.com/a", "events": [{"venue": "Hall A", "country": "JP", "admin_area": "", "local_date": "2026-05-01", "open_time": "", "start_time": ""}]},
				{"title": "Special Live", "source_url": "https://www.example.com/b", "events": [{"venue": "Hall B", "country": "JP", "admin_area": "", "local_date": "2026-06-01", "open_time": "", "start_time": ""}]}
			]}`,
			want: want{
				events: []flatEvent{
					{Title: "Special Live", Type: entity.SeriesTypeSingle, SourceURL: "https://www.example.com/a", Venue: "Hall A", LocalDate: "2026-05-01"},
					{Title: "Special Live", Type: entity.SeriesTypeSingle, SourceURL: "https://www.example.com/b", Venue: "Hall B", LocalDate: "2026-06-01"},
				},
				seriesSize: []int{1, 1},
			},
		},
		{
			// @spec components/entity/concert/search "First and second stage kept"
			name: "First and second stage kept",
			body: `{"series": [{"title": "Billboard Live", "source_url": "https://www.example.com/bb", "events": [
				{"venue": "Billboard Live TOKYO", "country": "JP", "admin_area": "JP-13", "local_date": "2026-08-07", "open_time": "", "start_time": "18:00"},
				{"venue": "Billboard Live TOKYO", "country": "JP", "admin_area": "JP-13", "local_date": "2026-08-07", "open_time": "", "start_time": "21:00"}
			]}]}`,
			want: want{
				events: []flatEvent{
					{Title: "Billboard Live", Type: entity.SeriesTypeSingle, SourceURL: "https://www.example.com/bb", Venue: "Billboard Live TOKYO", AdminArea: "JP-13", LocalDate: "2026-08-07", StartTime: "18:00"},
					{Title: "Billboard Live", Type: entity.SeriesTypeSingle, SourceURL: "https://www.example.com/bb", Venue: "Billboard Live TOKYO", AdminArea: "JP-13", LocalDate: "2026-08-07", StartTime: "21:00"},
				},
				seriesSize: []int{2},
			},
		},
		{
			// @spec components/entity/concert/search "Identical event listed twice"
			name: "Identical event listed twice",
			body: `{"series": [{"title": "TOUR", "source_url": "https://www.example.com/tour", "events": [
				{"venue": "Zepp Haneda", "country": "JP", "admin_area": "JP-13", "local_date": "2026-04-10", "open_time": "", "start_time": "19:00"}
			]},
				{"title": "TOUR Tokyo", "source_url": "https://www.example.com/news", "events": [{"venue": "Zepp Haneda", "country": "JP", "admin_area": "JP-13", "local_date": "2026-04-10", "open_time": "", "start_time": "19:00"}]}
			]}`,
			want: want{
				events:     []flatEvent{{Title: "TOUR", Type: entity.SeriesTypeSingle, SourceURL: "https://www.example.com/tour", Venue: "Zepp Haneda", AdminArea: "JP-13", LocalDate: "2026-04-10", StartTime: "19:00"}},
				seriesSize: []int{1},
			},
		},
		{
			// @spec components/entity/concert/search "Same venue in two notations"
			name: "Same venue in two notations",
			body: `{"series": [{"title": "TOUR", "source_url": "https://www.example.com/tour", "events": [
				{"venue": "渋谷CLUB QUATTRO", "country": "JP", "admin_area": "JP-13", "local_date": "2026-05-20", "open_time": "", "start_time": "19:00"},
				{"venue": "渋谷 CLUB QUATTRO", "country": "JP", "admin_area": "JP-13", "local_date": "2026-05-20", "open_time": "", "start_time": "19:00"}
			]}]}`,
			want: want{
				events:     []flatEvent{{Title: "TOUR", Type: entity.SeriesTypeSingle, SourceURL: "https://www.example.com/tour", Venue: "渋谷CLUB QUATTRO", AdminArea: "JP-13", LocalDate: "2026-05-20", StartTime: "19:00"}},
				seriesSize: []int{1},
			},
		},
		{
			// @spec components/entity/concert/search "Former name kept"
			name: "Former name kept",
			body: `{"series": [
				{"title": "Nagoya Live", "source_url": "https://www.example.com/nagoya", "events": [{"venue": "クロコくんホール（旧 日本ガイシホール）", "country": "JP", "admin_area": "JP-23", "local_date": "2026-07-01", "open_time": "", "start_time": ""}]}
			]}`,
			want: want{
				events:     []flatEvent{{Title: "Nagoya Live", Type: entity.SeriesTypeSingle, SourceURL: "https://www.example.com/nagoya", Venue: "クロコくんホール（旧 日本ガイシホール）", AdminArea: "JP-23", LocalDate: "2026-07-01"}},
				seriesSize: []int{1},
			},
		},
		{
			// @spec components/entity/concert/search "Literal null start"
			name: "Literal null start",
			body: `{"series": [
				{"title": "Show", "source_url": "https://www.example.com/show", "events": [{"venue": "Hall A", "country": "JP", "admin_area": "", "local_date": "2026-05-01", "open_time": "null", "start_time": "null"}]}
			]}`,
			want: want{
				events:     []flatEvent{{Title: "Show", Type: entity.SeriesTypeSingle, SourceURL: "https://www.example.com/show", Venue: "Hall A", LocalDate: "2026-05-01"}},
				seriesSize: []int{1},
			},
		},
		{
			// @spec components/entity/concert/search "Show abroad keeps its local time"
			name: "Show abroad keeps its local time",
			body: `{"series": [{"title": "Seoul Live", "source_url": "https://www.example.com/seoul", "events": [
				{"venue": "Olympic Hall", "country": "KR", "admin_area": "KR-11", "local_date": "2026-09-05", "open_time": "17:30", "start_time": "18:30"}
			]}]}`,
			want: want{
				events:     []flatEvent{{Title: "Seoul Live", Type: entity.SeriesTypeSingle, SourceURL: "https://www.example.com/seoul", Venue: "Olympic Hall", AdminArea: "KR-11", LocalDate: "2026-09-05", StartTime: "18:30", OpenTime: "17:30"}},
				seriesSize: []int{1},
			},
		},
		{
			// @spec components/entity/concert/search "Time skipped by daylight saving"
			name: "Time skipped by daylight saving",
			body: `{"series": [{"title": "NYC Live", "source_url": "https://www.example.com/nyc", "events": [
				{"venue": "Hall NY", "country": "US", "admin_area": "US-NY", "local_date": "2027-03-14", "open_time": "01:30", "start_time": "02:30"}
			]}]}`,
			want: want{
				events:     []flatEvent{{Title: "NYC Live", Type: entity.SeriesTypeSingle, SourceURL: "https://www.example.com/nyc", Venue: "Hall NY", AdminArea: "US-NY", LocalDate: "2027-03-14", OpenTime: "01:30"}},
				seriesSize: []int{1},
			},
		},
		{
			// @spec components/entity/concert/search "Two days at one venue"
			name: "Two days at one venue",
			body: `{"series": [{"title": "LIVE 2026 at LaLa arena TOKYO-BAY", "source_url": "https://www.example.com/lala", "events": [
				{"venue": "LaLa arena TOKYO-BAY", "country": "JP", "admin_area": "JP-12", "local_date": "2026-11-25", "open_time": "", "start_time": ""},
				{"venue": "LaLa arena TOKYO-BAY", "country": "JP", "admin_area": "JP-12", "local_date": "2026-11-26", "open_time": "", "start_time": ""}
			]}]}`,
			want: want{
				events: []flatEvent{
					{Title: "LIVE 2026 at LaLa arena TOKYO-BAY", Type: entity.SeriesTypeSingle, SourceURL: "https://www.example.com/lala", Venue: "LaLa arena TOKYO-BAY", AdminArea: "JP-12", LocalDate: "2026-11-25"},
					{Title: "LIVE 2026 at LaLa arena TOKYO-BAY", Type: entity.SeriesTypeSingle, SourceURL: "https://www.example.com/lala", Venue: "LaLa arena TOKYO-BAY", AdminArea: "JP-12", LocalDate: "2026-11-26"},
				},
				seriesSize: []int{2},
			},
		},
		{
			name: "venue not yet announced does not make a tour",
			body: `{"series": [{"title": "TOUR", "source_url": "https://www.example.com/tour", "events": [
				{"venue": "Zepp Haneda", "country": "JP", "admin_area": "JP-13", "local_date": "2026-04-10", "open_time": "", "start_time": ""},
				{"venue": "TBA", "country": "JP", "admin_area": "", "local_date": "2026-04-20", "open_time": "", "start_time": ""}
			]}]}`,
			want: want{
				events: []flatEvent{
					{Title: "TOUR", Type: entity.SeriesTypeSingle, SourceURL: "https://www.example.com/tour", Venue: "Zepp Haneda", AdminArea: "JP-13", LocalDate: "2026-04-10"},
					{Title: "TOUR", Type: entity.SeriesTypeSingle, SourceURL: "https://www.example.com/tour", Venue: "TBA", LocalDate: "2026-04-20"},
				},
				seriesSize: []int{2},
			},
		},
		{
			name: "code-fenced JSON is accepted",
			body: "```json\n" + `{"series": [{"title": "Show", "source_url": "https://www.example.com/show", "events": [{"venue": "Hall A", "country": "JP", "admin_area": "", "local_date": "2026-05-01", "open_time": "", "start_time": ""}]}]}` + "\n```",
			want: want{
				events:     []flatEvent{{Title: "Show", Type: entity.SeriesTypeSingle, SourceURL: "https://www.example.com/show", Venue: "Hall A", LocalDate: "2026-05-01"}},
				seriesSize: []int{1},
			},
		},
		{
			name: "no events",
			body: `{"series": []}`,
			want: want{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, calls := newTestSearcher(t, gemini.Config{}, nil, func(int32, map[string]any) (int, string) {
				return http.StatusOK, geminiResponse(tt.body, "STOP")
			})

			at := from
			if !tt.at.IsZero() {
				at = tt.at
			}
			got, err := s.Search(context.Background(), artist, site, at)

			require.NoError(t, err)
			assert.Equal(t, tt.want.events, flatten(t, got))
			sizes := make([]int, 0, len(got))
			for _, ds := range got {
				sizes = append(sizes, len(ds.Events))
			}
			if tt.want.seriesSize == nil {
				assert.Empty(t, sizes)
			} else {
				assert.Equal(t, tt.want.seriesSize, sizes)
			}
			assert.Equal(t, int32(1), calls.Load(), "one grounded call per Search")
		})
	}
}

func TestConcertSearcher_Search_Request(t *testing.T) {
	t.Parallel()

	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
	from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		cfg   gemini.Config
		site  *entity.OfficialSite
		check func(t *testing.T, body map[string]any)
	}{
		{
			name: "production configuration",
			cfg: gemini.Config{
				Model:                            "gemini-3.8-flash",
				ThinkingLevel:                    "low",
				OmitTemperature:                  true,
				Temperature:                      1.0,
				IncludeServerSideToolInvocations: true,
			},
			site: &entity.OfficialSite{URL: "https://www.example.com/artist/"},
			check: func(t *testing.T, body map[string]any) {
				t.Helper()
				genCfg, _ := body["generationConfig"].(map[string]any)
				require.NotNil(t, genCfg)
				assert.NotContains(t, genCfg, "temperature", "temperature must not be sent")
				assert.Equal(t, "application/json", genCfg["responseMimeType"])
				assert.Equal(t, map[string]any{"thinkingLevel": "LOW"}, genCfg["thinkingConfig"])

				schema, _ := genCfg["responseJsonSchema"].(map[string]any)
				require.NotNil(t, schema, "responseJsonSchema must be set")
				props := schema["properties"].(map[string]any)
				assert.Equal(t, []any{"series"}, schema["required"])
				events := props["series"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["events"].(map[string]any)
				assert.EqualValues(t, 1, events["minItems"])

				toolCfg, _ := body["toolConfig"].(map[string]any)
				assert.Equal(t, true, toolCfg["includeServerSideToolInvocations"])

				prompt := body["contents"].([]any)[0].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"].(string)
				assert.Contains(t, prompt, "Official site: https://www.example.com/artist/\n", "the full official-site URL is passed")
				assert.Contains(t, prompt, "Return every tour and show of Test Artist taking place on or after ")

				sys := body["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"].(string)
				// The instruction asks for the exclusion; the model's judgement is not unit-testable.
				assert.Contains(t, sys, "Exclude music festivals, cancelled shows, private events that are not open to the public, and shows held on a ship or at sea (a cruise), which have no fixed place.")
				assert.Contains(t, sys, "Done when: every tour that the official sources mention has all of its announced dates in your answer.")
			},
		},
		{
			name: "temperature sent unless omitted",
			cfg:  gemini.Config{Temperature: 0.3},
			site: &entity.OfficialSite{URL: "https://www.example.com/"},
			check: func(t *testing.T, body map[string]any) {
				t.Helper()
				genCfg := body["generationConfig"].(map[string]any)
				assert.InDelta(t, 0.3, genCfg["temperature"], 1e-6)
				assert.NotContains(t, genCfg, "thinkingConfig")
				assert.NotContains(t, body, "toolConfig")
			},
		},
		{
			// @spec components/entity/concert/search "No official site"
			name: "No official site",
			cfg:  gemini.Config{},
			site: nil,
			check: func(t *testing.T, body map[string]any) {
				t.Helper()
				prompt := body["contents"].([]any)[0].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"].(string)
				assert.Contains(t, prompt, "of Test Artist taking place")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var (
				mu       sync.Mutex
				captured map[string]any
			)
			s, _ := newTestSearcher(t, tt.cfg, nil, func(_ int32, body map[string]any) (int, string) {
				mu.Lock()
				captured = body
				mu.Unlock()
				return http.StatusOK, geminiResponse(`{"series":[]}`, "STOP")
			})

			before := time.Now().UTC().Truncate(time.Second)
			_, err := s.Search(context.Background(), artist, tt.site, from)
			after := time.Now().UTC()
			require.NoError(t, err)

			mu.Lock()
			defer mu.Unlock()
			require.NotNil(t, captured)

			// Tools: GoogleSearch with a 2-month window in whole seconds, then URL context.
			tools, _ := captured["tools"].([]any)
			require.Len(t, tools, 2)
			gs, _ := tools[0].(map[string]any)["googleSearch"].(map[string]any)
			require.NotNil(t, gs, "first tool must be googleSearch")
			trf := gs["timeRangeFilter"].(map[string]any)
			start, err := time.Parse(time.RFC3339Nano, trf["startTime"].(string))
			require.NoError(t, err)
			end, err := time.Parse(time.RFC3339Nano, trf["endTime"].(string))
			require.NoError(t, err)
			assert.Zero(t, end.Nanosecond(), "time range must be whole seconds")
			assert.False(t, end.Before(before) || end.After(after), "time range ends now")
			assert.True(t, start.Equal(end.AddDate(0, -2, 0)), "time range starts 2 months before now")
			assert.Contains(t, tools[1].(map[string]any), "urlContext")

			tt.check(t, captured)
		})
	}
}

func TestConcertSearcher_Search_Errors(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
	site := &entity.OfficialSite{URL: "https://www.example.com/"}

	tests := []struct {
		name      string
		response  string
		wantErr   []error
		wantCalls int32
	}{
		{
			// @spec components/entity/concert/search "Response without a candidate"
			name: "Response without a candidate",
			response: `{"candidates": [], "promptFeedback": {"blockReason": "OTHER"},
				"usageMetadata": {"promptTokenCount": 10, "toolUsePromptTokenCount": 120000, "totalTokenCount": 120010}}`,
			wantErr:   []error{apperr.ErrInternal},
			wantCalls: 1,
		},
		{
			name:      "truncated JSON is permanent",
			response:  geminiResponse(`{"series": [{"title": "Test`, "STOP"),
			wantErr:   []error{apperr.ErrInternal},
			wantCalls: 1,
		},
		{
			name:      "structural mismatch is permanent",
			response:  geminiResponse(`{"series": "not an array"}`, "STOP"),
			wantErr:   []error{apperr.ErrInternal},
			wantCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, calls := newTestSearcher(t, gemini.Config{}, nil, func(int32, map[string]any) (int, string) {
				return http.StatusOK, tt.response
			})

			got, err := s.Search(context.Background(), artist, site, from)

			assert.Nil(t, got)
			for _, want := range tt.wantErr {
				assert.ErrorIs(t, err, want)
			}
			assert.Equal(t, tt.wantCalls, calls.Load(), "not retried")
		})
	}
}

// TestConcertSearcher_Search_LogsSearchQueries asserts that the response log
// carries the Google Search queries, read from groundingMetadata or, when it
// is absent, from the server-side tool-call parts.
func TestConcertSearcher_Search_LogsSearchQueries(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
	site := &entity.OfficialSite{URL: "https://www.example.com/"}
	body := strconv.Quote(`{"series":[]}`)

	tests := []struct {
		name     string
		response string
		want     []any
	}{
		{
			name: "tool-call parts when groundingMetadata is absent",
			response: `{"candidates": [{"content": {"parts": [
				{"toolCall": {"toolType": "GOOGLE_SEARCH_WEB", "args": {"queries": ["Test Artist ライブ 2026", "Test Artist ツアー"]}}},
				{"toolResponse": {"toolType": "GOOGLE_SEARCH_WEB"}},
				{"toolCall": {"toolType": "GOOGLE_SEARCH_WEB", "args": {"queries": ["Test Artist 公演"]}}},
				{"text": ` + body + `}
			]}, "finishReason": "STOP"}]}`,
			want: []any{"Test Artist ライブ 2026", "Test Artist ツアー", "Test Artist 公演"},
		},
		{
			name: "groundingMetadata when present",
			response: `{"candidates": [{"content": {"parts": [
				{"toolCall": {"toolType": "GOOGLE_SEARCH_WEB", "args": {"queries": ["ignored"]}}},
				{"text": ` + body + `}
			]}, "finishReason": "STOP", "groundingMetadata": {"webSearchQueries": ["grounded query"]}}]}`,
			want: []any{"grounded query"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf syncBuffer
			logger, err := logging.New(logging.WithWriter(&buf), logging.WithFormat(logging.FormatJSON))
			require.NoError(t, err)
			s, _ := newTestSearcher(t, gemini.Config{IncludeServerSideToolInvocations: true}, logger, func(int32, map[string]any) (int, string) {
				return http.StatusOK, tt.response
			})

			_, err = s.Search(context.Background(), artist, site, from)
			require.NoError(t, err)

			entry := findLogEntry(t, buf.Bytes(), "successfully received Gemini response")
			assert.Equal(t, tt.want, entry["search_queries"])
			assert.EqualValues(t, len(tt.want), entry["web_search_queries"])
		})
	}
}

// syncBuffer is a bytes.Buffer safe for the logger's concurrent writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}

// findLogEntry returns the first JSON log entry whose msg equals msg.
func findLogEntry(t *testing.T, logs []byte, msg string) map[string]any {
	t.Helper()
	sc := bufio.NewScanner(bytes.NewReader(logs))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var entry map[string]any
		if err := json.Unmarshal(sc.Bytes(), &entry); err != nil {
			continue
		}
		if entry["msg"] == msg {
			return entry
		}
	}
	require.Failf(t, "log entry not found", "no %q entry in logs:\n%s", msg, logs)
	return nil
}

// promptOf returns the user prompt of a captured Gemini request body.
func promptOf(t *testing.T, body map[string]any) string {
	t.Helper()
	require.NotNil(t, body)
	return body["contents"].([]any)[0].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"].(string)
}

// TestConcertSearcher_Search_LinkedPages asserts that the official top page's
// links are listed in the prompt with their labels, that the official site
// in the prompt is the URL the top page was read from, and that the prompt
// is the plain one when the top page has no link to list or cannot be read.
func TestConcertSearcher_Search_LinkedPages(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{ID: "artist-1", Name: "Vaundy"}

	tests := []struct {
		name      string
		site      string
		pages     map[string]http.HandlerFunc
		wantSite  string
		wantLinks []string
		wantURLs  []string
	}{
		{
			name: "top page links listed with their labels",
			site: "http://vaundy.jp/",
			pages: map[string]http.HandlerFunc{"vaundy.jp": htmlPage(link("/live/", "LIVE") +
				banner("https://member.vaundy.jp/feature/ASIAARENATOUR_2026", "ASIA ARENA TOUR 2026") +
				link("/news/", "NEWS") + link("/profile/", "PROFILE"))},
			wantSite: "http://vaundy.jp/",
			wantLinks: []string{
				"LIVE: http://vaundy.jp/live/",
				"ASIA ARENA TOUR 2026: https://member.vaundy.jp/feature/ASIAARENATOUR_2026",
				"NEWS: http://vaundy.jp/news/",
			},
			wantURLs: []string{"http://vaundy.jp/live/", "https://member.vaundy.jp/feature/ASIAARENATOUR_2026", "http://vaundy.jp/news/"},
		},
		{
			name:      "registered www host without DNS: the apex URL is passed",
			site:      "http://www.vaundy.jp/",
			pages:     map[string]http.HandlerFunc{"vaundy.jp": htmlPage(link("/live/", "LIVE"))},
			wantSite:  "http://vaundy.jp/",
			wantLinks: []string{"LIVE: http://vaundy.jp/live/"},
			wantURLs:  []string{"http://vaundy.jp/live/"},
		},
		{
			name:     "top page with only site information links",
			site:     "http://vaundy.jp/",
			pages:    map[string]http.HandlerFunc{"vaundy.jp": htmlPage(link("/profile/", "PROFILE") + link("/discography/", "DISCOGRAPHY"))},
			wantSite: "http://vaundy.jp/",
		},
		{
			name: "top page unavailable",
			site: "http://vaundy.jp/",
			pages: map[string]http.HandlerFunc{
				"vaundy.jp": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) },
			},
			wantSite: "http://vaundy.jp/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var (
				mu   sync.Mutex
				body map[string]any
			)
			pageClient := newSiteClient(t, tt.pages)
			s, _ := newTestSearcherWithPages(t, gemini.Config{}, nil, pageClient, func(_ int32, b map[string]any) (int, string) {
				mu.Lock()
				body = b
				mu.Unlock()
				return http.StatusOK, geminiResponse(`{"series":[]}`, "STOP")
			})

			_, md, err := s.SearchExt(context.Background(), artist, &entity.OfficialSite{URL: tt.site}, from)
			require.NoError(t, err)

			mu.Lock()
			defer mu.Unlock()
			got := promptOf(t, body)
			want := fmt.Sprintf("Return every tour and show of Vaundy taking place on or after %s.\n\nOfficial site: %s\n",
				time.Now().UTC().Format("2006-01-02"), tt.wantSite)
			if tt.wantLinks != nil {
				want += "\nLinks on the official top page (label: URL):\n" + strings.Join(tt.wantLinks, "\n") + "\n"
			}
			assert.Equal(t, want, got)
			require.NotNil(t, md.Grounded)
			assert.Equal(t, tt.wantURLs, md.Grounded.LinkedPageURLs)
		})
	}
}

// TestConcertSearcher_Search_LogsURLContextCounts feeds the URL context tool
// parts recorded from gemini-3.8-flash (2026-10-07; thought signatures and
// text trimmed): one call for two URLs, one retrieved and one not.
func TestConcertSearcher_Search_LogsURLContextCounts(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{ID: "artist-1", Name: "Vaundy"}
	site := &entity.OfficialSite{URL: "http://vaundy.jp/"}
	response := `{"candidates": [{"content": {"parts": [
		{"toolCall": {"toolType": "URL_CONTEXT", "args": {"urls": ["https://vaundy.jp/live/", "https://vaundy.jp/this-page-does-not-exist-xyz/"]}, "id": "call_655975"}},
		{"toolResponse": {"toolType": "URL_CONTEXT", "response": {"url_metadata": [
			{"retrieved_url": "https://vaundy.jp/live/", "url_retrieval_status": "URL_RETRIEVAL_STATUS_SUCCESS"},
			{"retrieved_url": "https://vaundy.jp/this-page-does-not-exist-xyz/", "url_retrieval_status": "URL_RETRIEVAL_STATUS_ERROR"}
		]}, "id": "call_655975"}},
		{"text": ` + strconv.Quote(`{"series":[]}`) + `}
	]}, "finishReason": "STOP",
	"urlContextMetadata": {"urlMetadata": [
		{"retrievedUrl": "https://vaundy.jp/live/", "urlRetrievalStatus": "URL_RETRIEVAL_STATUS_SUCCESS"}
	]}}]}`

	var buf syncBuffer
	logger, err := logging.New(logging.WithWriter(&buf), logging.WithFormat(logging.FormatJSON))
	require.NoError(t, err)
	pageClient := newSiteClient(t, map[string]http.HandlerFunc{"vaundy.jp": htmlPage(link("/live/", "LIVE"))})
	s, _ := newTestSearcherWithPages(t, gemini.Config{IncludeServerSideToolInvocations: true}, logger, pageClient,
		func(int32, map[string]any) (int, string) { return http.StatusOK, response })

	_, md, err := s.SearchExt(context.Background(), artist, site, from)
	require.NoError(t, err)

	require.NotNil(t, md.Grounded)
	assert.Equal(t, 1, md.Grounded.URLContextCalls)
	assert.Equal(t, 1, md.Grounded.URLContextSucceeded)
	entry := findLogEntry(t, buf.Bytes(), "successfully received Gemini response")
	assert.EqualValues(t, 1, entry["url_context_calls"])
	assert.EqualValues(t, 1, entry["url_context_succeeded"])
	assert.EqualValues(t, 1, entry["url_context_retrieved"])
	assert.EqualValues(t, 1, entry["linked_pages"])
	assert.Equal(t, []any{"http://vaundy.jp/live/"}, entry["linked_page_urls"])
}

// TestConcertSearcher_AreaAndTimeZone checks how Search resolves the admin
// area the model returns to a first-level Area, and picks each event's time
// zone, against the seeded reference data.
func TestConcertSearcher_AreaAndTimeZone(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
	site := &entity.OfficialSite{URL: "https://www.example.com/"}

	tests := []struct {
		name, area, zone string
		wantArea         string // "" for none
		wantZone         string // "" for none
	}{
		// @spec components/entity/concert/search "Prefecture in Japanese"
		// @spec components/entity/concert/search "Area with one time zone"
		{name: "Prefecture in Japanese", area: "愛知県", wantArea: "JP-23", wantZone: "Asia/Tokyo"},
		// @spec components/entity/concert/search "Prefecture in English"
		{name: "Prefecture in English", area: "tokyo", wantArea: "JP-13", wantZone: "Asia/Tokyo"},
		// @spec components/entity/concert/search "Overseas venue"
		{name: "Overseas venue", area: "TW-TPE", wantArea: "TW-TPE", wantZone: "Asia/Taipei"},
		// @spec components/entity/concert/search "State name"
		{name: "State name", area: "California", wantArea: "US-CA", wantZone: "America/Los_Angeles"},
		// @spec components/entity/concert/search "Lower-level code"
		{name: "Lower-level code", area: "FR-33", wantArea: "FR-NAQ", wantZone: "Europe/Paris"},
		// @spec components/entity/concert/search "Territory without subdivisions"
		{name: "Territory by its own code", area: "HK", wantArea: "HK", wantZone: "Asia/Hong_Kong"},
		{name: "Territory by its subdivision code", area: "CN-HK", wantArea: "HK", wantZone: "Asia/Hong_Kong"},
		// @spec components/entity/concert/search "Unrecognized area"
		{name: "Unrecognized name", area: "Atlantis"},
		{name: "Well-formed code that is no Area", area: "JP-99"},
		// @spec components/entity/concert/search "Name of areas in two countries"
		{name: "Name of areas in two countries", area: "Victoria"},
		// @spec components/entity/concert/search "Area without a single time zone"
		{name: "Area without a single time zone", area: "US-TX", zone: "America/Chicago", wantArea: "US-TX", wantZone: "America/Chicago"},
		// @spec components/entity/concert/search "Zone of another country"
		{name: "Zone of another country", area: "US-TX", zone: "Europe/London", wantArea: "US-TX"},
		// @spec components/entity/concert/search "Area unknown"
		{name: "Area unknown", area: "", zone: "Asia/Tokyo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := fmt.Sprintf(`{"series": [{"title": "Show", "source_url": "https://www.example.com/s", "events": [
				{"venue": "Hall", "country": "", "admin_area": %q, "time_zone": %q, "local_date": "2026-05-01", "open_time": "", "start_time": ""}]}]}`,
				tt.area, tt.zone)
			s, _ := newTestSearcher(t, gemini.Config{}, nil, func(int32, map[string]any) (int, string) {
				return http.StatusOK, geminiResponse(body, "STOP")
			})

			got, err := s.Search(context.Background(), artist, site, from)

			require.NoError(t, err)
			require.Len(t, got, 1)
			require.Len(t, got[0].Events, 1)
			ev := got[0].Events[0]
			if tt.wantArea == "" {
				assert.Nil(t, ev.AdminArea)
			} else {
				require.NotNil(t, ev.AdminArea)
				assert.Equal(t, tt.wantArea, *ev.AdminArea)
			}
			if tt.wantZone == "" {
				assert.Nil(t, ev.TimeZone)
			} else {
				require.NotNil(t, ev.TimeZone)
				assert.Equal(t, tt.wantZone, *ev.TimeZone)
			}
		})
	}
}
