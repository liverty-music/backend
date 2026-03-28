package usecase_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
)

// followTestDeps holds all dependencies for FollowUseCase tests.
type followTestDeps struct {
	followRepo    *mocks.MockFollowRepository
	artistRepo    *mocks.MockArtistRepository
	siteResolver  *mocks.MockOfficialSiteResolver
	concertUC     *ucmocks.MockConcertUseCase
	searchLogRepo *mocks.MockSearchLogRepository
	uc            usecase.FollowUseCase
}

func newFollowTestDeps(t *testing.T) *followTestDeps {
	t.Helper()
	d := &followTestDeps{
		followRepo:    mocks.NewMockFollowRepository(t),
		artistRepo:    mocks.NewMockArtistRepository(t),
		siteResolver:  mocks.NewMockOfficialSiteResolver(t),
		concertUC:     ucmocks.NewMockConcertUseCase(t),
		searchLogRepo: mocks.NewMockSearchLogRepository(t),
	}
	d.uc = usecase.NewFollowUseCase(
		d.followRepo,
		d.artistRepo,
		d.siteResolver,
		d.concertUC,
		d.searchLogRepo,
		noopMetrics{},
		newTestLogger(t),
	)
	return d
}

func TestFollowUseCase_SetHype(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	type args struct {
		userID   string
		artistID string
		hype     entity.Hype
	}

	tests := []struct {
		name    string
		args    args
		setup   func(t *testing.T, d *followTestDeps)
		wantErr error
	}{
		{
			name: "success",
			args: args{
				userID:   "internal-uuid-1",
				artistID: "artist-1",
				hype:     entity.HypeAway,
			},
			setup: func(t *testing.T, d *followTestDeps) {
				t.Helper()
				d.followRepo.EXPECT().
					SetHype(ctx, "internal-uuid-1", "artist-1", entity.HypeAway).
					Return(nil).
					Once()
			},
			wantErr: nil,
		},
		{
			name: "return error when repository SetHype fails",
			args: args{
				userID:   "internal-uuid-1",
				artistID: "artist-1",
				hype:     entity.HypeAway,
			},
			setup: func(t *testing.T, d *followTestDeps) {
				t.Helper()
				d.followRepo.EXPECT().
					SetHype(ctx, "internal-uuid-1", "artist-1", entity.HypeAway).
					Return(apperr.ErrInternal).
					Once()
			},
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newFollowTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			err := d.uc.SetHype(ctx, tt.args.userID, tt.args.artistID, tt.args.hype)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}
