package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	googlePasswordManagerAAGUID = "ea9b8d66-4d01-1d21-3ce4-b6b48cb575d4"
	icloudKeychainAAGUID        = "dd4ec289-e01d-41c9-bb89-70fa845d4bf2" // iCloud Keychain (Managed)
)

type passkeyTestDeps struct {
	passkeys *entitymocks.MockPasskeyRepository
	users    *entitymocks.MockUserRepository
	now      time.Time
	uc       usecase.PasskeyUseCase
}

// newPasskeyTestDeps returns a PasskeyUseCase over strict mocks whose clock
// reads changeTime plus elapsed.
func newPasskeyTestDeps(t *testing.T, elapsed time.Duration) *passkeyTestDeps {
	t.Helper()
	d := &passkeyTestDeps{
		passkeys: entitymocks.NewMockPasskeyRepository(t),
		users:    entitymocks.NewMockUserRepository(t),
		now:      passkeyChangeTime.Add(elapsed),
	}
	d.uc = usecase.NewPasskeyUseCase(d.passkeys, d.users, func() time.Time { return d.now }, newTestLogger(t))
	return d
}

var passkeyChangeTime = time.Date(2026, 10, 22, 3, 4, 5, 0, time.UTC)

func addedChange() entity.UserPasskeyChangedData {
	return entity.UserPasskeyChangedData{
		ProviderUserID:    "zitadel-fan-1",
		ProviderPasskeyID: "token-1",
		Kind:              "added",
		Name:              "Google Pixel 9, Android, Chrome",
		AAGUID:            googlePasswordManagerAAGUID,
		ChangeTime:        passkeyChangeTime,
	}
}

