// Package diagnostics holds the result type every checker of the check command returns: a closed set of
// states, a stage and findings. It knows no command, format or checker.
package diagnostics

import (
	"slices"
	"time"

	"github.com/TomazVidovic533/stackpuddle/model"
)

// State is the outcome of one checker run. The set is closed so that adding a state breaks every
// unhandled switch.
type State int

// The states a checker reports.
const (
	// StatePass means the checker ran and found nothing.
	StatePass State = iota + 1
	// StateFail means the checker ran and found at least one problem.
	StateFail
	// StateUnknown means the checker ran but could not decide part of its question.
	StateUnknown
	// StateSkipped means the checker did not run because an earlier stage failed.
	StateSkipped
)

// String returns "pass", "fail", "unknown" or "skipped".
func (s State) String() string {
	switch s {
	case StatePass:
		return "pass"
	case StateFail:
		return "fail"
	case StateUnknown:
		return "unknown"
	case StateSkipped:
		return "skipped"
	}
	return "invalid"
}

// Stage is the part of the pipeline a checker belongs to.
type Stage int

// The stages of the check command.
const (
	// StageStructure covers loading: syntax, compatibility, structure, identity, references, bindings.
	StageStructure Stage = iota + 1
	// StageConfiguration covers configuration inputs, layers and ownership conflicts.
	StageConfiguration
	// StageSemantic covers the module-specific rules of the effective options, offline.
	StageSemantic
	// StageLive covers the questions only the running system answers; it runs on request.
	StageLive
)

// String returns "structure", "configuration", "semantic" or "live".
func (s Stage) String() string {
	switch s {
	case StageStructure:
		return "structure"
	case StageConfiguration:
		return "configuration"
	case StageSemantic:
		return "semantic"
	case StageLive:
		return "live"
	}
	return "invalid"
}

// Finding is one problem a checker found: a stable code, where it applies and a message. Messages name
// pointers, keys and files only, never a supplied value, and are not part of the contract.
type Finding struct {
	Code     string
	Location model.Location
	Message  string
	// Consumers names the objects that bind the object the finding is about, when it is about one.
	Consumers []string
}

// Result is the outcome of one checker, optionally for one environment.
type Result struct {
	Checker  string
	Stage    Stage
	State    State
	Findings []Finding
	// ObservedAt and Generation are the freshness of a live result: when it was read and which state
	// generation it is based on. They are zero for an offline result.
	ObservedAt time.Time
	Generation int64

	environment    model.EnvironmentName
	hasEnvironment bool
}

// ForEnvironment returns a copy of r that reports on env.
func (r Result) ForEnvironment(env model.EnvironmentName) Result {
	r.environment = env
	r.hasEnvironment = true
	return r
}

// Environment returns the environment r reports on; ok is false for a project-wide result.
func (r Result) Environment() (model.EnvironmentName, bool) {
	return r.environment, r.hasEnvironment
}

// Report is the ordered results of one check run.
type Report struct {
	Results []Result
}

// Outcome returns StateFail when any result failed, else StateUnknown when any result is unknown or
// skipped (or there is no result at all, because nothing was checked), else StatePass.
func (r Report) Outcome() State {
	if len(r.Results) == 0 {
		return StateUnknown
	}
	outcome := StatePass
	for _, result := range r.Results {
		switch result.State {
		case StateFail:
			return StateFail
		case StateUnknown, StateSkipped:
			outcome = StateUnknown
		case StatePass:
		}
	}
	return outcome
}

// FromModel converts the diagnostics of the model package to findings, in their order.
func FromModel(ds model.Diagnostics) []Finding {
	return slices.Collect(func(yield func(Finding) bool) {
		for _, d := range ds {
			if !yield(Finding{Code: string(d.Code()), Location: d.Location(), Message: d.Message()}) {
				return
			}
		}
	})
}
