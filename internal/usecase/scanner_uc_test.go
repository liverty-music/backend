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

const openProcedure = "/liverty_music.rpc.scanner.v1.ScannerService/Open"

// scannerFixture wires ScannerUseCase to mocks for one event
// owned by "org-owner", published, on 2026-11-20 opening 18:00 and starting
// 19:00 (window 16:00 to 04:00 the next day).
type scannerFixture struct {
	uc         usecase.ScannerUseCase
	scanners   *entitymocks.MockScannerRepository
	events     *entitymocks.MockEventRepository
	eventState *ucmocks.MockEventPublishStatePort
	owner      *stubEventOrganizerRepo
	event      *entity.Event
}

func newScannerFixture(t *testing.T) *scannerFixture {
	t.Helper()
	open, start := evening(18, 0, 0), evening(19, 0, 0)
	f := &scannerFixture{
		scanners:   entitymocks.NewMockScannerRepository(t),
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
	f.uc = usecase.NewScannerUseCase(f.scanners, f.events, f.owner, f.eventState, newTestLogger(t))
	return f
}

func TestScannerUseCase_Create(t *testing.T) {
	t.Parallel()

	t.Run("scanner created the day before", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/scanner/create "Scanner created the day before"
		f := newScannerFixture(t)
		f.eventState.EXPECT().IsEventPublished(mock.Anything, f.event.ID).Return(true, nil)
		f.events.EXPECT().Get(mock.Anything, f.event.ID).Return(f.event, nil)
		f.scanners.EXPECT().Create(mock.Anything, mock.MatchedBy(func(l *entity.Scanner) bool {
			return l.EventID == f.event.ID && l.Status == entity.ScannerStatusUnused && l.LinkToken != ""
		})).RunAndReturn(func(_ context.Context, l *entity.Scanner) (*entity.Scanner, error) {
			created := *l
			created.Number = 1
			return &created, nil
		})

		got, err := f.uc.Create(context.Background(), "org-owner", f.event.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, got.Number)
		assert.Equal(t, entity.ScannerStatusUnused, got.Status)
		assert.NotEmpty(t, got.LinkToken)
	})

	t.Run("another organizer's event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/scanner/create "Another Organizer's event"
		f := newScannerFixture(t)
		_, err := f.uc.Create(context.Background(), "org-other", f.event.ID)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		f.scanners.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})

	t.Run("unknown event is not revealed", func(t *testing.T) {
		t.Parallel()
		f := newScannerFixture(t)
		_, err := f.uc.Create(context.Background(), "org-owner", entity.NewID())
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("draft event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/scanner/create "Draft event"
		f := newScannerFixture(t)
		f.eventState.EXPECT().IsEventPublished(mock.Anything, f.event.ID).Return(false, nil)
		_, err := f.uc.Create(context.Background(), "org-owner", f.event.ID)
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		f.scanners.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})

	t.Run("event without a start time", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/scanner/create "Event without a start time"
		f := newScannerFixture(t)
		f.event.StartTime = nil
		f.eventState.EXPECT().IsEventPublished(mock.Anything, f.event.ID).Return(true, nil)
		f.events.EXPECT().Get(mock.Anything, f.event.ID).Return(f.event, nil)
		_, err := f.uc.Create(context.Background(), "org-owner", f.event.ID)
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		f.scanners.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})
}

func TestScannerUseCase_ListByEvent(t *testing.T) {
	t.Parallel()

	t.Run("scanners of an event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/scanner/list-by-event "Scanners of an event"
		f := newScannerFixture(t)
		bound := evening(14, 10, 0)
		f.scanners.EXPECT().ListByEvent(mock.Anything, f.event.ID).Return([]*entity.Scanner{
			// The link token of an InUse scanner is cleared by the usecase even if a
			// store still returned one.
			{ID: "l1", EventID: f.event.ID, Number: 1, LinkToken: "stale", Status: entity.ScannerStatusInUse, BoundTime: &bound},
			{ID: "l2", EventID: f.event.ID, Number: 2, LinkToken: "tok-2", Status: entity.ScannerStatusUnused},
		}, nil)

		got, err := f.uc.ListByEvent(context.Background(), "org-owner", f.event.ID)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, 1, got[0].Number)
		assert.True(t, got[0].BoundTime.Equal(bound))
		assert.Empty(t, got[0].LinkToken)
		assert.Equal(t, "tok-2", got[1].LinkToken)
	})

	t.Run("another organizer's event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/scanner/list-by-event "Another Organizer's event"
		f := newScannerFixture(t)
		_, err := f.uc.ListByEvent(context.Background(), "org-other", f.event.ID)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})
}

