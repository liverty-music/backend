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

	t.Run("success with home", func(t *testing.T) {
		mockRepo := mocks.NewMockUserRepository(t)
		uc := usecase.NewUserUseCase(mockRepo, logger)

		params := &entity.NewUser{
			Name:  "John Doe",
			Email: "john@example.com",
			Home: &entity.Home{
				CountryCode: "JP",
				Level1:      "JP-13",
			},
		}

		expectedUser := &entity.User{
			ID:    "user-123",
			Name:  "John Doe",
			Email: "john@example.com",
			Home: &entity.Home{
				ID:          "home-1",
				CountryCode: "JP",
				Level1:      "JP-13",
			},
		}

		mockRepo.EXPECT().Create(ctx, params).Return(expectedUser, nil).Once()

		result, err := uc.Create(ctx, params)

		assert.NoError(t, err)
		assert.Equal(t, expectedUser, result)
	})

	t.Run("error - invalid home country_code", func(t *testing.T) {
		mockRepo := mocks.NewMockUserRepository(t)
		uc := usecase.NewUserUseCase(mockRepo, logger)

		params := &entity.NewUser{
			Name:  "John Doe",
			Email: "john@example.com",
			Home: &entity.Home{
				CountryCode: "jp",
				Level1:      "JP-13",
			},
		}

		result, err := uc.Create(ctx, params)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("error - home level_1 prefix mismatch", func(t *testing.T) {
		mockRepo := mocks.NewMockUserRepository(t)
		uc := usecase.NewUserUseCase(mockRepo, logger)

		params := &entity.NewUser{
			Name:  "John Doe",
			Email: "john@example.com",
			Home: &entity.Home{
				CountryCode: "JP",
				Level1:      "US-CA",
			},
		}

		result, err := uc.Create(ctx, params)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("error - repository returns nil user without error", func(t *testing.T) {
		mockRepo := mocks.NewMockUserRepository(t)
		uc := usecase.NewUserUseCase(mockRepo, logger)

		params := &entity.NewUser{
			Name:  "Jane Doe",
			Email: "jane@example.com",
		}

		mockRepo.EXPECT().Create(ctx, params).Return(nil, nil).Once()

		result, err := uc.Create(ctx, params)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrInternal)
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

func TestUserUseCase_UpdateHome(t *testing.T) {
	ctx := context.Background()
	logger, _ := logging.New()

	t.Run("success", func(t *testing.T) {
		mockRepo := mocks.NewMockUserRepository(t)
		uc := usecase.NewUserUseCase(mockRepo, logger)

		home := &entity.Home{
			CountryCode: "JP",
			Level1:      "JP-13",
		}

		expectedUser := &entity.User{
			ID:   "user-123",
			Name: "John Doe",
			Home: &entity.Home{
				ID:          "home-1",
				CountryCode: "JP",
				Level1:      "JP-13",
			},
		}

		mockRepo.EXPECT().UpdateHome(ctx, "user-123", home).Return(expectedUser, nil).Once()

		result, err := uc.UpdateHome(ctx, "user-123", home)

		assert.NoError(t, err)
		assert.Equal(t, expectedUser, result)
		assert.Equal(t, "JP-13", result.Home.Level1)
	})

	t.Run("error - empty ID", func(t *testing.T) {
		mockRepo := mocks.NewMockUserRepository(t)
		uc := usecase.NewUserUseCase(mockRepo, logger)

		home := &entity.Home{
			CountryCode: "JP",
			Level1:      "JP-13",
		}

		result, err := uc.UpdateHome(ctx, "", home)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("error - nil home", func(t *testing.T) {
		mockRepo := mocks.NewMockUserRepository(t)
		uc := usecase.NewUserUseCase(mockRepo, logger)

		result, err := uc.UpdateHome(ctx, "user-123", nil)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("error - invalid country_code", func(t *testing.T) {
		mockRepo := mocks.NewMockUserRepository(t)
		uc := usecase.NewUserUseCase(mockRepo, logger)

		tests := []struct {
			name string
			home *entity.Home
		}{
			{"lowercase", &entity.Home{CountryCode: "jp", Level1: "JP-13"}},
			{"too long", &entity.Home{CountryCode: "JPN", Level1: "JP-13"}},
			{"single char", &entity.Home{CountryCode: "J", Level1: "JP-13"}},
			{"empty", &entity.Home{CountryCode: "", Level1: "JP-13"}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result, err := uc.UpdateHome(ctx, "user-123", tt.home)

				assert.Error(t, err)
				assert.Nil(t, result)
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			})
		}
	})

	t.Run("error - invalid level_1", func(t *testing.T) {
		mockRepo := mocks.NewMockUserRepository(t)
		uc := usecase.NewUserUseCase(mockRepo, logger)

		tests := []struct {
			name string
			home *entity.Home
		}{
			{"empty", &entity.Home{CountryCode: "JP", Level1: ""}},
			{"no hyphen", &entity.Home{CountryCode: "JP", Level1: "JP13"}},
			{"lowercase country prefix", &entity.Home{CountryCode: "JP", Level1: "jp-13"}},
			{"free text", &entity.Home{CountryCode: "JP", Level1: "東京都"}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result, err := uc.UpdateHome(ctx, "user-123", tt.home)

				assert.Error(t, err)
				assert.Nil(t, result)
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			})
		}
	})

	t.Run("error - level_1 prefix mismatch", func(t *testing.T) {
		mockRepo := mocks.NewMockUserRepository(t)
		uc := usecase.NewUserUseCase(mockRepo, logger)

		home := &entity.Home{
			CountryCode: "JP",
			Level1:      "US-CA",
		}

		result, err := uc.UpdateHome(ctx, "user-123", home)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("error - invalid level_2 length", func(t *testing.T) {
		mockRepo := mocks.NewMockUserRepository(t)
		uc := usecase.NewUserUseCase(mockRepo, logger)

		emptyL2 := ""
		tooLongL2 := "123456789012345678901"

		tests := []struct {
			name string
			home *entity.Home
		}{
			{"empty level_2", &entity.Home{CountryCode: "JP", Level1: "JP-13", Level2: &emptyL2}},
			{"too long level_2", &entity.Home{CountryCode: "JP", Level1: "JP-13", Level2: &tooLongL2}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result, err := uc.UpdateHome(ctx, "user-123", tt.home)

				assert.Error(t, err)
				assert.Nil(t, result)
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			})
		}
	})

	t.Run("success - various valid codes", func(t *testing.T) {
		tests := []struct {
			countryCode string
			level1      string
		}{
			{"JP", "JP-13"},
			{"JP", "JP-01"},
			{"US", "US-CA"},
			{"GB", "GB-ENG"},
		}

		for _, tt := range tests {
			t.Run(tt.level1, func(t *testing.T) {
				mockRepo := mocks.NewMockUserRepository(t)
				uc := usecase.NewUserUseCase(mockRepo, logger)

				home := &entity.Home{
					CountryCode: tt.countryCode,
					Level1:      tt.level1,
				}

				expectedUser := &entity.User{
					ID: "user-123",
					Home: &entity.Home{
						ID:          "home-1",
						CountryCode: tt.countryCode,
						Level1:      tt.level1,
					},
				}
				mockRepo.EXPECT().UpdateHome(ctx, "user-123", home).Return(expectedUser, nil).Once()

				result, err := uc.UpdateHome(ctx, "user-123", home)

				assert.NoError(t, err)
				assert.Equal(t, tt.level1, result.Home.Level1)
			})
		}
	})

	t.Run("error - repository fails", func(t *testing.T) {
		mockRepo := mocks.NewMockUserRepository(t)
		uc := usecase.NewUserUseCase(mockRepo, logger)

		home := &entity.Home{
			CountryCode: "JP",
			Level1:      "JP-13",
		}

		mockRepo.EXPECT().UpdateHome(ctx, "user-123", home).Return(nil, apperr.New(codes.NotFound, "user not found")).Once()

		result, err := uc.UpdateHome(ctx, "user-123", home)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
