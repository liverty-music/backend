package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func strPtr(s string) *string { return &s }

func TestParsedEmailData_JourneyStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		parsed    *entity.ParsedEmailData
		emailType entity.TicketEmailType
		want      *entity.TicketJourneyStatus
	}{
		{
			name:      "LotteryInfo always returns Tracking",
			parsed:    &entity.ParsedEmailData{},
			emailType: entity.TicketEmailTypeLotteryInfo,
			want:      statusPtr(entity.TicketJourneyStatusTracking),
		},
		{
			name: "LotteryResult with lost returns Lost",
			parsed: &entity.ParsedEmailData{
				LotteryResult: strPtr("lost"),
			},
			emailType: entity.TicketEmailTypeLotteryResult,
			want:      statusPtr(entity.TicketJourneyStatusLost),
		},
		{
			name: "LotteryResult with payment_status paid returns Paid",
			parsed: &entity.ParsedEmailData{
				PaymentStatus: strPtr("paid"),
			},
			emailType: entity.TicketEmailTypeLotteryResult,
			want:      statusPtr(entity.TicketJourneyStatusPaid),
		},
		{
			name: "LotteryResult with no result or payment returns Unpaid",
			parsed: &entity.ParsedEmailData{
				PaymentStatus: strPtr("unpaid"),
			},
			emailType: entity.TicketEmailTypeLotteryResult,
			want:      statusPtr(entity.TicketJourneyStatusUnpaid),
		},
		{
			name:      "LotteryResult with no fields returns Unpaid",
			parsed:    &entity.ParsedEmailData{},
			emailType: entity.TicketEmailTypeLotteryResult,
			want:      statusPtr(entity.TicketJourneyStatusUnpaid),
		},
		{
			name:      "lost takes priority over payment status",
			parsed:    &entity.ParsedEmailData{LotteryResult: strPtr("lost"), PaymentStatus: strPtr("paid")},
			emailType: entity.TicketEmailTypeLotteryResult,
			want:      statusPtr(entity.TicketJourneyStatusLost),
		},
		{
			name:      "unknown email type returns nil",
			parsed:    &entity.ParsedEmailData{},
			emailType: entity.TicketEmailType(99),
			want:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.parsed.JourneyStatus(tt.emailType)
			if tt.want == nil {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				assert.Equal(t, *tt.want, *got)
			}
		})
	}
}

func statusPtr(s entity.TicketJourneyStatus) *entity.TicketJourneyStatus { return &s }
