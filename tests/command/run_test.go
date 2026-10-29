package command_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/cli"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fakeops"
)

func TestRunPrintsTheInvocationAndTheNativeExitCode(t *testing.T) {
	t.Parallel()
	got := execute(t, storefront(t), nil, "run", "migrate")
	if got.exit != cli.ExitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", got.exit, got.stderr)
	}
	contains(t, "stdout", got.stdout, "invocation "+fakeops.ID(1).String()+"/s1")
	contains(t, "stdout", got.stdout, "exit code 0")
}

func TestARunWhoseJobExitsNonZeroExitsTen(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{RunFunc: func(context.Context, application.RunRequest) (application.SubmitResult, error) {
		return application.SubmitResult{}, application.ErrorForRecord(fakeops.RunRecord(2, 3))
	}}
	got := executeOps(t, storefront(t), nil, ops, "run", "migrate")
	if got.exit != cli.ExitOperationFailed {
		t.Fatalf("exit = %d, want %d\nstderr: %s", got.exit, cli.ExitOperationFailed, got.stderr)
	}
	contains(t, "stderr", got.stderr, "job.exited_nonzero")
	contains(t, "stderr", got.stderr, "exit code 3")
}

func TestRunDryRunShowsThePreviewAndRunsNothing(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{}
	got := executeOps(t, storefront(t), nil, ops, "run", "migrate", "--dry-run", "--output", "json")
	if got.exit != cli.ExitOK {
		t.Fatalf("exit = %d, want 0\nstderr: %s", got.exit, got.stderr)
	}
	var doc struct {
		DryRun     bool   `json:"dryRun"`
		Invocation string `json:"invocation"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &doc); err != nil || !doc.DryRun || doc.Invocation != "" {
		t.Fatalf("stdout = %s (%v), want a dry-run document with no invocation", got.stdout, err)
	}
	if runs := ops.Runs(); len(runs) != 1 || !runs[0].DryRun {
		t.Fatalf("runs = %+v, want one dry run", runs)
	}
}

func TestRunNeedsExactlyOneJob(t *testing.T) {
	t.Parallel()
	if got := execute(t, storefront(t), nil, "run"); got.exit != cli.ExitUsage {
		t.Fatalf("exit = %d, want usage %d", got.exit, cli.ExitUsage)
	}
}
