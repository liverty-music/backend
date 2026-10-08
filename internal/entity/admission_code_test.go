package entity_test

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

var jst = time.FixedZone("JST", 9*60*60)

// at returns 2026-11-20 hh:mm:ss in Japan time.
func at(hh, mm, ss int) time.Time {
	return time.Date(2026, 11, 20, hh, mm, ss, 0, jst)
}

// newCode returns an unsigned code for a fresh user and event presenting n
// fresh tickets, signed at signed.
func newCode(n int, signed time.Time) *entity.AdmissionCode {
	code := &entity.AdmissionCode{
		UserID:     entity.UserID(entity.NewID()),
		EventID:    entity.NewID(),
		SignedTime: signed,
	}
	for range n {
		code.TicketIDs = append(code.TicketIDs, entity.TicketID(entity.NewID()))
	}
	return code
}

// rawPayload builds the binary AdmissionCode layout without validation, so
// tests can describe codes Encode refuses to make.
func rawPayload(version byte, user, event string, tickets []string, signed uint32, sigLen int) []byte {
	out := []byte{version}
	u := uuid.MustParse(user)
	e := uuid.MustParse(event)
	out = append(out, u[:]...)
	out = append(out, e[:]...)
	out = append(out, byte(len(tickets)))
	for _, id := range tickets {
		t := uuid.MustParse(id)
		out = append(out, t[:]...)
	}
	out = binary.BigEndian.AppendUint32(out, signed)
	return append(out, make([]byte, sigLen)...)
}

func TestAdmissionCode_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tickets int
		dup     bool
		wantErr bool
	}{
		// @spec components/entity/admission-code "Group of three"
		{name: "three distinct tickets of one event", tickets: 3, wantErr: false},
		{name: "ten tickets", tickets: 10, wantErr: false},
		// @spec components/entity/admission-code "Too many tickets"
		{name: "eleven tickets", tickets: 11, wantErr: true},
		{name: "no ticket", tickets: 0, wantErr: true},
		{name: "same ticket twice", tickets: 2, dup: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code := newCode(tt.tickets, at(18, 30, 0))
			if tt.dup {
				code.TicketIDs[1] = code.TicketIDs[0]
			}
			err := code.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestAdmissionCode_IsFreshAt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		signed time.Time
		now    time.Time
		want   bool
	}{
		// @spec components/entity/admission-code "Just shown"
		{name: "checked 10 seconds after signing", signed: at(18, 30, 0), now: at(18, 30, 10), want: true},
		{name: "checked exactly 30 seconds after signing", signed: at(18, 30, 0), now: at(18, 30, 30), want: true},
		// @spec components/entity/admission-code "Screenshot shown later"
		{name: "checked 31 seconds after signing", signed: at(18, 30, 0), now: at(18, 30, 31), want: false},
		// @spec components/entity/admission-code "Device clock slightly fast"
		{name: "signed 10 seconds ahead of the check", signed: at(18, 30, 10), now: at(18, 30, 0), want: true},
		{name: "signed exactly 15 seconds ahead", signed: at(18, 30, 15), now: at(18, 30, 0), want: true},
		// @spec components/entity/admission-code "Device clock far ahead"
		{name: "signed a minute ahead of the check", signed: at(18, 31, 0), now: at(18, 30, 0), want: false},
		{name: "signed 16 seconds ahead", signed: at(18, 30, 16), now: at(18, 30, 0), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code := newCode(1, tt.signed)
			assert.Equal(t, tt.want, code.IsFreshAt(tt.now))
		})
	}
}

func TestDecodeAdmissionCode(t *testing.T) {
	t.Parallel()

	key := testutil.NewDeviceKey(t)

	t.Run("code from the tickets screen", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/admission-code/decode "Code from the tickets screen"
		code := newCode(3, at(18, 30, 0))
		text := key.SignCode(t, code)

		got, ok := entity.DecodeAdmissionCode(text)
		require.True(t, ok)
		assert.Equal(t, code.UserID, got.UserID)
		assert.Equal(t, code.EventID, got.EventID)
		assert.Equal(t, code.TicketIDs, got.TicketIDs)
		assert.True(t, code.SignedTime.Equal(got.SignedTime))
		assert.Equal(t, code.Signature, got.Signature)
	})

	t.Run("payload sizes match the documented layout", func(t *testing.T) {
		t.Parallel()
		one := key.SignCode(t, newCode(1, at(18, 30, 0)))
		ten := key.SignCode(t, newCode(10, at(18, 30, 0)))
		// 118 bytes → 59 groups of 2 → 177 characters; 262 bytes → 393.
		assert.Len(t, one, 177)
		assert.Len(t, ten, 393)
	})

	user, event := entity.NewID(), entity.NewID()
	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = entity.NewID()
	}
	ticket := entity.NewID()
	signed := uint32(at(18, 30, 0).Unix())

	malformed := []struct {
		name string
		text string
	}{
		// @spec components/entity/admission-code/decode "Ordinary QR code"
		{name: "web address", text: "https://example.com/tickets"},
		// @spec components/entity/admission-code/decode "Too many tickets"
		{name: "eleven tickets", text: entity.Base45Encode(rawPayload(0x01, user, event, eleven, signed, entity.SignatureLen))},
		{name: "empty text", text: ""},
		{name: "not base45 alphabet", text: "abc"},
		{name: "dangling base45 character", text: "AAAA"},
		{name: "base45 group overflow", text: ":::"},
		{name: "unknown version", text: entity.Base45Encode(rawPayload(0x02, user, event, []string{ticket}, signed, entity.SignatureLen))},
		{name: "no ticket", text: entity.Base45Encode(rawPayload(0x01, user, event, nil, signed, entity.SignatureLen))},
		{name: "repeated ticket", text: entity.Base45Encode(rawPayload(0x01, user, event, []string{ticket, ticket}, signed, entity.SignatureLen))},
		{name: "signature too short", text: entity.Base45Encode(rawPayload(0x01, user, event, []string{ticket}, signed, entity.SignatureLen-1))},
		{name: "trailing byte", text: entity.Base45Encode(rawPayload(0x01, user, event, []string{ticket}, signed, entity.SignatureLen+1))},
	}
	for _, tt := range malformed {
		t.Run("malformed: "+tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := entity.DecodeAdmissionCode(tt.text)
			assert.False(t, ok)
			assert.Nil(t, got)
		})
	}
}

