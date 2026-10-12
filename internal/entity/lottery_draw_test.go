package entity_test

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertDrawIntegrity checks the structural invariants that must hold for ANY
// draw result regardless of the shuffle: (1) partition — every input entry
// appears exactly once across winners + losers; (2) draw position values form
// a permutation of 0..n-1; (3) each winner's TicketCount is preserved
// (all-or-nothing, no mutation); (4) no winner has a non-positive count. It is
// applied to every table case so a DrawPosition/count/partition regression
// cannot hide behind an ID-only ElementsMatch assertion.
func assertDrawIntegrity(t *testing.T, input []entity.DrawEntry, result entity.DrawResult) {
	t.Helper()

	countByID := make(map[entity.LotteryEntryID]int, len(input))
	for _, a := range input {
		countByID[a.ID] = a.TicketCount
	}

	seen := make(map[entity.LotteryEntryID]int, len(input))
	seqs := make([]int, 0, len(input))
	for _, w := range result.Winners {
		seen[w.Entry.ID]++
		seqs = append(seqs, w.DrawPosition)
		assert.Equalf(t, countByID[w.Entry.ID], w.Entry.TicketCount,
			"winner %q: TicketCount must be preserved (all-or-nothing)", w.Entry.ID)
		assert.Positivef(t, w.Entry.TicketCount,
			"winner %q: a non-positive request must never be admitted", w.Entry.ID)
	}
	for _, l := range result.Losers {
		seen[l.Entry.ID]++
		seqs = append(seqs, l.DrawPosition)
	}

	// Partition: exactly the input set, each once.
	require.Lenf(t, seen, len(input),
		"result must contain exactly the input entries (distinct IDs)")
	for _, a := range input {
		assert.Equalf(t, 1, seen[a.ID],
			"entry %q must appear exactly once across winners and losers", a.ID)
	}

	// draw position values are a permutation of 0..n-1.
	sort.Ints(seqs)
	expected := make([]int, len(input))
	for i := range expected {
		expected[i] = i
	}
	assert.Equal(t, expected, seqs,
		"draw sequences across winners and losers must be a permutation of 0..n-1")
}

// uniqueDrawEntries builds n entries with distinct IDs (entry-0..entry-n-1) and the
// per-entry TicketCount produced by count(i). Distinct IDs are
// required so partition and per-entry properties are actually checkable at
// scale (a shared ID makes drops/duplicates invisible).
func uniqueDrawEntries(n int, count func(i int) int) []entity.DrawEntry {
	entries := make([]entity.DrawEntry, n)
	for i := range entries {
		entries[i] = entity.DrawEntry{
			ID:          entity.LotteryEntryID(fmt.Sprintf("entry-%d", i)),
			TicketCount: count(i),
		}
	}
	return entries
}

// newSeededRng constructs a deterministic *rand.Rand from a fixed seed for use
// in table-driven tests. Using a seeded source guarantees reproducible shuffle
// orders without relying on global state.
func newSeededRng(seed uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, 0))
}

// drawEntries is a helper that builds a []entity.DrawEntry slice from a
// sequence of (id, count) pairs supplied as alternating strings and ints.
func drawEntries(pairs ...any) []entity.DrawEntry {
	if len(pairs)%2 != 0 {
		panic("drawEntries: pairs must be even-length (id, count, id, count, ...)")
	}
	entries := make([]entity.DrawEntry, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		entries = append(entries, entity.DrawEntry{
			ID:          entity.LotteryEntryID(pairs[i].(string)),
			TicketCount: pairs[i+1].(int),
		})
	}
	return entries
}

// collectIDs extracts the entry IDs from a winner or loser slice for
// order-independent membership assertions.
func collectWinnerIDs(ws []entity.DrawWinner) []string {
	ids := make([]string, len(ws))
	for i, w := range ws {
		ids[i] = string(w.Entry.ID)
	}
	return ids
}

func collectLoserIDs(ls []entity.DrawLoser) []string {
	ids := make([]string, len(ls))
	for i, l := range ls {
		ids[i] = string(l.Entry.ID)
	}
	return ids
}

