package mapper_test

import (
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTicketToProto(t *testing.T) {
	t.Parallel()

	mintTime := time.Date(2025, 4, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		args *entity.Ticket
		want *entityv1.Ticket
	}{
		{
			name: "nil ticket returns nil",
			args: nil,
			want: nil,
		},
		{
			name: "ticket with all fields populated",
			args: &entity.Ticket{
				ID:       "ticket-id-1",
				EventID:  "event-id-1",
				UserID:   "user-id-1",
				TokenID:  42,
				TxHash:   "0xabc123def456",
				MintTime: mintTime,
			},
			want: &entityv1.Ticket{
				Id:      &entityv1.TicketId{Value: "ticket-id-1"},
				EventId: &entityv1.EventId{Value: "event-id-1"},
				UserId:  &entityv1.UserId{Value: "user-id-1"},
				TokenId: &entityv1.TokenId{Value: 42},
				TxHash:  "0xabc123def456",
			},
		},
		{
			name: "ticket with zero token ID",
			args: &entity.Ticket{
				ID:       "ticket-id-2",
				EventID:  "event-id-2",
				UserID:   "user-id-2",
				TokenID:  0,
				TxHash:   "0x000000",
				MintTime: mintTime,
			},
			want: &entityv1.Ticket{
				Id:      &entityv1.TicketId{Value: "ticket-id-2"},
				EventId: &entityv1.EventId{Value: "event-id-2"},
				UserId:  &entityv1.UserId{Value: "user-id-2"},
				TokenId: &entityv1.TokenId{Value: 0},
				TxHash:  "0x000000",
			},
		},
		{
			name: "ticket with empty tx hash",
			args: &entity.Ticket{
				ID:       "ticket-id-3",
				EventID:  "event-id-3",
				UserID:   "user-id-3",
				TokenID:  99,
				TxHash:   "",
				MintTime: mintTime,
			},
			want: &entityv1.Ticket{
				Id:      &entityv1.TicketId{Value: "ticket-id-3"},
				EventId: &entityv1.EventId{Value: "event-id-3"},
				UserId:  &entityv1.UserId{Value: "user-id-3"},
				TokenId: &entityv1.TokenId{Value: 99},
				TxHash:  "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mapper.TicketToProto(tt.args)

			if tt.want == nil {
				assert.Nil(t, got)
				return
			}

			require.NotNil(t, got)
			assert.Equal(t, tt.want.GetId().GetValue(), got.GetId().GetValue())
			assert.Equal(t, tt.want.GetEventId().GetValue(), got.GetEventId().GetValue())
			assert.Equal(t, tt.want.GetUserId().GetValue(), got.GetUserId().GetValue())
			assert.Equal(t, tt.want.GetTokenId().GetValue(), got.GetTokenId().GetValue())
			assert.Equal(t, tt.want.GetTxHash(), got.GetTxHash())
		})
	}
}

func TestTicketToProto_mintTimeIsPreserved(t *testing.T) {
	t.Parallel()

	mintTime := time.Date(2025, 4, 1, 12, 0, 0, 0, time.UTC)
	ticket := &entity.Ticket{
		ID:       "ticket-id",
		EventID:  "event-id",
		UserID:   "user-id",
		TokenID:  1,
		TxHash:   "0xhash",
		MintTime: mintTime,
	}

	got := mapper.TicketToProto(ticket)

	require.NotNil(t, got)
	require.NotNil(t, got.GetMintTime())
	assert.Equal(t, mintTime.Unix(), got.GetMintTime().GetSeconds())
}

func TestTicketsToProto(t *testing.T) {
	t.Parallel()

	mintTime := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
	tickets := []*entity.Ticket{
		{ID: "t-1", EventID: "e-1", UserID: "u-1", TokenID: 1, TxHash: "0x01", MintTime: mintTime},
		{ID: "t-2", EventID: "e-2", UserID: "u-2", TokenID: 2, TxHash: "0x02", MintTime: mintTime},
	}

	got := mapper.TicketsToProto(tickets)

	require.Len(t, got, 2)
	assert.Equal(t, "t-1", got[0].GetId().GetValue())
	assert.Equal(t, uint64(1), got[0].GetTokenId().GetValue())
	assert.Equal(t, "t-2", got[1].GetId().GetValue())
	assert.Equal(t, uint64(2), got[1].GetTokenId().GetValue())
}

func TestTicketsToProto_empty(t *testing.T) {
	t.Parallel()

	got := mapper.TicketsToProto([]*entity.Ticket{})
	assert.Empty(t, got)
}
