package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/config"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// Codes the use cases recognise on an error the engine port returns. The in-process adapter keeps them equal
// to the engine's published codes; a test compares them.
const (
	CodeEnvironmentBusy       = "conflict.environment_busy"
	CodeExpandedScopeRequired = "selection.expanded_scope_required"
	CodeOperationInterrupted  = "operation.interrupted"
	CodeOperationCancelled    = "operation.cancelled"
	codeWaitTimeout           = "conflict.wait_timeout"
)

// PollInterval is how often `inspect operation/<id> --wait|--follow` reads the state file.
const PollInterval = 500 * time.Millisecond

// MutateRequest is one up, stop or destroy: which project, which objects and how.
type MutateRequest struct {
	Selection Selection
	Targets   TargetRequest
	// DryRun previews: no lock, no write.
	DryRun bool
	// ExpandedScope permits a change that reaches unselected consumers.
	ExpandedScope bool
	// DeleteData makes destroy delete the owned volumes too.
	DeleteData bool
	// Wait bounds the wait for a busy environment; zero fails at once.
	Wait time.Duration
	// OnSelection, when set, receives the resolved selection before the engine is called.
	OnSelection func(SelectionSummary)
	// Progress, when set, is called after every persisted change of the operation.
	Progress func(OperationRecord)
}

// MutateResult is what a mutating verb did.
type MutateResult struct {
	Selection SelectionSummary
	DryRun    bool
	// Record is the recorded operation; HasRecord is false for a dry run and for a request with nothing to do.
	Record    OperationRecord
	HasRecord bool
	Preview   Preview
}

// Mutate runs up, stop or destroy: it loads the project, resolves the selection and the inputs, and submits
// the request through the Operations port. A recorded operation that did not succeed is an Error that carries
// the record.
type Mutate func(ctx context.Context, req MutateRequest) (MutateResult, error)

// NewMutate returns the mutating use case over the project source, the module resolver, the operations port
// and the process lookup.
func NewMutate(src ProjectSource, mods ModuleResolver, ops Operations, lookup func(string) (string, bool)) Mutate {
	return func(ctx context.Context, req MutateRequest) (MutateResult, error) {
		var result MutateResult
		err := withProject(ctx, src, mods, req.Selection, func(ctx context.Context, p LoadedProject) error {
			var runErr error
			result, runErr = mutate(ctx, p, req, ops, lookup)
			return runErr
		})
		return result, err
	}
}

func mutate(ctx context.Context, p LoadedProject, req MutateRequest, ops Operations, lookup func(string) (string, bool)) (MutateResult, error) {
	if err := requireValid(p); err != nil {
		return MutateResult{}, err
	}
	sel, err := selectionWithEnvRef(req.Selection, req.Targets.Refs)
	if err != nil {
		return MutateResult{}, err
	}
	env, err := inspectionEnvironment(p.Project, sel)
	if err != nil {
		return MutateResult{}, err
	}
	targets, summary, err := planTargets(p.Project, env, req.Targets)
	if err != nil {
		return MutateResult{}, err
	}
	inputs, err := submitInputs(ctx, p, env, lookup, req.Targets.Command == CommandUp)
	if err != nil {
		return MutateResult{}, err
	}
	if req.OnSelection != nil {
		req.OnSelection(summary)
	}
	submit := SubmitRequest{
		Scope: Scope{Project: p.Project.Name(), Environment: env}, Project: p.Project, ProjectDir: p.Dir,
		Command: req.Targets.Command, Targets: targets, Inputs: inputs, DryRun: req.DryRun,
		ExpandedScope: req.ExpandedScope, DeleteData: req.DeleteData, Wait: req.Wait, Progress: req.Progress,
	}
	res, err := ops.Submit(ctx, submit)
	if err != nil {
		return MutateResult{}, refine(ctx, ops, submit, err)
	}
	return MutateResult{Selection: summary, DryRun: req.DryRun, Record: res.Record, HasRecord: res.HasRecord, Preview: res.Preview}, nil
}

// selectionWithEnvRef lets an env/<name> reference choose the environment. It fails when --env names another.
func selectionWithEnvRef(sel Selection, refs []model.Ref) (Selection, error) {
	name, ok, err := environmentRef(refs)
	if err != nil {
		return sel, err
	}
	if !ok {
		return sel, nil
	}
	if sel.HasEnvironment && sel.Environment != name {
		return sel, NewError(ClassUsage, "selection.environment_mismatch", "--env and the env/<name> reference name different environments")
	}
	sel.Environment, sel.HasEnvironment = name, true
	return sel, nil
}

