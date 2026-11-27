package usecase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

// WorkflowDeleteUser is the registered name of UserUseCase.RunDeleteUser.
const WorkflowDeleteUser = "delete-user"

// WorkflowForgetRemovedIdentity is the registered name of
// UserUseCase.RunForgetRemovedIdentity.
const WorkflowForgetRemovedIdentity = "forget-removed-identity"

const (
	// deleteUserWait is how long Delete waits for the removal. The apps give up
	// on a call after 10 seconds, so the answer must reach them before that.
	deleteUserWait = 5 * time.Second
	// deleteUserDeadline is how long a removal is tried before it stops and an
	// admin is told.
	deleteUserDeadline = 24 * time.Hour
	// forgetRemovedIdentityDelay is how long after the identity is removed its
	// record is forgotten. An API reuses an answer that a token is active for
	// at most 60 seconds after it asked, so a request is admitted at latest 60
	// seconds after an ask that preceded the removal, and its handler runs at
	// most 30 seconds: after 90 seconds no request of the identity is
	// accepted or running.
	forgetRemovedIdentityDelay = 90 * time.Second
	// forgetRemovedIdentityDeadline is how long forgetting is tried before it
	// stops and an admin is told.
	forgetRemovedIdentityDeadline = 24 * time.Hour
)

// UserDeletion is the input of the delete-user workflow: the User and its
// identity, which the removal still needs once the User is gone.
type UserDeletion struct {
	// UserID is the User to remove.
	UserID string `json:"user_id"`
	// ProviderUserID is the identity provider's ID for that User.
	ProviderUserID string `json:"provider_user_id"`
}

// Delete implements [UserUseCase]. It refuses a user with a deletion blocker,
// starts the delete-user workflow and waits deleteUserWait for it.
func (uc *userUseCase) Delete(ctx context.Context, id string) error {
	user, err := uc.userRepo.Get(ctx, id)
	if err != nil {
		return err
	}
	blockers, err := uc.ListDeletionBlockers(ctx, id)
	if err != nil {
		return err
	}
	if err := checkNoDeletionBlocker(id, blockers); err != nil {
		return err
	}

	// A new id for every call: the id of an earlier removal that failed would
	// return that run and never try again. Runs of one User take turns on the
	// deletion lock.
	runID := requestWorkflowID(WorkflowDeleteUser+":"+id, entity.NewID())
	if err := uc.workflows.Start(ctx, WorkflowDeleteUser, runID,
		UserDeletion{UserID: id, ProviderUserID: user.ProviderUserID},
		WithDeadline(uc.clock().Add(deleteUserDeadline))); err != nil {
		return apperr.Wrap(err, codes.Unavailable, "start the user removal", slog.String("user_id", id))
	}
	_, err = Await[struct{}](ctx, uc.workflows, runID, deleteUserWait)
	switch {
	case errors.Is(err, ErrWorkflowFailed):
		// A run that removed the User and then failed on the identity deleted
		// the account: the caller cannot ask again, and an admin is told.
		if _, getErr := uc.userRepo.Get(ctx, id); errors.Is(getErr, apperr.ErrNotFound) {
			return nil
		}
		return err
	case errors.Is(err, apperr.ErrDeadlineExceeded):
		uc.logger.Info(ctx, "user removal goes on after the request",
			slog.String("user_id", id), slog.String("workflow_id", runID))
		return nil
	case err != nil:
		return apperr.Wrap(err, codes.Unavailable, "wait for the user removal",
			slog.String("user_id", id), slog.String("workflow_id", runID))
	}
	return nil
}

// RunDeleteUser implements [UserUseCase]. The User goes first: nothing can
// block what follows, because every record that blocks a deletion needs the
// User, and the removed identity gets no new one (User.Delete) until it is
// forgotten. A run that started before the forgetting existed (the patch
// `forget-removed-identity`) ends after the identity, as it did.
func (uc *userUseCase) RunDeleteUser(ctx context.Context, f Flow, in UserDeletion) (struct{}, error) {
	if _, err := Step(ctx, f, "remove-user", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, uc.removeUser(ctx, in.UserID)
	}); err != nil {
		return struct{}{}, err
	}
	if _, err := Step(ctx, f, "remove-identity", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, uc.identityRemover.DeleteIdentity(ctx, in.ProviderUserID)
	}); err != nil {
		return struct{}{}, err
	}
	patched, err := f.Patched(ctx, "forget-removed-identity")
	if err != nil || !patched {
		return struct{}{}, err
	}
	// The forgetting outlives this workflow, which ends right after starting
	// it; one run per identity.
	deadline, err := Step(ctx, f, "forget-deadline", func(context.Context) (time.Time, error) {
		return uc.clock().Add(forgetRemovedIdentityDelay + forgetRemovedIdentityDeadline), nil
	})
	if err != nil {
		return struct{}{}, err
	}
	return struct{}{}, f.Start(context.WithoutCancel(ctx), WorkflowForgetRemovedIdentity,
		WorkflowForgetRemovedIdentity+":"+in.ProviderUserID, in.ProviderUserID,
		WithDelay(forgetRemovedIdentityDelay), WithDeadline(deadline))
}

// RunForgetRemovedIdentity implements [UserUseCase].
func (uc *userUseCase) RunForgetRemovedIdentity(ctx context.Context, f Flow, providerUserID string) (struct{}, error) {
	return Step(ctx, f, "forget", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, uc.userRepo.ForgetRemovedIdentity(ctx, providerUserID)
	})
}

// removeUser removes the User userID under the User's deletion lock when the
// User has no deletion blocker, so none is created in between. A User already
// gone counts as removed, and is read first: reading the blockers of a removed
// User fails. Without identity removal configured it removes nothing.
func (uc *userUseCase) removeUser(ctx context.Context, userID string) error {
	if uc.identityRemover == nil {
		return apperr.New(codes.Internal, "identity removal is not configured")
	}
	return uc.userRepo.SerializeDeletion(ctx, userID, func(ctx context.Context) error {
		_, err := uc.userRepo.Get(ctx, userID)
		if errors.Is(err, apperr.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		blockers, err := uc.ListDeletionBlockers(ctx, userID)
		if err != nil {
			return err
		}
		if err := checkNoDeletionBlocker(userID, blockers); err != nil {
			return err
		}
		if err := uc.userRepo.Delete(ctx, userID); err != nil {
			return err
		}
		uc.logger.Info(ctx, "User deleted successfully", slog.String("user_id", userID))
		return nil
	})
}

// checkNoDeletionBlocker fails with FailedPrecondition when blockers is not
// empty.
func checkNoDeletionBlocker(userID string, blockers []entity.DeletionBlocker) error {
	if len(blockers) == 0 {
		return nil
	}
	return apperr.New(codes.FailedPrecondition, "user has activity in progress",
		slog.String("user_id", userID), slog.Int("blocker_kinds", len(blockers)))
}
