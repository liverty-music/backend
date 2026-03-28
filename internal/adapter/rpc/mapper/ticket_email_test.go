package mapper_test

import (
	"encoding/json"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTicketEmailToProto(t *testing.T) {
	t.Parallel()

	paymentDeadline := time.Date(2025, 5, 10, 23, 59, 0, 0, time.UTC)
	lotteryStart := time.Date(2025, 4, 1, 10, 0, 0, 0, time.UTC)
	lotteryEnd := time.Date(2025, 4, 15, 23, 59, 0, 0, time.UTC)

	appURL := "https://ticket.example.com/apply"

	journeyApplied := entity.TicketJourneyStatusApplied
	journeyPaid := entity.TicketJourneyStatusPaid

	tests := []struct {
		name string
		args *entity.TicketEmail
		want *entityv1.TicketEmail
	}{
		{
			name: "nil ticket email returns nil",
			args: nil,
			want: nil,
		},
		{
			name: "minimal ticket email with lottery info type",
			args: &entity.TicketEmail{
				ID:        "email-id-1",
				UserID:    "user-id-1",
				EventID:   "event-id-1",
				EmailType: entity.TicketEmailTypeLotteryInfo,
				RawBody:   "Lottery application open",
			},
			want: &entityv1.TicketEmail{
				Id:        &entityv1.TicketEmailId{Value: "email-id-1"},
				UserId:    &entityv1.UserId{Value: "user-id-1"},
				EventId:   &entityv1.EventId{Value: "event-id-1"},
				EmailType: entityv1.TicketEmailType_TICKET_EMAIL_TYPE_LOTTERY_INFO,
				RawBody:   "Lottery application open",
			},
		},
		{
			name: "ticket email with lottery result type",
			args: &entity.TicketEmail{
				ID:        "email-id-2",
				UserID:    "user-id-2",
				EventID:   "event-id-2",
				EmailType: entity.TicketEmailTypeLotteryResult,
				RawBody:   "Congratulations! You won.",
			},
			want: &entityv1.TicketEmail{
				Id:        &entityv1.TicketEmailId{Value: "email-id-2"},
				UserId:    &entityv1.UserId{Value: "user-id-2"},
				EventId:   &entityv1.EventId{Value: "event-id-2"},
				EmailType: entityv1.TicketEmailType_TICKET_EMAIL_TYPE_LOTTERY_RESULT,
				RawBody:   "Congratulations! You won.",
			},
		},
		{
			name: "ticket email with all optional timestamp fields",
			args: &entity.TicketEmail{
				ID:                  "email-id-3",
				UserID:              "user-id-3",
				EventID:             "event-id-3",
				EmailType:           entity.TicketEmailTypeLotteryInfo,
				RawBody:             "Full lottery info",
				PaymentDeadlineTime: &paymentDeadline,
				LotteryStartTime:    &lotteryStart,
				LotteryEndTime:      &lotteryEnd,
			},
			want: &entityv1.TicketEmail{
				Id:        &entityv1.TicketEmailId{Value: "email-id-3"},
				UserId:    &entityv1.UserId{Value: "user-id-3"},
				EventId:   &entityv1.EventId{Value: "event-id-3"},
				EmailType: entityv1.TicketEmailType_TICKET_EMAIL_TYPE_LOTTERY_INFO,
				RawBody:   "Full lottery info",
			},
		},
		{
			name: "ticket email with application URL",
			args: &entity.TicketEmail{
				ID:             "email-id-4",
				UserID:         "user-id-4",
				EventID:        "event-id-4",
				EmailType:      entity.TicketEmailTypeLotteryInfo,
				RawBody:        "Apply here",
				ApplicationURL: appURL,
			},
			want: &entityv1.TicketEmail{
				Id:             &entityv1.TicketEmailId{Value: "email-id-4"},
				UserId:         &entityv1.UserId{Value: "user-id-4"},
				EventId:        &entityv1.EventId{Value: "event-id-4"},
				EmailType:      entityv1.TicketEmailType_TICKET_EMAIL_TYPE_LOTTERY_INFO,
				RawBody:        "Apply here",
				ApplicationUrl: &appURL,
			},
		},
		{
			name: "ticket email with empty application URL omits field",
			args: &entity.TicketEmail{
				ID:             "email-id-5",
				UserID:         "user-id-5",
				EventID:        "event-id-5",
				EmailType:      entity.TicketEmailTypeLotteryResult,
				RawBody:        "No apply URL",
				ApplicationURL: "",
			},
			want: &entityv1.TicketEmail{
				Id:        &entityv1.TicketEmailId{Value: "email-id-5"},
				UserId:    &entityv1.UserId{Value: "user-id-5"},
				EventId:   &entityv1.EventId{Value: "event-id-5"},
				EmailType: entityv1.TicketEmailType_TICKET_EMAIL_TYPE_LOTTERY_RESULT,
				RawBody:   "No apply URL",
			},
		},
		{
			name: "ticket email with journey status applied",
			args: &entity.TicketEmail{
				ID:            "email-id-6",
				UserID:        "user-id-6",
				EventID:       "event-id-6",
				EmailType:     entity.TicketEmailTypeLotteryInfo,
				RawBody:       "Applied",
				JourneyStatus: &journeyApplied,
			},
			want: func() *entityv1.TicketEmail {
				s := entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_APPLIED
				return &entityv1.TicketEmail{
					Id:            &entityv1.TicketEmailId{Value: "email-id-6"},
					UserId:        &entityv1.UserId{Value: "user-id-6"},
					EventId:       &entityv1.EventId{Value: "event-id-6"},
					EmailType:     entityv1.TicketEmailType_TICKET_EMAIL_TYPE_LOTTERY_INFO,
					RawBody:       "Applied",
					JourneyStatus: &s,
				}
			}(),
		},
		{
			name: "ticket email with journey status paid",
			args: &entity.TicketEmail{
				ID:            "email-id-7",
				UserID:        "user-id-7",
				EventID:       "event-id-7",
				EmailType:     entity.TicketEmailTypeLotteryResult,
				RawBody:       "Payment complete",
				JourneyStatus: &journeyPaid,
			},
			want: func() *entityv1.TicketEmail {
				s := entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_PAID
				return &entityv1.TicketEmail{
					Id:            &entityv1.TicketEmailId{Value: "email-id-7"},
					UserId:        &entityv1.UserId{Value: "user-id-7"},
					EventId:       &entityv1.EventId{Value: "event-id-7"},
					EmailType:     entityv1.TicketEmailType_TICKET_EMAIL_TYPE_LOTTERY_RESULT,
					RawBody:       "Payment complete",
					JourneyStatus: &s,
				}
			}(),
		},
		{
			name: "ticket email with nil journey status omits field",
			args: &entity.TicketEmail{
				ID:            "email-id-8",
				UserID:        "user-id-8",
				EventID:       "event-id-8",
				EmailType:     entity.TicketEmailTypeLotteryInfo,
				RawBody:       "No status yet",
				JourneyStatus: nil,
			},
			want: &entityv1.TicketEmail{
				Id:        &entityv1.TicketEmailId{Value: "email-id-8"},
				UserId:    &entityv1.UserId{Value: "user-id-8"},
				EventId:   &entityv1.EventId{Value: "event-id-8"},
				EmailType: entityv1.TicketEmailType_TICKET_EMAIL_TYPE_LOTTERY_INFO,
				RawBody:   "No status yet",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mapper.TicketEmailToProto(tt.args)

			if tt.want == nil {
				assert.Nil(t, got)
				return
			}

			require.NotNil(t, got)
			// Compare scalar fields directly for robustness with timestamp fields.
			assert.Equal(t, tt.want.GetId().GetValue(), got.GetId().GetValue())
			assert.Equal(t, tt.want.GetUserId().GetValue(), got.GetUserId().GetValue())
			assert.Equal(t, tt.want.GetEventId().GetValue(), got.GetEventId().GetValue())
			assert.Equal(t, tt.want.GetEmailType(), got.GetEmailType())
			assert.Equal(t, tt.want.GetRawBody(), got.GetRawBody())
			assert.Equal(t, tt.want.GetApplicationUrl(), got.GetApplicationUrl())
			assert.Equal(t, tt.want.GetJourneyStatus(), got.GetJourneyStatus())
		})
	}
}

func TestTicketEmailToProto_timestampsArePreserved(t *testing.T) {
	t.Parallel()

	paymentDeadline := time.Date(2025, 5, 10, 23, 59, 0, 0, time.UTC)
	lotteryStart := time.Date(2025, 4, 1, 10, 0, 0, 0, time.UTC)
	lotteryEnd := time.Date(2025, 4, 15, 23, 59, 0, 0, time.UTC)

	email := &entity.TicketEmail{
		ID:                  "email-ts",
		UserID:              "user-ts",
		EventID:             "event-ts",
		EmailType:           entity.TicketEmailTypeLotteryInfo,
		RawBody:             "timestamps",
		PaymentDeadlineTime: &paymentDeadline,
		LotteryStartTime:    &lotteryStart,
		LotteryEndTime:      &lotteryEnd,
	}

	got := mapper.TicketEmailToProto(email)

	require.NotNil(t, got)
	require.NotNil(t, got.GetPaymentDeadline())
	assert.Equal(t, paymentDeadline.Unix(), got.GetPaymentDeadline().GetSeconds())
	require.NotNil(t, got.GetLotteryStart())
	assert.Equal(t, lotteryStart.Unix(), got.GetLotteryStart().GetSeconds())
	require.NotNil(t, got.GetLotteryEnd())
	assert.Equal(t, lotteryEnd.Unix(), got.GetLotteryEnd().GetSeconds())
}

func TestTicketEmailToProto_parsedDataIsUnmarshaled(t *testing.T) {
	t.Parallel()

	rawJSON := json.RawMessage(`{"lottery_id":"LT-001","seats":2}`)

	email := &entity.TicketEmail{
		ID:         "email-parsed",
		UserID:     "user-parsed",
		EventID:    "event-parsed",
		EmailType:  entity.TicketEmailTypeLotteryInfo,
		RawBody:    "parsed data test",
		ParsedData: rawJSON,
	}

	got := mapper.TicketEmailToProto(email)

	require.NotNil(t, got)
	require.NotNil(t, got.GetParsedData())
	fields := got.GetParsedData().GetFields()
	require.NotNil(t, fields)
	assert.Equal(t, "LT-001", fields["lottery_id"].GetStringValue())
	assert.InDelta(t, 2.0, fields["seats"].GetNumberValue(), 0.001)
}

func TestTicketEmailToProto_nilParsedDataOmitsField(t *testing.T) {
	t.Parallel()

	email := &entity.TicketEmail{
		ID:         "email-no-parsed",
		UserID:     "user-1",
		EventID:    "event-1",
		EmailType:  entity.TicketEmailTypeLotteryInfo,
		RawBody:    "no parsed data",
		ParsedData: nil,
	}

	got := mapper.TicketEmailToProto(email)

	require.NotNil(t, got)
	assert.Nil(t, got.GetParsedData())
}

func TestTicketEmailToProto_invalidJSONParsedDataOmitsField(t *testing.T) {
	t.Parallel()

	email := &entity.TicketEmail{
		ID:         "email-bad-json",
		UserID:     "user-1",
		EventID:    "event-1",
		EmailType:  entity.TicketEmailTypeLotteryInfo,
		RawBody:    "bad json",
		ParsedData: json.RawMessage(`{invalid json`),
	}

	got := mapper.TicketEmailToProto(email)

	require.NotNil(t, got)
	assert.Nil(t, got.GetParsedData())
}

func TestTicketEmailsToProto(t *testing.T) {
	t.Parallel()

	emails := []*entity.TicketEmail{
		{
			ID:        "e-1",
			UserID:    "u-1",
			EventID:   "ev-1",
			EmailType: entity.TicketEmailTypeLotteryInfo,
			RawBody:   "first",
		},
		{
			ID:        "e-2",
			UserID:    "u-2",
			EventID:   "ev-2",
			EmailType: entity.TicketEmailTypeLotteryResult,
			RawBody:   "second",
		},
	}

	got := mapper.TicketEmailsToProto(emails)

	require.Len(t, got, 2)
	assert.Equal(t, "e-1", got[0].GetId().GetValue())
	assert.Equal(t, entityv1.TicketEmailType_TICKET_EMAIL_TYPE_LOTTERY_INFO, got[0].GetEmailType())
	assert.Equal(t, "e-2", got[1].GetId().GetValue())
	assert.Equal(t, entityv1.TicketEmailType_TICKET_EMAIL_TYPE_LOTTERY_RESULT, got[1].GetEmailType())
}

func TestTicketEmailsToProto_empty(t *testing.T) {
	t.Parallel()

	got := mapper.TicketEmailsToProto([]*entity.TicketEmail{})
	assert.Empty(t, got)
}

func TestTicketEmailTypeFromProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		proto entityv1.TicketEmailType
		want  entity.TicketEmailType
	}{
		{
			name:  "lottery info proto maps to lottery info domain",
			proto: entityv1.TicketEmailType_TICKET_EMAIL_TYPE_LOTTERY_INFO,
			want:  entity.TicketEmailTypeLotteryInfo,
		},
		{
			name:  "lottery result proto maps to lottery result domain",
			proto: entityv1.TicketEmailType_TICKET_EMAIL_TYPE_LOTTERY_RESULT,
			want:  entity.TicketEmailTypeLotteryResult,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mapper.TicketEmailTypeFromProto[tt.proto]
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestJourneyStatusFromProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		proto entityv1.TicketJourneyStatus
		want  entity.TicketJourneyStatus
	}{
		{
			name:  "tracking proto maps to tracking domain",
			proto: entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING,
			want:  entity.TicketJourneyStatusTracking,
		},
		{
			name:  "applied proto maps to applied domain",
			proto: entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_APPLIED,
			want:  entity.TicketJourneyStatusApplied,
		},
		{
			name:  "lost proto maps to lost domain",
			proto: entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_LOST,
			want:  entity.TicketJourneyStatusLost,
		},
		{
			name:  "unpaid proto maps to unpaid domain",
			proto: entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_UNPAID,
			want:  entity.TicketJourneyStatusUnpaid,
		},
		{
			name:  "paid proto maps to paid domain",
			proto: entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_PAID,
			want:  entity.TicketJourneyStatusPaid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mapper.JourneyStatusFromProto[tt.proto]
			assert.Equal(t, tt.want, got)
		})
	}
}