func TestPasskeyUseCase_RecordChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	wantPasskey := &entity.Passkey{ProviderPasskeyID: "token-1", UserID: "user-1", AAGUID: googlePasswordManagerAAGUID, CreateTime: passkeyChangeTime}

	// @spec components/usecase/passkey/record-change "Passkey added"
	t.Run("stores the added passkey for the fan with its AAGUID and the change's time", func(t *testing.T) {
		t.Parallel()
		d := newPasskeyTestDeps(t, time.Second)
		d.users.EXPECT().GetByProviderUserID(mock.Anything, "zitadel-fan-1").Return(passkeyFan, nil).Once()
		d.passkeys.EXPECT().Create(mock.Anything, wantPasskey).Return(nil).Once()

		assert.NoError(t, d.uc.RecordChange(ctx, addedChange()))
	})

	t.Run("stores an added passkey without an AAGUID as all zeros", func(t *testing.T) {
		t.Parallel()
		d := newPasskeyTestDeps(t, time.Second)
		change := addedChange()
		change.AAGUID = ""
		d.users.EXPECT().GetByProviderUserID(mock.Anything, "zitadel-fan-1").Return(passkeyFan, nil).Once()
		d.passkeys.EXPECT().Create(mock.Anything, &entity.Passkey{ProviderPasskeyID: "token-1", UserID: "user-1", AAGUID: entity.ZeroAAGUID, CreateTime: passkeyChangeTime}).
			Return(nil).Once()

		assert.NoError(t, d.uc.RecordChange(ctx, change))
	})

	// @spec components/usecase/passkey/record-change "Passkey removed"
	t.Run("removes the record of a removed passkey", func(t *testing.T) {
		t.Parallel()
		d := newPasskeyTestDeps(t, time.Second)
		d.passkeys.EXPECT().Delete(mock.Anything, "token-1").Return(nil).Once()

		assert.NoError(t, d.uc.RecordChange(ctx, entity.UserPasskeyChangedData{
			ProviderUserID: "zitadel-fan-1", ProviderPasskeyID: "token-1", Kind: "removed", ChangeTime: passkeyChangeTime,
		}))
	})

	// @spec components/usecase/passkey/record-change "Change announced twice"
	t.Run("handles the same added change twice through the idempotent Create", func(t *testing.T) {
		t.Parallel()
		d := newPasskeyTestDeps(t, time.Second)
		d.users.EXPECT().GetByProviderUserID(mock.Anything, "zitadel-fan-1").Return(passkeyFan, nil).Twice()
		// Passkey.Create keeps one Passkey per provider passkey id; the
		// usecase asks for the same Passkey both times.
		d.passkeys.EXPECT().Create(mock.Anything, wantPasskey).Return(nil).Twice()

		require.NoError(t, d.uc.RecordChange(ctx, addedChange()))
		assert.NoError(t, d.uc.RecordChange(ctx, addedChange()))
	})

	// @spec components/usecase/passkey/record-change "Store unavailable"
	t.Run("fails with Unavailable so it runs again, and stores the passkey when it does", func(t *testing.T) {
		t.Parallel()
		d := newPasskeyTestDeps(t, time.Second)
		d.users.EXPECT().GetByProviderUserID(mock.Anything, "zitadel-fan-1").Return(passkeyFan, nil).Twice()
		d.passkeys.EXPECT().Create(mock.Anything, wantPasskey).Return(apperr.New(codes.Unavailable, "db down")).Once()
		d.passkeys.EXPECT().Create(mock.Anything, wantPasskey).Return(nil).Once()

		assert.ErrorIs(t, d.uc.RecordChange(ctx, addedChange()), apperr.ErrUnavailable)
		assert.NoError(t, d.uc.RecordChange(ctx, addedChange()))
	})

	// @spec components/usecase/passkey/record-change "Account created after the passkey"
	t.Run("fails to run again while the fan's user does not exist yet, and stores the passkey once it does", func(t *testing.T) {
		t.Parallel()
		d := newPasskeyTestDeps(t, time.Second)
		d.users.EXPECT().GetByProviderUserID(mock.Anything, "zitadel-fan-1").Return(nil, apperr.New(codes.NotFound, "no user")).Once()

		err := d.uc.RecordChange(ctx, addedChange())

		assert.ErrorIs(t, err, usecase.ErrPasskeyAccountPending)

		// The User is created 5 seconds later; the next run stores the passkey.
		d.now = passkeyChangeTime.Add(6 * time.Second)
		d.users.EXPECT().GetByProviderUserID(mock.Anything, "zitadel-fan-1").Return(passkeyFan, nil).Once()
		d.passkeys.EXPECT().Create(mock.Anything, wantPasskey).Return(nil).Once()
		assert.NoError(t, d.uc.RecordChange(ctx, addedChange()))
	})

	// @spec components/usecase/passkey/record-change "Identity that never becomes an account"
	t.Run("finishes without storing once the change is more than 10 minutes old and no user has the identity", func(t *testing.T) {
		t.Parallel()
		d := newPasskeyTestDeps(t, 10*time.Minute+time.Second)
		d.users.EXPECT().GetByProviderUserID(mock.Anything, "zitadel-fan-1").Return(nil, apperr.New(codes.NotFound, "no user")).Once()
		// The strict passkey repository mock fails the test if anything is stored.

		assert.NoError(t, d.uc.RecordChange(ctx, addedChange()))
	})

	t.Run("fails with InvalidArgument for a change without a provider passkey id", func(t *testing.T) {
		t.Parallel()
		d := newPasskeyTestDeps(t, time.Second)
		change := addedChange()
		change.ProviderPasskeyID = ""

		assert.ErrorIs(t, d.uc.RecordChange(ctx, change), apperr.ErrInvalidArgument)
	})
}

func TestPasskeyUseCase_List(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @spec components/usecase/passkey/list "Fan with recorded passkeys"
	t.Run("returns the fan's recorded passkeys, each with its provider name when it has one", func(t *testing.T) {
		t.Parallel()
		d := newPasskeyTestDeps(t, 0)
		recorded := []*entity.Passkey{
			{ProviderPasskeyID: "token-2", UserID: "user-1", AAGUID: icloudKeychainAAGUID, CreateTime: passkeyChangeTime},
			{ProviderPasskeyID: "token-1", UserID: "user-1", AAGUID: entity.ZeroAAGUID, CreateTime: passkeyChangeTime.Add(-time.Hour)},
		}
		d.passkeys.EXPECT().ListByUser(mock.Anything, "user-1").Return(recorded, nil).Once()

		got, err := d.uc.List(ctx, "user-1")

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "iCloud Keychain (Managed)", got[0].ProviderName())
		assert.Empty(t, got[1].ProviderName())
	})

	// @spec components/usecase/passkey/list "Fan with no record"
	t.Run("returns an empty list for a fan with no recorded passkey", func(t *testing.T) {
		t.Parallel()
		d := newPasskeyTestDeps(t, 0)
		d.passkeys.EXPECT().ListByUser(mock.Anything, "user-1").Return([]*entity.Passkey{}, nil).Once()

		got, err := d.uc.List(ctx, "user-1")

		require.NoError(t, err)
		assert.Empty(t, got)
	})
}
