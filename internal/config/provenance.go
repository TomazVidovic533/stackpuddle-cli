// Package config resolves the effective configuration of a project environment under the precedence documented in docs/commands.md:
// module defaults, project declarations, the selected environment, explicitly applicable ignored local
// values, then explicitly mapped process inputs. Secrets resolve separately and are held as secret.Value.
// Every effective value carries its source and the sources it shadowed. The package reads no process
// environment itself: the lookup arrives as a function.
package config

import (
	"fmt"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/config/secret"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// Layer is the configuration layer a value came from, lowest precedence first.
type Layer int

// The layers, in precedence order. LayerProvider is not a precedence layer: it marks a secret that no
// layer supplied and that the environment's secret provider resolves later.
const (
	LayerModuleDefault Layer = iota + 1
	LayerProject
	LayerEnvironment
	LayerLocal
	LayerProcess
	LayerProvider
)

// String returns the layer name used in text and JSON output.
func (l Layer) String() string {
	switch l {
	case LayerModuleDefault:
		return "module-default"
	case LayerProject:
		return "project"
	case LayerEnvironment:
		return "environment"
	case LayerLocal:
		return "local"
	case LayerProcess:
		return "process"
	case LayerProvider:
		return "provider"
	}
	return "invalid"
}

// Source says where one value came from. Location names the declaration. What a layer adds is held
// privately and read through Local, Process and Provider, which report absence explicitly instead of as an
// empty string. None of the detail holds a value. Build one with NewSource, NewLocalSource,
// NewProcessSource or NewProviderSource.
type Source struct {
	Location model.Location

	layer     Layer
	localFile string
	localKey  string
	process   string
	provider  string
}

// DeclaredLayer is a layer whose sources add no detail: a declaration in the module defaults, the project or
// the selected environment. The local, process and provider layers have their own constructors, so a source
// can never claim one of them without the detail that layer carries.
type DeclaredLayer int

// The declared layers.
const (
	DeclaredModuleDefault DeclaredLayer = iota + 1
	DeclaredProject
	DeclaredEnvironment
)

// layer returns the Layer of l; an unknown DeclaredLayer maps to the zero Layer, which prints as "invalid".
func (l DeclaredLayer) layer() Layer {
	switch l {
	case DeclaredModuleDefault:
		return LayerModuleDefault
	case DeclaredProject:
		return LayerProject
	case DeclaredEnvironment:
		return LayerEnvironment
	}
	return Layer(0)
}

// NewSource returns a source of a declared layer.
func NewSource(layer DeclaredLayer, loc model.Location) Source {
	return Source{layer: layer.layer(), Location: loc}
}

// Layer returns the layer the source belongs to.
func (s Source) Layer() Layer { return s.layer }

// NewLocalSource returns a LayerLocal source naming the ignored file and the key read from it.
func NewLocalSource(loc model.Location, file, key string) Source {
	return Source{layer: LayerLocal, Location: loc, localFile: file, localKey: key}
}

// NewProcessSource returns a LayerProcess source naming the process variable.
func NewProcessSource(loc model.Location, variable string) Source {
	return Source{layer: LayerProcess, Location: loc, process: variable}
}

// NewProviderSource returns a LayerProvider source naming the secret provider.
func NewProviderSource(loc model.Location, provider string) Source {
	return Source{layer: LayerProvider, Location: loc, provider: provider}
}

// Local returns the local file and key, and false for any other layer.
func (s Source) Local() (file, key string, ok bool) {
	return s.localFile, s.localKey, s.layer == LayerLocal
}

// Process returns the process variable name, and false for any other layer.
func (s Source) Process() (string, bool) {
	return s.process, s.layer == LayerProcess
}

// Provider returns the secret provider name, and false for any other layer.
func (s Source) Provider() (string, bool) {
	return s.provider, s.layer == LayerProvider
}

// ShadowReason says why a source did not supply the effective value.
type ShadowReason int

// The reasons a source is shadowed.
const (
	// ShadowOverridden means a source of higher precedence supplied the value.
	ShadowOverridden ShadowReason = iota + 1
	// ShadowCI means the local layer is excluded because the run is a CI run.
	ShadowCI
)

// String returns "overridden" or "ci".
func (r ShadowReason) String() string {
	switch r {
	case ShadowOverridden:
		return "overridden"
	case ShadowCI:
		return "ci"
	}
	return "invalid"
}

// Shadow is a source that did not supply the effective value, with the reason.
type Shadow struct {
	Source Source
	Reason ShadowReason
}

// Status says whether a value was supplied.
type Status int

// The statuses of a value.
const (
	// StatusSupplied means a layer supplied the value, possibly the empty string.
	StatusSupplied Status = iota + 1
	// StatusDeferred means a secret no layer supplied; the environment's secret provider resolves it.
	StatusDeferred
	// StatusMissing means a non-secret value no layer supplied.
	StatusMissing
)

// String returns "supplied", "deferred" or "missing".
func (s Status) String() string {
	switch s {
	case StatusSupplied:
		return "supplied"
	case StatusDeferred:
		return "deferred"
	case StatusMissing:
		return "missing"
	}
	return "invalid"
}

// Value is one effective value. A sensitive value is held as a secret.Value and a plain one as text; the
// constructors set exactly one, so the two can never disagree. Source is meaningful unless Status is
// StatusMissing; Shadowed lists the sources that did not supply the value.
type Value struct {
	Source   Source
	Shadowed []Shadow

	status    Status
	sensitive bool
	text      string
	secret    secret.Value
}

// NewText returns a supplied, non-sensitive value.
func NewText(text string, src Source) Value {
	return Value{status: StatusSupplied, Source: src, text: text}
}

// NewSecret returns a supplied, sensitive value.
func NewSecret(text string, src Source) Value {
	return Value{status: StatusSupplied, Source: src, sensitive: true, secret: secret.New(text)}
}

// NewDeferred returns the sensitive value that a secret provider resolves later.
func NewDeferred(src Source) Value {
	return Value{status: StatusDeferred, Source: src, sensitive: true}
}

// NewMissing returns a value no layer supplied.
func NewMissing() Value {
	return Value{status: StatusMissing}
}

// String prints the status and, for plain text, the quoted text. fmt cannot reach the unexported secret
// field, so this method is what keeps any %v or %+v print of a Value redacted.
func (v Value) String() string {
	if v.sensitive {
		return v.status.String() + " " + v.secret.String()
	}
	return fmt.Sprintf("%s %q", v.status, v.text)
}

// GoString makes %#v print the same redacted form.
func (v Value) GoString() string { return v.String() }

// Status returns whether the value was supplied, deferred or missing.
func (v Value) Status() Status { return v.status }

// Sensitive reports whether the value is a secret.
func (v Value) Sensitive() bool { return v.sensitive }

// Text returns the plain text of a supplied, non-sensitive value, and false otherwise.
func (v Value) Text() (string, bool) {
	return v.text, v.status == StatusSupplied && !v.sensitive
}

// Secret returns the held secret of a supplied, sensitive value, and false otherwise.
func (v Value) Secret() (secret.Value, bool) {
	return v.secret, v.status == StatusSupplied && v.sensitive
}

// Entry is one named effective value: an environment variable of a workload or a leaf of a module's
// options (named by its JSON pointer).
type Entry struct {
	Name  string
	Value Value
}

// View is the effective configuration of one object in one environment. Unknown is true when the object's
// definition is unresolved, so no value can be shown without guessing.
type View struct {
	Ref         model.Ref
	Environment model.EnvironmentName
	Unknown     bool
	Entries     []Entry
}
