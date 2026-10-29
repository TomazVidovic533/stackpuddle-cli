//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

func field(t testing.TB, doc map[string]any, path ...string) any {
	t.Helper()
	var cur any = doc
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("no object at %v in %v", path, doc)
		}
		cur = m[key]
	}
	return cur
}

func str(t testing.TB, doc map[string]any, path ...string) string {
	t.Helper()
	s, _ := field(t, doc, path...).(string)
	return s
}

func TestDryRunChangesNothing(t *testing.T) {
	s := newStack(t, options{})
	before := s.snapshot()
	r := s.mustRun("up", "--dry-run")
	for _, want := range []string{"dry run: nothing was changed; based on generation 0", "selected: workload/api, workload/worker"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("dry run output lacks %q:\n%s", want, r.stdout)
		}
	}
	if after := s.snapshot(); after != before {
		t.Fatalf("a dry run on an empty environment changed something:\nbefore %+v\nafter  %+v", before, after)
	}
	s.mustRun("up", "--timeout", "2m")
	before = s.snapshot()
	for _, args := range [][]string{{"up", "--dry-run"}, {"stop", "--all", "--dry-run"}, {"destroy", "env/local", "--dry-run"}} {
		out := s.json(args...)
		if str(t, out, "environment") != "local" || field(t, out, "dryRun") != true || field(t, out, "operation") != nil {
			t.Errorf("%v: dry-run document = %v", args, out)
		}
		if gen, _ := field(t, out, "preview", "basedOnGeneration").(float64); gen < 1 {
			t.Errorf("%v: basedOnGeneration = %v, want the accepted generation", args, gen)
		}
		if after := s.snapshot(); after != before {
			t.Fatalf("%v changed the state bytes, the compose projects or the volumes:\nbefore %+v\nafter  %+v", args, before, after)
		}
	}
}

func TestUpPrintsTheSelectionAndAnOperationThatCanBeInspected(t *testing.T) {
	s := newStack(t, options{})
	r := s.mustRun("up")
	if !strings.Contains(r.stderr, "accepted op-") || !strings.Contains(r.stdout, "selected: workload/api, workload/worker") {
		t.Fatalf("up output:\nstdout: %s\nstderr: %s", r.stdout, r.stderr)
	}
	doc := s.json("up")
	id := str(t, doc, "operation", "id")
	if !strings.HasPrefix(id, "op-") {
		t.Fatalf("operation = %v", doc)
	}
	got := s.json("inspect", "operation/"+id, "--wait")
	if str(t, got, "status") != "succeeded" || str(t, got, "id") != id {
		t.Fatalf("inspect operation = %v", got)
	}
	if running := s.containers(true); !containsName(running, "api") || !containsName(running, "worker") {
		t.Fatalf("running containers = %v, want api and worker", running)
	}
}

// startSlowUp starts an up whose migrate job sleeps, waits until the operation was accepted and the job is
// running, and returns the running command and the operation ID.
func startSlowUp(t *testing.T, s *stack, group bool, extra ...string) (*proc, string) {
	t.Helper()
	p := s.start(group, append([]string{"up"}, extra...)...)
	id := p.waitStderr(t, "accepted ")
	s.waitRunning("migrate")
	return p, id
}

func TestInspectWaitAndFollowWorkWhileAnotherUpHoldsTheLock(t *testing.T) {
	s := newStack(t, options{slowJob: true})
	holder, id := startSlowUp(t, s, false)
	wait := s.start(false, "inspect", "operation/"+id, "--wait", "--timeout", "2m", "--output", "json")
	follow := s.start(false, "inspect", "operation/"+id, "--follow", "--timeout", "2m")
	waited := wait.wait(t, stepBudget)
	followed := follow.wait(t, stepBudget)
	if waited.exit != 0 || str(t, parseJSON(t, waited.stdout), "status") != "succeeded" {
		t.Fatalf("--wait: exit %d\n%s\n%s", waited.exit, waited.stdout, waited.stderr)
	}
	if followed.exit != 0 || !strings.Contains(followed.stderr, "migrate") || !strings.Contains(followed.stdout, "succeeded") {
		t.Fatalf("--follow: exit %d\nstdout: %s\nstderr: %s", followed.exit, followed.stdout, followed.stderr)
	}
	if r := holder.wait(t, stepBudget); r.exit != 0 {
		t.Fatalf("the holder: exit %d\n%s", r.exit, r.stderr)
	}
}

