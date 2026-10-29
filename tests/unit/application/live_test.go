package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fakeops"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fixture"
)

var liveNow = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func liveCheck(t *testing.T, files map[string]string, ops *fakeops.Operations, live bool) diagnostics.Report {
	t.Helper()
	lookup := fixture.Lookup(map[string]string{"SHOP_API_TOKEN": "x"})
	check := application.NewCheckProject(source(fixture.Dir(t, files)), []application.Checker{
		application.StructureChecker(),
		application.LiveChecker(ops, lookup, func() time.Time { return liveNow }),
	})
	report, err := check(context.Background(), application.CheckRequest{Live: live})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return report
}

func liveResult(t *testing.T, r diagnostics.Report) diagnostics.Result {
	t.Helper()
	for _, result := range r.Results {
		if result.Checker == application.LiveCheckerName {
			return result
		}
	}
	t.Fatalf("no live result in %+v", r)
	return diagnostics.Result{}
}

func previewOf(actions ...application.Action) *fakeops.Operations {
	return &fakeops.Operations{SubmitFunc: func(context.Context, application.SubmitRequest) (application.SubmitResult, error) {
		return application.SubmitResult{Preview: application.Preview{BasedOnGeneration: 7, Actions: actions}}, nil
	}}
}

func TestLiveChecksAskThroughADryRunAndOnlyWhenAsked(t *testing.T) {
	t.Parallel()
	ops := previewOf(application.Action{Action: "compose.service", Object: "service/api", Effect: application.EffectApply})
	report := liveCheck(t, fixture.ShopFiles(nil), ops, false)
	for _, r := range report.Results {
		if r.Checker == application.LiveCheckerName {
			t.Fatalf("live result without --live: %+v", r)
		}
	}
	if len(ops.Submits()) != 0 {
		t.Fatalf("submits = %d without --live, want none", len(ops.Submits()))
	}
	result := liveResult(t, liveCheck(t, fixture.ShopFiles(nil), ops, true))
	submits := ops.Submits()
	if len(submits) != 1 || !submits[0].DryRun || submits[0].Command != application.CommandUp || len(ops.Runs()) != 0 {
		t.Fatalf("submits = %+v, want exactly one dry-run up", submits)
	}
	if result.State != diagnostics.StatePass || result.Generation != 7 || !result.ObservedAt.Equal(liveNow) {
		t.Fatalf("result = %+v, want pass with generation 7 observed at the clock", result)
	}
}

func TestALostObjectFailsAndNamesItsConsumers(t *testing.T) {
	t.Parallel()
	ops := previewOf(application.Action{Object: "volume/api--data", Effect: application.EffectBlocked, Problem: "ownership.lost_object"})
	result := liveResult(t, liveCheck(t, fixture.ShopFiles(nil), ops, true))
	if result.State != diagnostics.StateFail || len(result.Findings) != 1 {
		t.Fatalf("result = %+v, want one failing finding", result)
	}
	finding := result.Findings[0]
	if finding.Code != "ownership.lost_object" || len(finding.Consumers) != 1 || finding.Consumers[0] != "workload/web" {
		t.Fatalf("finding = %+v, want ownership.lost_object naming workload/web", finding)
	}
}

func TestAnUnreachableDaemonOrAnUndecidedStepIsUnknownNeverPass(t *testing.T) {
	t.Parallel()
	down := &fakeops.Operations{SubmitFunc: func(context.Context, application.SubmitRequest) (application.SubmitResult, error) {
		return application.SubmitResult{}, application.NewError(application.ClassUnavailable, "runtime.target_unreachable", "docker is not running")
	}}
	if got := liveResult(t, liveCheck(t, fixture.ShopFiles(nil), down, true)).State; got != diagnostics.StateUnknown {
		t.Fatalf("daemon down = %s, want unknown", got)
	}
	undecided := previewOf(application.Action{Action: "module.step", Object: "module/audit", Effect: application.EffectApply, Unknown: true})
	if got := liveResult(t, liveCheck(t, fixture.ShopFiles(nil), undecided, true)).State; got != diagnostics.StateUnknown {
		t.Fatalf("unknown step = %s, want unknown", got)
	}
}

func TestLiveIsSkippedWhenAnEarlierStageFailed(t *testing.T) {
	t.Parallel()
	ops := previewOf()
	report := liveCheck(t, map[string]string{"stackpuddle.yaml": "apiVersion: nope\n"}, ops, true)
	if got := liveResult(t, report).State; got != diagnostics.StateSkipped || len(ops.Submits()) != 0 {
		t.Fatalf("live = %s with %d submits, want skipped and no submit", got, len(ops.Submits()))
	}
}
