package project_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/project"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fixture"
	"github.com/TomazVidovic533/stackpuddle/model"
)

func removeIn(t *testing.T, dir string, sel application.Selection, ref string) (application.Removed, error) {
	t.Helper()
	parsed, err := model.ParseRef(ref)
	if err != nil {
		t.Fatalf("ParseRef(%q): %v", ref, err)
	}
	return project.NewEditor(func() (string, error) { return dir, nil }).Remove(context.Background(), sel, parsed)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRemoveCutsOnlyTheDeclarationLines(t *testing.T) {
	t.Parallel()
	dir := fixture.Dir(t, fixture.StorefrontFiles())
	root := filepath.Join(dir, "stackpuddle.yaml")
	before := readFile(t, root)
	removed, err := removeIn(t, dir, application.Selection{}, "module/audit-copy")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if removed.File != "stackpuddle.yaml" || removed.Ref.String() != "module/audit-copy" {
		t.Fatalf("removed = %+v", removed)
	}
	want := strings.Replace(before, "    audit-copy:\n      definition: audit-store\n      options:\n        retentionDays: 7\n", "", 1)
	if want == before {
		t.Fatal("the test fixture changed: the audit-copy block was not found")
	}
	if got := readFile(t, root); got != want {
		t.Fatalf("only the declaration may change.\n--- want\n%s\n--- got\n%s", want, got)
	}
	if !strings.Contains(readFile(t, root), "# api serves the storefront and the back office.") {
		t.Fatal("a comment outside the declaration was lost")
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, ".stackpuddle-edit-*")); len(entries) != 0 {
		t.Fatalf("a temporary file was left behind: %v", entries)
	}
}

func TestRemoveKeepsTheFileMode(t *testing.T) {
	t.Parallel()
	dir := fixture.Dir(t, fixture.StorefrontFiles())
	root := filepath.Join(dir, "stackpuddle.yaml")
	if err := os.Chmod(root, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := removeIn(t, dir, application.Selection{}, "module/audit-copy"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	info, err := os.Stat(root)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, %v; want 0640", info.Mode().Perm(), err)
	}
}

func TestRemoveThatLeavesADanglingReferenceWritesNothing(t *testing.T) {
	t.Parallel()
	for _, ref := range []string{"workload/api", "workload/web", "workload/migrate", "workload/backoffice"} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()
			dir := fixture.Dir(t, fixture.StorefrontFiles())
			root := filepath.Join(dir, "stackpuddle.yaml")
			before := readFile(t, root)
			_, err := removeIn(t, dir, application.Selection{}, ref)
			appErr, ok := asApp(err)
			if !ok || appErr.Class() != application.ClassValidation || appErr.Code() != "remove.leaves_invalid_project" {
				t.Fatalf("err = %v, want remove.leaves_invalid_project", err)
			}
			if len(appErr.Findings()) == 0 {
				t.Error("the refusal carries no findings")
			}
			if got := readFile(t, root); got != before {
				t.Fatalf("the file changed although the removal was refused:\n%s", got)
			}
		})
	}
}

func TestRemoveOfAnUnknownDeclarationIsNotFound(t *testing.T) {
	t.Parallel()
	dir := fixture.Dir(t, fixture.StorefrontFiles())
	_, err := removeIn(t, dir, application.Selection{}, "module/missing")
	wantError(t, err, application.ClassNotFound, "project.unknown_object")
}

func TestRemoveRefusesAnSDKRootWithEditGuidance(t *testing.T) {
	t.Parallel()
	loaded, diags := model.Load(fixture.MapFS(fixture.StorefrontFiles()), "stackpuddle.yaml", nil)
	if len(diags) > 0 {
		t.Fatalf("load: %v", diags)
	}
	files := fixture.AuditDefinition()
	// The Model document records where each declaration was written; an SDK writes "sdk" there.
	files["stackpuddle.model.json"] = strings.ReplaceAll(string(model.Encode(loaded)), `"origin": "yaml"`, `"origin": "sdk"`)
	dir := fixture.Dir(t, files)
	sel := application.Selection{File: "stackpuddle.model.json", HasFile: true}
	_, err := removeIn(t, dir, sel, "module/audit-copy")
	wantError(t, err, application.ClassValidation, "remove.sdk_root")
	appErr, _ := asApp(err)
	for _, want := range []string{"module/audit-copy", "SDK source", "regenerate"} {
		if !strings.Contains(appErr.Error(), want) {
			t.Errorf("guidance lacks %q: %s", want, appErr.Error())
		}
	}
}