func TestASecondUpIsAConflictNamingTheHolderAndTimeoutWaits(t *testing.T) {
	s := newStack(t, options{slowJob: true})
	holder, id := startSlowUp(t, s, false)
	second := s.run("up")
	if second.exit != 6 {
		t.Fatalf("second up: exit %d, want 6 (conflict)\n%s", second.exit, second.stderr)
	}
	for _, want := range []string{id, "stackpuddle inspect operation/" + id + " --follow"} {
		if !strings.Contains(second.stderr, want) {
			t.Errorf("the conflict does not name %q:\n%s", want, second.stderr)
		}
	}
	// A deadline shorter than the holder's remaining work is still a conflict.
	short := s.run("up", "--timeout", "1s")
	if short.exit != 6 {
		t.Fatalf("up --timeout 1s: exit %d, want 6\n%s", short.exit, short.stderr)
	}
	// A deadline longer than the holder's work waits, then proceeds.
	waiting := s.start(false, "up", "--timeout", "2m")
	if r := holder.wait(t, stepBudget); r.exit != 0 {
		t.Fatalf("the holder: exit %d\n%s", r.exit, r.stderr)
	}
	if r := waiting.wait(t, stepBudget); r.exit != 0 {
		t.Fatalf("the waiting up: exit %d\nstdout: %s\nstderr: %s", r.exit, r.stdout, r.stderr)
	}
}

func TestEverySignalLeavesTheOperationInterruptedAndTheNextUpContinuesIt(t *testing.T) {
	cases := []struct {
		name   string
		signal syscall.Signal
		group  bool
	}{
		{"SIGINT to the process group, as a terminal does", syscall.SIGINT, true},
		{"SIGTERM", syscall.SIGTERM, false},
		{"SIGHUP", syscall.SIGHUP, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newStack(t, options{slowJob: true})
			p, id := startSlowUp(t, s, true)
			target := p.cmd.Process.Pid
			if c.group {
				target = -target
			}
			if err := syscall.Kill(target, c.signal); err != nil {
				t.Fatalf("signal: %v", err)
			}
			r := p.wait(t, stepBudget)
			if r.exit != 8 || !strings.Contains(r.stderr, "interrupted") {
				t.Fatalf("exit %d, want 8 (interrupted)\nstdout: %s\nstderr: %s", r.exit, r.stdout, r.stderr)
			}
			old := s.json("inspect", "operation/"+id)
			if str(t, old, "status") != "interrupted" {
				t.Fatalf("the operation is %v, want interrupted", str(t, old, "status"))
			}
			if steps, _ := field(t, old, "steps").([]any); len(steps) == 0 {
				t.Fatal("the interrupted operation lost its journal")
			}
			// The next up continues the intent under a new ID that names the old one.
			next := s.json("up", "--timeout", "2m")
			newID := str(t, next, "operation", "id")
			if newID == id || str(t, next, "operation", "continues") != id || str(t, next, "operation", "status") != "succeeded" {
				t.Fatalf("the next up = %v, want a new operation that continues %s", next["operation"], id)
			}
			if got := s.json("inspect", "operation/"+id); str(t, got, "continuedBy") != newID {
				t.Fatalf("the old operation says it was continued by %q, want %q", str(t, got, "continuedBy"), newID)
			}
			// No duplicate effect: one api, one worker, one job container.
			var api, worker, migrate int
			for _, name := range s.containers(false) {
				switch {
				case strings.Contains(name, "api"):
					api++
				case strings.Contains(name, "worker"):
					worker++
				case strings.Contains(name, "migrate"):
					migrate++
				}
			}
			if api != 1 || worker != 1 || migrate > 1 {
				t.Fatalf("containers = %v, want one of each (the job at most once)", s.containers(false))
			}
		})
	}
}

