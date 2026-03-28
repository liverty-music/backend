package mapper_test

import (
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTicketJourneyToProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args *entity.TicketJourney
		want *entityv1.TicketJourney
	}{
		{
			name: "nil ticket journey returns nil",
			args: nil,
			want: nil,
		},
		{
			name: "journey with status tracking",
			args: &entity.TicketJourney{
				UserID:  "user-id-1",
				EventID: "event-id-1",
				Status:  entity.TicketJourneyStatusTracking,
			},
			want: &entityv1.TicketJourney{
				UserId:  &entityv1.UserId{Value: "user-id-1"},
				EventId: &entityv1.EventId{Value: "event-id-1"},
				Status:  entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING,
			},
		},
		{
			name: "journey with status applied",
			args: &entity.TicketJourney{
				UserID:  "user-id-2",
				EventID: "event-id-2",
				Status:  entity.TicketJourneyStatusApplied,
			},
			want: &entityv1.TicketJourney{
				UserId:  &entityv1.UserId{Value: "user-id-2"},
				EventId: &entityv1.EventId{Value: "event-id-2"},
				Status:  entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_APPLIED,
			},
		},
		{
			name: "journey with status lost",
			args: &entity.TicketJourney{
				UserID:  "user-id-3",
				EventID: "event-id-3",
				Status:  entity.TicketJourneyStatusLost,
			},
			want: &entityv1.TicketJourney{
				UserId:  &entityv1.UserId{Value: "user-id-3"},
				EventId: &entityv1.EventId{Value: "event-id-3"},
				Status:  entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_LOST,
			},
		},
		{
			name: "journey with status unpaid",
			args: &entity.TicketJourney{
				UserID:  "user-id-4",
				EventID: "event-id-4",
				Status:  entity.TicketJourneyStatusUnpaid,
			},
			want: &entityv1.TicketJourney{
				UserId:  &entityv1.UserId{Value: "user-id-4"},
				EventId: &entityv1.EventId{Value: "event-id-4"},
				Status:  entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_UNPAID,
			},
		},
		{
			name: "journey with status paid",
			args: &entity.TicketJourney{
				UserID:  "user-id-5",
				EventID: "event-id-5",
				Status:  entity.TicketJourneyStatusPaid,
			},
			want: &entityv1.TicketJourney{
				UserId:  &entityv1.UserId{Value: "user-id-5"},
				EventId: &entityv1.EventId{Value: "event-id-5"},
				Status:  entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_PAID,
			},
		},
		{
			name: "journey with empty user and event IDs",
			args: &entity.TicketJourney{
				UserID:  "",
				EventID: "",
				Status:  entity.TicketJourneyStatusTracking,
			},
			want: &entityv1.TicketJourney{
				UserId:  &entityv1.UserId{Value: ""},
				EventId: &entityv1.EventId{Value: ""},
				Status:  entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mapper.TicketJourneyToProto(tt.args)

			if tt.want == nil {
				assert.Nil(t, got)
				return
			}

			require.NotNil(t, got)
			assert.Equal(t, tt.want.String(), got.String())
		})
	}
}

func TestTicketJourneysToProto(t *testing.T) {
	t.Parallel()

	journeys := []*entity.TicketJourney{
		{UserID: "u-1", EventID: "e-1", Status: entity.TicketJourneyStatusTracking},
		{UserID: "u-2", EventID: "e-2", Status: entity.TicketJourneyStatusPaid},
	}

	got := mapper.TicketJourneysToProto(journeys)

	require.Len(t, got, 2)
	assert.Equal(t, "u-1", got[0].GetUserId().GetValue())
	assert.Equal(t, entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_TRACKING, got[0].GetStatus())
	assert.Equal(t, "u-2", got[1].GetUserId().GetValue())
	assert.Equal(t, entityv1.TicketJourneyStatus_TICKET_JOURNEY_STATUS_PAID, got[1].GetStatus())
}

func TestTicketJourneysToProto_empty(t *testing.T) {
	t.Parallel()

	got := mapper.TicketJourneysToProto([]*entity.TicketJourney{})
	assert.Empty(t, got)
}

func TestTicketJourneyStatusFromProto(t *testing.T) {
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

			got := mapper.TicketJourneyStatusFromProto[tt.proto]
			assert.Equal(t, tt.want, got)
		})
	}
}
