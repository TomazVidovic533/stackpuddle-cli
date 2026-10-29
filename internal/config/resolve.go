package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// Codes of the findings Resolve reports.
const (
	// CodeInputMissing: a non-secret input has no value in any layer.
	CodeInputMissing = "config.input_missing"
	// CodeUnknownEnvironment: the environment is not declared.
	CodeUnknownEnvironment = "config.unknown_environment"
)

// LocalPolicy says whether developer-local values apply.
type LocalPolicy int

// The policies.
const (
	// LocalApplies lets the ignored local files an environment names supply values.
	LocalApplies LocalPolicy = iota + 1
	// LocalExcludedInCI keeps developer-local values out, as in CI.
	LocalExcludedInCI
)

// Inputs is everything Resolve reads besides the project: parsed local files by the path the environment
// names (an absent file is an absent entry), the process lookup, the local policy and the file that
// declares project-level inputs.
type Inputs struct {
	LocalFiles    map[string]DotenvFile
	LookupProcess func(string) (string, bool)
	Local         LocalPolicy
	RootFile      string
}

// Resolution is the effective configuration of one environment.
type Resolution struct {
	Environment model.EnvironmentName
	Inputs      map[model.InputKey]Value
	Workloads   map[model.ObjectKey]View
	Modules     map[model.ObjectKey]View
}

// Resolve applies the documented layers to env of p. It reads the process only through
// in.LookupProcess and only for the variable names an environment maps. The one finding is
// config.input_missing; resolution continues past it so one run shows every problem. Binding-owned keys
// need no check here: model.Load already refuses an override of a delivered key (binding.delivery_conflict).
func Resolve(p model.Project, env model.EnvironmentName, in Inputs) (Resolution, []diagnostics.Finding) {
	environment, ok := p.Environment(env)
	if !ok {
		return Resolution{}, []diagnostics.Finding{{
			Code: CodeUnknownEnvironment, Message: fmt.Sprintf("environment %q is not declared", env),
		}}
	}
	r := &resolver{p: p, env: env, environment: environment, in: in}
	res := Resolution{
		Environment: env,
		Inputs:      map[model.InputKey]Value{},
		Workloads:   map[model.ObjectKey]View{},
		Modules:     map[model.ObjectKey]View{},
	}
	for _, input := range p.Inputs() {
		res.Inputs[input.Key()] = r.resolveInput(input)
	}
	for _, w := range p.Workloads() {
		res.Workloads[w.Key()] = r.workloadView(w, res.Inputs)
	}
	for _, m := range p.ModuleInstances() {
		res.Modules[m.Key()] = r.moduleView(m)
	}
	return res, r.findings
}

type resolver struct {
	p           model.Project
	env         model.EnvironmentName
	environment model.Environment
	in          Inputs
	findings    []diagnostics.Finding
}

type candidate struct {
	source Source
	text   string
	has    bool // false: a CI-excluded source that supplies nothing
}

func (r *resolver) resolveInput(input model.Input) Value {
	key := input.Key().String()
	sensitive := input.Sensitivity() == model.SensitivitySecret
	var cands []candidate
	if v, ok := input.Value(); ok {
		cands = append(cands, candidate{
			source: NewSource(DeclaredProject, r.rootLocation("/spec/inputs/"+key+"/value")),
			text:   v, has: true,
		})
	}
	envPointer := "/spec/environments/" + r.env.String() + "/inputs/" + key
	for _, s := range r.environment.InputSources() {
		if s.Key != input.Key() {
			continue
		}
		if v, ok := s.Value(); ok {
			cands = append(cands, candidate{
				source: NewSource(DeclaredEnvironment, r.envLocation(envPointer+"/value")),
				text:   v, has: true,
			})
		}
		if local, ok := s.Local(); ok {
			cands = append(cands, r.localCandidate(local, envPointer+"/local"))
		}
		if name, ok := s.Process(); ok {
			cands = append(cands, r.processCandidate(name.String(), envPointer+"/process"))
		}
	}
	return r.assemble(input, sensitive, cands)
}

func (r *resolver) localCandidate(local model.LocalSource, pointer string) candidate {
	src := NewLocalSource(r.envLocation(pointer), local.File, local.Key.String())
	if r.in.Local == LocalExcludedInCI {
		return candidate{source: src}
	}
	if entry, ok := r.in.LocalFiles[local.File].Lookup(local.Key.String()); ok {
		return candidate{source: src, text: entry.Value, has: true}
	}
	return candidate{source: src}
}

func (r *resolver) processCandidate(name, pointer string) candidate {
	src := NewProcessSource(r.envLocation(pointer), name)
	if r.in.LookupProcess != nil {
		if v, ok := r.in.LookupProcess(name); ok {
			return candidate{source: src, text: v, has: true}
		}
	}
	return candidate{source: src}
}

