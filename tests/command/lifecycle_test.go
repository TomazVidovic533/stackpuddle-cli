package command_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/cli"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fakeops"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fixture"
)

func storefront(t *testing.T) string {
	t.Helper()
	return fixture.Dir(t, fixture.StorefrontFiles())
}

func failing(err *application.Error) *fakeops.Operations {
	return &fakeops.Operations{SubmitFunc: func(context.Context, application.SubmitRequest) (application.SubmitResult, error) {
		return application.SubmitResult{}, err
	}}
}

func TestEveryFailureClassHasItsOwnExitCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		class application.Class
		code  string
		exit  int
	}{
		{application.ClassConflict, "conflict.stale_generation", cli.ExitConflict},
		{application.ClassCompatibility, "compatibility.digest_mismatch", cli.ExitCompatibility},
		{application.ClassInterrupted, "operation.interrupted", cli.ExitInterrupted},
		{application.ClassCancelled, "operation.cancelled", cli.ExitCancelled},
		{application.ClassOperationFailed, "runtime.effect_failed", cli.ExitOperationFailed},
		{application.ClassForbidden, "runtime.out_of_scope", cli.ExitForbidden},
		{application.ClassUnavailable, "runtime.target_unreachable", cli.ExitUnavailable},
	}
	seen := map[int]application.Class{}
	for _, tt := range tests {
		t.Run(tt.class.String(), func(t *testing.T) {
			t.Parallel()
			got := executeOps(t, storefront(t), nil, failing(application.NewError(tt.class, tt.code, "refused")), "up")
			if got.exit != tt.exit {
				t.Fatalf("exit = %d, want %d\nstderr: %s", got.exit, tt.exit, got.stderr)
			}
			contains(t, "stderr", got.stderr, tt.code)
			json := executeOps(t, storefront(t), nil, failing(application.NewError(tt.class, tt.code, "refused")), "up", "--output", "json")
			contains(t, "json stderr", json.stderr, `"class": "`+tt.class.String()+`"`)
		})
		if other, dup := seen[tt.exit]; dup {
			t.Errorf("exit %d is shared by %s and %s", tt.exit, other, tt.class)
		}
		seen[tt.exit] = tt.class
	}
}

