package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/internal/infrastructure/workflow"
	"github.com/liverty-music/backend/pkg/shutdown"
)

const workflowUsage = `usage: worker workflow <command> [arguments]

Runs in the workflow worker's Pod, as its database identity:
  kubectl exec deploy/workflow-worker -n backend -- /worker workflow <command>

commands:
  list [-status STATUS,...] [-limit N]   workflows, newest first (statuses: PENDING, ENQUEUED,
                                         DELAYED, SUCCESS, ERROR, CANCELLED, MAX_RECOVERY_ATTEMPTS_EXCEEDED)
  steps ID                               the recorded steps of a workflow, with outputs and errors
  resume ID                              run a workflow again from its last completed step
  fork ID -step N                        start a new workflow that reuses the steps before N
  delete ID                              delete a workflow an admin resolved by hand

See cloud-provisioning docs/runbooks/workflow-needs-admin.md.
`

// commandTimeout bounds one admin command, including the wait for the
// database.
const commandTimeout = 2 * time.Minute

// runWorkflowCommand runs one `workflow` subcommand, prints its result as
// JSON to stdout and returns the process exit code.
func runWorkflowCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, workflowUsage)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	run, err := parseWorkflowCommand(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n\n%s", err, workflowUsage)
		return 2
	}

	client, _, err := di.InitializeWorkflowAdmin(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "connect to the workflow tables: %v\n", err)
		return 1
	}
	defer func() { _ = shutdown.Shutdown(context.Background()) }()

	result, err := run(ctx, client)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		_, _ = fmt.Fprintf(stderr, "print the result: %v\n", err)
		return 1
	}
	return 0
}

// workflowCommand runs a parsed subcommand against the client.
type workflowCommand func(context.Context, *workflow.Client) (any, error)

// parseWorkflowCommand parses args before anything connects, so a typo fails
// at once.
func parseWorkflowCommand(args []string) (workflowCommand, error) {
	name, rest := args[0], args[1:]
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	switch name {
	case "list":
		status := fs.String("status", "", "comma-separated statuses")
		limit := fs.Int("limit", 50, "maximum number of workflows")
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		var statuses []string
		if *status != "" {
			statuses = strings.Split(*status, ",")
		}
		return func(ctx context.Context, c *workflow.Client) (any, error) {
			return c.List(ctx, *limit, statuses...)
		}, nil
	case "steps", "resume", "delete":
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		id, err := oneID(fs)
		if err != nil {
			return nil, err
		}
		switch name {
		case "steps":
			return func(ctx context.Context, c *workflow.Client) (any, error) { return c.Steps(ctx, id) }, nil
		case "resume":
			return func(ctx context.Context, c *workflow.Client) (any, error) {
				return map[string]string{"resumed": id}, c.Resume(ctx, id)
			}, nil
		default:
			return func(ctx context.Context, c *workflow.Client) (any, error) {
				return map[string]string{"deleted": id}, c.Delete(ctx, id)
			}, nil
		}
	case "fork":
		step := fs.Int("step", -1, "the first step the fork runs again")
		// The id comes first (fork ID -step N); flag parsing stops at it.
		if len(rest) == 0 {
			return nil, fmt.Errorf("fork needs a workflow id")
		}
		id := rest[0]
		if err := fs.Parse(rest[1:]); err != nil {
			return nil, err
		}
		if *step < 0 {
			return nil, fmt.Errorf("fork needs -step N, the first step to run again (see `steps %s`)", id)
		}
		return func(ctx context.Context, c *workflow.Client) (any, error) {
			forked, err := c.Fork(ctx, id, uint(*step))
			return map[string]string{"forked_from": id, "workflow_id": forked}, err
		}, nil
	default:
		return nil, fmt.Errorf("unknown command %q", name)
	}
}

// oneID returns the single workflow id argument.
func oneID(fs *flag.FlagSet) (string, error) {
	if fs.NArg() != 1 {
		return "", fmt.Errorf("%s needs one workflow id", fs.Name())
	}
	return fs.Arg(0), nil
}
