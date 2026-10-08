package entity_test

import (
	"crypto/rand"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/testutil"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicKey_Validate(t *testing.T) {
	t.Parallel()

	randomPoint := make([]byte, entity.PublicKeyLen)
	_, err := rand.Read(randomPoint)
	require.NoError(t, err)
	randomPoint[0] = 0x04 // right prefix, almost surely not on the curve

	compressed := make([]byte, 33)
	compressed[0] = 0x02

	tests := []struct {
		name    string
		key     entity.PublicKey
		wantErr bool
	}{
		// @spec components/entity/wallet-public-key "Key from a browser"
		{name: "exported key of a new P-256 key pair", key: testutil.NewDeviceKey(t).PublicKey(t), wantErr: false},
		// @spec components/entity/wallet-public-key "Not a curve point"
		{name: "65 random bytes", key: randomPoint, wantErr: true},
		{name: "empty", key: nil, wantErr: true},
		{name: "compressed point", key: compressed, wantErr: true},
		{name: "all zero uncompressed", key: append([]byte{0x04}, make([]byte, 64)...), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.key.Validate()
			if tt.wantErr {
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestVerifySignature(t *testing.T) {
	t.Parallel()

	key := testutil.NewDeviceKey(t)
	msg := []byte("liverty-music")
	sig := key.Sign(t, msg)

	assert.True(t, entity.VerifySignature(key.PublicKey(t), msg, sig))
	assert.False(t, entity.VerifySignature(key.PublicKey(t), []byte("changed"), sig), "changed message")
	assert.False(t, entity.VerifySignature(testutil.NewDeviceKey(t).PublicKey(t), msg, sig), "other key")
	assert.False(t, entity.VerifySignature(key.PublicKey(t), msg, sig[:63]), "short signature")
	assert.False(t, entity.VerifySignature(entity.PublicKey{0x04}, msg, sig), "malformed key")
}