// TestRunLotteryDraw covers the core draw scenarios with deterministic seeds.
func TestRunLotteryDraw(t *testing.T) {
	t.Parallel()

	type args struct {
		entries  []entity.DrawEntry
		quantity int
		rng      *rand.Rand
	}
	tests := []struct {
		name          string
		args          args
		wantWinnerIDs []string // nil = skip winner ID check; set to non-nil (even empty) to assert
		wantLoserIDs  []string // nil = skip loser ID check; set to non-nil (even empty) to assert
	}{
		// @spec components/entity/lottery-entry/run-lottery-draw "No entries"
		{
			name: "return empty result when no entries",
			args: args{
				entries:  nil,
				quantity: 100,
				rng:      newSeededRng(1),
			},
			wantWinnerIDs: []string{},
			wantLoserIDs:  []string{},
		},
		{
			name: "return empty result when entries slice is empty",
			args: args{
				entries:  []entity.DrawEntry{},
				quantity: 100,
				rng:      newSeededRng(1),
			},
			wantWinnerIDs: []string{},
			wantLoserIDs:  []string{},
		},
		{
			name: "all entries win when total demand is exactly equal to quantity",
			args: args{
				entries: drawEntries(
					"app-1", 2,
					"app-2", 3,
					"app-3", 5,
				), // 2+3+5 = 10
				quantity: 10,
				rng:      newSeededRng(42),
			},
			wantWinnerIDs: []string{"app-1", "app-2", "app-3"},
			wantLoserIDs:  []string{},
		},
		// @spec components/entity/lottery-entry/run-lottery-draw "Demand at or below quantity"
		{
			name: "all entries win when total demand is below quantity",
			args: args{
				entries: drawEntries(
					"app-1", 1,
					"app-2", 2,
				), // 3 tickets demanded, 50 available
				quantity: 50,
				rng:      newSeededRng(7),
			},
			wantWinnerIDs: []string{"app-1", "app-2"},
			wantLoserIDs:  []string{},
		},
		{
			name: "single entry larger than quantity loses and quantity stays unfilled",
			args: args{
				entries: drawEntries(
					"app-big", 10,
				),
				quantity: 5,
				rng:      newSeededRng(3),
			},
			wantWinnerIDs: []string{},
			wantLoserIDs:  []string{"app-big"},
		},
		{
			name: "greedy fit: skip too-large entry and admit later smaller one",
			// quantity = 3. With any shuffle order:
			//   - if app-big(5) goes first: 5 > 3 → loses; then app-small(2) → 2 <= 3 wins.
			//   - if app-small(2) goes first: 2 <= 3 wins, remaining=1; then app-big(5) → 5 > 1 loses.
			// Either way: app-small wins, app-big loses.
			args: args{
				entries: drawEntries(
					"app-small", 2,
					"app-big", 5,
				),
				quantity: 3,
				rng:      newSeededRng(99),
			},
			wantWinnerIDs: []string{"app-small"},
			wantLoserIDs:  []string{"app-big"},
		},
		{
			name: "greedy fit: three entries where the largest loses regardless of shuffle order",
			// quantity = 5; entries: A(3), B(8), C(2).
			// B(8) can never fit within quantity=5, so B always loses.
			// A(3) and C(2) together total 5 = quantity, so both always win.
			args: args{
				entries: drawEntries(
					"A", 3,
					"B", 8,
					"C", 2,
				),
				quantity: 5,
				rng:      newSeededRng(0),
			},
			wantWinnerIDs: []string{"A", "C"},
			wantLoserIDs:  []string{"B"},
		},
		{
			name: "single entry that exactly fills quantity wins",
			args: args{
				entries: drawEntries(
					"app-exact", 7,
				),
				quantity: 7,
				rng:      newSeededRng(5),
			},
			wantWinnerIDs: []string{"app-exact"},
			wantLoserIDs:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := entity.RunLotteryDraw(tt.args.entries, tt.args.quantity, tt.args.rng)

			if tt.wantWinnerIDs != nil {
				assert.ElementsMatch(t, tt.wantWinnerIDs, collectWinnerIDs(result.Winners),
					"winner IDs must match expected set")
			}
			if tt.wantLoserIDs != nil {
				assert.ElementsMatch(t, tt.wantLoserIDs, collectLoserIDs(result.Losers),
					"loser IDs must match expected set")
			}

			// Structural invariants (partition, draw position permutation, count
			// preservation) hold for every case beyond the ID-set match.
			assertDrawIntegrity(t, tt.args.entries, result)
		})
	}
}

