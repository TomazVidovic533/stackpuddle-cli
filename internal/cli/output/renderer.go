// Package output renders the typed results of the commands. There is one Renderer per format; commands
// never format output themselves, so the text and JSON forms carry the same facts and the interactive
// shell adds a third renderer without touching a command.
package output

import (
	"io"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/config"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// Format is an output format. The set is closed.
type Format int

// The formats `--output` accepts.
const (
	FormatText Format = iota + 1
	FormatJSON
)

// ParseFormat returns the format named by s ("text" or "json"); ok is false for anything else.
func ParseFormat(s string) (Format, bool) {
	switch s {
	case "text":
		return FormatText, true
	case "json":
		return FormatJSON, true
	}
	return 0, false
}

// String returns "text" or "json".
func (f Format) String() string {
	switch f {
	case FormatText:
		return "text"
	case FormatJSON:
		return "json"
	}
	return "invalid"
}

// CancelNote is said whenever a cancel request is accepted: cancellation acts between steps.
const CancelNote = "cancellation acts between steps: a long step such as an image pull or initdb runs to its own bound before the operation stops; partial outcomes are kept"

// RemoveNote is said whenever remove cut a declaration: nothing physical changed.
const RemoveNote = "only the declaration was removed; its realization is retained until destroy"

// ViewOptions selects what a rendered view includes.
type ViewOptions struct {
	// Explain adds the source and the shadowed sources of each value.
	Explain bool
}

// Failure is an expected failure ready to print: its class, stable code, message and the findings that
// explain it.
type Failure struct {
	Class    string
	Code     string
	Message  string
	Findings []diagnostics.Finding
	// Operation is the recorded operation the failure ended, when it ended one: an operation failure, an
	// interruption or a cancellation. A submission failure has none.
	Operation *application.OperationRecord
}

// Renderer writes the results of the commands in one format. Secret values never reach it as text: a
// config.Value holds them as secret.Value, which prints as "[redacted]".
type Renderer interface {
	Report(w io.Writer, r diagnostics.Report) error
	View(w io.Writer, v config.View, opts ViewOptions) error
	Version(w io.Writer, v model.VersionInfo) error
	Failure(w io.Writer, f Failure) error
	// Mutation prints the result of up, stop or destroy: the selection, then the operation or the preview.
	Mutation(w io.Writer, r application.MutateResult) error
	// Operation prints one operation record.
	Operation(w io.Writer, r application.OperationRecord) error
	// CancelRequested prints that the cancel request for id was written.
	CancelRequested(w io.Writer, id application.OperationID) error
	// State prints the state view of one reference.
	State(w io.Writer, v application.StateView) error
	// List prints one collection.
	List(w io.Writer, r application.ListResult) error
	// Invocation prints the result of run: the invocation ID and native exit code, or the dry-run preview.
	Invocation(w io.Writer, r application.RunJobResult) error
	// Removed prints what remove cut.
	Removed(w io.Writer, r application.Removed) error
}

// NewRenderer returns the renderer of f. An invalid format yields the text renderer.
func NewRenderer(f Format) Renderer {
	if f == FormatJSON {
		return jsonRenderer{}
	}
	return textRenderer{}
}
