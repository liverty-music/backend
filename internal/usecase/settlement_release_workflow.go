package usecase

import (
	"context"
	"time"
)

// WorkflowReleaseDueSettlements is the stable name of the scheduled workflow
// that pays out every held Settlement whose release gate has passed.
const WorkflowReleaseDueSettlements = "release-due-settlements"

// ReleaseDueSettlementsSchedule runs [WorkflowReleaseDueSettlements] at second
// 0 of every fifth minute (a cron expression with seconds).
const ReleaseDueSettlementsSchedule = "0 */5 * * * *"

// ReleaseDueSettlementsWorkflow returns the workflow method the worker
// schedules as [WorkflowReleaseDueSettlements]: it runs
// [SettlementUseCase.ReleaseDueSettlements] as one step.
//
// The sweep reads the held Settlements and calls Stripe, so it runs inside a
// step, never in the workflow body. Running it again is safe: a released
// Settlement is skipped by the status guard of MarkReleased, and a Stripe
// Transfer carries an idempotency key made of the Settlement and the payee, so
// two overlapping runs never pay the same split twice. A failure of the work
// list is returned as it is, which ends the run and reports it to an admin; the
// next tick sweeps again.
//
// It replaces the sweeper that ran in every fan API process (OpenSpec change
// add-prod-preview-release, design D10): the worker is the only process that
// runs schedules.
func ReleaseDueSettlementsWorkflow(uc SettlementUseCase) func(context.Context, Flow, time.Time) (struct{}, error) {
	return func(ctx context.Context, f Flow, _ time.Time) (struct{}, error) {
		return Step(ctx, f, "release due settlements", func(ctx context.Context) (struct{}, error) {
			return struct{}{}, uc.ReleaseDueSettlements(ctx)
		})
	}
}
