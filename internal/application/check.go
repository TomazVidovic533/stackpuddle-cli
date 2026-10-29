package application

import (
	"context"
	"fmt"
	"slices"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/config"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
	"github.com/TomazVidovic533/stackpuddle/model"
	"github.com/TomazVidovic533/stackpuddle/validation"
)

// StructureCheckerName is the name of the structure checker in a report.
const StructureCheckerName = "structure"

// CheckRequest is the input of the check use case.
type CheckRequest struct {
	Selection Selection
	// Live also runs the checkers that ask the running system. They take no lock and change nothing.
	Live bool
}

// CheckProject runs the checkers over the selected project and returns their results. It takes no lock,
// reads no state root and starts nothing, so it runs where no engine or Docker exists.
type CheckProject func(ctx context.Context, req CheckRequest) (diagnostics.Report, error)

// NewCheckProject returns the check use case over src and the checker list. A checker of a later stage
// than a failed result reports skipped instead of running.
func NewCheckProject(src ProjectSource, checkers []Checker) CheckProject {
	return NewResolvingCheckProject(src, nil, checkers)
}

// NewResolvingCheckProject is NewCheckProject for checkers that need the packaged module definitions. It asks
// mods for them on a best-effort basis: a project that cannot be resolved (no Lock, nothing cached, source
// unreachable) is checked unresolved, and a checker that needs a definition reports unknown. With the verified
// cache warm it reads no network.
func NewResolvingCheckProject(src ProjectSource, mods ModuleResolver, checkers []Checker) CheckProject {
	return func(ctx context.Context, req CheckRequest) (diagnostics.Report, error) {
		var report diagnostics.Report
		run := func(ctx context.Context, p LoadedProject) error {
			var runErr error
			report, runErr = runCheckers(ctx, p, req, checkers)
			return runErr
		}
		err := src.With(ctx, req.Selection, LoadOptions{}, func(ctx context.Context, first LoadedProject) error {
			defs, ok := resolveQuietly(ctx, first, mods)
			if !ok {
				return run(ctx, first)
			}
			return src.With(ctx, req.Selection, LoadOptions{Packaged: defs}, func(ctx context.Context, second LoadedProject) error {
				if len(second.Diagnostics) > 0 || len(second.Project.Unresolved()) > 0 {
					return run(ctx, first)
				}
				return run(ctx, second)
			})
		})
		return report, err
	}
}

// resolveQuietly returns the verified definitions of a loaded project's packaged modules, or false when there
// is nothing to resolve or they cannot be had. Its failure is not the check's failure.
func resolveQuietly(ctx context.Context, p LoadedProject, mods ModuleResolver) ([]model.ModuleDefinition, bool) {
	if mods == nil || len(p.Diagnostics) > 0 || len(p.Project.Unresolved()) == 0 {
		return nil, false
	}
	lock, ok := p.Project.Lock()
	if !ok {
		return nil, false
	}
	defs, err := mods.Resolve(ctx, lock)
	return defs, err == nil
}

func runCheckers(ctx context.Context, p LoadedProject, req CheckRequest, checkers []Checker) (diagnostics.Report, error) {
	sel := req.Selection
	loaded := len(p.Diagnostics) == 0
	var envs []model.EnvironmentName
	if loaded {
		var err error
		if envs, err = selectEnvironments(p.Project, sel); err != nil {
			return diagnostics.Report{}, err
		}
	}
	var report diagnostics.Report
	failedStage := diagnostics.Stage(0)
	for _, c := range checkers {
		if c.Live && !req.Live {
			continue
		}
		if failedStage != 0 && failedStage < c.Stage {
			report.Results = append(report.Results, skipped(c, envs)...)
			continue
		}
		results, err := c.Run(ctx, p, envs)
		if err != nil {
			return diagnostics.Report{}, fmt.Errorf("checker %s: %w", c.Name, err)
		}
		for _, r := range results {
			if r.State == diagnostics.StateFail && (failedStage == 0 || r.Stage < failedStage) {
				failedStage = r.Stage
			}
		}
		report.Results = append(report.Results, results...)
	}
	return report, nil
}

