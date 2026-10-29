// Package inprocess is the one adapter that runs the application's operations port in this process, by
// calling the control plane's engine package. Nothing else in the command line imports the engine; the
// service adapter of a later milestone sits beside this one and implements the same port.
package inprocess

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-control-plane/engine"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// Operations implements application.Operations over an in-process engine.
type Operations struct {
	engine func() (*engine.Engine, error)
}

// New returns the adapter. The engine is composed on first use, so check, inspect --view config and --version
// never construct it and read no state root. lookup is the process lookup; now is the clock, nil for time.Now.
func New(lookup func(string) (string, bool), now func() time.Time) *Operations {
	return &Operations{engine: sync.OnceValues(func() (*engine.Engine, error) {
		eng, err := engine.New(engine.Options{LookupEnv: lookup, Now: now})
		if err != nil {
			return nil, classify(err)
		}
		return eng, nil
	})}
}

// Advertise reports what the engine can run, or the error that kept it from being composed.
func (o *Operations) Advertise() (model.EngineInfo, error) {
	eng, err := o.engine()
	if err != nil {
		return model.EngineInfo{}, err
	}
	return eng.Advertise(), nil
}

// Submit changes the environment as req says.
func (o *Operations) Submit(ctx context.Context, req application.SubmitRequest) (application.SubmitResult, error) {
	eng, err := o.engine()
	if err != nil {
		return application.SubmitResult{}, err
	}
	command, err := commandOf(req.Command)
	if err != nil {
		return application.SubmitResult{}, err
	}
	out := engine.SubmitRequest{
		Scope: engine.Scope{Project: req.Scope.Project, Environment: req.Scope.Environment}, Project: req.Project,
		ProjectDir: req.ProjectDir, Command: command, Selection: selectionOf(req.Targets), Inputs: inputsOf(req.Inputs),
		DryRun: req.DryRun, ExpandedScope: req.ExpandedScope, DeleteData: req.DeleteData, Wait: req.Wait,
	}
	progress, progressErr := progressOf(req.Progress)
	out.Progress = progress
	res, submitErr := eng.Submit(ctx, out)
	return resultOf(res, submitErr, *progressErr)
}

// Run invokes one job as its own recorded operation.
func (o *Operations) Run(ctx context.Context, req application.RunRequest) (application.SubmitResult, error) {
	eng, err := o.engine()
	if err != nil {
		return application.SubmitResult{}, err
	}
	out := engine.RunRequest{
		Scope: engine.Scope{Project: req.Scope.Project, Environment: req.Scope.Environment}, Project: req.Project,
		ProjectDir: req.ProjectDir, Job: req.Job, Inputs: inputsOf(req.Inputs), DryRun: req.DryRun, Wait: req.Wait,
	}
	progress, progressErr := progressOf(req.Progress)
	out.Progress = progress
	res, runErr := eng.Run(ctx, out)
	return resultOf(res, runErr, *progressErr)
}

// progressOf adapts the application's progress callback to the engine's. The returned pointer holds the first
// mapping failure, read after the engine call returns.
func progressOf(fn func(application.OperationRecord)) (func(engine.Operation), *error) {
	var failed error
	if fn == nil {
		return nil, &failed
	}
	return func(op engine.Operation) {
		record, err := recordOf(op)
		if err != nil {
			if failed == nil {
				failed = err
			}
			return
		}
		fn(record)
	}, &failed
}

// resultOf maps an engine result and its error to the port's result, classifying a failure.
func resultOf(res engine.Result, callErr, progressErr error) (application.SubmitResult, error) {
	if progressErr != nil {
		return application.SubmitResult{}, progressErr
	}
	result := application.SubmitResult{}
	if op, ok := res.Operation(); ok {
		record, err := recordOf(op)
		if err != nil {
			return application.SubmitResult{}, err
		}
		result.Record, result.HasRecord = record, true
	}
	preview, err := previewOf(res.Preview())
	if err != nil {
		return application.SubmitResult{}, err
	}
	result.Preview = preview
	if callErr != nil {
		return application.SubmitResult{}, classifyRun(callErr, result)
	}
	return result, nil
}

// Inspect reads an environment or one operation without the lock.
func (o *Operations) Inspect(ctx context.Context, q application.InspectQuery) (application.Inspection, error) {
	eng, err := o.engine()
	if err != nil {
		return nil, err
	}
	scope := engine.Scope{Project: q.Scope.Project, Environment: q.Scope.Environment}
	request := engine.InspectEnvironment(scope)
	if !q.Operation.IsZero() {
		id, parseErr := engine.ParseOperationID(q.Operation.String())
		if parseErr != nil {
			return nil, application.NewError(application.ClassUsage, "reference.invalid_ref", "the operation ID is not valid").WithCause(parseErr)
		}
		request = engine.InspectOperation(scope, id)
	}
	inspection, err := eng.Inspect(ctx, request)
	if err != nil {
		return nil, classify(err)
	}
	if op, ok := inspection.Operation(); ok {
		return recordOf(op)
	}
	if env, ok := inspection.Environment(); ok {
		return environmentOf(env)
	}
	return nil, application.Unexpected("engine", fmt.Errorf("the engine returned an empty inspection"))
}

// Cancel asks the running operation to stop at its next step.
func (o *Operations) Cancel(ctx context.Context, scope application.Scope, id application.OperationID) error {
	eng, err := o.engine()
	if err != nil {
		return err
	}
	opID, err := engine.ParseOperationID(id.String())
	if err != nil {
		return application.NewError(application.ClassUsage, "reference.invalid_ref", "the operation ID is not valid").WithCause(err)
	}
	if err := eng.Cancel(ctx, engine.CancelRequest{
		Scope: engine.Scope{Project: scope.Project, Environment: scope.Environment}, Operation: opID,
	}); err != nil {
		return classify(err)
	}
	return nil
}