func TestEveryClassHasAnExitCodeAndADocumentedRow(t *testing.T) {
	t.Parallel()
	docs, err := os.ReadFile(filepath.Join("..", "..", "docs", "commands.md"))
	if err != nil {
		t.Fatal(err)
	}
	rows := regexp.MustCompile(`(?m)^\| (\d+) \| (.*)$`).FindAllStringSubmatch(string(docs), -1)
	byCode := map[string]string{}
	for _, r := range rows {
		byCode[r[1]] = strings.ToLower(r[2])
	}
	for _, class := range application.Classes() {
		exit := cli.ExitCode(application.NewError(class, "x.y", "x"))
		if exit == 0 || (exit == cli.ExitUnexpected && class != application.ClassUnexpected) {
			t.Errorf("class %s has no exit code of its own (%d)", class, exit)
		}
		row, ok := byCode[itoa(exit)]
		if !ok || !strings.Contains(row, strings.ReplaceAll(class.String(), "_", " ")) {
			t.Errorf("docs/commands.md has no exit-code row %d for class %s (row: %q)", exit, class, row)
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestUpPrintsTheSelectionAndTheOperationID(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{}
	text := executeOps(t, storefront(t), nil, ops, "up", "app/storefront")
	if text.exit != cli.ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", text.exit, text.stderr)
	}
	id := fakeops.ID(1).String()
	for _, want := range []string{"environment local", "selected: workload/api, workload/web", "groups: app/storefront",
		"prerequisites (jobs the selection requires): workload/migrate", "operation " + id + " succeeded (up, revision 1)"} {
		contains(t, "text", text.stdout, want)
	}
	asJSON := executeOps(t, storefront(t), nil, ops, "up", "app/storefront", "--output", "json")
	var doc struct {
		Environment string `json:"environment"`
		Selection   struct {
			Selected      []string `json:"selected"`
			Groups        []string `json:"groups"`
			Prerequisites []string `json:"prerequisites"`
		} `json:"selection"`
		DryRun    bool `json:"dryRun"`
		Operation struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"operation"`
	}
	if err := json.Unmarshal([]byte(asJSON.stdout), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, asJSON.stdout)
	}
	if doc.Environment != "local" || doc.Operation.ID != id || doc.Operation.Status != "succeeded" || doc.DryRun ||
		strings.Join(doc.Selection.Selected, ",") != "workload/api,workload/web" ||
		strings.Join(doc.Selection.Groups, ",") != "app/storefront" || strings.Join(doc.Selection.Prerequisites, ",") != "workload/migrate" {
		t.Fatalf("the JSON carries different facts than the text: %+v", doc)
	}
}

func TestUpProgressGoesToStderrInTextOnly(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{SubmitFunc: func(_ context.Context, req application.SubmitRequest) (application.SubmitResult, error) {
		if req.Progress != nil {
			req.Progress(fakeops.Record(application.StatusRunning))
		}
		return application.SubmitResult{Record: fakeops.Record(application.StatusSucceeded), HasRecord: true}, nil
	}}
	text := executeOps(t, storefront(t), nil, ops, "up")
	contains(t, "text stderr", text.stderr, "accepted "+fakeops.ID(1).String())
	contains(t, "text stderr", text.stderr, "inspect operation/"+fakeops.ID(1).String()+" --follow")
	asJSON := executeOps(t, storefront(t), nil, ops, "up", "--output", "json")
	if asJSON.stderr != "" {
		t.Fatalf("JSON mode wrote to stderr: %q", asJSON.stderr)
	}
}

func TestDryRunPrintsActionsUnknownsAndTheGeneration(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{SubmitFunc: func(_ context.Context, req application.SubmitRequest) (application.SubmitResult, error) {
		if !req.DryRun {
			t.Error("the request is not a dry run")
		}
		return application.SubmitResult{Preview: application.Preview{BasedOnGeneration: 9, Actions: []application.Action{
			{Action: "workload.apply", Object: "workload/api", Effect: application.EffectApply},
			{Action: "module.apply", Object: "module/audit", Effect: application.EffectApply, Unknown: true},
			{Effect: application.EffectBlocked, Problem: "compatibility.capability_unsupported"},
		}}}, nil
	}}
	text := executeOps(t, storefront(t), nil, ops, "up", "--dry-run")
	for _, want := range []string{"dry run: nothing was changed; based on generation 9", "apply workload.apply workload/api",
		"apply module.apply module/audit (unknown", "blocked (compatibility.capability_unsupported)"} {
		contains(t, "text", text.stdout, want)
	}
	if strings.Contains(text.stdout, "operation op-") {
		t.Errorf("a dry run printed an operation:\n%s", text.stdout)
	}
	asJSON := executeOps(t, storefront(t), nil, ops, "up", "--dry-run", "--output", "json")
	var doc struct {
		DryRun  bool `json:"dryRun"`
		Preview struct {
			BasedOnGeneration int64 `json:"basedOnGeneration"`
			Actions           []struct {
				Effect  string `json:"effect"`
				Unknown bool   `json:"unknown"`
				Problem string `json:"problem"`
			} `json:"actions"`
		} `json:"preview"`
		Operation *struct{} `json:"operation"`
	}
	if err := json.Unmarshal([]byte(asJSON.stdout), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, asJSON.stdout)
	}
	if !doc.DryRun || doc.Preview.BasedOnGeneration != 9 || len(doc.Preview.Actions) != 3 || !doc.Preview.Actions[1].Unknown ||
		doc.Preview.Actions[2].Problem == "" || doc.Operation != nil {
		t.Fatalf("preview document = %+v", doc)
	}
}

func TestFlagsReachTheEngineRequest(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{}
	dir := storefront(t)
	got := executeOps(t, dir, nil, ops, "destroy", "env/local", "--delete-data", "--expand-scope", "--timeout", "45s")
	if got.exit != cli.ExitOK {
		t.Fatalf("destroy: exit %d\n%s", got.exit, got.stderr)
	}
	req := ops.Submits()[0]
	if req.Command != application.CommandDestroy || !req.DeleteData || !req.ExpandedScope || !req.Targets.All || req.Wait != 45*time.Second {
		t.Fatalf("request = %+v", req)
	}
	got = executeOps(t, dir, nil, ops, "destroy", "workload/web")
	if req = ops.Submits()[1]; got.exit != cli.ExitOK || req.DeleteData || req.ExpandedScope || req.Wait != 0 {
		t.Fatalf("destroy without the data-deletion intent: %+v", req)
	}
	got = executeOps(t, dir, nil, ops, "stop", "--all")
	if req = ops.Submits()[2]; got.exit != cli.ExitOK || req.Command != application.CommandStop || !req.Targets.All {
		t.Fatalf("stop --all: %+v", req)
	}
}

func TestLifecycleUsageErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		code string
	}{
		{"all with references", []string{"up", "--all", "workload/api"}, "selection.all_and_refs"},
		{"stop without scope", []string{"stop"}, "selection.scope_required"},
		{"destroy without scope", []string{"destroy"}, "selection.scope_required"},
		{"destroy has no --all", []string{"destroy", "--all"}, "usage.invalid"},
		{"stop has no --delete-data", []string{"stop", "--all", "--delete-data"}, "usage.invalid"},
		{"invalid reference", []string{"up", "nonsense"}, "reference.invalid_ref"},
		{"negative timeout", []string{"up", "--timeout", "-1s"}, "usage.invalid_timeout"},
		{"timeout is not a duration", []string{"up", "--timeout", "soon"}, "usage.invalid"},
		{"wait outside operations", []string{"inspect", "workload/api", "--wait"}, "inspect.wait_needs_operation"},
		{"cancel with a bad id", []string{"cancel", "op-nope"}, "reference.invalid_ref"},
		{"cancel without an id", []string{"cancel"}, "usage.invalid"},
		{"remove of an environment", []string{"remove", "env/local"}, "remove.unsupported_ref_kind"},
		{"list sessions", []string{"list", "sessions"}, "list.unsupported_collection"},
		{"list operations", []string{"list", "operations"}, "list.unsupported_collection"},
		{"list nothing known", []string{"list", "bogus"}, "list.unknown_collection"},
		{"list without a collection", []string{"list"}, "usage.invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ops := &fakeops.Operations{}
			got := executeOps(t, storefront(t), nil, ops, tt.args...)
			if got.exit != cli.ExitUsage {
				t.Fatalf("exit = %d, want %d\nstderr: %s", got.exit, cli.ExitUsage, got.stderr)
			}
			contains(t, "stderr", got.stderr, tt.code)
			if len(ops.Submits()) != 0 || len(ops.Cancels()) != 0 {
				t.Fatal("a refused command reached the engine")
			}
		})
	}
}

func TestListOperationsNamesTheReplacement(t *testing.T) {
	t.Parallel()
	got := execute(t, storefront(t), nil, "list", "operations")
	contains(t, "stderr", got.stderr, "inspect operation/<id>")
	got = execute(t, storefront(t), nil, "list", "sessions")
	contains(t, "stderr", got.stderr, "interactive shell")
}

func TestListPrintsEachCollectionInBothFormats(t *testing.T) {
	t.Parallel()
	dir := storefront(t)
	for _, collection := range application.Collections() {
		text := execute(t, dir, nil, "list", string(collection))
		asJSON := execute(t, dir, nil, "list", string(collection), "--output", "json")
		if text.exit != cli.ExitOK || asJSON.exit != cli.ExitOK {
			t.Fatalf("list %s: exits %d and %d\n%s%s", collection, text.exit, asJSON.exit, text.stderr, asJSON.stderr)
		}
		var doc struct {
			Collection string `json:"collection"`
			Items      []struct {
				Ref string `json:"ref"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(asJSON.stdout), &doc); err != nil || doc.Collection != string(collection) {
			t.Fatalf("list %s json: %v\n%s", collection, err, asJSON.stdout)
		}
		for _, item := range doc.Items {
			contains(t, "text list "+string(collection), text.stdout, item.Ref)
		}
	}
}

func TestInspectOperationOutcomes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status application.Status
		exit   int
	}{
		{application.StatusSucceeded, cli.ExitOK},
		{application.StatusFailed, cli.ExitOperationFailed},
		{application.StatusBlocked, cli.ExitOperationFailed},
		{application.StatusInterrupted, cli.ExitInterrupted},
		{application.StatusCancelled, cli.ExitCancelled},
	}
	ref := "operation/" + fakeops.ID(1).String()
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			t.Parallel()
			ops := &fakeops.Operations{InspectFunc: func(context.Context, application.InspectQuery) (application.Inspection, error) {
				return fakeops.Record(tt.status), nil
			}}
			got := executeOps(t, storefront(t), nil, ops, "inspect", ref, "--wait")
			if got.exit != tt.exit {
				t.Fatalf("--wait exit = %d, want %d\n%s%s", got.exit, tt.exit, got.stdout, got.stderr)
			}
			// Reading without --wait is a successful read whatever the status.
			plain := executeOps(t, storefront(t), nil, ops, "inspect", ref)
			if plain.exit != cli.ExitOK {
				t.Fatalf("plain read exit = %d\n%s", plain.exit, plain.stderr)
			}
			contains(t, "plain stdout", plain.stdout, string(tt.status))
			if tt.exit != cli.ExitOK {
				asJSON := executeOps(t, storefront(t), nil, ops, "inspect", ref, "--wait", "--output", "json")
				contains(t, "failure document", asJSON.stderr, `"operation": {`)
				contains(t, "failure document", asJSON.stderr, `"status": "`+string(tt.status)+`"`)
			}
		})
	}
}

