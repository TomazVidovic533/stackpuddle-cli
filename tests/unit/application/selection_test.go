package application_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fakeops"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fixture"
)

func TestSelectionExpandsGroupsAndDeduplicates(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{}
	up := mutateIn(t, storefront(t), ops, nil)
	// storefront and admin overlap on api; the job migrate is in storefront but a group never selects a job.
	res, err := up(context.Background(), application.MutateRequest{
		Targets: application.TargetRequest{Refs: refs(t, "app/storefront", "app/admin"), Command: application.CommandUp},
	})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	want := []string{"workload/api", "workload/backoffice", "workload/web"}
	if got := res.Selection.Selected; !slices.Equal(got, want) {
		t.Fatalf("selected = %v, want %v", got, want)
	}
	if got := submittedRefs(ops.Submits()[0]); !slices.Equal(got, want) {
		t.Fatalf("submitted = %v, want %v", got, want)
	}
	if got := res.Selection.Groups; !slices.Equal(got, []string{"app/admin", "app/storefront"}) {
		t.Fatalf("groups = %v", got)
	}
	if got := res.Selection.Prerequisites; !slices.Equal(got, []string{"workload/migrate"}) {
		t.Fatalf("prerequisites = %v, want the job api requires, shown apart", got)
	}
}

func TestUpWithoutReferencesSelectsTheDefaultApplications(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{}
	res, err := mutateIn(t, storefront(t), ops, nil)(context.Background(), application.MutateRequest{
		Targets: application.TargetRequest{Command: application.CommandUp},
	})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if want := []string{"workload/api", "workload/web"}; !slices.Equal(res.Selection.Selected, want) {
		t.Fatalf("selected = %v, want the default application's services %v", res.Selection.Selected, want)
	}
}

func TestAModuleSelectionDoesNotSelectItsConsumers(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{}
	res, err := mutateIn(t, storefront(t), ops, nil)(context.Background(), application.MutateRequest{
		Targets: application.TargetRequest{Refs: refs(t, "workload/api"), Command: application.CommandUp},
	})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if want := []string{"workload/api"}; !slices.Equal(res.Selection.Selected, want) {
		t.Fatalf("selected = %v, want only %v: web consumes api but is not selected", res.Selection.Selected, want)
	}
}

func TestAnExplicitJobReferenceIsSelected(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{}
	res, err := mutateIn(t, storefront(t), ops, nil)(context.Background(), application.MutateRequest{
		Targets: application.TargetRequest{Refs: refs(t, "workload/migrate"), Command: application.CommandUp},
	})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if !slices.Contains(res.Selection.Selected, "workload/migrate") {
		t.Fatalf("selected = %v, want the named job", res.Selection.Selected)
	}
}

func TestSelectionRefusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		command application.Command
		refs    []string
		all     bool
		class   application.Class
		code    string
	}{
		{"all and references", application.CommandUp, []string{"workload/api"}, true, application.ClassUsage, "selection.all_and_refs"},
		{"stop without scope", application.CommandStop, nil, false, application.ClassUsage, "selection.scope_required"},
		{"destroy without scope", application.CommandDestroy, nil, false, application.ClassUsage, "selection.scope_required"},
		{"environment with references", application.CommandDestroy, []string{"env/local", "workload/api"}, false, application.ClassUsage, "selection.env_with_refs"},
		{"unsupported kind", application.CommandUp, []string{"client/cli"}, false, application.ClassUsage, "selection.unsupported_ref_kind"},
		{"unknown workload", application.CommandUp, []string{"workload/missing"}, false, application.ClassNotFound, "project.unknown_object"},
		{"unknown application", application.CommandUp, []string{"app/missing"}, false, application.ClassNotFound, "project.unknown_object"},
		{"unknown module", application.CommandStop, []string{"module/missing"}, false, application.ClassNotFound, "project.unknown_object"},
		{"unknown environment", application.CommandDestroy, []string{"env/nowhere"}, false, application.ClassNotFound, "project.unknown_environment"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ops := &fakeops.Operations{}
			_, err := mutateIn(t, storefront(t), ops, nil)(context.Background(), application.MutateRequest{
				Targets: application.TargetRequest{Refs: refs(t, tt.refs...), All: tt.all, Command: tt.command},
			})
			wantError(t, err, tt.class, tt.code)
			if n := len(ops.Submits()); n != 0 {
				t.Fatalf("the engine was called %d times for a refused selection", n)
			}
		})
	}
}

