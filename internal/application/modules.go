package application

import (
	"context"
	"slices"

	"github.com/TomazVidovic533/stackpuddle/model"
)

// withProject loads the selected project and calls use with it. A project whose Manifest lists packaged
// modules loads twice: once to read its Lock, then again with the definitions the resolver verified. The
// second load must report the same Lock and no unresolved module, or the project is refused, so use never
// sees a half-resolved project. A project with nothing unresolved loads once and never touches mods.
func withProject(ctx context.Context, src ProjectSource, mods ModuleResolver, sel Selection,
	use func(ctx context.Context, p LoadedProject) error,
) error {
	return src.With(ctx, sel, LoadOptions{}, func(ctx context.Context, first LoadedProject) error {
		if len(first.Diagnostics) > 0 || len(first.Project.Unresolved()) == 0 {
			return use(ctx, first)
		}
		lock, ok := first.Project.Lock()
		if !ok {
			return NewError(ClassValidation, "project.lock_missing",
				"the project lists packaged modules but names no Lock; add one before running an operation")
		}
		if mods == nil {
			return Unexpected("modules", errNoResolver{})
		}
		defs, err := mods.Resolve(ctx, lock)
		if err != nil {
			return err
		}
		return src.With(ctx, sel, LoadOptions{Packaged: defs}, func(ctx context.Context, second LoadedProject) error {
			if len(second.Diagnostics) > 0 {
				return use(ctx, second)
			}
			if !sameLock(lock, second.Project) || len(second.Project.Unresolved()) > 0 {
				return NewError(ClassValidation, "project.modules_unresolved",
					"the verified module definitions do not resolve every packaged module of the project")
			}
			return use(ctx, second)
		})
	})
}

type errNoResolver struct{}

func (errNoResolver) Error() string {
	return "the module resolver is not wired at the composition root"
}

func sameLock(want model.Lock, p model.Project) bool {
	got, ok := p.Lock()
	return ok && sameArtifact(got.Bundle(), want.Bundle()) &&
		slices.EqualFunc(got.Modules(), want.Modules(), func(a, b model.LockedModule) bool {
			return a.Name.String() == b.Name.String() && sameArtifact(a.LockedArtifact, b.LockedArtifact)
		})
}

func sameArtifact(a, b model.LockedArtifact) bool {
	return a.Version.String() == b.Version.String() && a.Source == b.Source && a.Digest == b.Digest
}
