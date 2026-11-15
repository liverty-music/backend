package entity_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/liverty-music/backend/internal/entity"
)

func TestTicketSaleSearch_DueAt(t *testing.T) {
	t.Parallel()

	jst := time.FixedZone("JST", 9*60*60)
	searched := time.Date(2026, 10, 7, 21, 0, 30, 0, jst)

	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{
			// @spec components/usecase/ticket-sale/discover-for-artist "Ten days counted by date"
			name: "Ten days counted by date",
			now:  time.Date(2026, 10, 17, 21, 0, 0, 0, jst),
			want: true,
		},
		{
			// @spec components/usecase/ticket-sale/discover-for-artist "Nine days counted by date"
			name: "Nine days counted by date",
			now:  time.Date(2026, 10, 16, 23, 59, 0, 0, jst),
			want: false,
		},
		{
			name: "dates are taken in Japan time, not UTC",
			// 17 October 08:59 JST is still 16 October in UTC.
			now:  time.Date(2026, 10, 16, 23, 59, 0, 0, time.UTC),
			want: true,
		},
		{
			name: "same day",
			now:  time.Date(2026, 10, 7, 23, 0, 0, 0, jst),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, (&entity.TicketSaleSearch{SearchedTime: searched}).DueAt(tt.now))
		})
	}
}
