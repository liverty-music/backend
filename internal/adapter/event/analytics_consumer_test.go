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
// payload. The ce_time metadata is set 1 second in the past so
// recordLag exercises a deterministic non-zero sample.
func makeAnalyticsMsg(t *testing.T, data any) *message.Message {
	t.Helper()
	payload, err := json.Marshal(data)
	require.NoError(t, err)
	msg := message.NewMessage("test-id", payload)
	msg.Metadata.Set("ce_time", time.Now().UTC().Add(-time.Second).Format(time.RFC3339))
	return msg
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
		expectStatus     string
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
			want: want{expectEnqueue: true, expectStatus: "forwarded"},
		},
		{
			name: "skips forward when client is nil (local dev)",
			args: args{data: entity.UserCreatedData{
				UserID:     validUserID,
				ExternalID: "zitadel-sub-abc",
				Email:      "alice@example.com",
			}},
			want:      want{expectEnqueue: false, expectStatus: "skipped_nil_client"},
			nilClient: true,
		},
		{
			name: "skips forward when UserID is empty",
			args: args{data: entity.UserCreatedData{
				UserID:     "",
				ExternalID: "zitadel-sub-abc",
				Email:      "alice@example.com",
			}},
			want: want{expectEnqueue: false, expectStatus: "skipped_empty_user_id"},
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
				expectStatus:     "enqueue_error",
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

			metricsMock := ucmocks.NewMockAnalyticsConsumerMetrics(t)
			metricsMock.EXPECT().
				RecordMessage(mock.Anything, tt.want.expectStatus).
				Once()
			metricsMock.EXPECT().
				RecordLag(mock.Anything, mock.MatchedBy(func(s float64) bool { return s >= 0 })).
				Once()

			handler := event.NewAnalyticsConsumer(client, metricsMock, newTestLogger(t))
			err := handler.HandleUserCreated(makeAnalyticsMsg(t, tt.args.data))

			if tt.want.err != nil {
				assert.ErrorIs(t, err, tt.want.err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// TestAnalyticsConsumer_HandleUserPreferredLanguageUpdated covers
// USER.preferred_language_updated → account.preferred_language.updated
// routing. Properties from_locale and to_locale travel verbatim.
func TestAnalyticsConsumer_HandleUserPreferredLanguageUpdated(t *testing.T) {
	t.Parallel()

	const validUserID = "11111111-2222-3333-4444-555555555555"

	type args struct {
		data entity.UserPreferredLanguageUpdatedData
	}
	type want struct {
		err              error
		expectEnqueueErr error
		expectEnqueue    bool
		expectStatus     string
	}
	tests := []struct {
		name      string
		args      args
		want      want
		nilClient bool
	}{
		{
			name: "forwards with both locales when client + UserID present",
			args: args{data: entity.UserPreferredLanguageUpdatedData{
				UserID:     validUserID,
				FromLocale: "ja",
				ToLocale:   "en",
			}},
			want: want{expectEnqueue: true, expectStatus: "forwarded"},
		},
		{
			name: "forwards with empty from_locale when prior was unset",
			args: args{data: entity.UserPreferredLanguageUpdatedData{
				UserID:     validUserID,
				FromLocale: "",
				ToLocale:   "ja",
			}},
			want: want{expectEnqueue: true, expectStatus: "forwarded"},
		},
		{
			name: "skips forward when client is nil",
			args: args{data: entity.UserPreferredLanguageUpdatedData{
				UserID:   validUserID,
				ToLocale: "ja",
			}},
			want:      want{expectEnqueue: false, expectStatus: "skipped_nil_client"},
			nilClient: true,
		},
		{
			name: "skips forward when UserID is empty",
			args: args{data: entity.UserPreferredLanguageUpdatedData{
				UserID:   "",
				ToLocale: "ja",
			}},
			want: want{expectEnqueue: false, expectStatus: "skipped_empty_user_id"},
		},
		{
			name: "wraps Enqueue error as apperr.ErrInternal",
			args: args{data: entity.UserPreferredLanguageUpdatedData{
				UserID:   validUserID,
				ToLocale: "ja",
			}},
			want: want{
				expectEnqueue:    true,
				expectEnqueueErr: errors.New("queue full"),
				err:              apperr.ErrInternal,
				expectStatus:     "enqueue_error",
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
							usecase.EventAccountPreferredLanguageUpdated,
							mock.MatchedBy(func(props usecase.AnalyticsProperties) bool {
								return props["from_locale"] == tt.args.data.FromLocale &&
									props["to_locale"] == tt.args.data.ToLocale
							}),
						).
						Return(tt.want.expectEnqueueErr).
						Once()
				}
			}

			metricsMock := ucmocks.NewMockAnalyticsConsumerMetrics(t)
			metricsMock.EXPECT().
				RecordMessage(mock.Anything, tt.want.expectStatus).
				Once()
			metricsMock.EXPECT().
				RecordLag(mock.Anything, mock.MatchedBy(func(s float64) bool { return s >= 0 })).
				Once()

			handler := event.NewAnalyticsConsumer(client, metricsMock, newTestLogger(t))
			err := handler.HandleUserPreferredLanguageUpdated(makeAnalyticsMsg(t, tt.args.data))

			if tt.want.err != nil {
				assert.ErrorIs(t, err, tt.want.err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// TestAnalyticsConsumer_HandleArtistFollowed covers ARTIST.followed →
// artist.follow.completed routing. The catalogue's optional `source`
// property is FE-only and not present on the backend payload.
func TestAnalyticsConsumer_HandleArtistFollowed(t *testing.T) {
	t.Parallel()

	const (
		validUserID   = "11111111-2222-3333-4444-555555555555"
		validArtistID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	)

	type args struct {
		data entity.ArtistFollowedData
	}
	type want struct {
		err              error
		expectEnqueueErr error
		expectEnqueue    bool
		expectStatus     string
	}
	tests := []struct {
		name      string
		args      args
		want      want
		nilClient bool
	}{
		{
			name: "forwards artist.follow.completed with artist_id property",
			args: args{data: entity.ArtistFollowedData{
				UserID:   validUserID,
				ArtistID: validArtistID,
			}},
			want: want{expectEnqueue: true, expectStatus: "forwarded"},
		},
		{
			name: "skips forward when client is nil",
			args: args{data: entity.ArtistFollowedData{
				UserID:   validUserID,
				ArtistID: validArtistID,
			}},
			want:      want{expectEnqueue: false, expectStatus: "skipped_nil_client"},
			nilClient: true,
		},
		{
			name: "skips forward when UserID is empty",
			args: args{data: entity.ArtistFollowedData{
				UserID:   "",
				ArtistID: validArtistID,
			}},
			want: want{expectEnqueue: false, expectStatus: "skipped_empty_user_id"},
		},
		{
			name: "wraps Enqueue error as apperr.ErrInternal",
			args: args{data: entity.ArtistFollowedData{
				UserID:   validUserID,
				ArtistID: validArtistID,
			}},
			want: want{
				expectEnqueue:    true,
				expectEnqueueErr: errors.New("queue full"),
				err:              apperr.ErrInternal,
				expectStatus:     "enqueue_error",
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
							usecase.EventArtistFollowCompleted,
							mock.MatchedBy(func(props usecase.AnalyticsProperties) bool {
								return props["artist_id"] == tt.args.data.ArtistID
							}),
						).
						Return(tt.want.expectEnqueueErr).
						Once()
				}
			}

			metricsMock := ucmocks.NewMockAnalyticsConsumerMetrics(t)
			metricsMock.EXPECT().
				RecordMessage(mock.Anything, tt.want.expectStatus).
				Once()
			metricsMock.EXPECT().
				RecordLag(mock.Anything, mock.MatchedBy(func(s float64) bool { return s >= 0 })).
				Once()

			handler := event.NewAnalyticsConsumer(client, metricsMock, newTestLogger(t))
			err := handler.HandleArtistFollowed(makeAnalyticsMsg(t, tt.args.data))

			if tt.want.err != nil {
				assert.ErrorIs(t, err, tt.want.err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// TestAnalyticsConsumer_HandleArtistUnfollowed mirrors the followed
// test for the negative engagement case (artist.unfollow.completed).
func TestAnalyticsConsumer_HandleArtistUnfollowed(t *testing.T) {
	t.Parallel()

	const (
		validUserID   = "11111111-2222-3333-4444-555555555555"
		validArtistID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	)

	type args struct {
		data entity.ArtistUnfollowedData
	}
	type want struct {
		err              error
		expectEnqueueErr error
		expectEnqueue    bool
		expectStatus     string
	}
	tests := []struct {
		name      string
		args      args
		want      want
		nilClient bool
	}{
		{
			name: "forwards artist.unfollow.completed with artist_id property",
			args: args{data: entity.ArtistUnfollowedData{
				UserID:   validUserID,
				ArtistID: validArtistID,
			}},
			want: want{expectEnqueue: true, expectStatus: "forwarded"},
		},
		{
			name: "skips forward when client is nil",
			args: args{data: entity.ArtistUnfollowedData{
				UserID:   validUserID,
				ArtistID: validArtistID,
			}},
			want:      want{expectEnqueue: false, expectStatus: "skipped_nil_client"},
			nilClient: true,
		},
		{
			name: "skips forward when UserID is empty",
			args: args{data: entity.ArtistUnfollowedData{
				UserID:   "",
				ArtistID: validArtistID,
			}},
			want: want{expectEnqueue: false, expectStatus: "skipped_empty_user_id"},
		},
		{
			name: "wraps Enqueue error as apperr.ErrInternal",
			args: args{data: entity.ArtistUnfollowedData{
				UserID:   validUserID,
				ArtistID: validArtistID,
			}},
			want: want{
				expectEnqueue:    true,
				expectEnqueueErr: errors.New("queue full"),
				err:              apperr.ErrInternal,
				expectStatus:     "enqueue_error",
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
							usecase.EventArtistUnfollowCompleted,
							mock.MatchedBy(func(props usecase.AnalyticsProperties) bool {
								return props["artist_id"] == tt.args.data.ArtistID
							}),
						).
						Return(tt.want.expectEnqueueErr).
						Once()
				}
			}

			metricsMock := ucmocks.NewMockAnalyticsConsumerMetrics(t)
			metricsMock.EXPECT().
				RecordMessage(mock.Anything, tt.want.expectStatus).
				Once()
			metricsMock.EXPECT().
				RecordLag(mock.Anything, mock.MatchedBy(func(s float64) bool { return s >= 0 })).
				Once()

			handler := event.NewAnalyticsConsumer(client, metricsMock, newTestLogger(t))
			err := handler.HandleArtistUnfollowed(makeAnalyticsMsg(t, tt.args.data))

			if tt.want.err != nil {
				assert.ErrorIs(t, err, tt.want.err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// TestAnalyticsConsumer_HandleNotificationSubscribed covers
// NOTIFICATION.subscribed → notification.subscribed routing.
// Property: device_type (the endpoint host classifier).
func TestAnalyticsConsumer_HandleNotificationSubscribed(t *testing.T) {
	t.Parallel()

	const validUserID = "11111111-2222-3333-4444-555555555555"

	type args struct {
		data entity.NotificationSubscribedData
	}
	type want struct {
		err              error
		expectEnqueueErr error
		expectEnqueue    bool
		expectStatus     string
	}
	tests := []struct {
		name      string
		args      args
		want      want
		nilClient bool
	}{
		{
			name: "forwards with device_type when client + UserID present",
			args: args{data: entity.NotificationSubscribedData{
				UserID:     validUserID,
				DeviceType: "android",
			}},
			want: want{expectEnqueue: true, expectStatus: "forwarded"},
		},
		{
			name: "skips forward when client is nil",
			args: args{data: entity.NotificationSubscribedData{
				UserID:     validUserID,
				DeviceType: "apple",
			}},
			want:      want{expectEnqueue: false, expectStatus: "skipped_nil_client"},
			nilClient: true,
		},
		{
			name: "skips forward when UserID is empty",
			args: args{data: entity.NotificationSubscribedData{
				UserID:     "",
				DeviceType: "android",
			}},
			want: want{expectEnqueue: false, expectStatus: "skipped_empty_user_id"},
		},
		{
			name: "wraps Enqueue error as apperr.ErrInternal",
			args: args{data: entity.NotificationSubscribedData{
				UserID:     validUserID,
				DeviceType: "other",
			}},
			want: want{
				expectEnqueue:    true,
				expectEnqueueErr: errors.New("queue full"),
				err:              apperr.ErrInternal,
				expectStatus:     "enqueue_error",
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
							usecase.EventNotificationSubscribed,
							mock.MatchedBy(func(props usecase.AnalyticsProperties) bool {
								return props["device_type"] == tt.args.data.DeviceType
							}),
						).
						Return(tt.want.expectEnqueueErr).
						Once()
				}
			}

			metricsMock := ucmocks.NewMockAnalyticsConsumerMetrics(t)
			metricsMock.EXPECT().
				RecordMessage(mock.Anything, tt.want.expectStatus).
				Once()
			metricsMock.EXPECT().
				RecordLag(mock.Anything, mock.MatchedBy(func(s float64) bool { return s >= 0 })).
				Once()

			handler := event.NewAnalyticsConsumer(client, metricsMock, newTestLogger(t))
			err := handler.HandleNotificationSubscribed(makeAnalyticsMsg(t, tt.args.data))

			if tt.want.err != nil {
				assert.ErrorIs(t, err, tt.want.err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// TestAnalyticsConsumer_HandleEntryZkProofVerified covers
// ENTRY.zk_proof_verified routing. Distinct_id is the nullifier hash
// hex (anonymous per ZK guarantee); event_id is a property.
func TestAnalyticsConsumer_HandleEntryZkProofVerified(t *testing.T) {
	t.Parallel()

	const (
		validNullifier = "deadbeefcafebabe1234567890abcdef"
		validEventID   = "550e8400-e29b-41d4-a716-446655440000"
	)

	type args struct {
		data entity.EntryZkProofVerifiedData
	}
	type want struct {
		err              error
		expectEnqueueErr error
		expectEnqueue    bool
		expectStatus     string
	}
	tests := []struct {
		name      string
		args      args
		want      want
		nilClient bool
	}{
		{
			name: "forwards entry.zk_proof.verified with event_id property",
			args: args{data: entity.EntryZkProofVerifiedData{
				NullifierHashHex: validNullifier,
				EventID:          validEventID,
			}},
			want: want{expectEnqueue: true, expectStatus: "forwarded"},
		},
		{
			name: "skips forward when client is nil",
			args: args{data: entity.EntryZkProofVerifiedData{
				NullifierHashHex: validNullifier,
				EventID:          validEventID,
			}},
			want:      want{expectEnqueue: false, expectStatus: "skipped_nil_client"},
			nilClient: true,
		},
		{
			name: "skips forward when nullifier_hash_hex is empty",
			args: args{data: entity.EntryZkProofVerifiedData{
				NullifierHashHex: "",
				EventID:          validEventID,
			}},
			want: want{expectEnqueue: false, expectStatus: "skipped_empty_user_id"},
		},
		{
			name: "wraps Enqueue error as apperr.ErrInternal",
			args: args{data: entity.EntryZkProofVerifiedData{
				NullifierHashHex: validNullifier,
				EventID:          validEventID,
			}},
			want: want{
				expectEnqueue:    true,
				expectEnqueueErr: errors.New("queue full"),
				err:              apperr.ErrInternal,
				expectStatus:     "enqueue_error",
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
							tt.args.data.NullifierHashHex,
							usecase.EventEntryZkProofVerified,
							mock.MatchedBy(func(props usecase.AnalyticsProperties) bool {
								return props["event_id"] == tt.args.data.EventID
							}),
						).
						Return(tt.want.expectEnqueueErr).
						Once()
				}
			}

			metricsMock := ucmocks.NewMockAnalyticsConsumerMetrics(t)
			metricsMock.EXPECT().
				RecordMessage(mock.Anything, tt.want.expectStatus).
				Once()
			metricsMock.EXPECT().
				RecordLag(mock.Anything, mock.MatchedBy(func(s float64) bool { return s >= 0 })).
				Once()

			handler := event.NewAnalyticsConsumer(client, metricsMock, newTestLogger(t))
			err := handler.HandleEntryZkProofVerified(makeAnalyticsMsg(t, tt.args.data))

			if tt.want.err != nil {
				assert.ErrorIs(t, err, tt.want.err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// TestAnalyticsConsumer_HandleEntryZkProofRejected mirrors the verified
// test for the rejection case (carries the reason property additionally).
func TestAnalyticsConsumer_HandleEntryZkProofRejected(t *testing.T) {
	t.Parallel()

	const (
		validNullifier = "deadbeefcafebabe1234567890abcdef"
		validEventID   = "550e8400-e29b-41d4-a716-446655440000"
	)

	type args struct {
		data entity.EntryZkProofRejectedData
	}
	type want struct {
		err              error
		expectEnqueueErr error
		expectEnqueue    bool
		expectStatus     string
	}
	tests := []struct {
		name      string
		args      args
		want      want
		nilClient bool
	}{
		{
			name: "forwards entry.zk_proof.rejected with event_id + reason",
			args: args{data: entity.EntryZkProofRejectedData{
				NullifierHashHex: validNullifier,
				EventID:          validEventID,
				Reason:           entity.EntryRejectionMerkleRootMismatch,
			}},
			want: want{expectEnqueue: true, expectStatus: "forwarded"},
		},
		{
			name: "skips forward when client is nil",
			args: args{data: entity.EntryZkProofRejectedData{
				NullifierHashHex: validNullifier,
				EventID:          validEventID,
				Reason:           entity.EntryRejectionProofInvalid,
			}},
			want:      want{expectEnqueue: false, expectStatus: "skipped_nil_client"},
			nilClient: true,
		},
		{
			name: "skips forward when nullifier_hash_hex is empty",
			args: args{data: entity.EntryZkProofRejectedData{
				NullifierHashHex: "",
				EventID:          validEventID,
				Reason:           entity.EntryRejectionAlreadyCheckedIn,
			}},
			want: want{expectEnqueue: false, expectStatus: "skipped_empty_user_id"},
		},
		{
			name: "wraps Enqueue error as apperr.ErrInternal",
			args: args{data: entity.EntryZkProofRejectedData{
				NullifierHashHex: validNullifier,
				EventID:          validEventID,
				Reason:           entity.EntryRejectionProofInvalid,
			}},
			want: want{
				expectEnqueue:    true,
				expectEnqueueErr: errors.New("queue full"),
				err:              apperr.ErrInternal,
				expectStatus:     "enqueue_error",
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
							tt.args.data.NullifierHashHex,
							usecase.EventEntryZkProofRejected,
							mock.MatchedBy(func(props usecase.AnalyticsProperties) bool {
								return props["event_id"] == tt.args.data.EventID &&
									props["reason"] == string(tt.args.data.Reason)
							}),
						).
						Return(tt.want.expectEnqueueErr).
						Once()
				}
			}

			metricsMock := ucmocks.NewMockAnalyticsConsumerMetrics(t)
			metricsMock.EXPECT().
				RecordMessage(mock.Anything, tt.want.expectStatus).
				Once()
			metricsMock.EXPECT().
				RecordLag(mock.Anything, mock.MatchedBy(func(s float64) bool { return s >= 0 })).
				Once()

			handler := event.NewAnalyticsConsumer(client, metricsMock, newTestLogger(t))
			err := handler.HandleEntryZkProofRejected(makeAnalyticsMsg(t, tt.args.data))

			if tt.want.err != nil {
				assert.ErrorIs(t, err, tt.want.err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// TestAnalyticsConsumer_HandleTicketJourneyStatusChanged covers
// TICKET_JOURNEY.status_changed → ticket.journey.status.changed routing.
// Properties: event_id, from_status, to_status. from_status is "UNSPECIFIED"
// when no prior journey existed.
func TestAnalyticsConsumer_HandleTicketJourneyStatusChanged(t *testing.T) {
	t.Parallel()

	const (
		validUserID  = "11111111-2222-3333-4444-555555555555"
		validEventID = "550e8400-e29b-41d4-a716-446655440000"
	)

	type args struct {
		data entity.TicketJourneyStatusChangedData
	}
	type want struct {
		err              error
		expectEnqueueErr error
		expectEnqueue    bool
		expectStatus     string
	}
	tests := []struct {
		name      string
		args      args
		want      want
		nilClient bool
	}{
		{
			name: "forwards ticket.journey.status.changed with event_id + from_status + to_status",
			args: args{data: entity.TicketJourneyStatusChangedData{
				UserID:     validUserID,
				EventID:    validEventID,
				FromStatus: "UNSPECIFIED",
				ToStatus:   "TRACKING",
			}},
			want: want{expectEnqueue: true, expectStatus: "forwarded"},
		},
		{
			name: "forwards when from_status is a named status (non-first transition)",
			args: args{data: entity.TicketJourneyStatusChangedData{
				UserID:     validUserID,
				EventID:    validEventID,
				FromStatus: "TRACKING",
				ToStatus:   "APPLIED",
			}},
			want: want{expectEnqueue: true, expectStatus: "forwarded"},
		},
		{
			name: "skips forward when client is nil",
			args: args{data: entity.TicketJourneyStatusChangedData{
				UserID:     validUserID,
				EventID:    validEventID,
				FromStatus: "UNSPECIFIED",
				ToStatus:   "TRACKING",
			}},
			want:      want{expectEnqueue: false, expectStatus: "skipped_nil_client"},
			nilClient: true,
		},
		{
			name: "skips forward when UserID is empty",
			args: args{data: entity.TicketJourneyStatusChangedData{
				UserID:     "",
				EventID:    validEventID,
				FromStatus: "UNSPECIFIED",
				ToStatus:   "TRACKING",
			}},
			want: want{expectEnqueue: false, expectStatus: "skipped_empty_user_id"},
		},
		{
			name: "wraps Enqueue error as apperr.ErrInternal",
			args: args{data: entity.TicketJourneyStatusChangedData{
				UserID:     validUserID,
				EventID:    validEventID,
				FromStatus: "UNPAID",
				ToStatus:   "PAID",
			}},
			want: want{
				expectEnqueue:    true,
				expectEnqueueErr: errors.New("queue full"),
				err:              apperr.ErrInternal,
				expectStatus:     "enqueue_error",
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
							usecase.EventTicketJourneyStatusChanged,
							mock.MatchedBy(func(props usecase.AnalyticsProperties) bool {
								return props["event_id"] == tt.args.data.EventID &&
									props["from_status"] == tt.args.data.FromStatus &&
									props["to_status"] == tt.args.data.ToStatus
							}),
						).
						Return(tt.want.expectEnqueueErr).
						Once()
				}
			}

			metricsMock := ucmocks.NewMockAnalyticsConsumerMetrics(t)
			metricsMock.EXPECT().
				RecordMessage(mock.Anything, tt.want.expectStatus).
				Once()
			metricsMock.EXPECT().
				RecordLag(mock.Anything, mock.MatchedBy(func(s float64) bool { return s >= 0 })).
				Once()

			handler := event.NewAnalyticsConsumer(client, metricsMock, newTestLogger(t))
			err := handler.HandleTicketJourneyStatusChanged(makeAnalyticsMsg(t, tt.args.data))

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
	metricsMock := ucmocks.NewMockAnalyticsConsumerMetrics(t)
	metricsMock.EXPECT().
		RecordMessage(mock.Anything, "skipped_parse_error").
		Once()
	// No ce_time metadata on this raw-payload message, so recordLag
	// silently no-ops and RecordLag is NOT expected.

	handler := event.NewAnalyticsConsumer(clientMock, metricsMock, newTestLogger(t))

	msg := message.NewMessage("test-id", []byte("not-valid-json"))
	err := handler.HandleUserCreated(msg)

	assert.ErrorIs(t, err, apperr.ErrInternal)
	// The client mock has no EXPECT() calls — assertExpectations on
	// Cleanup would fail if Enqueue had been invoked.
}
