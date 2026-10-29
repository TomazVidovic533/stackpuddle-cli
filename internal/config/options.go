package config

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"

	"github.com/TomazVidovic533/stackpuddle/model"
)

// moduleView shows the effective options of one module instance in the selected environment, one entry
// per leaf, named by its JSON pointer, with the layer that supplied it. The options come from the model's
// EnvironmentOptions, the one owner of the merge. A definition the caller did not supply cannot be
// merged, so the view is Unknown rather than a guessed default.
func (r *resolver) moduleView(m model.ModuleInstance) View {
	view := View{Ref: mustRef("module/" + m.Key().String()), Environment: r.env}
	options, ok := r.p.EnvironmentOptions(r.env, m.Key())
	if !ok {
		view.Unknown = true
		return view
	}
	leaves := optionLeaves(options.JSON())
	for _, leaf := range leaves {
		src := options.Source(leaf.pointer)
		view.Entries = append(view.Entries, Entry{
			Name: leaf.pointer, Value: NewText(leaf.text, NewSource(optionLayer(src.Layer), src.Location)),
		})
	}
	return view
}

func optionLayer(l model.OptionLayer) DeclaredLayer {
	switch l {
	case model.OptionLayerDefault:
		return DeclaredModuleDefault
	case model.OptionLayerProject:
		return DeclaredProject
	case model.OptionLayerEnvironment:
		return DeclaredEnvironment
	}
	return DeclaredLayer(0)
}

type optionLeaf struct {
	pointer string
	text    string
}

// optionLeaves walks canonical JSON options. Objects recurse in key order; scalars, lists and empty
// objects are leaves (lists replace as a whole, so they are one value).
func optionLeaves(doc json.RawMessage) []optionLeaf {
	var leaves []optionLeaf
	var walk func(pointer string, raw json.RawMessage)
	walk = func(pointer string, raw json.RawMessage) {
		var object map[string]json.RawMessage
		if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '{' &&
			json.Unmarshal(trimmed, &object) == nil && len(object) > 0 {
			keys := make([]string, 0, len(object))
			for k := range object {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				walk(pointer+"/"+escapePointer(k), object[k])
			}
			return
		}
		leaves = append(leaves, optionLeaf{pointer: pointer, text: leafText(raw)})
	}
	walk("", doc)
	return leaves
}

// leafText prints a string leaf as its text and any other leaf as compact JSON.
func leafText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil {
		return strings.TrimSpace(string(raw))
	}
	return compact.String()
}

func escapePointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