// requireValid refuses a project that failed to load, with the findings.
func requireValid(p LoadedProject) error {
	if len(p.Diagnostics) > 0 {
		return NewError(ClassValidation, "project.invalid", "the project is invalid; run check for the findings").
			WithFindings(diagnostics.FromModel(p.Diagnostics.Sorted()))
	}
	return nil
}

// submitInputs resolves the inputs of env and hands the supplied ones to the engine. A missing non-secret
// input refuses `up`, which needs every value; stop and destroy pass what is supplied.
func submitInputs(ctx context.Context, p LoadedProject, env model.EnvironmentName, lookup func(string) (string, bool), strict bool) ([]Input, error) {
	policy := config.PolicyFor(lookup)
	files, findings, err := config.LoadLocalFiles(ctx, p.Root, p.Project, env, policy)
	if err != nil {
		return nil, Unexpected("inputs", err)
	}
	if len(findings) > 0 {
		return nil, NewError(ClassValidation, "config.local_file_invalid", "a local file is invalid").WithFindings(findings)
	}
	resolution, missing := config.Resolve(p.Project, env, config.Inputs{
		LocalFiles: files, LookupProcess: lookup, Local: policy, RootFile: p.RootFile,
	})
	if strict && len(missing) > 0 {
		return nil, NewError(ClassValidation, config.CodeInputMissing, "an input has no value; run check for the findings").WithFindings(missing)
	}
	var inputs []Input
	for key, value := range resolution.Inputs {
		if text, ok := value.Text(); ok {
			inputs = append(inputs, PublicInput(key.String(), text))
		} else if secret, ok := value.Secret(); ok {
			inputs = append(inputs, SecretInput(key.String(), secret))
		}
	}
	slices.SortFunc(inputs, func(a, b Input) int { return strings.Compare(a.Key(), b.Key()) })
	return inputs, nil
}

// refine adds what only the use case knows to an error the port returned: the holder of a busy environment
// and the consumers an expanded scope would reach.
func refine(ctx context.Context, ops Operations, req SubmitRequest, err error) error {
	appErr, ok := errors.AsType[*Error](err)
	if !ok {
		return err
	}
	switch appErr.Code() {
	case CodeEnvironmentBusy:
		state, inspectErr := ops.Inspect(ctx, InspectQuery{Scope: req.Scope})
		env, isEnv := state.(EnvironmentState)
		if inspectErr != nil || !isEnv || !env.HasOpen {
			return err
		}
		id := env.Open.ID
		return NewError(ClassConflict, CodeEnvironmentBusy, fmt.Sprintf(
			"environment %s is busy: operation %s holds it; follow it with: stackpuddle inspect %s --follow, or wait with --timeout",
			req.Scope.Environment, id, id.Ref())).WithCause(err)
	case CodeExpandedScopeRequired:
		consumers := unselectedConsumers(req.Project, req.Targets)
		message := "the change reaches objects the selection does not name; select with --all or pass --expand-scope"
		if len(consumers) > 0 {
			expanded := append(refStrings(req.Targets.Refs), consumers...)
			slices.Sort(expanded)
			message = fmt.Sprintf("the change reaches unselected consumers: %s; select them (%s) or pass --expand-scope",
				strings.Join(consumers, ", "), strings.Join(expanded, " "))
		}
		return NewError(ClassValidation, CodeExpandedScopeRequired, message).WithCause(err)
	}
	return err
}

func refStrings(refs []model.ObjectRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.String())
	}
	return out
}

// ErrorForRecord returns the Error an operation that did not succeed exits with, carrying the record, or
// nil for one that succeeded or still runs. It keeps an operation failure apart from a submission failure.
func ErrorForRecord(rec OperationRecord) *Error {
	id := rec.ID.String()
	switch rec.Status {
	case StatusSucceeded, StatusRunning:
		return nil
	case StatusFailed, StatusBlocked:
		code := "operation.failed"
		if rec.Problem != nil {
			code = rec.Problem.Code
		}
		return NewError(ClassOperationFailed, code, fmt.Sprintf("operation %s %s: %s", id, rec.Status, code)).WithRecord(rec)
	case StatusInterrupted:
		return NewError(ClassInterrupted, CodeOperationInterrupted, fmt.Sprintf(
			"operation %s was interrupted at a step boundary and its journal is intact; run stackpuddle up again to continue under a new operation that names it", id)).WithRecord(rec)
	case StatusCancelled:
		return NewError(ClassCancelled, CodeOperationCancelled, fmt.Sprintf(
			"operation %s was cancelled; partial outcomes are kept and the work is not re-run automatically", id)).WithRecord(rec)
	}
	return Unexpected("operation", fmt.Errorf("unknown status %q", rec.Status))
}

