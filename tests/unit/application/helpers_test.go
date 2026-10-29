package application_test

import (
	"errors"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/project"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fakeops"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fixture"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// mutateIn builds the production Mutate use case over a real project directory and the operations double.
func mutateIn(t *testing.T, dir string, ops *fakeops.Operations, lookup map[string]string) application.Mutate {
	t.Helper()
	source := project.NewSource(func() (string, error) { return dir, nil })
	return application.NewMutate(source, nil, ops, fixture.Lookup(lookup))
}

func storefront(t *testing.T) string {
	t.Helper()
	return fixture.Dir(t, fixture.StorefrontFiles())
}

func refs(t *testing.T, names ...string) []model.Ref {
	t.Helper()
	out := make([]model.Ref, 0, len(names))
	for _, n := range names {
		r, err := model.ParseRef(n)
		if err != nil {
			t.Fatalf("ParseRef(%q): %v", n, err)
		}
		out = append(out, r)
	}
	return out
}

func wantError(t *testing.T, err error, class application.Class, code string) *application.Error {
	t.Helper()
	appErr, ok := errors.AsType[*application.Error](err)
	if !ok {
		t.Fatalf("err = %v, want *application.Error %s/%s", err, class, code)
	}
	if appErr.Class() != class || appErr.Code() != code {
		t.Fatalf("err = %s/%s (%v), want %s/%s", appErr.Class(), appErr.Code(), appErr, class, code)
	}
	return appErr
}

func submittedRefs(req application.SubmitRequest) []string {
	out := make([]string, 0, len(req.Targets.Refs))
	for _, r := range req.Targets.Refs {
		out = append(out, r.String())
	}
	return out
}
