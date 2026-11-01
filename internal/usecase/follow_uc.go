package usecase

import (
	"context"
	"errors"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// FollowUseCase defines the interface for follow-related business logic.
type FollowUseCase interface {
	// Follow establishes a subscription between a user and an artist.
	//
	// # Possible errors:
	//
	//   - NotFound: the artist does not exist.
	//   - Internal: unexpected failure during relationship establishment.
	Follow(ctx context.Context, userID string, artistID string) error

	// Unfollow terminates a subscription between a user and an artist.
	//
	// # Possible errors:
	//
	//   - Internal: execution failure.
	Unfollow(ctx context.Context, userID, artistID string) error

	// SetHype updates the enthusiasm tier for a followed artist.
	//
	// # Possible errors:
	//
	//   - NotFound: the user is not following the specified artist.
	SetHype(ctx context.Context, userID, artistID string, hype entity.Hype) error

	// List retrieves all artists currently followed by the specified user,
	// enriched with per-user hype metadata.
	//
	// # Possible errors:
	//
	//   - Internal: query failure.
	List(ctx context.Context, userID string) ([]*entity.FollowedArtist, error)
}

// followUseCase implements the FollowUseCase interface.
type followUseCase struct {
	followRepo   entity.FollowRepository
	artistRepo   entity.ArtistRepository
	siteResolver entity.OfficialSiteResolver
	metrics      FollowMetrics
	logger       *logging.Logger
}

// Compile-time interface compliance check.
var _ FollowUseCase = (*followUseCase)(nil)

// NewFollowUseCase creates a new instance of the follow business logic handler.
func NewFollowUseCase(
	followRepo entity.FollowRepository,
	artistRepo entity.ArtistRepository,
	siteResolver entity.OfficialSiteResolver,
	metrics FollowMetrics,
	logger *logging.Logger,
) FollowUseCase {
	return &followUseCase{
		followRepo:   followRepo,
		artistRepo:   artistRepo,
		siteResolver: siteResolver,
		metrics:      metrics,
		logger:       logger,
	}
}

// Follow establishes a follow relationship between a user and an artist
// through Follow.Follow, which announces the new Follow (ARTIST.followed)
// together with storing it. After the follow is stored, it asynchronously
// resolves and stores the artist's official site URL if one is not already
// recorded. A repeat follow succeeds without starting that work.
//
// # Possible errors:
//
//   - Unavailable: the announcement could not be recorded; nothing is stored
//     and the call can be retried.
//   - Internal: any other failure to store the follow, including an unknown
//     artist.
func (uc *followUseCase) Follow(ctx context.Context, userID string, artistID string) error {
	err := uc.followRepo.Follow(ctx, userID, artistID)
	if err != nil {
		if errors.Is(err, apperr.ErrAlreadyExists) {
			return nil
		}
		if errors.Is(err, apperr.ErrUnavailable) {
			return err
		}
		return apperr.Wrap(err, codes.Internal, "failed to establish follow relationship")
	}

	uc.logger.Info(ctx, "User followed artist", slog.String("user_id", userID), slog.String("artist_id", artistID))
	uc.metrics.RecordFollow(ctx, "follow")

	bgCtx := context.WithoutCancel(ctx)
	go uc.resolveAndPersistOfficialSite(bgCtx, artistID)

	return nil
}

// resolveAndPersistOfficialSite fetches the official site URL from MusicBrainz
// and persists it for the given artist. It is intended to run in a background
// goroutine; all errors are logged and swallowed.
func (uc *followUseCase) resolveAndPersistOfficialSite(ctx context.Context, artistID string) {
	// Skip if a record already exists.
	_, err := uc.artistRepo.GetOfficialSite(ctx, artistID)
	if err == nil {
		return
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		uc.logger.Warn(ctx, "failed to check official site before resolution", slog.String("artist_id", artistID), slog.Any("error", err))
		return
	}

	artist, err := uc.artistRepo.Get(ctx, artistID)
	if err != nil {
		uc.logger.Warn(ctx, "failed to get artist for official site resolution", slog.String("artist_id", artistID), slog.Any("error", err))
		return
	}

	if artist.MBID == "" {
		return
	}

	url, err := uc.siteResolver.ResolveOfficialSiteURL(ctx, artist.MBID)
	if err != nil {
		uc.logger.Warn(ctx, "failed to resolve official site URL", slog.String("artist_id", artistID), slog.String("mbid", artist.MBID), slog.Any("error", err))
		return
	}
	if url == "" {
		return
	}

	site := entity.NewOfficialSite(artistID, url)
	if err := uc.artistRepo.CreateOfficialSite(ctx, site); err != nil {
		if !errors.Is(err, apperr.ErrAlreadyExists) {
			uc.logger.Warn(ctx, "failed to persist official site", slog.String("artist_id", artistID), slog.String("url", url), slog.Any("error", err))
		}
		return
	}

	uc.logger.Info(ctx, "official site resolved and persisted", slog.String("artist_id", artistID), slog.String("url", url))
}

// Unfollow removes a follow relationship through Follow.Unfollow, which
// announces the removal (ARTIST.unfollowed) together with it. Unfollowing an
// artist the user does not follow succeeds.
func (uc *followUseCase) Unfollow(ctx context.Context, userID, artistID string) error {
	if err := uc.followRepo.Unfollow(ctx, userID, artistID); err != nil {
		return err
	}

	uc.logger.Info(ctx, "Artist unfollowed", slog.String("user_id", userID), slog.String("artist_id", artistID))
	uc.metrics.RecordFollow(ctx, "unfollow")
	return nil
}

// SetHype updates the enthusiasm tier for a followed artist through
// Follow.SetHype, which announces a changed level (ARTIST.hype_changed)
// together with the change.
func (uc *followUseCase) SetHype(ctx context.Context, userID, artistID string, hype entity.Hype) error {
	if err := uc.followRepo.SetHype(ctx, userID, artistID, hype); err != nil {
		return err
	}

	uc.logger.Info(ctx, "Hype updated",
		slog.String("user_id", userID),
		slog.String("artist_id", artistID),
		slog.String("hype", string(hype)),
	)
	return nil
}

// List retrieves the list of artists followed by a user, including hype level.
func (uc *followUseCase) List(ctx context.Context, userID string) ([]*entity.FollowedArtist, error) {
	return uc.followRepo.ListByUser(ctx, userID)
}
