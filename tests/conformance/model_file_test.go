// Package conformance_test runs the CLI over stackpuddle's conformance cases.
package conformance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/project"
)

const modelModule = "github.com/TomazVidovic533/stackpuddle"

// stackpuddleDir returns the directory of the stackpuddle module this build resolves: the workspace tree
// today, the pinned module after the re-pin. It fails by name when the directory is missing.
func stackpuddleDir(t *testing.T) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "go", "list", "-m", "-json", modelModule)
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -m %s: %v", modelModule, err)
	}
	var mod struct{ Dir string }
	if err := json.Unmarshal(out, &mod); err != nil {
		t.Fatalf("go list output: %v", err)
	}
	if mod.Dir == "" {
		t.Fatalf("module %s has no directory in this build; the conformance cases cannot be read", modelModule)
	}
	if _, err := os.Stat(filepath.Join(mod.Dir, "tests", "conformance", "cases", "valid")); err != nil {
		t.Fatalf("module %s at %s has no conformance cases: %v", modelModule, mod.Dir, err)
	}
	return mod.Dir
}

// shape is what must agree between a project and its canonical model: everything but locations.
func shape(r diagnostics.Report) []string {
	var out []string
	for _, res := range r.Results {
		env, _ := res.Environment()
		line := fmt.Sprintf("%s/%s/%s/%s", res.Checker, res.Stage, res.State, env)
		var codes []string
		for _, f := range res.Findings {
			codes = append(codes, f.Code)
		}
		slices.Sort(codes)
		out = append(out, line+":"+strings.Join(codes, ","))
	}
	return append(out, "outcome:"+r.Outcome().String())
}

func check(t *testing.T, dir, file string) diagnostics.Report {
	t.Helper()
	source := project.NewSource(func() (string, error) { return dir, nil })
	run := application.NewCheckProject(source, []application.Checker{
		application.StructureChecker(), application.ConfigurationChecker(func(string) (string, bool) { return "", false }),
	})
	report, err := run(context.Background(), application.CheckRequest{
		Selection: application.Selection{File: file, HasFile: true},
	})
	if err != nil {
		t.Fatalf("check --file %s: %v", file, err)
	}
	return report
}

func TestCanonicalModelFileChecksLikeItsYAMLTwin(t *testing.T) {
	t.Parallel()
	cases := filepath.Join(stackpuddleDir(t), "tests", "conformance", "cases", "valid")
	entries, err := os.ReadDir(cases)
	if err != nil {
		t.Fatalf("read cases: %v", err)
	}
	ran := 0
	for _, e := range entries {
		caseDir := filepath.Join(cases, e.Name())
		modelFile := filepath.Join(caseDir, "expected", "model.json")
		if _, err := os.Stat(modelFile); err != nil {
			continue
		}
		ran++
		t.Run(e.Name(), func(t *testing.T) {
			t.Parallel()
			var meta struct{ Root string }
			raw, err := os.ReadFile(filepath.Join(caseDir, "case.json"))
			if err != nil {
				t.Fatalf("read case.json: %v", err)
			}
			if err := json.Unmarshal(raw, &meta); err != nil || meta.Root == "" {
				t.Fatalf("case.json root: %v", err)
			}
			twin := shape(check(t, caseDir, meta.Root))
			model := shape(check(t, withModel(t, caseDir, modelFile), "model.json"))
			if !slices.Equal(twin, model) {
				t.Fatalf("results differ\nYAML:  %v\nmodel: %v", twin, model)
			}
		})
	}
	if ran == 0 {
		t.Fatal("no valid case has an expected model.json")
	}
}

// withModel copies a case into a new directory and adds its canonical model next to the files the model
// refers to, so relative references (local definitions, the manifest) resolve as they do for the YAML twin.
func withModel(t *testing.T, caseDir, modelFile string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(caseDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(caseDir, path)
		if err != nil {
			return err
		}
		if d.IsDir() && rel == "expected" {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatalf("copy case: %v", err)
	}
	data, err := os.ReadFile(modelFile)
	if err != nil {
		t.Fatalf("read model: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dst, "model.json"), data, 0o600); err != nil {
		t.Fatalf("write model: %v", err)
	}
	return dst
}
