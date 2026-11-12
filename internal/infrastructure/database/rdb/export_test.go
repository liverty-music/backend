package rdb

import "github.com/liverty-music/backend/internal/entity"

// WaitForDatabase exposes waitForDatabase for testing with a fake clock.
var WaitForDatabase = waitForDatabase

// StartupBudget exposes startupBudget for testing.
const StartupBudget = startupBudget

// LockAndCheckEventRemoval and RemoveEvents expose the shared event removal
// steps so a test can run another transaction between them.
var (
	LockAndCheckEventRemoval = lockAndCheckEventRemoval
	RemoveEvents             = removeEvents
)

// ApplyTransitionOfLotteryEntry exposes applyTransition for the entry state.
var ApplyTransitionOfLotteryEntry = applyTransition[entity.LotteryEntryState]

// The publish-state SQL fragments, exposed for their parity tests.
const (
	FanVisibleCondition      = fanVisibleCondition
	CatalogEventCondition    = catalogEventCondition
	SeriesPublishStateColumn = seriesPublishStateColumn
)
