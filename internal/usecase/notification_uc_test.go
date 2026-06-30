package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
)

func buildNotificationUC(
	t *testing.T,
	notifRepo *entitymocks.MockNotificationRepository,
	pushSubRepo *entitymocks.MockPushSubscriptionRepository,
	sender *entitymocks.MockPushNotificationSender,
) usecase.NotificationUseCase {
	t.Helper()
	return usecase.NewNotificationUseCase(
		notifRepo,
		pushSubRepo,
		sender,
		noopMetrics{},
		newTestLogger(t),
	)
}

func notifPayload() *entity.NotificationPayload {
	return &entity.NotificationPayload{Title: "Artist", Body: "1 new concert found", URL: "/concerts", Tag: "concert-x"}
}

func sub(userID, endpoint string) *entity.PushSubscription {
	return &entity.PushSubscription{UserID: userID, Endpoint: endpoint, P256dh: "p", Auth: "a"}
}

// Success path: record created, sent, delivery recorded as delivered, and the
// minted notification id is carried into the dispatched payload.
func TestNotify_Success(t *testing.T) {
	t.Parallel()

	notifRepo := entitymocks.NewMockNotificationRepository(t)
	pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
	sender := entitymocks.NewMockPushNotificationSender(t)

	notifRepo.EXPECT().
		Create(anyCtx, mock.AnythingOfType("*entity.Notification")).
		Run(func(_ context.Context, n *entity.Notification) { n.ID = "01890000-0000-7000-8000-000000000abc" }).
		Return(nil)
	pushSubRepo.EXPECT().
		ListByUserIDs(anyCtx, []string{"user-1"}).
		Return([]*entity.PushSubscription{sub("user-1", "https://push/1")}, nil)
	sender.EXPECT().
		Send(anyCtx, mock.AnythingOfType("[]uint8"), mock.AnythingOfType("*entity.PushSubscription")).
		Return(nil)
	notifRepo.EXPECT().
		UpdateDelivery(anyCtx, "01890000-0000-7000-8000-000000000abc", entity.NotificationDeliveryStatusDelivered, mock.AnythingOfType("*time.Time"), "").
		Return(nil)

	uc := buildNotificationUC(t, notifRepo, pushSubRepo, sender)
	payload := notifPayload()
	n, err := uc.Notify(context.Background(), "user-1", entity.NotificationTypeNewConcerts, payload)

	require.NoError(t, err)
	assert.Equal(t, entity.NotificationDeliveryStatusDelivered, n.DeliveryStatus)
	// The notification id is propagated end-to-end into the payload data.
	assert.Equal(t, "01890000-0000-7000-8000-000000000abc", payload.Data[entity.NotificationDataKeyNotificationID])
}

// Send-failure path: the record is created, the send fails, and the outcome is
// recorded as failed (not returned as an error).
func TestNotify_SendFailureRecordedAsFailed(t *testing.T) {
	t.Parallel()

	notifRepo := entitymocks.NewMockNotificationRepository(t)
	pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
	sender := entitymocks.NewMockPushNotificationSender(t)

	notifRepo.EXPECT().
		Create(anyCtx, mock.AnythingOfType("*entity.Notification")).
		Run(func(_ context.Context, n *entity.Notification) { n.ID = "id-fail" }).
		Return(nil)
	pushSubRepo.EXPECT().
		ListByUserIDs(anyCtx, []string{"user-1"}).
		Return([]*entity.PushSubscription{sub("user-1", "https://push/1")}, nil)
	sender.EXPECT().
		Send(anyCtx, mock.Anything, mock.Anything).
		Return(errors.New("push service rejected"))
	notifRepo.EXPECT().
		UpdateDelivery(anyCtx, "id-fail", entity.NotificationDeliveryStatusFailed, (*time.Time)(nil), mock.MatchedBy(func(s string) bool { return s != "" })).
		Return(nil)

	uc := buildNotificationUC(t, notifRepo, pushSubRepo, sender)
	n, err := uc.Notify(context.Background(), "user-1", entity.NotificationTypeNewConcerts, notifPayload())

	require.NoError(t, err)
	assert.Equal(t, entity.NotificationDeliveryStatusFailed, n.DeliveryStatus)
}

