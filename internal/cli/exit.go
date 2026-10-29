package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/cli/output"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
)

// The exit codes. This is the one map from a failure class to a code; a code never changes meaning and a new
// class takes the next free number.
const (
	ExitOK         = 0 // success; the check outcome is pass
	ExitUnexpected = 1 // a programming or infrastructure fault
	ExitUsage      = 2 // unknown command, argument, flag value, unsupported view or ref kind, invalid ref
	ExitValidation = 3 // the check outcome is fail, an ambiguous root, an invalid project the command needs
	ExitNotFound   = 4 // no root file, unknown environment or object
	ExitIncomplete = 5 // check found no failure but a result is skipped or unknown

	ExitConflict        = 6  // the environment is busy, or the state moved since it was read
	ExitCompatibility   = 7  // apiVersion, capability, state or store version, digest or a module outside the bundle
	ExitInterrupted     = 8  // the operation stopped at a step boundary because the process was signalled
	ExitCancelled       = 9  // the operation was cancelled
	ExitOperationFailed = 10 // the operation was accepted, then failed or was blocked
	ExitForbidden       = 11 // refused by scope or policy
	ExitUnavailable     = 12 // the Docker target or an artifact source could not be reached
)

// outcomeError carries the outcome of a check run whose report was already printed. It is not a failure to
// print again, only an exit code.
type outcomeError struct {
	state diagnostics.State
}

func (e outcomeError) Error() string { return "check outcome: " + e.state.String() }

// ExitCode returns the exit code for err, nil meaning success.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	if outcome, ok := errors.AsType[outcomeError](err); ok {
		return outcomeExit(outcome.state)
	}
	if appErr, ok := errors.AsType[*application.Error](err); ok {
		return classExit(appErr.Class())
	}
	return ExitUnexpected
}

func outcomeExit(s diagnostics.State) int {
	switch s {
	case diagnostics.StatePass:
		return ExitOK
	case diagnostics.StateFail:
		return ExitValidation
	case diagnostics.StateUnknown, diagnostics.StateSkipped:
		return ExitIncomplete
	}
	return ExitUnexpected
}

func classExit(c application.Class) int {
	switch c {
	case application.ClassUsage:
		return ExitUsage
	case application.ClassValidation:
		return ExitValidation
	case application.ClassNotFound:
		return ExitNotFound
	case application.ClassUnexpected:
		return ExitUnexpected
	case application.ClassConflict:
		return ExitConflict
	case application.ClassCompatibility:
		return ExitCompatibility
	case application.ClassInterrupted:
		return ExitInterrupted
	case application.ClassCancelled:
		return ExitCancelled
	case application.ClassOperationFailed:
		return ExitOperationFailed
	case application.ClassForbidden:
		return ExitForbidden
	case application.ClassUnavailable:
		return ExitUnavailable
	}
	return ExitUnexpected
}

// failureOf describes err for printing, or reports false when err is only an exit code (a check outcome).
func failureOf(err error) (output.Failure, bool) {
	if _, ok := errors.AsType[outcomeError](err); ok {
		return output.Failure{}, false
	}
	if appErr, ok := errors.AsType[*application.Error](err); ok {
		failure := output.Failure{
			Class: appErr.Class().String(), Code: appErr.Code(), Message: appErr.Error(), Findings: appErr.Findings(),
		}
		if record, ok := appErr.Record(); ok {
			failure.Operation = &record
		}
		return failure, true
	}
	code := "unexpected.internal"
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		code = "unexpected.cancelled"
	}
	return output.Failure{Class: application.ClassUnexpected.String(), Code: code, Message: err.Error()}, true
}

func usageError(err error) error {
	return application.NewError(application.ClassUsage, "usage.invalid", fmt.Sprint(err)).WithCause(err)
}