func TestScannerUseCase_Revoke(t *testing.T) {
	t.Parallel()

	now := evening(19, 10, 0)

	t.Run("device lost during the show", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/scanner/revoke "Device lost during the show"
		f := newScannerFixture(t)
		scanner := &entity.Scanner{ID: "l1", EventID: f.event.ID, Number: 1, Status: entity.ScannerStatusInUse}
		f.scanners.EXPECT().Get(mock.Anything, scanner.ID).Return(scanner, nil)
		f.scanners.EXPECT().Revoke(mock.Anything, scanner.ID, now).
			Return(&entity.Scanner{ID: "l1", EventID: f.event.ID, Number: 1, Status: entity.ScannerStatusRevoked, RevokedTime: &now}, nil)

		got, err := f.uc.Revoke(context.Background(), "org-owner", scanner.ID, now)
		require.NoError(t, err)
		assert.Equal(t, entity.ScannerStatusRevoked, got.Status)
		assert.True(t, got.RevokedTime.Equal(now))
		// The next scan through it is refused: see TestTicketUseCase_Admit_Caller
		// "revoked scanner" and the store-level "scanner revoked before the
		// admission" contract test.
	})

	t.Run("revoked twice", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/scanner/revoke "Revoked twice"
		f := newScannerFixture(t)
		earlier := evening(18, 5, 0)
		revoked := &entity.Scanner{ID: "l1", EventID: f.event.ID, Number: 1, Status: entity.ScannerStatusRevoked, RevokedTime: &earlier}
		f.scanners.EXPECT().Get(mock.Anything, revoked.ID).Return(revoked, nil)
		f.scanners.EXPECT().Revoke(mock.Anything, revoked.ID, now).Return(revoked, nil)

		got, err := f.uc.Revoke(context.Background(), "org-owner", revoked.ID, now)
		require.NoError(t, err)
		assert.True(t, got.RevokedTime.Equal(earlier))
	})

	t.Run("another organizer's scanner", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/scanner/revoke "Another Organizer's scanner"
		f := newScannerFixture(t)
		scanner := &entity.Scanner{ID: "l1", EventID: f.event.ID, Status: entity.ScannerStatusInUse}
		f.scanners.EXPECT().Get(mock.Anything, scanner.ID).Return(scanner, nil)

		_, err := f.uc.Revoke(context.Background(), "org-other", scanner.ID, now)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		f.scanners.AssertNotCalled(t, "Revoke", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("unknown scanner is not revealed", func(t *testing.T) {
		t.Parallel()
		f := newScannerFixture(t)
		f.scanners.EXPECT().Get(mock.Anything, entity.ScannerID("nope")).Return(nil, apperr.New(codes.NotFound, "no scanner"))

		_, err := f.uc.Revoke(context.Background(), "org-owner", "nope", now)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})
}

func TestScannerUseCase_Open(t *testing.T) {
	t.Parallel()

	// openInput returns an Open call from device, signed at now.
	openInput := func(t *testing.T, device *testutil.DeviceKey, now time.Time) usecase.OpenScannerInput {
		t.Helper()
		key := device.PublicKey(t)
		call := device.SignCall(t, openProcedure, "tok-1", key.Base64URL(), now)
		return usecase.OpenScannerInput{
			Procedure: openProcedure,
			LinkToken: "tok-1",
			PublicKey: key,
			SignTime:  call.SignTime,
			Signature: call.Signature,
			Now:       now,
		}
	}
	unused := func(f *scannerFixture) *entity.Scanner {
		return &entity.Scanner{ID: "l1", EventID: f.event.ID, Number: 1, LinkToken: "tok-1", Status: entity.ScannerStatusUnused}
	}
	boundTo := func(t *testing.T, f *scannerFixture, device *testutil.DeviceKey, at time.Time) *entity.Scanner {
		t.Helper()
		return &entity.Scanner{ID: "l1", EventID: f.event.ID, Number: 1, Status: entity.ScannerStatusInUse, BoundPublicKey: device.PublicKey(t), BoundTime: &at}
	}

	t.Run("staff open the link for the first time", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/scanner/open "Staff open the link for the first time"
		// @spec components/usecase/scanner/open "Opened during the window"
		f := newScannerFixture(t)
		device := testutil.NewDeviceKey(t)
		now := evening(16, 0, 0)
		f.scanners.EXPECT().GetByLinkToken(mock.Anything, "tok-1").Return(unused(f), nil)
		f.scanners.EXPECT().BindDevice(mock.Anything, entity.ScannerID("l1"), device.PublicKey(t), now).
			Return(entity.BindOutcomeBound, boundTo(t, f, device, now), nil)
		f.events.EXPECT().Get(mock.Anything, f.event.ID).Return(f.event, nil)

		got, err := f.uc.Open(context.Background(), openInput(t, device, now))
		require.NoError(t, err)
		assert.Equal(t, entity.ScannerStatusInUse, got.Scanner.Status)
		assert.Empty(t, got.Scanner.LinkToken, "the link token is never returned to the device")
		require.NotNil(t, got.Window)
		assert.True(t, got.Window.OpenTime.Equal(evening(16, 0, 0)))
		assert.True(t, got.InsideWindow)
	})

	t.Run("opened the day before", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/scanner/open "Opened the day before"
		f := newScannerFixture(t)
		device := testutil.NewDeviceKey(t)
		now := time.Date(2026, 11, 19, 14, 10, 0, 0, jst)
		f.scanners.EXPECT().GetByLinkToken(mock.Anything, "tok-1").Return(unused(f), nil)
		f.scanners.EXPECT().BindDevice(mock.Anything, entity.ScannerID("l1"), device.PublicKey(t), now).
			Return(entity.BindOutcomeBound, boundTo(t, f, device, now), nil)
		f.events.EXPECT().Get(mock.Anything, f.event.ID).Return(f.event, nil)

		got, err := f.uc.Open(context.Background(), openInput(t, device, now))
		require.NoError(t, err)
		assert.False(t, got.InsideWindow)
		require.NotNil(t, got.Window)
		assert.True(t, got.Window.OpenTime.Equal(evening(16, 0, 0)))
	})

	t.Run("event without a start time has no window", func(t *testing.T) {
		t.Parallel()
		f := newScannerFixture(t)
		f.event.StartTime = nil
		device := testutil.NewDeviceKey(t)
		now := evening(16, 0, 0)
		f.scanners.EXPECT().GetByLinkToken(mock.Anything, "tok-1").Return(unused(f), nil)
		f.scanners.EXPECT().BindDevice(mock.Anything, entity.ScannerID("l1"), device.PublicKey(t), now).
			Return(entity.BindOutcomeBound, boundTo(t, f, device, now), nil)
		f.events.EXPECT().Get(mock.Anything, f.event.ID).Return(f.event, nil)

		got, err := f.uc.Open(context.Background(), openInput(t, device, now))
		require.NoError(t, err)
		assert.Nil(t, got.Window)
		assert.False(t, got.InsideWindow)
	})

	t.Run("link forwarded to another device", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/scanner/open "Link forwarded to another device"
		f := newScannerFixture(t)
		first, second := testutil.NewDeviceKey(t), testutil.NewDeviceKey(t)
		now := evening(16, 0, 0)
		bound := boundTo(t, f, first, evening(14, 10, 0))
		f.scanners.EXPECT().GetByLinkToken(mock.Anything, "tok-1").Return(bound, nil)
		f.scanners.EXPECT().BindDevice(mock.Anything, entity.ScannerID("l1"), second.PublicKey(t), now).
			Return(entity.BindOutcomeOtherDevice, bound, nil)

		_, err := f.uc.Open(context.Background(), openInput(t, second, now))
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("revoked scanner", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/scanner/open "Revoked scanner"
		f := newScannerFixture(t)
		device := testutil.NewDeviceKey(t)
		now := evening(16, 0, 0)
		revoked := &entity.Scanner{ID: "l1", EventID: f.event.ID, Number: 1, Status: entity.ScannerStatusRevoked}
		f.scanners.EXPECT().GetByLinkToken(mock.Anything, "tok-1").Return(revoked, nil)
		f.scanners.EXPECT().BindDevice(mock.Anything, entity.ScannerID("l1"), device.PublicKey(t), now).
			Return(entity.BindOutcomeRevoked, revoked, nil)

		_, err := f.uc.Open(context.Background(), openInput(t, device, now))
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		assert.NotErrorIs(t, err, usecase.ErrUnknownScannerLinkToken)
	})

	t.Run("unknown link token", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/scanner/open "Unknown link token"
		f := newScannerFixture(t)
		f.scanners.EXPECT().GetByLinkToken(mock.Anything, "tok-1").Return(nil, apperr.New(codes.NotFound, "no scanner"))

		_, err := f.uc.Open(context.Background(), openInput(t, testutil.NewDeviceKey(t), evening(16, 0, 0)))
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		assert.ErrorIs(t, err, usecase.ErrUnknownScannerLinkToken)
	})

	t.Run("signature from another key", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/scanner/open "Signature from another key"
		f := newScannerFixture(t)
		device := testutil.NewDeviceKey(t)
		now := evening(16, 0, 0)
		in := openInput(t, device, now)
		in.PublicKey = testutil.NewDeviceKey(t).PublicKey(t) // claims a key it does not hold
		f.scanners.EXPECT().GetByLinkToken(mock.Anything, "tok-1").Return(unused(f), nil)

		_, err := f.uc.Open(context.Background(), in)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		f.scanners.AssertNotCalled(t, "BindDevice", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("stale call from the bound device is refused", func(t *testing.T) {
		t.Parallel()
		f := newScannerFixture(t)
		device := testutil.NewDeviceKey(t)
		now := evening(16, 0, 0)
		in := openInput(t, device, now.Add(-time.Minute))
		in.Now = now
		bound := boundTo(t, f, device, evening(14, 10, 0))
		f.scanners.EXPECT().GetByLinkToken(mock.Anything, "tok-1").Return(bound, nil)

		_, err := f.uc.Open(context.Background(), in)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		f.scanners.AssertNotCalled(t, "BindDevice", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("invalid public key", func(t *testing.T) {
		t.Parallel()
		f := newScannerFixture(t)
		in := openInput(t, testutil.NewDeviceKey(t), evening(16, 0, 0))
		in.PublicKey = append(entity.PublicKey{0x04}, make([]byte, 64)...)
		f.scanners.EXPECT().GetByLinkToken(mock.Anything, "tok-1").Return(unused(f), nil)

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

func TestWalletPublicKeyUseCase_Get(t *testing.T) {
	t.Parallel()

	user := entity.UserID(entity.NewID())

	t.Run("fan with an entry device", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/wallet-public-key/get "Fan with an entry device"
		keys := entitymocks.NewMockWalletPublicKeyRepository(t)
		k2 := testutil.NewDeviceKey(t).PublicKey(t)
		registered := evening(10, 0, 0)
		keys.EXPECT().GetByUser(mock.Anything, user).
			Return(&entity.WalletPublicKey{UserID: user, PublicKey: k2, RegisteredTime: registered}, nil)

		got, err := usecase.NewWalletPublicKeyUseCase(keys, newTestLogger(t)).Get(context.Background(), user)
		require.NoError(t, err)
		assert.Equal(t, k2, got.PublicKey)
		assert.True(t, got.RegisteredTime.Equal(registered))
		keys.AssertNotCalled(t, "Register", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("fan without an entry device", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/wallet-public-key/get "Fan without an entry device"
		keys := entitymocks.NewMockWalletPublicKeyRepository(t)
		keys.EXPECT().GetByUser(mock.Anything, user).Return(nil, apperr.New(codes.NotFound, "no key"))

		_, err := usecase.NewWalletPublicKeyUseCase(keys, newTestLogger(t)).Get(context.Background(), user)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