// TestRunLotteryDraw_NoOversellAndPartitionAtScale is a property-style test: for
// many random seeds and random inputs, the draw must (a) never oversell, (b)
// partition every entry into exactly one bucket, and (c) preserve each
// winner's requested count. Entries carry DISTINCT IDs so a dropped,
// duplicated, or swapped entry is actually detectable — with a shared ID
// the property test would be blind to any per-entry defect.
func TestRunLotteryDraw_NoOversellAndPartitionAtScale(t *testing.T) {
	t.Parallel()

	// @spec components/entity/lottery-entry/run-lottery-draw "Demand above quantity"

	const (
		trials      = 500
		maxEntries  = 30
		maxCount    = 10
		maxQuantity = 100
	)

	for trial := range trials {
		seed := uint64(trial)*1_000_003 + 7 // arbitrary spread over seeds
		metaRng := rand.New(rand.NewPCG(seed, 1))

		nEntries := metaRng.IntN(maxEntries) + 1
		quantity := metaRng.IntN(maxQuantity) + 1

		entries := uniqueDrawEntries(nEntries, func(int) int { return metaRng.IntN(maxCount) + 1 })

		drawRng := newSeededRng(seed)
		result := entity.RunLotteryDraw(entries, quantity, drawRng)

		totalWon := 0
		for _, w := range result.Winners {
			totalWon += w.Entry.TicketCount
		}
		assert.LessOrEqualf(t, totalWon, quantity,
			"trial %d: oversell: total won tickets (%d) > quantity (%d)",
			trial, totalWon, quantity)

		// Partition + count preservation + draw position permutation at scale.
		assertDrawIntegrity(t, entries, result)
	}
}

// TestRunLotteryDraw_AllOrNothing verifies that each winner receives its full
// requested count — no partial allocations.
func TestRunLotteryDraw_AllOrNothing(t *testing.T) {
	t.Parallel()

	// @spec components/entity/lottery-entry/run-lottery-draw "Companion group is all-or-nothing"

	entries := drawEntries(
		"a1", 3,
		"a2", 1,
		"a3", 4,
		"a4", 2,
	)
	quantity := 6
	rng := newSeededRng(17)

	result := entity.RunLotteryDraw(entries, quantity, rng)

	// Build a map of id → requested count for fast lookup.
	countByID := make(map[entity.LotteryEntryID]int, len(entries))
	for _, a := range entries {
		countByID[a.ID] = a.TicketCount
	}

	for _, w := range result.Winners {
		expected := countByID[w.Entry.ID]
		assert.Equalf(t, expected, w.Entry.TicketCount,
			"winner %q: TicketCount mutated (want %d got %d)",
			w.Entry.ID, expected, w.Entry.TicketCount)
	}
}

// TestRunLotteryDraw_PartitionProperty verifies that every input entry
// appears exactly once across winners + losers.
func TestRunLotteryDraw_PartitionProperty(t *testing.T) {
	t.Parallel()

	type args struct {
		entries  []entity.DrawEntry
		quantity int
		rng      *rand.Rand
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "partition holds with all winners",
			args: args{
				entries:  drawEntries("a", 1, "b", 2),
				quantity: 100,
				rng:      newSeededRng(1),
			},
		},
		{
			name: "partition holds with all losers",
			args: args{
				entries:  drawEntries("a", 10),
				quantity: 5,
				rng:      newSeededRng(2),
			},
		},
		{
			name: "partition holds with mixed winners and losers",
			args: args{
				entries:  drawEntries("a", 3, "b", 8, "c", 2, "d", 4),
				quantity: 5,
				rng:      newSeededRng(3),
			},
		},
		{
			name: "partition holds with empty input",
			args: args{
				entries:  nil,
				quantity: 10,
				rng:      newSeededRng(4),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := entity.RunLotteryDraw(tt.args.entries, tt.args.quantity, tt.args.rng)

			seen := make(map[entity.LotteryEntryID]int)
			for _, w := range result.Winners {
				seen[w.Entry.ID]++
			}
			for _, l := range result.Losers {
				seen[l.Entry.ID]++
			}

			require.Len(t, seen, len(tt.args.entries),
				"number of distinct IDs in result must equal number of input entries")
			for _, a := range tt.args.entries {
				assert.Equalf(t, 1, seen[a.ID],
					"entry %q must appear exactly once across winners and losers", a.ID)
			}
		})
	}
}

