package usecase

import (
	"context"
	"log/slog"
	"net/url"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
)

// maxMerchURLLength mirrors the proto Url value-object constraint
// (entity/v1 Url.value max_len = 2048). A resolved value longer than this — or
// otherwise not a valid absolute http(s) URI — is discarded rather than
// persisted, matching the "invalid URL discarded" requirement.
const maxMerchURLLength = 2048

// MerchOutcome classifies what happened to a single candidate so the job can
// aggregate run-level counts without re-deriving state.
type MerchOutcome string

const (
	// MerchOutcomeAlreadyLive — the existing merch_url passed the liveness
	// check and was left unchanged (no search, no write).
	MerchOutcomeAlreadyLive MerchOutcome = "already_live"
	// MerchOutcomeNoSource — no confident official source was found; the field
	// stays empty.
	MerchOutcomeNoSource MerchOutcome = "no_source"
	// MerchOutcomeInvalidDiscarded — the resolved value failed URL validation
	// and was discarded; the field stays empty.
	MerchOutcomeInvalidDiscarded MerchOutcome = "invalid_discarded"
	// MerchOutcomeFilled — a valid official URL was persisted to an empty field.
	MerchOutcomeFilled MerchOutcome = "filled"
)

// MerchDiscoveryUseCase resolves and maintains Series.merch_url for series with
// an upcoming earliest event.
type MerchDiscoveryUseCase interface {
	// ListCandidates returns every in-window series (earliest event within the
	// configured window), each paired with a representative artist name and its
	// current merch_url. The caller iterates and applies the circuit breaker.
	ListCandidates(ctx context.Context) ([]*entity.MerchCandidate, error)
	// ResolveMerchURL processes one candidate: it revalidates a non-empty
	// merch_url (clearing it if dead), then — when the field is empty — searches
	// for an official URL and persists it fill-once. A live link is never
	// overwritten and an invalid resolved value is discarded.
	//
	// Only a genuine resolution or persistence failure returns a non-nil error
	// (which the job counts toward its circuit breaker); "already live", "no
	// source", and "invalid discarded" are successful, non-fatal outcomes.
	ResolveMerchURL(ctx context.Context, candidate *entity.MerchCandidate) (MerchOutcome, error)
}

type merchDiscoveryUseCase struct {
	seriesRepo entity.SeriesRepository
	searcher   entity.MerchSearcher
	checker    entity.MerchLivenessChecker
	window     time.Duration
	logger     *logging.Logger
}

// Compile-time interface compliance check.
var _ MerchDiscoveryUseCase = (*merchDiscoveryUseCase)(nil)

// NewMerchDiscoveryUseCase wires the merch-url discovery use case.
func NewMerchDiscoveryUseCase(
	seriesRepo entity.SeriesRepository,
	searcher entity.MerchSearcher,
	checker entity.MerchLivenessChecker,
	window time.Duration,
	logger *logging.Logger,
) *merchDiscoveryUseCase {
	return &merchDiscoveryUseCase{
		seriesRepo: seriesRepo,
		searcher:   searcher,
		checker:    checker,
		window:     window,
		logger:     logger,
	}
}

// ListCandidates implements [MerchDiscoveryUseCase].
func (uc *merchDiscoveryUseCase) ListCandidates(ctx context.Context) ([]*entity.MerchCandidate, error) {
	return uc.seriesRepo.ListSeriesInMerchWindow(ctx, uc.window)
}

// ResolveMerchURL implements [MerchDiscoveryUseCase].
func (uc *merchDiscoveryUseCase) ResolveMerchURL(ctx context.Context, candidate *entity.MerchCandidate) (MerchOutcome, error) {
	attrs := []slog.Attr{
		slog.String("series_id", candidate.SeriesID),
		slog.String("series_title", candidate.SeriesTitle),
	}

	// Revalidate an existing link first. A live link is left untouched; a dead
	// link is cleared so the field is empty before the fill-once search.
	if candidate.MerchURL != "" {
		if !uc.checker.IsDeadLink(ctx, candidate.MerchURL) {
			uc.logger.Debug(ctx, "merch url still live; leaving unchanged", attrs...)
			return MerchOutcomeAlreadyLive, nil
		}
		if err := uc.seriesRepo.ClearMerchURL(ctx, candidate.SeriesID); err != nil {
			return "", err
		}
		uc.logger.Info(ctx, "cleared dead merch url before re-search",
			append(attrs, slog.String("dead_url", candidate.MerchURL))...)
	}

	resolved, err := uc.searcher.SearchMerchURL(ctx, candidate.ArtistName, candidate.SeriesTitle)
	if err != nil {
		return "", err
	}
	if resolved == "" {
		uc.logger.Debug(ctx, "no confident official merch url found", attrs...)
		return MerchOutcomeNoSource, nil
	}
	if !validMerchURL(resolved) {
		uc.logger.Warn(ctx, "resolved merch url failed validation; discarding",
			append(attrs, slog.String("resolved_url", resolved))...)
		return MerchOutcomeInvalidDiscarded, nil
	}

	if err := uc.seriesRepo.SetMerchURL(ctx, candidate.SeriesID, resolved); err != nil {
		return "", err
	}
	uc.logger.Info(ctx, "persisted merch url",
		append(attrs, slog.String("merch_url", resolved))...)
	return MerchOutcomeFilled, nil
}

// validMerchURL mirrors the proto Url value-object constraints: a non-empty,
// absolute http(s) URI no longer than maxMerchURLLength.
func validMerchURL(raw string) bool {
	if raw == "" || len(raw) > maxMerchURLLength {
		return false
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != ""
}
