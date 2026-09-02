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
// draw result regardless of the shuffle: (1) partition — every input application
// appears exactly once across winners + waitlist; (2) draw_sequence values form
// a permutation of 0..n-1; (3) each winner's RequestedTicketCount is preserved
// (all-or-nothing, no mutation); (4) no winner has a non-positive count. It is
// applied to every table case so a DrawSequence/count/partition regression
// cannot hide behind an ID-only ElementsMatch assertion.
func assertDrawIntegrity(t *testing.T, input []entity.DrawApplication, result entity.DrawResult) {
	t.Helper()

	countByID := make(map[entity.TicketApplicationID]int, len(input))
	for _, a := range input {
		countByID[a.ID] = a.RequestedTicketCount
	}

	seen := make(map[entity.TicketApplicationID]int, len(input))
	seqs := make([]int, 0, len(input))
	for _, w := range result.Winners {
		seen[w.Application.ID]++
		seqs = append(seqs, w.DrawSequence)
		assert.Equalf(t, countByID[w.Application.ID], w.Application.RequestedTicketCount,
			"winner %q: RequestedTicketCount must be preserved (all-or-nothing)", w.Application.ID)
		assert.Positivef(t, w.Application.RequestedTicketCount,
			"winner %q: a non-positive request must never be admitted", w.Application.ID)
	}
	for _, l := range result.Waitlist {
		seen[l.Application.ID]++
		seqs = append(seqs, l.DrawSequence)
	}

	// Partition: exactly the input set, each once.
	require.Lenf(t, seen, len(input),
		"result must contain exactly the input applications (distinct IDs)")
	for _, a := range input {
		assert.Equalf(t, 1, seen[a.ID],
			"application %q must appear exactly once across winners and waitlist", a.ID)
	}

	// draw_sequence values are a permutation of 0..n-1.
	sort.Ints(seqs)
	expected := make([]int, len(input))
	for i := range expected {
		expected[i] = i
	}
	assert.Equal(t, expected, seqs,
		"draw sequences across winners and waitlist must be a permutation of 0..n-1")
}

// uniqueDrawApps builds n applications with distinct IDs (app-0..app-n-1) and the
// per-application RequestedTicketCount produced by count(i). Distinct IDs are
// required so partition and per-application properties are actually checkable at
// scale (a shared ID makes drops/duplicates invisible).
func uniqueDrawApps(n int, count func(i int) int) []entity.DrawApplication {
	apps := make([]entity.DrawApplication, n)
	for i := range apps {
		apps[i] = entity.DrawApplication{
			ID:                   entity.TicketApplicationID(fmt.Sprintf("app-%d", i)),
			RequestedTicketCount: count(i),
		}
	}
	return apps
}

// newSeededRng constructs a deterministic *rand.Rand from a fixed seed for use
// in table-driven tests. Using a seeded source guarantees reproducible shuffle
// orders without relying on global state.
func newSeededRng(seed uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, 0))
}

// drawApps is a helper that builds a []entity.DrawApplication slice from a
// sequence of (id, count) pairs supplied as alternating strings and ints.
func drawApps(pairs ...any) []entity.DrawApplication {
	if len(pairs)%2 != 0 {
		panic("drawApps: pairs must be even-length (id, count, id, count, ...)")
	}
	apps := make([]entity.DrawApplication, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		apps = append(apps, entity.DrawApplication{
			ID:                   entity.TicketApplicationID(pairs[i].(string)),
			RequestedTicketCount: pairs[i+1].(int),
		})
	}
	return apps
}

// collectIDs extracts the application IDs from a winner or loser slice for
// order-independent membership assertions.
func collectWinnerIDs(ws []entity.DrawWinner) []string {
	ids := make([]string, len(ws))
	for i, w := range ws {
		ids[i] = string(w.Application.ID)
	}
	return ids
}

func collectLoserIDs(ls []entity.DrawLoser) []string {
	ids := make([]string, len(ls))
	for i, l := range ls {
		ids[i] = string(l.Application.ID)
	}
	return ids
}