func TestFollowPrintsProgressWhileWaiting(t *testing.T) {
	t.Parallel()
	calls := 0
	ops := &fakeops.Operations{InspectFunc: func(context.Context, application.InspectQuery) (application.Inspection, error) {
		calls++
		rec := fakeops.Record(application.StatusRunning)
		if calls >= 3 {
			rec = fakeops.Record(application.StatusSucceeded)
		}
		rec.UpdatedAt = rec.UpdatedAt.Add(time.Duration(calls) * time.Second)
		return rec, nil
	}}
	got := executeOps(t, storefront(t), nil, ops, "inspect", "operation/"+fakeops.ID(1).String(), "--follow", "--timeout", "1m")
	if got.exit != cli.ExitOK {
		t.Fatalf("exit = %d\n%s", got.exit, got.stderr)
	}
	contains(t, "progress", got.stderr, "s1 workload.apply workload/api: completed")
	contains(t, "result", got.stdout, "succeeded")
}

func TestCancelPrintsTheStepBoundaryNote(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{}
	id := fakeops.ID(2).String()
	got := executeOps(t, storefront(t), nil, ops, "cancel", id)
	if got.exit != cli.ExitOK {
		t.Fatalf("exit = %d\n%s", got.exit, got.stderr)
	}
	contains(t, "text", got.stdout, "cancel requested for "+id)
	contains(t, "text", got.stdout, "image pull or initdb")
	if cancels := ops.Cancels(); len(cancels) != 1 || cancels[0].String() != id {
		t.Fatalf("cancels = %v", cancels)
	}
	asJSON := executeOps(t, storefront(t), nil, ops, "cancel", "operation/"+id, "--output", "json")
	contains(t, "json", asJSON.stdout, `"cancelRequested": true`)
	contains(t, "json", asJSON.stdout, "image pull or initdb")
}

