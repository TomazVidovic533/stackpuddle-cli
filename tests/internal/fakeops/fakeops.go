// Package fakeops is the one test double of application.Operations. It stands in for the engine in the
// Docker-free tests so they run where no Docker or control plane runs; the integration suite runs the same
// verbs against the real in-process adapter, which is what keeps the double honest. Everything below the
// port is still real: the project is loaded from disk and the use cases are the production ones.
package fakeops

import (
	"context"
	"sync"
	"time"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// Operations records what the use cases ask and answers from the configured functions.
type Operations struct {
	mu sync.Mutex

	// SubmitFunc answers Submit; nil answers with a succeeded operation.
	SubmitFunc func(ctx context.Context, req application.SubmitRequest) (application.SubmitResult, error)
	// RunFunc answers Run; nil answers with a succeeded run whose job step exited 0.
	RunFunc func(ctx context.Context, req application.RunRequest) (application.SubmitResult, error)
	// InspectFunc answers Inspect; nil answers with an empty environment.
	InspectFunc func(ctx context.Context, q application.InspectQuery) (application.Inspection, error)
	// CancelFunc answers Cancel; nil accepts.
	CancelFunc func(ctx context.Context, scope application.Scope, id application.OperationID) error

	submits []application.SubmitRequest
	runs    []application.RunRequest
	cancels []application.OperationID
}

// Submits returns the requests Submit received.
func (o *Operations) Submits() []application.SubmitRequest {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]application.SubmitRequest(nil), o.submits...)
}

// Runs returns the requests Run received.
func (o *Operations) Runs() []application.RunRequest {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]application.RunRequest(nil), o.runs...)
}

// Cancels returns the operation IDs Cancel received.
func (o *Operations) Cancels() []application.OperationID {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]application.OperationID(nil), o.cancels...)
}

// Submit records req and answers it.
func (o *Operations) Submit(ctx context.Context, req application.SubmitRequest) (application.SubmitResult, error) {
	o.mu.Lock()
	o.submits = append(o.submits, req)
	fn := o.SubmitFunc
	o.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	if req.DryRun {
		return application.SubmitResult{Preview: application.Preview{BasedOnGeneration: 3}}, nil
	}
	return application.SubmitResult{Record: Record(application.StatusSucceeded), HasRecord: true}, nil
}

// Run records req and answers it. Each default answer is a new operation, as the engine records one per run.
func (o *Operations) Run(ctx context.Context, req application.RunRequest) (application.SubmitResult, error) {
	o.mu.Lock()
	o.runs = append(o.runs, req)
	n := len(o.runs)
	fn := o.RunFunc
	o.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
	if req.DryRun {
		return application.SubmitResult{Preview: application.Preview{BasedOnGeneration: 3}}, nil
	}
	return application.SubmitResult{Record: RunRecord(n, 0), HasRecord: true}, nil
}

// Inspect answers q.
func (o *Operations) Inspect(ctx context.Context, q application.InspectQuery) (application.Inspection, error) {
	o.mu.Lock()
	fn := o.InspectFunc
	o.mu.Unlock()
	if fn != nil {
		return fn(ctx, q)
	}
	return application.EnvironmentState{}, nil
}

// Cancel records id and answers it.
func (o *Operations) Cancel(ctx context.Context, scope application.Scope, id application.OperationID) error {
	o.mu.Lock()
	o.cancels = append(o.cancels, id)
	fn := o.CancelFunc
	o.mu.Unlock()
	if fn != nil {
		return fn(ctx, scope, id)
	}
	return nil
}

// Advertise reports an engine with no capability.
func (*Operations) Advertise() (model.EngineInfo, error) { return model.EngineInfo{}, nil }

// ID returns a valid operation ID, "op-" and 26 characters, distinguished by n (0-9).
func ID(n int) application.OperationID {
	id, err := application.ParseOperationID("op-01J9Z8K3M4N5P6Q7R8S9T0V1W" + string(rune('0'+n%10)))
	if err != nil {
		panic(err) // a fixed spelling; a failure is a bug in this file
	}
	return id
}

// Record returns a complete operation record with the given status and one step.
func Record(status application.Status) application.OperationRecord {
	project, _ := model.ParseProjectName("shop")
	env, _ := model.ParseEnvironmentName("local")
	created := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	record := application.OperationRecord{
		ID: ID(1), Scope: application.Scope{Project: project, Environment: env}, ScopeUID: "sc-01J9Z8K3M4N5P6Q7R8S9T0V1W2",
		Command: application.CommandUp, Refs: []string{"workload/api"}, Revision: 1, Status: status,
		Actor: application.ActorOSUser, CreatedAt: created, UpdatedAt: created.Add(time.Second),
		Steps: []application.StepRecord{{
			ID: "s1", Action: "workload.apply", Object: "workload/api", Status: application.StepCompleted,
			StartedAt: created, FinishedAt: created.Add(time.Second),
		}},
	}
	if status.Terminal() {
		record.FinishedAt = created.Add(2 * time.Second)
	}
	return record
}

// RunRecord returns a succeeded or failed run operation, distinguished by n (0-9), whose single job step
// exited with exitCode.
func RunRecord(n, exitCode int) application.OperationRecord {
	record := Record(application.StatusSucceeded)
	record.ID = ID(n)
	record.Command = application.CommandRun
	record.Refs = []string{"workload/migrate"}
	record.Steps = []application.StepRecord{{
		ID: "s1", Action: "compose.job", Object: "workload/migrate", Status: application.StepCompleted,
		Job: &application.JobResult{ExitCode: exitCode},
	}}
	if exitCode != 0 {
		record.Status = application.StatusFailed
		record.Steps[0].Status = application.StepFailed
		record.Problem = &application.Problem{Code: "job.exited_nonzero", Category: "validation"}
	}
	return record
}
