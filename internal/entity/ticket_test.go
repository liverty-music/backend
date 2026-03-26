package entity_test

import (
	"fmt"
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

func TestValidateEthereumAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		addr    string
		wantErr error
	}{
		{
			name:    "valid checksummed address",
			addr:    "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD18",
			wantErr: nil,
		},
		{
			name:    "valid lowercase address",
			addr:    "0x742d35cc6634c0532925a3b844bc9e7595f2bd18",
			wantErr: nil,
		},
		{
			name:    "missing 0x prefix",
			addr:    "742d35cc6634c0532925a3b844bc9e7595f2bd18",
			wantErr: fmt.Errorf("invalid Ethereum address: must be 0x followed by 40 hex characters"),
		},
		{
			name:    "too short",
			addr:    "0x742d35cc",
			wantErr: fmt.Errorf("invalid Ethereum address: must be 0x followed by 40 hex characters"),
		},
		{
			name:    "empty string",
			addr:    "",
			wantErr: fmt.Errorf("invalid Ethereum address: must be 0x followed by 40 hex characters"),
		},
		{
			name:    "invalid hex characters",
			addr:    "0xZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ",
			wantErr: fmt.Errorf("invalid Ethereum address: must be 0x followed by 40 hex characters"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := entity.ValidateEthereumAddress(tt.addr)

			if tt.wantErr == nil {
				assert.NoError(t, got)
			} else {
				assert.EqualError(t, got, tt.wantErr.Error())
			}
		})
	}
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
