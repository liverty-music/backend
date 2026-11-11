package usecase_test

import (
	"testing"

	"github.com/stretchr/testify/mock"

	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

// anyConcertMetrics returns a ConcertMetrics mock that accepts every
// recording, for tests that do not check the concert search signal.
func anyConcertMetrics(t *testing.T) *ucmocks.MockConcertMetrics {
	t.Helper()
	m := ucmocks.NewMockConcertMetrics(t)
	m.EXPECT().RecordConcertSearch(mock.Anything, mock.Anything).Maybe()
	return m
}

// anyFollowMetrics returns a FollowMetrics mock that accepts every recording,
// for tests that do not check the follow signal.
func anyFollowMetrics(t *testing.T) *ucmocks.MockFollowMetrics {
	t.Helper()
	m := ucmocks.NewMockFollowMetrics(t)
	m.EXPECT().RecordFollow(mock.Anything, mock.Anything).Maybe()
	return m
}

// anyPushMetrics returns a PushMetrics mock that accepts every recording, for
// tests that do not check the push signals.
func anyPushMetrics(t *testing.T) *ucmocks.MockPushMetrics {
	t.Helper()
	m := ucmocks.NewMockPushMetrics(t)
	m.EXPECT().RecordPushSend(mock.Anything, mock.Anything).Maybe()
	m.EXPECT().RecordDeliveryOutcome(mock.Anything, mock.Anything, mock.Anything).Maybe()
	return m
}
