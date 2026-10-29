package application

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/TomazVidovic533/stackpuddle/model"
)

// TargetRequest is what the user named on the command line for a mutating verb: references, or everything.
type TargetRequest struct {
	Refs    []model.Ref
	All     bool
	Command Command
}

// SelectionSummary is the selection a mutating verb resolved, as it is shown before and after the run.
type SelectionSummary struct {
	Environment model.EnvironmentName
	// All is true when everything is selected; Selected is empty then.
	All bool
	// Groups lists the applications that were expanded.
	Groups []string
	// Selected lists the workloads and module instances chosen, deduplicated and sorted.
	Selected []string
	// Prerequisites lists the jobs the selection requires and does not itself name. They are shown apart.
	Prerequisites []string
}

// refTail returns the part of a reference after its kind prefix.
func refTail(ref model.Ref) string {
	_, tail, _ := strings.Cut(ref.String(), "/")
	return tail
}

// environmentRef returns the environment an env/<name> reference names. It reports false when refs holds none
// and an error when they disagree.
func environmentRef(refs []model.Ref) (model.EnvironmentName, bool, error) {
	var found model.EnvironmentName
	has := false
	for _, ref := range refs {
		if ref.Kind() != model.RefEnv {
			continue
		}
		name, err := model.ParseEnvironmentName(refTail(ref))
		if err != nil {
			return model.EnvironmentName{}, false, NewError(ClassUsage, "reference.invalid_ref", ref.String()+" is not a valid environment reference").WithCause(err)
		}
		if has && name != found {
			return model.EnvironmentName{}, false, NewError(ClassUsage, "selection.several_environments", "the references name more than one environment")
		}
		found, has = name, true
	}
	return found, has, nil
}

// planTargets resolves req against p for env. Groups expand and deduplicate, a module selection does not
// select its consumers, a job never runs because its group was selected, and --all and references exclude
// each other. It is pure: no I/O, no clock.
func planTargets(p model.Project, env model.EnvironmentName, req TargetRequest) (Targets, SelectionSummary, error) {
	summary := SelectionSummary{Environment: env}
	hasEnvRef := slices.ContainsFunc(req.Refs, func(r model.Ref) bool { return r.Kind() == model.RefEnv })
	switch {
	case req.All && len(req.Refs) > 0:
		return Targets{}, summary, NewError(ClassUsage, "selection.all_and_refs", "--all and explicit references exclude each other")
	case hasEnvRef && len(req.Refs) > 1:
		return Targets{}, summary, NewError(ClassUsage, "selection.env_with_refs", "env/<name> selects the whole environment and takes no other reference")
	case req.All || hasEnvRef:
		summary.All = true
		return Targets{All: true}, summary, nil
	}
	refs := req.Refs
	if len(refs) == 0 {
		var err error
		if refs, err = defaultRefs(p, req.Command); err != nil {
			return Targets{}, summary, err
		}
	}
	chosen := map[string]model.ObjectRef{}
	for _, ref := range refs {
		if err := expandRef(p, ref, chosen, &summary); err != nil {
			return Targets{}, summary, err
		}
	}
	targets := Targets{}
	for _, name := range slices.Sorted(maps.Keys(chosen)) {
		targets.Refs = append(targets.Refs, chosen[name])
		summary.Selected = append(summary.Selected, name)
	}
	summary.Groups = slices.Compact(slices.Sorted(slices.Values(summary.Groups)))
	summary.Prerequisites = prerequisitesOf(p, chosen)
	if len(targets.Refs) == 0 {
		return Targets{}, summary, NewError(ClassUsage, "selection.empty", "the selection holds no workload or module instance")
	}
	return targets, summary, nil
}

// defaultRefs is the selection of a verb without references: only `up` has one, the default applications.
func defaultRefs(p model.Project, cmd Command) ([]model.Ref, error) {
	switch cmd {
	case CommandUp:
		var refs []model.Ref
		for _, app := range p.Applications() {
			if !app.Default() {
				continue
			}
			ref, err := model.ParseRef("app/" + app.Key().String())
			if err != nil {
				return nil, Unexpected("selection", err)
			}
			refs = append(refs, ref)
		}
		if len(refs) == 0 {
			return nil, NewError(ClassUsage, "selection.no_default", "the project declares no default application; name references or pass --all")
		}
		return refs, nil
	case CommandStop, CommandDestroy:
		return nil, NewError(ClassUsage, "selection.scope_required", string(cmd)+" needs an explicit scope: name references, "+scopeHint(cmd))
	}
	return nil, NewError(ClassUsage, "selection.scope_required", "name references or pass --all")
}

