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

const (
	// deleteUserWait is how long Delete waits for the removal. The apps give up
	// on a call after 10 seconds, so the answer must reach them before that.
	deleteUserWait = 5 * time.Second
	// deleteUserDeadline is how long a removal is tried before it stops and an
	// admin is told.
	deleteUserDeadline = 24 * time.Hour
)

// Delete implements [UserUseCase]. It refuses a user with a deletion blocker,
// starts the delete-user workflow and waits deleteUserWait for it.
func (uc *userUseCase) Delete(ctx context.Context, id string) error {
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
	if err := uc.workflows.Start(ctx, WorkflowDeleteUser, runID, UserRequest{UserID: id},
		WithDeadline(uc.clock().Add(deleteUserDeadline))); err != nil {
		return err
	}
	_, err = Await[struct{}](ctx, uc.workflows, runID, deleteUserWait)
	if errors.Is(err, apperr.ErrDeadlineExceeded) && !errors.Is(err, ErrWorkflowFailed) {
		uc.logger.Info(ctx, "user removal goes on after the request",
			slog.String("user_id", id), slog.String("workflow_id", runID))
		return nil
	}
	return err
}

// RunDeleteUser implements [UserUseCase]. The deletion lock is released
// between tries of a step, so each step checks the blockers again.
func (uc *userUseCase) RunDeleteUser(ctx context.Context, f Flow, in UserRequest) (struct{}, error) {
	if _, err := Step(ctx, f, "remove-identity", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, uc.removeUnblocked(ctx, in.UserID, func(ctx context.Context, user *entity.User) error {
			if uc.identityRemover == nil {
				return apperr.New(codes.Internal, "identity removal is not configured")
			}
			return uc.identityRemover.DeleteIdentity(ctx, user.ProviderUserID)
		})
	}); err != nil {
		return struct{}{}, err
	}
	return Step(ctx, f, "remove-user", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, uc.removeUnblocked(ctx, in.UserID, func(ctx context.Context, user *entity.User) error {
			if err := uc.userRepo.Delete(ctx, user.ID); err != nil {
				return err
			}
			uc.logger.Info(ctx, "User deleted successfully",
				slog.String("user_id", user.ID),
				slog.String("provider_user_id", user.ProviderUserID),
			)
			return nil
		})
	})
}

// removeUnblocked runs remove for the User userID under the User's deletion
// lock when the User has no deletion blocker. A User already gone counts as
// removed, and is read first: reading the blockers of a removed User fails.
func (uc *userUseCase) removeUnblocked(ctx context.Context, userID string, remove func(context.Context, *entity.User) error) error {
	return uc.userRepo.SerializeDeletion(ctx, userID, func(ctx context.Context) error {
		user, err := uc.userRepo.Get(ctx, userID)
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
		return remove(ctx, user)
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
