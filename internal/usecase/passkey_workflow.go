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

// WorkflowRecordPasskeyChange is the registered name of
// PasskeyUseCase.RunRecordChange.
const WorkflowRecordPasskeyChange = "record-passkey-change"

// passkeyAccountPoll is how long RecordChange waits before it looks again for
// the User of an added passkey's identity.
const passkeyAccountPoll = 30 * time.Second

// passkeyAccountTopic is the topic RecordChange waits on between looks; no
// message is ever sent to it, so each wait lasts passkeyAccountPoll.
const passkeyAccountTopic = "passkey-account"

// recordPasskeyChangeID names the record-passkey-change workflow of a change
// after its provider passkey id and kind, so a repeated notice starts nothing
// new.
func recordPasskeyChangeID(providerPasskeyID string, kind entity.PasskeyChangeKind) string {
	return WorkflowRecordPasskeyChange + ":" + providerPasskeyID + ":" + string(kind)
}

// StartRecordChange implements [PasskeyUseCase].
func (uc *passkeyUseCase) StartRecordChange(ctx context.Context, change entity.UserPasskeyChangedData) error {
	if err := uc.workflows.Start(ctx, WorkflowRecordPasskeyChange,
		recordPasskeyChangeID(change.ProviderPasskeyID, entity.PasskeyChangeKind(change.Kind)), change); err != nil {
		return apperr.Wrap(err, codes.Unavailable, "start recording the passkey change",
			slog.String("provider_passkey_id", change.ProviderPasskeyID), slog.String("kind", change.Kind))
	}
	return nil
}

// RunRecordChange implements [PasskeyUseCase]. An added passkey whose identity
// has no User yet is looked for again every 30 seconds, until 10 minutes
// after the first run; a removal first waits for the run of the same
// passkey's addition to end.
func (uc *passkeyUseCase) RunRecordChange(ctx context.Context, f Flow, in entity.UserPasskeyChangedData) (struct{}, error) {
	switch entity.PasskeyChangeKind(in.Kind) {
	case entity.PasskeyChangeRemoved:
		if _, err := Step(ctx, f, "await-added", func(ctx context.Context) (struct{}, error) {
			return struct{}{}, uc.awaitAdded(ctx, in.ProviderPasskeyID)
		}); err != nil {
			return struct{}{}, err
		}
		return Step(ctx, f, "remove", func(ctx context.Context) (struct{}, error) {
			return struct{}{}, uc.RecordChange(ctx, in)
		})
	case entity.PasskeyChangeAdded:
	default:
		return struct{}{}, apperr.New(codes.InvalidArgument, "unknown kind of passkey change", slog.String("kind", in.Kind))
	}

	firstRun, err := Step(ctx, f, "first-run", func(context.Context) (time.Time, error) { return uc.now(), nil })
	if err != nil {
		return struct{}{}, err
	}
	for look := 1; ; look++ {
		waiting, err := Step(ctx, f, fmt.Sprintf("record-%d", look), func(ctx context.Context) (bool, error) {
			err := uc.RecordChange(ctx, in)
			if !errors.Is(err, ErrPasskeyAccountPending) {
				return false, err
			}
			if uc.now().Sub(firstRun) >= passkeyAccountWait {
				uc.logger.Info(ctx, "passkey not recorded: no user has the identity 10 minutes after the first run",
					slog.String("provider_user_id", in.ProviderUserID), slog.String("provider_passkey_id", in.ProviderPasskeyID))
				return false, nil
			}
			return true, nil
		})
		if err != nil || !waiting {
			return struct{}{}, err
		}
		if _, _, err := f.Recv(ctx, passkeyAccountTopic, passkeyAccountPoll); err != nil {
			return struct{}{}, err
		}
	}
}

// awaitAdded waits for the record-passkey-change run of the addition of
// providerPasskeyID to end, so a removal is never followed by that addition
// storing the Passkey. A passkey whose addition was never seen has no run.
func (uc *passkeyUseCase) awaitAdded(ctx context.Context, providerPasskeyID string) error {
	err := uc.workflows.Await(ctx, recordPasskeyChangeID(providerPasskeyID, entity.PasskeyChangeAdded), passkeyAccountWait+time.Minute, nil)
	switch {
	case err == nil, errors.Is(err, apperr.ErrNotFound):
		return nil
	case errors.Is(err, apperr.ErrDeadlineExceeded), errors.Is(err, apperr.ErrUnavailable):
		return apperr.Wrap(err, codes.Unavailable, "wait for the addition of the passkey", slog.String("provider_passkey_id", providerPasskeyID))
	default:
		// The addition's run ended with a failure, so it stores nothing.
		return nil
	}
}
