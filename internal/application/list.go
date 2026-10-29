package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/TomazVidovic533/stackpuddle/model"
)

// Collection is a kind of object `list` shows. The set is closed; operations and sessions are named by the
// command and refused there, because nothing publishes them yet.
type Collection string

// The collections `list` serves.
const (
	CollectionApplications Collection = "applications"
	CollectionWorkloads    Collection = "workloads"
	CollectionModules      Collection = "modules"
	CollectionResources    Collection = "resources"
	CollectionBindings     Collection = "bindings"
	CollectionEnvironments Collection = "environments"
)

// Collections returns every collection `list` serves.
func Collections() []Collection {
	return []Collection{
		CollectionApplications, CollectionWorkloads, CollectionModules, CollectionResources,
		CollectionBindings, CollectionEnvironments,
	}
}

// ParseCollection returns the collection spelled s.
func ParseCollection(s string) (Collection, bool) { return parseNamed(s, Collections()) }

// Attribute is one named fact about a listed item.
type Attribute struct {
	Key   string
	Value string
}

// ListItem is one row of a collection: its reference and its facts, in a fixed order.
type ListItem struct {
	Ref        string
	Attributes []Attribute
}

// ListResult is one collection of one project.
type ListResult struct {
	Collection  Collection
	Environment model.EnvironmentName
	HasEnv      bool
	Items       []ListItem
}

// ListRequest names the collection to list.
type ListRequest struct {
	Selection  Selection
	Collection Collection
}

// List returns one collection of the project. Resources come from the environment's state, the rest from
// the loaded model, so every collection but resources needs no engine.
type List func(ctx context.Context, req ListRequest) (ListResult, error)

// NewList returns the list use case.
func NewList(src ProjectSource, ops Operations) List {
	return func(ctx context.Context, req ListRequest) (ListResult, error) {
		var result ListResult
		err := src.With(ctx, req.Selection, LoadOptions{}, func(ctx context.Context, p LoadedProject) error {
			var listErr error
			result, listErr = list(ctx, p, req, ops)
			return listErr
		})
		return result, err
	}
}

func list(ctx context.Context, p LoadedProject, req ListRequest, ops Operations) (ListResult, error) {
	if err := requireValid(p); err != nil {
		return ListResult{}, err
	}
	result := ListResult{Collection: req.Collection}
	switch req.Collection {
	case CollectionApplications:
		for _, a := range p.Project.Applications() {
			result.Items = append(result.Items, ListItem{Ref: "app/" + a.Key().String(), Attributes: []Attribute{
				{"default", fmt.Sprint(a.Default())}, {"workloads", joinKeys(a.Workloads())},
			}})
		}
	case CollectionWorkloads:
		for _, w := range p.Project.Workloads() {
			result.Items = append(result.Items, ListItem{Ref: "workload/" + w.Key().String(), Attributes: []Attribute{
				{"type", w.Type().String()},
			}})
		}
	case CollectionModules:
		for _, m := range p.Project.ModuleInstances() {
			result.Items = append(result.Items, ListItem{Ref: "module/" + m.Key().String(), Attributes: []Attribute{
				{"definition", m.Definition().String()},
			}})
		}
	case CollectionBindings:
		for _, b := range p.Project.Bindings() {
			result.Items = append(result.Items, ListItem{Ref: b.Ref().String(), Attributes: []Attribute{
				{"source", b.Source().Object.String() + "#" + b.Source().Export.String()},
			}})
		}
	case CollectionEnvironments:
		for _, e := range p.Project.Environments() {
			result.Items = append(result.Items, ListItem{Ref: "env/" + e.Name().String(), Attributes: []Attribute{
				{"secrets", e.SecretProvider().String()},
			}})
		}
	case CollectionResources:
		return listResources(ctx, p, req.Selection, ops)
	}
	return result, nil
}

func listResources(ctx context.Context, p LoadedProject, sel Selection, ops Operations) (ListResult, error) {
	scope, err := scopeOf(p, sel)
	if err != nil {
		return ListResult{}, err
	}
	inspection, err := ops.Inspect(ctx, InspectQuery{Scope: scope})
	if err != nil {
		return ListResult{}, err
	}
	state, ok := inspection.(EnvironmentState)
	if !ok {
		return ListResult{}, Unexpected("list", errors.New("the engine returned an operation for an environment query"))
	}
	result := ListResult{Collection: CollectionResources, Environment: scope.Environment, HasEnv: true}
	for _, o := range state.Objects {
		result.Items = append(result.Items, ListItem{Ref: o.Ref, Attributes: []Attribute{{"state", o.State}}})
	}
	return result, nil
}

func joinKeys(keys []model.ObjectKey) string {
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += ","
		}
		out += k.String()
	}
	return out
}
