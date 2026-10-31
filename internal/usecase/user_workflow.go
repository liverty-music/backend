package usecase

import (
	"context"
	"log/slog"

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