// TestRunLotteryDraw covers the core draw scenarios with deterministic seeds.
func TestRunLotteryDraw(t *testing.T) {
	t.Parallel()

	type args struct {
		applications   []entity.DrawApplication
		ticketCapacity int
		rng            *rand.Rand
	}
	tests := []struct {
		name          string
		args          args
		wantWinnerIDs []string // nil = skip winner ID check; set to non-nil (even empty) to assert
		wantLoserIDs  []string // nil = skip loser ID check; set to non-nil (even empty) to assert
	}{
		{
			name: "return empty result when no applications",
			args: args{
				applications:   nil,
				ticketCapacity: 100,
				rng:            newSeededRng(1),
			},
			wantWinnerIDs: []string{},
			wantLoserIDs:  []string{},
		},
		{
			name: "return empty result when applications slice is empty",
			args: args{
				applications:   []entity.DrawApplication{},
				ticketCapacity: 100,
				rng:            newSeededRng(1),
			},
			wantWinnerIDs: []string{},
			wantLoserIDs:  []string{},
		},
		{
			name: "all applications win when total demand is exactly equal to capacity",
			args: args{
				applications: drawApps(
					"app-1", 2,
					"app-2", 3,
					"app-3", 5,
				), // 2+3+5 = 10
				ticketCapacity: 10,
				rng:            newSeededRng(42),
			},
			wantWinnerIDs: []string{"app-1", "app-2", "app-3"},
			wantLoserIDs:  []string{},
		},
		{
			name: "all applications win when total demand is below capacity",
			args: args{
				applications: drawApps(
					"app-1", 1,
					"app-2", 2,
				), // 3 tickets demanded, 50 available
				ticketCapacity: 50,
				rng:            newSeededRng(7),
			},
			wantWinnerIDs: []string{"app-1", "app-2"},
			wantLoserIDs:  []string{},
		},
		{
			name: "single application larger than capacity loses and capacity stays unfilled",
			args: args{
				applications: drawApps(
					"app-big", 10,
				),
				ticketCapacity: 5,
				rng:            newSeededRng(3),
			},
			wantWinnerIDs: []string{},
			wantLoserIDs:  []string{"app-big"},
		},
		{
			name: "greedy fit: skip too-large application and admit later smaller one",
			// capacity = 3. With any shuffle order:
			//   - if app-big(5) goes first: 5 > 3 → loses; then app-small(2) → 2 <= 3 wins.
			//   - if app-small(2) goes first: 2 <= 3 wins, remaining=1; then app-big(5) → 5 > 1 loses.
			// Either way: app-small wins, app-big loses.
			args: args{
				applications: drawApps(
					"app-small", 2,
					"app-big", 5,
				),
				ticketCapacity: 3,
				rng:            newSeededRng(99),
			},
			wantWinnerIDs: []string{"app-small"},
			wantLoserIDs:  []string{"app-big"},
		},
		{
			name: "greedy fit: three apps where the largest loses regardless of shuffle order",
			// capacity = 5; apps: A(3), B(8), C(2).
			// B(8) can never fit within capacity=5, so B always loses.
			// A(3) and C(2) together total 5 = capacity, so both always win.
			args: args{
				applications: drawApps(
					"A", 3,
					"B", 8,
					"C", 2,
				),
				ticketCapacity: 5,
				rng:            newSeededRng(0),
			},
			wantWinnerIDs: []string{"A", "C"},
			wantLoserIDs:  []string{"B"},
		},
		{
			name: "single application that exactly fills capacity wins",
			args: args{
				applications: drawApps(
					"app-exact", 7,
				),
				ticketCapacity: 7,
				rng:            newSeededRng(5),
			},
			wantWinnerIDs: []string{"app-exact"},
			wantLoserIDs:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := entity.RunLotteryDraw(tt.args.applications, tt.args.ticketCapacity, tt.args.rng)

			if tt.wantWinnerIDs != nil {
				assert.ElementsMatch(t, tt.wantWinnerIDs, collectWinnerIDs(result.Winners),
					"winner IDs must match expected set")
			}
			if tt.wantLoserIDs != nil {
				assert.ElementsMatch(t, tt.wantLoserIDs, collectLoserIDs(result.Waitlist),
					"loser IDs must match expected set")
			}

			// Structural invariants (partition, draw_sequence permutation, count
			// preservation) hold for every case beyond the ID-set match.
			assertDrawIntegrity(t, tt.args.applications, result)
		})
	}
}

