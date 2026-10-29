package command_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/cli"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fixture"
)

func TestExitMap(t *testing.T) {
	t.Parallel()
	ambiguous := fixture.Dir(t, map[string]string{"stackpuddle.yaml": fixture.Shop, "stackpuddle.yml": fixture.Shop})
	tests := []struct {
		name string
		dir  string
		args []string
		exit int
		code string
	}{
		{"unknown command", t.TempDir(), []string{"frobnicate"}, cli.ExitUsage, "usage.invalid"},
		{"unknown flag", t.TempDir(), []string{"check", "--bogus"}, cli.ExitUsage, "usage.invalid"},
		{"extra argument", t.TempDir(), []string{"check", "extra"}, cli.ExitUsage, "usage.invalid"},
		{"unsupported output format", t.TempDir(), []string{"check", "--output", "yaml"}, cli.ExitUsage, "usage.invalid_output"},
		{"unsupported view", shop(t, nil), []string{"inspect", "workload/api", "--view", "graph"}, cli.ExitUsage, "inspect.unsupported_view"},
		{"unknown view", shop(t, nil), []string{"inspect", "workload/api", "--view", "nope"}, cli.ExitUsage, "inspect.unknown_view"},
		{"invalid reference", shop(t, nil), []string{"inspect", "nonsense"}, cli.ExitUsage, "reference.invalid_ref"},
		{"unsupported reference kind", shop(t, nil), []string{"inspect", "app/storefront"}, cli.ExitUsage, "inspect.unsupported_ref_kind"},
		{"invalid environment name", shop(t, nil), []string{"check", "--env", "Not.Valid"}, cli.ExitUsage, "usage.invalid_environment"},
		{"missing reference", shop(t, nil), []string{"inspect"}, cli.ExitUsage, "usage.invalid"},
		{"no root file", t.TempDir(), []string{"check"}, cli.ExitNotFound, "project.root_not_found"},
		{"unknown object", shop(t, nil), []string{"inspect", "workload/missing"}, cli.ExitNotFound, "project.unknown_object"},
		{"unknown environment", shop(t, nil), []string{"inspect", "workload/api", "--env", "nowhere"}, cli.ExitNotFound, "project.unknown_environment"},
		{"ambiguous root", ambiguous, []string{"check"}, cli.ExitValidation, "project.root_ambiguous"},
		{"invalid project for inspect", fixture.Dir(t, map[string]string{"stackpuddle.yaml": "apiVersion: stackpuddle/v1\nkind: Project\nmetadata: {name: shop}\nspec: {surprise: 1}\n"}),
			[]string{"inspect", "workload/api"}, cli.ExitValidation, "project.invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := execute(t, tt.dir, nil, append(tt.args, "--non-interactive")...)
			if got.exit != tt.exit {
				t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", got.exit, tt.exit, got.stdout, got.stderr)
			}
			contains(t, "stderr", got.stderr, tt.code)
		})
	}
}

func TestJSONFailureDocument(t *testing.T) {
	t.Parallel()
	got := execute(t, t.TempDir(), nil, "check", "--output", "json")
	var doc struct {
		Error struct{ Class, Code, Message string } `json:"error"`
	}
	if err := json.Unmarshal([]byte(got.stderr), &doc); err != nil {
		t.Fatalf("stderr is not the failure document: %v\n%s", err, got.stderr)
	}
	if doc.Error.Class != "not_found" || doc.Error.Code != "project.root_not_found" || doc.Error.Message == "" {
		t.Fatalf("failure = %+v", doc.Error)
	}
	if got.stdout != "" {
		t.Fatalf("a failure wrote to stdout: %s", got.stdout)
	}
}

func TestVersionFlag(t *testing.T) {
	t.Parallel()
	text := execute(t, t.TempDir(), nil, "--version")
	js := execute(t, t.TempDir(), nil, "--version", "--output", "json")
	if text.exit != cli.ExitOK || js.exit != cli.ExitOK {
		t.Fatalf("exits = %d/%d", text.exit, js.exit)
	}
	var doc struct {
		CLI   struct{ Version, Go, Platform string } `json:"cli"`
		Model struct {
			Version     string   `json:"version"`
			APIVersions []string `json:"apiVersions"`
		} `json:"model"`
	}
	if err := json.Unmarshal([]byte(js.stdout), &doc); err != nil {
		t.Fatalf("version JSON: %v\n%s", err, js.stdout)
	}
	if len(doc.Model.APIVersions) == 0 {
		t.Fatalf("no API versions in %s", js.stdout)
	}
	contains(t, "text", text.stdout, doc.CLI.Version)
	contains(t, "text", text.stdout, doc.CLI.Go)
	contains(t, "text", text.stdout, doc.Model.APIVersions[0])
}

func TestBareRootPrintsHelp(t *testing.T) {
	t.Parallel()
	got := execute(t, t.TempDir(), nil)
	if got.exit != cli.ExitOK || !strings.Contains(got.stdout, "check") || !strings.Contains(got.stdout, "inspect") {
		t.Fatalf("exit %d, output:\n%s", got.exit, got.stdout)
	}
}

func TestCompletionRegistersTheCommandName(t *testing.T) {
	t.Parallel()
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		got := execute(t, t.TempDir(), nil, "completion", shell)
		if got.exit != cli.ExitOK || !strings.Contains(got.stdout, "stackpuddle") {
			t.Errorf("%s completion: exit %d, registers stackpuddle: %v", shell, got.exit, strings.Contains(got.stdout, "stackpuddle"))
		}
	}
}
