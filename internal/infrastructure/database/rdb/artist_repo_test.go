package rdb_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArtistRepository_Create(t *testing.T) {
	repo := rdb.NewArtistRepository(testDB)
	ctx := context.Background()

	// Pre-build reusable inputs so we can reference their generated IDs in want.
	beatles := entity.NewArtist("The Beatles", "b10bbbfc-cf9e-42e0-be17-e2c3e1d2600d")
	bulkA := entity.NewArtist("Artist A", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaa111")
	bulkB := entity.NewArtist("Artist B", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbb222")
	bulkC := entity.NewArtist("Artist C", "cccccccc-cccc-cccc-cccc-ccccccccc333")
	presetID := &entity.Artist{ID: "018b2f19-e591-7d12-bf9e-f0e74f1b4900", Name: "Pre-set ID Artist", MBID: "11111111-2222-3333-4444-55555preset1"}

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
			name:  "bulk insert multiple artists",
			setup: cleanDatabase,
			args:  args{artists: []*entity.Artist{bulkA, bulkB, bulkC}},
			want:  []*entity.Artist{bulkA, bulkB, bulkC},
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
				_, err := repo.Create(ctx, entity.NewArtist("Original Name", "eeeeeeee-eeee-eeee-eeee-eeeeeeeed001"))
				require.NoError(t, err)
			},
			args: args{artists: []*entity.Artist{entity.NewArtist("Different Name", "eeeeeeee-eeee-eeee-eeee-eeeeeeeed001")}},
			want: []*entity.Artist{{Name: "Original Name", MBID: "eeeeeeee-eeee-eeee-eeee-eeeeeeeed001"}},
		},
		{
			name:    "empty MBID returns error",
			setup:   cleanDatabase,
			args:    args{artists: []*entity.Artist{entity.NewArtist("Test", "")}},
			wantErr: apperr.ErrInvalidArgument,
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

func TestArtistRepository_ListByMBIDs(t *testing.T) {
	repo := rdb.NewArtistRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func()
		mbids   []string
		want    []*entity.Artist
		wantErr error
	}{
		{
			name: "returns matching artists in input order",
			setup: func() {
				cleanDatabase()
				_, err := repo.Create(ctx,
					entity.NewArtist("Artist A", "11111111-1111-1111-1111-11111111a001"),
					entity.NewArtist("Artist B", "22222222-2222-2222-2222-22222222b002"),
					entity.NewArtist("Artist C", "33333333-3333-3333-3333-33333333c003"),
				)
				require.NoError(t, err)
			},
			mbids: []string{"33333333-3333-3333-3333-33333333c003", "11111111-1111-1111-1111-11111111a001"},
			want: []*entity.Artist{
				{Name: "Artist C", MBID: "33333333-3333-3333-3333-33333333c003"},
				{Name: "Artist A", MBID: "11111111-1111-1111-1111-11111111a001"},
			},
		},
		{
			name: "unknown MBIDs are silently skipped",
			setup: func() {
				cleanDatabase()
				_, err := repo.Create(ctx, entity.NewArtist("Known", "44444444-4444-4444-4444-444444known1"))
				require.NoError(t, err)
			},
			mbids: []string{"44444444-4444-4444-4444-444444known1", "55555555-5555-5555-5555-5555unknown1"},
			want: []*entity.Artist{
				{Name: "Known", MBID: "44444444-4444-4444-4444-444444known1"},
			},
		},
		{
			name:  "empty input returns empty slice",
			setup: cleanDatabase,
			mbids: nil,
			want:  []*entity.Artist{},
		},
		{
			name: "all MBIDs unknown returns empty slice",
			setup: func() {
				cleanDatabase()
			},
			mbids: []string{"66666666-6666-6666-6666-666nonexist1"},
			want:  []*entity.Artist{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup()
			}

			got, err := repo.ListByMBIDs(ctx, tt.mbids)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			require.Len(t, got, len(tt.want))
			for i, w := range tt.want {
				assert.NotEmpty(t, got[i].ID)
				assert.Equal(t, w.Name, got[i].Name)
				assert.Equal(t, w.MBID, got[i].MBID)
			}
		})
	}
}

func TestArtistRepository_UpdateName(t *testing.T) {
	repo := rdb.NewArtistRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() string // returns artist ID
		newName string
		wantErr error
	}{
		{
			name: "updates name successfully",
			setup: func() string {
				cleanDatabase()
				created, err := repo.Create(ctx, entity.NewArtist("Old Name", "77777777-7777-7777-7777-77update0001"))
				require.NoError(t, err)
				return created[0].ID
			},
			newName: "New Name",
		},
		{
			name: "returns NotFound for unknown ID",
			setup: func() string {
				cleanDatabase()
				return "00000000-0000-0000-0000-000000000000"
			},
			newName: "Anything",
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artistID := tt.setup()

			err := repo.UpdateName(ctx, artistID, tt.newName)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)

			// Verify the name was actually updated.
			got, err := repo.Get(ctx, artistID)
			require.NoError(t, err)
			assert.Equal(t, tt.newName, got.Name)
		})
	}
}