func TestEnvironmentReferenceSelectsEverything(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{}
	res, err := mutateIn(t, storefront(t), ops, nil)(context.Background(), application.MutateRequest{
		Targets: application.TargetRequest{Refs: refs(t, "env/local"), Command: application.CommandDestroy}, DryRun: true,
	})
	if err != nil {
		t.Fatalf("destroy env/local: %v", err)
	}
	req := ops.Submits()[0]
	if !res.Selection.All || !req.Targets.All || !req.DryRun || req.Command != application.CommandDestroy {
		t.Fatalf("request = %+v, want a dry-run destroy of everything", req)
	}
	if req.Scope.Environment.String() != "local" || req.Scope.Project.String() != "storefront" {
		t.Fatalf("scope = %v", req.Scope)
	}
}

func TestExpandedScopeRefusalNamesTheUnselectedConsumers(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{SubmitFunc: func(context.Context, application.SubmitRequest) (application.SubmitResult, error) {
		return application.SubmitResult{}, application.NewError(application.ClassValidation, application.CodeExpandedScopeRequired, "plan refused")
	}}
	_, err := mutateIn(t, storefront(t), ops, nil)(context.Background(), application.MutateRequest{
		Targets: application.TargetRequest{Refs: refs(t, "workload/api", "module/audit"), Command: application.CommandUp},
	})
	appErr := wantError(t, err, application.ClassValidation, application.CodeExpandedScopeRequired)
	for _, want := range []string{"workload/web", "module/audit-copy", "--expand-scope", "module/audit module/audit-copy workload/api workload/web"} {
		if !strings.Contains(appErr.Error(), want) {
			t.Errorf("message lacks %q:\n%s", want, appErr.Error())
		}
	}
}

func TestBusyEnvironmentNamesTheHolderAndTheFollowCommand(t *testing.T) {
	t.Parallel()
	holder := fakeops.Record(application.StatusRunning)
	ops := &fakeops.Operations{
		SubmitFunc: func(context.Context, application.SubmitRequest) (application.SubmitResult, error) {
			return application.SubmitResult{}, application.NewError(application.ClassConflict, application.CodeEnvironmentBusy, "busy")
		},
		InspectFunc: func(context.Context, application.InspectQuery) (application.Inspection, error) {
			return application.EnvironmentState{Open: holder, HasOpen: true}, nil
		},
	}
	_, err := mutateIn(t, storefront(t), ops, nil)(context.Background(), application.MutateRequest{
		Targets: application.TargetRequest{Command: application.CommandUp},
	})
	appErr := wantError(t, err, application.ClassConflict, application.CodeEnvironmentBusy)
	for _, want := range []string{holder.ID.String(), "stackpuddle inspect operation/" + holder.ID.String() + " --follow"} {
		if !strings.Contains(appErr.Error(), want) {
			t.Errorf("message lacks %q:\n%s", want, appErr.Error())
		}
	}
}

func TestWaitIsPassedToTheEngine(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{}
	_, err := mutateIn(t, storefront(t), ops, nil)(context.Background(), application.MutateRequest{
		Targets: application.TargetRequest{Command: application.CommandUp}, Wait: 90_000_000_000,
	})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if got := ops.Submits()[0].Wait; got.Seconds() != 90 {
		t.Fatalf("wait = %s, want 90s", got)
	}
}

func TestSecretInputsCrossThePortAsSecretValues(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{}
	dir := fixture.Dir(t, fixture.ShopFiles(nil))
	_, err := mutateIn(t, dir, ops, map[string]string{"SHOP_API_TOKEN": fixture.Canary})(context.Background(), application.MutateRequest{
		Targets: application.TargetRequest{Refs: refs(t, "workload/api"), Command: application.CommandUp},
	})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	var found bool
	for _, in := range ops.Submits()[0].Inputs {
		if in.Key() == "api-token" {
			value, isSecret := in.Secret()
			_, isPublic := in.Text()
			found = isSecret && !isPublic && value.Reveal() == fixture.Canary
		}
	}
	if !found {
		t.Fatalf("inputs = %v, want api-token as a secret value", ops.Submits()[0].Inputs)
	}
}
