package usecase

import (
	"context"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// ConcertUseCase defines the interface for concert-related business logic.
type ConcertUseCase interface {
	ListByArtist(ctx context.Context, artistID string) ([]*entity.Concert, error)
}

// concertUseCase implements the ConcertUseCase interface.
type concertUseCase struct {
	concertRepo entity.ConcertRepository
	logger      *logging.Logger
}

// Compile-time interface compliance check
var _ ConcertUseCase = (*concertUseCase)(nil)

// NewConcertUseCase creates a new concert use case.
func NewConcertUseCase(concertRepo entity.ConcertRepository, logger *logging.Logger) ConcertUseCase {
	return &concertUseCase{
		concertRepo: concertRepo,
		logger:      logger,
	}
}

// ListByArtist returns all concerts for a specific artist.
func (uc *concertUseCase) ListByArtist(ctx context.Context, artistID string) ([]*entity.Concert, error) {
	if artistID == "" {
		return nil, apperr.New(codes.InvalidArgument, "artist ID is required")
	}

	concerts, err := uc.concertRepo.ListByArtist(ctx, artistID)
	if err != nil {
		return nil, err
	}

	return concerts, nil
}
