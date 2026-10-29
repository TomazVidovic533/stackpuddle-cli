package application_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/project"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fakeops"
)

func source(dir string) application.ProjectSource {
	return project.NewSource(func() (string, error) { return dir, nil })
}

// noSleep is a Sleeper that never waits, so polling tests do not touch the wall clock.
func noSleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }

// recordsInOrder answers Inspect with each status in turn, then the last one for ever.
func recordsInOrder(statuses ...application.Status) func(context.Context, application.InspectQuery) (application.Inspection, error) {
	i := 0
	return func(context.Context, application.InspectQuery) (application.Inspection, error) {
		rec := fakeops.Record(statuses[min(i, len(statuses)-1)])
		rec.UpdatedAt = rec.UpdatedAt.Add(time.Duration(i) * time.Second)
		i++
		return rec, nil
	}
}

func TestInspectOperationReadsOnceWithoutWait(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{InspectFunc: recordsInOrder(application.StatusRunning, application.StatusSucceeded)}
	inspect := application.NewInspectOperation(source(storefront(t)), ops, noSleep)
	rec, err := inspect(context.Background(), application.InspectOperationRequest{Operation: fakeops.ID(1)})
	if err != nil || rec.Status != application.StatusRunning {
		t.Fatalf("inspect = %v, %v; want one read of the running operation", rec.Status, err)
	}
}

func TestWaitPollsUntilTheOperationEnds(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{InspectFunc: recordsInOrder(application.StatusRunning, application.StatusRunning, application.StatusSucceeded)}
	sleeps := 0
	sleep := func(ctx context.Context, d time.Duration) error {
		sleeps++
		if d != application.PollInterval {
			t.Errorf("poll interval = %s, want %s", d, application.PollInterval)
		}
		return ctx.Err()
	}
	rec, err := application.NewInspectOperation(source(storefront(t)), ops, sleep)(context.Background(),
		application.InspectOperationRequest{Operation: fakeops.ID(1), Wait: true})
	if err != nil || rec.Status != application.StatusSucceeded || sleeps != 2 {
		t.Fatalf("wait = %v, %v after %d sleeps; want succeeded after 2", rec.Status, err, sleeps)
	}
}

func TestFollowReportsEveryChange(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{InspectFunc: recordsInOrder(application.StatusRunning, application.StatusRunning, application.StatusSucceeded)}
	var seen []application.Status
	_, err := application.NewInspectOperation(source(storefront(t)), ops, noSleep)(context.Background(),
		application.InspectOperationRequest{
			Operation: fakeops.ID(1), Follow: true, Progress: func(r application.OperationRecord) { seen = append(seen, r.Status) },
		})
	if err != nil {
		t.Fatalf("follow: %v", err)
	}
	if want := []application.Status{application.StatusRunning, application.StatusRunning, application.StatusSucceeded}; !slices.Equal(seen, want) {
		t.Fatalf("progress = %v, want %v", seen, want)
	}
}

func TestWaitMapsEveryOutcomeToItsClass(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status application.Status
		class  application.Class
	}{
		{application.StatusFailed, application.ClassOperationFailed},
		{application.StatusBlocked, application.ClassOperationFailed},
		{application.StatusInterrupted, application.ClassInterrupted},
		{application.StatusCancelled, application.ClassCancelled},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			t.Parallel()
			ops := &fakeops.Operations{InspectFunc: recordsInOrder(tt.status)}
			_, err := application.NewInspectOperation(source(storefront(t)), ops, noSleep)(context.Background(),
				application.InspectOperationRequest{Operation: fakeops.ID(1), Wait: true})
			appErr, ok := errors.AsType[*application.Error](err)
			if !ok || appErr.Class() != tt.class {
				t.Fatalf("err = %v, want class %s", err, tt.class)
			}
			if rec, has := appErr.Record(); !has || rec.Status != tt.status {
				t.Fatalf("record = %v, %v; the failure must carry the operation it ended", rec.Status, has)
			}
		})
	}
}

func TestWaitDeadlineIsAConflictAndKeepsTheOperation(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{InspectFunc: recordsInOrder(application.StatusRunning)}
	blocking := func(ctx context.Context, _ time.Duration) error {
		<-ctx.Done()
		return ctx.Err()
	}
	_, err := application.NewInspectOperation(source(storefront(t)), ops, blocking)(context.Background(),
		application.InspectOperationRequest{Operation: fakeops.ID(1), Wait: true, Timeout: 20 * time.Millisecond})
	appErr := wantError(t, err, application.ClassConflict, "conflict.wait_timeout")
	if _, has := appErr.Record(); !has {
		t.Fatal("the timeout must keep the running record: the operation is not cancelled")
	}
}

func TestInspectOperationOfAnUnknownProjectFails(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{}
	_, err := application.NewInspectOperation(source(t.TempDir()), ops, noSleep)(context.Background(),
		application.InspectOperationRequest{Operation: fakeops.ID(1)})
	wantError(t, err, application.ClassNotFound, "project.root_not_found")
}

func TestCancelAsksTheEngineForTheNamedOperation(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{}
	err := application.NewCancel(source(storefront(t)), ops)(context.Background(), application.CancelRequest{Operation: fakeops.ID(2)})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got := ops.Cancels(); len(got) != 1 || got[0] != fakeops.ID(2) {
		t.Fatalf("cancels = %v", got)
	}
}

