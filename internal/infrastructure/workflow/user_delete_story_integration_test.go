package workflow_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/jackc/pgx/v5"
	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/liverty-music/backend/internal/infrastructure/repository"
	"github.com/liverty-music/backend/internal/infrastructure/workflow"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// failingRemovals is the user store whose record removal fails with
// Unavailable while failing is set.
type failingRemovals struct {
	entity.UserRepository
	failing atomic.Bool
}

func (r *failingRemovals) Delete(ctx context.Context, id string) error {
	if r.failing.Load() {
		return apperr.New(codes.Unavailable, "the store is unreachable")
	}
	return r.UserRepository.Delete(ctx, id)
}

// deletionStory is a fan with a follow and an identity check, the API that
// accepts their deletion and the worker that removes the account, both on one
// database. Only the identity provider is a mock.
type deletionStory struct {
	dsn   string
	db    *rdb.Database
	users *failingRemovals
	fan   *entity.User
	api   usecase.UserUseCase
}

// newDeletionStory starts the story. apiClock is the API's clock, which sets
// the deadline of the removals it starts.
func newDeletionStory(t *testing.T, apiClock usecase.Clock) *deletionStory {
	t.Helper()
	ctx := context.Background()
	dsn := setupSystemDB(t)
	parsed, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	logger, _ := newLogger(t)
	db, err := rdb.New(ctx, config.DatabaseConfig{
		Host: parsed.Host, Port: int(parsed.Port), Name: parsed.Database, User: parsed.User,
		SSLMode: "disable", Schema: "app", MaxOpenConns: 4, MaxIdleConns: 1,
		ConnMaxLifetime: 1800, MaxConnIdleTime: 600, HealthCheckPeriod: 60,
	}, true, logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	outbox := messaging.NewOutbox(watermill.NopLogger{}, pb.NewAnnouncementEncoder())
	users := &failingRemovals{UserRepository: rdb.NewUserRepository(db, outbox)}

	fan, err := users.Create(ctx, storyFan("zitadel-fan-1"))
	require.NoError(t, err)
	artistID := entity.NewID()
	_, err = db.Pool.Exec(ctx, `INSERT INTO artists (id, name, mbid) VALUES ($1, 'Story Artist', $2)`, artistID, entity.NewID())
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `INSERT INTO followed_artists (user_id, artist_id) VALUES ($1, $2)`, fan.ID, artistID)
	require.NoError(t, err)
	_, err = rdb.NewIdentityVerificationRepository(db).Create(ctx,
		entity.NewIdentityVerification(fan.ID, "ps-"+fan.ID, entity.VerificationMethodJPKI, time.Now()))
	require.NoError(t, err)

	reads := usecase.DeletionBlockerReads{
		Tickets:        rdb.NewTicketRepository(db, outbox),
		Events:         rdb.NewEventRepository(db),
		ResaleListings: rdb.NewResaleListingRepository(db, outbox),
		LotteryEntries: rdb.NewLotteryEntryRepository(db, outbox),
		Checkouts:      rdb.NewCheckoutRepository(db, outbox),
		Orders:         repository.NewOrderRepository(rdb.NewOrderRepository(db), payment.NewNoopOrderPayments(logger)),
		Now:            time.Now,
	}
	identities := mocks.NewMockIdentityRemover(t)
	identities.EXPECT().DeleteIdentity(mock.Anything, "zitadel-fan-1").Return(nil).Once()

	worker := usecase.NewUserUseCase(users, nil, reads, nil, nil, nil, identities, usecase.PasskeyMailers{}, time.Now, logger)
	startWorker(t, dsn, func(rt *workflow.Runtime) {
		workflow.Register(rt, usecase.WorkflowDeleteUser, worker.RunDeleteUser)
	})
	api := usecase.NewUserUseCase(users, nil, reads, newClient(t, dsn), nil, nil, nil, usecase.PasskeyMailers{}, apiClock, logger)
	return &deletionStory{dsn: dsn, db: db, users: users, fan: fan, api: api}
}

// storyFan is the sign-up of the story's fan, always with the same email
// address, by the identity providerUserID.
func storyFan(providerUserID string) *entity.NewUser {
	return &entity.NewUser{
		ProviderUserID: providerUserID, Email: "story@example.com", Name: "Story",
		PreferredLanguage: "ja", TimeZone: "Asia/Tokyo",
	}
}

// count returns the rows query finds for the user.
func (s *deletionStory) count(t *testing.T, query, userID string) int {
	t.Helper()
	var n int
	require.NoError(t, s.db.Pool.QueryRow(context.Background(), query, userID).Scan(&n))
	return n
}

// @spec stories/delete-my-account "Failure halfway"
func TestDeleteMyAccountStory_FailureHalfway(t *testing.T) {
	ctx := context.Background()
	s := newDeletionStory(t, time.Now)
	s.users.failing.Store(true)

	require.NoError(t, s.api.Delete(ctx, s.fan.ID), "the deletion is accepted")
	_, err := s.users.Get(ctx, s.fan.ID)
	require.NoError(t, err, "the account removal is still failing")

	s.users.failing.Store(false)
	require.Eventually(t, func() bool {
		_, err := s.users.Get(ctx, s.fan.ID)
		return errors.Is(err, apperr.ErrNotFound)
	}, waitLimit, 100*time.Millisecond, "the account is removed without the fan acting again")

	again, err := s.users.Create(ctx, storyFan("zitadel-fan-2"))
	require.NoError(t, err, "signing up again with the same email address creates a new account")
	assert.NotEqual(t, s.fan.ID, again.ID)
	for _, query := range []string{
		`SELECT count(*) FROM followed_artists WHERE user_id = $1`,
		`SELECT count(*) FROM tickets WHERE user_id = $1`,
		`SELECT count(*) FROM identity_verifications WHERE user_id = $1`,
	} {
		assert.Zero(t, s.count(t, query, again.ID), query)
	}
}

// The 24 hours are scaled down: the API's clock is set so that the removal's
// deadline passes 8 seconds after it starts.
//
// @spec stories/delete-my-account "Deletion that cannot complete"
// @spec components/usecase/user/delete "Still failing after 24 hours"
func TestDeleteMyAccountStory_CannotComplete(t *testing.T) {
	ctx := context.Background()
	s := newDeletionStory(t, func() time.Time { return time.Now().Add(-24*time.Hour + 8*time.Second) })
	s.users.failing.Store(true)

	require.NoError(t, s.api.Delete(ctx, s.fan.ID))

	var runID string
	require.NoError(t, s.db.Pool.QueryRow(ctx,
		`SELECT workflow_uuid FROM dbos.workflow_status WHERE workflow_uuid LIKE 'delete-user:' || $1 || ':%'`, s.fan.ID).Scan(&runID))
	require.Eventually(t, func() bool { return needsAdmin(workflowStatus(t, s.dsn, runID)) },
		waitLimit, 200*time.Millisecond, "an admin is told")
	_, err := s.users.Get(ctx, s.fan.ID)
	assert.NoError(t, err, "the account is still there")
}