// TestRunLotteryDraw_LoserOrder verifies that the losers (Losers slice)
// is sorted in ascending DrawPosition order, matching the persisted losers
// for 繰上げ promotion.
func TestRunLotteryDraw_LoserOrder(t *testing.T) {
	t.Parallel()

	// @spec components/entity/lottery-entry/run-lottery-draw "Order of the losers"

	// Use a quantity small enough that several entries must lose, so we can check
	// ordering on a non-trivial losers.
	entries := drawEntries(
		"a", 3,
		"b", 3,
		"c", 3,
		"d", 3,
		"e", 3,
	)
	quantity := 6 // admits at most 2 entries of size 3

	for seed := range 20 {
		rng := newSeededRng(uint64(seed))
		result := entity.RunLotteryDraw(entries, quantity, rng)

		seqs := make([]int, len(result.Losers))
		for i, l := range result.Losers {
			seqs[i] = l.DrawPosition
		}

		assert.IsIncreasing(t, seqs,
			"seed %d: losers DrawPosition must be strictly increasing (sorted by draw order)", seed)
	}
}

// TestRunLotteryDraw_DrawPositions verifies that DrawPosition values are
// distinct and form a valid permutation index (0 .. n-1).
func TestRunLotteryDraw_DrawPositions(t *testing.T) {
	t.Parallel()

	entries := drawEntries("a", 1, "b", 1, "c", 1, "d", 1)
	quantity := 2
	rng := newSeededRng(55)

	result := entity.RunLotteryDraw(entries, quantity, rng)

	allSeqs := make([]int, 0, len(entries))
	for _, w := range result.Winners {
		allSeqs = append(allSeqs, w.DrawPosition)
	}
	for _, l := range result.Losers {
		allSeqs = append(allSeqs, l.DrawPosition)
	}

	sort.Ints(allSeqs)
	expected := make([]int, len(entries))
	for i := range expected {
		expected[i] = i
	}
	assert.Equal(t, expected, allSeqs,
		"draw sequences across winners and losers must be a permutation of 0..n-1")
}

// TestRunLotteryDraw_Determinism verifies that the same entries, quantity,
// and rng seed always produce identical results.
func TestRunLotteryDraw_Determinism(t *testing.T) {
	t.Parallel()

	entries := drawEntries(
		"x1", 2,
		"x2", 5,
		"x3", 1,
		"x4", 3,
	)
	quantity := 7

	const runs = 10
	first := entity.RunLotteryDraw(entries, quantity, newSeededRng(1234))

	for i := range runs - 1 {
		got := entity.RunLotteryDraw(entries, quantity, newSeededRng(1234))
		assert.Equalf(t, first, got,
			"run %d: result differs from first run with same seed", i+1)
	}
}

// TestRunLotteryDraw_GreedyFitContinuesPastTooLarge explicitly constructs a
// scenario where the quantity cannot accommodate a large entry but a
// later smaller entry fits. The test asserts that the smaller entry
// wins and the larger one loses.
func TestRunLotteryDraw_GreedyFitContinuesPastTooLarge(t *testing.T) {
	t.Parallel()

	// @spec components/entity/lottery-entry/run-lottery-draw "A smaller entry fills the gap"

	// We want to test: big arrives before small in shuffle order, big is skipped,
	// small wins. To guarantee the shuffle order, we run over many seeds and check
	// that REGARDLESS of shuffle order the greedy-fit invariant holds:
	//   - if big comes first (count=10 > remaining=3): big loses, small(count=2) wins.
	//   - if small comes first (count=2 <= 3): small wins, big(count=10 > 1) loses.
	// In both orderings, small wins and big loses when quantity=3.

	entries := drawEntries(
		"big", 10,
		"small", 2,
	)
	quantity := 3

	for seed := range 50 {
		rng := newSeededRng(uint64(seed))
		result := entity.RunLotteryDraw(entries, quantity, rng)

		winnerIDs := collectWinnerIDs(result.Winners)
		loserIDs := collectLoserIDs(result.Losers)

		assert.Contains(t, winnerIDs, "small",
			"seed %d: 'small' entry (count=2) must always win when quantity=3", seed)
		assert.Contains(t, loserIDs, "big",
			"seed %d: 'big' entry (count=10) must always lose when quantity=3", seed)

		// Confirm no oversell.
		totalWon := 0
		for _, w := range result.Winners {
			totalWon += w.Entry.TicketCount
		}
		assert.LessOrEqual(t, totalWon, quantity,
			"seed %d: total won tickets must not exceed quantity", seed)
	}
}

