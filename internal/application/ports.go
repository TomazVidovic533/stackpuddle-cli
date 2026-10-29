package application

import (
	"context"
	"io/fs"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// Selection is what the user chose on the command line. Absence is explicit: File and Environment are
// meaningful only when their Has flag is set.
type Selection struct {
	File           string
	HasFile        bool
	Environment    model.EnvironmentName
	HasEnvironment bool
}

// LoadOptions are the extras a load takes. Packaged holds the definitions of packaged modules; without
// them a project whose Manifest lists a packaged module loads with that module unresolved.
type LoadOptions struct {
	Packaged []model.ModuleDefinition
}

// LoadedProject is a project opened for one command. Diagnostics holds what loading found; Project is the
// zero value when it is not empty. Root reads files relative to the project directory and is valid only
// inside the ProjectSource.With callback. Dir is the absolute project directory.
type LoadedProject struct {
	Project     model.Project
	Diagnostics model.Diagnostics
	Root        fs.FS
	RootFile    string
	Dir         string
}

// ProjectSource finds a project root and loads it. One adapter implements it (internal/project).
type ProjectSource interface {
	// With finds the root for sel, loads it and calls use with the result; the root stays open until use
	// returns. A missing root is a ClassNotFound Error with code project.root_not_found, more than one
	// candidate a ClassValidation Error with code project.root_ambiguous.
	With(ctx context.Context, sel Selection, opts LoadOptions, use func(ctx context.Context, p LoadedProject) error) error
}

// ModuleResolver turns a project Lock into verified module definitions. It fetches or reads each locked
// archive, checks the bytes against the Lock digest before anything else reads them, checks the modules
// against the bundle's tested set and returns the definitions. A digest mismatch is a ClassCompatibility
// Error naming the module; an unreachable source is ClassUnavailable.
type ModuleResolver interface {
	Resolve(ctx context.Context, lock model.Lock) ([]model.ModuleDefinition, error)
}

// Removed says what Remove changed.
type Removed struct {
	Ref model.Ref
	// File is the project file the declaration was cut from, relative to the project directory.
	File string
}

// DeclarationEditor removes a declaration from the YAML composition root and changes nothing else. One
// adapter implements it (internal/project).
type DeclarationEditor interface {
	// Remove cuts the declaration ref names. A root written by an SDK is a ClassValidation Error with
	// the exact edit guidance; a result that no longer loads is refused and nothing is written.
	Remove(ctx context.Context, sel Selection, ref model.Ref) (Removed, error)
}

// Checker is one entry of the list `check` runs. Run receives the environments to check and returns
// results for them; an error means the run was cancelled or failed unexpectedly, not that the project is
// invalid. The composition root builds the list; each checker is one line there. A Live checker asks the
// running system and runs only when the request says Live; otherwise it is left out of the report.
type Checker struct {
	Name  string
	Stage diagnostics.Stage
	Live  bool
	Run   func(ctx context.Context, p LoadedProject, envs []model.EnvironmentName) ([]diagnostics.Result, error)
}
