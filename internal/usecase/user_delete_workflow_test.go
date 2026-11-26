package usecase_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// serialized expects times deletion steps, each run under the mocked lock.
func (d *userTestDeps) serialized(times int) {
	d.repo.EXPECT().SerializeDeletion(mock.Anything, deletionTestUser.ID, mock.Anything).
		RunAndReturn(func(ctx context.Context, _ string, fn func(context.Context) error) error { return fn(ctx) }).Times(times)
}

// found expects times reads of the stored User.
func (d *userTestDeps) found(times int) {
	d.repo.EXPECT().Get(mock.Anything, deletionTestUser.ID).Return(deletionTestUser, nil).Times(times)
}

// removalStarts expects one delete-user run to be started with a deadline 24
// hours ahead, and returns its input once Delete started it.
func (d *userTestDeps) removalStarts(t *testing.T) *usecase.UserRequest {
	t.Helper()
	in := &usecase.UserRequest{}
	d.workflows.EXPECT().Start(mock.Anything, usecase.WorkflowDeleteUser, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _, id string, input any, opts ...usecase.StartOption) error {
			assert.True(t, strings.HasPrefix(id, usecase.WorkflowDeleteUser+":"+deletionTestUser.ID+":"), "workflow id %s", id)
			assert.WithinDuration(t, time.Now().Add(24*time.Hour), usecase.NewStartOptions(opts...).Deadline, time.Minute)
			*in = input.(usecase.UserRequest)
			return nil
		}).Once()
	return in
}

// removalRuns makes one Delete's wait run the removal it started to its end
// with f, as the worker does within the wait, and answer its failure as Await
// does.
func (d *userTestDeps) removalRuns(t *testing.T, f usecase.Flow) {
	t.Helper()
	in := d.removalStarts(t)
	d.workflows.EXPECT().Await(mock.Anything, mock.Anything, 5*time.Second, mock.Anything).
		RunAndReturn(func(ctx context.Context, _ string, _ time.Duration, _ any) error {
			if _, err := d.uc.RunDeleteUser(ctx, f, *in); err != nil {
				return fmt.Errorf("%w: %w", usecase.ErrWorkflowFailed, err)
			}
			return nil
		}).Once()
}

// removes expects the identity and then the record to be removed once.
func (d *userTestDeps) removes() {
	identity := d.identityRemover.EXPECT().DeleteIdentity(mock.Anything, "zitadel-user-1").Return(nil).Once()
	d.repo.EXPECT().Delete(mock.Anything, deletionTestUser.ID).Return(nil).Once().NotBefore(identity)
}

