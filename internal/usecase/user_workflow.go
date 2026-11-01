package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

// WorkflowReissueEmailVerification is the registered name of
// UserUseCase.RunReissueEmailVerification.
const WorkflowReissueEmailVerification = "reissue-email-verification"

// WorkflowSendPasskeyRecoveryLink is the registered name of
// UserUseCase.RunSendPasskeyRecoveryLink.
const WorkflowSendPasskeyRecoveryLink = "send-passkey-recovery-link"

// WorkflowNotifyPasskeyChange is the registered name of
// UserUseCase.RunNotifyPasskeyChange.
const WorkflowNotifyPasskeyChange = "notify-passkey-change"

// PasskeyChangeNotice is the input of the notify-passkey-change workflow: the
// User of an announced Passkey and the change to email.
type PasskeyChangeNotice struct {
	// UserID is the User the Passkey belongs to.
	UserID string `json:"user_id"`
	// Change is the kind of change, the Passkey's name and the time of the
	// change.
	Change entity.PasskeyChange `json:"change"`
}

// UserRequest is the input of the user workflows: the User the request is
// for.
type UserRequest struct {
	// UserID is the User the request is for.
	UserID string `json:"user_id"`
}

// requestWorkflowID names a workflow after the request itself: requestID is
// new for every request (or the id of the announcement it runs for), so each
// request starts one run.
func requestWorkflowID(workflow, requestID string) string {
	return workflow + ":" + requestID
}

// RunReissueEmailVerification is the reissue-email-verification workflow: it
// sends a fresh verification code through ReissueEmailVerification in one
// step. A step that fails with Unavailable is tried again.
func (uc *userUseCase) RunReissueEmailVerification(ctx context.Context, f Flow, in UserRequest) (struct{}, error) {
	return Step(ctx, f, "reissue", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, uc.ReissueEmailVerification(ctx, in.UserID)
	})
}

// RunSendPasskeyRecoveryLink is the send-passkey-recovery-link workflow: it
// sends the registration link through SendPasskeyRecoveryLink in one step. A
// step that fails with Unavailable is tried again.
func (uc *userUseCase) RunSendPasskeyRecoveryLink(ctx context.Context, f Flow, in UserRequest) (struct{}, error) {
	return Step(ctx, f, "send", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, uc.SendPasskeyRecoveryLink(ctx, in.UserID)
	})
}

// StartNotifyPasskeyChange implements [UserUseCase].
func (uc *userUseCase) StartNotifyPasskeyChange(ctx context.Context, announcementID string, notice PasskeyChangeNotice) error {
	if err := uc.workflows.Start(ctx, WorkflowNotifyPasskeyChange,
		requestWorkflowID(WorkflowNotifyPasskeyChange, announcementID), notice); err != nil {
		return apperr.Wrap(err, codes.Unavailable, "start the passkey change email",
			slog.String("user_id", notice.UserID), slog.String("kind", string(notice.Change.Kind)))
	}
	return nil
}

// RunNotifyPasskeyChange implements [UserUseCase].
func (uc *userUseCase) RunNotifyPasskeyChange(ctx context.Context, f Flow, in PasskeyChangeNotice) (struct{}, error) {
	return Step(ctx, f, "email", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, uc.NotifyPasskeyChange(ctx, in.UserID, in.Change)
	})
}

// WorkflowSendVerification is the registered name of
// UserUseCase.RunSendVerification.
const WorkflowSendVerification = "send-verification"

// sendVerificationAttempts is how many times RunSendVerification tries to
// send the verification email: the first try and 3 retries.
const sendVerificationAttempts = 4

// sendVerificationRetryWait is how long RunSendVerification waits before it
// tries again.
const sendVerificationRetryWait = time.Minute

// sendVerificationTopic is the topic RunSendVerification waits on between
// tries; no message is ever sent to it.
const sendVerificationTopic = "send-verification-retry"

// StartSendVerification implements [UserUseCase].
func (uc *userUseCase) StartSendVerification(ctx context.Context, announcementID, userID string) error {
	if err := uc.workflows.Start(ctx, WorkflowSendVerification,
		requestWorkflowID(WorkflowSendVerification, announcementID), UserRequest{UserID: userID}); err != nil {
		return apperr.Wrap(err, codes.Unavailable, "start the verification email", slog.String("user_id", userID))
	}
	return nil
}

// RunSendVerification implements [UserUseCase]. Each try is a step of its
// own whose failure is recorded, not returned, so a failed send is tried
// again a minute later instead of at once, and given up after the last try.
func (uc *userUseCase) RunSendVerification(ctx context.Context, f Flow, in UserRequest) (struct{}, error) {
	for attempt := 1; attempt <= sendVerificationAttempts; attempt++ {
		sent, err := Step(ctx, f, fmt.Sprintf("send-%d", attempt), func(ctx context.Context) (bool, error) {
			if err := uc.sendVerification(ctx, in.UserID); err != nil {
				uc.logger.Warn(ctx, "verification email not sent",
					slog.String("user_id", in.UserID), slog.Int("attempt", attempt), slog.String("error", err.Error()))
				return false, nil
			}
			return true, nil
		})
		if err != nil || sent {
			return struct{}{}, err
		}
		if attempt == sendVerificationAttempts {
			break
		}
		if _, _, err := f.Recv(ctx, sendVerificationTopic, sendVerificationRetryWait); err != nil {
			return struct{}{}, err
		}
	}
	uc.logger.Error(ctx, "verification email given up after every try", errors.New("verification email not sent"),
		slog.String("user_id", in.UserID), slog.Int("attempts", sendVerificationAttempts))
	return struct{}{}, nil
}

// sendVerification sends the verification email of the User userID. A User
// that no longer exists, an address already verified, or email verification
// that is not configured in this process finishes without sending.
func (uc *userUseCase) sendVerification(ctx context.Context, userID string) error {
	attrs := slog.String("user_id", userID)
	if uc.emailVerifier == nil {
		uc.logger.Warn(ctx, "email verifier not configured, skipping verification", attrs)
		return nil
	}
	user, err := uc.userRepo.Get(ctx, userID)
	if errors.Is(err, apperr.ErrNotFound) {
		uc.logger.Info(ctx, "user deleted since sign-up, skipping verification", attrs)
		return nil
	}
	if err != nil {
		return fmt.Errorf("get user %q for email verification: %w", userID, err)
	}
	err = uc.emailVerifier.SendVerification(ctx, user)
	if errors.Is(err, apperr.ErrFailedPrecondition) {
		uc.logger.Info(ctx, "email already verified, skipping verification", attrs)
		return nil
	}
	if err != nil {
		return fmt.Errorf("send the verification email of user %q: %w", userID, err)
	}
	uc.logger.Info(ctx, "email verification sent", attrs)
	return nil
}
