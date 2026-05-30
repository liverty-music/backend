package event_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

// makeAnalyticsMsg builds a watermill message with a JSON-encoded
// UserCreatedData payload (the only subject the consumer subscribes to
// in this batch).
func makeAnalyticsMsg(t *testing.T, data entity.UserCreatedData) *message.Message {
	t.Helper()
	payload, err := json.Marshal(data)
	require.NoError(t, err)
	return message.NewMessage("test-id", payload)
}

// TestAnalyticsConsumer_HandleUserCreated covers the routing/validation
// surface of the USER.created handler. The actual PostHog network
// interaction is exercised under internal/infrastructure/analytics/posthog.
func TestAnalyticsConsumer_HandleUserCreated(t *testing.T) {
	t.Parallel()

	const validUserID = "11111111-2222-3333-4444-555555555555"

	type args struct {
		data entity.UserCreatedData
	}
	type want struct {
		err              error
		expectEnqueueErr error // when set, the mock Enqueue returns this
		expectEnqueue    bool  // whether Enqueue is expected to be called
	}
	tests := []struct {
		name      string
		args      args
		want      want
		nilClient bool
	}{
		{
			name: "forwards user.created when client + UserID present",
			args: args{data: entity.UserCreatedData{
				UserID:     validUserID,
				ExternalID: "zitadel-sub-abc",
				Email:      "alice@example.com",
			}},
			want: want{expectEnqueue: true},
		},
		{
			name: "skips forward when client is nil (local dev)",
			args: args{data: entity.UserCreatedData{
				UserID:     validUserID,
				ExternalID: "zitadel-sub-abc",
				Email:      "alice@example.com",
			}},
			want:      want{expectEnqueue: false},
			nilClient: true,
		},
		{
			name: "skips forward when UserID is empty",
			args: args{data: entity.UserCreatedData{
				UserID:     "",
				ExternalID: "zitadel-sub-abc",
				Email:      "alice@example.com",
			}},
			want: want{expectEnqueue: false},
		},
		{
			name: "wraps Enqueue error as apperr.ErrInternal",
			args: args{data: entity.UserCreatedData{
				UserID: validUserID,
			}},
			want: want{
				expectEnqueue:    true,
				expectEnqueueErr: errors.New("queue full"),
				err:              apperr.ErrInternal,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var client usecase.AnalyticsClient
			var clientMock *ucmocks.MockAnalyticsClient
			if !tt.nilClient {
				clientMock = ucmocks.NewMockAnalyticsClient(t)
				client = clientMock
				if tt.want.expectEnqueue {
					clientMock.EXPECT().
						Enqueue(
							mock.Anything,
							tt.args.data.UserID,
							usecase.EventUserCreated,
							mock.MatchedBy(func(props usecase.AnalyticsProperties) bool {
								v, ok := props["signup_month"].(string)
								if !ok {
									return false
								}
								// signup_month must be YYYY-MM at the test instant.
								_, err := time.Parse("2006-01", v)
								return err == nil
							}),
						).
						Return(tt.want.expectEnqueueErr).
						Once()
				}
			}

			handler := event.NewAnalyticsConsumer(client, newTestLogger(t))
			err := handler.HandleUserCreated(makeAnalyticsMsg(t, tt.args.data))

			if tt.want.err != nil {
				assert.ErrorIs(t, err, tt.want.err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// TestAnalyticsConsumer_HandleUserCreated_BadPayload covers the
// CloudEvent decode failure path separately because constructing an
// invalid JSON payload with the same table shape would obscure the
// happy-path readability.
func TestAnalyticsConsumer_HandleUserCreated_BadPayload(t *testing.T) {
	t.Parallel()

	clientMock := ucmocks.NewMockAnalyticsClient(t)
	handler := event.NewAnalyticsConsumer(clientMock, newTestLogger(t))

	msg := message.NewMessage("test-id", []byte("not-valid-json"))
	err := handler.HandleUserCreated(msg)

	assert.ErrorIs(t, err, apperr.ErrInternal)
	// The mock has no EXPECT() calls — assertExpectations on Cleanup
	// would fail if Enqueue had been invoked.
}
