package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/testutil"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const openProcedure = "/liverty_music.rpc.organizer.reception.v1.ReceptionService/Open"

// receptionLinkFixture wires ReceptionLinkUseCase to mocks for one event
// owned by "org-owner", published, on 2026-11-20 opening 18:00 and starting
// 19:00 (window 15:00 to 04:00 the next day).
type receptionLinkFixture struct {
	uc         usecase.ReceptionLinkUseCase
	links      *entitymocks.MockReceptionLinkRepository
	events     *entitymocks.MockEventRepository
	eventState *ucmocks.MockEventPublishStatePort
	owner      *stubEventOrganizerRepo
	event      *entity.Event
}

func newReceptionLinkFixture(t *testing.T) *receptionLinkFixture {
	t.Helper()
	open, start := evening(18, 0, 0), evening(19, 0, 0)
	f := &receptionLinkFixture{
		links:      entitymocks.NewMockReceptionLinkRepository(t),
		events:     entitymocks.NewMockEventRepository(t),
		eventState: ucmocks.NewMockEventPublishStatePort(t),
		event: &entity.Event{
			ID:        entity.NewID(),
			LocalDate: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
			OpenTime:  &open,
			StartTime: &start,
		},
	}
	f.owner = &stubEventOrganizerRepo{getOrganizerIDFn: func(_ context.Context, eventID string) (string, error) {
		if eventID != f.event.ID {
			return "", apperr.New(codes.NotFound, "no event")
		}
		return "org-owner", nil
	}}
	f.uc = usecase.NewReceptionLinkUseCase(f.links, f.events, f.owner, f.eventState, newTestLogger(t))
	return f
}

func TestReceptionLinkUseCase_Issue(t *testing.T) {
	t.Parallel()

	t.Run("link issued the day before", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reception-link/issue "Link issued the day before"
		f := newReceptionLinkFixture(t)
		f.eventState.EXPECT().IsEventPublished(mock.Anything, f.event.ID).Return(true, nil)
		f.events.EXPECT().Get(mock.Anything, f.event.ID).Return(f.event, nil)
		f.links.EXPECT().Create(mock.Anything, mock.MatchedBy(func(l *entity.ReceptionLink) bool {
			return l.EventID == f.event.ID && l.Status == entity.ReceptionLinkStatusUnused && l.Token != ""
		})).RunAndReturn(func(_ context.Context, l *entity.ReceptionLink) (*entity.ReceptionLink, error) {
			created := *l
			created.Number = 1
			return &created, nil
		})

		got, err := f.uc.Issue(context.Background(), "org-owner", f.event.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, got.Number)
		assert.Equal(t, entity.ReceptionLinkStatusUnused, got.Status)
		assert.NotEmpty(t, got.Token)
	})

	t.Run("another organizer's event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reception-link/issue "Another organizer's event"
		f := newReceptionLinkFixture(t)
		_, err := f.uc.Issue(context.Background(), "org-other", f.event.ID)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		f.links.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})

	t.Run("unknown event is not revealed", func(t *testing.T) {
		t.Parallel()
		f := newReceptionLinkFixture(t)
		_, err := f.uc.Issue(context.Background(), "org-owner", entity.NewID())
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("draft event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reception-link/issue "Draft event"
		f := newReceptionLinkFixture(t)
		f.eventState.EXPECT().IsEventPublished(mock.Anything, f.event.ID).Return(false, nil)
		_, err := f.uc.Issue(context.Background(), "org-owner", f.event.ID)
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		f.links.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})

	t.Run("event without a start time", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reception-link/issue "Event without a start time"
		f := newReceptionLinkFixture(t)
		f.event.StartTime = nil
		f.eventState.EXPECT().IsEventPublished(mock.Anything, f.event.ID).Return(true, nil)
		f.events.EXPECT().Get(mock.Anything, f.event.ID).Return(f.event, nil)
		_, err := f.uc.Issue(context.Background(), "org-owner", f.event.ID)
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		f.links.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})
}

func TestReceptionLinkUseCase_ListByEvent(t *testing.T) {
	t.Parallel()

	t.Run("links of an event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reception-link/list-by-event "Links of an event"
		f := newReceptionLinkFixture(t)
		bound := evening(14, 10, 0)
		f.links.EXPECT().ListByEvent(mock.Anything, f.event.ID).Return([]*entity.ReceptionLink{
			// The token of an InUse link is cleared by the usecase even if a
			// store still returned one.
			{ID: "l1", EventID: f.event.ID, Number: 1, Token: "stale", Status: entity.ReceptionLinkStatusInUse, BoundTime: &bound},
			{ID: "l2", EventID: f.event.ID, Number: 2, Token: "tok-2", Status: entity.ReceptionLinkStatusUnused},
		}, nil)

		got, err := f.uc.ListByEvent(context.Background(), "org-owner", f.event.ID)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, 1, got[0].Number)
		assert.True(t, got[0].BoundTime.Equal(bound))
		assert.Empty(t, got[0].Token)
		assert.Equal(t, "tok-2", got[1].Token)
	})

	t.Run("another organizer's event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reception-link/list-by-event "Another organizer's event"
		f := newReceptionLinkFixture(t)
		_, err := f.uc.ListByEvent(context.Background(), "org-other", f.event.ID)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})
}