func TestInspectStateShowsOwnershipAndFreshnessWithNoSecret(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{InspectFunc: func(context.Context, application.InspectQuery) (application.Inspection, error) {
		return application.EnvironmentState{Generation: 4, Revision: 2, ScopeUID: "sc-01J9Z8K3M4N5P6Q7R8S9T0V1W2", ComposeProject: "storefront-local",
			Objects: []application.RealizedObject{{Ref: "service/api", State: "running",
				Mappings: []application.Mapping{{Kind: "container", Name: "storefront-local-api-1", ID: "c0ffee"}}}}}, nil
	}}
	vars := map[string]string{"SHOP_API_TOKEN": fixture.Canary}
	text := executeOps(t, storefront(t), vars, ops, "inspect", "workload/api", "--view", "state")
	for _, want := range []string{"workload/api in environment local", "freshness: generation 4, accepted revision 2", "owned container storefront-local-api-1 (c0ffee)"} {
		contains(t, "text", text.stdout, want)
	}
	asJSON := executeOps(t, storefront(t), vars, ops, "inspect", "workload/api", "--view", "state", "--output", "json")
	var doc struct {
		Generation int64 `json:"generation"`
		Objects    []struct {
			Ref      string `json:"ref"`
			Mappings []struct{ Kind, Name, ID string }
		} `json:"objects"`
	}
	if err := json.Unmarshal([]byte(asJSON.stdout), &doc); err != nil || doc.Generation != 4 || len(doc.Objects) != 1 || doc.Objects[0].Mappings[0].ID != "c0ffee" {
		t.Fatalf("state json = %+v, %v\n%s", doc, err, asJSON.stdout)
	}
	for _, out := range []string{text.stdout, text.stderr, asJSON.stdout, asJSON.stderr} {
		if strings.Contains(out, fixture.Canary) {
			t.Fatalf("a secret value reached the output:\n%s", out)
		}
	}
}

