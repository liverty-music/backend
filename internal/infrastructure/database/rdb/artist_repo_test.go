package rdb_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArtistRepository_Create(t *testing.T) {
	repo := rdb.NewArtistRepository(testDB)
	ctx := context.Background()

	// Pre-build reusable inputs so we can reference their generated IDs in want.
	beatles := entity.NewArtist("The Beatles", "b10bbbfc-cf9e-42e0-be17-e2c3e1d2600d")
	unknown := entity.NewArtist("Unknown Artist", "")
	bulkA := entity.NewArtist("Artist A", "aaaa-1111")
	bulkB := entity.NewArtist("Artist B", "bbbb-2222")
	bulkC := entity.NewArtist("Artist C", "cccc-3333")
	withMBID := entity.NewArtist("With MBID", "mixed-mbid-001")
	noMBID1 := entity.NewArtist("Without MBID 1", "")
	noMBID2 := entity.NewArtist("Without MBID 2", "")
	presetID := &entity.Artist{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4900", Name: "Pre-set ID Artist"}

	type args struct {
		artists []*entity.Artist
	}

	tests := []struct {
		name    string
		setup   func()
		args    args
		want    []*entity.Artist
		wantErr error
	}{
		{
			name:  "single artist with MBID",
			setup: cleanDatabase,
			args:  args{artists: []*entity.Artist{beatles}},
			want:  []*entity.Artist{beatles},
		},
		{
			name:  "single artist without MBID",
			setup: cleanDatabase,
			args:  args{artists: []*entity.Artist{unknown}},
			want:  []*entity.Artist{unknown},
		},
		{
			name:  "bulk insert multiple artists",
			setup: cleanDatabase,
			args:  args{artists: []*entity.Artist{bulkA, bulkB, bulkC}},
			want:  []*entity.Artist{bulkA, bulkB, bulkC},
		},
		{
			name:  "mixed MBID and no-MBID artists",
			setup: cleanDatabase,
			args:  args{artists: []*entity.Artist{withMBID, noMBID1, noMBID2}},
			want:  []*entity.Artist{withMBID, noMBID1, noMBID2},
		},
		{
			name:  "empty slice returns empty",
			setup: cleanDatabase,
			args:  args{artists: nil},
			want:  []*entity.Artist{},
		},
		{
			name:  "preserves pre-set ID",
			setup: cleanDatabase,
			args:  args{artists: []*entity.Artist{presetID}},
			want:  []*entity.Artist{presetID},
		},
		{
			name: "duplicate MBID returns original artist",
			setup: func() {
				cleanDatabase()
				_, err := repo.Create(ctx, entity.NewArtist("Original Name", "dedup-mbid-001"))
				require.NoError(t, err)
			},
			args: args{artists: []*entity.Artist{entity.NewArtist("Different Name", "dedup-mbid-001")}},
			want: []*entity.Artist{{Name: "Original Name", MBID: "dedup-mbid-001"}},
		},
		{
			// Regression: nil elements in the variadic slice must be skipped without panicking.
			name:  "nil elements are skipped",
			setup: cleanDatabase,
			args:  args{artists: []*entity.Artist{nil, entity.NewArtist("Non-nil Artist", ""), nil}},
			want:  []*entity.Artist{{Name: "Non-nil Artist", MBID: ""}},
		},
		{
			// Regression: interleaved MBID/no-MBID artists must be returned in original input order.
			// Previously the two groups were returned as MBID-batch first, no-MBID-batch second,
			// losing the relevance ranking from the external searcher.
			name:  "interleaved MBID and no-MBID artists preserve input order",
			setup: cleanDatabase,
			args: args{artists: []*entity.Artist{
				entity.NewArtist("First no-MBID", ""),
				entity.NewArtist("Second with MBID", "order-mbid-001"),
				entity.NewArtist("Third no-MBID", ""),
				entity.NewArtist("Fourth with MBID", "order-mbid-002"),
			}},
			want: []*entity.Artist{
				{Name: "First no-MBID", MBID: ""},
				{Name: "Second with MBID", MBID: "order-mbid-001"},
				{Name: "Third no-MBID", MBID: ""},
				{Name: "Fourth with MBID", MBID: "order-mbid-002"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup()
			}

			got, err := repo.Create(ctx, tt.args.artists...)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			require.Len(t, got, len(tt.want))
			for i, w := range tt.want {
				if w.ID != "" {
					assert.Equal(t, w.ID, got[i].ID)
				} else {
					assert.NotEmpty(t, got[i].ID)
				}
				assert.Equal(t, w.Name, got[i].Name)
				assert.Equal(t, w.MBID, got[i].MBID)
			}
		})
	}
}