// CancelRequest names the operation to cancel in the selected environment.
type CancelRequest struct {
	Selection Selection
	Operation OperationID
}

// Cancel asks the running operation to stop at its next step.
type Cancel func(ctx context.Context, req CancelRequest) error

// NewCancel returns the cancel use case. It loads the project only to name the scope.
func NewCancel(src ProjectSource, ops Operations) Cancel {
	return func(ctx context.Context, req CancelRequest) error {
		return src.With(ctx, req.Selection, LoadOptions{}, func(ctx context.Context, p LoadedProject) error {
			scope, err := scopeOf(p, req.Selection)
			if err != nil {
				return err
			}
			return ops.Cancel(ctx, scope, req.Operation)
		})
	}
}

func scopeOf(p LoadedProject, sel Selection) (Scope, error) {
	if err := requireValid(p); err != nil {
		return Scope{}, err
	}
	env, err := inspectionEnvironment(p.Project, sel)
	if err != nil {
		return Scope{}, err
	}
	return Scope{Project: p.Project.Name(), Environment: env}, nil
}

// Sleeper waits for d or until ctx ends, returning ctx's error then. It is the seam that keeps polling tests
// off the wall clock.
type Sleeper func(ctx context.Context, d time.Duration) error

// Sleep is the Sleeper over a real timer.
func Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("waiting: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

// InspectOperationRequest reads one operation, optionally until it ends.
type InspectOperationRequest struct {
	Selection Selection
	Operation OperationID
	// Wait polls until the operation is terminal and exits with its outcome.
	Wait bool
	// Follow is Wait plus a progress report for every change.
	Follow bool
	// Timeout bounds Wait and Follow; zero means no bound. It never cancels the operation.
	Timeout  time.Duration
	Progress func(OperationRecord)
}

// InspectOperation reads an open or archived operation without the lock.
type InspectOperation func(ctx context.Context, req InspectOperationRequest) (OperationRecord, error)

// NewInspectOperation returns the operation inspection use case; sleep paces the polling.
func NewInspectOperation(src ProjectSource, ops Operations, sleep Sleeper) InspectOperation {
	return func(ctx context.Context, req InspectOperationRequest) (OperationRecord, error) {
		var out OperationRecord
		err := src.With(ctx, req.Selection, LoadOptions{}, func(ctx context.Context, p LoadedProject) error {
			scope, err := scopeOf(p, req.Selection)
			if err != nil {
				return err
			}
			out, err = pollOperation(ctx, ops, sleep, scope, req)
			return err
		})
		return out, err
	}
}

func pollOperation(ctx context.Context, ops Operations, sleep Sleeper, scope Scope, req InspectOperationRequest) (OperationRecord, error) {
	waiting := req.Wait || req.Follow
	parent := ctx
	if waiting && req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}
	var last OperationRecord
	var reported time.Time
	for {
		rec, err := readOperation(ctx, ops, scope, req.Operation)
		if err != nil {
			if parent.Err() == nil && ctx.Err() != nil && last.ID.String() != "" {
				return last, waitTimeout(last, req.Timeout)
			}
			return last, err
		}
		last = rec
		if req.Follow && req.Progress != nil && !rec.UpdatedAt.Equal(reported) {
			reported = rec.UpdatedAt
			req.Progress(rec)
		}
		if !waiting {
			return rec, nil
		}
		if rec.Status.Terminal() {
			if failure := ErrorForRecord(rec); failure != nil {
				return rec, failure
			}
			return rec, nil
		}
		if err := sleep(ctx, PollInterval); err != nil {
			if parent.Err() == nil && ctx.Err() != nil {
				return last, waitTimeout(last, req.Timeout)
			}
			return last, err
		}
	}
}

