package rpc_test

import (
	"context"
	"net/http"
	"testing"

	userv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/user/v1/userv1connect"
	entitypb "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	userv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/user/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	testCallerUserID = "user-1"
	testCallerExtID  = "ext-123"
)

// The caller's User is resolved by UserUseCase.ResolveCaller (see
// internal/usecase/user_uc.go); these tests exercise the handler's
// proto<->entity mapping, the checks it runs before the usecase, and error
// propagation.

func newUserHandler(t *testing.T) (*rpc.UserHandler, *mocks.MockUserUseCase) {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	userUC := mocks.NewMockUserUseCase(t)
	return rpc.NewUserHandler(userUC, logger), userUC
}

// newUserBoundaryClient serves the fan UserService behind the consumer
// server's authn middleware and validation interceptor.
func newUserBoundaryClient(t *testing.T, h *rpc.UserHandler) userv1connect.UserServiceClient {
	t.Helper()
	srv := newFanBoundaryServer(t, func(opts ...connect.HandlerOption) (string, http.Handler) {
		return userv1connect.NewUserServiceHandler(h, opts...)
	})
	return userv1connect.NewUserServiceClient(srv.Client(), srv.URL)
}

func callerUser() *entity.User {
	return &entity.User{ID: testCallerUserID, ExternalID: testCallerExtID, Email: "test@example.com", Name: "Test User"}
}

func TestUserHandler_NotSignedIn(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/fan/api/rpc/user "Not signed in"
	t.Run("every call fails with UNAUTHENTICATED without a sign-in token", func(t *testing.T) {
		t.Parallel()
		// The mock fails the test if any usecase method runs.
		h, _ := newUserHandler(t)
		client := newUserBoundaryClient(t, h)
		ctx := context.Background()

		_, getErr := client.Get(ctx, connect.NewRequest(&userv1.GetRequest{}))
		_, createErr := client.Create(ctx, connect.NewRequest(&userv1.CreateRequest{Email: &entitypb.UserEmail{Value: "a@example.com"}}))
		_, updateErr := client.Update(ctx, connect.NewRequest(&userv1.UpdateRequest{PhoneNumber: &entitypb.PhoneNumber{Value: "+819098765432"}}))
		_, langErr := client.SetPreferredLanguage(ctx, connect.NewRequest(&userv1.SetPreferredLanguageRequest{PreferredLanguage: "en"}))
		_, homeErr := client.SetHome(ctx, connect.NewRequest(&userv1.SetHomeRequest{Home: &entitypb.Home{CountryCode: "JP", Level_1: "JP-13"}}))
		_, resendErr := client.ResendEmailVerification(ctx, connect.NewRequest(&userv1.ResendEmailVerificationRequest{}))

		for _, err := range []error{getErr, createErr, updateErr, langErr, homeErr, resendErr} {
			assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
		}
	})
}

