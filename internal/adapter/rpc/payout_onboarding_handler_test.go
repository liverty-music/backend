package rpc_test

import (
	"context"
	"testing"

	organizerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// failingOnboardingUCStub is an OnboardingUseCase that fails the test when it
// is called, for cases that must stop before the onboarding usecase runs.
type failingOnboardingUCStub struct {
	t *testing.T
}

var _ usecase.OnboardingUseCase = (*failingOnboardingUCStub)(nil)

func (s *failingOnboardingUCStub) GetOrCreateOnboarding(context.Context, string) (*entity.OrganizerConnectedAccount, string, error) {
	s.t.Helper()
	s.t.Error("OnboardingUseCase.GetOrCreateOnboarding must not be called")
	return nil, "", nil
}

// TestPayoutOnboardingHandler_GetPayoutOnboarding verifies that a failure of
// OrganizerUseCase.ResolveCaller is returned unchanged and stops the request
// before the onboarding usecase runs.
func TestPayoutOnboardingHandler_GetPayoutOnboarding(t *testing.T) {
	t.Parallel()

	type args struct {
		ctx context.Context
	}
	type dep struct {
		resolveCallerErr error
	}
	tests := []struct {
		name     string
		args     args
		dep      dep
		wantCode connect.Code
	}{
		{
			// @spec components/adapter/organizer/api/rpc/payout-onboarding "Tenant with no Organizer"
			name:     "return PERMISSION_DENIED unchanged when ResolveCaller finds no organizer for the tenant",
			args:     args{ctx: orgCtx(testZitadelOrgID)},
			dep:      dep{resolveCallerErr: apperr.New(apperr.ErrPermissionDenied.Code, "permission denied")},
			wantCode: connect.CodePermissionDenied,
		},
		{
			// @spec components/adapter/organizer/api/rpc/payout-onboarding "Deactivated Organizer"
			name:     "return FAILED_PRECONDITION unchanged when ResolveCaller reports the organizer is deactivated",
			args:     args{ctx: orgCtx(testZitadelOrgID)},
			dep:      dep{resolveCallerErr: apperr.New(apperr.ErrFailedPrecondition.Code, "organizer is deactivated")},
			wantCode: connect.CodeFailedPrecondition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			organizerUC := ucmocks.NewMockOrganizerUseCase(t)
			organizerUC.EXPECT().
				ResolveCaller(mock.Anything, testZitadelOrgID).
				Return(nil, tt.dep.resolveCallerErr).
				Once()

			logger, err := logging.New()
			require.NoError(t, err)
			h := rpc.NewPayoutOnboardingHandler(&failingOnboardingUCStub{t: t}, organizerUC, logger)

			resp, err := h.GetPayoutOnboarding(tt.args.ctx, connect.NewRequest(&organizerv1.GetPayoutOnboardingRequest{}))

			require.Error(t, err)
			assert.ErrorIs(t, err, tt.dep.resolveCallerErr)
			assert.Equal(t, tt.wantCode, connectCodeOf(err))
			assert.Nil(t, resp)
		})
	}
}