// assemble picks the highest-precedence supplied candidate and records the rest as shadowed.
func (r *resolver) assemble(input model.Input, sensitive bool, cands []candidate) Value {
	winner := -1
	for i, c := range cands {
		if c.has && !r.excluded(c) {
			winner = i
		}
	}
	var shadowed []Shadow
	for i := len(cands) - 1; i >= 0; i-- {
		c := cands[i]
		switch {
		case i == winner:
		case r.excluded(c):
			shadowed = append(shadowed, Shadow{Source: c.source, Reason: ShadowCI})
		case c.has:
			shadowed = append(shadowed, Shadow{Source: c.source, Reason: ShadowOverridden})
		}
	}
	var v Value
	switch {
	case winner >= 0 && sensitive:
		v = NewSecret(cands[winner].text, cands[winner].source)
	case winner >= 0:
		v = NewText(cands[winner].text, cands[winner].source)
	case sensitive:
		v = NewDeferred(NewProviderSource(
			r.envLocation("/spec/environments/"+r.env.String()+"/secrets/provider"),
			r.environment.SecretProvider().String()))
	default:
		v = NewMissing()
		loc := r.rootLocation("/spec/inputs/" + input.Key().String())
		r.findings = append(r.findings, diagnostics.Finding{
			Code: CodeInputMissing, Location: loc,
			Message: fmt.Sprintf("input %q has no value in environment %q", input.Key(), r.env),
		})
	}
	v.Shadowed = shadowed
	return v
}

// excluded reports whether c is a local source kept out by policy.
func (r *resolver) excluded(c candidate) bool {
	return c.source.Layer() == LayerLocal && r.in.Local == LocalExcludedInCI
}

func (r *resolver) workloadView(w model.Workload, inputs map[model.InputKey]Value) View {
	ref := mustRef("workload/" + w.Key().String())
	view := View{Ref: ref, Environment: r.env}
	vars := map[string]entryVar{}
	for _, v := range w.Environment() {
		vars[v.Key.String()] = entryVar{v: v, layer: DeclaredProject,
			pointer: "/spec/workloads/" + w.Key().String() + "/environment/" + v.Key.String()}
	}
	for _, o := range r.environment.WorkloadOverrides() {
		if o.Key != w.Key() {
			continue
		}
		for _, v := range o.Environment {
			vars[v.Key.String()] = entryVar{v: v, layer: DeclaredEnvironment,
				pointer: "/spec/environments/" + r.env.String() + "/overrides/workloads/" + w.Key().String() +
					"/environment/" + v.Key.String()}
		}
	}
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		ev := vars[name]
		view.Entries = append(view.Entries, Entry{Name: name, Value: r.envVarValue(w, ev, inputs)})
	}
	return view
}

type entryVar struct {
	v       model.EnvVar
	layer   DeclaredLayer
	pointer string
}

func (r *resolver) envVarValue(w model.Workload, ev entryVar, inputs map[model.InputKey]Value) Value {
	switch val := ev.v.Value.(type) {
	case model.LiteralValue:
		loc := r.declLocation("workload/"+w.Key().String(), ev.pointer)
		if ev.layer == DeclaredEnvironment {
			loc = r.envLocation(ev.pointer)
		}
		return NewText(val.Text(), NewSource(ev.layer, loc))
	case model.InputValue:
		return inputs[val.Input()]
	}
	return NewMissing()
}

func (r *resolver) rootLocation(pointer string) model.Location {
	return model.NewLocation(r.in.RootFile, pointer, 0, 0)
}

func (r *resolver) envLocation(pointer string) model.Location {
	return r.declLocation("env/"+r.env.String(), pointer)
}

// declLocation places pointer in the file that declared the object ref names.
func (r *resolver) declLocation(ref, pointer string) model.Location {
	parsed, err := model.ParseRef(ref)
	if err != nil {
		return r.rootLocation(pointer)
	}
	prov, ok := r.p.Provenance(parsed)
	if !ok {
		return r.rootLocation(pointer)
	}
	origin := prov.Declared()
	file, hasFile := origin.File()
	if !hasFile {
		file = r.in.RootFile
	}
	// The model records the position of the declaration, not of members below it, so the location names
	// file and pointer only rather than a line that points at a different member.
	return model.NewLocation(file, pointer, 0, 0)
}

// mustRef parses a reference built from validated keys; a failure is a programming error.
func mustRef(s string) model.Ref {
	ref, err := model.ParseRef(s)
	if err != nil {
		panic(fmt.Sprintf("config: invalid internal reference %q", strings.TrimSpace(s)))
	}
	return ref
}
