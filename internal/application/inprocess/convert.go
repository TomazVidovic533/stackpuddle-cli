package inprocess

import (
	"fmt"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-control-plane/engine"
)

func commandOf(c application.Command) (engine.Command, error) {
	switch c {
	case application.CommandUp:
		return engine.CommandUp, nil
	case application.CommandStop:
		return engine.CommandStop, nil
	case application.CommandDestroy:
		return engine.CommandDestroy, nil
	}
	return 0, application.Unexpected("engine", fmt.Errorf("unknown command %q", c))
}

func selectionOf(t application.Targets) engine.Selection {
	if t.All {
		return engine.SelectAll()
	}
	return engine.SelectRefs(t.Refs...)
}

func inputsOf(in []application.Input) []engine.InputValue {
	out := make([]engine.InputValue, 0, len(in))
	for _, i := range in {
		if value, ok := i.Secret(); ok {
			out = append(out, engine.SecretInput(i.Key(), engine.NewSecretValue([]byte(value.Reveal()))))
			continue
		}
		text, _ := i.Text()
		out = append(out, engine.PublicInput(i.Key(), text))
	}
	return out
}

// recordOf maps an engine operation to the application record. An unknown enum spelling is an unexpected
// error, so a new engine status never reaches a renderer unnoticed.
func recordOf(op engine.Operation) (application.OperationRecord, error) {
	command, ok := application.ParseCommand(op.Command().String())
	if !ok {
		return application.OperationRecord{}, unknown("command", op.Command().String())
	}
	status, ok := application.ParseStatus(op.Status().String())
	if !ok {
		return application.OperationRecord{}, unknown("status", op.Status().String())
	}
	actor, ok := application.ParseActorKind(op.Actor().String())
	if !ok {
		return application.OperationRecord{}, unknown("actor", op.Actor().String())
	}
	id, err := idOf(op.ID().String())
	if err != nil {
		return application.OperationRecord{}, err
	}
	scope := op.Scope()
	record := application.OperationRecord{
		ID:       id,
		Scope:    application.Scope{Project: scope.Project, Environment: scope.Environment},
		ScopeUID: op.ScopeUID(), Command: command, All: op.SelectedAll(), Refs: op.SelectionRefs(),
		ExpandedScope: op.ExpandedScope(), Revision: op.Revision(), Status: status, Actor: actor,
		CreatedAt: op.CreatedAt().UTC(), UpdatedAt: op.UpdatedAt().UTC(),
	}
	if prior, ok := op.Continues(); ok {
		if record.Continues, err = idOf(prior.String()); err != nil {
			return application.OperationRecord{}, err
		}
	}
	if next, ok := op.ContinuedBy(); ok {
		if record.ContinuedBy, err = idOf(next.String()); err != nil {
			return application.OperationRecord{}, err
		}
	}
	if finished, ok := op.FinishedAt(); ok {
		record.FinishedAt = finished.UTC()
	}
	if problem, ok := op.Problem(); ok {
		record.Problem = &application.Problem{Code: string(problem.Code), Category: problem.Category.String()}
	}
	for _, s := range op.Steps() {
		step, err := stepOf(s)
		if err != nil {
			return application.OperationRecord{}, err
		}
		record.Steps = append(record.Steps, step)
	}
	return record, nil
}

func stepOf(s engine.Step) (application.StepRecord, error) {
	status, ok := application.ParseStepStatus(s.Status.String())
	if !ok {
		return application.StepRecord{}, unknown("step status", s.Status.String())
	}
	step := application.StepRecord{ID: s.ID, Action: s.Action, Object: s.Object, Status: status}
	if started, ok := s.StartedAt(); ok {
		step.StartedAt = started.UTC()
	}
	if finished, ok := s.FinishedAt(); ok {
		step.FinishedAt = finished.UTC()
	}
	if s.Problem != nil {
		step.Problem = &application.Problem{Code: string(s.Problem.Code), Category: s.Problem.Category.String()}
	}
	if job, ok := s.Job(); ok {
		step.Job = &application.JobResult{ExitCode: job.ExitCode}
	}
	return step, nil
}

// idOf wraps an identifier the engine already validated. The application parser accepts exactly the
// spelling the engine writes, so a failure here means the engine broke its own contract and is reported as
// unexpected.
func idOf(text string) (application.OperationID, error) {
	id, err := application.ParseOperationID(text)
	if err != nil {
		return application.OperationID{}, unknown("operation id", text)
	}
	return id, nil
}

func unknown(what, text string) error {
	return application.Unexpected("engine", fmt.Errorf("the engine reported an unknown %s %q", what, text))
}

func previewOf(p engine.Preview) (application.Preview, error) {
	out := application.Preview{BasedOnGeneration: p.BasedOnGeneration()}
	for _, a := range p.Actions() {
		effect, ok := application.ParseEffect(a.Effect.String())
		if !ok {
			return application.Preview{}, unknown("effect", a.Effect.String())
		}
		out.Actions = append(out.Actions, application.Action{
			Action: a.Action, Object: a.Object, Effect: effect, Unknown: a.Unknown, Problem: string(a.Problem),
		})
	}
	return out, nil
}

func environmentOf(v engine.EnvironmentView) (application.EnvironmentState, error) {
	state := application.EnvironmentState{
		Generation: v.Generation(), Revision: v.Revision(), ScopeUID: v.ScopeUID(), ComposeProject: v.ComposeProject(),
	}
	for _, o := range v.Objects() {
		object := application.RealizedObject{Ref: o.Ref, State: o.State}
		for _, m := range o.Mappings {
			object.Mappings = append(object.Mappings, application.Mapping{Kind: m.Kind, Name: m.Name, ID: m.ID})
		}
		state.Objects = append(state.Objects, object)
	}
	if open, ok := v.OpenOperation(); ok {
		record, err := recordOf(open)
		if err != nil {
			return application.EnvironmentState{}, err
		}
		state.Open, state.HasOpen = record, true
	}
	return state, nil
}
