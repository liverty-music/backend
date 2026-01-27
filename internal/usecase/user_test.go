package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

var fakeTime = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

func TestUserUseCase_CreateUser(t *testing.T) {
	type args struct {
		ctx    context.Context
		params *entity.NewUser
	}

	type dep struct {
		userRepo *entity.MockUserRepository
		logger   *logging.Logger
	}

	tests := []struct {
		name    string
		args    args
		dep     func() dep
		want    *entity.User
		wantErr error
	}{
		{
			name: "return created user when valid input provided",
			args: args{
				ctx: context.Background(),
				params: &entity.NewUser{
					Name:  "John Doe",
					Email: "john@example.com",
				},
			},
			dep: func() dep {
				mockRepo := entity.NewMockUserRepository(t)
				logger, _ := logging.New()

				expectedUser := &entity.User{
					ID:        "user-123",
					Name:      "John Doe",
					Email:     "john@example.com",
					CreatedAt: fakeTime,
					UpdatedAt: fakeTime,
				}

				mockRepo.EXPECT().Create(context.Background(), &entity.NewUser{
					Name:  "John Doe",
					Email: "john@example.com",
				}).Return(expectedUser, nil).Once()

				return dep{
					userRepo: mockRepo,
					logger:   logger,
				}
			},
			want: &entity.User{
				ID:        "user-123",
				Name:      "John Doe",
				Email:     "john@example.com",
				CreatedAt: fakeTime,
				UpdatedAt: fakeTime,
			},
			wantErr: nil,
		},
		{
			name: "return error when repository fails",
			args: args{
				ctx: context.Background(),
				params: &entity.NewUser{
					Name:  "Jane Doe",
					Email: "jane@example.com",
				},
			},
			dep: func() dep {
				mockRepo := entity.NewMockUserRepository(t)
				logger, _ := logging.New()

				mockRepo.EXPECT().Create(context.Background(), &entity.NewUser{
					Name:  "Jane Doe",
					Email: "jane@example.com",
				}).Return(nil, apperr.New(codes.Internal, "failed to create user")).Once()

				return dep{
					userRepo: mockRepo,
					logger:   logger,
				}
			},
			want:    nil,
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := tt.dep()
			uc := usecase.NewUserUseCase(d.userRepo, d.logger)

			got, err := uc.Create(tt.args.ctx, tt.args.params)

			if tt.wantErr != nil {
				assert.Error(t, err)
				assert.Nil(t, got)

				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestUserUseCase_GetUser(t *testing.T) {
	type args struct {
		ctx context.Context
		id  string
	}

	type dep struct {
		userRepo *entity.MockUserRepository
		logger   *logging.Logger
	}

	tests := []struct {
		name    string
		args    args
		dep     func() dep
		want    *entity.User
		wantErr error
	}{
		{
			name: "return user when valid ID provided",
			args: args{
				ctx: context.Background(),
				id:  "user-123",
			},
			dep: func() dep {
				mockRepo := entity.NewMockUserRepository(t)
				logger, _ := logging.New()

				expectedUser := &entity.User{
					ID:        "user-123",
					Name:      "John Doe",
					Email:     "john@example.com",
					CreatedAt: fakeTime,
					UpdatedAt: fakeTime,
				}

				mockRepo.EXPECT().Get(context.Background(), "user-123").Return(expectedUser, nil).Once()

				return dep{
					userRepo: mockRepo,
					logger:   logger,
				}
			},
			want: &entity.User{
				ID:        "user-123",
				Name:      "John Doe",
				Email:     "john@example.com",
				CreatedAt: fakeTime,
				UpdatedAt: fakeTime,
			},
			wantErr: nil,
		},
		{
			name: "return error when empty ID provided",
			args: args{
				ctx: context.Background(),
				id:  "",
			},
			dep: func() dep {
				mockRepo := entity.NewMockUserRepository(t)
				logger, _ := logging.New()

				// No expectations on mockRepo since validation happens before repo call

				return dep{
					userRepo: mockRepo,
					logger:   logger,
				}
			},
			want:    nil,
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "return error when repository fails",
			args: args{
				ctx: context.Background(),
				id:  "user-123",
			},
			dep: func() dep {
				mockRepo := entity.NewMockUserRepository(t)
				logger, _ := logging.New()

				mockRepo.EXPECT().Get(context.Background(), "user-123").Return(nil, apperr.New(codes.NotFound, "user not found")).Once()

				return dep{
					userRepo: mockRepo,
					logger:   logger,
				}
			},
			want:    nil,
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := tt.dep()
			uc := usecase.NewUserUseCase(d.userRepo, d.logger)

			got, err := uc.Get(tt.args.ctx, tt.args.id)

			if tt.wantErr != nil {
				assert.Error(t, err)
				assert.Nil(t, got)

				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestUserUseCase_DeleteUser(t *testing.T) {
	type args struct {
		ctx context.Context
		id  string
	}

	type dep struct {
		userRepo *entity.MockUserRepository
		logger   *logging.Logger
	}

	tests := []struct {
		name    string
		args    args
		dep     func() dep
		wantErr error
	}{
		{
			name: "return nil when valid ID provided",
			args: args{
				ctx: context.Background(),
				id:  "user-123",
			},
			dep: func() dep {
				mockRepo := entity.NewMockUserRepository(t)
				logger, _ := logging.New()

				mockRepo.EXPECT().Delete(context.Background(), "user-123").Return(nil).Once()

				return dep{
					userRepo: mockRepo,
					logger:   logger,
				}
			},
			wantErr: nil,
		},
		{
			name: "return error when empty ID provided",
			args: args{
				ctx: context.Background(),
				id:  "",
			},
			dep: func() dep {
				mockRepo := entity.NewMockUserRepository(t)
				logger, _ := logging.New()

				// No expectations on mockRepo since validation happens before repo call

				return dep{
					userRepo: mockRepo,
					logger:   logger,
				}
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "return error when repository fails",
			args: args{
				ctx: context.Background(),
				id:  "user-123",
			},
			dep: func() dep {
				mockRepo := entity.NewMockUserRepository(t)
				logger, _ := logging.New()

				mockRepo.EXPECT().Delete(context.Background(), "user-123").Return(apperr.New(codes.Internal, "failed to delete user")).Once()

				return dep{
					userRepo: mockRepo,
					logger:   logger,
				}
			},
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := tt.dep()
			uc := usecase.NewUserUseCase(d.userRepo, d.logger)

			err := uc.Delete(tt.args.ctx, tt.args.id)

			if tt.wantErr != nil {
				assert.Error(t, err)

				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNewUserUseCase(t *testing.T) {
	type args struct {
		userRepo entity.UserRepository
		logger   *logging.Logger
	}

	logger, _ := logging.New()

	tests := []struct {
		name string
		args args
		want *usecase.UserUseCase
	}{
		{
			name: "return UserUseCase with provided dependencies",
			args: args{
				userRepo: entity.NewMockUserRepository(t),
				logger:   logger,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := usecase.NewUserUseCase(tt.args.userRepo, tt.args.logger)

			assert.NotNil(t, got)
		})
	}
}