func TestReceptionLinkUseCase_Revoke(t *testing.T) {
	t.Parallel()

	now := evening(19, 10, 0)

	t.Run("device lost during the show", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reception-link/revoke "Device lost during the show"
		f := newReceptionLinkFixture(t)
		link := &entity.ReceptionLink{ID: "l1", EventID: f.event.ID, Number: 1, Status: entity.ReceptionLinkStatusInUse}
		f.links.EXPECT().Get(mock.Anything, link.ID).Return(link, nil)
		f.links.EXPECT().Revoke(mock.Anything, link.ID, now).
			Return(&entity.ReceptionLink{ID: "l1", EventID: f.event.ID, Number: 1, Status: entity.ReceptionLinkStatusRevoked, RevokedTime: &now}, nil)

		got, err := f.uc.Revoke(context.Background(), "org-owner", link.ID, now)
		require.NoError(t, err)
		assert.Equal(t, entity.ReceptionLinkStatusRevoked, got.Status)
		assert.True(t, got.RevokedTime.Equal(now))
		// The next scan through it is refused: see TestTicketUseCase_Admit_Caller
		// "revoked link" and the store-level "link revoked before the
		// admission" contract test.
	})

	t.Run("revoked twice", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reception-link/revoke "Revoked twice"
		f := newReceptionLinkFixture(t)
		earlier := evening(18, 5, 0)
		revoked := &entity.ReceptionLink{ID: "l1", EventID: f.event.ID, Number: 1, Status: entity.ReceptionLinkStatusRevoked, RevokedTime: &earlier}
		f.links.EXPECT().Get(mock.Anything, revoked.ID).Return(revoked, nil)
		f.links.EXPECT().Revoke(mock.Anything, revoked.ID, now).Return(revoked, nil)

		got, err := f.uc.Revoke(context.Background(), "org-owner", revoked.ID, now)
		require.NoError(t, err)
		assert.True(t, got.RevokedTime.Equal(earlier))
	})

	t.Run("another organizer's link", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reception-link/revoke "Another organizer's link"
		f := newReceptionLinkFixture(t)
		link := &entity.ReceptionLink{ID: "l1", EventID: f.event.ID, Status: entity.ReceptionLinkStatusInUse}
		f.links.EXPECT().Get(mock.Anything, link.ID).Return(link, nil)

		_, err := f.uc.Revoke(context.Background(), "org-other", link.ID, now)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		f.links.AssertNotCalled(t, "Revoke", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("unknown link is not revealed", func(t *testing.T) {
		t.Parallel()
		f := newReceptionLinkFixture(t)
		f.links.EXPECT().Get(mock.Anything, entity.ReceptionLinkID("nope")).Return(nil, apperr.New(codes.NotFound, "no link"))

		_, err := f.uc.Revoke(context.Background(), "org-owner", "nope", now)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})
}

func TestReceptionLinkUseCase_Open(t *testing.T) {
	t.Parallel()

	// openInput returns an Open call from device, signed at now.
	openInput := func(t *testing.T, device *testutil.DeviceKey, now time.Time) usecase.OpenReceptionLinkInput {
		t.Helper()
		key := device.PublicKey(t)
		call := device.SignCall(t, openProcedure, "tok-1", key.Base64URL(), now)
		return usecase.OpenReceptionLinkInput{
			Procedure: openProcedure,
			LinkToken: "tok-1",
			PublicKey: key,
			SignTime:  call.SignTime,
			Signature: call.Signature,
			Now:       now,
		}
	}
	unused := func(f *receptionLinkFixture) *entity.ReceptionLink {
		return &entity.ReceptionLink{ID: "l1", EventID: f.event.ID, Number: 1, Token: "tok-1", Status: entity.ReceptionLinkStatusUnused}
	}
	boundTo := func(t *testing.T, f *receptionLinkFixture, device *testutil.DeviceKey, at time.Time) *entity.ReceptionLink {
		t.Helper()
		return &entity.ReceptionLink{ID: "l1", EventID: f.event.ID, Number: 1, Status: entity.ReceptionLinkStatusInUse, BoundPublicKey: device.PublicKey(t), BoundTime: &at}
	}

	t.Run("staff open the link for the first time", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reception-link/open "Staff open the link for the first time"
		// @spec components/usecase/reception-link/open "Opened during the window"
		f := newReceptionLinkFixture(t)
		device := testutil.NewDeviceKey(t)
		now := evening(16, 0, 0)
		f.links.EXPECT().GetByToken(mock.Anything, "tok-1").Return(unused(f), nil)
		f.links.EXPECT().BindDevice(mock.Anything, entity.ReceptionLinkID("l1"), device.PublicKey(t), now).
			Return(entity.BindOutcomeBound, boundTo(t, f, device, now), nil)
		f.events.EXPECT().Get(mock.Anything, f.event.ID).Return(f.event, nil)

		got, err := f.uc.Open(context.Background(), openInput(t, device, now))
		require.NoError(t, err)
		assert.Equal(t, entity.ReceptionLinkStatusInUse, got.Link.Status)
		assert.Empty(t, got.Link.Token, "the token is never returned to the device")
		require.NotNil(t, got.Window)
		assert.True(t, got.Window.OpenTime.Equal(evening(15, 0, 0)))
		assert.True(t, got.InsideWindow)
	})

	t.Run("opened the day before", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reception-link/open "Opened the day before"
		f := newReceptionLinkFixture(t)
		device := testutil.NewDeviceKey(t)
		now := time.Date(2026, 11, 19, 14, 10, 0, 0, jst)
		f.links.EXPECT().GetByToken(mock.Anything, "tok-1").Return(unused(f), nil)
		f.links.EXPECT().BindDevice(mock.Anything, entity.ReceptionLinkID("l1"), device.PublicKey(t), now).
			Return(entity.BindOutcomeBound, boundTo(t, f, device, now), nil)
		f.events.EXPECT().Get(mock.Anything, f.event.ID).Return(f.event, nil)

		got, err := f.uc.Open(context.Background(), openInput(t, device, now))
		require.NoError(t, err)
		assert.False(t, got.InsideWindow)
		require.NotNil(t, got.Window)
		assert.True(t, got.Window.OpenTime.Equal(evening(15, 0, 0)))
	})

	t.Run("event without a start time has no window", func(t *testing.T) {
		t.Parallel()
		f := newReceptionLinkFixture(t)
		f.event.StartTime = nil
		device := testutil.NewDeviceKey(t)
		now := evening(16, 0, 0)
		f.links.EXPECT().GetByToken(mock.Anything, "tok-1").Return(unused(f), nil)
		f.links.EXPECT().BindDevice(mock.Anything, entity.ReceptionLinkID("l1"), device.PublicKey(t), now).
			Return(entity.BindOutcomeBound, boundTo(t, f, device, now), nil)
		f.events.EXPECT().Get(mock.Anything, f.event.ID).Return(f.event, nil)

		got, err := f.uc.Open(context.Background(), openInput(t, device, now))
		require.NoError(t, err)
		assert.Nil(t, got.Window)
		assert.False(t, got.InsideWindow)
	})

	t.Run("link forwarded to another device", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reception-link/open "Link forwarded to another device"
		f := newReceptionLinkFixture(t)
		first, second := testutil.NewDeviceKey(t), testutil.NewDeviceKey(t)
		now := evening(16, 0, 0)
		bound := boundTo(t, f, first, evening(14, 10, 0))
		f.links.EXPECT().GetByToken(mock.Anything, "tok-1").Return(bound, nil)
		f.links.EXPECT().BindDevice(mock.Anything, entity.ReceptionLinkID("l1"), second.PublicKey(t), now).
			Return(entity.BindOutcomeOtherDevice, bound, nil)

		_, err := f.uc.Open(context.Background(), openInput(t, second, now))
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("revoked link", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reception-link/open "Revoked link"
		f := newReceptionLinkFixture(t)
		device := testutil.NewDeviceKey(t)
		now := evening(16, 0, 0)
		revoked := &entity.ReceptionLink{ID: "l1", EventID: f.event.ID, Number: 1, Status: entity.ReceptionLinkStatusRevoked}
		f.links.EXPECT().GetByToken(mock.Anything, "tok-1").Return(revoked, nil)
		f.links.EXPECT().BindDevice(mock.Anything, entity.ReceptionLinkID("l1"), device.PublicKey(t), now).
			Return(entity.BindOutcomeRevoked, revoked, nil)

		_, err := f.uc.Open(context.Background(), openInput(t, device, now))
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		assert.NotErrorIs(t, err, usecase.ErrUnknownReceptionLinkToken)
	})

	t.Run("unknown token", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/reception-link/open "Unknown token"
		f := newReceptionLinkFixture(t)
		f.links.EXPECT().GetByToken(mock.Anything, "tok-1").Return(nil, apperr.New(codes.NotFound, "no link"))

		_, err := f.uc.Open(context.Background(), openInput(t, testutil.NewDeviceKey(t), evening(16, 0, 0)))
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		assert.ErrorIs(t, err, usecase.ErrUnknownReceptionLinkToken)
	})

	t.Run("call not proven by the key being bound is refused without binding", func(t *testing.T) {
		t.Parallel()
		f := newReceptionLinkFixture(t)
		device := testutil.NewDeviceKey(t)
		now := evening(16, 0, 0)
		in := openInput(t, device, now)
		in.PublicKey = testutil.NewDeviceKey(t).PublicKey(t) // claims a key it does not hold
		f.links.EXPECT().GetByToken(mock.Anything, "tok-1").Return(unused(f), nil)

		_, err := f.uc.Open(context.Background(), in)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		f.links.AssertNotCalled(t, "BindDevice", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("stale call from the bound device is refused", func(t *testing.T) {
		t.Parallel()
		f := newReceptionLinkFixture(t)
		device := testutil.NewDeviceKey(t)
		now := evening(16, 0, 0)
		in := openInput(t, device, now.Add(-time.Minute))
		in.Now = now
		bound := boundTo(t, f, device, evening(14, 10, 0))
		f.links.EXPECT().GetByToken(mock.Anything, "tok-1").Return(bound, nil)
		f.links.EXPECT().BindDevice(mock.Anything, entity.ReceptionLinkID("l1"), device.PublicKey(t), now).
			Return(entity.BindOutcomeBound, bound, nil)

		_, err := f.uc.Open(context.Background(), in)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("invalid public key", func(t *testing.T) {
		t.Parallel()
		f := newReceptionLinkFixture(t)
		in := openInput(t, testutil.NewDeviceKey(t), evening(16, 0, 0))
		in.PublicKey = append(entity.PublicKey{0x04}, make([]byte, 64)...)
		f.links.EXPECT().GetByToken(mock.Anything, "tok-1").Return(unused(f), nil)

		_, err := f.uc.Open(context.Background(), in)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestWalletPublicKeyUseCase_Register(t *testing.T) {
	t.Parallel()

	now := evening(10, 0, 0)
	user := entity.UserID(entity.NewID())

	t.Run("first time on a phone", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/wallet-public-key/register "First time on a phone"
		keys := entitymocks.NewMockWalletPublicKeyRepository(t)
		key := testutil.NewDeviceKey(t).PublicKey(t)
		keys.EXPECT().Register(mock.Anything, user, key, now).
			Return(&entity.WalletPublicKey{UserID: user, PublicKey: key, RegisteredTime: now}, false, nil)

		got, err := usecase.NewWalletPublicKeyUseCase(keys, newTestLogger(t)).Register(context.Background(), user, key, now)
		require.NoError(t, err)
		assert.Equal(t, key, got.Key.PublicKey)
		assert.True(t, got.Key.RegisteredTime.Equal(now))
		assert.False(t, got.ReplacedOtherKey)
	})

	t.Run("fan moves to a new phone", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/wallet-public-key/register "Fan moves to a new phone"
		keys := entitymocks.NewMockWalletPublicKeyRepository(t)
		key := testutil.NewDeviceKey(t).PublicKey(t)
		keys.EXPECT().Register(mock.Anything, user, key, now).
			Return(&entity.WalletPublicKey{UserID: user, PublicKey: key, RegisteredTime: now}, true, nil)

		got, err := usecase.NewWalletPublicKeyUseCase(keys, newTestLogger(t)).Register(context.Background(), user, key, now)
		require.NoError(t, err)
		assert.True(t, got.ReplacedOtherKey)
		// Codes from the old phone are then refused at the venue: see
		// TestTicketUseCase_Admit_Code "code from a replaced phone".
	})

	t.Run("not a P-256 key", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/wallet-public-key/register "Not a P-256 key"
		keys := entitymocks.NewMockWalletPublicKeyRepository(t)
		bad := append(entity.PublicKey{0x04}, make([]byte, 64)...)
		keys.EXPECT().Register(mock.Anything, user, bad, now).
			Return(nil, false, apperr.New(codes.InvalidArgument, "not a P-256 point"))

		_, err := usecase.NewWalletPublicKeyUseCase(keys, newTestLogger(t)).Register(context.Background(), user, bad, now)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}