// Gone (410) path: the dead subscription is cleaned up and, with no successful
// send, the outcome is failed.
func TestNotify_GoneSubscriptionCleanedUpAndFailed(t *testing.T) {
	t.Parallel()

	notifRepo := entitymocks.NewMockNotificationRepository(t)
	pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
	sender := entitymocks.NewMockPushNotificationSender(t)

	notifRepo.EXPECT().
		Create(anyCtx, mock.Anything).
		Run(func(_ context.Context, n *entity.Notification) { n.ID = "id-gone" }).
		Return(nil)
	pushSubRepo.EXPECT().
		ListByUserIDs(anyCtx, []string{"user-1"}).
		Return([]*entity.PushSubscription{sub("user-1", "https://push/gone")}, nil)
	sender.EXPECT().
		Send(anyCtx, mock.Anything, mock.Anything).
		Return(apperr.New(codes.NotFound, "410 gone"))
	pushSubRepo.EXPECT().
		Delete(anyCtx, "user-1", "https://push/gone").
		Return(nil)
	notifRepo.EXPECT().
		UpdateDelivery(anyCtx, "id-gone", entity.NotificationDeliveryStatusFailed, (*time.Time)(nil), mock.Anything).
		Return(nil)

	uc := buildNotificationUC(t, notifRepo, pushSubRepo, sender)
	_, err := uc.Notify(context.Background(), "user-1", entity.NotificationTypeNewConcerts, notifPayload())
	require.NoError(t, err)
}

// No-subscription path: a record is created but there is no push endpoint, so the
// outcome is failed with the "no active push subscription" reason; no send.
func TestNotify_NoSubscriptionRecordedAsFailed(t *testing.T) {
	t.Parallel()

	notifRepo := entitymocks.NewMockNotificationRepository(t)
	pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
	sender := entitymocks.NewMockPushNotificationSender(t)

	notifRepo.EXPECT().
		Create(anyCtx, mock.Anything).
		Run(func(_ context.Context, n *entity.Notification) { n.ID = "id-nosub" }).
		Return(nil)
	pushSubRepo.EXPECT().
		ListByUserIDs(anyCtx, []string{"user-1"}).
		Return([]*entity.PushSubscription{}, nil)
	notifRepo.EXPECT().
		UpdateDelivery(anyCtx, "id-nosub", entity.NotificationDeliveryStatusFailed, (*time.Time)(nil), "no active push subscription").
		Return(nil)

	uc := buildNotificationUC(t, notifRepo, pushSubRepo, sender)
	_, err := uc.Notify(context.Background(), "user-1", entity.NotificationTypeNewConcerts, notifPayload())
	require.NoError(t, err)
	sender.AssertNotCalled(t, "Send")
}

