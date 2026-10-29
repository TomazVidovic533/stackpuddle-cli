package application_test

import (
	"context"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/application/inprocess"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fakeops"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fixture"
	"github.com/TomazVidovic533/stackpuddle-control-plane/engine"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// The adapter is exercised through the real engine over an empty state root: nothing here starts Docker.

func scope(t *testing.T) application.Scope {
	t.Helper()
	name, err := model.ParseProjectName("storefront")
	if err != nil {
		t.Fatal(err)
	}
	env, err := model.ParseEnvironmentName("local")
	if err != nil {
		t.Fatal(err)
	}
	return application.Scope{Project: name, Environment: env}
}

func adapter(t *testing.T) *inprocess.Operations {
	t.Helper()
	vars := map[string]string{"STACKPUDDLE_STATE_DIR": t.TempDir()}
	return inprocess.New(fixture.Lookup(vars), nil)
}

func TestCodesTheUseCasesRecogniseAreTheEnginesPublishedCodes(t *testing.T) {
	t.Parallel()
	pairs := map[string]engine.Code{
		application.CodeEnvironmentBusy:       engine.CodeEnvironmentBusy,
		application.CodeExpandedScopeRequired: engine.CodeExpandedScopeRequired,
		application.CodeOperationInterrupted:  engine.CodeOperationInterrupted,
		application.CodeOperationCancelled:    engine.CodeOperationCancelled,
	}
	for ours, theirs := range pairs {
		if ours != string(theirs) {
			t.Errorf("application code %q differs from the engine's %q", ours, theirs)
		}
	}
}

func TestTheAdapterComposesNoEngineUntilItIsUsed(t *testing.T) {
	t.Parallel()
	reads := 0
	lookup := func(string) (string, bool) {
		reads++
		return "", false
	}
	_ = inprocess.New(lookup, nil)
	if reads != 0 {
		t.Fatalf("the process environment was read %d times before any use", reads)
	}
}

func TestAnUnconfiguredEngineFailsAsAValidationError(t *testing.T) {
	t.Parallel()
	_, err := inprocess.New(fixture.Lookup(map[string]string{"STACKPUDDLE_STATE_DIR": "relative/path"}), nil).Advertise()
	wantError(t, err, application.ClassValidation, "config.invalid_setting")
}

func TestInspectEnvironmentOfAnEmptyStateRootIsGenerationZero(t *testing.T) {
	t.Parallel()
	got, err := adapter(t).Inspect(context.Background(), application.InspectQuery{Scope: scope(t)})
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	state, ok := got.(application.EnvironmentState)
	if !ok || state.Generation != 0 || len(state.Objects) != 0 || state.HasOpen {
		t.Fatalf("inspection = %#v, want an empty environment", got)
	}
}

func TestInspectOfAnUnknownOperationIsNotFound(t *testing.T) {
	t.Parallel()
	_, err := adapter(t).Inspect(context.Background(), application.InspectQuery{Scope: scope(t), Operation: fakeops.ID(4)})
	wantError(t, err, application.ClassNotFound, "operation.not_found")
}

func TestCancelOfAnUnknownOperationIsNotFound(t *testing.T) {
	t.Parallel()
	err := adapter(t).Cancel(context.Background(), scope(t), fakeops.ID(5))
	wantError(t, err, application.ClassNotFound, "operation.not_found")
}
