package application

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// LiveCheckerName is the name of the live checker in a report.
const LiveCheckerName = "live"

// Codes the live checker reports besides the engine's own.
const (
	codeLiveUnresolved = "live.project_unresolved"
	codeLiveUndecided  = "live.undecided"
	codeLiveUnknown    = "live.step_unknown"
	codeKeyMissing     = "secrets.key_missing"
	codeTargetDown     = "runtime.target_unreachable"
	ownershipPrefix    = "ownership."
)

// LiveChecker returns the checker that asks the running system whether `up` could proceed, per selected
// environment. It submits a dry-run `up` through the port, which takes no lock and writes nothing, so it runs
// no hidden mutating probe. A stopped Docker daemon or a missing secrets key is unknown, never pass; a blocked
// row with an ownership code (a lost data object) fails and names the consumers that bind the object; a step
// the plan cannot decide is unknown. now stamps the freshness; the preview's generation completes it.
func LiveChecker(ops Operations, lookup func(string) (string, bool), now func() time.Time) Checker {
	return Checker{
		Name:  LiveCheckerName,
		Stage: diagnostics.StageLive,
		Live:  true,
		Run: func(ctx context.Context, p LoadedProject, envs []model.EnvironmentName) ([]diagnostics.Result, error) {
			results := make([]diagnostics.Result, 0, len(envs))
			for _, env := range slices.Clone(envs) {
				result, err := liveEnvironment(ctx, p, env, ops, lookup, now)
				if err != nil {
					return nil, err
				}
				results = append(results, result.ForEnvironment(env))
			}
			return results, nil
		},
	}
}

func liveEnvironment(ctx context.Context, p LoadedProject, env model.EnvironmentName, ops Operations,
	lookup func(string) (string, bool), now func() time.Time,
) (diagnostics.Result, error) {
	result := diagnostics.Result{Checker: LiveCheckerName, Stage: diagnostics.StageLive, State: diagnostics.StateUnknown, ObservedAt: now().UTC()}
	if len(p.Project.Unresolved()) > 0 {
		result.Findings = []diagnostics.Finding{{Code: codeLiveUnresolved, Message: "the packaged modules are not resolved, so the live state cannot be planned"}}
		return result, nil
	}
	inputs, err := submitInputs(ctx, p, env, lookup, false)
	if err != nil {
		return undecided(result, err), nil
	}
	res, err := ops.Submit(ctx, SubmitRequest{
		Scope: Scope{Project: p.Project.Name(), Environment: env}, Project: p.Project, ProjectDir: p.Dir,
		Command: CommandUp, Targets: Targets{All: true}, Inputs: inputs, DryRun: true,
	})
	if err != nil {
		if ctx.Err() != nil {
			return diagnostics.Result{}, fmt.Errorf("live check: %w", ctx.Err())
		}
		return failedSubmit(result, err), nil
	}
	result.Generation = res.Preview.BasedOnGeneration
	return judge(p.Project, result, res.Preview), nil
}

func undecided(r diagnostics.Result, err error) diagnostics.Result {
	r.State = diagnostics.StateUnknown
	r.Findings = []diagnostics.Finding{{Code: codeLiveUndecided, Message: "the live state could not be planned: " + err.Error()}}
	if appErr, ok := errors.AsType[*Error](err); ok {
		r.Findings[0].Code = appErr.Code()
	}
	return r
}

// failedSubmit maps a refused dry run: a daemon that cannot be reached or a key that is missing leaves the
// question open; anything else the engine refuses is a failure with its code.
func failedSubmit(r diagnostics.Result, err error) diagnostics.Result {
	code, message := codeLiveUndecided, err.Error()
	class := ClassUnexpected
	if appErr, ok := errors.AsType[*Error](err); ok {
		code, class = appErr.Code(), appErr.Class()
	}
	r.Findings = []diagnostics.Finding{{Code: code, Message: message}}
	if class == ClassUnavailable || code == codeTargetDown || code == codeKeyMissing {
		r.State = diagnostics.StateUnknown
		return r
	}
	r.State = diagnostics.StateFail
	return r
}

func judge(p model.Project, r diagnostics.Result, preview Preview) diagnostics.Result {
	r.State = diagnostics.StatePass
	for _, a := range preview.Actions {
		switch {
		case a.Effect == EffectBlocked:
			r.State = diagnostics.StateFail
			finding := diagnostics.Finding{Code: a.Problem, Message: fmt.Sprintf("%s is blocked: %s", a.Object, a.Problem)}
			if strings.HasPrefix(a.Problem, ownershipPrefix) {
				finding.Consumers = consumersOf(p, a.Object)
				finding.Message = fmt.Sprintf("%s is lost or unowned; up refuses to recreate it. Run destroy, then up", a.Object)
			}
			r.Findings = append(r.Findings, finding)
		case a.Unknown && r.State == diagnostics.StatePass:
			r.State = diagnostics.StateUnknown
			r.Findings = append(r.Findings, diagnostics.Finding{
				Code: codeLiveUnknown, Message: fmt.Sprintf("%s %s may have nothing left to do; the plan cannot say", a.Action, a.Object),
			})
		}
	}
	if r.State == diagnostics.StateFail {
		r.Findings = slices.DeleteFunc(r.Findings, func(f diagnostics.Finding) bool { return f.Code == codeLiveUnknown })
	}
	return r
}

// consumersOf names the objects that bind an export of the object the engine named ("<kind>/<key>" or
// "<kind>/<key>--<part>").
func consumersOf(p model.Project, object string) []string {
	_, name, _ := strings.Cut(object, "/")
	found := map[string]bool{}
	for _, b := range p.Bindings() {
		source := b.Source().Object.String()
		_, key, _ := strings.Cut(source, "/")
		if name == key || strings.HasPrefix(name, key+"--") {
			found[b.Ref().Consumer.String()] = true
		}
	}
	return slices.Sorted(maps.Keys(found))
}