// TestRunLotteryDraw_NoOversellAndPartitionAtScale is a property-style test: for
// many random seeds and random inputs, the draw must (a) never oversell, (b)
// partition every application into exactly one bucket, and (c) preserve each
// winner's requested count. Applications carry DISTINCT IDs so a dropped,
// duplicated, or swapped application is actually detectable — with a shared ID
// the property test would be blind to any per-application defect.
func TestRunLotteryDraw_NoOversellAndPartitionAtScale(t *testing.T) {
	t.Parallel()

	const (
		trials      = 500
		maxApps     = 30
		maxCount    = 10
		maxCapacity = 100
	)

	for trial := range trials {
		seed := uint64(trial)*1_000_003 + 7 // arbitrary spread over seeds
		metaRng := rand.New(rand.NewPCG(seed, 1))

		nApps := metaRng.IntN(maxApps) + 1
		capacity := metaRng.IntN(maxCapacity) + 1

		apps := uniqueDrawApps(nApps, func(int) int { return metaRng.IntN(maxCount) + 1 })

		drawRng := newSeededRng(seed)
		result := entity.RunLotteryDraw(apps, capacity, drawRng)

		totalWon := 0
		for _, w := range result.Winners {
			totalWon += w.Application.RequestedTicketCount
		}
		assert.LessOrEqualf(t, totalWon, capacity,
			"trial %d: oversell: total won tickets (%d) > capacity (%d)",
			trial, totalWon, capacity)

		// Partition + count preservation + draw_sequence permutation at scale.
		assertDrawIntegrity(t, apps, result)
	}
}

// TestRunLotteryDraw_AllOrNothing verifies that each winner receives its full
// requested count — no partial allocations.
func TestRunLotteryDraw_AllOrNothing(t *testing.T) {
	t.Parallel()

	apps := drawApps(
		"a1", 3,
		"a2", 1,
		"a3", 4,
		"a4", 2,
	)
	capacity := 6
	rng := newSeededRng(17)

	result := entity.RunLotteryDraw(apps, capacity, rng)

	// Build a map of id → requested count for fast lookup.
	countByID := make(map[entity.TicketApplicationID]int, len(apps))
	for _, a := range apps {
		countByID[a.ID] = a.RequestedTicketCount
	}

	for _, w := range result.Winners {
		expected := countByID[w.Application.ID]
		assert.Equalf(t, expected, w.Application.RequestedTicketCount,
			"winner %q: RequestedTicketCount mutated (want %d got %d)",
			w.Application.ID, expected, w.Application.RequestedTicketCount)
	}
}

// TestRunLotteryDraw_PartitionProperty verifies that every input application
// appears exactly once across winners + losers.
func TestRunLotteryDraw_PartitionProperty(t *testing.T) {
	t.Parallel()

	type args struct {
		applications   []entity.DrawApplication
		ticketCapacity int
		rng            *rand.Rand
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "partition holds with all winners",
			args: args{
				applications:   drawApps("a", 1, "b", 2),
				ticketCapacity: 100,
				rng:            newSeededRng(1),
			},
		},
		{
			name: "partition holds with all losers",
			args: args{
				applications:   drawApps("a", 10),
				ticketCapacity: 5,
				rng:            newSeededRng(2),
			},
		},
		{
			name: "partition holds with mixed winners and losers",
			args: args{
				applications:   drawApps("a", 3, "b", 8, "c", 2, "d", 4),
				ticketCapacity: 5,
				rng:            newSeededRng(3),
			},
		},
		{
			name: "partition holds with empty input",
			args: args{
				applications:   nil,
				ticketCapacity: 10,
				rng:            newSeededRng(4),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := entity.RunLotteryDraw(tt.args.applications, tt.args.ticketCapacity, tt.args.rng)

			seen := make(map[entity.TicketApplicationID]int)
			for _, w := range result.Winners {
				seen[w.Application.ID]++
			}
			for _, l := range result.Waitlist {
				seen[l.Application.ID]++
			}

			require.Len(t, seen, len(tt.args.applications),
				"number of distinct IDs in result must equal number of input applications")
			for _, a := range tt.args.applications {
				assert.Equalf(t, 1, seen[a.ID],
					"application %q must appear exactly once across winners and waitlist", a.ID)
			}
		})
	}
}

