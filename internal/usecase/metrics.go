package usecase

import "context"

// ConcertMetrics records observability signals for concert search operations.
type ConcertMetrics interface {
	RecordConcertSearch(ctx context.Context, status string)
}

// FollowMetrics records observability signals for follow/unfollow operations.
type FollowMetrics interface {
	RecordFollow(ctx context.Context, action string)
}

// PushMetrics records observability signals for push notification send operations.
type PushMetrics interface {
	RecordPushSend(ctx context.Context, status string)
}
