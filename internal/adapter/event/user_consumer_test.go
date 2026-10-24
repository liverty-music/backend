package event_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeUserCreatedMsg(t *testing.T, data entity.UserCreatedData) *message.Message {
	t.Helper()
	payload, err := json.Marshal(data)
	require.NoError(t, err)
	return message.NewMessage("test-id", payload)
}

func TestUserConsumer_Handle(t *testing.T) {
	t.Parallel()

	newUser := &entity.User{ID: "019a0000-0000-7000-8000-000000000001", ProviderUserID: "zitadel-user-001", PreferredLanguage: "en"}

	t.Run("sends the new User the verification email", func(t *testing.T) {
		t.Parallel()

		users := ucmocks.NewMockUserUseCase(t)
		users.EXPECT().GetByProviderUserID(anyCtx, "zitadel-user-001").Return(newUser, nil).Once()
		emailVerifier := entitymocks.NewMockEmailVerifier(t)
		emailVerifier.EXPECT().SendVerification(anyCtx, newUser).Return(nil).Once()
		handler := event.NewUserConsumer(users, emailVerifier, newTestLogger(t))

		err := handler.Handle(makeUserCreatedMsg(t, entity.UserCreatedData{
			UserID:         newUser.ID,
			ProviderUserID: "zitadel-user-001",
			Email:          "user@example.com",
		}))

		assert.NoError(t, err)
	})

	t.Run("returns error when the email cannot be sent", func(t *testing.T) {
		t.Parallel()

		users := ucmocks.NewMockUserUseCase(t)
		users.EXPECT().GetByProviderUserID(anyCtx, "zitadel-user-001").Return(newUser, nil).Once()
		emailVerifier := entitymocks.NewMockEmailVerifier(t)
		emailVerifier.EXPECT().SendVerification(anyCtx, newUser).Return(fmt.Errorf("postmark unavailable")).Once()
		handler := event.NewUserConsumer(users, emailVerifier, newTestLogger(t))

		err := handler.Handle(makeUserCreatedMsg(t, entity.UserCreatedData{ProviderUserID: "zitadel-user-001"}))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "send email verification")
	})

	t.Run("finishes when the address is already verified, so a redelivery does not retry forever", func(t *testing.T) {
		t.Parallel()

		users := ucmocks.NewMockUserUseCase(t)
		users.EXPECT().GetByProviderUserID(anyCtx, "zitadel-user-001").Return(newUser, nil).Once()
		emailVerifier := entitymocks.NewMockEmailVerifier(t)
		emailVerifier.EXPECT().SendVerification(anyCtx, newUser).
			Return(apperr.New(codes.FailedPrecondition, "email is already verified")).
			Once()
		handler := event.NewUserConsumer(users, emailVerifier, newTestLogger(t))

		err := handler.Handle(makeUserCreatedMsg(t, entity.UserCreatedData{ProviderUserID: "zitadel-user-001"}))

		assert.NoError(t, err)
	})

	t.Run("finishes without sending when the User was deleted since sign-up", func(t *testing.T) {
		t.Parallel()

		users := ucmocks.NewMockUserUseCase(t)
		users.EXPECT().GetByProviderUserID(anyCtx, "zitadel-user-002").Return(nil, apperr.New(codes.NotFound, "gone")).Once()
		emailVerifier := entitymocks.NewMockEmailVerifier(t) // no expectation: a send fails the test
		handler := event.NewUserConsumer(users, emailVerifier, newTestLogger(t))

		err := handler.Handle(makeUserCreatedMsg(t, entity.UserCreatedData{ProviderUserID: "zitadel-user-002"}))

		assert.NoError(t, err)
	})

	t.Run("returns error when the User cannot be read", func(t *testing.T) {
		t.Parallel()

		users := ucmocks.NewMockUserUseCase(t)
		users.EXPECT().GetByProviderUserID(anyCtx, "zitadel-user-003").Return(nil, apperr.New(codes.Unavailable, "db down")).Once()
		handler := event.NewUserConsumer(users, entitymocks.NewMockEmailVerifier(t), newTestLogger(t))

		err := handler.Handle(makeUserCreatedMsg(t, entity.UserCreatedData{ProviderUserID: "zitadel-user-003"}))

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	t.Run("skips verification when email verifier is nil", func(t *testing.T) {
		t.Parallel()

		// When the identity service is not configured (local dev), emailVerifier is nil.
		handler := event.NewUserConsumer(ucmocks.NewMockUserUseCase(t), nil, newTestLogger(t))

		err := handler.Handle(makeUserCreatedMsg(t, entity.UserCreatedData{ProviderUserID: "zitadel-user-004"}))

		assert.NoError(t, err)
	})

	t.Run("returns error on malformed JSON payload", func(t *testing.T) {
		t.Parallel()

		handler := event.NewUserConsumer(ucmocks.NewMockUserUseCase(t), entitymocks.NewMockEmailVerifier(t), newTestLogger(t))

		err := handler.Handle(message.NewMessage("bad-id", []byte("{not valid json")))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse user.created event")
	})
}