// TestRunLotteryDraw_GreedyFitThreeApps tests the three-app greedy-fit scenario
// by pinning down the shuffle order via a controlled shuffle function rather
// than a seed hunt. Since RunLotteryDraw accepts *rand.Rand and rng.Shuffle is
// called on it, we set up inputs where ALL possible shuffle orderings produce a
// predictable winner/loser split given the greedy-fit rule.
//
// quantity=5, entries: A(3), B(4), C(2).
//
//	Any ordering where A and C are both encountered before each other's
//	admission is blocked: greedy fit always admits A(3) and C(2) because
//	3+2=5<=5, and the only way to block both is if A and C together exceed
//	quantity (they don't). B(4) can only win if it appears before BOTH A and C
//	AND remaining is still >=4 at that point. If B appears first, it wins (4<=5)
//	leaving 1 remaining, and then A(3>1) loses and C(2>1) loses. If A appears
//	first, A wins (remaining=2), B(4>2) loses, C(2<=2) wins.
//
// We iterate over enough seeds to hit both orderings and verify the invariants:
// - no oversell, - partition property, - B and (A or C) is in expected role.
func TestRunLotteryDraw_GreedyFitThreeApps(t *testing.T) {
	t.Parallel()

	entries := drawEntries("A", 3, "B", 4, "C", 2)
	quantity := 5

	for seed := range 100 {
		rng := newSeededRng(uint64(seed))
		result := entity.RunLotteryDraw(entries, quantity, rng)

		// Partition: every app appears exactly once.
		seen := make(map[entity.LotteryEntryID]bool)
		for _, w := range result.Winners {
			seen[w.Entry.ID] = true
		}
		for _, l := range result.Losers {
			seen[l.Entry.ID] = true
		}
		require.Len(t, seen, 3, "seed %d: all 3 entries must appear in result", seed)

		// No oversell.
		totalWon := 0
		for _, w := range result.Winners {
			totalWon += w.Entry.TicketCount
		}
		assert.LessOrEqualf(t, totalWon, quantity,
			"seed %d: total won (%d) must not exceed quantity (%d)", seed, totalWon, quantity)

		// Losers is ordered by ascending DrawPosition.
		seqs := make([]int, len(result.Losers))
		for i, l := range result.Losers {
			seqs[i] = l.DrawPosition
		}
		assert.IsIncreasing(t, seqs, "seed %d: losers must be sorted by DrawPosition", seed)
	}
}

// TestRunLotteryDraw_UniformFairness is the defining property of a fair lottery:
// across many random shuffles, symmetric entries must win with (statistically)
// equal probability. Ten identical single-ticket entries compete for a
// quantity of 5, so each entry wins iff it lands in the first five of the
// shuffle — probability 0.5 under a uniform shuffle. Over 20,000 deterministic
// trials each entry's win count must sit within ±5% of the expected 10,000
// (a ~7σ band: safe from flakiness, yet a biased or no-op shuffle — which would
// make the same entries win every time — falls far outside it). Every other
// test in this file is invariant to shuffle order, so THIS is the only test that
// would fail if the shuffle were removed.
func TestRunLotteryDraw_UniformFairness(t *testing.T) {
	t.Parallel()

	const (
		nEntries = 10
		quantity = 5 // exactly 5 of 10 single-ticket entries win each trial
		trials   = 20_000
	)

	entries := uniqueDrawEntries(nEntries, func(int) int { return 1 })

	wins := make(map[entity.LotteryEntryID]int, nEntries)
	for trial := range trials {
		// Deterministic per-trial seed so the test is fully reproducible.
		rng := newSeededRng(uint64(trial)*2_654_435_761 + 1)
		result := entity.RunLotteryDraw(entries, quantity, rng)

		require.Lenf(t, result.Winners, quantity,
			"trial %d: exactly %d single-ticket entries must win", trial, quantity)
		for _, w := range result.Winners {
			wins[w.Entry.ID]++
		}
	}

	expected := float64(trials) * float64(quantity) / float64(nEntries) // 10,000
	tolerance := expected * 0.05                                        // ±5%
	for _, a := range entries {
		assert.InDeltaf(t, expected, float64(wins[a.ID]), tolerance,
			"app %q won %d/%d trials; expected %.0f±%.0f — a biased or no-op shuffle would fall far outside this band",
			a.ID, wins[a.ID], trials, expected, tolerance)
	}
}

