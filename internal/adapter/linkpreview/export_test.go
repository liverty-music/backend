package linkpreview

import "time"

// SetNow replaces the handler's clock, for cache expiry tests.
func (h *Handler) SetNow(now func() time.Time) {
	h.now = now
}
