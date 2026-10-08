package linkpreview

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"
	"uuid"

	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// Pattern is the route of the handler on the fan-api Connect server's mux.
// The fan web's Caddy includes the response into the HTML of /events/{id}.
const Pattern = "GET /link-preview/events/{id}"

const (
	// cacheTTL bounds how long an edit to the Series or Event takes to reach a
	// newly served preview.
	cacheTTL = 60 * time.Second
	// maxCacheEntries caps the cache so requests for many distinct ids cannot
	// grow memory without bound; the endpoint is public and not rate-limited.
	maxCacheEntries = 10_000
)

// Handler serves the preview tag block of one Event page. It always answers
// 200: on NotFound or any error the body is empty, so the site-default tags
// in the fan web's index.html stand.
type Handler struct {
	concerts usecase.ConcertUseCase
	tags     *TagBuilder
	logger   *logging.Logger
	now      func() time.Time

	mu    sync.Mutex
	cache map[string]cacheEntry
}

// cacheEntry is one cached response body (empty for an Event without a page).
type cacheEntry struct {
	body    []byte
	expires time.Time
}

// NewHandler creates the link preview handler.
func NewHandler(concerts usecase.ConcertUseCase, tags *TagBuilder, logger *logging.Logger) *Handler {
	return &Handler{
		concerts: concerts,
		tags:     tags,
		logger:   logger,
		now:      time.Now,
		cache:    make(map[string]cacheEntry),
	}
}

// ServeHTTP writes the tag block of the Event named by the {id} path value.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	eventID := id.String()

	if body, ok := h.cached(eventID); ok {
		write(w, body)
		return
	}

	body, err := h.render(r.Context(), eventID)
	switch {
	case err == nil:
		h.store(eventID, body)
	case errors.Is(err, apperr.ErrNotFound):
		h.store(eventID, nil)
	default:
		// Transient failures are not cached, so the next request retries.
		h.logger.Warn(r.Context(), "failed to build link preview; serving site defaults",
			slog.String("event_id", eventID), slog.String("error", err.Error()))
	}
	write(w, body)
}

// render reads the Event page's Concert and its Series' Concerts and renders
// the tag block.
func (h *Handler) render(ctx context.Context, eventID string) ([]byte, error) {
	concert, err := h.concerts.Get(ctx, eventID)
	if err != nil {
		return nil, err
	}
	seriesConcerts, err := h.concerts.ListBySeries(ctx, concert.SeriesID)
	if err != nil {
		return nil, err
	}
	return Render(h.tags.Build(concert, seriesConcerts))
}

func (h *Handler) cached(eventID string) ([]byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.cache[eventID]
	if !ok || !h.now().Before(e.expires) {
		return nil, false
	}
	return e.body, true
}

func (h *Handler) store(eventID string, body []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	if len(h.cache) >= maxCacheEntries {
		for id, e := range h.cache {
			if !now.Before(e.expires) {
				delete(h.cache, id)
			}
		}
		if len(h.cache) >= maxCacheEntries {
			return
		}
	}
	h.cache[eventID] = cacheEntry{body: body, expires: now.Add(cacheTTL)}
}

// write answers 200 with body, which may be empty.
func write(w http.ResponseWriter, body []byte) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