func skipped(c Checker, envs []model.EnvironmentName) []diagnostics.Result {
	base := diagnostics.Result{Checker: c.Name, Stage: c.Stage, State: diagnostics.StateSkipped}
	if len(envs) == 0 {
		return []diagnostics.Result{base}
	}
	out := make([]diagnostics.Result, 0, len(envs))
	for _, e := range envs {
		out = append(out, base.ForEnvironment(e))
	}
	return out
}

// selectEnvironments returns the selected environment, or every declared one when none was selected.
func selectEnvironments(p model.Project, sel Selection) ([]model.EnvironmentName, error) {
	if sel.HasEnvironment {
		if _, ok := p.Environment(sel.Environment); !ok {
			return nil, unknownEnvironment(sel.Environment)
		}
		return []model.EnvironmentName{sel.Environment}, nil
	}
	envs := p.Environments()
	names := make([]model.EnvironmentName, 0, len(envs))
	for _, e := range envs {
		names = append(names, e.Name())
	}
	return names, nil
}

func unknownEnvironment(name model.EnvironmentName) *Error {
	return NewError(ClassNotFound, "project.unknown_environment", fmt.Sprintf("environment %q is not declared", name))
}

// SemanticCheckerName is the name of the semantic checker in a report.
const SemanticCheckerName = "semantic"

// Codes the semantic checker adds to the validators' own.
const codeModuleUnchecked = "semantic.module_unchecked"

// SemanticChecker returns the checker that applies each module's own rules to the effective options, offline.
// A module whose definition is not resolved, or that has no validator, is reported unknown: the checker never
// says pass for what it did not read.
func SemanticChecker() Checker {
	return Checker{
		Name:  SemanticCheckerName,
		Stage: diagnostics.StageSemantic,
		Run: func(_ context.Context, p LoadedProject, _ []model.EnvironmentName) ([]diagnostics.Result, error) {
			result := diagnostics.Result{Checker: SemanticCheckerName, Stage: diagnostics.StageSemantic, State: diagnostics.StatePass}
			checked, err := validation.Validate(p.Project)
			if err != nil {
				return nil, fmt.Errorf("semantic validation: %w", err)
			}
			if len(checked.Diagnostics) > 0 {
				result.State = diagnostics.StateFail
				result.Findings = diagnostics.FromModel(checked.Diagnostics)
				return []diagnostics.Result{result}, nil
			}
			for _, name := range checked.Unchecked {
				result.State = diagnostics.StateUnknown
				result.Findings = append(result.Findings, diagnostics.Finding{
					Code: codeModuleUnchecked, Message: fmt.Sprintf("module %s was not checked: its definition is not resolved or it has no semantic rules", name),
				})
			}
			return []diagnostics.Result{result}, nil
		},
	}
}

// StructureChecker returns the checker that reports what loading found: syntax, compatibility, structure,
// identity, references, bindings and graph.
func StructureChecker() Checker {
	return Checker{
		Name:  StructureCheckerName,
		Stage: diagnostics.StageStructure,
		Run: func(_ context.Context, p LoadedProject, _ []model.EnvironmentName) ([]diagnostics.Result, error) {
			result := diagnostics.Result{Checker: StructureCheckerName, Stage: diagnostics.StageStructure, State: diagnostics.StatePass}
			if len(p.Diagnostics) > 0 {
				result.State = diagnostics.StateFail
				result.Findings = diagnostics.FromModel(p.Diagnostics.Sorted())
			}
			return []diagnostics.Result{result}, nil
		},
	}
}

// ConfigurationChecker returns the checker that applies the configuration layers to each selected
// environment and reports missing inputs, binding-owned keys and bad local files. lookup is the process
// lookup; it is consulted only for the CI variable and the variable names an environment maps.
func ConfigurationChecker(lookup func(string) (string, bool)) Checker {
	return Checker{
		Name:  config.CheckerName,
		Stage: diagnostics.StageConfiguration,
		Run: func(ctx context.Context, p LoadedProject, envs []model.EnvironmentName) ([]diagnostics.Result, error) {
			results := make([]diagnostics.Result, 0, len(envs))
			for _, env := range slices.Clone(envs) {
				r, err := config.Check(ctx, p.Root, p.RootFile, p.Project, env, lookup)
				if err != nil {
					return nil, err
				}
				results = append(results, r)
			}
			return results, nil
		},
	}
}
