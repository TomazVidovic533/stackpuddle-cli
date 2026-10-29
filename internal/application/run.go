package application

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/TomazVidovic533/stackpuddle/model"
)

// actionJob is the action of the step that runs the job's container.
const actionJob = "compose.job"

const workloadRefPrefix = "workload/"

// RunJobRequest is one explicit invocation of a job from the command line.
type RunJobRequest struct {
	Selection Selection
	// Job is the job's key, or its reference "workload/<key>".
	Job string
	// DryRun shows the invocation and runs nothing.
	DryRun bool
	// Wait bounds the wait for a busy environment; zero fails at once.
	Wait time.Duration
	// Progress, when set, is called after every persisted change of the operation.
	Progress func(OperationRecord)
}

// RunJobResult is what an invocation did.
type RunJobResult struct {
	Environment model.EnvironmentName
	Job         string
	DryRun      bool
	// Record is the recorded operation; HasRecord is false for a dry run.
	Record    OperationRecord
	HasRecord bool
	Preview   Preview
	// Invocation is "<operation id>/<step id>" of the job step, empty for a dry run.
	Invocation string
	// Result is the job's native result, set once the job exited.
	Result *JobResult
}

// RunJob invokes one job as its own recorded operation, with its own invocation ID and native result. A job
// that exits non-zero is an operation failure that carries the record.
type RunJob func(ctx context.Context, req RunJobRequest) (RunJobResult, error)

// NewRunJob returns the job invocation use case over the project source, the module resolver, the operations
// port and the process lookup.
func NewRunJob(src ProjectSource, mods ModuleResolver, ops Operations, lookup func(string) (string, bool)) RunJob {
	return func(ctx context.Context, req RunJobRequest) (RunJobResult, error) {
		var result RunJobResult
		err := withProject(ctx, src, mods, req.Selection, func(ctx context.Context, p LoadedProject) error {
			var runErr error
			result, runErr = runJob(ctx, p, req, ops, lookup)
			return runErr
		})
		return result, err
	}
}

func runJob(ctx context.Context, p LoadedProject, req RunJobRequest, ops Operations, lookup func(string) (string, bool)) (RunJobResult, error) {
	if err := requireValid(p); err != nil {
		return RunJobResult{}, err
	}
	env, err := inspectionEnvironment(p.Project, req.Selection)
	if err != nil {
		return RunJobResult{}, err
	}
	job, err := jobRef(p.Project, req.Job)
	if err != nil {
		return RunJobResult{}, err
	}
	inputs, err := submitInputs(ctx, p, env, lookup, true)
	if err != nil {
		return RunJobResult{}, err
	}
	out := RunRequest{
		Scope: Scope{Project: p.Project.Name(), Environment: env}, Project: p.Project, ProjectDir: p.Dir,
		Job: job, Inputs: inputs, DryRun: req.DryRun, Wait: req.Wait, Progress: req.Progress,
	}
	res, err := ops.Run(ctx, out)
	if err != nil {
		return RunJobResult{}, err
	}
	result := RunJobResult{
		Environment: env, Job: job.String(), DryRun: req.DryRun, Record: res.Record, HasRecord: res.HasRecord, Preview: res.Preview,
	}
	if res.HasRecord {
		result.Invocation, result.Result = invocationOf(res.Record)
	}
	return result, nil
}

// jobRef resolves the user's spelling to a declared job. Anything declared that is not a job is a usage error,
// so a service is never started by a typo.
func jobRef(p model.Project, spelled string) (model.ObjectRef, error) {
	key := strings.TrimPrefix(spelled, workloadRefPrefix)
	ref, err := model.ParseObjectRef(workloadRefPrefix + key)
	if err != nil {
		return model.ObjectRef{}, NewError(ClassUsage, "reference.invalid_ref",
			fmt.Sprintf("%q is not a valid job; use the job's key or workload/<key>", spelled)).WithCause(err)
	}
	idx := slices.IndexFunc(p.Workloads(), func(w model.Workload) bool { return workloadRefPrefix+w.Key().String() == ref.String() })
	if idx < 0 {
		return model.ObjectRef{}, NewError(ClassNotFound, "project.unknown_object", ref.String()+" is not declared")
	}
	if p.Workloads()[idx].Type() != model.WorkloadJob {
		return model.ObjectRef{}, NewError(ClassUsage, "run.not_a_job", ref.String()+" is a service, not a job; run takes a job")
	}
	return ref, nil
}

// invocationOf finds the job step of a recorded run and names the invocation "<operation id>/<step id>".
func invocationOf(rec OperationRecord) (string, *JobResult) {
	for _, s := range rec.Steps {
		if s.Job != nil || s.Action == actionJob {
			return rec.ID.String() + "/" + s.ID, s.Job
		}
	}
	return "", nil
}