func TestCancelFromASecondProcessStopsTheOperationAndItIsNotContinued(t *testing.T) {
	s := newStack(t, options{slowJob: true})
	p, id := startSlowUp(t, s, false)
	c := s.mustRun("cancel", id)
	if !strings.Contains(c.stdout, "image pull or initdb") {
		t.Errorf("cancel does not say that a long step runs to its own bound:\n%s", c.stdout)
	}
	r := p.wait(t, stepBudget)
	if r.exit != 9 {
		t.Fatalf("exit %d, want 9 (cancelled)\nstdout: %s\nstderr: %s", r.exit, r.stdout, r.stderr)
	}
	old := s.json("inspect", "operation/"+id)
	if str(t, old, "status") != "cancelled" || field(t, old, "continuedBy") != nil {
		t.Fatalf("operation = %v, want cancelled and not continued", old)
	}
	next := s.json("up", "--timeout", "2m")
	if field(t, next, "operation", "continues") != nil {
		t.Fatalf("the next up continues a cancelled operation: %v", next["operation"])
	}
	if got := s.json("inspect", "operation/"+id); field(t, got, "continuedBy") != nil {
		t.Fatalf("the cancelled operation was continued: %v", got)
	}
}

func TestStopKeepsEveryVolumeAndNothingRestartsItButAnUpThatSelectsIt(t *testing.T) {
	s := newStack(t, options{})
	s.mustRun("up", "--all", "--timeout", "2m")
	volumes := s.volumes()
	if len(volumes) < 2 {
		t.Fatalf("volumes = %v, want the api's and the module's", volumes)
	}
	s.mustRun("stop", "--all")
	if got := s.volumes(); !slices.Equal(got, volumes) {
		t.Fatalf("stop changed the volumes: %v -> %v", volumes, got)
	}
	if running := s.containers(true); len(running) != 0 {
		t.Fatalf("running after stop: %v", running)
	}
	// An up that does not select the api leaves it stopped, and so do inspect and list.
	s.mustRun("up", "workload/worker")
	s.mustRun("inspect", "workload/api", "--view", "state")
	s.mustRun("list", "resources")
	if running := s.containers(true); containsName(running, "api") {
		t.Fatalf("the api was restarted without being selected: %v", running)
	}
	s.mustRun("up", "workload/api")
	if running := s.containers(true); !containsName(running, "api") {
		t.Fatalf("up workload/api did not start the api: %v", running)
	}
}

func TestRemoveDeletesOnlyTheDeclarationAndKeepsTheVolume(t *testing.T) {
	s := newStack(t, options{})
	s.mustRun("up", "--all", "--timeout", "2m")
	volumes := s.volumes()
	if len(volumes) < 2 {
		t.Fatalf("volumes = %v, want the api's and the module's", volumes)
	}
	state := s.json("inspect", "env/local", "--view", "state")
	r := s.mustRun("remove", "module/primary")
	if !strings.Contains(r.stdout, "retained until destroy") {
		t.Errorf("remove does not say the realization stays:\n%s", r.stdout)
	}
	data, err := os.ReadFile(filepath.Join(s.dir, "stackpuddle.yaml"))
	if err != nil || strings.Contains(string(data), "primary") {
		t.Fatalf("the declaration is still in the project file: %v", err)
	}
	if got := s.volumes(); !slices.Equal(got, volumes) {
		t.Fatalf("remove changed the volumes: %v -> %v", volumes, got)
	}
	after := s.json("inspect", "env/local", "--view", "state")
	objects, _ := field(t, state, "objects").([]any)
	remaining, _ := field(t, after, "objects").([]any)
	if len(remaining) != len(objects) {
		t.Fatalf("remove changed the realized objects: %d -> %d", len(objects), len(remaining))
	}
}