// TestRunLotteryDraw_WaitlistOrder verifies that the waitlist (Waitlist slice)
// is sorted in ascending DrawSequence order, matching the persisted waitlist
// for 繰上げ promotion.
func TestRunLotteryDraw_WaitlistOrder(t *testing.T) {
	t.Parallel()

	// Use a capacity small enough that several apps must lose, so we can check
	// ordering on a non-trivial waitlist.
	apps := drawApps(
		"a", 3,
		"b", 3,
		"c", 3,
		"d", 3,
		"e", 3,
	)
	capacity := 6 // admits at most 2 apps of size 3

	for seed := range 20 {
		rng := newSeededRng(uint64(seed))
		result := entity.RunLotteryDraw(apps, capacity, rng)

		seqs := make([]int, len(result.Waitlist))
		for i, l := range result.Waitlist {
			seqs[i] = l.DrawSequence
		}

		assert.IsIncreasing(t, seqs,
			"seed %d: waitlist DrawSequence must be strictly increasing (sorted by draw order)", seed)
	}
}

// TestRunLotteryDraw_DrawSequences verifies that DrawSequence values are
// distinct and form a valid permutation index (0 .. n-1).
func TestRunLotteryDraw_DrawSequences(t *testing.T) {
	t.Parallel()

	apps := drawApps("a", 1, "b", 1, "c", 1, "d", 1)
	capacity := 2
	rng := newSeededRng(55)

	result := entity.RunLotteryDraw(apps, capacity, rng)

	allSeqs := make([]int, 0, len(apps))
	for _, w := range result.Winners {
		allSeqs = append(allSeqs, w.DrawSequence)
	}
	for _, l := range result.Waitlist {
		allSeqs = append(allSeqs, l.DrawSequence)
	}

	sort.Ints(allSeqs)
	expected := make([]int, len(apps))
	for i := range expected {
		expected[i] = i
	}
	assert.Equal(t, expected, allSeqs,
		"draw sequences across winners and losers must be a permutation of 0..n-1")
}

// TestRunLotteryDraw_Determinism verifies that the same applications, capacity,
// and rng seed always produce identical results.
func TestRunLotteryDraw_Determinism(t *testing.T) {
	t.Parallel()

	apps := drawApps(
		"x1", 2,
		"x2", 5,
		"x3", 1,
		"x4", 3,
	)
	capacity := 7

	const runs = 10
	first := entity.RunLotteryDraw(apps, capacity, newSeededRng(1234))

	for i := range runs - 1 {
		got := entity.RunLotteryDraw(apps, capacity, newSeededRng(1234))
		assert.Equalf(t, first, got,
			"run %d: result differs from first run with same seed", i+1)
	}
}

// TestRunLotteryDraw_GreedyFitContinuesPastTooLarge explicitly constructs a
// scenario where the capacity cannot accommodate a large application but a
// later smaller application fits. The test asserts that the smaller application
// wins and the larger one is waitlisted.
func TestRunLotteryDraw_GreedyFitContinuesPastTooLarge(t *testing.T) {
	t.Parallel()

	// We want to test: big arrives before small in shuffle order, big is skipped,
	// small wins. To guarantee the shuffle order, we run over many seeds and check
	// that REGARDLESS of shuffle order the greedy-fit invariant holds:
	//   - if big comes first (count=10 > remaining=3): big loses, small(count=2) wins.
	//   - if small comes first (count=2 <= 3): small wins, big(count=10 > 1) loses.
	// In both orderings, small wins and big loses when capacity=3.

	apps := drawApps(
		"big", 10,
		"small", 2,
	)
	capacity := 3

	for seed := range 50 {
		rng := newSeededRng(uint64(seed))
		result := entity.RunLotteryDraw(apps, capacity, rng)

		winnerIDs := collectWinnerIDs(result.Winners)
		loserIDs := collectLoserIDs(result.Waitlist)

		assert.Contains(t, winnerIDs, "small",
			"seed %d: 'small' application (count=2) must always win when capacity=3", seed)
		assert.Contains(t, loserIDs, "big",
			"seed %d: 'big' application (count=10) must always lose when capacity=3", seed)

		// Confirm no oversell.
		totalWon := 0
		for _, w := range result.Winners {
			totalWon += w.Application.RequestedTicketCount
		}
		assert.LessOrEqual(t, totalWon, capacity,
			"seed %d: total won tickets must not exceed capacity", seed)
	}
}

