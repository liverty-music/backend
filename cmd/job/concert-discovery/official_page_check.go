package main

// The official-page-check mode measures whether official pages change before
// new concerts are found (OpenSpec change
// measure-official-page-change-detection, design D1 to D5).
//
// TODO(measure-official-page-change-detection): remove this file and the
// mode after the measurement.

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/liverty-music/backend/pkg/analyticslog"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"golang.org/x/sync/errgroup"
)

// pageCheckConcurrency is how many artists' sites are read at once. The pages
// of one site are read one after another, so no site gets parallel requests.
const pageCheckConcurrency = 4

// pageChecker checks the official pages of followed artists.
type pageChecker struct {
	artists    entity.ArtistRepository
	organizers entity.OrganizerRepository
	// check reads the official pages of one site (gemini.CheckOfficialPages
	// with the official page client).
	check  func(ctx context.Context, siteURL string) []gemini.OfficialPageCheck
	logger *logging.Logger
}

// pageCheckResult is how far a run got: the artists whose pages it read (or
// whose official site it failed to look up), those whose top page was not
// read, and the pages it read and failed to read.
type pageCheckResult struct {
	Artists     int
	FailedTop   int
	Pages       int
	FailedPages int
	mu          sync.Mutex
}

// run checks the official pages of every followed artist the concert search
// would search: it leaves out artists whose activity has ended, those an
// active organizer represents and those without an official site, as
// SearchNewConcerts does. It logs one line per page and returns how far it
// got; a site that fails does not stop the run.
func (c *pageChecker) run(ctx context.Context, followed []*entity.Artist) *pageCheckResult {
	result := &pageCheckResult{}
	var g errgroup.Group
	g.SetLimit(pageCheckConcurrency)
	for _, artist := range followed {
		if ctx.Err() != nil {
			break
		}
		g.Go(func() error {
			c.checkArtist(ctx, artist, result)
			return nil
		})
	}
	_ = g.Wait()
	return result
}

// checkArtist checks one artist's pages and adds them to result.
func (c *pageChecker) checkArtist(ctx context.Context, artist *entity.Artist, result *pageCheckResult) {
	if !artist.IsActive() || c.represented(ctx, artist.ID) {
		return
	}
	site, err := c.artists.GetOfficialSite(ctx, artist.ID)
	if errors.Is(err, apperr.ErrNotFound) {
		return
	}
	if err != nil {
		c.logger.Error(ctx, "failed to get the official site for the official page check", err,
			slog.String("artist_id", artist.ID))
		result.add(true, 0, 0)
		return
	}

	checks := c.check(ctx, site.URL)
	if ctx.Err() != nil {
		// Reads cut off by a shutdown say nothing about the site: the
		// artist counts as not reached, and its pages are not logged.
		return
	}
	failed := 0
	for _, p := range checks {
		if p.Status != gemini.PageStatusOK {
			failed++
		}
		c.logger.Info(ctx, "official page checked",
			slog.String("artist_id", artist.ID),
			slog.String("page_url", p.URL),
			slog.String("page_kind", p.Kind),
			slog.String("status", p.Status),
			slog.Int("http_status", p.HTTPStatus),
			slog.String("text_sha256", p.TextSHA256),
			slog.Int("text_length", p.TextLength),
		)
	}
	result.add(len(checks) == 0 || checks[0].Status != gemini.PageStatusOK, len(checks), failed)
}

// represented reports whether an active organizer represents the artist. A
// failure to check is logged and the artist is checked, as the concert
// search does.
func (c *pageChecker) represented(ctx context.Context, artistID string) bool {
	represented, err := c.organizers.IsArtistRepresentedByActiveOrganizer(ctx, artistID)
	if err != nil {
		c.logger.Warn(ctx, "failed to check organizer representation for the official page check",
			slog.String("artist_id", artistID), slog.Any("error", err))
		return false
	}
	return represented
}

func (r *pageCheckResult) add(failedTop bool, pages, failedPages int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Artists++
	if failedTop {
		r.FailedTop++
	}
	r.Pages += pages
	r.FailedPages += failedPages
}

// logPageCheckSummary writes the run's summary line, marked for analytics:
// the artists reached, those whose top page was not read, and those whose
// was. The run never stops early: a site that fails says nothing about the
// next one.
func logPageCheckSummary(ctx context.Context, logger *logging.Logger, followed int, result *pageCheckResult) {
	logger.Info(ctx, "official page check complete", analyticslog.Attrs(analyticslog.TopicJobResult,
		slog.Int("followed_artists", followed),
		slog.Int("artists_attempted", result.Artists),
		slog.Int("artists_succeeded", result.Artists-result.FailedTop),
		slog.Int("failures", result.FailedTop),
		slog.Bool("circuit_broken", false),
		slog.Int("pages_checked", result.Pages),
		slog.Int("pages_failed", result.FailedPages),
	)...)
}