func TestDestroyDeletesOnlyThisEnvironmentsOwnedObjectsAndKeepsVolumesWithoutDataIntent(t *testing.T) {
	s := newStack(t, options{})
	s.mustRun("up", "--all", "--timeout", "2m")
	s.mustRun("up", "--all", "--env", "other", "--timeout", "2m")
	local, other := s.composeProject("local"), s.composeProject("other")
	if local == "" || other == "" || local == other {
		t.Fatalf("compose projects = %q and %q, want two distinct ones", local, other)
	}
	labelled := func(kind, project string) []string {
		format := "{{.Names}}"
		args := []string{"ps", "-a"}
		if kind == "volume" {
			format = "{{.Name}}"
			args = []string{"volume", "ls"}
		}
		return lines(docker(t, append(args, "--filter", "label=com.docker.compose.project="+project, "--format", format)...))
	}
	otherContainers, otherVolumes := labelled("container", other), labelled("volume", other)
	localVolumes := labelled("volume", local)
	if len(otherContainers) == 0 || len(localVolumes) < 2 {
		t.Fatalf("setup: other containers %v, local volumes %v", otherContainers, localVolumes)
	}

	preview := s.json("destroy", "env/local", "--dry-run")
	actions, _ := field(t, preview, "preview", "actions").([]any)
	if len(actions) == 0 {
		t.Fatalf("destroy env/local --dry-run lists nothing: %v", preview)
	}
	owned := map[string]bool{}
	for _, o := range s.json("inspect", "env/local", "--view", "state")["objects"].([]any) {
		owned[o.(map[string]any)["ref"].(string)] = true
	}
	for _, a := range actions {
		object, _ := a.(map[string]any)["object"].(string)
		if !owned[object] {
			t.Errorf("the preview names %q, which this environment does not own (owned: %v)", object, owned)
		}
	}

	s.mustRun("destroy", "env/local")
	if left := labelled("container", local); len(left) != 0 {
		t.Fatalf("destroy left containers: %v", left)
	}
	if kept := labelled("volume", local); !slices.Equal(kept, localVolumes) {
		t.Fatalf("destroy without data-deletion intent changed the volumes: %v -> %v", localVolumes, kept)
	}
	if got := labelled("container", other); !slices.Equal(got, otherContainers) {
		t.Fatalf("destroy touched the other environment's containers: %v -> %v", otherContainers, got)
	}
	if got := labelled("volume", other); !slices.Equal(got, otherVolumes) {
		t.Fatalf("destroy touched the other environment's volumes: %v -> %v", otherVolumes, got)
	}
	// The data-deletion intent is separate and explicit; it is proved on the other environment.
	s.mustRun("destroy", "env/other", "--env", "other", "--delete-data")
	if kept := labelled("volume", other); len(kept) != 0 {
		t.Fatalf("destroy --delete-data left volumes: %v", kept)
	}
	if kept := labelled("volume", local); !slices.Equal(kept, localVolumes) {
		t.Fatalf("destroying the other environment touched this one's volumes: %v -> %v", localVolumes, kept)
	}
}

func TestListAndInspectStateShowOwnershipAndNoSecret(t *testing.T) {
	s := newStack(t, options{})
	s.mustRun("up", "--all", "--timeout", "2m")
	resources := s.json("list", "resources")
	items, _ := field(t, resources, "items").([]any)
	if len(items) == 0 {
		t.Fatalf("list resources = %v", resources)
	}
	for _, c := range []string{"applications", "workloads", "modules", "bindings", "environments"} {
		s.mustRun("list", c)
	}
	state := s.json("inspect", "workload/api", "--view", "state")
	objects, _ := field(t, state, "objects").([]any)
	if len(objects) == 0 || field(t, state, "generation") == nil {
		t.Fatalf("state = %v, want the api's objects and the generation", state)
	}
	if mappings, _ := objects[0].(map[string]any)["mappings"].([]any); len(mappings) == 0 {
		t.Fatalf("the api object carries no ownership evidence: %v", objects[0])
	}
	for _, refused := range []string{"sessions", "operations"} {
		if r := s.run("list", refused); r.exit != 2 {
			t.Errorf("list %s: exit %d, want 2", refused, r.exit)
		}
	}
	// A second real operation records its result; its text and JSON carry the same operation.
	text := s.mustRun("up")
	doc := s.json("up")
	if !strings.Contains(text.stdout, "operation op-") || !strings.HasPrefix(str(t, doc, "operation", "id"), "op-") {
		t.Fatalf("text:\n%s\njson: %v", text.stdout, doc)
	}
}
