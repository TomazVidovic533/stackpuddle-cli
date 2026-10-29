package project_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/project"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fixture"
)

const minimal = `apiVersion: stackpuddle/v1
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

func open(t *testing.T, cwd string, sel application.Selection) (application.LoadedProject, error) {
	t.Helper()
	var got application.LoadedProject
	err := project.NewSource(func() (string, error) { return cwd, nil }).With(context.Background(), sel, application.LoadOptions{},
		func(_ context.Context, p application.LoadedProject) error {
			got = p
			return nil
		})
	return got, err
}

func wantError(t *testing.T, err error, class application.Class, code string) {
	t.Helper()
	appErr, ok := errors.AsType[*application.Error](err)
	if !ok {
		t.Fatalf("err = %v, want *application.Error %s", err, code)
	}
	if appErr.Class() != class || appErr.Code() != code {
		t.Fatalf("err = %s/%s, want %s/%s", appErr.Class(), appErr.Code(), class, code)
	}
}

func TestDefaultRootIsFound(t *testing.T) {
	t.Parallel()
	dir := fixture.Dir(t, map[string]string{"stackpuddle.yaml": minimal})
	p, err := open(t, dir, application.Selection{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if p.RootFile != "stackpuddle.yaml" || len(p.Diagnostics) != 0 {
		t.Fatalf("root %q diagnostics %v, want a clean stackpuddle.yaml", p.RootFile, p.Diagnostics)
	}
}

func TestYmlSpellingIsFoundWhenAlone(t *testing.T) {
	t.Parallel()
	dir := fixture.Dir(t, map[string]string{"stackpuddle.yml": minimal})
	p, err := open(t, dir, application.Selection{})
	if err != nil || p.RootFile != "stackpuddle.yml" {
		t.Fatalf("root %q err %v, want stackpuddle.yml", p.RootFile, err)
	}
}

func TestTwoCandidatesFailWithoutGuessing(t *testing.T) {
	t.Parallel()
	dir := fixture.Dir(t, map[string]string{"stackpuddle.yaml": minimal, "stackpuddle.yml": minimal})
	_, err := open(t, dir, application.Selection{})
	wantError(t, err, application.ClassValidation, "project.root_ambiguous")
	_, err = open(t, "/", application.Selection{File: dir, HasFile: true})
	wantError(t, err, application.ClassValidation, "project.root_ambiguous")
}

func TestFileFlagWinsOverTheDefault(t *testing.T) {
	t.Parallel()
	dir := fixture.Dir(t, map[string]string{"stackpuddle.yaml": minimal, "other/custom.yaml": minimal})
	p, err := open(t, dir, application.Selection{File: "other/custom.yaml", HasFile: true})
	if err != nil || p.RootFile != "custom.yaml" {
		t.Fatalf("root %q err %v, want custom.yaml", p.RootFile, err)
	}
}

func TestNoRootIsNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := open(t, dir, application.Selection{})
	wantError(t, err, application.ClassNotFound, "project.root_not_found")
	_, err = open(t, dir, application.Selection{File: "missing.yaml", HasFile: true})
	wantError(t, err, application.ClassNotFound, "project.root_not_found")
}

func TestProjectFilesCannotLeaveTheRootDirectory(t *testing.T) {
	t.Parallel()
	dir := fixture.Dir(t, map[string]string{
		"outside/fragment.yaml": "apiVersion: stackpuddle/v1\nkind: Fragment\nspec: {}\n",
		"root/stackpuddle.yaml": minimal + "  imports:\n    - ../outside/fragment.yaml\n",
	})
	p, err := open(t, filepath.Join(dir, "root"), application.Selection{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if len(p.Diagnostics) == 0 {
		t.Fatal("an import outside the root directory loaded cleanly")
	}
}

func TestSymlinkOutOfTheRootIsNotFollowed(t *testing.T) {
	t.Parallel()
	dir := fixture.Dir(t, map[string]string{"outside/stackpuddle.yaml": minimal, "root/placeholder": ""})
	if err := os.Symlink(filepath.Join(dir, "outside", "stackpuddle.yaml"), filepath.Join(dir, "root", "stackpuddle.yaml")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	p, err := open(t, filepath.Join(dir, "root"), application.Selection{})
	if err == nil && len(p.Diagnostics) == 0 {
		t.Fatal("a symlink leading out of the root directory was followed")
	}
}

func TestCancelledContextStopsBeforeLoading(t *testing.T) {
	t.Parallel()
	dir := fixture.Dir(t, map[string]string{"stackpuddle.yaml": minimal})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := project.NewSource(func() (string, error) { return dir, nil }).With(ctx, application.Selection{}, application.LoadOptions{},
		func(context.Context, application.LoadedProject) error { called = true; return nil })
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("err = %v, called = %v, want context.Canceled and no call", err, called)
	}
}
