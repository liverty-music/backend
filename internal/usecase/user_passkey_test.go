package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type userPasskeyTestDeps struct {
	repo         *entitymocks.MockUserRepository
	workflows    *ucmocks.MockWorkflows
	links        *entitymocks.MockPasskeyRegistrationLinkSender
	changeEmails *entitymocks.MockPasskeyChangeEmailSender
	uc           usecase.UserUseCase
}

// newUserPasskeyTestDeps returns a UserUseCase with strict mocks whose
// background recovery task runs before RequestPasskeyRecovery returns.
func newUserPasskeyTestDeps(t *testing.T) *userPasskeyTestDeps {
	t.Helper()
	d := &userPasskeyTestDeps{
		repo:         entitymocks.NewMockUserRepository(t),
		workflows:    ucmocks.NewMockWorkflows(t),
		links:        entitymocks.NewMockPasskeyRegistrationLinkSender(t),
		changeEmails: entitymocks.NewMockPasskeyChangeEmailSender(t),
	}
	d.uc = usecase.NewUserUseCase(d.repo, usecase.DeletionBlockerReads{}, d.workflows, nil, nil, nil,
		usecase.PasskeyMailers{RegistrationLinks: d.links, ChangeEmails: d.changeEmails}, newTestLogger(t))
	usecase.RunPasskeyRecoverySynchronously(d.uc)
	return d
}

var passkeyFan = &entity.User{ID: "user-1", ProviderUserID: "zitadel-fan-1", Email: "fan@example.com", PreferredLanguage: "ja"}

func TestUserUseCase_RequestPasskeyRecovery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @spec components/usecase/user/request-passkey-recovery "Address of a fan account"
	t.Run("starts the recovery link with the user id only for a fan's address", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		d.repo.EXPECT().GetByEmail(mock.Anything, "fan@example.com").Return(passkeyFan, nil).Once()
		d.workflows.EXPECT().Start(mock.Anything, usecase.WorkflowSendPasskeyRecoveryLink, mock.Anything,
			usecase.UserRequest{UserID: "user-1"}).Return(nil).Once()

		d.uc.RequestPasskeyRecovery(ctx, "fan@example.com")
	})

	t.Run("each request starts its own run", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		d.repo.EXPECT().GetByEmail(mock.Anything, "fan@example.com").Return(passkeyFan, nil).Twice()
		var ids []string
		d.workflows.EXPECT().Start(mock.Anything, usecase.WorkflowSendPasskeyRecoveryLink, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, _, id string, _ any, _ ...usecase.StartOption) error {
				ids = append(ids, id)
				return nil
			}).Twice()

		d.uc.RequestPasskeyRecovery(ctx, "fan@example.com")
		d.uc.RequestPasskeyRecovery(ctx, "fan@example.com")

		require.Len(t, ids, 2)
		assert.NotEqual(t, ids[0], ids[1])
	})

	// @spec components/usecase/user/request-passkey-recovery "Address with no account"
	t.Run("succeeds the same way and starts nothing for an address no user has", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		d.repo.EXPECT().GetByEmail(mock.Anything, "nobody@example.com").Return(nil, apperr.New(codes.NotFound, "no user")).Once()
		// The strict workflows mock fails the test if anything is started.

		d.uc.RequestPasskeyRecovery(ctx, "nobody@example.com")
	})

	// @spec components/usecase/user/request-passkey-recovery "Store unavailable"
	t.Run("still succeeds and starts nothing when the store is unavailable", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		d.repo.EXPECT().GetByEmail(mock.Anything, "fan@example.com").Return(nil, apperr.New(codes.Unavailable, "db down")).Once()

		d.uc.RequestPasskeyRecovery(ctx, "fan@example.com")
	})

	// @spec components/usecase/user/request-passkey-recovery "Work cannot be started"
	t.Run("still succeeds when the recovery link cannot be started", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		d.repo.EXPECT().GetByEmail(mock.Anything, "fan@example.com").Return(passkeyFan, nil).Once()
		d.workflows.EXPECT().Start(mock.Anything, usecase.WorkflowSendPasskeyRecoveryLink, mock.Anything, mock.Anything).
			Return(apperr.New(codes.Unavailable, "database down")).Once()

		d.uc.RequestPasskeyRecovery(ctx, "fan@example.com")
	})

	// @spec components/usecase/user/request-passkey-recovery "Fourth request for one address"
	t.Run("starts at most 3 links per address, comparing it without regard to letter case", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		d.repo.EXPECT().GetByEmail(mock.Anything, mock.Anything).Return(passkeyFan, nil).Times(3)
		d.workflows.EXPECT().Start(mock.Anything, usecase.WorkflowSendPasskeyRecoveryLink, mock.Anything, mock.Anything).Return(nil).Times(3)

		d.uc.RequestPasskeyRecovery(ctx, "fan@example.com")
		d.uc.RequestPasskeyRecovery(ctx, "Fan@Example.com")
		d.uc.RequestPasskeyRecovery(ctx, "fan@example.com")
		// The fourth request within the window returns without a lookup or
		// a start (the mocks allow exactly three of each).
		d.uc.RequestPasskeyRecovery(ctx, "FAN@example.com")

		// Another address has its own allowance.
		d.repo.EXPECT().GetByEmail(mock.Anything, "other@example.com").Return(nil, apperr.ErrNotFound).Once()
		d.uc.RequestPasskeyRecovery(ctx, "other@example.com")
	})

	t.Run("answers before the lookup and the start run, on a context the request's cancel does not stop", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		var tasks []func()
		usecase.RunPasskeyRecoveryWith(d.uc, func(task func()) { tasks = append(tasks, task) })
		reqCtx, cancel := context.WithCancel(ctx)

		// No repository or workflows call happens before it returns.
		d.uc.RequestPasskeyRecovery(reqCtx, "fan@example.com")
		cancel()

		require.Len(t, tasks, 1)
		d.repo.EXPECT().GetByEmail(mock.Anything, "fan@example.com").
			RunAndReturn(func(ctx context.Context, _ string) (*entity.User, error) {
				assert.NoError(t, ctx.Err(), "the background lookup outlives the request")
				return passkeyFan, nil
			}).Once()
		d.workflows.EXPECT().Start(mock.Anything, usecase.WorkflowSendPasskeyRecoveryLink, mock.Anything, mock.Anything).Return(nil).Once()
		tasks[0]()
	})
}

