package entity_test

import (
	"testing"
	"uuid"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewID_IsUUIDv7 pins the ID contract after migrating from
// github.com/google/uuid to the standard-library uuid package: NewID must keep
// producing canonical UUIDv7 strings so persisted primary keys stay
// format-compatible with every existing row.
func TestNewID_IsUUIDv7(t *testing.T) {
	t.Parallel()

	id := entity.NewID()

	// Canonical 8-4-4-4-12 textual form.
	require.Len(t, id, 36, "NewID() must be a canonical 36-char UUID")

	// Must round-trip through the standard-library parser.
	_, err := uuid.Parse(id)
	require.NoError(t, err, "NewID() must be a valid UUID")

	// The version nibble (first hex digit of the third group) must be 7.
	assert.Equal(t, byte('7'), id[14], "NewID() must be UUID version 7")
}

// TestNewID_Unique guards against a degenerate generator returning a constant.
func TestNewID_Unique(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		id := entity.NewID()
		_, dup := seen[id]
		require.Falsef(t, dup, "NewID() produced duplicate %q", id)
		seen[id] = struct{}{}
	}
}
