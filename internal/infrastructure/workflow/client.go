package workflow

import (
	"context"
	"encoding/json"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

var _ usecase.Workflows = (*Client)(nil)

// Client starts, signals and awaits workflows without running them. API
// servers, event consumers and jobs use it; it never calls Launch, so no
// process but the worker recovers or runs a workflow, and cancelling a request
// context can never cancel a workflow.
type Client struct {
	c    dbos.Client
	pool *pgxpool.Pool
}

// NewClient connects a DBOS client to the system tables over pool, the
// process's own database pool.
func NewClient(ctx context.Context, pool *pgxpool.Pool, logger *logging.Logger) (*Client, error) {
	c, err := dbos.NewClient(ctx, dbos.ClientConfig{
		AppName:                AppName,
		SystemDBPool:           pool,
		DatabaseSchema:         Schema,
		Logger:                 logger.Slog(),
		SystemDBStartupTimeout: startupTimeout,
	})
	if err != nil {
		return nil, toAppErr(err, "connect the workflow client")
	}
	return &Client{c: c, pool: pool}, nil
}

// Close releases the client and closes the database pool NewClient was given
// (DBOS closes the system database pool even when it did not create it), so
// it belongs in a shutdown phase before the database's.
func (c *Client) Close() error {
	return toAppErr(dbos.Shutdown(c.c, 10*time.Second), "close the workflow client")
}

// Start enqueues the named workflow once per id.
func (c *Client) Start(ctx context.Context, workflow, id string, input any, opts ...usecase.StartOption) error {
	return start(c.c, ctx, workflow, id, input, opts...)
}

// start enqueues the named workflow on c, a client or a running workflow
// (where the enqueue is checkpointed as a step). Starting an id that exists
// returns without a second run (DBOS's default id-reuse policy). traceCtx
// carries the caller's trace context into the workflow's attributes.
func start(c dbos.Client, traceCtx context.Context, workflow, id string, input any, opts ...usecase.StartOption) error {
	o := usecase.NewStartOptions(opts...)
	queue := o.Queue
	if queue == "" {
		queue = DefaultQueue
	}
	enqueueOpts := []dbos.EnqueueOption{
		dbos.WithEnqueueWorkflowID(id),
		dbos.WithEnqueueApplicationVersion(ApplicationVersion),
		// Usecase workflows are registered as instances named after the workflow.
		dbos.WithEnqueueConfigName(workflow),
	}
	if attrs := traceAttributes(traceCtx); attrs != nil {
		enqueueOpts = append(enqueueOpts, dbos.WithEnqueueAttributes(attrs))
	}
	if o.Delay > 0 {
		enqueueOpts = append(enqueueOpts, dbos.WithEnqueueDelay(o.Delay))
	}
	if !o.Deadline.IsZero() {
		// DBOS counts an enqueue timeout from the dequeue, which follows the
		// delay; the queue picks a workflow up within a second, so the stored
		// deadline lands at most that much after the one asked for.
		timeout := time.Until(o.Deadline) - o.Delay
		if timeout <= 0 {
			return apperr.New(codes.InvalidArgument, "workflow deadline already passed")
		}
		enqueueOpts = append(enqueueOpts, dbos.WithEnqueueTimeout(timeout))
	}
	_, err := dbos.Enqueue[any, any](c, queue, workflow, input, enqueueOpts...)
	return toAppErr(err, "start workflow "+workflow)
}

// Send sends message, JSON-encoded, to the workflow id under topic.
func (c *Client) Send(ctx context.Context, id, topic string, message any) error {
	raw, err := json.Marshal(message)
	if err != nil {
		return apperr.Wrap(err, codes.InvalidArgument, "encode the workflow message")
	}
	return toAppErr(dbos.Send(c.c, id, raw, topic), "send to workflow "+id)
}

// Await waits up to timeout for the workflow id to end and decodes its result
// into out.
func (c *Client) Await(ctx context.Context, id string, timeout time.Duration, out any) error {
	handle, err := dbos.RetrieveWorkflow[json.RawMessage](c.c, id)
	if err != nil {
		return toAppErr(err, "find workflow "+id)
	}
	raw, err := handle.GetResult(dbos.WithHandleTimeout(timeout))
	if err != nil {
		return toAppErr(err, "await workflow "+id)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return apperr.Wrap(err, codes.Internal, "decode the result of workflow "+id)
	}
	return nil
}

// cancelNotStartedQuery deletes a workflow that is still waiting to start, in
// one statement, so a queue runner that dequeues it at the same moment either
// sees it gone or has already moved it to PENDING. DBOS's CancelWorkflow also
// stops running workflows and leaves them CANCELLED, which the admin report
// would page about; a workflow that never started has nothing to report.
const cancelNotStartedQuery = `DELETE FROM dbos.workflow_status
WHERE workflow_uuid = $1 AND status IN ('ENQUEUED', 'DELAYED')`

// Cancel deletes the workflow id if it has not started yet and reports
// whether it did. Starting the id again afterwards starts a new run.
func (c *Client) Cancel(ctx context.Context, id string) (bool, error) {
	tag, err := c.pool.Exec(ctx, cancelNotStartedQuery, id)
	if err != nil {
		return false, apperr.Wrap(err, codes.Internal, "cancel workflow "+id)
	}
	return tag.RowsAffected() == 1, nil
}

// List returns the workflows in the given statuses (all when none), newest
// first, at most limit of them.
func (c *Client) List(ctx context.Context, limit int, statuses ...string) ([]dbos.WorkflowStatus, error) {
	opts := []dbos.ListWorkflowsOption{
		dbos.WithFilterSortDesc(),
		dbos.WithFilterLimit(limit),
		dbos.WithFilterLoadInput(false),
		dbos.WithFilterLoadOutput(false),
	}
	if len(statuses) > 0 {
		types := make([]dbos.WorkflowStatusType, len(statuses))
		for i, s := range statuses {
			types[i] = dbos.WorkflowStatusType(s)
		}
		opts = append(opts, dbos.WithFilterStatus(types...))
	}
	list, err := dbos.ListWorkflows(c.c, opts...)
	return list, toAppErr(err, "list workflows")
}

// Steps returns the recorded steps of the workflow id, with their outputs and
// errors.
func (c *Client) Steps(ctx context.Context, id string) ([]dbos.StepInfo, error) {
	steps, err := dbos.GetWorkflowSteps(c.c, id)
	return steps, toAppErr(err, "list the steps of workflow "+id)
}

// Resume runs the workflow id again from its last completed step, under the
// same id, once its cause is fixed.
func (c *Client) Resume(ctx context.Context, id string) error {
	_, err := dbos.ResumeWorkflow[json.RawMessage](c.c, id)
	return toAppErr(err, "resume workflow "+id)
}

// Fork starts a new workflow from the steps of id before startStep, which it
// runs again, and returns the new workflow's id.
func (c *Client) Fork(ctx context.Context, id string, startStep uint) (string, error) {
	handle, err := dbos.ForkWorkflow[json.RawMessage](c.c, dbos.ForkWorkflowInput{
		OriginalWorkflowID: id,
		StartStep:          startStep,
		ApplicationVersion: ApplicationVersion,
	})
	if err != nil {
		return "", toAppErr(err, "fork workflow "+id)
	}
	return handle.GetWorkflowID(), nil
}

// Delete deletes the record of the workflow id, after an admin handled it by
// hand.
func (c *Client) Delete(ctx context.Context, id string) error {
	return toAppErr(dbos.DeleteWorkflows(c.c, []string{id}), "delete workflow "+id)
}

// TriggerSchedule runs the named schedule now instead of at its next tick.
func (c *Client) TriggerSchedule(ctx context.Context, name string) (string, error) {
	handle, err := dbos.TriggerSchedule[json.RawMessage](c.c, name)
	if err != nil {
		return "", toAppErr(err, "trigger schedule "+name)
	}
	return handle.GetWorkflowID(), nil
}