func TestUserHandler_Get(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/fan/api/rpc/user "Own account"
	t.Run("proceed for the caller's User", func(t *testing.T) {
		t.Parallel()
		h, userUC := newUserHandler(t)
		userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID).Return(callerUser(), nil).Once()

		resp, err := h.Get(authedCtx(testCallerExtID), connect.NewRequest(&userv1.GetRequest{}))

		require.NoError(t, err)
		assert.Equal(t, testCallerUserID, resp.Msg.GetUser().GetId().GetValue())
	})

	// @spec components/adapter/fan/api/rpc/user "Caller reads their profile"
	t.Run("return the caller's home and preferred language", func(t *testing.T) {
		t.Parallel()
		h, userUC := newUserHandler(t)
		u := callerUser()
		u.PreferredLanguage = "ja"
		u.Home = &entity.Home{CountryCode: "JP", Level1: "JP-13"}
		userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID).Return(u, nil).Once()

		resp, err := h.Get(authedCtx(testCallerExtID), connect.NewRequest(&userv1.GetRequest{}))

		require.NoError(t, err)
		assert.Equal(t, "test@example.com", resp.Msg.GetUser().GetEmail().GetValue())
		assert.Equal(t, "ja", resp.Msg.GetUser().GetPreferredLanguage())
		assert.Equal(t, "JP-13", resp.Msg.GetUser().GetHome().GetLevel_1())
	})

	// @spec components/adapter/fan/api/rpc/user "Missing user id"
	t.Run("act on the caller's own User though the request carries no User id", func(t *testing.T) {
		t.Parallel()
		h, userUC := newUserHandler(t)
		userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID).Return(callerUser(), nil).Once()
		client := newUserBoundaryClient(t, h)

		resp, err := client.Get(context.Background(), withFanToken(connect.NewRequest(&userv1.GetRequest{})))

		require.NoError(t, err)
		assert.Equal(t, testCallerUserID, resp.Msg.GetUser().GetId().GetValue())
	})

	// @spec components/adapter/fan/api/rpc/user "Another user's account"
	t.Run("resolve only the caller's identity", func(t *testing.T) {
		t.Parallel()
		// No request names a User: the generated requests have no user id
		// field, and the handler hands only the caller's identity to the
		// usecase, so another User's account can never be named.
		h, userUC := newUserHandler(t)
		userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID).Return(callerUser(), nil).Once()

		resp, err := h.Get(authedCtx(testCallerExtID), connect.NewRequest(&userv1.GetRequest{}))

		require.NoError(t, err)
		assert.Equal(t, testCallerUserID, resp.Msg.GetUser().GetId().GetValue())
		assert.Nil(t, (&userv1.GetRequest{}).ProtoReflect().Descriptor().Fields().ByName("user_id"), "GetRequest names no User")
		assert.Nil(t, (&userv1.SetHomeRequest{}).ProtoReflect().Descriptor().Fields().ByName("user_id"), "SetHomeRequest names no User")
		assert.Nil(t, (&userv1.SetPreferredLanguageRequest{}).ProtoReflect().Descriptor().Fields().ByName("user_id"), "SetPreferredLanguageRequest names no User")
		assert.Nil(t, (&userv1.UpdateRequest{}).ProtoReflect().Descriptor().Fields().ByName("user_id"), "UpdateRequest names no User")
		assert.Nil(t, (&userv1.ResendEmailVerificationRequest{}).ProtoReflect().Descriptor().Fields().ByName("user_id"), "ResendEmailVerificationRequest names no User")
	})

	// @spec components/adapter/fan/api/rpc/user "Caller has no account"
	t.Run("return NOT_FOUND when the caller has no User", func(t *testing.T) {
		t.Parallel()
		h, userUC := newUserHandler(t)
		userUC.EXPECT().ResolveCaller(mock.Anything, "ext-unknown").
			Return(nil, apperr.New(apperr.ErrNotFound.Code, "user not found")).Once()

		resp, err := h.Get(authedCtx("ext-unknown"), connect.NewRequest(&userv1.GetRequest{}))

		assert.Nil(t, resp)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestUserHandler_Create(t *testing.T) {
	t.Parallel()

	identityCtx := func() context.Context {
		return auth.WithClaims(context.Background(), &auth.Claims{Sub: testCallerExtID, Email: "id@example.com", Name: "Identity Name"})
	}

	// @spec components/adapter/fan/api/rpc/user "First sign-in"
	t.Run("register the sign-in identity with the preferred language", func(t *testing.T) {
		t.Parallel()
		h, userUC := newUserHandler(t)
		userUC.EXPECT().Create(mock.Anything, mock.MatchedBy(func(nu *entity.NewUser) bool {
			return nu.ExternalID == testCallerExtID && nu.Email == "id@example.com" && nu.Name == "Identity Name" && nu.PreferredLanguage == "ja"
		})).Return(&entity.User{ID: testCallerUserID, ExternalID: testCallerExtID, Email: "id@example.com", Name: "Identity Name", PreferredLanguage: "ja"}, nil).Once()

		lang := "ja"
		resp, err := h.Create(identityCtx(), connect.NewRequest(&userv1.CreateRequest{
			Email:             &entitypb.UserEmail{Value: "id@example.com"},
			PreferredLanguage: &lang,
		}))

		require.NoError(t, err)
		assert.Equal(t, "id@example.com", resp.Msg.GetUser().GetEmail().GetValue())
		assert.Equal(t, "ja", resp.Msg.GetUser().GetPreferredLanguage())
	})

	// @spec components/adapter/fan/api/rpc/user "Request email differs from the identity"
	t.Run("take the email from the sign-in identity, not the request", func(t *testing.T) {
		t.Parallel()
		h, userUC := newUserHandler(t)
		userUC.EXPECT().Create(mock.Anything, mock.MatchedBy(func(nu *entity.NewUser) bool {
			return nu.Email == "id@example.com"
		})).Return(&entity.User{ID: testCallerUserID, Email: "id@example.com"}, nil).Once()

		resp, err := h.Create(identityCtx(), connect.NewRequest(&userv1.CreateRequest{
			Email: &entitypb.UserEmail{Value: "other@example.com"},
		}))

		require.NoError(t, err)
		assert.Equal(t, "id@example.com", resp.Msg.GetUser().GetEmail().GetValue())
	})

	// @spec components/adapter/fan/api/rpc/user "Returning identity"
	t.Run("return the existing User the usecase returns", func(t *testing.T) {
		t.Parallel()
		h, userUC := newUserHandler(t)
		existing := callerUser()
		userUC.EXPECT().Create(mock.Anything, mock.Anything).Return(existing, nil).Once()

		resp, err := h.Create(identityCtx(), connect.NewRequest(&userv1.CreateRequest{
			Email: &entitypb.UserEmail{Value: "id@example.com"},
		}))

		require.NoError(t, err)
		assert.Equal(t, testCallerUserID, resp.Msg.GetUser().GetId().GetValue())
	})

	// @spec components/adapter/fan/api/rpc/user "Missing email in the request"
	t.Run("return INVALID_ARGUMENT without an email", func(t *testing.T) {
		t.Parallel()
		h, _ := newUserHandler(t)
		client := newUserBoundaryClient(t, h)

		_, err := client.Create(context.Background(), withFanToken(connect.NewRequest(&userv1.CreateRequest{})))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}

func TestUserHandler_Update(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/fan/api/rpc/user "Fan corrects their phone number"
	t.Run("change only the phone number", func(t *testing.T) {
		t.Parallel()
		h, userUC := newUserHandler(t)
		updated := callerUser()
		updated.FullName = "山田 花子"
		updated.PhoneNumber = "+819098765432"
		userUC.EXPECT().Update(mock.Anything, testCallerExtID,
			mock.MatchedBy(func(fullName *string) bool { return fullName == nil }),
			mock.MatchedBy(func(phone *string) bool { return phone != nil && *phone == "+819098765432" })).
			Return(updated, nil).Once()

		resp, err := h.Update(authedCtx(testCallerExtID), connect.NewRequest(&userv1.UpdateRequest{
			PhoneNumber: &entitypb.PhoneNumber{Value: "+819098765432"},
		}))

		require.NoError(t, err)
		assert.Equal(t, "+819098765432", resp.Msg.GetUser().GetPhoneNumber().GetValue())
		assert.Equal(t, "山田 花子", resp.Msg.GetUser().GetFullName().GetValue(), "the full name is unchanged")
	})

	// @spec components/adapter/fan/api/rpc/user "Nothing to update"
	t.Run("return INVALID_ARGUMENT with neither a full name nor a phone number", func(t *testing.T) {
		t.Parallel()
		// The mock fails the test if the usecase runs.
		h, _ := newUserHandler(t)

		_, direct := h.Update(authedCtx(testCallerExtID), connect.NewRequest(&userv1.UpdateRequest{}))
		_, viaBoundary := newUserBoundaryClient(t, h).Update(context.Background(), withFanToken(connect.NewRequest(&userv1.UpdateRequest{})))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(direct))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(viaBoundary))
	})

	// @spec components/adapter/fan/api/rpc/user "Domestic-format phone number on update"
	t.Run("return INVALID_ARGUMENT for a domestic-format phone number", func(t *testing.T) {
		t.Parallel()
		h, _ := newUserHandler(t)
		req := &userv1.UpdateRequest{PhoneNumber: &entitypb.PhoneNumber{Value: "090-9876-5432"}}

		_, direct := h.Update(authedCtx(testCallerExtID), connect.NewRequest(req))
		_, viaBoundary := newUserBoundaryClient(t, h).Update(context.Background(), withFanToken(connect.NewRequest(req)))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(direct))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(viaBoundary))
	})

	t.Run("return INVALID_ARGUMENT for an empty full name", func(t *testing.T) {
		t.Parallel()
		h, _ := newUserHandler(t)

		_, err := h.Update(authedCtx(testCallerExtID), connect.NewRequest(&userv1.UpdateRequest{
			FullName: &entitypb.FullName{Value: ""},
		}))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("return the usecase failure unchanged", func(t *testing.T) {
		t.Parallel()
		h, userUC := newUserHandler(t)
		userUC.EXPECT().Update(mock.Anything, testCallerExtID, mock.Anything, mock.Anything).
			Return(nil, apperr.New(apperr.ErrNotFound.Code, "user not found")).Once()

		_, err := h.Update(authedCtx(testCallerExtID), connect.NewRequest(&userv1.UpdateRequest{
			FullName: &entitypb.FullName{Value: "山田 花子"},
		}))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestUserHandler_SetHome(t *testing.T) {
	t.Parallel()

	homeProto := &entitypb.Home{CountryCode: "JP", Level_1: "JP-13"}

	// @spec components/adapter/fan/api/rpc/user "Home set"
	t.Run("return the User with the new home", func(t *testing.T) {
		t.Parallel()
		h, userUC := newUserHandler(t)
		updated := callerUser()
		updated.Home = &entity.Home{CountryCode: "JP", Level1: "JP-13"}
		userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID).Return(callerUser(), nil).Once()
		userUC.EXPECT().UpdateHome(mock.Anything, testCallerUserID,
			mock.MatchedBy(func(home *entity.Home) bool { return home.CountryCode == "JP" && home.Level1 == "JP-13" })).
			Return(updated, nil).Once()

		resp, err := h.SetHome(authedCtx(testCallerExtID), connect.NewRequest(&userv1.SetHomeRequest{Home: homeProto}))

		require.NoError(t, err)
		assert.Equal(t, "JP", resp.Msg.GetUser().GetHome().GetCountryCode())
		assert.Equal(t, "JP-13", resp.Msg.GetUser().GetHome().GetLevel_1())
	})

	// @spec components/adapter/fan/api/rpc/user "Missing home"
	t.Run("return INVALID_ARGUMENT without a home", func(t *testing.T) {
		t.Parallel()
		// The mock fails the test if the usecase runs.
		h, _ := newUserHandler(t)

		_, err := newUserBoundaryClient(t, h).SetHome(context.Background(), withFanToken(connect.NewRequest(&userv1.SetHomeRequest{})))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	// @spec components/adapter/fan/api/rpc/user "Another user's account"
	t.Run("change only the caller's own home", func(t *testing.T) {
		t.Parallel()
		h, userUC := newUserHandler(t)
		userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID).Return(callerUser(), nil).Once()
		// UpdateHome runs for the resolved caller's id and no other.
		userUC.EXPECT().UpdateHome(mock.Anything, testCallerUserID, mock.Anything).Return(callerUser(), nil).Once()

		_, err := h.SetHome(authedCtx(testCallerExtID), connect.NewRequest(&userv1.SetHomeRequest{Home: homeProto}))

		require.NoError(t, err)
	})

	t.Run("return NOT_FOUND when the caller has no User", func(t *testing.T) {
		t.Parallel()
		h, userUC := newUserHandler(t)
		userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID).
			Return(nil, apperr.New(apperr.ErrNotFound.Code, "user not found")).Once()

		_, err := h.SetHome(authedCtx(testCallerExtID), connect.NewRequest(&userv1.SetHomeRequest{Home: homeProto}))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestUserHandler_SetPreferredLanguage(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/fan/api/rpc/user "Language changed"
	t.Run("return the User with the new language", func(t *testing.T) {
		t.Parallel()
		h, userUC := newUserHandler(t)
		updated := callerUser()
		updated.PreferredLanguage = "en"
		userUC.EXPECT().ResolveCaller(mock.Anything, testCallerExtID).Return(callerUser(), nil).Once()
		userUC.EXPECT().UpdatePreferredLanguage(mock.Anything, testCallerUserID, "en").Return(updated, nil).Once()

		resp, err := h.SetPreferredLanguage(authedCtx(testCallerExtID), connect.NewRequest(&userv1.SetPreferredLanguageRequest{
			PreferredLanguage: "en",
		}))

		require.NoError(t, err)
		assert.Equal(t, "en", resp.Msg.GetUser().GetPreferredLanguage())
	})

	// @spec components/adapter/fan/api/rpc/user "Malformed language from a caller without an account"
	t.Run("return INVALID_ARGUMENT before resolving the caller", func(t *testing.T) {
		t.Parallel()
		// The format check runs before ResolveCaller, so a caller without an
		// account sees InvalidArgument, not NotFound; the mock fails the test
		// if ResolveCaller runs.
		h, _ := newUserHandler(t)

		_, err := h.SetPreferredLanguage(authedCtx("ext-unknown"), connect.NewRequest(&userv1.SetPreferredLanguageRequest{
			PreferredLanguage: "ja-JP",
		}))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("return INVALID_ARGUMENT for every malformed language", func(t *testing.T) {
		t.Parallel()
		for _, bad := range []string{"", "e", "eng", "EN", "42", "ja-JP", "english", " ja"} {
			t.Run(bad, func(t *testing.T) {
				t.Parallel()
				h, _ := newUserHandler(t)

				_, err := h.SetPreferredLanguage(authedCtx(testCallerExtID), connect.NewRequest(&userv1.SetPreferredLanguageRequest{
					PreferredLanguage: bad,
				}))

				assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
			})
		}
	})
}

func TestUserHandler_ResendEmailVerification(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/fan/api/rpc/user "Caller resends their own email"
	t.Run("delegate to the usecase with the caller's identity", func(t *testing.T) {
		t.Parallel()
		h, userUC := newUserHandler(t)
		userUC.EXPECT().ResendEmailVerification(mock.Anything, testCallerExtID).Return(nil).Once()

		resp, err := h.ResendEmailVerification(authedCtx(testCallerExtID), connect.NewRequest(&userv1.ResendEmailVerificationRequest{}))

		require.NoError(t, err)
		assert.NotNil(t, resp)
	})

	// @spec components/adapter/fan/api/rpc/user "Usecase failure returned unchanged"
	t.Run("return the usecase failure unchanged", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name    string
			ucErr   error
			wantErr error
		}{
			{"ResourceExhausted on rate limit", apperr.New(apperr.ErrResourceExhausted.Code, "resend rate limit exceeded"), apperr.ErrResourceExhausted},
			{"FailedPrecondition when already verified", apperr.New(apperr.ErrFailedPrecondition.Code, "email is already verified"), apperr.ErrFailedPrecondition},
			{"Unavailable when verifier is not configured", apperr.New(apperr.ErrUnavailable.Code, "email verification service is not configured"), apperr.ErrUnavailable},
			{"NotFound when the caller has no User", apperr.New(apperr.ErrNotFound.Code, "user not found"), apperr.ErrNotFound},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				h, userUC := newUserHandler(t)
				userUC.EXPECT().ResendEmailVerification(mock.Anything, testCallerExtID).Return(tc.ucErr).Once()

				resp, err := h.ResendEmailVerification(authedCtx(testCallerExtID), connect.NewRequest(&userv1.ResendEmailVerificationRequest{}))

				assert.Nil(t, resp)
				assert.ErrorIs(t, err, tc.wantErr)
			})
		}
	})
}
