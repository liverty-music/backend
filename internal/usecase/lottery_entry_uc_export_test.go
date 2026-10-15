package usecase

import (
	mrand "math/rand/v2"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
)

// NewLotteryEntryUseCaseWithRand exposes the LotteryEntryUseCase constructor with an
// injected draw order source, so black-box tests can draw deterministically.
func NewLotteryEntryUseCaseWithRand(
	saleRepo entity.TicketSaleRepository,
	entryRepo entity.LotteryEntryRepository,
	referralRepo entity.ReferralRepository,
	ticketRepo entity.TicketRepository,
	identityVerificationRepo entity.IdentityVerificationRepository,
	userRepo entity.UserRepository,
	cardAuthorizations entity.CardAuthorizationRepository,
	clock Clock,
	newRand func() *mrand.Rand,
	logger *logging.Logger,
) LotteryEntryUseCase {
	return newLotteryUseCase(saleRepo, entryRepo, referralRepo, ticketRepo, identityVerificationRepo, userRepo, cardAuthorizations, clock, newRand, logger)
}
