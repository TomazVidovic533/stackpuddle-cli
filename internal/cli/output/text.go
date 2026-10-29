package output

import (
	"fmt"
	"io"
	"strings"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/config"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
	"github.com/TomazVidovic533/stackpuddle/model"
)

type textRenderer struct{}

func (textRenderer) Report(w io.Writer, r diagnostics.Report) error {
	var b strings.Builder
	for _, result := range r.Results {
		fmt.Fprintf(&b, "%s", result.Checker)
		if env, ok := result.Environment(); ok {
			fmt.Fprintf(&b, " [%s]", env)
		}
		fmt.Fprintf(&b, ": %s", result.State)
		if !result.ObservedAt.IsZero() {
			fmt.Fprintf(&b, " (observed %s, generation %d)", utcText(result.ObservedAt), result.Generation)
		}
		b.WriteString("\n")
		for _, f := range result.Findings {
			b.WriteString(findingText(f))
		}
	}
	fmt.Fprintf(&b, "outcome: %s\n", r.Outcome())
	_, err := io.WriteString(w, b.String())
	return err
}

func (textRenderer) View(w io.Writer, v config.View, opts ViewOptions) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s in environment %s\n", v.Ref, v.Environment)
	if v.Unknown {
		b.WriteString("  unknown: the module definition is not supplied, so no value is shown\n")
	}
	for _, e := range v.Entries {
		fmt.Fprintf(&b, "  %s = %s\n", e.Name, valueText(e.Value))
		if !opts.Explain {
			continue
		}
		if e.Value.Status() != config.StatusMissing {
			fmt.Fprintf(&b, "      source: %s\n", sourceText(e.Value.Source))
		}
		for _, s := range e.Value.Shadowed {
			fmt.Fprintf(&b, "      shadowed: %s (%s)\n", sourceText(s.Source), s.Reason)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func (textRenderer) Version(w io.Writer, v model.VersionInfo) error {
	cli, m := v.CLI(), v.Model()
	_, err := fmt.Fprintf(w, "stackpuddle %s (%s, %s)\nmodel %s reads %s\n",
		cli.Version, cli.Go, cli.Platform, m.Version, strings.Join(m.APIVersions, ", "))
	return err
}

func (textRenderer) Failure(w io.Writer, f Failure) error {
	var b strings.Builder
	fmt.Fprintf(&b, "error (%s): %s\n", f.Code, f.Message)
	for _, finding := range f.Findings {
		b.WriteString(findingText(finding))
	}
	if f.Operation != nil {
		b.WriteString(operationText(*f.Operation))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// valueText prints a value; a sensitive value prints through secret.Value, which redacts.
func valueText(v config.Value) string {
	switch v.Status() {
	case config.StatusSupplied:
		if sec, ok := v.Secret(); ok {
			return sec.String()
		}
		text, _ := v.Text()
		return fmt.Sprintf("%q", text)
	case config.StatusDeferred:
		return "(deferred: the secret provider resolves it)"
	case config.StatusMissing:
		return "(missing)"
	}
	return "(invalid)"
}

func sourceText(s config.Source) string {
	var b strings.Builder
	b.WriteString(s.Layer().String())
	if file, key, ok := s.Local(); ok {
		fmt.Fprintf(&b, " %s key %s", file, key)
	}
	if name, ok := s.Process(); ok {
		fmt.Fprintf(&b, " variable %s", name)
	}
	if name, ok := s.Provider(); ok {
		fmt.Fprintf(&b, " %s", name)
	}
	if loc := locationText(s.Location); loc != "" {
		fmt.Fprintf(&b, " at %s", loc)
	}
	return b.String()
}

// locationText prints file[:line[:column]] and the pointer, whichever are known.
func locationText(l model.Location) string {
	var parts []string
	if file := l.File(); file != "" {
		pos := file
		if line, col, ok := l.Position(); ok {
			pos = fmt.Sprintf("%s:%d", pos, line)
			if col > 0 {
				pos = fmt.Sprintf("%s:%d", pos, col)
			}
		}
		parts = append(parts, pos)
	}
	if p := l.Pointer(); p != "" {
		parts = append(parts, p)
	}
	return strings.Join(parts, " ")
}

func findingText(f diagnostics.Finding) string {
	if loc := locationText(f.Location); loc != "" {
		return fmt.Sprintf("  %s %s: %s\n", f.Code, loc, f.Message)
	}
	return fmt.Sprintf("  %s: %s%s\n", f.Code, f.Message, consumersText(f))
}

func consumersText(f diagnostics.Finding) string {
	if len(f.Consumers) == 0 {
		return ""
	}
	return " (consumers: " + strings.Join(f.Consumers, ", ") + ")"
}

func (textRenderer) Invocation(w io.Writer, r application.RunJobResult) error {
	var b strings.Builder
	fmt.Fprintf(&b, "environment %s\njob %s\n", r.Environment, r.Job)
	if r.HasRecord {
		fmt.Fprintf(&b, "invocation %s\n", r.Invocation)
		if r.Result != nil {
			fmt.Fprintf(&b, "exit code %d\n", r.Result.ExitCode)
		}
		b.WriteString(operationText(r.Record))
	}
	if r.DryRun {
		fmt.Fprintf(&b, "dry run: nothing was run; based on generation %d\n", r.Preview.BasedOnGeneration)
		for _, a := range r.Preview.Actions {
			b.WriteString(actionText(a))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func (textRenderer) Mutation(w io.Writer, r application.MutateResult) error {
	var b strings.Builder
	fmt.Fprintf(&b, "environment %s\n", r.Selection.Environment)
	b.WriteString(selectionText(r.Selection))
	if r.HasRecord {
		b.WriteString(operationText(r.Record))
	}
	if r.DryRun {
		fmt.Fprintf(&b, "dry run: nothing was changed; based on generation %d\n", r.Preview.BasedOnGeneration)
		for _, a := range r.Preview.Actions {
			b.WriteString(actionText(a))
		}
	}
	if !r.HasRecord && !r.DryRun {
		b.WriteString("nothing to do\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func selectionText(s application.SelectionSummary) string {
	var b strings.Builder
	if s.All {
		b.WriteString("selected: everything\n")
	} else {
		fmt.Fprintf(&b, "selected: %s\n", strings.Join(s.Selected, ", "))
	}
	if len(s.Groups) > 0 {
		fmt.Fprintf(&b, "groups: %s\n", strings.Join(s.Groups, ", "))
	}
	if len(s.Prerequisites) > 0 {
		fmt.Fprintf(&b, "prerequisites (jobs the selection requires): %s\n", strings.Join(s.Prerequisites, ", "))
	}
	return b.String()
}

func actionText(a application.Action) string {
	line := "  " + string(a.Effect)
	if a.Action != "" {
		line += " " + a.Action
	}
	if a.Object != "" {
		line += " " + a.Object
	}
	if a.Unknown {
		line += " (unknown: may have nothing left to do)"
	}
	if a.Problem != "" {
		line += " (" + a.Problem + ")"
	}
	return line + "\n"
}

// operationText prints the accepted operation and its steps.
func operationText(r application.OperationRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "operation %s %s (%s, revision %d)\n", r.ID, r.Status, r.Command, r.Revision)
	if !r.Continues.IsZero() {
		fmt.Fprintf(&b, "  continues %s\n", r.Continues)
	}
	if !r.ContinuedBy.IsZero() {
		fmt.Fprintf(&b, "  continued by %s\n", r.ContinuedBy)
	}
	for _, s := range r.Steps {
		fmt.Fprintf(&b, "  %s %s %s: %s", s.ID, s.Action, s.Object, s.Status)
		if s.Job != nil {
			fmt.Fprintf(&b, " (exit code %d)", s.Job.ExitCode)
		}
		if s.Problem != nil {
			fmt.Fprintf(&b, " (%s)", s.Problem.Code)
		}
		b.WriteString("\n")
	}
	if r.Problem != nil {
		fmt.Fprintf(&b, "  error: %s (%s)\n", r.Problem.Code, r.Problem.Category)
	}
	return b.String()
}

func (textRenderer) Operation(w io.Writer, r application.OperationRecord) error {
	_, err := io.WriteString(w, operationText(r))
	return err
}

func (textRenderer) CancelRequested(w io.Writer, id application.OperationID) error {
	_, err := fmt.Fprintf(w, "cancel requested for %s\n%s\n", id, CancelNote)
	return err
}

func (textRenderer) State(w io.Writer, v application.StateView) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s in environment %s\n", v.Ref, v.Environment)
	fmt.Fprintf(&b, "  freshness: generation %d, accepted revision %d\n", v.State.Generation, v.State.Revision)
	if v.State.ScopeUID != "" {
		fmt.Fprintf(&b, "  scope %s, compose project %s\n", v.State.ScopeUID, v.State.ComposeProject)
	}
	if len(v.State.Objects) == 0 {
		b.WriteString("  no realized object\n")
	}
	for _, o := range v.State.Objects {
		fmt.Fprintf(&b, "  %s: %s\n", o.Ref, o.State)
		for _, m := range o.Mappings {
			fmt.Fprintf(&b, "      owned %s %s (%s)\n", m.Kind, m.Name, m.ID)
		}
	}
	if v.State.HasOpen {
		fmt.Fprintf(&b, "  open operation %s %s\n", v.State.Open.ID, v.State.Open.Status)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func (textRenderer) List(w io.Writer, r application.ListResult) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d\n", r.Collection, len(r.Items))
	for _, item := range r.Items {
		b.WriteString("  " + item.Ref)
		for _, a := range item.Attributes {
			fmt.Fprintf(&b, "  %s=%s", a.Key, a.Value)
		}
		b.WriteString("\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func (textRenderer) Removed(w io.Writer, r application.Removed) error {
	_, err := fmt.Fprintf(w, "removed %s from %s\n%s\n", r.Ref, r.File, RemoveNote)
	return err
}
