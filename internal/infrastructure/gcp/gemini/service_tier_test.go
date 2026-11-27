package gemini_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
)

// modelTransport answers each request to the model after a delay on the
// test's clock, without a network, and records what each request asked for.
type modelTransport struct {
	mu       sync.Mutex
	requests []modelRequest
	// answer returns the delay, status and body of the n-th request (from 1).
	answer func(n int) (time.Duration, int, string)
}

// modelRequest is what one request asked for.
type modelRequest struct {
	body string
	// serverTimeout is the answer budget the request announced, in seconds.
	serverTimeout string
}

// RoundTrip implements http.RoundTripper.
func (m *modelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.requests = append(m.requests, modelRequest{body: string(body), serverTimeout: req.Header.Get("X-Server-Timeout")})
	n := len(m.requests)
	m.mu.Unlock()

	delay, status, resp := m.answer(n)
	select {
	case <-req.Context().Done():
		return nil, req.Context().Err()
	case <-time.After(delay):
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(resp)),
		Request:    req,
	}, nil
}

func (m *modelTransport) sent() []modelRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]modelRequest(nil), m.requests...)
}

// newTierSearcher returns a searcher at tier whose requests transport
// answers. It reads no database, so it can run on a synctest clock.
func newTierSearcher(t *testing.T, cfg gemini.Config, transport *modelTransport) *gemini.ConcertSearcher {
	t.Helper()
	areas := mocks.NewMockAreaRepository(t)
	areas.EXPECT().ListByCountry(mock.Anything, "JP").
		Return(&entity.CountryAreas{TimeZones: []*entity.AreaTimeZone{{Name: "Asia/Tokyo"}}}, nil).Maybe()
	logger, err := logging.New()
	require.NoError(t, err)
	cfg.APIKey, cfg.Model = "test", "gemini-test"
	s, err := gemini.NewConcertSearcher(context.Background(), cfg, &http.Client{Transport: transport}, nil, areas, logger)
	require.NoError(t, err)
	return s
}

const oneShowBody = `{"series": [{"title": "Show", "source_url": "https://example.com/show", "events": [
	{"venue": "Test Hall", "country": "JP", "admin_area": "", "local_date": "2026-03-01", "open_time": "", "start_time": ""}]}]}`

func TestSearch_ServiceTier(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{ID: "artist-1", Name: "Test Artist"}
	answer := func(delay time.Duration) func(int) (time.Duration, int, string) {
		return func(int) (time.Duration, int, string) {
			return delay, http.StatusOK, geminiResponse(oneShowBody, "STOP")
		}
	}

	// @spec components/entity/concert/search "Late answer"
	t.Run("returns an answer that comes after 9 minutes", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			transport := &modelTransport{answer: answer(9 * time.Minute)}
			s := newTierSearcher(t, gemini.Config{ServiceTier: gemini.ServiceTierFlex}, transport)

			got, err := s.Search(t.Context(), artist, nil, from)

			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, "Show", got[0].Title)
			require.Len(t, transport.sent(), 1)
		})
	})

	// @spec components/entity/concert/search "Busy on every attempt"
	t.Run("fails with Unavailable when all 3 attempts are refused as busy", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			transport := &modelTransport{answer: func(int) (time.Duration, int, string) {
				return time.Second, http.StatusServiceUnavailable, errBody503
			}}
			s := newTierSearcher(t, gemini.Config{ServiceTier: gemini.ServiceTierFlex}, transport)

			got, err := s.Search(t.Context(), artist, nil, from)

			assert.ErrorIs(t, err, apperr.ErrUnavailable)
			assert.Empty(t, got)
			assert.Len(t, transport.sent(), 3)
		})
	})

	// @spec components/entity/concert/search "No answer within 15 minutes"
	t.Run("sends the next attempt when none answers within 15 minutes", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			reporter := &recordingReporter{}
			transport := &modelTransport{answer: func(n int) (time.Duration, int, string) {
				if n == 1 {
					return time.Hour, http.StatusOK, geminiResponse(oneShowBody, "STOP")
				}
				return time.Second, http.StatusOK, geminiResponse(oneShowBody, "STOP")
			}}
			s := newTierSearcher(t, gemini.Config{ServiceTier: gemini.ServiceTierFlex, Reporter: reporter}, transport)
			start := time.Now()

			got, err := s.Search(t.Context(), artist, nil, from)

			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Len(t, transport.sent(), 2)
			reported := reporter.reported()
			require.Len(t, reported, 2)
			assert.Equal(t, "deadline_exceeded", reported[0].gen.ErrorKind)
			assert.Equal(t, 15*time.Minute, reported[0].gen.Latency, "waited the whole budget")
			assert.Less(t, time.Since(start), 16*time.Minute)
		})
	})

	t.Run("fails with Unavailable when no attempt is answered", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			transport := &modelTransport{answer: answer(time.Hour)}
			s := newTierSearcher(t, gemini.Config{ServiceTier: gemini.ServiceTierFlex}, transport)

			_, err := s.Search(t.Context(), artist, nil, from)

			assert.ErrorIs(t, err, apperr.ErrUnavailable)
			assert.Len(t, transport.sent(), 3)
		})
	})

	tiers := []struct {
		name         string
		tier         string
		wantInBody   bool
		wantTimeout  string
		wantReported string
	}{
		// @spec components/entity/concert/search "Lower-priced service level"
		{name: "flex asks for the tier and waits 15 minutes", tier: gemini.ServiceTierFlex, wantInBody: true, wantTimeout: "900", wantReported: "flex"},
		{name: "standard sends no tier and waits 120 seconds", tier: gemini.ServiceTierStandard, wantTimeout: "120", wantReported: "standard"},
		{name: "no tier is the standard tier", wantTimeout: "120", wantReported: "standard"},
	}
	for _, tt := range tiers {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				reporter := &recordingReporter{}
				transport := &modelTransport{answer: answer(0)}
				s := newTierSearcher(t, gemini.Config{ServiceTier: tt.tier, Reporter: reporter}, transport)

				_, err := s.Search(t.Context(), artist, nil, from)

				require.NoError(t, err)
				sent := transport.sent()
				require.Len(t, sent, 1)
				if tt.wantInBody {
					assert.Contains(t, sent[0].body, `"serviceTier":"flex"`)
				} else {
					assert.NotContains(t, sent[0].body, "serviceTier")
				}
				assert.Equal(t, tt.wantTimeout, sent[0].serverTimeout)
				reported := reporter.reported()
				require.Len(t, reported, 1)
				assert.Equal(t, tt.wantReported, reported[0].gen.ServiceTier)
			})
		})
	}

	t.Run("an unknown tier fails the constructor", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)

		_, err = gemini.NewConcertSearcher(context.Background(),
			gemini.Config{APIKey: "test", Model: "m", ServiceTier: "priority"}, nil, nil, nil, logger)

		assert.ErrorContains(t, err, "unknown ServiceTier")
	})
}
