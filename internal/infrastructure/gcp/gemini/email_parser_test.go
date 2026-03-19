package gemini_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmailParser_Parse(t *testing.T) {
	t.Parallel()
	logger, _ := logging.New()
	ctx := context.Background()

	type args struct {
		emailType entity.TicketEmailType
	}

	tests := []struct {
		name         string
		args         args
		responseBody string
		statusCode   int
		want         *entity.ParsedEmailData
		wantErr      error
	}{
		{
			name:       "lottery info - all fields present",
			args:       args{emailType: entity.TicketEmailTypeLotteryInfo},
			statusCode: http.StatusOK,
			responseBody: `{
				"lottery_start": "2026-04-01T10:00:00+09:00",
				"lottery_end": "2026-04-10T23:59:00+09:00",
				"application_url": "https://eplus.jp/apply/12345"
			}`,
			want: &entity.ParsedEmailData{
				LotteryStart:   ptrStr("2026-04-01T10:00:00+09:00"),
				LotteryEnd:     ptrStr("2026-04-10T23:59:00+09:00"),
				ApplicationURL: ptrStr("https://eplus.jp/apply/12345"),
			},
		},
		{
			name:       "lottery info - partial fields (only start and URL)",
			args:       args{emailType: entity.TicketEmailTypeLotteryInfo},
			statusCode: http.StatusOK,
			responseBody: `{
				"lottery_start": "2026-05-01T12:00:00+09:00",
				"lottery_end": null,
				"application_url": "https://pia.jp/lottery/98765"
			}`,
			want: &entity.ParsedEmailData{
				LotteryStart:   ptrStr("2026-05-01T12:00:00+09:00"),
				ApplicationURL: ptrStr("https://pia.jp/lottery/98765"),
			},
		},
		{
			name:       "lottery info - all null fields",
			args:       args{emailType: entity.TicketEmailTypeLotteryInfo},
			statusCode: http.StatusOK,
			responseBody: `{
				"lottery_start": null,
				"lottery_end": null,
				"application_url": null
			}`,
			want: &entity.ParsedEmailData{},
		},
		{
			name:       "lottery result - won with unpaid status and deadline",
			args:       args{emailType: entity.TicketEmailTypeLotteryResult},
			statusCode: http.StatusOK,
			responseBody: `{
				"lottery_result": "won",
				"payment_status": "unpaid",
				"payment_deadline": "2026-04-20T23:59:00+09:00"
			}`,
			want: &entity.ParsedEmailData{
				LotteryResult:   ptrStr("won"),
				PaymentStatus:   ptrStr("unpaid"),
				PaymentDeadline: ptrStr("2026-04-20T23:59:00+09:00"),
			},
		},
		{
			name:       "lottery result - won with paid status (auto-charge)",
			args:       args{emailType: entity.TicketEmailTypeLotteryResult},
			statusCode: http.StatusOK,
			responseBody: `{
				"lottery_result": "won",
				"payment_status": "paid",
				"payment_deadline": null
			}`,
			want: &entity.ParsedEmailData{
				LotteryResult: ptrStr("won"),
				PaymentStatus: ptrStr("paid"),
			},
		},
		{
			name:       "lottery result - lost",
			args:       args{emailType: entity.TicketEmailTypeLotteryResult},
			statusCode: http.StatusOK,
			responseBody: `{
				"lottery_result": "lost",
				"payment_status": null,
				"payment_deadline": null
			}`,
			want: &entity.ParsedEmailData{
				LotteryResult: ptrStr("lost"),
			},
		},
		{
			name:         "error - invalid email type",
			args:         args{emailType: entity.TicketEmailType(99)},
			statusCode:   http.StatusOK,
			responseBody: `{}`,
			wantErr:      apperr.ErrInvalidArgument,
		},
		{
			name:         "error - empty response from Gemini",
			args:         args{emailType: entity.TicketEmailTypeLotteryInfo},
			statusCode:   http.StatusOK,
			responseBody: ``,
			wantErr:      apperr.ErrInternal,
		},
		{
			name:         "error - invalid JSON response",
			args:         args{emailType: entity.TicketEmailTypeLotteryInfo},
			statusCode:   http.StatusOK,
			responseBody: `not valid json at all`,
			wantErr:      apperr.ErrInternal,
		},
		{
			name:         "error - API returns 500",
			args:         args{emailType: entity.TicketEmailTypeLotteryInfo},
			statusCode:   http.StatusInternalServerError,
			responseBody: `{"error":{"code":500,"message":"Internal Server Error","status":"INTERNAL"}}`,
			wantErr:      apperr.ErrInternal,
		},
		{
			name:         "error - API returns 429 rate limit",
			args:         args{emailType: entity.TicketEmailTypeLotteryResult},
			statusCode:   http.StatusTooManyRequests,
			responseBody: `{"error":{"code":429,"message":"Rate limit exceeded","status":"RESOURCE_EXHAUSTED"}}`,
			wantErr:      apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)

				if tt.statusCode != http.StatusOK {
					if _, err := w.Write([]byte(tt.responseBody)); err != nil {
						t.Errorf("failed to write error response: %v", err)
					}
					return
				}

				// Wrap response body in Gemini API response envelope
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
							"finishReason": "STOP"
						}
					],
					"usageMetadata": {
						"promptTokenCount": 100,
						"candidatesTokenCount": 50,
						"totalTokenCount": 150
					}
				}`, strconv.Quote(tt.responseBody))

				if _, err := w.Write([]byte(fullResponse)); err != nil {
					t.Errorf("failed to write response: %v", err)
				}
			}))
			defer ts.Close()

			httpClient := &http.Client{
				Transport: &rewriteTransport{URL: ts.URL},
			}

			parser, err := gemini.NewEmailParser(ctx, gemini.EmailParserConfig{
				ProjectID: "test-project",
				Location:  "us-central1",
				ModelName: "gemini-2.0-flash",
			}, httpClient, logger)
			require.NoError(t, err)

			got, err := parser.Parse(ctx, "テストメール本文", tt.args.emailType)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tt.want.LotteryStart, got.LotteryStart)
			assert.Equal(t, tt.want.LotteryEnd, got.LotteryEnd)
			assert.Equal(t, tt.want.ApplicationURL, got.ApplicationURL)
			assert.Equal(t, tt.want.LotteryResult, got.LotteryResult)
			assert.Equal(t, tt.want.PaymentStatus, got.PaymentStatus)
			assert.Equal(t, tt.want.PaymentDeadline, got.PaymentDeadline)
		})
	}
}
