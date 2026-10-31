package usecase

import (
	"context"
)

// WorkflowReissueEmailVerification is the registered name of
// UserUseCase.RunReissueEmailVerification.
const WorkflowReissueEmailVerification = "reissue-email-verification"

// WorkflowSendPasskeyRecoveryLink is the registered name of
// UserUseCase.RunSendPasskeyRecoveryLink.
const WorkflowSendPasskeyRecoveryLink = "send-passkey-recovery-link"

// UserRequest is the input of the user workflows: the User the request is
// for.
type UserRequest struct {
	// UserID is the User the request is for.
	UserID string `json:"user_id"`
}

// requestWorkflowID names a workflow after the request itself: requestID is
// new for every request, so each request starts one run.
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
