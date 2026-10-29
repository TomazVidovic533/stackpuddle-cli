// Package application holds the use cases of the command line: check, inspect, version and the lifecycle verbs. It is free of
// the command framework, the filesystem layout and the output format. Ports are declared here and
// implemented at the edge; the composition root wires them.
package application

import (
	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
)

// Class is the category of an expected failure. The set is closed; the exit map in internal/cli is the one
// place that turns a class into an exit code.
type Class int

// The classes of expected failure.
const (
	// ClassUsage is a request the command cannot honour as written: bad flag value, unsupported view.
	ClassUsage Class = iota + 1
	// ClassValidation is a project or input that is invalid for what the command needs.
	ClassValidation
	// ClassNotFound is a root file, environment or object that does not exist.
	ClassNotFound
	// ClassUnexpected is a programming or infrastructure fault.
	ClassUnexpected
	// ClassConflict is an environment that is busy or a state that moved since it was read.
	ClassConflict
	// ClassCompatibility is a version, capability, digest or bundle the build cannot accept.
	ClassCompatibility
	// ClassInterrupted is an operation that stopped at a step boundary because the process was signalled.
	ClassInterrupted
	// ClassCancelled is an operation that stopped because someone cancelled it.
	ClassCancelled
	// ClassOperationFailed is an operation that was accepted and then failed or was blocked.
	ClassOperationFailed
	// ClassForbidden is a request refused by scope or policy.
	ClassForbidden
	// ClassUnavailable is a target or an artifact source that could not be reached.
	ClassUnavailable
)

// Classes returns every class, so a test can walk the closed set.
func Classes() []Class {
	return []Class{
		ClassUsage, ClassValidation, ClassNotFound, ClassUnexpected, ClassConflict, ClassCompatibility,
		ClassInterrupted, ClassCancelled, ClassOperationFailed, ClassForbidden, ClassUnavailable,
	}
}

// String returns the lower-case name of the class, such as "not_found" or "operation_failed".
func (c Class) String() string {
	switch c {
	case ClassUsage:
		return "usage"
	case ClassValidation:
		return "validation"
	case ClassNotFound:
		return "not_found"
	case ClassUnexpected:
		return "unexpected"
	case ClassConflict:
		return "conflict"
	case ClassCompatibility:
		return "compatibility"
	case ClassInterrupted:
		return "interrupted"
	case ClassCancelled:
		return "cancelled"
	case ClassOperationFailed:
		return "operation_failed"
	case ClassForbidden:
		return "forbidden"
	case ClassUnavailable:
		return "unavailable"
	}
	return "invalid"
}

// Error is an expected failure with a stable class and code. Match it with errors.AsType[*Error] and
// branch on Class and Code, never on the message. Messages name files, keys and pointers, never a value.
type Error struct {
	class    Class
	code     string
	message  string
	findings []diagnostics.Finding
	cause    error
	record   *OperationRecord
}

// NewError returns an Error. code is a stable "<area>.<name>" such as "project.root_ambiguous".
func NewError(class Class, code, message string) *Error {
	return &Error{class: class, code: code, message: message}
}

// WithFindings returns a copy of e that carries the findings that explain it.
func (e *Error) WithFindings(findings []diagnostics.Finding) *Error {
	c := *e
	c.findings = append([]diagnostics.Finding(nil), findings...)
	return &c
}

// WithCause returns a copy of e that wraps cause for errors.Is and errors.As.
func (e *Error) WithCause(cause error) *Error {
	c := *e
	c.cause = cause
	return &c
}

// WithRecord returns a copy of e that carries the operation the failure ended. It is how an operation
// failure stays apart from a submission failure.
func (e *Error) WithRecord(record OperationRecord) *Error {
	c := *e
	c.record = &record
	return &c
}

// Record returns the operation the failure ended, if it ended one.
func (e *Error) Record() (OperationRecord, bool) {
	if e.record == nil {
		return OperationRecord{}, false
	}
	return *e.record, true
}

// Class returns the failure class.
func (e *Error) Class() Class { return e.class }

// Code returns the stable code.
func (e *Error) Code() string { return e.code }

// Findings returns a copy of the findings that explain the failure.
func (e *Error) Findings() []diagnostics.Finding {
	return append([]diagnostics.Finding(nil), e.findings...)
}

// Error returns the message.
func (e *Error) Error() string { return e.message }

// Unwrap returns the wrapped cause, if any.
func (e *Error) Unwrap() error { return e.cause }

// Unexpected wraps err as a ClassUnexpected Error with code "unexpected.<area>". The cause stays
// reachable through errors.Is and errors.As, so a context cancellation is still recognisable.
func Unexpected(area string, err error) *Error {
	return NewError(ClassUnexpected, "unexpected."+area, area+" failed: "+err.Error()).WithCause(err)
}
