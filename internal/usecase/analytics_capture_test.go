package usecase_test

import (
	"context"
	"sync"

	"github.com/stretchr/testify/mock"

	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

// capturedAnalytics lists the events an AnalyticsClient mock was given.
type capturedAnalytics struct {
	mu     sync.Mutex
	events []usecase.AnalyticsEvent
}

// captureAnalytics makes a accept any number of Capture calls, each
// returning err, and records the events it is given.
func captureAnalytics(a *ucmocks.MockAnalyticsClient, err error) *capturedAnalytics {
	c := &capturedAnalytics{}
	a.EXPECT().Capture(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, event usecase.AnalyticsEvent) error {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.events = append(c.events, event)
			return err
		}).Maybe()
	return c
}

// captured returns the captured events of name.
func (c *capturedAnalytics) captured(name usecase.AnalyticsEventName) []usecase.AnalyticsEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []usecase.AnalyticsEvent
	for _, e := range c.events {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
}

// sent returns the properties of the captured events of name.
func (c *capturedAnalytics) sent(name usecase.AnalyticsEventName) []usecase.AnalyticsProperties {
	var out []usecase.AnalyticsProperties
	for _, e := range c.captured(name) {
		out = append(out, e.Properties)
	}
	return out
}
