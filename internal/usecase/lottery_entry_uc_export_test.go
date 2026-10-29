package usecase

import (
	mrand "math/rand/v2"
)

// NewLotteryEntryUseCaseWithRand exposes the LotteryEntryUseCase constructor with an
// injected draw order source, so black-box tests can draw deterministically.
func NewLotteryEntryUseCaseWithRand(d LotteryEntryDeps, newRand func() *mrand.Rand) LotteryEntryUseCase {
	return newLotteryUseCase(d, newRand)
}
