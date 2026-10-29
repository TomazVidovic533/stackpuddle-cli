// Package project is the adapter that finds a project root on disk and loads it. Every path comes from
// the user or from project files, so the directory opens as an os.Root: a path or symlink that leaves it
// fails. It implements application.ProjectSource.
package project

import (
	"context"
	"fmt"
	"os"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// Source implements application.ProjectSource over the local filesystem.
type Source struct {
	getwd func() (string, error)
}

// NewSource returns a Source that resolves relative paths against getwd().
func NewSource(getwd func() (string, error)) *Source { return &Source{getwd: getwd} }

// With finds the root for sel, loads it through an os.Root with opts.Packaged and calls use while the root is open.
// Filesystem work is bounded by the model's per-file limit and ctx is checked before each step; model.Load
// itself does not take a context, so cancellation lands after a load that already started.
func (s *Source) With(ctx context.Context, sel application.Selection, opts application.LoadOptions,
	use func(ctx context.Context, p application.LoadedProject) error,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("opening the project: %w", err)
	}
	cwd, err := s.getwd()
	if err != nil {
		return application.Unexpected("project", err)
	}
	dir, name, err := locate(cwd, sel)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return application.Unexpected("project", err)
	}
	defer root.Close()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("opening the project: %w", err)
	}
	fsys := root.FS()
	project, diags := model.Load(fsys, name, opts.Packaged)
	return use(ctx, application.LoadedProject{
		Project: project, Diagnostics: diags, Root: fsys, RootFile: name, Dir: dir,
	})
}