func TestUserUseCase_SendPasskeyRecoveryLink(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("sends the registration link to the user", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		d.repo.EXPECT().Get(mock.Anything, "user-1").Return(passkeyFan, nil).Once()
		d.links.EXPECT().SendPasskeyRegistrationLink(mock.Anything, passkeyFan).Return(nil).Once()

		assert.NoError(t, d.uc.SendPasskeyRecoveryLink(ctx, "user-1"))
	})

	// @spec components/usecase/user/send-passkey-recovery-link "Unverified address"
	t.Run("finishes without failing when the identity's address is not verified", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		d.repo.EXPECT().Get(mock.Anything, "user-1").Return(passkeyFan, nil).Once()
		d.links.EXPECT().SendPasskeyRegistrationLink(mock.Anything, passkeyFan).
			Return(apperr.New(codes.FailedPrecondition, "not verified")).Once()

		assert.NoError(t, d.uc.SendPasskeyRecoveryLink(ctx, "user-1"))
	})

	t.Run("finishes without failing when the identity no longer exists", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		d.repo.EXPECT().Get(mock.Anything, "user-1").Return(passkeyFan, nil).Once()
		d.links.EXPECT().SendPasskeyRegistrationLink(mock.Anything, passkeyFan).
			Return(apperr.New(codes.NotFound, "identity gone")).Once()

		assert.NoError(t, d.uc.SendPasskeyRecoveryLink(ctx, "user-1"))
	})

	// @spec components/usecase/user/send-passkey-recovery-link "Account deleted since the request"
	t.Run("finishes without sending when the user no longer exists", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		d.repo.EXPECT().Get(mock.Anything, "user-1").Return(nil, apperr.New(codes.NotFound, "no user")).Once()
		// The strict link sender mock fails the test if anything is sent.

		assert.NoError(t, d.uc.SendPasskeyRecoveryLink(ctx, "user-1"))
	})

	// @spec components/usecase/user/send-passkey-recovery-link "Mail service refuses"
	t.Run("fails with the mail service's refusal", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		d.repo.EXPECT().Get(mock.Anything, "user-1").Return(passkeyFan, nil).Once()
		d.links.EXPECT().SendPasskeyRegistrationLink(mock.Anything, passkeyFan).
			Return(apperr.New(codes.Internal, "postmark down")).Once()

		err := d.uc.SendPasskeyRecoveryLink(ctx, "user-1")

		assert.ErrorIs(t, err, apperr.ErrInternal)
	})

	t.Run("fails so it runs again when the user cannot be read", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		d.repo.EXPECT().Get(mock.Anything, "user-1").Return(nil, apperr.New(codes.Unavailable, "db down")).Once()

		assert.ErrorIs(t, d.uc.SendPasskeyRecoveryLink(ctx, "user-1"), apperr.ErrUnavailable)
	})
}