func TestRemoveRefusesASymlinkedRootFile(t *testing.T) {
	t.Parallel()
	dir := fixture.Dir(t, fixture.StorefrontFiles())
	if err := os.Rename(filepath.Join(dir, "stackpuddle.yaml"), filepath.Join(dir, "real.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.yaml", filepath.Join(dir, "stackpuddle.yaml")); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}
	before := readFile(t, filepath.Join(dir, "real.yaml"))
	_, err := removeIn(t, dir, application.Selection{}, "module/audit-copy")
	wantError(t, err, application.ClassValidation, "remove.not_editable")
	if readFile(t, filepath.Join(dir, "real.yaml")) != before {
		t.Fatal("the symlink target was edited")
	}
}

func TestRemoveRefusesAFlowStyleMapping(t *testing.T) {
	t.Parallel()
	flow := strings.Replace(fixture.Storefront, `  modules:
    audit:
      definition: audit-store
      options:
        retentionDays: 30
    audit-copy:
      definition: audit-store
      options:
        retentionDays: 7
`, "  modules: {audit: {definition: audit-store, options: {retentionDays: 30}}, audit-copy: {definition: audit-store, options: {retentionDays: 7}}}\n", 1)
	if flow == fixture.Storefront {
		t.Fatal("the test fixture changed")
	}
	files := fixture.StorefrontFiles()
	files["stackpuddle.yaml"] = flow
	dir := fixture.Dir(t, files)
	_, err := removeIn(t, dir, application.Selection{}, "module/audit-copy")
	wantError(t, err, application.ClassValidation, "remove.not_editable")
	if readFile(t, filepath.Join(dir, "stackpuddle.yaml")) != flow {
		t.Fatal("the file changed")
	}
}

func TestRemoveStopsOnACancelledContext(t *testing.T) {
	t.Parallel()
	dir := fixture.Dir(t, fixture.StorefrontFiles())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ref, _ := model.ParseRef("module/audit-copy")
	_, err := project.NewEditor(func() (string, error) { return dir, nil }).Remove(ctx, application.Selection{}, ref)
	if err == nil {
		t.Fatal("remove ran on a cancelled context")
	}
}

func TestRemovingTheLastEntryRemovesTheEmptyMappingToo(t *testing.T) {
	t.Parallel()
	dir := fixture.Dir(t, fixture.StorefrontFiles())
	root := filepath.Join(dir, "stackpuddle.yaml")
	for _, ref := range []string{"module/audit-copy", "module/audit"} {
		if _, err := removeIn(t, dir, application.Selection{}, ref); err != nil {
			t.Fatalf("remove %s: %v", ref, err)
		}
	}
	got := readFile(t, root)
	if strings.Contains(got, "modules:") || strings.Contains(got, "audit") && strings.Contains(got, "retentionDays") {
		t.Fatalf("the empty modules mapping was left behind:\n%s", got)
	}
	if !strings.Contains(got, "  workloads:") || !strings.Contains(got, "  applications:") {
		t.Fatalf("the neighbours of the removed mapping were damaged:\n%s", got)
	}
}

func asApp(err error) (*application.Error, bool) { return errors.AsType[*application.Error](err) }

func TestRemoveOfADeclarationWhoseFileIsNotHereIsNotEditable(t *testing.T) {
	t.Parallel()
	loaded, diags := model.Load(fixture.MapFS(fixture.StorefrontFiles()), "stackpuddle.yaml", nil)
	if len(diags) > 0 {
		t.Fatalf("load: %v", diags)
	}
	files := fixture.AuditDefinition()
	files["stackpuddle.model.json"] = string(model.Encode(loaded)) // origins say yaml, but no YAML file is here
	dir := fixture.Dir(t, files)
	_, err := removeIn(t, dir, application.Selection{File: "stackpuddle.model.json", HasFile: true}, "module/audit-copy")
	wantError(t, err, application.ClassValidation, "remove.not_editable")
}
