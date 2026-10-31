package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// passkeyAccountWait is how long after its first run RecordChange keeps
// waiting for the User of an added passkey's identity: at sign-up the passkey is registered
// before UserService.Create stores the User.
const passkeyAccountWait = 10 * time.Minute

// ErrPasskeyAccountPending reports that an added passkey's identity has no
// User yet; RunRecordChange looks again later.
var ErrPasskeyAccountPending = errors.New("no user has the passkey's identity yet")

// PasskeyUseCase keeps and reads the platform's Passkey records.
type PasskeyUseCase interface {
	// StartRecordChange starts the record-passkey-change workflow for a
	// passkey notice, named after the provider passkey id and the kind of
	// change, so a repeated notice starts nothing new.
	//
	// # Possible errors
	//
	//  - Unavailable: the workflow could not be started.
	StartRecordChange(ctx context.Context, change entity.UserPasskeyChangedData) error

	// RunRecordChange is the record-passkey-change workflow StartRecordChange
	// starts: it records the change with RecordChange, looking again for the
	// User of an added passkey every 30 seconds until 10 minutes after its
	// first run, and lets a removal wait for the run of the same passkey's
	// addition to end.
	RunRecordChange(ctx context.Context, f Flow, in entity.UserPasskeyChangedData) (struct{}, error)

	// RecordChange makes one attempt to record a passkey change: for an added
	// passkey it reads the User with User.GetByProviderUserID and stores the
	// Passkey (create time = the change's time; an empty AAGUID is stored as
	// all zeros), for a removed one it removes the record. Recording the same
	// added change twice stores one Passkey.
	//
	// # Possible errors
	//
	//  - ErrPasskeyAccountPending (wrapping NotFound): no User has the
	//    identity yet.
	//  - InvalidArgument: the change has no provider passkey id or an unknown
	//    kind.
	//  - Unavailable and other failures of the store, unchanged.
	RecordChange(ctx context.Context, change entity.UserPasskeyChangedData) error

	// List returns the recorded Passkeys of the User userID, newest first
	// (Passkey.ListByUser); an empty list when it has none. It never reads
	// the identity provider.
	List(ctx context.Context, userID string) ([]*entity.Passkey, error)
}

type passkeyUseCase struct {
	passkeys  entity.PasskeyRepository
	users     entity.UserRepository
	workflows Workflows
	now       func() time.Time
	logger    *logging.Logger
}

// Compile-time interface compliance check.
var _ PasskeyUseCase = (*passkeyUseCase)(nil)

// NewPasskeyUseCase creates a PasskeyUseCase over the passkey and user
// repositories and the workflows it starts and waits for. now is the clock
// (time.Now in production).
func NewPasskeyUseCase(passkeys entity.PasskeyRepository, users entity.UserRepository, workflows Workflows, now func() time.Time, logger *logging.Logger) PasskeyUseCase {
	return &passkeyUseCase{passkeys: passkeys, users: users, workflows: workflows, now: now, logger: logger}
}

// RecordChange implements [PasskeyUseCase].
func (uc *passkeyUseCase) RecordChange(ctx context.Context, change entity.UserPasskeyChangedData) error {
	attrs := []slog.Attr{
		slog.String("provider_user_id", change.ProviderUserID),
		slog.String("provider_passkey_id", change.ProviderPasskeyID),
		slog.String("kind", change.Kind),
	}
	if change.ProviderPasskeyID == "" {
		return apperr.New(codes.InvalidArgument, "the passkey change has no provider passkey id", attrs...)
	}

	switch entity.PasskeyChangeKind(change.Kind) {
	case entity.PasskeyChangeRemoved:
		if err := uc.passkeys.Delete(ctx, change.ProviderPasskeyID); err != nil {
			return fmt.Errorf("delete passkey %q: %w", change.ProviderPasskeyID, err)
		}
		uc.logger.Info(ctx, "passkey record removed", attrs...)
		return nil
	case entity.PasskeyChangeAdded:
	default:
		return apperr.New(codes.InvalidArgument, "unknown kind of passkey change", attrs...)
	}

	user, err := uc.users.GetByProviderUserID(ctx, change.ProviderUserID)
	if err != nil {
		if !errors.Is(err, apperr.ErrNotFound) {
			return fmt.Errorf("get user by provider user ID %q for a passkey: %w", change.ProviderUserID, err)
		}
		return fmt.Errorf("%w: %w", ErrPasskeyAccountPending, err)
	}

	aaguid := change.AAGUID
	if aaguid == "" {
		aaguid = entity.ZeroAAGUID
	}
	passkey := &entity.Passkey{
		ProviderPasskeyID: change.ProviderPasskeyID,
		UserID:            user.ID,
		AAGUID:            aaguid,
		CreateTime:        change.ChangeTime,
	}
	if change.Name != "" {
		passkey.Name = &change.Name
	}
	if err := uc.passkeys.Create(ctx, passkey); err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// The User was deleted after it was read.
			uc.logger.Info(ctx, "passkey not recorded: the user no longer exists", append(attrs, slog.String("user_id", user.ID))...)
			return nil
		}
		return fmt.Errorf("create passkey %q: %w", change.ProviderPasskeyID, err)
	}
	uc.logger.Info(ctx, "passkey recorded", append(attrs, slog.String("user_id", user.ID))...)
	return nil
}

// List implements [PasskeyUseCase].
func (uc *passkeyUseCase) List(ctx context.Context, userID string) ([]*entity.Passkey, error) {
	passkeys, err := uc.passkeys.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list passkeys of user %q: %w", userID, err)
	}
	return passkeys, nil
}
