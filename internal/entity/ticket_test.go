package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestCreateTicket(t *testing.T) {
	t.Parallel()

	t.Run("set all fields from params and generate UUIDv7 ID", func(t *testing.T) {
		t.Parallel()

		params := &entity.NewTicket{
			EventID: "event-abc",
			UserID:  "user-xyz",
			TokenID: 42,
			TxHash:  "0xdeadbeef",
		}

		got := entity.CreateTicket(params)

		assert.NotEmpty(t, got.ID)
		assert.Equal(t, params.EventID, got.EventID)
		assert.Equal(t, params.UserID, got.UserID)
		assert.Equal(t, params.TokenID, got.TokenID)
		assert.Equal(t, params.TxHash, got.TxHash)
		// MintTime is set by the database layer, not the constructor.
		assert.True(t, got.MintTime.IsZero())
	})

	t.Run("generate different IDs on successive calls", func(t *testing.T) {
		t.Parallel()

		params := &entity.NewTicket{
			EventID: "event-abc",
			UserID:  "user-xyz",
			TokenID: 1,
			TxHash:  "0xabc",
		}

		first := entity.CreateTicket(params)
		second := entity.CreateTicket(params)

		assert.NotEqual(t, first.ID, second.ID)
	})
}

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
