package gemini_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
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

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(t.URL)
	req.URL.Scheme = u.Scheme
	req.URL.Host = u.Host
	return http.DefaultTransport.RoundTrip(req)
}

func TestConcertSearcher_Search(t *testing.T) {
	t.Parallel()

	logger, _ := logging.New()
	ctx := context.Background()
	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}

	tests := []struct {
		name         string
		responseBody string
		statusCode   int
		finishReason string
		want         []*entity.ScrapedConcert
		wantErr      error
	}{
		{
			name:       "success - single standalone event",
			statusCode: http.StatusOK,
			responseBody: `{
				"tours": [],
				"standalones": [
					{
						"event_title": "Test One-Off 2026",
						"venue": "Test Hall",
						"local_date": "2026-03-01",
						"start_time": "2026-03-01T18:00:00Z",
						"source_url": "https://example.com/test"
					}
				]
			}`,
			want: []*entity.ScrapedConcert{
				{
					Title:           "Test One-Off 2026",
					ListedVenueName: "Test Hall",
					LocalDate:       time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
					StartTime:       time.Date(2026, 3, 1, 18, 0, 0, 0, time.UTC),
					SourceURL:       "https://example.com/test",
				},
			},
			wantErr: nil,
		},
		{
			name:       "success - tour with multiple dates flattens to multiple concerts",
			statusCode: http.StatusOK,
			responseBody: `{
				"tours": [
					{
						"tour_title": "Test Tour 2026",
						"events": [
							{
								"venue": "Hall A",
								"local_date": "2026-03-01",
								"start_time": "2026-03-01T18:00:00Z",
								"source_url": "https://example.com/test/a"
							},
							{
								"venue": "Hall B",
								"local_date": "2026-03-05",
								"start_time": "2026-03-05T19:00:00Z",
								"source_url": "https://example.com/test/b"
							}
						]
					}
				],
				"standalones": []
			}`,
			want: []*entity.ScrapedConcert{
				{
					Title:           "Test Tour 2026",
					ListedVenueName: "Hall A",
					LocalDate:       time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
					StartTime:       time.Date(2026, 3, 1, 18, 0, 0, 0, time.UTC),
					SourceURL:       "https://example.com/test/a",
				},
				{
					Title:           "Test Tour 2026",
					ListedVenueName: "Hall B",
					LocalDate:       time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC),
					StartTime:       time.Date(2026, 3, 5, 19, 0, 0, 0, time.UTC),
					SourceURL:       "https://example.com/test/b",
				},
			},
			wantErr: nil,
		},
		{
			name:       "success - event with admin_area",
			statusCode: http.StatusOK,
			responseBody: `{
				"tours": [],
				"standalones": [
					{
						"event_title": "Nagoya Concert",
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
					AdminArea:       new("JP-23"),
					LocalDate:       time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
					StartTime:       time.Date(2026, 3, 15, 18, 0, 0, 0, time.FixedZone("", 9*60*60)),
					SourceURL:       "https://example.com/nagoya",
				},
			},
			wantErr: nil,
		},
		{
			name:       "success - event with empty admin_area returns nil",
			statusCode: http.StatusOK,
			responseBody: `{
				"tours": [],
				"standalones": [
					{
						"event_title": "Unknown Venue Concert",
						"venue": "Some Venue",
						"admin_area": "",
						"local_date": "2026-03-20",
						"start_time": "",
						"source_url": "https://example.com/unknown"
					}
				]
			}`,
			want: []*entity.ScrapedConcert{
				{
					Title:           "Unknown Venue Concert",
					ListedVenueName: "Some Venue",
					AdminArea:       nil,
					LocalDate:       time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC),
					SourceURL:       "https://example.com/unknown",
				},
			},
			wantErr: nil,
		},
		{
			name:       "success - multiple standalone events without deduplication",
			statusCode: http.StatusOK,
			responseBody: `{
				"tours": [],
				"standalones": [
					{
						"event_title": "Test One-Off A",
						"venue": "Test Hall",
						"local_date": "2026-03-01",
						"start_time": "2026-03-01T18:00:00Z",
						"source_url": "https://example.com/test"
					},
					{
						"event_title": "Test One-Off A",
						"venue": "Test Hall",
						"local_date": "2026-03-01",
						"start_time": "2026-03-01T18:00:00Z",
						"source_url": "https://example.com/test-dup"
					}
				]
			}`,
			want: []*entity.ScrapedConcert{
				{
					Title:           "Test One-Off A",
					ListedVenueName: "Test Hall",
					LocalDate:       time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
					StartTime:       time.Date(2026, 3, 1, 18, 0, 0, 0, time.UTC),
					SourceURL:       "https://example.com/test",
				},
				{
					Title:           "Test One-Off A",
					ListedVenueName: "Test Hall",
					LocalDate:       time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
					StartTime:       time.Date(2026, 3, 1, 18, 0, 0, 0, time.UTC),
					SourceURL:       "https://example.com/test-dup",
				},
			},
			wantErr: nil,
		},
		{
			name:       "success - no filter excluded in searcher",
			statusCode: http.StatusOK,
			responseBody: `{
				"tours": [],
				"standalones": [
					{
						"event_title": "New Event",
						"venue": "Test Hall",
						"local_date": "2026-04-01",
						"start_time": "2026-04-01T19:00:00Z",
						"source_url": "https://example.com/new"
					}
				]
			}`,
			want: []*entity.ScrapedConcert{
				{
					Title:           "New Event",
					ListedVenueName: "Test Hall",
					LocalDate:       time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
					StartTime:       time.Date(2026, 4, 1, 19, 0, 0, 0, time.UTC),
					SourceURL:       "https://example.com/new",
				},
			},
			wantErr: nil,
		},
		{
			name:       "success - filter past events",
			statusCode: http.StatusOK,
			responseBody: `{
				"tours": [],
				"standalones": [
					{
						"event_title": "Past Event",
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
			name:         "error - invalid json is permanent (not retried)",
			statusCode:   http.StatusOK,
			responseBody: `invalid json`,
			want:         nil,
			wantErr:      gemini.ErrInvalidJSON, // permanent: truncated output from maxOutputTokens exhaustion
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
				"tours": [],
				"standalones": [
					{
						"event_title": "Invalid Date",
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
				"tours": [],
				"standalones": [
					{
						"event_title": "HH:MM Format",
						"venue": "Test Hall",
						"local_date": "2026-03-01",
						"start_time": "18:00",
						"source_url": "https://example.com/hh-mm"
					},
					{
						"event_title": "Empty Start Time",
						"venue": "Test Hall",
						"local_date": "2026-03-02",
						"start_time": "",
						"source_url": "https://example.com/empty"
					},
					{
						"event_title": "Valid RFC3339",
						"venue": "Test Hall",
						"local_date": "2026-03-03",
						"start_time": "2026-03-03T19:00:00+09:00",
						"source_url": "https://example.com/valid"
					}
				]
			}`,
			want: []*entity.ScrapedConcert{
				{
					Title:           "HH:MM Format",
					ListedVenueName: "Test Hall",
					LocalDate:       time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
					// Invalid HH:MM results in zero StartTime
					SourceURL: "https://example.com/hh-mm",
				},
				{
					Title:           "Empty Start Time",
					ListedVenueName: "Test Hall",
					LocalDate:       time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC),
					// Empty results in zero StartTime
					SourceURL: "https://example.com/empty",
				},
				{
					Title:           "Valid RFC3339",
					ListedVenueName: "Test Hall",
					LocalDate:       time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC),
					StartTime:       time.Date(2026, 3, 3, 19, 0, 0, 0, time.FixedZone("", 9*60*60)),
					SourceURL:       "https://example.com/valid",
				},
			},
			wantErr: nil,
		},
		{
			name:       "success - literal null string start_time treated as nil",
			statusCode: http.StatusOK,
			responseBody: `{
				"tours": [],
				"standalones": [
					{
						"event_title": "Null Start Time Concert",
						"venue": "Test Hall",
						"local_date": "2026-03-10",
						"start_time": "null",
						"open_time": "null",
						"source_url": "https://example.com/null-time"
					}
				]
			}`,
			want: []*entity.ScrapedConcert{
				{
					Title:           "Null Start Time Concert",
					ListedVenueName: "Test Hall",
					LocalDate:       time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC),
					SourceURL:       "https://example.com/null-time",
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
		{
			name:         "resilience - MAX_TOKENS returns nil after retries",
			statusCode:   http.StatusOK,
			finishReason: "MAX_TOKENS",
			responseBody: `{"tours": [], "standalones": [{"event_title": "Trunca`,
			want:         nil,
			wantErr:      nil, // non-STOP finish reason retried then returns empty
		},
		{
			name:         "resilience - SAFETY finish reason returns nil",
			statusCode:   http.StatusOK,
			finishReason: "SAFETY",
			responseBody: `{}`,
			want:         nil,
			wantErr:      nil, // non-STOP finish reason retried then returns empty
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
				finishReason := tt.finishReason
				if finishReason == "" {
					finishReason = "STOP"
				}
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
							"finishReason": %q,
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
				}`, strconv.Quote(tt.responseBody), finishReason)

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
			}, httpClient, false, logger)
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
				assert.True(t, tt.want[i].LocalDate.Equal(got[i].LocalDate), "LocalDate mismatch at index %d", i)
				assert.True(t, tt.want[i].StartTime.Equal(got[i].StartTime), "StartTime mismatch at index %d", i)
				assert.True(t, tt.want[i].OpenTime.Equal(got[i].OpenTime), "OpenTime mismatch at index %d", i)
				assert.Equal(t, tt.want[i].SourceURL, got[i].SourceURL, "SourceURL mismatch at index %d", i)
			}
		})
	}
}

func TestConcertSearcher_Search_NoOfficialSite(t *testing.T) {
	t.Parallel()

	logger, _ := logging.New()
	ctx := context.Background()
	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}

	responseBody := `{
		"tours": [],
		"standalones": [
			{
				"event_title": "Nameless Tour",
				"venue": "Test Hall",
				"local_date": "2026-03-01",
				"start_time": "2026-03-01T18:00:00Z",
				"source_url": "https://example.com/nameless"
			}
		]
	}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		}`, strconv.Quote(responseBody))

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
	}, httpClient, false, logger)
	require.NoError(t, err)

	// Pass nil officialSite — should use the fallback prompt and still return concerts
	got, err := s.Search(ctx, artist, nil, from)

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Nameless Tour", got[0].Title)
	assert.Equal(t, "Test Hall", got[0].ListedVenueName)
	assert.Equal(t, "https://example.com/nameless", got[0].SourceURL)
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

func TestConcertSearcher_Search_InvalidJSON_Permanent(t *testing.T) {
	logger, _ := logging.New()
	ctx := context.Background()
	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}

	var callCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// Return truncated JSON — permanent error, should not be retried.
		_, _ = w.Write([]byte(geminiResponse(`{"tours": [], "standalones": [{"event_title": "Test`, "STOP")))
	}))
	defer ts.Close()

	s, err := gemini.NewConcertSearcher(ctx, gemini.Config{
		ProjectID: "test",
		Location:  "us-central1",
		ModelName: "gemini-pro",
	}, &http.Client{Transport: &rewriteTransport{URL: ts.URL}}, false, logger)
	require.NoError(t, err)

	got, err := s.Search(ctx, artist, officialSite, from)

	assert.Nil(t, got)
	require.Error(t, err)
	assert.ErrorIs(t, err, gemini.ErrInvalidJSON)
	assert.Equal(t, int32(1), callCount.Load(), "invalid JSON should not retry (permanent error)")
}

func TestConcertSearcher_Search_StructuralMismatch(t *testing.T) {
	logger, _ := logging.New()
	ctx := context.Background()
	from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	artist := &entity.Artist{Name: "Test Artist"}
	officialSite := &entity.OfficialSite{URL: "https://example.com"}

	// Valid JSON but wrong structure: "tours" is a string instead of an array.
	wrongStructure := `{"tours": "not an array", "standalones": []}`

	var callCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(geminiResponse(wrongStructure, "STOP")))
	}))
	defer ts.Close()

	s, err := gemini.NewConcertSearcher(ctx, gemini.Config{
		ProjectID: "test",
		Location:  "us-central1",
		ModelName: "gemini-pro",
	}, &http.Client{Transport: &rewriteTransport{URL: ts.URL}}, false, logger)
	require.NoError(t, err)

	got, err := s.Search(ctx, artist, officialSite, from)

	assert.Nil(t, got)
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrInternal)
	assert.Equal(t, int32(1), callCount.Load(), "structural mismatch should not retry")
}