// Cancellation path: a context cancelled before dispatch short-circuits the send
// loop — the record still exists (created first) and is recorded failed, but no
// push is sent.
func TestNotify_ContextCancelledStopsDispatch(t *testing.T) {
	t.Parallel()

	notifRepo := entitymocks.NewMockNotificationRepository(t)
	pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
	sender := entitymocks.NewMockPushNotificationSender(t)

	notifRepo.EXPECT().
		Create(anyCtx, mock.Anything).
		Run(func(_ context.Context, n *entity.Notification) { n.ID = "id-cancel" }).
		Return(nil)
	pushSubRepo.EXPECT().
		ListByUserIDs(anyCtx, []string{"user-1"}).
		Return([]*entity.PushSubscription{sub("user-1", "https://push/1")}, nil)
	notifRepo.EXPECT().
		UpdateDelivery(anyCtx, "id-cancel", entity.NotificationDeliveryStatusFailed, (*time.Time)(nil), mock.MatchedBy(func(s string) bool { return s != "" })).
		Return(nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	uc := buildNotificationUC(t, notifRepo, pushSubRepo, sender)
	n, err := uc.Notify(ctx, "user-1", entity.NotificationTypeNewConcerts, notifPayload())

	require.NoError(t, err)
	assert.Equal(t, entity.NotificationDeliveryStatusFailed, n.DeliveryStatus)
	sender.AssertNotCalled(t, "Send")
}

// Record-failure path: when the record cannot be created, NO send is attempted
// and the error surfaces ("no record => no send").
func TestNotify_RecordFailureDoesNotSend(t *testing.T) {
	t.Parallel()

	notifRepo := entitymocks.NewMockNotificationRepository(t)
	pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
	sender := entitymocks.NewMockPushNotificationSender(t)

	notifRepo.EXPECT().
		Create(anyCtx, mock.Anything).
		Return(apperr.New(codes.Internal, "db down"))

	uc := buildNotificationUC(t, notifRepo, pushSubRepo, sender)
	_, err := uc.Notify(context.Background(), "user-1", entity.NotificationTypeNewConcerts, notifPayload())

	require.Error(t, err)
	sender.AssertNotCalled(t, "Send")
	pushSubRepo.AssertNotCalled(t, "ListByUserIDs")
	notifRepo.AssertNotCalled(t, "UpdateDelivery")
}

// Nil payload is rejected with InvalidArgument before any record is created.
func TestNotify_NilPayloadRejected(t *testing.T) {
	t.Parallel()

	notifRepo := entitymocks.NewMockNotificationRepository(t)
	pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
	sender := entitymocks.NewMockPushNotificationSender(t)

	uc := buildNotificationUC(t, notifRepo, pushSubRepo, sender)
	_, err := uc.Notify(context.Background(), "user-1", entity.NotificationTypeNewConcerts, nil)

	require.ErrorIs(t, err, apperr.ErrInvalidArgument)
	notifRepo.AssertNotCalled(t, "Create")
}

// Read idempotency: MarkRead loads the record, confirms ownership, and delegates
// the idempotent set to the repository.
func TestMarkRead_OwnedDelegatesToRepo(t *testing.T) {
	t.Parallel()

	notifRepo := entitymocks.NewMockNotificationRepository(t)
	pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
	sender := entitymocks.NewMockPushNotificationSender(t)

	notifRepo.EXPECT().
		Get(anyCtx, "n-1").
		Return(&entity.Notification{ID: "n-1", UserID: "user-1"}, nil)
	notifRepo.EXPECT().
		MarkRead(anyCtx, "user-1", "n-1").
		Return(nil)

	uc := buildNotificationUC(t, notifRepo, pushSubRepo, sender)
	require.NoError(t, uc.MarkRead(context.Background(), "user-1", "n-1"))
}

// Cross-user rejection: marking another user's notification is PermissionDenied
// and never reaches the repository mutation.
func TestMarkRead_CrossUserRejected(t *testing.T) {
	t.Parallel()

	notifRepo := entitymocks.NewMockNotificationRepository(t)
	pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
	sender := entitymocks.NewMockPushNotificationSender(t)

	notifRepo.EXPECT().
		Get(anyCtx, "n-1").
		Return(&entity.Notification{ID: "n-1", UserID: "owner"}, nil)

	uc := buildNotificationUC(t, notifRepo, pushSubRepo, sender)
	err := uc.MarkRead(context.Background(), "attacker", "n-1")

	require.ErrorIs(t, err, apperr.ErrPermissionDenied)
	notifRepo.AssertNotCalled(t, "MarkRead")
}

// MarkDismissed enforces the same ownership rule.
func TestMarkDismissed_CrossUserRejected(t *testing.T) {
	t.Parallel()

	notifRepo := entitymocks.NewMockNotificationRepository(t)
	pushSubRepo := entitymocks.NewMockPushSubscriptionRepository(t)
	sender := entitymocks.NewMockPushNotificationSender(t)

	notifRepo.EXPECT().
		Get(anyCtx, "n-1").
		Return(&entity.Notification{ID: "n-1", UserID: "owner"}, nil)

	uc := buildNotificationUC(t, notifRepo, pushSubRepo, sender)
	err := uc.MarkDismissed(context.Background(), "attacker", "n-1")

	require.ErrorIs(t, err, apperr.ErrPermissionDenied)
	notifRepo.AssertNotCalled(t, "MarkDismissed")
}
