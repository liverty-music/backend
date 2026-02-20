package gemini_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rewriteTransport struct {
	URL string
}

func ptr(t time.Time) *time.Time {
	return &t
}

func ptrStr(s string) *string {
	return &s
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(t.URL)
	req.URL.Scheme = u.Scheme
	req.URL.Host = u.Host
	return http.DefaultTransport.RoundTrip(req)
}

func TestConcertSearcher_Search(t *testing.T) {
	logger, _ := logging.New()
	ctx := context.Background()
	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}

	tests := []struct {
		name         string
		responseBody string
		statusCode   int
		want         []*entity.ScrapedConcert
		wantErr      error
	}{
		{
			name:       "success - single event",
			statusCode: http.StatusOK,
			responseBody: `{
				"events": [
					{
						"artist_name": "Test Artist",
						"event_name": "Test Tour 2026",
						"venue": "Test Hall",
						"local_date": "2026-03-01",
						"start_time": "2026-03-01T18:00:00Z",
						"source_url": "https://example.com/test"
					}
				]
			}`,
			want: []*entity.ScrapedConcert{
				{
					Title:          "Test Tour 2026",
					ListedVenueName:      "Test Hall",
					LocalEventDate: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
					StartTime:      ptr(time.Date(2026, 3, 1, 18, 0, 0, 0, time.UTC)),
					SourceURL:      "https://example.com/test",
				},
			},
			wantErr: nil,
		},
		{
			name:       "success - event with admin_area",
			statusCode: http.StatusOK,
			responseBody: `{
				"events": [
					{
						"artist_name": "Test Artist",
						"event_name": "Nagoya Concert",
						"venue": "Zepp Nagoya",
						"admin_area": "愛知県",
						"local_date": "2026-03-15",
						"start_time": "2026-03-15T18:00:00+09:00",
						"source_url": "https://example.com/nagoya"
					}
				]
			}`,
			want: []*entity.ScrapedConcert{
				{
					Title:           "Nagoya Concert",
					ListedVenueName: "Zepp Nagoya",
					AdminArea:       ptrStr("愛知県"),
					LocalEventDate:  time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
					StartTime:       ptr(time.Date(2026, 3, 15, 18, 0, 0, 0, time.FixedZone("", 9*60*60))),
					SourceURL:       "https://example.com/nagoya",
				},
			},
			wantErr: nil,
		},
		{
			name:       "success - event with empty admin_area returns nil",
			statusCode: http.StatusOK,
			responseBody: `{
				"events": [
					{
						"artist_name": "Test Artist",
						"event_name": "Unknown Venue Concert",
						"venue": "Some Venue",
						"admin_area": "",
						"local_date": "2026-03-20",
						"start_time": null,
						"source_url": "https://example.com/unknown"
					}
				]
			}`,
			want: []*entity.ScrapedConcert{
				{
					Title:           "Unknown Venue Concert",
					ListedVenueName: "Some Venue",
					AdminArea:       nil,
					LocalEventDate:  time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC),
					StartTime:       nil,
					SourceURL:       "https://example.com/unknown",
				},
			},
			wantErr: nil,
		},
		{
			name:       "success - multiple events without deduplication",
			statusCode: http.StatusOK,
			responseBody: `{
				"events": [
					{
						"artist_name": "Test Artist",
						"event_name": "Test Tour 2026",
						"venue": "Test Hall",
						"local_date": "2026-03-01",
						"start_time": "2026-03-01T18:00:00Z",
						"source_url": "https://example.com/test"
					},
					{
						"artist_name": "Test Artist",
						"event_name": "Test Tour 2026",
						"venue": "Test Hall",
						"local_date": "2026-03-01",
						"start_time": "2026-03-01T18:00:00Z",
						"source_url": "https://example.com/test-dup"
					}
				]
			}`,
			want: []*entity.ScrapedConcert{
				{
					Title:          "Test Tour 2026",
					ListedVenueName:      "Test Hall",
					LocalEventDate: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
					StartTime:      ptr(time.Date(2026, 3, 1, 18, 0, 0, 0, time.UTC)),
					SourceURL:      "https://example.com/test",
				},
				{
					Title:          "Test Tour 2026",
					ListedVenueName:      "Test Hall",
					LocalEventDate: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
					StartTime:      ptr(time.Date(2026, 3, 1, 18, 0, 0, 0, time.UTC)),
					SourceURL:      "https://example.com/test-dup",
				},
			},
			wantErr: nil,
		},
		{
			name:       "success - no filter excluded in searcher",
			statusCode: http.StatusOK,
			responseBody: `{
				"events": [
					{
						"artist_name": "Test Artist",
						"event_name": "New Event",
						"venue": "Test Hall",
						"local_date": "2026-04-01",
						"start_time": "2026-04-01T19:00:00Z",
						"source_url": "https://example.com/new"
					}
				]
			}`,
			want: []*entity.ScrapedConcert{
				{
					Title:          "New Event",
					ListedVenueName:      "Test Hall",
					LocalEventDate: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
					StartTime:      ptr(time.Date(2026, 4, 1, 19, 0, 0, 0, time.UTC)),
					SourceURL:      "https://example.com/new",
				},
			},
			wantErr: nil,
		},
		{
			name:       "success - filter past events",
			statusCode: http.StatusOK,
			responseBody: `{
				"events": [
					{
						"artist_name": "Test Artist",
						"event_name": "Past Event",
						"venue": "Test Hall",
						"local_date": "2026-01-01",
						"start_time": "2026-01-01T18:00:00Z",
						"source_url": "https://example.com/past"
					}
				]
			}`,
			want:    nil,
			wantErr: nil,
		},
		{
			name:         "error - invalid json in response content",
			statusCode:   http.StatusOK,
			responseBody: `invalid json`,
			want:         nil,
			wantErr:      apperr.ErrInternal, // Unmarshal error is mapped to Internal in errors.go
		},
		{
			name:         "error - empty response",
			statusCode:   http.StatusOK,
			responseBody: ``,
			want:         nil,
			wantErr:      nil, // effectively empty -> no concerts found, no error
		},
		{
			name:       "error - invalid local date format (skips event)",
			statusCode: http.StatusOK,
			responseBody: `{
				"events": [
					{
						"artist_name": "Test Artist",
						"event_name": "Invalid Date",
						"venue": "Test Hall",
						"local_date": "invalid-date",
						"start_time": "2026-03-01T18:00:00Z",
						"source_url": "https://example.com/invalid"
					}
				]
			}`,
			want:    nil, // Should skip this event
			wantErr: nil, // No error, just filtered out
		},
		{
			name:       "success - various start_time formats",
			statusCode: http.StatusOK,
			responseBody: `{
				"events": [
					{
						"artist_name": "Test Artist",
						"event_name": "HH:MM Format",
						"venue": "Test Hall",
						"local_date": "2026-03-01",
						"start_time": "18:00",
						"source_url": "https://example.com/hh-mm"
					},
					{
						"artist_name": "Test Artist",
						"event_name": "Empty Start Time",
						"venue": "Test Hall",
						"local_date": "2026-03-02",
						"start_time": "",
						"source_url": "https://example.com/empty"
					},
					{
						"artist_name": "Test Artist",
						"event_name": "Valid RFC3339",
						"venue": "Test Hall",
						"local_date": "2026-03-03",
						"start_time": "2026-03-03T19:00:00+09:00",
						"source_url": "https://example.com/valid"
					}
				]
			}`,
			want: []*entity.ScrapedConcert{
				{
					Title:          "HH:MM Format",
					ListedVenueName:      "Test Hall",
					LocalEventDate: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
					StartTime:      nil, // Invalid HH:MM results in nil
					SourceURL:      "https://example.com/hh-mm",
				},
				{
					Title:          "Empty Start Time",
					ListedVenueName:      "Test Hall",
					LocalEventDate: time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC),
					StartTime:      nil, // Empty results in nil
					SourceURL:      "https://example.com/empty",
				},
				{
					Title:          "Valid RFC3339",
					ListedVenueName:      "Test Hall",
					LocalEventDate: time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC),
					StartTime:      ptr(time.Date(2026, 3, 3, 19, 0, 0, 0, time.FixedZone("", 9*60*60))),
					SourceURL:      "https://example.com/valid",
				},
			},
			wantErr: nil,
		},
		{
			name:       "api error - 500",
			statusCode: http.StatusInternalServerError,
			responseBody: `{
				"error": {
					"code": 500,
					"message": "Internal Server Error",
					"status": "INTERNAL"
				}
			}`,
			want:    nil,
			wantErr: apperr.ErrUnknown, // 500 from http is mapped to Unknown by errors.go switch default
		},
		{
			name:       "api error - 400",
			statusCode: http.StatusBadRequest,
			responseBody: `{
				"error": {
					"code": 400,
					"message": "Bad Request",
					"status": "INVALID_ARGUMENT"
				}
			}`,
			want:    nil,
			wantErr: apperr.ErrInvalidArgument, // 400 -> InvalidArgument
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				if tt.statusCode != http.StatusOK {
					if _, err := w.Write([]byte(tt.responseBody)); err != nil {
						t.Errorf("failed to write response body: %v", err)
					}
					return
				}

				// Construct mock Gemini response for success 200
				fullResponse := fmt.Sprintf(`{
					"candidates": [
						{
							"content": {
								"parts": [
									{
										"text": %s
									}
								]
							},
							"groundingMetadata": {
								"webSearchQueries": ["test query"]
							}
						}
					],
					"usageMetadata": {
						"promptTokenCount": 10,
						"candidatesTokenCount": 10,
						"totalTokenCount": 20
					}
				}`, strconv.Quote(tt.responseBody))

				w.Header().Set("Content-Type", "application/json")
				if _, err := w.Write([]byte(fullResponse)); err != nil {
					t.Errorf("failed to write response body: %v", err)
				}
			}))
			defer ts.Close()

			httpClient := &http.Client{
				Transport: &rewriteTransport{URL: ts.URL},
			}

			s, err := gemini.NewConcertSearcher(ctx, gemini.Config{
				ProjectID: "test",
				Location:  "us-central1",
				ModelName: "gemini-pro",
			}, httpClient, logger)
			require.NoError(t, err)

			got, err := s.Search(ctx, artist, officialSite, from)
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)

			require.Equal(t, len(tt.want), len(got))
			for i := range tt.want {
				assert.Equal(t, tt.want[i].Title, got[i].Title, "Title mismatch at index %d", i)
				assert.Equal(t, tt.want[i].ListedVenueName, got[i].ListedVenueName, "ListedVenueName mismatch at index %d", i)
				if tt.want[i].AdminArea == nil {
					assert.Nil(t, got[i].AdminArea, "AdminArea should be nil at index %d", i)
				} else {
					require.NotNil(t, got[i].AdminArea, "AdminArea should not be nil at index %d", i)
					assert.Equal(t, *tt.want[i].AdminArea, *got[i].AdminArea, "AdminArea mismatch at index %d", i)
				}
				assert.True(t, tt.want[i].LocalEventDate.Equal(got[i].LocalEventDate), "LocalEventDate mismatch at index %d", i)
				if tt.want[i].StartTime == nil {
					assert.Nil(t, got[i].StartTime, "StartTime should be nil at index %d", i)
				} else {
					require.NotNil(t, got[i].StartTime, "StartTime should not be nil at index %d", i)
					assert.True(t, tt.want[i].StartTime.Equal(*got[i].StartTime), "StartTime mismatch at index %d", i)
				}
				if tt.want[i].OpenTime == nil {
					assert.Nil(t, got[i].OpenTime, "OpenTime should be nil at index %d", i)
				} else {
					require.NotNil(t, got[i].OpenTime, "OpenTime should not be nil at index %d", i)
					assert.True(t, tt.want[i].OpenTime.Equal(*got[i].OpenTime), "OpenTime mismatch at index %d", i)
				}
				assert.Equal(t, tt.want[i].SourceURL, got[i].SourceURL, "SourceURL mismatch at index %d", i)
			}
		})
	}
}
