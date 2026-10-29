package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/config"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// InspectRequest is the input of the config inspection: what to show and where to find the project.
type InspectRequest struct {
	Selection Selection
	Ref       model.Ref
}

// InspectConfig returns the effective configuration of a workload or module instance in one environment,
// with the source of every value. Secret values are never part of the result's text: they are held as
// secret.Value.
type InspectConfig func(ctx context.Context, req InspectRequest) (config.View, error)

// NewInspectConfig returns the inspection use case over src. lookup is the process lookup.
func NewInspectConfig(src ProjectSource, lookup func(string) (string, bool)) InspectConfig {
	return func(ctx context.Context, req InspectRequest) (config.View, error) {
		var view config.View
		err := src.With(ctx, req.Selection, LoadOptions{}, func(ctx context.Context, p LoadedProject) error {
			var inspectErr error
			view, inspectErr = inspect(ctx, p, req, lookup)
			return inspectErr
		})
		return view, err
	}
}

func inspect(ctx context.Context, p LoadedProject, req InspectRequest, lookup func(string) (string, bool)) (config.View, error) {
	if len(p.Diagnostics) > 0 {
		return config.View{}, NewError(ClassValidation, "project.invalid", "the project is invalid; run check for the findings").
			WithFindings(diagnostics.FromModel(p.Diagnostics.Sorted()))
	}
	switch req.Ref.Kind() {
	case model.RefWorkload, model.RefModule:
	case model.RefClient, model.RefApp, model.RefEnv, model.RefResource, model.RefOperation, model.RefBinding:
		return config.View{}, NewError(ClassUsage, "inspect.unsupported_ref_kind",
			fmt.Sprintf("inspect --view config supports workload/<key> and module/<key>, not %s", req.Ref.Kind()))
	}
	env, err := inspectionEnvironment(p.Project, req.Selection)
	if err != nil {
		return config.View{}, err
	}
	policy := config.PolicyFor(lookup)
	files, findings, err := config.LoadLocalFiles(ctx, p.Root, p.Project, env, policy)
	if err != nil {
		return config.View{}, Unexpected("inspect", err)
	}
	if len(findings) > 0 {
		return config.View{}, NewError(ClassValidation, "config.local_file_invalid", "a local file is invalid").WithFindings(findings)
	}
	resolution, _ := config.Resolve(p.Project, env, config.Inputs{
		LocalFiles: files, LookupProcess: lookup, Local: policy, RootFile: p.RootFile,
	})
	var view config.View
	ok := false
	if key, parsed := objectKey(req.Ref); parsed {
		if req.Ref.Kind() == model.RefWorkload {
			view, ok = resolution.Workloads[key]
		} else {
			view, ok = resolution.Modules[key]
		}
	}
	if !ok {
		return config.View{}, NewError(ClassNotFound, "project.unknown_object", fmt.Sprintf("%s is not declared", req.Ref))
	}
	return view, nil
}

// objectKey extracts the key of a workload or module reference.
func objectKey(ref model.Ref) (model.ObjectKey, bool) {
	_, key, found := strings.Cut(ref.String(), "/")
	if !found {
		return model.ObjectKey{}, false
	}
	k, err := model.ParseObjectKey(key)
	return k, err == nil
}

func inspectionEnvironment(p model.Project, sel Selection) (model.EnvironmentName, error) {
	if sel.HasEnvironment {
		if _, ok := p.Environment(sel.Environment); !ok {
			return model.EnvironmentName{}, unknownEnvironment(sel.Environment)
		}
		return sel.Environment, nil
	}
	envs := p.Environments()
	switch len(envs) {
	case 0:
		return model.EnvironmentName{}, NewError(ClassNotFound, "project.no_environment", "the project declares no environment")
	case 1:
		return envs[0].Name(), nil
	}
	return model.EnvironmentName{}, NewError(ClassUsage, "inspect.environment_required",
		"the project declares several environments; select one with --env")
}