// TestRunLotteryDraw_GreedyFitThreeApps tests the three-app greedy-fit scenario
// by pinning down the shuffle order via a controlled shuffle function rather
// than a seed hunt. Since RunLotteryDraw accepts *rand.Rand and rng.Shuffle is
// called on it, we set up inputs where ALL possible shuffle orderings produce a
// predictable winner/loser split given the greedy-fit rule.
//
// capacity=5, apps: A(3), B(4), C(2).
//
//	Any ordering where A and C are both encountered before each other's
//	admission is blocked: greedy fit always admits A(3) and C(2) because
//	3+2=5<=5, and the only way to block both is if A and C together exceed
//	capacity (they don't). B(4) can only win if it appears before BOTH A and C
//	AND remaining is still >=4 at that point. If B appears first, it wins (4<=5)
//	leaving 1 remaining, and then A(3>1) loses and C(2>1) loses. If A appears
//	first, A wins (remaining=2), B(4>2) loses, C(2<=2) wins.
//
// We iterate over enough seeds to hit both orderings and verify the invariants:
// - no oversell, - partition property, - B and (A or C) is in expected role.
func TestRunLotteryDraw_GreedyFitThreeApps(t *testing.T) {
	t.Parallel()

	apps := drawApps("A", 3, "B", 4, "C", 2)
	capacity := 5

	for seed := range 100 {
		rng := newSeededRng(uint64(seed))
		result := entity.RunLotteryDraw(apps, capacity, rng)

		// Partition: every app appears exactly once.
		seen := make(map[entity.TicketApplicationID]bool)
		for _, w := range result.Winners {
			seen[w.Application.ID] = true
		}
		for _, l := range result.Waitlist {
			seen[l.Application.ID] = true
		}
		require.Len(t, seen, 3, "seed %d: all 3 apps must appear in result", seed)

		// No oversell.
		totalWon := 0
		for _, w := range result.Winners {
			totalWon += w.Application.RequestedTicketCount
		}
		assert.LessOrEqualf(t, totalWon, capacity,
			"seed %d: total won (%d) must not exceed capacity (%d)", seed, totalWon, capacity)

		// Waitlist is ordered by ascending DrawSequence.
		seqs := make([]int, len(result.Waitlist))
		for i, l := range result.Waitlist {
			seqs[i] = l.DrawSequence
		}
		assert.IsIncreasing(t, seqs, "seed %d: waitlist must be sorted by DrawSequence", seed)
	}
}

// TestRunLotteryDraw_UniformFairness is the defining property of a fair lottery:
// across many random shuffles, symmetric applicants must win with (statistically)
// equal probability. Ten identical single-ticket applications compete for a
// capacity of 5, so each application wins iff it lands in the first five of the
// shuffle — probability 0.5 under a uniform shuffle. Over 20,000 deterministic
// trials each application's win count must sit within ±5% of the expected 10,000
// (a ~7σ band: safe from flakiness, yet a biased or no-op shuffle — which would
// make the same applicants win every time — falls far outside it). Every other
// test in this file is invariant to shuffle order, so THIS is the only test that
// would fail if the shuffle were removed.
func TestRunLotteryDraw_UniformFairness(t *testing.T) {
	t.Parallel()

	const (
		nApps    = 10
		capacity = 5 // exactly 5 of 10 single-ticket apps win each trial
		trials   = 20_000
	)

	apps := uniqueDrawApps(nApps, func(int) int { return 1 })

	wins := make(map[entity.TicketApplicationID]int, nApps)
	for trial := range trials {
		// Deterministic per-trial seed so the test is fully reproducible.
		rng := newSeededRng(uint64(trial)*2_654_435_761 + 1)
		result := entity.RunLotteryDraw(apps, capacity, rng)

		require.Lenf(t, result.Winners, capacity,
			"trial %d: exactly %d single-ticket apps must win", trial, capacity)
		for _, w := range result.Winners {
			wins[w.Application.ID]++
		}
	}

	expected := float64(trials) * float64(capacity) / float64(nApps) // 10,000
	tolerance := expected * 0.05                                     // ±5%
	for _, a := range apps {
		assert.InDeltaf(t, expected, float64(wins[a.ID]), tolerance,
			"app %q won %d/%d trials; expected %.0f±%.0f — a biased or no-op shuffle would fall far outside this band",
			a.ID, wins[a.ID], trials, expected, tolerance)
	}
}

