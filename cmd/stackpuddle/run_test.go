package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/cli"
)

const validProject = `apiVersion: stackpuddle/v1
kind: Project
metadata:
  name: shop
spec:
  workloads:
    api:
      type: service
      image: ghcr.io/example/api:1.4.2
  environments:
    local:
      runtime:
        type: compose
        context: desktop-linux
      secrets:
        provider: local
`

func projectDir(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stackpuddle.yaml"), []byte(content), 0o600); err != nil {
		t.Fatalf("write project: %v", err)
	}
	return dir
}

func runIn(dir string, args ...string) (stdout, stderr string, exit int) {
	var out, errOut bytes.Buffer
	noEnv := func(string) (string, bool) { return "", false }
	err := run(context.Background(), args, noEnv, func() (string, error) { return dir, nil }, &out, &errOut)
	return out.String(), errOut.String(), cli.ExitCode(err)
}

func TestRunExecutesTheWholeTreeInProcess(t *testing.T) {
	t.Parallel()
	dir := projectDir(t, validProject)
	stdout, stderr, exit := runIn(dir, "check", "--output", "json")
	if exit != cli.ExitOK || stderr != "" || !strings.Contains(stdout, `"outcome": "pass"`) {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", exit, stdout, stderr)
	}
	stdout, _, exit = runIn(dir, "inspect", "workload/api", "--explain")
	if exit != cli.ExitOK || !strings.Contains(stdout, "workload/api in environment local") {
		t.Fatalf("inspect exit %d\n%s", exit, stdout)
	}
	if _, stderr, exit = runIn(dir, "frobnicate"); exit != cli.ExitUsage || !strings.Contains(stderr, "usage.invalid") {
		t.Fatalf("unknown command: exit %d stderr %s, want %d", exit, stderr, cli.ExitUsage)
	}
}

func TestRunNilArgsDoNotReadTheProcessArguments(t *testing.T) {
	t.Parallel()
	stdout, _, exit := runIn(t.TempDir())
	if exit != cli.ExitOK || !strings.Contains(stdout, "Usage:") {
		t.Fatalf("exit %d, stdout %q, want the help text", exit, stdout)
	}
}

func TestRunsShareNoState(t *testing.T) {
	t.Parallel()
	good := projectDir(t, validProject)
	bad := projectDir(t, strings.Replace(validProject, "kind: Project", "kind: Nonsense", 1))
	type result struct {
		stdout string
		exit   int
	}
	results := make([]result, 8)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			dir := good
			if i%2 == 1 {
				dir = bad
			}
			stdout, _, exit := runIn(dir, "check", "--output", "json")
			results[i] = result{stdout, exit}
		})
	}
	wg.Wait()
	for i, r := range results {
		want := cli.ExitOK
		if i%2 == 1 {
			want = cli.ExitValidation
		}
		if r.exit != want {
			t.Errorf("run %d exit = %d, want %d: parallel runs leaked state", i, r.exit, want)
		}
		if r.stdout != results[i%2].stdout {
			t.Errorf("run %d output differs from the first run of its kind", i)
		}
	}
}

func TestRunStopsOnACancelledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errOut bytes.Buffer
	dir := projectDir(t, validProject)
	err := run(ctx, []string{"check"}, func(string) (string, bool) { return "", false },
		func() (string, error) { return dir, nil }, &out, &errOut)
	if cli.ExitCode(err) != cli.ExitUnexpected || !strings.Contains(errOut.String(), "unexpected") {
		t.Fatalf("exit %d stderr %q, want an unexpected failure", cli.ExitCode(err), errOut.String())
	}
}
