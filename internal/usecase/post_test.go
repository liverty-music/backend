package usecase_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pannpers/go-backend-scaffold/internal/entity"
	"github.com/pannpers/go-backend-scaffold/internal/usecase"
	"github.com/pannpers/go-backend-scaffold/pkg/apperr"
	"github.com/pannpers/go-backend-scaffold/pkg/apperr/codes"
	"github.com/pannpers/go-backend-scaffold/pkg/logging"
)

func TestPostUseCase_CreatePost(t *testing.T) {
	type args struct {
		ctx    context.Context
		params *entity.NewPost
	}

	type dep struct {
		postRepo *entity.MockPostRepository
		logger   *logging.Logger
	}

	tests := []struct {
		name    string
		args    args
		dep     func() dep
		want    *entity.Post
		wantErr error
	}{
		{
			name: "return created post when valid input provided",
			args: args{
				ctx: context.Background(),
				params: &entity.NewPost{
					Title:  "Test Post",
					UserID: "user-123",
				},
			},
			dep: func() dep {
				mockRepo := entity.NewMockPostRepository(t)
				logger := logging.New()

				expectedPost := &entity.Post{
					ID:        "post-456",
					Title:     "Test Post",
					UserID:    "user-123",
					CreatedAt: fakeTime,
					UpdatedAt: fakeTime,
				}

				mockRepo.EXPECT().Create(context.Background(), &entity.NewPost{
					Title:  "Test Post",
					UserID: "user-123",
				}).Return(expectedPost, nil).Once()

				return dep{
					postRepo: mockRepo,
					logger:   logger,
				}
			},
			want: &entity.Post{
				ID:        "post-456",
				Title:     "Test Post",
				UserID:    "user-123",
				CreatedAt: fakeTime,
				UpdatedAt: fakeTime,
			},
			wantErr: nil,
		},
		{
			name: "return error when repository fails",
			args: args{
				ctx: context.Background(),
				params: &entity.NewPost{
					Title:  "Failed Post",
					UserID: "user-456",
				},
			},
			dep: func() dep {
				mockRepo := entity.NewMockPostRepository(t)
				logger := logging.New()

				mockRepo.EXPECT().Create(context.Background(), &entity.NewPost{
					Title:  "Failed Post",
					UserID: "user-456",
				}).Return(nil, apperr.New(codes.Internal, "failed to create post")).Once()

				return dep{
					postRepo: mockRepo,
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
			uc := usecase.NewPostUseCase(d.postRepo, d.logger)

			got, err := uc.CreatePost(tt.args.ctx, tt.args.params)

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

func TestPostUseCase_GetPost(t *testing.T) {
	type args struct {
		ctx context.Context
		id  string
	}

	type dep struct {
		postRepo *entity.MockPostRepository
		logger   *logging.Logger
	}

	tests := []struct {
		name    string
		args    args
		dep     func() dep
		want    *entity.Post
		wantErr error
	}{
		{
			name: "return post when valid ID provided",
			args: args{
				ctx: context.Background(),
				id:  "post-123",
			},
			dep: func() dep {
				mockRepo := entity.NewMockPostRepository(t)
				logger := logging.New()

				expectedPost := &entity.Post{
					ID:        "post-123",
					Title:     "Test Post",
					UserID:    "user-123",
					CreatedAt: fakeTime,
					UpdatedAt: fakeTime,
				}

				mockRepo.EXPECT().Get(context.Background(), "post-123").Return(expectedPost, nil).Once()

				return dep{
					postRepo: mockRepo,
					logger:   logger,
				}
			},
			want: &entity.Post{
				ID:        "post-123",
				Title:     "Test Post",
				UserID:    "user-123",
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
				mockRepo := entity.NewMockPostRepository(t)
				logger := logging.New()

				// No expectations on mockRepo since validation happens before repo call

				return dep{
					postRepo: mockRepo,
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
				id:  "post-123",
			},
			dep: func() dep {
				mockRepo := entity.NewMockPostRepository(t)
				logger := logging.New()

				mockRepo.EXPECT().Get(context.Background(), "post-123").Return(nil, apperr.New(codes.NotFound, "post not found")).Once()

				return dep{
					postRepo: mockRepo,
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
			uc := usecase.NewPostUseCase(d.postRepo, d.logger)

			got, err := uc.GetPost(tt.args.ctx, tt.args.id)

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

func TestPostUseCase_DeletePost(t *testing.T) {
	type args struct {
		ctx context.Context
		id  string
	}

	type dep struct {
		postRepo *entity.MockPostRepository
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
				id:  "post-123",
			},
			dep: func() dep {
				mockRepo := entity.NewMockPostRepository(t)
				logger := logging.New()

				mockRepo.EXPECT().Delete(context.Background(), "post-123").Return(nil).Once()

				return dep{
					postRepo: mockRepo,
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
				mockRepo := entity.NewMockPostRepository(t)
				logger := logging.New()

				// No expectations on mockRepo since validation happens before repo call

				return dep{
					postRepo: mockRepo,
					logger:   logger,
				}
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "return error when repository fails",
			args: args{
				ctx: context.Background(),
				id:  "post-123",
			},
			dep: func() dep {
				mockRepo := entity.NewMockPostRepository(t)
				logger := logging.New()

				mockRepo.EXPECT().Delete(context.Background(), "post-123").Return(apperr.New(codes.Internal, "failed to delete post")).Once()

				return dep{
					postRepo: mockRepo,
					logger:   logger,
				}
			},
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := tt.dep()
			uc := usecase.NewPostUseCase(d.postRepo, d.logger)

			err := uc.DeletePost(tt.args.ctx, tt.args.id)

			if tt.wantErr != nil {
				assert.Error(t, err)

				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNewPostUseCase(t *testing.T) {
	type args struct {
		postRepo entity.PostRepository
		logger   *logging.Logger
	}

	tests := []struct {
		name string
		args args
		want *usecase.PostUseCase
	}{
		{
			name: "return PostUseCase with provided dependencies",
			args: args{
				postRepo: entity.NewMockPostRepository(t),
				logger:   logging.New(),
			},
			want: &usecase.PostUseCase{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := usecase.NewPostUseCase(tt.args.postRepo, tt.args.logger)

			assert.NotNil(t, got)
		})
	}
}
