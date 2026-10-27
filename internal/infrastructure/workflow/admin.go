package workflow

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// LogKeyWorkflowNeedsAdmin is the log message of a workflow that needs an
// admin, logged once for each such workflow the report sees, with its
// workflow_id, workflow_name and status. The runbook
// (cloud-provisioning docs/runbooks/workflow-needs-admin.md) searches for it.
const LogKeyWorkflowNeedsAdmin = "workflow needs an admin"

// needsAdminStatuses are the ends of a workflow nobody planned for: it ended
// with an error, its process stopped during it too often, or it passed its
// deadline or was cancelled. A workflow in one of them stays until an admin
// resumes, forks or deletes it.
var needsAdminStatuses = []dbos.WorkflowStatusType{
	dbos.WorkflowStatusError,
	dbos.WorkflowStatusMaxRecoveryAttemptsExceeded,
	dbos.WorkflowStatusCancelled,
}

const (
	// ReportScheduleName runs reportNeedsAdmin every 5 minutes, so a workflow
	// that needs an admin is reported within 5 minutes. DBOS cron expressions
	// carry a seconds field.
	ReportScheduleName = "report-workflows-needing-admin"
	reportSchedule     = "0 */5 * * * *"

	// RetentionScheduleName runs deleteOld at 03:00 Asia/Tokyo every day.
	RetentionScheduleName = "delete-old-workflows"
	retentionSchedule     = "0 0 3 * * *"
	retentionTimezone     = "Asia/Tokyo"

	// retention is how long a workflow that ended successfully is kept.
	retention = 30 * 24 * time.Hour

	needsAdminGauge = "workflow.needs_admin"
)

// gaugeKey is one series of the workflow.needs_admin gauge.
type gaugeKey struct {
	name   string
	status string
}

// admin runs the worker's two schedules: the report of workflows that need an
// admin, which feeds the workflow.needs_admin gauge, and the deletion of old
// successful workflows. Its state is the gauge's last values and the workflows
// already logged; both start empty when the worker does.
type admin struct {
	logger *logging.Logger

	mu     sync.Mutex
	now    func() time.Time
	counts map[gaugeKey]int64
	logged map[string]bool
}

func newAdmin(logger *logging.Logger) (*admin, error) {
	a := &admin{
		logger: logger,
		now:    time.Now,
		counts: map[gaugeKey]int64{},
		logged: map[string]bool{},
	}
	_, err := otel.Meter(tracerName).Int64ObservableGauge(needsAdminGauge,
		metric.WithDescription("Workflows that ended in a state an admin must resolve, by workflow name and status"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			a.mu.Lock()
			defer a.mu.Unlock()
			for k, v := range a.counts {
				o.Observe(v, metric.WithAttributes(
					attribute.String("workflow_name", k.name),
					attribute.String("status", k.status),
				))
			}
			return nil
		}),
	)
	if err != nil {
		return nil, toAppErr(err, "register the "+needsAdminGauge+" gauge")
	}
	return a, nil
}

// register registers the two schedule workflows and their schedules. They
// are plain functions, not usecase workflows: DBOS schedules address a
// workflow by its plain name.
func (a *admin) register(r *Runtime) error {
	dbos.RegisterWorkflow(r.ctx, a.reportNeedsAdmin, dbos.WithWorkflowName(ReportScheduleName))
	dbos.RegisterWorkflow(r.ctx, a.deleteOld, dbos.WithWorkflowName(RetentionScheduleName))
	err := dbos.ApplySchedules(r.ctx, []dbos.ScheduleSpec{
		{ScheduleName: ReportScheduleName, Schedule: reportSchedule, WorkflowName: ReportScheduleName},
		{ScheduleName: RetentionScheduleName, Schedule: retentionSchedule, CronTimezone: retentionTimezone, WorkflowName: RetentionScheduleName},
	})
	return toAppErr(err, "apply the workflow schedules")
}

// reportNeedsAdmin counts the workflows that need an admin into the gauge,
// which stays above 0 while one exists, and logs each such workflow once.
func (a *admin) reportNeedsAdmin(ctx dbos.Context, _ dbos.ScheduledWorkflowInput) (int, error) {
	return dbos.RunAsStep(ctx, func(ctx context.Context) (int, error) {
		statuses, err := dbos.ListWorkflows(ctx.(dbos.Context),
			dbos.WithFilterStatus(needsAdminStatuses...),
			dbos.WithFilterLoadInput(false),
			dbos.WithFilterLoadOutput(false),
		)
		if err != nil {
			return 0, toAppErr(err, "list the workflows that need an admin")
		}

		a.mu.Lock()
		defer a.mu.Unlock()
		// A series that had workflows and has none now reports 0, so the alert
		// closes.
		for k := range a.counts {
			a.counts[k] = 0
		}
		for _, s := range statuses {
			a.counts[gaugeKey{name: s.Name, status: string(s.Status)}]++
			if a.logged[s.ID] {
				continue
			}
			a.logged[s.ID] = true
			a.logger.Error(ctx, LogKeyWorkflowNeedsAdmin, s.Error,
				slog.String("workflow_id", s.ID),
				slog.String("workflow_name", s.Name),
				slog.String("status", string(s.Status)),
			)
		}
		return len(statuses), nil
	}, dbos.WithStepName("report"))
}

// deleteOld deletes the workflows that ended successfully and were created
// more than retention ago. A workflow that has not ended or needs an admin is
// kept whatever its age. The list is a step, so a resumed run deletes the same
// workflows; the delete is checkpointed by DBOS itself, which refuses it
// inside a step.
func (a *admin) deleteOld(ctx dbos.Context, _ dbos.ScheduledWorkflowInput) (int, error) {
	ids, err := dbos.RunAsStep(ctx, func(ctx context.Context) ([]string, error) {
		a.mu.Lock()
		now := a.now()
		a.mu.Unlock()
		statuses, err := dbos.ListWorkflows(ctx.(dbos.Context),
			dbos.WithFilterStatus(dbos.WorkflowStatusSuccess),
			dbos.WithFilterCreatedBefore(now.Add(-retention)),
			dbos.WithFilterLoadInput(false),
			dbos.WithFilterLoadOutput(false),
		)
		if err != nil {
			return nil, toAppErr(err, "list the old successful workflows")
		}
		ids := make([]string, len(statuses))
		for i, s := range statuses {
			ids[i] = s.ID
		}
		return ids, nil
	}, dbos.WithStepName("list"))
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	if err := dbos.DeleteWorkflows(ctx, ids); err != nil {
		return 0, toAppErr(err, "delete the old successful workflows")
	}
	a.logger.Info(ctx, "deleted old successful workflows", slog.Int("count", len(ids)))
	return len(ids), nil
}
