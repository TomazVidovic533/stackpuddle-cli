// Package command_test runs the whole command tree through cli.NewRootCommand with injected streams and
// real temporary projects. Nothing below the command line is replaced by a double.
package command_test

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/cli"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/project"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fakeops"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fixture"
	"github.com/TomazVidovic533/stackpuddle/model"
)

var update = flag.Bool("update", false, "rewrite the golden files for a reviewed diff")

// liveNow is the clock of the live checker in command tests.
var liveNow = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

type outcome struct {
	stdout, stderr string
	exit           int
}

// execute runs the command tree in dir with the given process variables and returns what it printed. The
// lifecycle verbs run over a default operations double.
func execute(t *testing.T, dir string, vars map[string]string, args ...string) outcome {
	t.Helper()
	return executeOps(t, dir, vars, &fakeops.Operations{}, args...)
}

// noSleep is a Sleeper that never waits, so the polling verbs do not touch the wall clock.
func noSleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }

// executeOps is execute over a chosen operations double. The double stands in for the engine only: the
// project is read from disk and every use case is the production one.
func executeOps(t *testing.T, dir string, vars map[string]string, ops application.Operations, args ...string) outcome {
	t.Helper()
	lookup := fixture.Lookup(vars)
	source := project.NewSource(func() (string, error) { return dir, nil })
	root := cli.NewRootCommand(cli.Dependencies{
		Check: application.NewCheckProject(source, []application.Checker{
			application.StructureChecker(), application.ConfigurationChecker(lookup),
			application.LiveChecker(ops, lookup, func() time.Time { return liveNow }),
		}),
		Inspect: application.NewInspectConfig(source, lookup),
		Version: func() (model.VersionInfo, error) { return application.VersionInfoFrom(nil), nil },

		Mutate:           application.NewMutate(source, nil, ops, lookup),
		InspectOperation: application.NewInspectOperation(source, ops, noSleep),
		InspectState:     application.NewInspectState(source, ops),
		Cancel:           application.NewCancel(source, ops),
		List:             application.NewList(source, ops),
		Remove:           application.NewRemove(project.NewEditor(func() (string, error) { return dir, nil })),
		RunJob:           application.NewRunJob(source, nil, ops, lookup),
	})
	var stdout, stderr bytes.Buffer
	root.SetArgs(append([]string{}, args...)) // never nil: Cobra reads os.Args for a nil slice
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	err := root.ExecuteContext(context.Background())
	if err != nil {
		if writeErr := cli.WriteFailure(root, &stderr, err); writeErr != nil {
			t.Fatalf("write failure: %v", writeErr)
		}
	}
	return outcome{stdout: stdout.String(), stderr: stderr.String(), exit: cli.ExitCode(err)}
}

func shop(t *testing.T, extra map[string]string) string {
	t.Helper()
	return fixture.Dir(t, fixture.ShopFiles(extra))
}

// golden compares got with testdata/<name>.golden, or rewrites it under -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatalf("update %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run with -update to create it)", path, err)
	}
	if string(want) != got {
		t.Fatalf("%s differs from the golden file:\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

func contains(t *testing.T, label, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("%s lacks %q:\n%s", label, needle, haystack)
	}
}
