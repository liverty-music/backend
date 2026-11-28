package main

import (
	"bytes"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestParseArtistCommand(t *testing.T) {
	t.Parallel()

	t.Run("correct-mbid reports the corrected artist", func(t *testing.T) {
		t.Parallel()
		run, err := parseArtistCommand([]string{"correct-mbid", "artist-1", "bbbbbbbb-0000-0000-0000-00000000000b"})
		require.NoError(t, err)
		uc := mocks.NewMockArtistUseCase(t)
		uc.EXPECT().CorrectMBID(mock.Anything, "artist-1", "bbbbbbbb-0000-0000-0000-00000000000b").Return(&entity.Artist{
			ID:         "artist-1",
			Name:       "Penthouse",
			MBID:       "bbbbbbbb-0000-0000-0000-00000000000b",
			OtherMBIDs: []string{"aaaaaaaa-0000-0000-0000-00000000000a"},
		}, nil).Once()

		got, err := run(t.Context(), uc)

		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"artist_id":      "artist-1",
			"name":           "Penthouse",
			"mbid":           "bbbbbbbb-0000-0000-0000-00000000000b",
			"other_mbids":    []string{"aaaaaaaa-0000-0000-0000-00000000000a"},
			"activity_ended": false,
		}, got)
	})

	t.Run("a typo fails before anything connects", func(t *testing.T) {
		t.Parallel()
		for _, args := range [][]string{
			{"correct-mbid"},
			{"correct-mbid", "artist-1"},
			{"correct-mbid", "artist-1", "mbid", "extra"},
			{"correct-mbd", "artist-1", "mbid"},
		} {
			_, err := parseArtistCommand(args)
			assert.Error(t, err, "%v", args)
		}
	})
}

func TestRunArtistCommand_Usage(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer

	code := runArtistCommand(nil, &stdout, &stderr)

	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "correct-mbid ARTIST_ID MBID")
	assert.Empty(t, stdout.String())
}
