package testutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
)

// DeviceKey is an ECDSA P-256 key pair standing in for the WebCrypto key a
// fan's or reception device creates. It signs the way WebCrypto does: SHA-256
// and the IEEE P1363 r || s form.
type DeviceKey struct {
	priv *ecdsa.PrivateKey
}

// NewDeviceKey creates a fresh P-256 device key.
func NewDeviceKey(t *testing.T) *DeviceKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate P-256 key: %v", err)
	}
	return &DeviceKey{priv: priv}
}

// PublicKey returns the 65-byte uncompressed SEC1 public key, as WebCrypto
// exportKey("raw") returns it.
func (k *DeviceKey) PublicKey(t *testing.T) entity.PublicKey {
	t.Helper()
	b, err := k.priv.PublicKey.Bytes()
	if err != nil {
		t.Fatalf("encode public key: %v", err)
	}
	return entity.PublicKey(b)
}

// Sign returns the P1363 signature of message.
func (k *DeviceKey) Sign(t *testing.T, message []byte) entity.Signature {
	t.Helper()
	digest := sha256.Sum256(message)
	r, s, err := ecdsa.Sign(rand.Reader, k.priv, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	sig := make([]byte, entity.SignatureLen)
	r.FillBytes(sig[:entity.SignatureLen/2])
	s.FillBytes(sig[entity.SignatureLen/2:])
	return sig
}

// SignCode signs code in place over its payload and returns its Base45 text.
func (k *DeviceKey) SignCode(t *testing.T, code *entity.AdmissionCode) string {
	t.Helper()
	payload, err := code.SignedPayload()
	if err != nil {
		t.Fatalf("admission code payload: %v", err)
	}
	code.Signature = k.Sign(t, payload)
	text, err := code.Encode()
	if err != nil {
		t.Fatalf("encode admission code: %v", err)
	}
	return text
}

// SignCall returns a reception call signed by this key at signTime.
func (k *DeviceKey) SignCall(t *testing.T, procedure, token, content string, signTime time.Time) entity.ReceptionCall {
	t.Helper()
	call := entity.ReceptionCall{
		Procedure: procedure,
		Token:     token,
		Content:   content,
		SignTime:  signTime,
	}
	call.Signature = k.Sign(t, call.SignatureInput())
	return call
}