func waitTimeout(rec OperationRecord, timeout time.Duration) *Error {
	return NewError(ClassConflict, codeWaitTimeout, fmt.Sprintf(
		"operation %s is still %s after %s; it keeps running, follow it again with: stackpuddle inspect %s --follow",
		rec.ID, rec.Status, timeout, rec.ID.Ref())).WithRecord(rec)
}

func readOperation(ctx context.Context, ops Operations, scope Scope, id OperationID) (OperationRecord, error) {
	inspection, err := ops.Inspect(ctx, InspectQuery{Scope: scope, Operation: id})
	if err != nil {
		return OperationRecord{}, err
	}
	switch v := inspection.(type) {
	case OperationRecord:
		return v, nil
	case EnvironmentState:
	}
	return OperationRecord{}, Unexpected("inspect", errors.New("the engine returned an environment for an operation query"))
}

// StateView is the state of one reference: accepted intent, observed facts and ownership evidence, with the
// generation as its freshness. It holds no secret value.
type StateView struct {
	Ref         model.Ref
	Environment model.EnvironmentName
	State       EnvironmentState
}

// InspectStateRequest names the reference whose state to show.
type InspectStateRequest struct {
	Selection Selection
	Ref       model.Ref
}

// InspectState reads the state of a workload, module instance or environment without the lock.
type InspectState func(ctx context.Context, req InspectStateRequest) (StateView, error)

// NewInspectState returns the state inspection use case.
func NewInspectState(src ProjectSource, ops Operations) InspectState {
	return func(ctx context.Context, req InspectStateRequest) (StateView, error) {
		var view StateView
		sel, err := selectionWithEnvRef(req.Selection, []model.Ref{req.Ref})
		if err != nil {
			return view, err
		}
		err = src.With(ctx, sel, LoadOptions{}, func(ctx context.Context, p LoadedProject) error {
			scope, err := scopeOf(p, sel)
			if err != nil {
				return err
			}
			if err := declared(p.Project, req.Ref); err != nil {
				return err
			}
			inspection, err := ops.Inspect(ctx, InspectQuery{Scope: scope})
			if err != nil {
				return err
			}
			state, ok := inspection.(EnvironmentState)
			if !ok {
				return Unexpected("inspect", errors.New("the engine returned an operation for an environment query"))
			}
			state.Objects = objectsOf(state.Objects, req.Ref)
			view = StateView{Ref: req.Ref, Environment: scope.Environment, State: state}
			return nil
		})
		return view, err
	}
}

// declared checks that ref names something the project declares and that state can be shown for.
func declared(p model.Project, ref model.Ref) error {
	switch ref.Kind() {
	case model.RefWorkload:
		if !slices.ContainsFunc(p.Workloads(), func(w model.Workload) bool { return "workload/"+w.Key().String() == ref.String() }) {
			return unknownObject(ref)
		}
	case model.RefModule:
		if !slices.ContainsFunc(p.ModuleInstances(), func(m model.ModuleInstance) bool { return "module/"+m.Key().String() == ref.String() }) {
			return unknownObject(ref)
		}
	case model.RefEnv:
		name, err := model.ParseEnvironmentName(refTail(ref))
		if err != nil {
			return NewError(ClassUsage, "reference.invalid_ref", ref.String()+" is not a valid environment reference").WithCause(err)
		}
		if _, ok := p.Environment(name); !ok {
			return unknownObject(ref)
		}
	case model.RefClient, model.RefApp, model.RefResource, model.RefOperation, model.RefBinding:
		return NewError(ClassUsage, "inspect.unsupported_ref_kind",
			fmt.Sprintf("inspect --view state supports workload/<key>, module/<key> and env/<name>, not %s", ref.Kind()))
	}
	return nil
}

// objectsOf keeps the realized objects that belong to ref. The engine names an object "<kind>/<name>", where
// name is the declaring key of a workload or module instance, optionally followed by "--" and what hangs below
// it ("service/api", "volume/api--uploads", "service/primary--store"). A key never holds "--", so the prefix
// is unambiguous.
func objectsOf(all []RealizedObject, ref model.Ref) []RealizedObject {
	if ref.Kind() == model.RefEnv {
		return all
	}
	key := refTail(ref)
	var out []RealizedObject
	for _, o := range all {
		_, name, _ := strings.Cut(o.Ref, "/")
		if name == key || strings.HasPrefix(name, key+"--") {
			out = append(out, o)
		}
	}
	return out
}
