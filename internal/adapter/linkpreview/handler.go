package linkpreview

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// Pattern is the route of the handler on the fan-api Connect server's mux.
// The fan web's Caddy includes the response into the HTML of /concerts/{id}.
const Pattern = "GET /link-preview/concerts/{id}"

const (
	// cacheTTL bounds how long an edit to the Series or Event takes to reach a
	// newly served preview.
	cacheTTL = 60 * time.Second
	// maxCacheEntries caps the cache so requests for many distinct ids cannot
	// grow memory without bound; the endpoint is public and not rate-limited.
	maxCacheEntries = 10_000
)

// Handler serves the preview tag block of one concert page. It always answers
// 200: on NotFound or any error the body is empty, so the site-default tags
// in the fan web's index.html stand.
type Handler struct {
	concerts usecase.ConcertUseCase
	zones    ZoneNames
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

// ZoneNames reads the time zones of a country with their names, from which a
// preview names the zone its times are read in.
type ZoneNames interface {
	// ListByCountry returns the country's Areas, regions and time zones.
	ListByCountry(ctx context.Context, country string) (*entity.CountryAreas, error)
}

// NewHandler creates the link preview handler.
func NewHandler(concerts usecase.ConcertUseCase, zones ZoneNames, tags *TagBuilder, logger *logging.Logger) *Handler {
	return &Handler{
		concerts: concerts,
		zones:    zones,
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

// render reads the concert page's Concert and its Series' Concerts and renders
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
	return Render(h.tags.Build(concert, seriesConcerts, h.zoneName(ctx, concert.Venue)))
}

// zoneName returns the Japanese name of the Venue's time zone, without 時間:
// 日本 for Asia/Tokyo, the published name of another zone, and the city of its
// IANA name when none is published or the names cannot be read.
func (h *Handler) zoneName(ctx context.Context, v *entity.Venue) string {
	if v == nil || v.TimeZone == nil {
		return ""
	}
	zone := *v.TimeZone
	if zone == "Asia/Tokyo" {
		return "日本"
	}
	if v.AdminArea != nil && len(*v.AdminArea) >= 2 {
		if country, err := h.zones.ListByCountry(ctx, (*v.AdminArea)[:2]); err == nil {
			for _, z := range country.TimeZones {
				if z.Name == zone && z.NameJA != nil {
					return *z.NameJA
				}
			}
		} else {
			h.logger.Warn(ctx, "failed to read the time zone names for a link preview", slog.String("error", err.Error()))
		}
	}
	city := zone[strings.LastIndex(zone, "/")+1:]
	return strings.ReplaceAll(city, "_", " ")
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