func TestErrorForRecordCoversEveryStatus(t *testing.T) {
	t.Parallel()
	want := map[application.Status]application.Class{
		application.StatusFailed: application.ClassOperationFailed, application.StatusBlocked: application.ClassOperationFailed,
		application.StatusInterrupted: application.ClassInterrupted, application.StatusCancelled: application.ClassCancelled,
	}
	for _, status := range application.Statuses() {
		err := application.ErrorForRecord(fakeops.Record(status))
		class, failing := want[status]
		if !failing {
			if err != nil {
				t.Errorf("%s: err = %v, want none", status, err)
			}
			continue
		}
		if err == nil || err.Class() != class {
			t.Errorf("%s: err = %v, want class %s", status, err, class)
		}
	}
}

func TestInspectStateFiltersTheObjectsOfTheReference(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{InspectFunc: func(context.Context, application.InspectQuery) (application.Inspection, error) {
		return application.EnvironmentState{Generation: 7, Revision: 2, Objects: []application.RealizedObject{
			{Ref: "service/api", State: "active", Mappings: []application.Mapping{{Kind: "compose.service", Name: "api-1", ID: "abc"}}},
			{Ref: "volume/api--uploads", State: "active"},
			{Ref: "service/api-gateway", State: "active"},
			{Ref: "service/web", State: "stopped"},
			{Ref: "volume/audit--store--log", State: "active"},
		}}, nil
	}}
	inspect := application.NewInspectState(source(storefront(t)), ops)
	view, err := inspect(context.Background(), application.InspectStateRequest{Ref: refs(t, "workload/api")[0]})
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if got := objectRefs(view.State.Objects); view.State.Generation != 7 || !slices.Equal(got, []string{"service/api", "volume/api--uploads"}) {
		t.Fatalf("view = %+v, want generation 7 and the api's service and volume only (not api-gateway)", view.State)
	}
	view, err = inspect(context.Background(), application.InspectStateRequest{Ref: refs(t, "module/audit")[0]})
	if got := objectRefs(view.State.Objects); err != nil || !slices.Equal(got, []string{"volume/audit--store--log"}) {
		t.Fatalf("module view = %v, %v; want the objects below the module", got, err)
	}
	view, err = inspect(context.Background(), application.InspectStateRequest{Ref: refs(t, "env/local")[0]})
	if err != nil || len(view.State.Objects) != 5 {
		t.Fatalf("env view = %d objects, %v; want all", len(view.State.Objects), err)
	}
	_, err = inspect(context.Background(), application.InspectStateRequest{Ref: refs(t, "workload/missing")[0]})
	wantError(t, err, application.ClassNotFound, "project.unknown_object")
	_, err = inspect(context.Background(), application.InspectStateRequest{Ref: refs(t, "app/storefront")[0]})
	wantError(t, err, application.ClassUsage, "inspect.unsupported_ref_kind")
}

func TestListServesEachCollection(t *testing.T) {
	t.Parallel()
	ops := &fakeops.Operations{InspectFunc: func(context.Context, application.InspectQuery) (application.Inspection, error) {
		return application.EnvironmentState{Objects: []application.RealizedObject{{Ref: "workload/api", State: "running"}}}, nil
	}}
	list := application.NewList(source(storefront(t)), ops)
	want := map[application.Collection][]string{
		application.CollectionApplications: {"app/admin", "app/storefront"},
		application.CollectionWorkloads:    {"workload/api", "workload/backoffice", "workload/migrate", "workload/web"},
		application.CollectionModules:      {"module/audit", "module/audit-copy"},
		application.CollectionBindings:     {"binding/workload/web/api"},
		application.CollectionEnvironments: {"env/local"},
		application.CollectionResources:    {"workload/api"},
	}
	if len(want) != len(application.Collections()) {
		t.Fatalf("the test covers %d collections, the application serves %d", len(want), len(application.Collections()))
	}
	for collection, refsWanted := range want {
		res, err := list(context.Background(), application.ListRequest{Collection: collection})
		if err != nil {
			t.Fatalf("list %s: %v", collection, err)
		}
		var got []string
		for _, item := range res.Items {
			got = append(got, item.Ref)
		}
		if !slices.Equal(got, refsWanted) {
			t.Errorf("list %s = %v, want %v", collection, got, refsWanted)
		}
	}
	if _, ok := application.ParseCollection("sessions"); ok {
		t.Error("sessions must not parse as a collection")
	}
}

func TestOperationIDParsing(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"op-01J9Z8K3M4N5P6Q7R8S9T0V1W2", "operation/op-01J9Z8K3M4N5P6Q7R8S9T0V1W2"} {
		id, err := application.ParseOperationID(text)
		if err != nil || id.String() != "op-01J9Z8K3M4N5P6Q7R8S9T0V1W2" || id.Ref() != "operation/op-01J9Z8K3M4N5P6Q7R8S9T0V1W2" {
			t.Errorf("ParseOperationID(%q) = %v, %v", text, id, err)
		}
	}
	for _, text := range []string{"", "op-", "op-lowercase0000000000000000", "workload/api", "op-01J9Z8K3M4N5P6Q7R8S9T0V1W2X"} {
		if _, err := application.ParseOperationID(text); err == nil {
			t.Errorf("ParseOperationID(%q) succeeded, want an error", text)
		}
	}
	var zero application.OperationID
	if !zero.IsZero() {
		t.Error("the zero ID must report IsZero")
	}
	if _, err := application.OperationIDFromRef(refs(t, "workload/api")[0]); err == nil {
		t.Error("OperationIDFromRef accepted a workload reference")
	}
}

func objectRefs(objects []application.RealizedObject) []string {
	out := make([]string, 0, len(objects))
	for _, o := range objects {
		out = append(out, o.Ref)
	}
	return out
}
