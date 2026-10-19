package rpc_test

import (
	"context"
	"testing"

	payoutaccountv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/payout_account/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc"
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

// payoutAccountUCStub is a function-field PayoutAccountUseCase. A method
// whose function is nil fails the test when called.
type payoutAccountUCStub struct {
	t                 *testing.T
	getFn             func(organizerID string) (*entity.PayoutAccount, error)
	startOnboardingFn func(organizerID string) (string, error)
}

var _ usecase.PayoutAccountUseCase = (*payoutAccountUCStub)(nil)

func (s *payoutAccountUCStub) Get(_ context.Context, organizerID string) (*entity.PayoutAccount, error) {
	if s.getFn == nil {
		s.t.Error("PayoutAccountUseCase.Get must not be called")
		return nil, nil
	}
	return s.getFn(organizerID)
}

func (s *payoutAccountUCStub) StartOnboarding(_ context.Context, organizerID string) (string, error) {
	if s.startOnboardingFn == nil {
		s.t.Error("PayoutAccountUseCase.StartOnboarding must not be called")
		return "", nil
	}
	return s.startOnboardingFn(organizerID)
}

// newPayoutAccountHandler builds a handler whose audience gate resolves the
// caller to resolved, or fails with resolveErr.
func newPayoutAccountHandler(t *testing.T, uc usecase.PayoutAccountUseCase, resolved *entity.Organizer, resolveErr error) *rpc.PayoutAccountHandler {
	t.Helper()
	organizerUC := ucmocks.NewMockOrganizerUseCase(t)
	organizerUC.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(resolved, resolveErr).Once()
	logger, err := logging.New()
	require.NoError(t, err)
	return rpc.NewPayoutAccountHandler(uc, organizerUC, logger)
}

// TestPayoutAccountHandler_AudienceGate verifies that a failure of
// OrganizerUseCase.ResolveCaller is returned unchanged by every method and
// stops the request before the payout account usecase runs.
func TestPayoutAccountHandler_AudienceGate(t *testing.T) {
	t.Parallel()

	methods := []struct {
		name string
		call func(h *rpc.PayoutAccountHandler) error
	}{
		{name: "Get", call: func(h *rpc.PayoutAccountHandler) error {
			_, err := h.Get(orgCtx(testProviderOrganizerID), connect.NewRequest(&payoutaccountv1.GetRequest{}))
			return err
		}},
		{name: "StartOnboarding", call: func(h *rpc.PayoutAccountHandler) error {
			_, err := h.StartOnboarding(orgCtx(testProviderOrganizerID), connect.NewRequest(&payoutaccountv1.StartOnboardingRequest{}))
			return err
		}},
	}
	tests := []struct {
		name             string
		resolveCallerErr error
		wantCode         connect.Code
	}{
		{
			// @spec components/adapter/organizer/api/rpc/payout-account "Organization with no Organizer"
			name:             "return PERMISSION_DENIED unchanged when ResolveCaller finds no organizer for the Organization",
			resolveCallerErr: apperr.New(apperr.ErrPermissionDenied.Code, "permission denied"),
			wantCode:         connect.CodePermissionDenied,
		},
		{
			// @spec components/adapter/organizer/api/rpc/payout-account "Deactivated Organizer"
			name:             "return FAILED_PRECONDITION unchanged when ResolveCaller reports the organizer is deactivated",
			resolveCallerErr: apperr.New(apperr.ErrFailedPrecondition.Code, "organizer is deactivated"),
			wantCode:         connect.CodeFailedPrecondition,
		},
	}

	for _, m := range methods {
		for _, tt := range tests {
			t.Run(m.name+": "+tt.name, func(t *testing.T) {
				t.Parallel()
				h := newPayoutAccountHandler(t, &payoutAccountUCStub{t: t}, nil, tt.resolveCallerErr)

				err := m.call(h)

				assert.ErrorIs(t, err, tt.resolveCallerErr)
				assert.Equal(t, tt.wantCode, connectCodeOf(err))
			})
		}
	}
}

func TestPayoutAccountHandler_Get(t *testing.T) {
	t.Parallel()

	caller := &entity.Organizer{ID: "org-1"}

	// @spec components/adapter/organizer/api/rpc/payout-account "Member opens payout settings"
	t.Run("Member opens payout settings: returns the caller's account and status", func(t *testing.T) {
		t.Parallel()
		h := newPayoutAccountHandler(t, &payoutAccountUCStub{t: t, getFn: func(organizerID string) (*entity.PayoutAccount, error) {
			return &entity.PayoutAccount{OrganizerID: organizerID, ProviderAccountID: "acct_1", Status: entity.PayoutOnboardingStatusPending}, nil
		}}, caller, nil)

		resp, err := h.Get(orgCtx(testProviderOrganizerID), connect.NewRequest(&payoutaccountv1.GetRequest{}))

		require.NoError(t, err)
		assert.Equal(t, "org-1", resp.Msg.GetPayoutAccount().GetOrganizerId().GetValue())
		assert.Equal(t, "acct_1", resp.Msg.GetPayoutAccount().GetProviderAccountId().GetValue())
		assert.NotZero(t, resp.Msg.GetPayoutAccount().GetStatus())
	})

	// @spec components/adapter/organizer/api/rpc/payout-account "No payout account yet"
	t.Run("No payout account yet: fails with NOT_FOUND", func(t *testing.T) {
		t.Parallel()
		h := newPayoutAccountHandler(t, &payoutAccountUCStub{t: t, getFn: func(string) (*entity.PayoutAccount, error) {
			return nil, apperr.New(codes.NotFound, "payout account not found")
		}}, caller, nil)

		_, err := h.Get(orgCtx(testProviderOrganizerID), connect.NewRequest(&payoutaccountv1.GetRequest{}))

		assert.Equal(t, connect.CodeNotFound, connectCodeOf(err))
	})
}

func TestPayoutAccountHandler_StartOnboarding(t *testing.T) {
	t.Parallel()

	caller := &entity.Organizer{ID: "org-1"}
	tests := []struct {
		name     string
		ucURL    string
		ucErr    error
		wantURL  string
		wantCode connect.Code
	}{
		{
			// @spec components/adapter/organizer/api/rpc/payout-account "Member starts onboarding"
			name:    "Member starts onboarding: returns the onboarding link",
			ucURL:   "https://connect.example/setup/1",
			wantURL: "https://connect.example/setup/1",
		},
		{
			// @spec components/adapter/organizer/api/rpc/payout-account "Organizer gone meanwhile"
			name:     "Organizer gone meanwhile: NotFound from the usecase becomes PERMISSION_DENIED",
			ucErr:    apperr.New(codes.NotFound, "organizer not found"),
			wantCode: connect.CodePermissionDenied,
		},
		{
			name:     "returns any other failure unchanged",
			ucErr:    apperr.New(codes.Unavailable, "payment provider unreachable"),
			wantCode: connect.CodeUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var calledFor string
			h := newPayoutAccountHandler(t, &payoutAccountUCStub{t: t, startOnboardingFn: func(organizerID string) (string, error) {
				calledFor = organizerID
				return tt.ucURL, tt.ucErr
			}}, caller, nil)

			resp, err := h.StartOnboarding(orgCtx(testProviderOrganizerID), connect.NewRequest(&payoutaccountv1.StartOnboardingRequest{}))

			assert.Equal(t, "org-1", calledFor, "the caller's own Organizer")
			if tt.wantCode != 0 {
				assert.Equal(t, tt.wantCode, connectCodeOf(err))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantURL, resp.Msg.GetOnboardingUrl().GetValue())
		})
	}
}
