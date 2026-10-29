package application_test

import (
	"context"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fakeops"
)

func runIn(t *testing.T, ops *fakeops.Operations) application.RunJob {
	t.Helper()
	return application.NewRunJob(source(storefront(t)), nil, ops, nil)
}

func TestTwoRunsAreTwoInvocationsWithTheirOwnIDs(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{}
	run := runIn(t, ops)
	first, err := run(context.Background(), application.RunJobRequest{Job: "migrate"})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	second, err := run(context.Background(), application.RunJobRequest{Job: "workload/migrate"})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if first.Invocation == "" || first.Invocation == second.Invocation {
		t.Fatalf("invocations = %q and %q, want two distinct IDs", first.Invocation, second.Invocation)
	}
	if first.Result == nil || first.Result.ExitCode != 0 {
		t.Fatalf("result = %+v, want the job's native exit code 0", first.Result)
	}
	if got := ops.Runs()[0].Job.String(); got != "workload/migrate" {
		t.Fatalf("job = %s, want workload/migrate", got)
	}
}

func TestANonZeroJobExitIsAnOperationFailureThatKeepsTheRecord(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{RunFunc: func(context.Context, application.RunRequest) (application.SubmitResult, error) {
		return application.SubmitResult{}, application.ErrorForRecord(fakeops.RunRecord(4, 3))
	}}
	_, err := runIn(t, ops)(context.Background(), application.RunJobRequest{Job: "migrate"})
	appErr := wantError(t, err, application.ClassOperationFailed, "job.exited_nonzero")
	record, ok := appErr.Record()
	if !ok || record.Steps[0].Job == nil || record.Steps[0].Job.ExitCode != 3 {
		t.Fatalf("record = %+v, want the failed run with the job's exit code 3", record)
	}
}

func TestADryRunRunsNothing(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{}
	res, err := runIn(t, ops)(context.Background(), application.RunJobRequest{Job: "migrate", DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if res.HasRecord || res.Invocation != "" || !ops.Runs()[0].DryRun || len(ops.Submits()) != 0 {
		t.Fatalf("dry run = %+v, runs %v, submits %v; want a preview only", res, ops.Runs(), ops.Submits())
	}
}

func TestRunRefusesAServiceAndAnUnknownJob(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{}
	run := runIn(t, ops)
	_, err := run(context.Background(), application.RunJobRequest{Job: "api"})
	wantError(t, err, application.ClassUsage, "run.not_a_job")
	_, err = run(context.Background(), application.RunJobRequest{Job: "missing"})
	wantError(t, err, application.ClassNotFound, "project.unknown_object")
	if len(ops.Runs()) != 0 {
		t.Fatalf("runs = %v, want none", ops.Runs())
	}
}