func scopeHint(cmd Command) string {
	if cmd == CommandStop {
		return "or pass --all"
	}
	return "or env/<name>"
}

func expandRef(p model.Project, ref model.Ref, chosen map[string]model.ObjectRef, summary *SelectionSummary) error {
	switch ref.Kind() {
	case model.RefApp:
		return expandApp(p, ref, chosen, summary)
	case model.RefWorkload:
		if !slices.ContainsFunc(p.Workloads(), func(w model.Workload) bool { return "workload/"+w.Key().String() == ref.String() }) {
			return unknownObject(ref)
		}
		return choose(ref, chosen)
	case model.RefModule:
		if !slices.ContainsFunc(p.ModuleInstances(), func(m model.ModuleInstance) bool { return "module/"+m.Key().String() == ref.String() }) {
			return unknownObject(ref)
		}
		return choose(ref, chosen)
	case model.RefClient, model.RefEnv, model.RefResource, model.RefOperation, model.RefBinding:
	}
	return NewError(ClassUsage, "selection.unsupported_ref_kind",
		fmt.Sprintf("%s cannot be selected; use app/<key>, workload/<key>, module/<key> or env/<name>", ref))
}

func expandApp(p model.Project, ref model.Ref, chosen map[string]model.ObjectRef, summary *SelectionSummary) error {
	for _, app := range p.Applications() {
		if "app/"+app.Key().String() != ref.String() {
			continue
		}
		summary.Groups = append(summary.Groups, ref.String())
		for _, key := range app.Workloads() {
			if isJob(p, key) {
				continue // a job runs on its own reference or as a prerequisite, never because its group was selected
			}
			member, err := model.ParseRef("workload/" + key.String())
			if err != nil {
				return Unexpected("selection", err)
			}
			if err := choose(member, chosen); err != nil {
				return err
			}
		}
		return nil
	}
	return unknownObject(ref)
}

func isJob(p model.Project, key model.ObjectKey) bool {
	for _, w := range p.Workloads() {
		if w.Key() == key {
			return w.Type() == model.WorkloadJob
		}
	}
	return false
}

func choose(ref model.Ref, chosen map[string]model.ObjectRef) error {
	object, err := model.ParseObjectRef(ref.String())
	if err != nil {
		return Unexpected("selection", err)
	}
	chosen[ref.String()] = object
	return nil
}

func unknownObject(ref model.Ref) *Error {
	return NewError(ClassNotFound, "project.unknown_object", fmt.Sprintf("%s is not declared", ref))
}

// prerequisitesOf lists the jobs the chosen workloads require that the selection does not name itself.
func prerequisitesOf(p model.Project, chosen map[string]model.ObjectRef) []string {
	need := map[string]bool{}
	for _, w := range p.Workloads() {
		if _, ok := chosen["workload/"+w.Key().String()]; !ok {
			continue
		}
		for _, job := range w.Prerequisites() {
			if _, named := chosen["workload/"+job.String()]; !named {
				need["workload/"+job.String()] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(need))
}

// unselectedConsumers names the objects a change to the selected ones reaches although the selection does not
// name them: the consumers of a selected object's exports through bindings, and the other instances of a
// definition a selected module instance shares.
func unselectedConsumers(p model.Project, targets Targets) []string {
	selected := map[string]bool{}
	for _, ref := range targets.Refs {
		selected[ref.String()] = true
	}
	found := map[string]bool{}
	for _, b := range p.Bindings() {
		consumer := b.Ref().Consumer.String()
		if selected[b.Source().Object.String()] && !selected[consumer] {
			found[consumer] = true
		}
	}
	for _, m := range p.ModuleInstances() {
		if selected["module/"+m.Key().String()] {
			continue
		}
		for _, other := range p.ModuleInstances() {
			if selected["module/"+other.Key().String()] && other.Definition() == m.Definition() {
				found["module/"+m.Key().String()] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(found))
}