func TestUserUseCase_Delete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	userID := deletionTestUser.ID

	removed := []struct {
		name  string
		reads deletionReads
	}{
		{
			// @spec components/usecase/user/delete "Existing user"
			name: "removes the identity and then the user",
		},
		{
			// @spec components/usecase/user/delete "User whose concerts are over"
			name: "removes a user whose only ticket is for a past event",
			reads: deletionReads{
				tickets: []*entity.Ticket{issuedTicketFor("t1", "e1")},
				events:  []*entity.Event{eventOn("e1", -1)},
			},
		},
		{
			// @spec components/usecase/user/delete "User with a resold listing awaiting its refund"
			// The listing is Sold and has no refund time: the user is removed.
			// That the refund runs without the user is covered where it runs:
			// ResaleListingUseCase.RefundSold reads no User (TestResaleListingUseCase_RefundSold),
			// and SendResaleSold skips the email of a deleted user (the "listing's user deleted" test).
			name: "removes a user whose only resale listing is sold and not refunded yet",
			reads: deletionReads{
				listings: []*entity.ResaleListing{{ID: "l1", EventID: "e1", Status: entity.ResaleListingStatusSold}},
			},
		},
		{
			// @spec components/usecase/user/delete "User with only refunded purchases"
			name: "removes a user whose only ticket is voided",
			reads: deletionReads{
				tickets: []*entity.Ticket{{ID: "t1", EventID: "e1", Status: entity.TicketStatusVoided}},
			},
		},
	}
	for _, tt := range removed {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newUserTestDeps(t)
			// Delete's check, then each of the two steps reads the User and
			// its blockers.
			d.found(5)
			tt.reads.expect(ctx, d, 3)
			d.serialized(2)
			d.removalRuns(t, &inlineFlow{})
			d.removes()

			assert.NoError(t, d.uc.Delete(ctx, userID))
		})
	}

	refused := []struct {
		name  string
		reads deletionReads
	}{
		{
			// @spec components/usecase/user/delete "User holding a ticket"
			name: "refuses a user holding an issued ticket for an upcoming event",
			reads: deletionReads{
				tickets: []*entity.Ticket{issuedTicketFor("t1", "e1")},
				events:  []*entity.Event{eventOn("e1", 0)},
			},
		},
		{
			// @spec components/usecase/user/delete "User with a listed resale"
			name: "refuses a user with a listed resale",
			reads: deletionReads{
				events:   []*entity.Event{eventStartingIn("e1", 7*24*time.Hour)},
				listings: []*entity.ResaleListing{listedFor("l1", "e1")},
			},
		},
		{
			// @spec components/usecase/user/delete "User with an entry awaiting its draw"
			name:  "refuses a user with an entered lottery entry",
			reads: deletionReads{entries: []*entity.LotteryEntry{{ID: "le1", State: entity.LotteryEntryStateEntered}}},
		},
		{
			// @spec components/usecase/user/delete "User paying for tickets"
			name:  "refuses a user with a committed checkout",
			reads: deletionReads{checkouts: []*entity.Checkout{{ID: "c1", Status: entity.CheckoutStatusCommitted}}},
		},
		{
			// @spec components/usecase/user/delete "User whose win is being charged"
			name:  "refuses a user with a won entry that has no order yet",
			reads: deletionReads{entries: []*entity.LotteryEntry{{ID: "le1", State: entity.LotteryEntryStateWon}}},
		},
	}
	for _, tt := range refused {
		t.Run(tt.name+" and starts no removal", func(t *testing.T) {
			t.Parallel()
			d := newUserTestDeps(t) // no workflow, lock or removal expectation
			d.found(1)
			tt.reads.expect(ctx, d, 1)

			assert.ErrorIs(t, d.uc.Delete(ctx, userID), apperr.ErrFailedPrecondition)
		})
	}

	// @spec components/usecase/user/delete "Unknown user"
	t.Run("returns NotFound for an unknown user", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		d.repo.EXPECT().Get(ctx, userID).Return(nil, apperr.New(codes.NotFound, "user not found")).Once()

		assert.ErrorIs(t, d.uc.Delete(ctx, userID), apperr.ErrNotFound)
	})

	// @spec components/usecase/user/delete "Retry after the record failed"
	t.Run("a second call removes the user after a removal ended with the record failing", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		d.found(10)
		deletionReads{}.expect(ctx, d, 6)
		d.serialized(4)
		// The identity is already gone on the retry, which DeleteIdentity
		// reports as success.
		d.identityRemover.EXPECT().DeleteIdentity(mock.Anything, "zitadel-user-1").Return(nil).Twice()
		d.repo.EXPECT().Delete(mock.Anything, userID).Return(apperr.New(codes.Internal, "delete failed")).Once()
		d.repo.EXPECT().Delete(mock.Anything, userID).Return(nil).Once()

		d.removalRuns(t, &inlineFlow{})
		assert.ErrorIs(t, d.uc.Delete(ctx, userID), apperr.ErrInternal)
		d.removalRuns(t, &inlineFlow{})
		assert.NoError(t, d.uc.Delete(ctx, userID))
	})
}