func TestInspectStateTakesTheEnvironmentOfAnEnvReference(t *testing.T) {
	t.Parallel()
	files := fixture.StorefrontFiles()
	files["stackpuddle.yaml"] += "    ci:\n      runtime:\n        type: compose\n        context: ci-runner\n      secrets:\n        provider: env\n"
	dir := fixture.Dir(t, files)
	ops := &fakeops.Operations{InspectFunc: func(context.Context, application.InspectQuery) (application.Inspection, error) {
		return application.EnvironmentState{Generation: 1, Revision: 1}, nil
	}}
	got := executeOps(t, dir, nil, ops, "inspect", "env/ci", "--view", "state")
	contains(t, "text", got.stdout, "env/ci in environment ci")
	mismatch := execute(t, dir, nil, "inspect", "env/ci", "--view", "state", "--env", "local")
	if mismatch.exit != cli.ExitUsage {
		t.Fatalf("--env local with env/ci: exit %d, want %d\n%s", mismatch.exit, cli.ExitUsage, mismatch.stderr)
	}
	contains(t, "stderr", mismatch.stderr, "selection.environment_mismatch")
}

func TestRemoveCutsTheDeclarationAndSaysNothingPhysicalChanged(t *testing.T) {
	t.Parallel()
	dir := storefront(t)
	text := execute(t, dir, nil, "remove", "module/audit-copy")
	if text.exit != cli.ExitOK {
		t.Fatalf("exit = %d\n%s", text.exit, text.stderr)
	}
	contains(t, "text", text.stdout, "removed module/audit-copy from stackpuddle.yaml")
	contains(t, "text", text.stdout, "retained until destroy")
	data, err := os.ReadFile(filepath.Join(dir, "stackpuddle.yaml"))
	if err != nil || strings.Contains(string(data), "audit-copy") {
		t.Fatalf("the declaration is still there: %v", err)
	}
	again := execute(t, dir, nil, "remove", "module/audit-copy", "--output", "json")
	if again.exit != cli.ExitNotFound {
		t.Fatalf("second remove exit = %d, want %d", again.exit, cli.ExitNotFound)
	}
}

func TestCheckAndInspectConfigNeverTouchTheEngine(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{
		SubmitFunc: func(context.Context, application.SubmitRequest) (application.SubmitResult, error) {
			t.Error("Submit was called")
			return application.SubmitResult{}, nil
		},
		InspectFunc: func(context.Context, application.InspectQuery) (application.Inspection, error) {
			t.Error("Inspect was called")
			return application.EnvironmentState{}, nil
		},
	}
	dir := fixture.Dir(t, fixture.ShopFiles(nil))
	executeOps(t, dir, nil, ops, "check")
	executeOps(t, dir, nil, ops, "inspect", "workload/api", "--explain")
	executeOps(t, dir, nil, ops, "--version")
}