// TestRunLotteryDraw_InvalidInputsAreSafe pins the defensive contract for
// malformed input: a non-positive RequestedTicketCount is never admitted (so a
// zero cannot occupy a 0-ticket winner slot and a negative cannot inflate
// remaining capacity into an oversell), and a non-positive capacity admits no
// one. These drive the guard in RunLotteryDraw; without it a negative count
// would win and ADD to remaining, letting later applications oversell.
func TestRunLotteryDraw_InvalidInputsAreSafe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		applications   []entity.DrawApplication
		ticketCapacity int
	}{
		{
			name:           "zero-count application is never admitted",
			applications:   drawApps("zero", 0, "a", 3),
			ticketCapacity: 10,
		},
		{
			name: "negative count cannot inflate capacity into oversell",
			// Without the guard: neg(-5) 'wins' and remaining becomes 15, then
			// a(10) and b(5) both win → 15 genuine tickets > capacity 10.
			applications:   drawApps("neg", -5, "a", 10, "b", 5),
			ticketCapacity: 10,
		},
		{
			name:           "zero capacity admits no one",
			applications:   drawApps("a", 1, "b", 2),
			ticketCapacity: 0,
		},
		{
			name:           "negative capacity admits no one",
			applications:   drawApps("a", 1),
			ticketCapacity: -5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := entity.RunLotteryDraw(tt.applications, tt.ticketCapacity, newSeededRng(11))

			// Every winner has a positive count (no zero/negative winners).
			totalWon := 0
			for _, w := range result.Winners {
				assert.Positivef(t, w.Application.RequestedTicketCount,
					"winner %q must have a positive count", w.Application.ID)
				totalWon += w.Application.RequestedTicketCount
			}

			// No oversell even against invalid capacity (floor at 0).
			capFloor := max(tt.ticketCapacity, 0)
			assert.LessOrEqualf(t, totalWon, capFloor,
				"total won tickets (%d) must not exceed effective capacity (%d)", totalWon, capFloor)

			// Partition still holds — invalid apps become waitlisted losers, not dropped.
			assertDrawIntegrity(t, tt.applications, result)
		})
	}
}

// TestRunLotteryDraw_LoserCanPrecedeWinner verifies the interleaving the fairness
// argument depends on: greedy fit can admit a later-shuffled application while
// waitlisting an earlier-shuffled one, so a loser's DrawSequence can be LOWER
// than a winner's. With big(10) + small(2) and capacity 3, whenever "big" is
// shuffled before "small", big loses at the lower sequence and small wins at the
// higher one. This asserts such an ordering actually occurs (and, when it does,
// that big's sequence is below small's) — a regression that reordered admission
// to prefer low sequences would eliminate it.
func TestRunLotteryDraw_LoserCanPrecedeWinner(t *testing.T) {
	t.Parallel()

	apps := drawApps("big", 10, "small", 2)
	capacity := 3

	foundLoserBeforeWinner := false
	for seed := range 100 {
		result := entity.RunLotteryDraw(apps, capacity, newSeededRng(uint64(seed)))

		require.Lenf(t, result.Winners, 1, "seed %d: exactly one winner (small)", seed)
		require.Lenf(t, result.Waitlist, 1, "seed %d: exactly one loser (big)", seed)

		winner := result.Winners[0]
		loser := result.Waitlist[0]
		require.Equalf(t, entity.TicketApplicationID("small"), winner.Application.ID,
			"seed %d: 'small' must be the winner", seed)
		require.Equalf(t, entity.TicketApplicationID("big"), loser.Application.ID,
			"seed %d: 'big' must be the loser", seed)

		if loser.DrawSequence < winner.DrawSequence {
			foundLoserBeforeWinner = true
			break
		}
	}

	assert.True(t, foundLoserBeforeWinner,
		"expected at least one shuffle where the greedy-skipped loser 'big' has a lower draw_sequence than the winner 'small' (the loser-before-winner interleaving)")
}
