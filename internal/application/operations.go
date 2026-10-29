package application

import (
	"context"
	"strings"
	"time"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/config/secret"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// Operations is the use-case port of the engine: the five calls the commands need. The in-process adapter
// (internal/application/inprocess) calls the control plane's engine package directly; the m05 service adds
// a remote adapter beside it, so no command changes then. The port mirrors the one published engine
// contract, which is why it has five methods and not fewer.
type Operations interface {
	// Submit changes the environment as req says. A dry run takes no lock and writes nothing. A recorded
	// operation that did not succeed comes back with its record inside the error.
	Submit(ctx context.Context, req SubmitRequest) (SubmitResult, error)
	// Run invokes one job as its own recorded operation. A dry run takes no lock and writes nothing. A
	// recorded operation that did not succeed comes back with its record inside the error.
	Run(ctx context.Context, req RunRequest) (SubmitResult, error)
	// Inspect reads an environment or one operation without the lock and writes nothing.
	Inspect(ctx context.Context, q InspectQuery) (Inspection, error)
	// Cancel asks the running operation to stop at its next step. It takes no lock.
	Cancel(ctx context.Context, scope Scope, id OperationID) error
	// Advertise reports what the engine can run. It fails when the engine cannot be composed.
	Advertise() (model.EngineInfo, error)
}

// Command is what an operation asks of an environment. The set is closed.
type Command string

// The commands.
const (
	CommandUp      Command = "up"
	CommandStop    Command = "stop"
	CommandDestroy Command = "destroy"
	CommandRun     Command = "run"
)

// Commands returns every command.
func Commands() []Command { return []Command{CommandUp, CommandStop, CommandDestroy, CommandRun} }

// Status is the lifecycle status of an operation. The set is closed.
type Status string

// The statuses.
const (
	StatusRunning     Status = "running"
	StatusSucceeded   Status = "succeeded"
	StatusFailed      Status = "failed"
	StatusCancelled   Status = "cancelled"
	StatusInterrupted Status = "interrupted"
	StatusBlocked     Status = "blocked"
)

// Statuses returns every status.
func Statuses() []Status {
	return []Status{StatusRunning, StatusSucceeded, StatusFailed, StatusCancelled, StatusInterrupted, StatusBlocked}
}

// Terminal reports whether an operation with this status will not change again.
func (s Status) Terminal() bool { return s != StatusRunning }

// StepStatus is the status of one step. The set is closed.
type StepStatus string

// The step statuses.
const (
	StepPending                StepStatus = "pending"
	StepStarted                StepStatus = "started"
	StepCompleted              StepStatus = "completed"
	StepCompletedByObservation StepStatus = "completed-by-observation"
	StepNotApplied             StepStatus = "not-applied"
	StepFailed                 StepStatus = "failed"
	StepBlocked                StepStatus = "blocked"
)

// StepStatuses returns every step status.
func StepStatuses() []StepStatus {
	return []StepStatus{
		StepPending, StepStarted, StepCompleted, StepCompletedByObservation, StepNotApplied, StepFailed, StepBlocked,
	}
}

// ActorKind says who submitted an operation. The set is closed.
type ActorKind string

// The actor kinds.
const (
	ActorOSUser ActorKind = "os-user"
	ActorToken  ActorKind = "token"
)

// ActorKinds returns every actor kind.
func ActorKinds() []ActorKind { return []ActorKind{ActorOSUser, ActorToken} }

// Effect is what a preview row does. The set is closed.
type Effect string

// The effects.
const (
	EffectApply   Effect = "apply"
	EffectStop    Effect = "stop"
	EffectDelete  Effect = "delete"
	EffectBlocked Effect = "blocked"
)

// Effects returns every effect.
func Effects() []Effect { return []Effect{EffectApply, EffectStop, EffectDelete, EffectBlocked} }

// parseNamed returns the member of all spelled s.
func parseNamed[T ~string](s string, all []T) (T, bool) {
	for _, v := range all {
		if string(v) == s {
			return v, true
		}
	}
	var zero T
	return zero, false
}

// ParseCommand returns the command spelled s.
func ParseCommand(s string) (Command, bool) { return parseNamed(s, Commands()) }

// ParseStatus returns the status spelled s.
func ParseStatus(s string) (Status, bool) { return parseNamed(s, Statuses()) }

// ParseStepStatus returns the step status spelled s.
func ParseStepStatus(s string) (StepStatus, bool) { return parseNamed(s, StepStatuses()) }

// ParseActorKind returns the actor kind spelled s.
func ParseActorKind(s string) (ActorKind, bool) { return parseNamed(s, ActorKinds()) }

// ParseEffect returns the effect spelled s.
func ParseEffect(s string) (Effect, bool) { return parseNamed(s, Effects()) }

// OperationID identifies one operation: "op-" and 26 Crockford base32 characters. It is comparable, so it
// works as a map key. The zero value is "no operation".
type OperationID struct{ text string }

const operationRefPrefix = "operation/"

// ParseOperationID parses "op-<26 characters>" or "operation/op-<26 characters>" through the reference
// grammar, so one parser owns the spelling.
func ParseOperationID(s string) (OperationID, error) {
	ref, err := model.ParseRef(operationRefPrefix + strings.TrimPrefix(s, operationRefPrefix))
	if err != nil {
		return OperationID{}, err
	}
	return OperationIDFromRef(ref)
}

// OperationIDFromRef returns the identifier of an operation reference, or an *Error when ref names
// something else.
func OperationIDFromRef(ref model.Ref) (OperationID, error) {
	if ref.Kind() != model.RefOperation {
		return OperationID{}, NewError(ClassUsage, "reference.not_operation", ref.String()+" is not an operation reference")
	}
	return OperationID{text: strings.TrimPrefix(ref.String(), operationRefPrefix)}, nil
}

// String returns "op-…", or "" for the zero value.
func (id OperationID) String() string { return id.text }

// IsZero reports whether id is the zero value.
func (id OperationID) IsZero() bool { return id.text == "" }

// Ref returns the reference "operation/<id>".
func (id OperationID) Ref() string { return operationRefPrefix + id.text }

// Scope names one environment of a project.
type Scope struct {
	Project     model.ProjectName
	Environment model.EnvironmentName
}

// Input is one resolved input: either a public text or a secret value, never both. A secret input holds its
// value as secret.Value and never becomes text. Build one with PublicInput or SecretInput.
type Input struct {
	key    string
	text   string
	secret secret.Value
	isSec  bool
}

// PublicInput returns the input key with a non-secret text value.
func PublicInput(key, text string) Input { return Input{key: key, text: text} }

// SecretInput returns the input key with a secret value.
func SecretInput(key string, value secret.Value) Input {
	return Input{key: key, secret: value, isSec: true}
}

// Key returns the input key.
func (i Input) Key() string { return i.key }

// Text returns the public value; ok is false for a secret input.
func (i Input) Text() (text string, ok bool) { return i.text, !i.isSec }

// Secret returns the secret value; ok is false for a public input.
func (i Input) Secret() (value secret.Value, ok bool) { return i.secret, i.isSec }

// Targets says which objects an operation changes.
type Targets struct {
	// All selects every object of the project; Refs is empty then.
	All  bool
	Refs []model.ObjectRef
}

// SubmitRequest is one request to change an environment.
type SubmitRequest struct {
	Scope      Scope
	Project    model.Project
	ProjectDir string
	Command    Command
	Targets    Targets
	Inputs     []Input
	// DryRun previews: no lock, no write.
	DryRun bool
	// ExpandedScope permits a change that reaches unselected consumers.
	ExpandedScope bool
	// DeleteData makes destroy delete the owned volumes too.
	DeleteData bool
	// Wait bounds the wait for a busy environment; zero fails at once.
	Wait time.Duration
	// Progress, when set, is called after every persisted change.
	Progress func(OperationRecord)
}

// RunRequest is one explicit invocation of a job.
type RunRequest struct {
	Scope      Scope
	Project    model.Project
	ProjectDir string
	// Job is the workload reference of the job to invoke.
	Job    model.ObjectRef
	Inputs []Input
	// DryRun plans the invocation and writes nothing.
	DryRun bool
	// Wait bounds the wait for a busy environment; zero fails at once.
	Wait time.Duration
	// Progress, when set, is called after every persisted change.
	Progress func(OperationRecord)
}

// SubmitResult is the outcome of Submit and Run.
type SubmitResult struct {
	// Record is the recorded operation; HasRecord is false for a dry run and for a request with nothing to do.
	Record    OperationRecord
	HasRecord bool
	// Preview is what a dry run would change.
	Preview Preview
}

// Action is one line of a preview: a planned step, or a finding that blocks the operation.
type Action struct {
	// Action is the step's action, empty for a blocked row.
	Action string
	Object string
	Effect Effect
	// Unknown is true when the plan cannot say whether the step still has anything to do.
	Unknown bool
	// Problem is the code of a blocked row's finding.
	Problem string
}

// Preview is the dry-run result: what would change, based on which generation.
type Preview struct {
	Actions           []Action
	BasedOnGeneration int64
}

// Problem is the stable part of a failure recorded on an operation or a step.
type Problem struct {
	Code     string
	Category string
}

// JobResult is the native result of a job step: the exit code of the job's own container.
type JobResult struct {
	ExitCode int
}

// StepRecord is one action of an operation.
type StepRecord struct {
	ID         string
	Action     string
	Object     string
	Status     StepStatus
	StartedAt  time.Time
	FinishedAt time.Time
	Problem    *Problem
	// Job is the native result of a job step, nil for every other step and until the job has exited.
	Job *JobResult
}

// OperationRecord is the record of one engine operation: every field of the published Operation schema.
// A zero time or identifier means the field is absent.
type OperationRecord struct {
	ID          OperationID
	Continues   OperationID
	ContinuedBy OperationID
	Scope       Scope
	ScopeUID    string
	Command     Command
	// All is true when everything was selected; Refs is empty then.
	All           bool
	Refs          []string
	ExpandedScope bool
	Revision      int
	Status        Status
	Steps         []StepRecord
	Problem       *Problem
	Actor         ActorKind
	CreatedAt     time.Time
	UpdatedAt     time.Time
	FinishedAt    time.Time
}

// Mapping is one runtime object that carries a realized object: the ownership evidence.
type Mapping struct {
	Kind string
	Name string
	ID   string
}

// RealizedObject is one realized object of an environment.
type RealizedObject struct {
	Ref      string
	State    string
	Mappings []Mapping
}

// EnvironmentState is the lock-free picture of one environment. Generation is its freshness.
type EnvironmentState struct {
	Generation     int64
	Revision       int
	ScopeUID       string
	ComposeProject string
	Objects        []RealizedObject
	// Open is the operation that is running or was left open.
	Open    OperationRecord
	HasOpen bool
}

// InspectQuery says what to inspect: one operation when Operation is set, else the environment.
type InspectQuery struct {
	Scope     Scope
	Operation OperationID
}

// Inspection is the result of Inspect: an EnvironmentState or an OperationRecord.
//
//sumtype:decl
type Inspection interface{ inspection() }

func (EnvironmentState) inspection() {}
func (OperationRecord) inspection()  {}
