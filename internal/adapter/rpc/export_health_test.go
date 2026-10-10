package rpc

import "time"

// SetClock replaces the handler's time source for testing.
func (h *HealthCheckHandler) SetClock(now func() time.Time) { h.now = now }
