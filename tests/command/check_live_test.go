package command_test

import (
	"context"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/cli"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fakeops"
)

func TestCheckWithoutLiveNeverAsksTheEngine(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{}
	executeOps(t, storefront(t), nil, ops, "check")
	if n := len(ops.Submits()) + len(ops.Runs()); n != 0 {
		t.Fatalf("the engine was asked %d times by check without --live", n)
	}
}

func TestCheckLivePrintsFreshnessAndAStoppedDaemonIsIncomplete(t *testing.T) {
	t.Parallel()
	got := execute(t, storefront(t), map[string]string{"STOREFRONT_TOKEN": "x"}, "check", "--live")
	contains(t, "stdout", got.stdout, "live")
	contains(t, "stdout", got.stdout, "observed 2026-10-29T12:00:00Z, generation 3")

	down := &fakeops.Operations{SubmitFunc: func(context.Context, application.SubmitRequest) (application.SubmitResult, error) {
		return application.SubmitResult{}, application.NewError(application.ClassUnavailable, "runtime.target_unreachable", "docker is not running")
	}}
	got = executeOps(t, storefront(t), map[string]string{"STOREFRONT_TOKEN": "x"}, down, "check", "--live")
	if got.exit != cli.ExitIncomplete {
		t.Fatalf("exit = %d, want %d: a stopped daemon is unknown, never pass\n%s", got.exit, cli.ExitIncomplete, got.stdout)
	}
	contains(t, "stdout", got.stdout, "live [local]: unknown")
}
