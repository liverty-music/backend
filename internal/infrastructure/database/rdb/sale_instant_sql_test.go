package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

// TestSaleInstantSQL checks that PostgreSQL reads a wall-clock sale time as
// the same instant as [entity.LocalDateTimeInstant], around clock changes of
// one hour, half an hour and two hours.
func TestSaleInstantSQL(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	tests := []struct{ zone, civil string }{
		{"Asia/Tokyo", "2026-11-01 12:00"},
		{"America/New_York", "2026-11-01 00:30"},
		{"America/New_York", "2026-11-01 01:30"},
		{"America/New_York", "2026-11-01 02:30"},
		{"America/New_York", "2027-03-14 01:30"},
		{"America/New_York", "2027-03-14 03:30"},
		{"Europe/London", "2026-10-25 01:30"},
		{"Australia/Lord_Howe", "2026-04-05 01:45"},
		{"Australia/Lord_Howe", "2026-04-05 02:15"},
		{"Antarctica/Troll", "2026-10-25 01:30"},
		{"Antarctica/Troll", "2026-10-25 02:30"},
		{"Antarctica/Troll", "2026-10-25 03:30"},
		// Times the clocks skip, which only a later change of a zone's rules
		// can leave stored.
		{"America/New_York", "2027-03-14 02:30"},
		{"Australia/Lord_Howe", "2026-10-04 02:15"},
	}
	for _, tt := range tests {
		t.Run(tt.zone+" "+tt.civil, func(t *testing.T) {
			civil, err := time.Parse("2006-01-02 15:04", tt.civil)
			require.NoError(t, err)
			loc, err := entity.LoadZone(tt.zone)
			require.NoError(t, err)
			want := rdb.SaleInstant(civil, loc)

			var got time.Time
			query := `SELECT ` + rdb.SaleEndInstantSQL + ` FROM (SELECT $1::text::timestamp AS local_end_time, $2::text AS time_zone) AS ts`
			require.NoError(t, db.Pool.QueryRow(context.Background(), query, tt.civil, tt.zone).Scan(&got))

			assert.Equal(t, want.UTC(), got.UTC())
		})
	}
}