func TestConcertSearcher_Search_ConfigHonored(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		temperature       float32
		thinkingLevel     string
		wantThinkingLevel string // empty string means: thinkingConfig should be absent
	}{
		{name: "temperature 0.2 + thinking medium", temperature: 0.2, thinkingLevel: "medium", wantThinkingLevel: "MEDIUM"},
		{name: "temperature 0.5 + thinking high", temperature: 0.5, thinkingLevel: "high", wantThinkingLevel: "HIGH"},
		{name: "temperature 1.0 + no thinking level", temperature: 1.0, thinkingLevel: "", wantThinkingLevel: ""},
		{name: "lowercase low maps to LOW", temperature: 0.3, thinkingLevel: "low", wantThinkingLevel: "LOW"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			logger, _ := logging.New()
			ctx := context.Background()
			from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
			artist := &entity.Artist{Name: "Test Artist"}
			officialSite := &entity.OfficialSite{URL: "https://example.com"}

			var capturedBody map[string]any
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&capturedBody)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(geminiResponse(`{"tours":[],"standalones":[]}`, "STOP")))
			}))
			defer ts.Close()

			s, err := gemini.NewConcertSearcher(ctx, gemini.Config{
				ProjectID:     "test",
				Location:      "us-central1",
				ModelName:     "gemini-pro",
				Temperature:   tt.temperature,
				ThinkingLevel: tt.thinkingLevel,
			}, &http.Client{Transport: &rewriteTransport{URL: ts.URL}}, false, logger)
			require.NoError(t, err)

			_, err = s.Search(ctx, artist, officialSite, from)
			require.NoError(t, err)
			require.NotNil(t, capturedBody, "request body must be captured")

			genCfg, _ := capturedBody["generationConfig"].(map[string]any)
			require.NotNil(t, genCfg, "generationConfig must be present in the request")

			temp, _ := genCfg["temperature"].(float64)
			assert.InDelta(t, float64(tt.temperature), temp, 1e-6, "temperature in request must equal Config.Temperature")

			// responseJsonSchema is sent (not responseSchema) and additionalProperties is wired through.
			assert.Nil(t, genCfg["responseSchema"], "responseSchema must NOT be set when using responseJsonSchema")
			respJSONSchema, _ := genCfg["responseJsonSchema"].(map[string]any)
			require.NotNil(t, respJSONSchema, "responseJsonSchema must be set")
			assert.Equal(t, false, respJSONSchema["additionalProperties"], "top-level additionalProperties must be false")

			thinkingCfg, _ := genCfg["thinkingConfig"].(map[string]any)
			if tt.wantThinkingLevel == "" {
				assert.Nil(t, thinkingCfg, "thinkingConfig must be omitted when ThinkingLevel is empty")
			} else {
				require.NotNil(t, thinkingCfg, "thinkingConfig must be present when ThinkingLevel is set")
				assert.Equal(t, tt.wantThinkingLevel, thinkingCfg["thinkingLevel"])
			}
		})
	}
}
