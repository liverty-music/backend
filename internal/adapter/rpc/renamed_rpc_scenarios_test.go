package rpc_test

import (
	"context"
	"testing"

	payoutaccountv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/payout_account/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// onboardingUCStub is a function-field PayoutAccountUseCase.
type onboardingUCStub struct {
	fn func(organizerID string) (*entity.PayoutAccount, string, error)
}

func (s *onboardingUCStub) Get(_ context.Context, organizerID string) (*entity.PayoutAccount, string, error) {
	return s.fn(organizerID)
}

func TestPayoutAccountHandler_GetScenarios(t *testing.T) {
	t.Parallel()

	newHandler := func(t *testing.T, uc usecase.PayoutAccountUseCase) *handler.PayoutAccountHandler {
		t.Helper()
		organizerUC := ucmocks.NewMockOrganizerUseCase(t)
		organizerUC.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(&entity.Organizer{ID: "org-1"}, nil).Once()
		logger, err := logging.New()
		require.NoError(t, err)
		return handler.NewPayoutAccountHandler(uc, organizerUC, logger)
	}

	t.Run("operator opens payout settings", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/payout-onboarding "Operator opens payout settings"
		h := newHandler(t, &onboardingUCStub{fn: func(organizerID string) (*entity.PayoutAccount, string, error) {
			return &entity.PayoutAccount{OrganizerID: organizerID, ProviderAccountID: "acct_1", Status: entity.PayoutOnboardingStatusPending},
				"https://connect.example/onboarding", nil
		}})
		resp, err := h.Get(orgCtx(testProviderOrganizerID), connect.NewRequest(&payoutaccountv1.GetRequest{}))
		require.NoError(t, err)
		assert.Equal(t, "org-1", resp.Msg.PayoutAccount.OrganizerId.Value)
		assert.Equal(t, "acct_1", resp.Msg.PayoutAccount.ProviderAccountId.GetValue())
		assert.Equal(t, "https://connect.example/onboarding", resp.Msg.OnboardingUrl, "an onboarding link while the account is not active")
	})

	t.Run("organizer gone meanwhile", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/payout-onboarding "Organizer gone meanwhile"
		h := newHandler(t, &onboardingUCStub{fn: func(string) (*entity.PayoutAccount, string, error) {
			return nil, "", apperr.New(codes.NotFound, "organizer not found")
		}})
		_, err := h.Get(orgCtx(testProviderOrganizerID), connect.NewRequest(&payoutaccountv1.GetRequest{}))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})
}
