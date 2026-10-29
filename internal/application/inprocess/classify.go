package inprocess

import (
	"errors"
	"strings"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-control-plane/engine"
)

// classify is the one translation from an engine error to an application error. It keys on the stable code
// first, then on the category, so a new code in a known area lands in the right exit class unchanged.
func classify(err error) error {
	if err == nil {
		return nil
	}
	engineErr, ok := errors.AsType[*engine.Error](err)
	if !ok {
		return application.Unexpected("engine", err)
	}
	return application.NewError(classOf(engineErr), string(engineErr.Code()), engineErr.Error()).WithCause(err)
}

// classifyRun classifies the error of a Submit that may have recorded an operation. A recorded operation that
// ended failed or blocked is an operation failure, apart from a submission failure; an interrupted or
// cancelled one keeps its record.
func classifyRun(err error, result application.SubmitResult) error {
	if result.HasRecord {
		if failure := application.ErrorForRecord(result.Record); failure != nil {
			return failure.WithCause(err)
		}
	}
	classified := classify(err)
	if appErr, ok := errors.AsType[*application.Error](classified); ok && result.HasRecord {
		return appErr.WithRecord(result.Record)
	}
	return classified
}

func classOf(err *engine.Error) application.Class {
	code := string(err.Code())
	switch {
	case strings.HasPrefix(code, "compatibility."):
		return application.ClassCompatibility
	case strings.HasPrefix(code, "conflict."):
		return application.ClassConflict
	case code == string(engine.CodeJobExitedNonzero):
		return application.ClassOperationFailed
	case code == string(engine.CodeOperationInterrupted):
		return application.ClassInterrupted
	case code == string(engine.CodeOperationCancelled):
		return application.ClassCancelled
	case code == string(engine.CodeExpandedScopeRequired):
		return application.ClassValidation
	}
	switch err.Category() {
	case engine.CategoryValidation:
		return application.ClassValidation
	case engine.CategoryNotFound:
		return application.ClassNotFound
	case engine.CategoryConflict:
		return application.ClassConflict
	case engine.CategoryForbidden:
		return application.ClassForbidden
	case engine.CategoryTransient:
		return application.ClassUnavailable
	case engine.CategoryUnexpected:
		return application.ClassUnexpected
	}
	return application.ClassUnexpected
}