// TestRunLotteryDraw_InvalidInputsAreSafe pins the defensive contract for
// malformed input: a non-positive TicketCount is never admitted (so a
// zero cannot occupy a 0-ticket winner slot and a negative cannot inflate
// remaining quantity into an oversell), and a non-positive quantity admits no
// one. These drive the guard in RunLotteryDraw; without it a negative count
// would win and ADD to remaining, letting later entries oversell.
func TestRunLotteryDraw_InvalidInputsAreSafe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		entries  []entity.DrawEntry
		quantity int
	}{
		{
			// @spec components/entity/lottery-entry/run-lottery-draw "Non-positive request"
			name:     "zero-count entry is never admitted",
			entries:  drawEntries("zero", 0, "a", 3),
			quantity: 10,
		},
		{
			name: "negative count cannot inflate quantity into oversell",
			// Without the guard: neg(-5) 'wins' and remaining becomes 15, then
			// a(10) and b(5) both win → 15 genuine tickets > quantity 10.
			entries:  drawEntries("neg", -5, "a", 10, "b", 5),
			quantity: 10,
		},
		{
			name:     "zero quantity admits no one",
			entries:  drawEntries("a", 1, "b", 2),
			quantity: 0,
		},
		{
			name:     "negative quantity admits no one",
			entries:  drawEntries("a", 1),
			quantity: -5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := entity.RunLotteryDraw(tt.entries, tt.quantity, newSeededRng(11))

			// Every winner has a positive count (no zero/negative winners).
			totalWon := 0
			for _, w := range result.Winners {
				assert.Positivef(t, w.Entry.TicketCount,
					"winner %q must have a positive count", w.Entry.ID)
				totalWon += w.Entry.TicketCount
			}

			// No oversell even against invalid quantity (floor at 0).
			capFloor := max(tt.quantity, 0)
			assert.LessOrEqualf(t, totalWon, capFloor,
				"total won tickets (%d) must not exceed effective quantity (%d)", totalWon, capFloor)

			// Partition still holds — invalid entries become losers, not dropped.
			assertDrawIntegrity(t, tt.entries, result)
		})
	}
}

// TestRunLotteryDraw_LoserCanPrecedeWinner verifies the interleaving the fairness
// argument depends on: greedy fit can admit a later-shuffled entry while
// making an earlier-shuffled one lose, so a loser's DrawPosition can be LOWER
// than a winner's. With big(10) + small(2) and quantity 3, whenever "big" is
// shuffled before "small", big loses at the lower sequence and small wins at the
// higher one. This asserts such an ordering actually occurs (and, when it does,
// that big's sequence is below small's) — a regression that reordered admission
// to prefer low sequences would eliminate it.
func TestRunLotteryDraw_LoserCanPrecedeWinner(t *testing.T) {
	t.Parallel()

	entries := drawEntries("big", 10, "small", 2)
	quantity := 3

	foundLoserBeforeWinner := false
	for seed := range 100 {
		result := entity.RunLotteryDraw(entries, quantity, newSeededRng(uint64(seed)))

		require.Lenf(t, result.Winners, 1, "seed %d: exactly one winner (small)", seed)
		require.Lenf(t, result.Losers, 1, "seed %d: exactly one loser (big)", seed)

		winner := result.Winners[0]
		loser := result.Losers[0]
		require.Equalf(t, entity.LotteryEntryID("small"), winner.Entry.ID,
			"seed %d: 'small' must be the winner", seed)
		require.Equalf(t, entity.LotteryEntryID("big"), loser.Entry.ID,
			"seed %d: 'big' must be the loser", seed)

		if loser.DrawPosition < winner.DrawPosition {
			foundLoserBeforeWinner = true
			break
		}
	}

	assert.True(t, foundLoserBeforeWinner,
		"expected at least one shuffle where the greedy-skipped loser 'big' has a lower draw position than the winner 'small' (the loser-before-winner interleaving)")
}
