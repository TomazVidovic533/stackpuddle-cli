package diagnostics_test

import (
	"testing"
	"testing/fstest"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
	"github.com/TomazVidovic533/stackpuddle/model"
)

func TestOutcome(t *testing.T) {
	t.Parallel()
	pass := diagnostics.Result{State: diagnostics.StatePass}
	fail := diagnostics.Result{State: diagnostics.StateFail}
	unknown := diagnostics.Result{State: diagnostics.StateUnknown}
	skipped := diagnostics.Result{State: diagnostics.StateSkipped}
	tests := []struct {
		name    string
		results []diagnostics.Result
		want    diagnostics.State
	}{
		{"all pass", []diagnostics.Result{pass, pass}, diagnostics.StatePass},
		{"fail beats everything", []diagnostics.Result{pass, unknown, skipped, fail}, diagnostics.StateFail},
		{"unknown without failure is incomplete", []diagnostics.Result{pass, unknown}, diagnostics.StateUnknown},
		{"skipped without failure is incomplete", []diagnostics.Result{pass, skipped}, diagnostics.StateUnknown},
		{"nothing checked is incomplete", nil, diagnostics.StateUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := (diagnostics.Report{Results: tt.results}).Outcome(); got != tt.want {
				t.Fatalf("outcome = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestResultEnvironment(t *testing.T) {
	t.Parallel()
	r := diagnostics.Result{Checker: "configuration"}
	if _, ok := r.Environment(); ok {
		t.Fatal("a project-wide result reports an environment")
	}
	env, err := model.ParseEnvironmentName("local")
	if err != nil {
		t.Fatal(err)
	}
	scoped := r.ForEnvironment(env)
	if got, ok := scoped.Environment(); !ok || got != env {
		t.Fatalf("environment = %v (%v), want local", got, ok)
	}
	if _, ok := r.Environment(); ok {
		t.Fatal("ForEnvironment changed its receiver")
	}
}

func TestClosedSetsHaveNames(t *testing.T) {
	t.Parallel()
	for _, s := range []diagnostics.State{diagnostics.StatePass, diagnostics.StateFail, diagnostics.StateUnknown, diagnostics.StateSkipped} {
		if s.String() == "invalid" {
			t.Errorf("state %d has no name", s)
		}
	}
	for _, s := range []diagnostics.Stage{diagnostics.StageStructure, diagnostics.StageConfiguration} {
		if s.String() == "invalid" {
			t.Errorf("stage %d has no name", s)
		}
	}
}

func TestFromModelKeepsCodeAndLocation(t *testing.T) {
	t.Parallel()
	_, diags := model.Load(fstest.MapFS{}, "stackpuddle.yaml", nil)
	if len(diags) == 0 {
		t.Fatal("loading a missing file reported nothing")
	}
	findings := diagnostics.FromModel(diags)
	if len(findings) != len(diags) || findings[0].Code != string(diags[0].Code()) {
		t.Fatalf("findings = %+v, want one per diagnostic with the same code", findings)
	}
}
