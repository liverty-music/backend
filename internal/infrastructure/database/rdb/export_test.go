package rdb

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
