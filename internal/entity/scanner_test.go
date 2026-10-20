package entity_test

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const admitProcedure = "/liverty_music.rpc.scanner.v1.ScannerService/Admit"

func TestNewScanner(t *testing.T) {
	t.Parallel()

	// @spec components/entity/scanner "New scanner"
	scanner := entity.NewScanner("event-1")
	assert.Equal(t, entity.ScannerStatusUnused, scanner.Status)
	assert.NotEmpty(t, scanner.ID)
	assert.Nil(t, scanner.BoundPublicKey)
	assert.Nil(t, scanner.BoundTime)
	assert.Nil(t, scanner.RevokedTime)

	raw, err := base64.RawURLEncoding.DecodeString(scanner.LinkToken)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(raw)*8, 128, "link token carries at least 128 random bits")
	assert.NotEqual(t, scanner.LinkToken, entity.NewScanner("event-1").LinkToken, "link tokens are fresh")
}

func TestScanner_IsUsable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status entity.ScannerStatus
		want   bool
	}{
		{name: "unused", status: entity.ScannerStatusUnused, want: true},
		// @spec components/entity/scanner "Scanner in use"
		{name: "in use", status: entity.ScannerStatusInUse, want: true},
		// @spec components/entity/scanner "Revoked scanner"
		{name: "revoked", status: entity.ScannerStatusRevoked, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, (&entity.Scanner{Status: tt.status}).IsUsable())
		})
	}
}

func TestScannerCall_SignatureInput(t *testing.T) {
	t.Parallel()

	call := entity.ScannerCall{
		Procedure: admitProcedure,
		LinkToken: "tok_abc",
		Content:   "SCANNED",
		SignTime:  time.Unix(1_790_000_000, 999),
	}
	want := "liverty-music.scanner.v1\n" + admitProcedure + "\ntok_abc\n1790000000\nSCANNED"
	assert.Equal(t, want, string(call.SignatureInput()))
}

func TestScanner_Proves(t *testing.T) {
	t.Parallel()

	device := testutil.NewDeviceKey(t)
	bound := func(t *testing.T) *entity.Scanner {
		t.Helper()
		return &entity.Scanner{LinkToken: "tok", Status: entity.ScannerStatusInUse, BoundPublicKey: device.PublicKey(t)}
	}

	tests := []struct {
		name    string
		scanner func(t *testing.T) *entity.Scanner
		call    func(t *testing.T) entity.ScannerCall
		now     time.Time
		want    bool
	}{
		{
			// @spec components/entity/scanner "Call from the bound device"
			name:    "bound device signs at 18:30:00, checked at 18:30:01",
			scanner: bound,
			call: func(t *testing.T) entity.ScannerCall {
				t.Helper()
				return device.SignCall(t, admitProcedure, "tok", "X", at(18, 30, 0))
			},
			now:  at(18, 30, 1),
			want: true,
		},
		{
			// @spec components/entity/scanner "Call from another device"
			name:    "signed with another key",
			scanner: bound,
			call: func(t *testing.T) entity.ScannerCall {
				t.Helper()
				return testutil.NewDeviceKey(t).SignCall(t, admitProcedure, "tok", "X", at(18, 30, 0))
			},
			now:  at(18, 30, 1),
			want: false,
		},
		{
			// @spec components/entity/scanner "Replayed call"
			name:    "signed at 18:30:00, checked at 18:31:00",
			scanner: bound,
			call: func(t *testing.T) entity.ScannerCall {
				t.Helper()
				return device.SignCall(t, admitProcedure, "tok", "X", at(18, 30, 0))
			},
			now:  at(18, 31, 0),
			want: false,
		},
		{
			name:    "content changed after signing",
			scanner: bound,
			call: func(t *testing.T) entity.ScannerCall {
				t.Helper()
				c := device.SignCall(t, admitProcedure, "tok", "X", at(18, 30, 0))
				c.Content = "Y"
				return c
			},
			now:  at(18, 30, 1),
			want: false,
		},
		{
			name:    "signed for another procedure",
			scanner: bound,
			call: func(t *testing.T) entity.ScannerCall {
				t.Helper()
				c := device.SignCall(t, "/other", "tok", "X", at(18, 30, 0))
				c.Procedure = admitProcedure
				return c
			},
			now:  at(18, 30, 1),
			want: false,
		},
		{
			name: "unused scanner never proves",
			scanner: func(t *testing.T) *entity.Scanner {
				t.Helper()
				return &entity.Scanner{LinkToken: "tok", Status: entity.ScannerStatusUnused}
			},
			call: func(t *testing.T) entity.ScannerCall {
				t.Helper()
				return device.SignCall(t, admitProcedure, "tok", "X", at(18, 30, 0))
			},
			now:  at(18, 30, 1),
			want: false,
		},
		{
			name: "revoked scanner never proves",
			scanner: func(t *testing.T) *entity.Scanner {
				t.Helper()
				s := bound(t)
				s.Status = entity.ScannerStatusRevoked
				return s
			},
			call: func(t *testing.T) entity.ScannerCall {
				t.Helper()
				return device.SignCall(t, admitProcedure, "tok", "X", at(18, 30, 0))
			},
			now:  at(18, 30, 1),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.scanner(t).Proves(tt.call(t), tt.now))
		})
	}
}

func TestAdmissionWindowOf(t *testing.T) {
	t.Parallel()

	date := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	ptr := func(t time.Time) *time.Time { return &t }

	t.Run("usual evening show", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/scanner "Usual evening show"
		w, ok := entity.AdmissionWindowOf(&entity.Event{LocalDate: date, OpenTime: ptr(at(18, 0, 0)), StartTime: ptr(at(19, 0, 0))})
		require.True(t, ok)
		assert.True(t, w.OpenTime.Equal(at(16, 0, 0)))
		assert.True(t, w.CloseTime.Equal(time.Date(2026, 11, 21, 4, 0, 0, 0, jst)))
	})

	t.Run("no open time", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/scanner "No open time"
		w, ok := entity.AdmissionWindowOf(&entity.Event{LocalDate: date, StartTime: ptr(at(19, 0, 0))})
		require.True(t, ok)
		assert.True(t, w.OpenTime.Equal(at(16, 0, 0)))
	})

	t.Run("no start time", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/scanner "No start time"
		_, ok := entity.AdmissionWindowOf(&entity.Event{LocalDate: date, OpenTime: ptr(at(18, 0, 0))})
		assert.False(t, ok)
	})

	t.Run("close is in Japan time whatever the date's zone", func(t *testing.T) {
		t.Parallel()
		w, ok := entity.AdmissionWindowOf(&entity.Event{LocalDate: date, StartTime: ptr(time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC))})
		require.True(t, ok)
		assert.True(t, w.CloseTime.Equal(time.Date(2026, 11, 20, 19, 0, 0, 0, time.UTC)))
	})

	window := entity.AdmissionWindow{OpenTime: at(16, 0, 0), CloseTime: time.Date(2026, 11, 21, 4, 0, 0, 0, jst)}
	contains := []struct {
		name string
		at   time.Time
		want bool
	}{
		// @spec components/entity/scanner "Before the window"
		{name: "15:59 before the opening", at: at(15, 59, 0), want: false},
		{name: "at the opening", at: at(16, 0, 0), want: true},
		{name: "during the show", at: at(20, 0, 0), want: true},
		// @spec components/entity/scanner "Window closed"
		{name: "at the closing", at: time.Date(2026, 11, 21, 4, 0, 0, 0, jst), want: false},
	}
	for _, tt := range contains {
		t.Run("contains: "+tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, window.Contains(tt.at))
		})
	}
}