func TestAdmissionCode_Verify(t *testing.T) {
	t.Parallel()

	device := testutil.NewDeviceKey(t)

	// signedCode returns a genuine code signed by device at 18:30:00 and the
	// user's WalletPublicKey for device.
	signedCode := func(t *testing.T) (*entity.AdmissionCode, *entity.WalletPublicKey) {
		t.Helper()
		code := newCode(2, at(18, 30, 0))
		text := device.SignCode(t, code)
		decoded, ok := entity.DecodeAdmissionCode(text)
		require.True(t, ok)
		return decoded, &entity.WalletPublicKey{UserID: decoded.UserID, PublicKey: device.PublicKey(t)}
	}

	tests := []struct {
		name   string
		mutate func(t *testing.T, code *entity.AdmissionCode, key *entity.WalletPublicKey)
		now    time.Time
		want   entity.AdmissionCodeVerdict
	}{
		{
			// @spec components/entity/admission-code/verify "Code just shown"
			name: "genuine code verified 5 seconds later",
			now:  at(18, 30, 5),
			want: entity.AdmissionCodeValid,
		},
		{
			// @spec components/entity/admission-code/verify "Screenshot shown later"
			name: "genuine code verified a minute later",
			now:  at(18, 31, 0),
			want: entity.AdmissionCodeExpired,
		},
		{
			name: "genuine code signed far ahead by a fast clock",
			mutate: func(t *testing.T, code *entity.AdmissionCode, _ *entity.WalletPublicKey) {
				t.Helper()
				code.SignedTime = at(18, 31, 0)
				device.SignCode(t, code)
			},
			now:  at(18, 30, 0),
			want: entity.AdmissionCodeExpired,
		},
		{
			// @spec components/entity/admission-code/verify "Ticket added to a genuine code"
			name: "ticket added after signing",
			mutate: func(t *testing.T, code *entity.AdmissionCode, _ *entity.WalletPublicKey) {
				t.Helper()
				code.TicketIDs = append(code.TicketIDs, entity.TicketID(entity.NewID()))
			},
			now:  at(18, 30, 5),
			want: entity.AdmissionCodeForged,
		},
		{
			name: "signed time changed after signing",
			mutate: func(t *testing.T, code *entity.AdmissionCode, _ *entity.WalletPublicKey) {
				t.Helper()
				code.SignedTime = code.SignedTime.Add(time.Minute)
			},
			now:  at(18, 31, 5),
			want: entity.AdmissionCodeForged,
		},
		{
			// @spec components/entity/admission-code/verify "Code from a replaced device"
			name: "verified with the user's new device key",
			mutate: func(t *testing.T, _ *entity.AdmissionCode, key *entity.WalletPublicKey) {
				t.Helper()
				key.PublicKey = testutil.NewDeviceKey(t).PublicKey(t)
			},
			now:  at(18, 30, 5),
			want: entity.AdmissionCodeForged,
		},
		{
			name: "key of another user",
			mutate: func(t *testing.T, _ *entity.AdmissionCode, key *entity.WalletPublicKey) {
				t.Helper()
				key.UserID = entity.UserID(entity.NewID())
			},
			now:  at(18, 30, 5),
			want: entity.AdmissionCodeForged,
		},
		{
			name: "corrupted signature",
			mutate: func(t *testing.T, code *entity.AdmissionCode, _ *entity.WalletPublicKey) {
				t.Helper()
				code.Signature = append(entity.Signature(nil), code.Signature...)
				code.Signature[0] ^= 0xFF
			},
			now:  at(18, 30, 5),
			want: entity.AdmissionCodeForged,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, key := signedCode(t)
			if tt.mutate != nil {
				tt.mutate(t, code, key)
			}
			assert.Equal(t, tt.want, code.Verify(key, tt.now))
		})
	}

	t.Run("no key", func(t *testing.T) {
		t.Parallel()
		code, _ := signedCode(t)
		assert.Equal(t, entity.AdmissionCodeForged, code.Verify(nil, at(18, 30, 5)))
	})
}