// A removal that ends with an error is reported to an admin by the workflow
// runtime (TestClient_AwaitKeepsTheCodeOfAFailedRun), so these tests check
// how the run ends.
func TestUserUseCase_RunDeleteUser(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	userID := deletionTestUser.ID
	request := usecase.UserRequest{UserID: userID}
	unavailable := apperr.New(codes.Unavailable, "database down")

	// @spec components/usecase/user/delete "Record removal fails once"
	t.Run("tries the record removal again and succeeds without another call", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		d.found(7)
		deletionReads{}.expect(ctx, d, 4)
		d.serialized(3)
		d.identityRemover.EXPECT().DeleteIdentity(mock.Anything, "zitadel-user-1").Return(nil).Once()
		d.repo.EXPECT().Delete(mock.Anything, userID).Return(unavailable).Once()
		d.repo.EXPECT().Delete(mock.Anything, userID).Return(nil).Once()
		d.removalRuns(t, &retryingFlow{})

		assert.NoError(t, d.uc.Delete(ctx, userID))
	})

	// @spec components/usecase/user/delete "Identity provider down for a minute"
	t.Run("Delete succeeds when the wait ends first, and the run removes the user later", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		// Delete's check, 3 tries of the identity step and the record step.
		d.found(9)
		deletionReads{}.expect(ctx, d, 5)
		d.serialized(4)
		d.identityRemover.EXPECT().DeleteIdentity(mock.Anything, "zitadel-user-1").
			Return(apperr.New(codes.Unavailable, "identity provider down")).Twice()
		d.removes()
		in := d.removalStarts(t)
		d.workflows.EXPECT().Await(mock.Anything, mock.Anything, 5*time.Second, mock.Anything).
			Return(apperr.New(codes.DeadlineExceeded, "still running")).Once()

		require.NoError(t, d.uc.Delete(ctx, userID))
		_, err := d.uc.RunDeleteUser(ctx, &retryingFlow{}, *in)

		assert.NoError(t, err)
	})

	// @spec components/usecase/user/delete "Restart in the middle"
	t.Run("a resumed run removes the user without removing the identity again", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		d.found(4)
		deletionReads{}.expect(ctx, d, 2)
		d.serialized(2)
		d.removes()
		f := newReplayFlow()
		f.stopAt = "remove-user"

		_, err := d.uc.RunDeleteUser(ctx, f, request)
		require.ErrorIs(t, err, errStopped)
		f.stopAt = ""
		_, err = d.uc.RunDeleteUser(ctx, f, request)

		assert.NoError(t, err)
	})

	// @spec components/usecase/user/delete "Failure that a retry cannot fix"
	t.Run("Delete fails with the code the run ended with", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		d.found(5)
		deletionReads{}.expect(ctx, d, 3)
		d.serialized(2)
		d.identityRemover.EXPECT().DeleteIdentity(mock.Anything, "zitadel-user-1").Return(nil).Once()
		d.repo.EXPECT().Delete(mock.Anything, userID).Return(apperr.New(codes.Internal, "delete failed")).Once()
		f := &retryingFlow{}
		d.removalRuns(t, f)

		err := d.uc.Delete(ctx, userID)

		assert.ErrorIs(t, err, apperr.ErrInternal)
		assert.ErrorIs(t, err, usecase.ErrWorkflowFailed)
		assert.Equal(t, 2, f.tries, "one try of each step")
	})

	t.Run("a run that failed with DeadlineExceeded is not mistaken for the wait ending", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		d.found(1)
		deletionReads{}.expect(ctx, d, 1)
		d.removalStarts(t)
		d.workflows.EXPECT().Await(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(fmt.Errorf("%w: %w", usecase.ErrWorkflowFailed, apperr.New(codes.DeadlineExceeded, "step timed out"))).Once()

		assert.ErrorIs(t, d.uc.Delete(ctx, userID), apperr.ErrDeadlineExceeded)
	})

	// @spec components/usecase/user/delete "Blocker appears before the removal"
	t.Run("a blocker the first step finds keeps the user and its identity", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		d.found(3)
		uid := entity.UserID(userID)
		d.ticketRepo.EXPECT().ListByUser(mock.Anything, uid).Return(nil, nil).Twice()
		d.listingRepo.EXPECT().ListByUser(mock.Anything, uid).Return(nil, nil).Twice()
		d.entryRepo.EXPECT().ListByUser(mock.Anything, uid).Return(nil, nil).Twice()
		d.checkoutRepo.EXPECT().ListByUser(mock.Anything, uid).Return(nil, nil).Once()
		d.checkoutRepo.EXPECT().ListByUser(mock.Anything, uid).
			Return([]*entity.Checkout{{ID: "c1", Status: entity.CheckoutStatusCommitted}}, nil).Once()
		d.serialized(1)
		d.removalRuns(t, &retryingFlow{})

		err := d.uc.Delete(ctx, userID)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.ErrorIs(t, err, usecase.ErrWorkflowFailed)
	})

	// @spec components/usecase/user/delete "Blocker appears after the identity is removed"
	t.Run("a blocker found before the next try of the record removal ends the run", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		d.found(6)
		uid := entity.UserID(userID)
		d.ticketRepo.EXPECT().ListByUser(mock.Anything, uid).Return(nil, nil).Times(3)
		d.listingRepo.EXPECT().ListByUser(mock.Anything, uid).Return(nil, nil).Times(3)
		d.entryRepo.EXPECT().ListByUser(mock.Anything, uid).Return(nil, nil).Times(3)
		d.checkoutRepo.EXPECT().ListByUser(mock.Anything, uid).Return(nil, nil).Twice()
		d.checkoutRepo.EXPECT().ListByUser(mock.Anything, uid).
			Return([]*entity.Checkout{{ID: "c1", Status: entity.CheckoutStatusCommitted}}, nil).Once()
		d.serialized(3)
		d.identityRemover.EXPECT().DeleteIdentity(mock.Anything, "zitadel-user-1").Return(nil).Once()
		d.repo.EXPECT().Delete(mock.Anything, userID).Return(unavailable).Once()

		_, err := d.uc.RunDeleteUser(ctx, &retryingFlow{}, request)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	// @spec components/usecase/user/delete "Two removals of one User"
	t.Run("a second removal finds the user gone and ends without an error", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		d.found(4)
		d.repo.EXPECT().Get(mock.Anything, userID).Return(nil, apperr.New(codes.NotFound, "user not found")).Twice()
		deletionReads{}.expect(ctx, d, 2)
		d.serialized(4)
		d.removes()

		_, first := d.uc.RunDeleteUser(ctx, &inlineFlow{}, request)
		_, second := d.uc.RunDeleteUser(ctx, &inlineFlow{}, request)

		assert.NoError(t, first)
		assert.NoError(t, second)
	})
}
