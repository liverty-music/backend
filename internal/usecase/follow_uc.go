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
	//   - InvalidArgument: required identification (ID) or user context is missing.
	//   - NotFound: the artist does not exist.
	//   - Internal: unexpected failure during relationship establishment.
	Follow(ctx context.Context, userID string, artistID string) error

	// Unfollow terminates a subscription between a user and an artist.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: missing user or artist identification.
	//   - Internal: execution failure.
	Unfollow(ctx context.Context, userID, artistID string) error

	// SetHype updates the enthusiasm tier for a followed artist.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: missing user or artist identification, or invalid hype.
	//   - NotFound: the user is not following the specified artist.
	SetHype(ctx context.Context, userID, artistID string, hype entity.Hype) error

	// ListFollowed retrieves all artists currently followed by the specified user,
	// enriched with per-user hype metadata.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: missing user identification.
	//   - Internal: query failure.
	ListFollowed(ctx context.Context, userID string) ([]*entity.FollowedArtist, error)
}

// followUseCase implements the FollowUseCase interface.
type followUseCase struct {
	followRepo    entity.FollowRepository
	artistRepo    entity.ArtistRepository
	userRepo      entity.UserRepository
	siteResolver  entity.OfficialSiteResolver
	concertUC     ConcertUseCase
	searchLogRepo entity.SearchLogRepository
	logger        *logging.Logger
}

// Compile-time interface compliance check.
var _ FollowUseCase = (*followUseCase)(nil)

// NewFollowUseCase creates a new instance of the follow business logic handler.
func NewFollowUseCase(
	followRepo entity.FollowRepository,
	artistRepo entity.ArtistRepository,
	userRepo entity.UserRepository,
	siteResolver entity.OfficialSiteResolver,
	concertUC ConcertUseCase,
	searchLogRepo entity.SearchLogRepository,
	logger *logging.Logger,
) FollowUseCase {
	return &followUseCase{
		followRepo:    followRepo,
		artistRepo:    artistRepo,
		userRepo:      userRepo,
		siteResolver:  siteResolver,
		concertUC:     concertUC,
		searchLogRepo: searchLogRepo,
		logger:        logger,
	}
}

// resolveUserID maps an external identity (Zitadel sub claim) to the internal user UUID.
func (uc *followUseCase) resolveUserID(ctx context.Context, externalID string) (string, error) {
	user, err := uc.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return "", apperr.Wrap(err, codes.NotFound, "resolve user by external ID")
	}
	return user.ID, nil
}

// Follow establishes a follow relationship between a user and an artist.
// After the follow is persisted, it asynchronously resolves and stores the
// artist's official site URL if one is not already recorded.
func (uc *followUseCase) Follow(ctx context.Context, userID string, artistID string) error {
	if userID == "" || artistID == "" {
		return apperr.New(codes.InvalidArgument, "user ID and artist ID are required")
	}

	internalUserID, err := uc.resolveUserID(ctx, userID)
	if err != nil {
		return err
	}

	err = uc.followRepo.Follow(ctx, internalUserID, artistID)
	if err != nil {
		// Treat "already following" as success
		if errors.Is(err, apperr.ErrAlreadyExists) {
			return nil
		}
		return apperr.Wrap(err, codes.Internal, "failed to establish follow relationship")
	}

	uc.logger.Info(ctx, "User followed artist", slog.String("user_id", internalUserID), slog.String("artist_id", artistID))

	bgCtx := context.WithoutCancel(ctx)
	go uc.resolveAndPersistOfficialSite(bgCtx, artistID)
	go uc.triggerFirstFollowSearch(bgCtx, artistID)

	return nil
}

// triggerFirstFollowSearch checks whether the artist has been searched before
// and, if not, triggers a background concert search. All errors are logged and
// swallowed so that the follow operation is never affected.
func (uc *followUseCase) triggerFirstFollowSearch(ctx context.Context, artistID string) {
	_, err := uc.searchLogRepo.GetByArtistID(ctx, artistID)
	if err == nil {
		// Search log exists — artist has been searched before, skip.
		return
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		uc.logger.Warn(ctx, "failed to check search log for first-follow search",
			slog.String("artist_id", artistID), slog.Any("error", err))
		return
	}

	// No search log — this is a first follow. Trigger concert discovery.
	uc.logger.Info(ctx, "First follow detected, triggering concert search",
		slog.String("artist_id", artistID))

	if _, err := uc.concertUC.SearchNewConcerts(ctx, artistID); err != nil {
		uc.logger.Warn(ctx, "background concert search failed after first follow",
			slog.String("artist_id", artistID), slog.Any("error", err))
	}
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

// Unfollow removes a follow relationship.
func (uc *followUseCase) Unfollow(ctx context.Context, userID, artistID string) error {
	if userID == "" || artistID == "" {
		return apperr.New(codes.InvalidArgument, "user ID and artist ID are required")
	}

	internalUserID, err := uc.resolveUserID(ctx, userID)
	if err != nil {
		return err
	}

	err = uc.followRepo.Unfollow(ctx, internalUserID, artistID)
	if err != nil {
		return err
	}

	uc.logger.Info(ctx, "Artist unfollowed", slog.String("user_id", internalUserID), slog.String("artist_id", artistID))
	return nil
}

// SetHype updates the enthusiasm tier for a followed artist.
func (uc *followUseCase) SetHype(ctx context.Context, userID, artistID string, hype entity.Hype) error {
	if userID == "" || artistID == "" {
		return apperr.New(codes.InvalidArgument, "user ID and artist ID are required")
	}

	internalUserID, err := uc.resolveUserID(ctx, userID)
	if err != nil {
		return err
	}

	err = uc.followRepo.SetHype(ctx, internalUserID, artistID, hype)
	if err != nil {
		return err
	}

	uc.logger.Info(ctx, "Hype updated",
		slog.String("user_id", internalUserID),
		slog.String("artist_id", artistID),
		slog.String("hype", string(hype)),
	)
	return nil
}

// ListFollowed retrieves the list of artists followed by a user, including hype level.
func (uc *followUseCase) ListFollowed(ctx context.Context, userID string) ([]*entity.FollowedArtist, error) {
	if userID == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID is required")
	}

	internalUserID, err := uc.resolveUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	return uc.followRepo.ListByUser(ctx, internalUserID)
}
