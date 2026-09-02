package entity

import "math/rand/v2"

// TicketApplicationID is the opaque identifier for a single fan's application
// to a lottery sales phase.
//
// TODO: swap to generated TicketApplicationId after BSR gen
// (liverty_music.entity.v1.TicketApplicationId.value wrapping a UUID string).
type TicketApplicationID string

// DrawApplication is the input record for one application entering the lottery
// draw. It carries only the fields the draw algorithm needs; all other
// application data (identity, payment authorization, etc.) is outside the
// algorithm's scope.
type DrawApplication struct {
	// ID is the opaque application identifier. Passed through unchanged in the
	// draw result so callers can correlate outcomes back to persisted rows.
	//
	// TODO: swap to generated TicketApplicationId after BSR gen.
	ID TicketApplicationID

	// RequestedTicketCount is the companion-group size for this application: how
	// many tickets must be allocated together (all-or-nothing). Must be positive.
	RequestedTicketCount int
}

// DrawWinner is the outcome for an application that was allocated tickets in
// the draw.
type DrawWinner struct {
	// Application is the original application that won.
	Application DrawApplication

	// DrawSequence is the application's zero-based position in the uniformly-
	// random shuffle that the draw ran. Recorded alongside the winning outcome
	// so the audit trail is preserved; not used to determine admission (winners
	// are chosen by greedy fit, not by sequence alone).
	//
	// Corresponds to TicketApplication.draw_sequence in the proto schema.
	DrawSequence int
}

// DrawLoser is the outcome for an application that was not allocated tickets.
// The losers are returned in ascending DrawSequence order (the draw's random
// shuffle order), which IS the persisted waitlist order for ⑦ official-resale.
type DrawLoser struct {
	// Application is the original application that lost.
	Application DrawApplication

	// DrawSequence is the application's zero-based position in the uniformly-
	// random shuffle. Losers are sorted by ascending DrawSequence so the waitlist
	// is deterministically ordered by draw outcome.
	//
	// Corresponds to TicketApplication.draw_sequence in the proto schema.
	DrawSequence int
}

// DrawResult holds the complete outcome of one lottery draw run. Every input
// application appears exactly once: either as a Winner or as a Loser (in
// Waitlist). No application is omitted.
type DrawResult struct {
	// Winners are the applications admitted by the greedy-fit pass, in no
	// guaranteed order (admission order is an implementation detail; callers
	// should not rely on it).
	Winners []DrawWinner

	// Waitlist are the applications not admitted, sorted in ascending DrawSequence
	// order. This slice IS the persisted waitlist: the loser draw order consumed
	// by ⑦ official-resale and any future 二次抽選.
	Waitlist []DrawLoser
}

// RunLotteryDraw executes the lottery draw algorithm against the given
// applications and ticket capacity.
//
// Algorithm — uniform-random order + greedy fit, whole-application
// all-or-nothing:
//
//  1. The applications are shuffled into a uniformly-random order using rng.
//     Passing a deterministic *rand.Rand (seeded from a fixed value) makes the
//     draw fully reproducible for tests. In production, seed with a
//     cryptographically random value.
//
//  2. The shuffled list is walked once. An application is admitted as a winner
//     if its RequestedTicketCount fits within the remaining capacity
//     (remaining >= count). Admission is whole-application and all-or-nothing:
//     the full RequestedTicketCount is deducted, never a fraction. The walk
//     continues past any application that does not fit so that a later,
//     smaller application can still fill remaining capacity (greedy fit,
//     better utilisation).
//
//  3. The total tickets allocated to winners never exceeds ticketCapacity.
//
//  4. Applications not admitted become losers. The Waitlist in the returned
//     DrawResult is sorted in ascending DrawSequence order (the shuffle order),
//     which is the persisted waitlist order for ⑦ official-resale.
//
// RunLotteryDraw returns an empty DrawResult (no winners, no losers) when
// applications is empty. It returns no error; validation (positive capacity,
// positive RequestedTicketCount) is the caller's responsibility. As a defensive
// measure, an application with a non-positive RequestedTicketCount is never
// admitted (it becomes a waitlisted loser), so malformed input can never inflate
// a winner slot or break the no-oversell invariant. A non-positive capacity
// simply admits no one.
//
// The rng parameter must not be nil.
func RunLotteryDraw(applications []DrawApplication, ticketCapacity int, rng *rand.Rand) DrawResult {
	if len(applications) == 0 {
		return DrawResult{}
	}

	// Build a shuffled index slice rather than copying the application slice, to
	// avoid an extra allocation and to keep the original slice unmodified.
	indices := make([]int, len(applications))
	for i := range indices {
		indices[i] = i
	}
	rng.Shuffle(len(indices), func(i, j int) {
		indices[i], indices[j] = indices[j], indices[i]
	})

	remaining := ticketCapacity
	winners := make([]DrawWinner, 0, len(applications))
	waitlist := make([]DrawLoser, 0, len(applications))

	for seq, origIdx := range indices {
		app := applications[origIdx]
		// Admit only a positive request that fits the remaining capacity. The
		// RequestedTicketCount > 0 guard is defensive: although the caller is
		// responsible for validating positive counts, a stray zero would waste a
		// winner slot on a 0-ticket allocation, and a negative count would ADD to
		// remaining and break the no-oversell invariant. Non-positive requests are
		// therefore never admitted — they fall through to the waitlist (safe side),
		// never affecting remaining.
		if app.RequestedTicketCount > 0 && app.RequestedTicketCount <= remaining {
			remaining -= app.RequestedTicketCount
			winners = append(winners, DrawWinner{
				Application:  app,
				DrawSequence: seq,
			})
		} else {
			// Does not fit (or is a non-positive request): becomes a loser in the
			// waitlist, ordered by draw_sequence.
			waitlist = append(waitlist, DrawLoser{
				Application:  app,
				DrawSequence: seq,
			})
		}
	}

	// The waitlist is already in ascending DrawSequence order because we walk the
	// shuffled slice linearly and append losers in encounter order. No sort needed.

	return DrawResult{
		Winners:  winners,
		Waitlist: waitlist,
	}
}
