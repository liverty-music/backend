package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestGenerateTokenID(t *testing.T) {
	t.Parallel()

	t.Run("return non-zero token ID", func(t *testing.T) {
		t.Parallel()

		got, err := entity.GenerateTokenID()

		assert.NoError(t, err)
		assert.NotZero(t, got)
	})

	t.Run("second call returns value greater than or equal to first", func(t *testing.T) {
		t.Parallel()

		first, err := entity.GenerateTokenID()
		assert.NoError(t, err)

		second, err := entity.GenerateTokenID()
		assert.NoError(t, err)

		assert.GreaterOrEqual(t, second, first)
	})
}
