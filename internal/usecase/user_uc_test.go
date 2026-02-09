package usecase_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

func TestUserUseCase_CreateUser(t *testing.T) {
	ctx := context.Background()
	logger, _ := logging.New()

	t.Run("success", func(t *testing.T) {
		mockRepo := mocks.NewMockUserRepository(t)
		uc := usecase.NewUserUseCase(mockRepo, logger)

		params := &entity.NewUser{
			Name:  "John Doe",
			Email: "john@example.com",
		}

		expectedUser := &entity.User{
			ID:    "user-123",
			Name:  "John Doe",
			Email: "john@example.com",
		}

		mockRepo.EXPECT().Create(ctx, params).Return(expectedUser, nil).Once()

		result, err := uc.Create(ctx, params)

		assert.NoError(t, err)
		assert.Equal(t, expectedUser, result)
	})

	t.Run("error - repository fails", func(t *testing.T) {
		mockRepo := mocks.NewMockUserRepository(t)
		uc := usecase.NewUserUseCase(mockRepo, logger)

		params := &entity.NewUser{
			Name:  "Jane Doe",
			Email: "jane@example.com",
		}

		mockRepo.EXPECT().Create(ctx, params).Return(nil, apperr.New(codes.Internal, "failed to create user")).Once()

		result, err := uc.Create(ctx, params)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrInternal)
	})
}

func TestUserUseCase_GetUser(t *testing.T) {
	ctx := context.Background()
	logger, _ := logging.New()

	t.Run("success", func(t *testing.T) {
		mockRepo := mocks.NewMockUserRepository(t)
		uc := usecase.NewUserUseCase(mockRepo, logger)

		expectedUser := &entity.User{
			ID:    "user-123",
			Name:  "John Doe",
			Email: "john@example.com",
		}

		mockRepo.EXPECT().Get(ctx, "user-123").Return(expectedUser, nil).Once()

		result, err := uc.Get(ctx, "user-123")

		assert.NoError(t, err)
		assert.Equal(t, expectedUser, result)
	})

	t.Run("error - empty ID", func(t *testing.T) {
		mockRepo := mocks.NewMockUserRepository(t)
		uc := usecase.NewUserUseCase(mockRepo, logger)

		result, err := uc.Get(ctx, "")

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}
