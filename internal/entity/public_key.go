package entity

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"math/big"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

const (
	// PublicKeyLen is the length of an uncompressed SEC1 P-256 point
	// (0x04 || X || Y), the form WebCrypto exportKey("raw") returns.
	PublicKeyLen = 65
	// SignatureLen is the length of an IEEE P1363 P-256 signature (r || s),
	// the form WebCrypto sign() returns.
	SignatureLen = 64
)

// PublicKey is the public half of an ECDSA P-256 key pair that a device
// created with WebCrypto as a non-extractable signing key, encoded as the
// 65-byte uncompressed SEC1 point. Used both for a fan's [WalletPublicKey] and
// for the device a [ReceptionLink] is bound to.
//
// Mirrors liverty_music.entity.v1.PublicKey.
type PublicKey []byte

// Validate reports InvalidArgument unless the key is a valid point on the
// P-256 curve in uncompressed SEC1 form.
func (k PublicKey) Validate() error {
	if _, err := k.parse(); err != nil {
		return err
	}
	return nil
}

// Equal reports whether k and other hold the same bytes.
func (k PublicKey) Equal(other PublicKey) bool {
	return bytes.Equal(k, other)
}

// Base64URL returns the key as base64url without padding — the call content
// of a reception Open call.
func (k PublicKey) Base64URL() string {
	return base64.RawURLEncoding.EncodeToString(k)
}

// parse parses the key, rejecting anything that is not an on-curve P-256 point.
func (k PublicKey) parse() (*ecdsa.PublicKey, error) {
	if len(k) != PublicKeyLen || k[0] != 0x04 {
		return nil, apperr.New(codes.InvalidArgument, "public key must be a 65-byte uncompressed P-256 point")
	}
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), k)
	if err != nil {
		return nil, apperr.Wrap(err, codes.InvalidArgument, "public key is not a point on the P-256 curve")
	}
	return pub, nil
}

// Signature is an ECDSA P-256 / SHA-256 signature in the IEEE P1363 form
// r || s (64 bytes), as WebCrypto produces it. Not DER.
//
// Mirrors liverty_music.entity.v1.Signature.
type Signature []byte

// VerifySignature reports whether sig is a valid ECDSA P-256 / SHA-256
// signature of message under key. It never fails: a malformed key or
// signature simply does not verify.
func VerifySignature(key PublicKey, message []byte, sig Signature) bool {
	if len(sig) != SignatureLen {
		return false
	}
	pub, err := key.parse()
	if err != nil {
		return false
	}
	digest := sha256.Sum256(message)
	r := new(big.Int).SetBytes(sig[:SignatureLen/2])
	s := new(big.Int).SetBytes(sig[SignatureLen/2:])
	return ecdsa.Verify(pub, digest[:], r, s)
}
