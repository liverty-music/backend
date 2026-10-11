package usecase

import (
	mrand "math/rand/v2"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
)

// NewLotteryUseCaseWithRand exposes the LotteryUseCase constructor with an
// injected draw order source, so black-box tests can draw deterministically.
func NewLotteryUseCaseWithRand(
	saleRepo entity.TicketSaleRepository,
	entryRepo entity.LotteryEntryRepository,
	ticketRepo entity.TicketRepository,
	kycRepo entity.KycRepository,
	userRepo entity.UserRepository,
	holds LotteryHoldPort,
	clock Clock,
	newRand func() *mrand.Rand,
	logger *logging.Logger,
) LotteryUseCase {
	return newLotteryUseCase(saleRepo, entryRepo, ticketRepo, kycRepo, userRepo, holds, clock, newRand, logger)
}
