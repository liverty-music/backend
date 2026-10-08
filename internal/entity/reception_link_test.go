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

const admitProcedure = "/liverty_music.rpc.organizer.reception.v1.ReceptionService/Admit"

func TestNewReceptionLink(t *testing.T) {
	t.Parallel()

	// @spec components/entity/reception-link "New link"
	link := entity.NewReceptionLink("event-1")
	assert.Equal(t, entity.ReceptionLinkStatusUnused, link.Status)
	assert.NotEmpty(t, link.ID)
	assert.Nil(t, link.BoundPublicKey)
	assert.Nil(t, link.BoundTime)
	assert.Nil(t, link.RevokedTime)

	raw, err := base64.RawURLEncoding.DecodeString(link.Token)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(raw)*8, 128, "token carries at least 128 random bits")
	assert.NotEqual(t, link.Token, entity.NewReceptionLink("event-1").Token, "tokens are fresh")
}

func TestReceptionLink_IsUsable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status entity.ReceptionLinkStatus
		want   bool
	}{
		{name: "unused", status: entity.ReceptionLinkStatusUnused, want: true},
		// @spec components/entity/reception-link "Link in use"
		{name: "in use", status: entity.ReceptionLinkStatusInUse, want: true},
		// @spec components/entity/reception-link "Revoked link"
		{name: "revoked", status: entity.ReceptionLinkStatusRevoked, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, (&entity.ReceptionLink{Status: tt.status}).IsUsable())
		})
	}
}

func TestReceptionCall_SignatureInput(t *testing.T) {
	t.Parallel()

	call := entity.ReceptionCall{
		Procedure: admitProcedure,
		Token:     "tok_abc",
		Content:   "SCANNED",
		SignTime:  time.Unix(1_790_000_000, 999),
	}
	want := "liverty-music.reception.v1\n" + admitProcedure + "\ntok_abc\n1790000000\nSCANNED"
	assert.Equal(t, want, string(call.SignatureInput()))
}

func TestReceptionLink_Proves(t *testing.T) {
	t.Parallel()

	device := testutil.NewDeviceKey(t)
	bound := func(t *testing.T) *entity.ReceptionLink {
		t.Helper()
		return &entity.ReceptionLink{Token: "tok", Status: entity.ReceptionLinkStatusInUse, BoundPublicKey: device.PublicKey(t)}
	}

	tests := []struct {
		name string
		link func(t *testing.T) *entity.ReceptionLink
		call func(t *testing.T) entity.ReceptionCall
		now  time.Time
		want bool
	}{
		{
			// @spec components/entity/reception-link "Call from the bound device"
			name: "bound device signs at 18:30:00, checked at 18:30:01",
			link: bound,
			call: func(t *testing.T) entity.ReceptionCall {
				t.Helper()
				return device.SignCall(t, admitProcedure, "tok", "X", at(18, 30, 0))
			},
			now:  at(18, 30, 1),
			want: true,
		},
		{
			// @spec components/entity/reception-link "Call from another device"
			name: "signed with another key",
			link: bound,
			call: func(t *testing.T) entity.ReceptionCall {
				t.Helper()
				return testutil.NewDeviceKey(t).SignCall(t, admitProcedure, "tok", "X", at(18, 30, 0))
			},
			now:  at(18, 30, 1),
			want: false,
		},
		{
			// @spec components/entity/reception-link "Replayed call"
			name: "signed at 18:30:00, checked at 18:31:00",
			link: bound,
			call: func(t *testing.T) entity.ReceptionCall {
				t.Helper()
				return device.SignCall(t, admitProcedure, "tok", "X", at(18, 30, 0))
			},
			now:  at(18, 31, 0),
			want: false,
		},
		{
			name: "content changed after signing",
			link: bound,
			call: func(t *testing.T) entity.ReceptionCall {
				t.Helper()
				c := device.SignCall(t, admitProcedure, "tok", "X", at(18, 30, 0))
				c.Content = "Y"
				return c
			},
			now:  at(18, 30, 1),
			want: false,
		},
		{
			name: "signed for another procedure",
			link: bound,
			call: func(t *testing.T) entity.ReceptionCall {
				t.Helper()
				c := device.SignCall(t, "/other", "tok", "X", at(18, 30, 0))
				c.Procedure = admitProcedure
				return c
			},
			now:  at(18, 30, 1),
			want: false,
		},
		{
			name: "unused link never proves",
			link: func(t *testing.T) *entity.ReceptionLink {
				t.Helper()
				return &entity.ReceptionLink{Token: "tok", Status: entity.ReceptionLinkStatusUnused}
			},
			call: func(t *testing.T) entity.ReceptionCall {
				t.Helper()
				return device.SignCall(t, admitProcedure, "tok", "X", at(18, 30, 0))
			},
			now:  at(18, 30, 1),
			want: false,
		},
		{
			name: "revoked link never proves",
			link: func(t *testing.T) *entity.ReceptionLink {
				t.Helper()
				l := bound(t)
				l.Status = entity.ReceptionLinkStatusRevoked
				return l
			},
			call: func(t *testing.T) entity.ReceptionCall {
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
			assert.Equal(t, tt.want, tt.link(t).Proves(tt.call(t), tt.now))
		})
	}
}

func TestReceptionWindowOf(t *testing.T) {
	t.Parallel()

	date := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	ptr := func(t time.Time) *time.Time { return &t }

	t.Run("usual evening show", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/reception-link "Usual evening show"
		w, ok := entity.ReceptionWindowOf(&entity.Event{LocalDate: date, OpenTime: ptr(at(18, 0, 0)), StartTime: ptr(at(19, 0, 0))})
		require.True(t, ok)
		assert.True(t, w.OpenTime.Equal(at(15, 0, 0)))
		assert.True(t, w.CloseTime.Equal(time.Date(2026, 11, 21, 4, 0, 0, 0, jst)))
	})

	t.Run("no open time", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/reception-link "No open time"
		w, ok := entity.ReceptionWindowOf(&entity.Event{LocalDate: date, StartTime: ptr(at(19, 0, 0))})
		require.True(t, ok)
		assert.True(t, w.OpenTime.Equal(at(16, 0, 0)))
	})

	t.Run("no start time", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/reception-link "No start time"
		_, ok := entity.ReceptionWindowOf(&entity.Event{LocalDate: date, OpenTime: ptr(at(18, 0, 0))})
		assert.False(t, ok)
	})

	t.Run("close is in Japan time whatever the date's zone", func(t *testing.T) {
		t.Parallel()
		w, ok := entity.ReceptionWindowOf(&entity.Event{LocalDate: date, StartTime: ptr(time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC))})
		require.True(t, ok)
		assert.True(t, w.CloseTime.Equal(time.Date(2026, 11, 20, 19, 0, 0, 0, time.UTC)))
	})

	window := entity.ReceptionWindow{OpenTime: at(15, 0, 0), CloseTime: time.Date(2026, 11, 21, 4, 0, 0, 0, jst)}
	contains := []struct {
		name string
		at   time.Time
		want bool
	}{
		// @spec components/entity/reception-link "Before the window"
		{name: "14:59 before the opening", at: at(14, 59, 0), want: false},
		{name: "at the opening", at: at(15, 0, 0), want: true},
		{name: "during the show", at: at(20, 0, 0), want: true},
		// @spec components/entity/reception-link "Window closed"
		{name: "at the closing", at: time.Date(2026, 11, 21, 4, 0, 0, 0, jst), want: false},
	}
	for _, tt := range contains {
		t.Run("contains: "+tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, window.Contains(tt.at))
		})
	}
}
