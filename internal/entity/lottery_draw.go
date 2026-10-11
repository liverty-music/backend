package entity

import "math/rand/v2"

// DrawEntry is the input record for one entry entering the lottery
// draw. It carries only the fields the draw algorithm needs; all other
// entry data (identity, payment authorization, etc.) is outside the
// algorithm's scope.
type DrawEntry struct {
	// ID is the entry's identifier. Passed through unchanged in the draw
	// result so callers can correlate outcomes back to persisted rows.
	ID LotteryEntryID

	// TicketCount is the companion-group size for this entry: how
	// many tickets must be allocated together (all-or-nothing). Must be positive.
	TicketCount int
}

// DrawWinner is the outcome for an entry that was allocated tickets in
// the draw.
type DrawWinner struct {
	// Entry is the original entry that won.
	Entry DrawEntry

	// DrawPosition is the entry's zero-based position in the uniformly-
	// random shuffle that the draw ran. Recorded alongside the winning outcome
	// so the audit trail is preserved; not used to determine admission (winners
	// are chosen by greedy fit, not by position alone).
	//
	// Corresponds to LotteryEntry.draw_position in the proto schema.
	DrawPosition int
}

// DrawLoser is the outcome for an entry that was not allocated tickets.
// The losers are returned in ascending DrawPosition order (the draw's random
// shuffle order), which IS the persisted waitlist order for ⑦ official-resale.
type DrawLoser struct {
	// Entry is the original entry that lost.
	Entry DrawEntry

	// DrawPosition is the entry's zero-based position in the uniformly-
	// random shuffle. Losers are sorted by ascending DrawPosition so the waitlist
	// is deterministically ordered by draw outcome.
	//
	// Corresponds to LotteryEntry.draw_position in the proto schema.
	DrawPosition int
}

// DrawResult holds the complete outcome of one lottery draw run. Every input
// entry appears exactly once: either as a Winner or as a Loser (in
// Waitlist). No entry is omitted.
type DrawResult struct {
	// Winners are the entries admitted by the greedy-fit pass, in no
	// guaranteed order (admission order is an implementation detail; callers
	// should not rely on it).
	Winners []DrawWinner

	// Waitlist are the entries not admitted, sorted in ascending DrawPosition
	// order. This slice IS the persisted waitlist: the loser draw order consumed
	// by ⑦ official-resale and any future 二次抽選.
	Waitlist []DrawLoser
}

// RunLotteryDraw executes the lottery draw algorithm against the given
// entries and ticket capacity.
//
// Algorithm — uniform-random order + greedy fit, whole-entry
// all-or-nothing:
//
//  1. The entries are shuffled into a uniformly-random order using rng.
//     Passing a deterministic *rand.Rand (seeded from a fixed value) makes the
//     draw fully reproducible for tests. In production, seed with a
//     cryptographically random value.
//
//  2. The shuffled list is walked once. An entry is admitted as a winner
//     if its TicketCount fits within the remaining capacity
//     (remaining >= count). Admission is whole-entry and all-or-nothing:
//     the full TicketCount is deducted, never a fraction. The walk
//     continues past any entry that does not fit so that a later,
//     smaller entry can still fill remaining capacity (greedy fit,
//     better utilisation).
//
//  3. The total tickets allocated to winners never exceeds ticketCapacity.
//
//  4. Entries not admitted become losers. The Waitlist in the returned
//     DrawResult is sorted in ascending DrawPosition order (the shuffle order),
//     which is the persisted waitlist order for ⑦ official-resale.
//
// RunLotteryDraw returns an empty DrawResult (no winners, no losers) when
// entries is empty. It returns no error; validation (positive capacity,
// positive TicketCount) is the caller's responsibility. As a defensive
// measure, an entry with a non-positive TicketCount is never
// admitted (it becomes a waitlisted loser), so malformed input can never inflate
// a winner slot or break the no-oversell invariant. A non-positive capacity
// simply admits no one.
//
// The rng parameter must not be nil.
func RunLotteryDraw(entries []DrawEntry, ticketCapacity int, rng *rand.Rand) DrawResult {
	if len(entries) == 0 {
		return DrawResult{}
	}

	// Build a shuffled index slice rather than copying the entry slice, to
	// avoid an extra allocation and to keep the original slice unmodified.
	indices := make([]int, len(entries))
	for i := range indices {
		indices[i] = i
	}
	rng.Shuffle(len(indices), func(i, j int) {
		indices[i], indices[j] = indices[j], indices[i]
	})

	remaining := ticketCapacity
	winners := make([]DrawWinner, 0, len(entries))
	waitlist := make([]DrawLoser, 0, len(entries))

	for pos, origIdx := range indices {
		entry := entries[origIdx]
		// Admit only a positive request that fits the remaining capacity. The
		// TicketCount > 0 guard is defensive: although the caller is
		// responsible for validating positive counts, a stray zero would waste a
		// winner slot on a 0-ticket allocation, and a negative count would ADD to
		// remaining and break the no-oversell invariant. Non-positive requests are
		// therefore never admitted — they fall through to the waitlist (safe side),
		// never affecting remaining.
		if entry.TicketCount > 0 && entry.TicketCount <= remaining {
			remaining -= entry.TicketCount
			winners = append(winners, DrawWinner{
				Entry:        entry,
				DrawPosition: pos,
			})
		} else {
			// Does not fit (or is a non-positive request): becomes a loser in the
			// waitlist, ordered by draw position.
			waitlist = append(waitlist, DrawLoser{
				Entry:        entry,
				DrawPosition: pos,
			})
		}
	}

	// The waitlist is already in ascending DrawPosition order because we walk the
	// shuffled slice linearly and append losers in encounter order. No sort needed.

	return DrawResult{
		Winners:  winners,
		Waitlist: waitlist,
	}
}
