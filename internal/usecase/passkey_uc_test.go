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
	passkeys  *entitymocks.MockPasskeyRepository
	users     *entitymocks.MockUserRepository
	workflows *passkeyWorkflows
	now       time.Time
	uc        usecase.PasskeyUseCase
}

// passkeyWorkflows records the workflows started and awaited; Await returns
// awaitErr.
type passkeyWorkflows struct {
	recordingWorkflows
	awaited  []string
	awaitErr error
	// onAwait, when set, runs before Await returns.
	onAwait func()
}

func (w *passkeyWorkflows) Await(_ context.Context, id string, _ time.Duration, _ any) error {
	w.awaited = append(w.awaited, id)
	if w.onAwait != nil {
		w.onAwait()
	}
	return w.awaitErr
}

// waitingFlow runs steps inline and moves the clock by each wait.
type waitingFlow struct {
	inlineFlow
	d     *passkeyTestDeps
	waits int
}

func (f *waitingFlow) Recv(_ context.Context, _ string, timeout time.Duration) ([]byte, bool, error) {
	f.waits++
	f.d.now = f.d.now.Add(timeout)
	return nil, false, nil
}

// newPasskeyTestDeps returns a PasskeyUseCase over strict mocks whose clock
// reads changeTime plus elapsed.
func newPasskeyTestDeps(t *testing.T, elapsed time.Duration) *passkeyTestDeps {
	t.Helper()
	d := &passkeyTestDeps{
		passkeys:  entitymocks.NewMockPasskeyRepository(t),
		users:     entitymocks.NewMockUserRepository(t),
		workflows: &passkeyWorkflows{},
		now:       passkeyChangeTime.Add(elapsed),
	}
	d.uc = usecase.NewPasskeyUseCase(d.passkeys, d.users, d.workflows, func() time.Time { return d.now }, newTestLogger(t))
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
	name := "Google Pixel 9, Android, Chrome"
	wantPasskey := &entity.Passkey{ProviderPasskeyID: "token-1", UserID: "user-1", AAGUID: googlePasswordManagerAAGUID, Name: &name, CreateTime: passkeyChangeTime}

	// @spec components/usecase/passkey/record-change "Passkey added"
	t.Run("stores the added passkey for the fan with its AAGUID, name and the change's time", func(t *testing.T) {
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
		d.passkeys.EXPECT().Create(mock.Anything, &entity.Passkey{ProviderPasskeyID: "token-1", UserID: "user-1", AAGUID: entity.ZeroAAGUID, Name: &name, CreateTime: passkeyChangeTime}).
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

	t.Run("reports a fan's user that does not exist yet", func(t *testing.T) {
		t.Parallel()
		d := newPasskeyTestDeps(t, time.Second)
		d.users.EXPECT().GetByProviderUserID(mock.Anything, "zitadel-fan-1").Return(nil, apperr.New(codes.NotFound, "no user")).Once()

		assert.ErrorIs(t, d.uc.RecordChange(ctx, addedChange()), usecase.ErrPasskeyAccountPending)
	})

	t.Run("fails with InvalidArgument for a change without a provider passkey id", func(t *testing.T) {
		t.Parallel()
		d := newPasskeyTestDeps(t, time.Second)
		change := addedChange()
		change.ProviderPasskeyID = ""

		assert.ErrorIs(t, d.uc.RecordChange(ctx, change), apperr.ErrInvalidArgument)
	})
}

func TestPasskeyUseCase_RunRecordChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @spec components/usecase/passkey/record-change "Account created after the passkey"
	t.Run("looks again after 30 seconds and stores the passkey once the fan's user exists", func(t *testing.T) {
		t.Parallel()
		d := newPasskeyTestDeps(t, time.Second)
		d.users.EXPECT().GetByProviderUserID(mock.Anything, "zitadel-fan-1").Return(nil, apperr.New(codes.NotFound, "no user")).Once()
		d.users.EXPECT().GetByProviderUserID(mock.Anything, "zitadel-fan-1").Return(passkeyFan, nil).Once()
		d.passkeys.EXPECT().Create(mock.Anything, mock.Anything).Return(nil).Once()
		f := &waitingFlow{d: d}

		_, err := d.uc.RunRecordChange(ctx, f, addedChange())

		require.NoError(t, err)
		assert.Equal(t, 1, f.waits, "one wait of 30 seconds before the second look")
	})

	// @spec components/usecase/passkey/record-change "Identity that never becomes an account"
	t.Run("finishes without storing 10 minutes after its first run", func(t *testing.T) {
		t.Parallel()
		d := newPasskeyTestDeps(t, time.Second)
		d.users.EXPECT().GetByProviderUserID(mock.Anything, "zitadel-fan-1").Return(nil, apperr.New(codes.NotFound, "no user"))
		// The strict passkey repository mock fails the test if anything is stored.
		f := &waitingFlow{d: d}

		_, err := d.uc.RunRecordChange(ctx, f, addedChange())

		require.NoError(t, err)
		assert.Equal(t, 20, f.waits, "every 30 seconds for 10 minutes")
	})

	// @spec components/usecase/passkey/record-change "Removed while its addition waits"
	t.Run("removes the passkey only after the run of its addition has ended", func(t *testing.T) {
		t.Parallel()
		d := newPasskeyTestDeps(t, time.Second)
		var order []string
		d.workflows.onAwait = func() { order = append(order, "await") }
		d.passkeys.EXPECT().Delete(mock.Anything, "token-1").RunAndReturn(func(context.Context, string) error {
			order = append(order, "delete")
			return nil
		}).Once()

		_, err := d.uc.RunRecordChange(ctx, &inlineFlow{}, entity.UserPasskeyChangedData{
			ProviderUserID: "zitadel-fan-1", ProviderPasskeyID: "token-1", Kind: "removed", ChangeTime: passkeyChangeTime,
		})

		require.NoError(t, err)
		assert.Equal(t, []string{"await", "delete"}, order)
		assert.Equal(t, []string{usecase.ExportedRecordPasskeyChangeID("token-1", entity.PasskeyChangeAdded)}, d.workflows.awaited)
	})

	t.Run("a removal whose addition was never seen removes at once", func(t *testing.T) {
		t.Parallel()
		d := newPasskeyTestDeps(t, time.Second)
		d.workflows.awaitErr = apperr.New(codes.NotFound, "no such workflow")
		d.passkeys.EXPECT().Delete(mock.Anything, "token-1").Return(nil).Once()

		_, err := d.uc.RunRecordChange(ctx, &inlineFlow{}, entity.UserPasskeyChangedData{ProviderPasskeyID: "token-1", Kind: "removed"})

		assert.NoError(t, err)
	})

	t.Run("a removal tries again while the addition has not ended", func(t *testing.T) {
		t.Parallel()
		d := newPasskeyTestDeps(t, time.Second)
		d.workflows.awaitErr = apperr.New(codes.DeadlineExceeded, "still running")

		_, err := d.uc.RunRecordChange(ctx, &inlineFlow{}, entity.UserPasskeyChangedData{ProviderPasskeyID: "token-1", Kind: "removed"})

		assert.ErrorIs(t, err, apperr.ErrUnavailable, "the step is tried again")
	})

	t.Run("a removal whose addition was cancelled removes at once", func(t *testing.T) {
		t.Parallel()
		d := newPasskeyTestDeps(t, time.Second)
		d.workflows.awaitErr = apperr.New(codes.Canceled, "the addition was cancelled")
		d.passkeys.EXPECT().Delete(mock.Anything, "token-1").Return(nil).Once()

		_, err := d.uc.RunRecordChange(ctx, &inlineFlow{}, entity.UserPasskeyChangedData{ProviderPasskeyID: "token-1", Kind: "removed"})

		assert.NoError(t, err)
	})

	t.Run("a removal returns its own cancellation and a conflict to the runtime", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name string
			ctx  func() context.Context
			err  error
		}{
			{"cancelled run", func() context.Context { c, cancel := context.WithCancel(ctx); cancel(); return c }, apperr.New(codes.Canceled, "context canceled")},
			{"conflicting run", func() context.Context { return ctx }, apperr.New(codes.Aborted, "another execution owns the workflow")},
		} {
			d := newPasskeyTestDeps(t, time.Second)
			d.workflows.awaitErr = tc.err

			_, err := d.uc.RunRecordChange(tc.ctx(), &inlineFlow{}, entity.UserPasskeyChangedData{ProviderPasskeyID: "token-1", Kind: "removed"})

			assert.ErrorIs(t, err, tc.err, tc.name)
		}
	})
}

func TestPasskeyUseCase_StartRecordChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @spec components/usecase/passkey/record-change "Change announced twice"
	t.Run("a repeated notice starts the same run", func(t *testing.T) {
		t.Parallel()
		d := newPasskeyTestDeps(t, time.Second)

		require.NoError(t, d.uc.StartRecordChange(ctx, addedChange()))
		require.NoError(t, d.uc.StartRecordChange(ctx, addedChange()))

		assert.Equal(t, 1, d.workflows.runs())
	})

	t.Run("the addition and the removal of a passkey are separate runs", func(t *testing.T) {
		t.Parallel()
		d := newPasskeyTestDeps(t, time.Second)
		removed := addedChange()
		removed.Kind = "removed"

		require.NoError(t, d.uc.StartRecordChange(ctx, addedChange()))
		require.NoError(t, d.uc.StartRecordChange(ctx, removed))

		assert.Equal(t, 2, d.workflows.runs())
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