func TestUserUseCase_NotifyPasskeyChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	changeTime := time.Date(2026, 10, 22, 3, 4, 0, 0, time.UTC)

	// @spec components/usecase/user/notify-passkey-change "Passkey added on a new device"
	t.Run("emails the fan that the named passkey was added", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		change := entity.PasskeyChange{Kind: entity.PasskeyChangeAdded, Name: "Google Pixel 9, Android, Chrome", ChangeTime: changeTime}
		d.repo.EXPECT().GetByProviderUserID(mock.Anything, "zitadel-fan-1").Return(passkeyFan, nil).Once()
		d.changeEmails.EXPECT().SendPasskeyChangeEmail(mock.Anything, passkeyFan, change).Return(nil).Once()

		assert.NoError(t, d.uc.NotifyPasskeyChange(ctx, "zitadel-fan-1", change))
	})

	// @spec components/usecase/user/notify-passkey-change "Passkey added through a recovery link"
	t.Run("emails the same added email for a passkey registered with a recovery link", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		// A recovery-link registration arrives as the same added change
		// (token.verified), named by the device it was made on.
		change := entity.PasskeyChange{Kind: entity.PasskeyChangeAdded, Name: "Apple iPhone, iOS, Mobile Safari", ChangeTime: changeTime}
		d.repo.EXPECT().GetByProviderUserID(mock.Anything, "zitadel-fan-1").Return(passkeyFan, nil).Once()
		d.changeEmails.EXPECT().SendPasskeyChangeEmail(mock.Anything, passkeyFan, change).Return(nil).Once()

		assert.NoError(t, d.uc.NotifyPasskeyChange(ctx, "zitadel-fan-1", change))
	})

	// @spec components/usecase/user/notify-passkey-change "Passkey removed"
	t.Run("emails the fan that a passkey was removed", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		change := entity.PasskeyChange{Kind: entity.PasskeyChangeRemoved, ChangeTime: changeTime}
		d.repo.EXPECT().GetByProviderUserID(mock.Anything, "zitadel-fan-1").Return(passkeyFan, nil).Once()
		d.changeEmails.EXPECT().SendPasskeyChangeEmail(mock.Anything, passkeyFan, change).Return(nil).Once()

		assert.NoError(t, d.uc.NotifyPasskeyChange(ctx, "zitadel-fan-1", change))
	})

	// @spec components/usecase/user/notify-passkey-change "Identity with no account"
	t.Run("finishes without sending when no user has the identity", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		d.repo.EXPECT().GetByProviderUserID(mock.Anything, "zitadel-signup-1").Return(nil, apperr.New(codes.NotFound, "no user")).Once()
		// The strict change email mock fails the test if anything is sent.

		assert.NoError(t, d.uc.NotifyPasskeyChange(ctx, "zitadel-signup-1",
			entity.PasskeyChange{Kind: entity.PasskeyChangeAdded, ChangeTime: changeTime}))
	})

	// @spec components/usecase/user/notify-passkey-change "Mail unavailable"
	t.Run("fails with Unavailable so it runs again, and sends when it runs again", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)
		change := entity.PasskeyChange{Kind: entity.PasskeyChangeRemoved, ChangeTime: changeTime}
		d.repo.EXPECT().GetByProviderUserID(mock.Anything, "zitadel-fan-1").Return(passkeyFan, nil).Twice()
		d.changeEmails.EXPECT().SendPasskeyChangeEmail(mock.Anything, passkeyFan, change).
			Return(apperr.New(codes.Unavailable, "postmark down")).Once()
		d.changeEmails.EXPECT().SendPasskeyChangeEmail(mock.Anything, passkeyFan, change).Return(nil).Once()

		assert.ErrorIs(t, d.uc.NotifyPasskeyChange(ctx, "zitadel-fan-1", change), apperr.ErrUnavailable)
		assert.NoError(t, d.uc.NotifyPasskeyChange(ctx, "zitadel-fan-1", change))
	})

	t.Run("fails with InvalidArgument for an unknown kind of change", func(t *testing.T) {
		t.Parallel()
		d := newUserPasskeyTestDeps(t)

		err := d.uc.NotifyPasskeyChange(ctx, "zitadel-fan-1", entity.PasskeyChange{Kind: "renamed", ChangeTime: changeTime})

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}
