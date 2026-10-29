package output

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/config"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
	"github.com/TomazVidovic533/stackpuddle/model"
)

type jsonRenderer struct{}

type jsonFinding struct {
	Code    string `json:"code"`
	File    string `json:"file"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Pointer string `json:"pointer"`
	Message string `json:"message"`
	// Consumers names the objects that bind the object the finding is about.
	Consumers []string `json:"consumers,omitempty"`
}

type jsonResult struct {
	Checker     string        `json:"checker"`
	Stage       string        `json:"stage"`
	State       string        `json:"state"`
	Environment string        `json:"environment,omitempty"`
	ObservedAt  string        `json:"observedAt,omitempty"`
	Generation  int64         `json:"generation,omitempty"`
	Findings    []jsonFinding `json:"findings"`
}

type jsonReport struct {
	Results []jsonResult `json:"results"`
	Outcome string       `json:"outcome"`
}

type jsonLocation struct {
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Pointer string `json:"pointer,omitempty"`
}

type jsonLocal struct {
	File string `json:"file"`
	Key  string `json:"key"`
}

type jsonSource struct {
	Layer    string       `json:"layer"`
	Location jsonLocation `json:"location"`
	Local    *jsonLocal   `json:"local,omitempty"`
	Process  string       `json:"process,omitempty"`
	Provider string       `json:"provider,omitempty"`
}

type jsonShadow struct {
	jsonSource
	Reason string `json:"reason"`
}

type jsonEntry struct {
	Name      string       `json:"name"`
	Status    string       `json:"status"`
	Sensitive bool         `json:"sensitive,omitempty"`
	Value     *string      `json:"value,omitempty"`
	Redacted  bool         `json:"redacted,omitempty"`
	Source    *jsonSource  `json:"source,omitempty"`
	Shadowed  []jsonShadow `json:"shadowed,omitempty"`
}

type jsonView struct {
	Ref         string      `json:"ref"`
	Environment string      `json:"environment"`
	Unknown     bool        `json:"unknown,omitempty"`
	Entries     []jsonEntry `json:"entries"`
}

type jsonFailure struct {
	Error struct {
		Class     string        `json:"class"`
		Code      string        `json:"code"`
		Message   string        `json:"message"`
		Findings  []jsonFinding `json:"findings,omitempty"`
		Operation *operationDoc `json:"operation,omitempty"`
	} `json:"error"`
}

func (jsonRenderer) Report(w io.Writer, r diagnostics.Report) error {
	doc := jsonReport{Results: make([]jsonResult, 0, len(r.Results)), Outcome: r.Outcome().String()}
	for _, result := range r.Results {
		out := jsonResult{
			Checker: result.Checker, Stage: result.Stage.String(), State: result.State.String(),
			Findings: findingsJSON(result.Findings), ObservedAt: utcText(result.ObservedAt), Generation: result.Generation,
		}
		if env, ok := result.Environment(); ok {
			out.Environment = env.String()
		}
		doc.Results = append(doc.Results, out)
	}
	return encode(w, doc)
}

func (jsonRenderer) View(w io.Writer, v config.View, opts ViewOptions) error {
	doc := jsonView{Ref: v.Ref.String(), Environment: v.Environment.String(), Unknown: v.Unknown, Entries: []jsonEntry{}}
	for _, e := range v.Entries {
		doc.Entries = append(doc.Entries, entryJSON(e, opts))
	}
	return encode(w, doc)
}

func (jsonRenderer) Version(w io.Writer, v model.VersionInfo) error {
	data, err := model.EncodeVersionInfo(v)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func (jsonRenderer) Failure(w io.Writer, f Failure) error {
	var doc jsonFailure
	doc.Error.Class, doc.Error.Code, doc.Error.Message = f.Class, f.Code, f.Message
	if len(f.Findings) > 0 {
		doc.Error.Findings = findingsJSON(f.Findings)
	}
	if f.Operation != nil {
		op := operationJSON(*f.Operation)
		doc.Error.Operation = &op
	}
	return encode(w, doc)
}

func entryJSON(e config.Entry, opts ViewOptions) jsonEntry {
	v := e.Value
	out := jsonEntry{Name: e.Name, Status: v.Status().String(), Sensitive: v.Sensitive()}
	if v.Status() == config.StatusSupplied {
		if text, ok := v.Text(); ok {
			out.Value = &text
		} else {
			out.Redacted = true
		}
	}
	if !opts.Explain {
		return out
	}
	if v.Status() != config.StatusMissing {
		src := sourceJSON(v.Source)
		out.Source = &src
	}
	for _, s := range v.Shadowed {
		out.Shadowed = append(out.Shadowed, jsonShadow{jsonSource: sourceJSON(s.Source), Reason: s.Reason.String()})
	}
	return out
}

func sourceJSON(s config.Source) jsonSource {
	out := jsonSource{Layer: s.Layer().String(), Location: locationJSON(s.Location)}
	out.Process, _ = s.Process()
	out.Provider, _ = s.Provider()
	if file, key, ok := s.Local(); ok {
		out.Local = &jsonLocal{File: file, Key: key}
	}
	return out
}

func locationJSON(l model.Location) jsonLocation {
	out := jsonLocation{File: l.File(), Pointer: l.Pointer()}
	if line, col, ok := l.Position(); ok {
		out.Line, out.Column = line, col
	}
	return out
}

func findingsJSON(fs []diagnostics.Finding) []jsonFinding {
	out := make([]jsonFinding, 0, len(fs))
	for _, f := range fs {
		loc := locationJSON(f.Location)
		out = append(out, jsonFinding{
			Code: f.Code, File: loc.File, Line: loc.Line, Column: loc.Column, Pointer: loc.Pointer, Message: f.Message,
			Consumers: f.Consumers,
		})
	}
	return out
}

// encode writes doc as two-space indented JSON with a trailing newline.
func encode(w io.Writer, doc any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

type jsonSelection struct {
	All           bool     `json:"all"`
	Groups        []string `json:"groups"`
	Selected      []string `json:"selected"`
	Prerequisites []string `json:"prerequisites"`
}

type jsonAction struct {
	Action  string `json:"action,omitempty"`
	Object  string `json:"object,omitempty"`
	Effect  string `json:"effect"`
	Unknown bool   `json:"unknown,omitempty"`
	Problem string `json:"problem,omitempty"`
}

type jsonPreview struct {
	BasedOnGeneration int64        `json:"basedOnGeneration"`
	Actions           []jsonAction `json:"actions"`
}

type jsonMutation struct {
	Environment string        `json:"environment"`
	Selection   jsonSelection `json:"selection"`
	DryRun      bool          `json:"dryRun"`
	Operation   *operationDoc `json:"operation,omitempty"`
	Preview     *jsonPreview  `json:"preview,omitempty"`
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (jsonRenderer) Mutation(w io.Writer, r application.MutateResult) error {
	doc := jsonMutation{
		Environment: r.Selection.Environment.String(), DryRun: r.DryRun,
		Selection: jsonSelection{
			All: r.Selection.All, Groups: nonNil(r.Selection.Groups), Selected: nonNil(r.Selection.Selected),
			Prerequisites: nonNil(r.Selection.Prerequisites),
		},
	}
	if r.HasRecord {
		op := operationJSON(r.Record)
		doc.Operation = &op
	}
	if r.DryRun {
		preview := jsonPreview{BasedOnGeneration: r.Preview.BasedOnGeneration, Actions: []jsonAction{}}
		for _, a := range r.Preview.Actions {
			preview.Actions = append(preview.Actions, jsonAction{
				Action: a.Action, Object: a.Object, Effect: string(a.Effect), Unknown: a.Unknown, Problem: a.Problem,
			})
		}
		doc.Preview = &preview
	}
	return encode(w, doc)
}

func (jsonRenderer) Invocation(w io.Writer, r application.RunJobResult) error {
	doc := struct {
		Environment string        `json:"environment"`
		Job         string        `json:"job"`
		DryRun      bool          `json:"dryRun"`
		Invocation  string        `json:"invocation,omitempty"`
		ExitCode    *int          `json:"exitCode,omitempty"`
		Operation   *operationDoc `json:"operation,omitempty"`
		Preview     *jsonPreview  `json:"preview,omitempty"`
	}{Environment: r.Environment.String(), Job: r.Job, DryRun: r.DryRun, Invocation: r.Invocation}
	if r.Result != nil {
		doc.ExitCode = &r.Result.ExitCode
	}
	if r.HasRecord {
		op := operationJSON(r.Record)
		doc.Operation = &op
	}
	if r.DryRun {
		preview := jsonPreview{BasedOnGeneration: r.Preview.BasedOnGeneration, Actions: []jsonAction{}}
		for _, a := range r.Preview.Actions {
			preview.Actions = append(preview.Actions, jsonAction{
				Action: a.Action, Object: a.Object, Effect: string(a.Effect), Unknown: a.Unknown, Problem: a.Problem,
			})
		}
		doc.Preview = &preview
	}
	return encode(w, doc)
}

func (jsonRenderer) Operation(w io.Writer, r application.OperationRecord) error {
	return encode(w, operationJSON(r))
}

func (jsonRenderer) CancelRequested(w io.Writer, id application.OperationID) error {
	return encode(w, struct {
		Operation       string `json:"operation"`
		CancelRequested bool   `json:"cancelRequested"`
		Note            string `json:"note"`
	}{Operation: id.String(), CancelRequested: true, Note: CancelNote})
}

type jsonMapping struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	ID   string `json:"id"`
}

type jsonObject struct {
	Ref      string        `json:"ref"`
	State    string        `json:"state"`
	Mappings []jsonMapping `json:"mappings"`
}

type jsonState struct {
	Ref            string        `json:"ref"`
	Environment    string        `json:"environment"`
	Generation     int64         `json:"generation"`
	Revision       int           `json:"revision"`
	ScopeUID       string        `json:"scopeUid,omitempty"`
	ComposeProject string        `json:"composeProject,omitempty"`
	Objects        []jsonObject  `json:"objects"`
	OpenOperation  *operationDoc `json:"openOperation,omitempty"`
}

func (jsonRenderer) State(w io.Writer, v application.StateView) error {
	doc := jsonState{
		Ref: v.Ref.String(), Environment: v.Environment.String(), Generation: v.State.Generation, Revision: v.State.Revision,
		ScopeUID: v.State.ScopeUID, ComposeProject: v.State.ComposeProject, Objects: []jsonObject{},
	}
	for _, o := range v.State.Objects {
		object := jsonObject{Ref: o.Ref, State: o.State, Mappings: []jsonMapping{}}
		for _, m := range o.Mappings {
			object.Mappings = append(object.Mappings, jsonMapping{Kind: m.Kind, Name: m.Name, ID: m.ID})
		}
		doc.Objects = append(doc.Objects, object)
	}
	if v.State.HasOpen {
		op := operationJSON(v.State.Open)
		doc.OpenOperation = &op
	}
	return encode(w, doc)
}

type jsonListItem struct {
	Ref        string            `json:"ref"`
	Attributes map[string]string `json:"attributes"`
}

func (jsonRenderer) List(w io.Writer, r application.ListResult) error {
	doc := struct {
		Collection  string         `json:"collection"`
		Environment string         `json:"environment,omitempty"`
		Items       []jsonListItem `json:"items"`
	}{Collection: string(r.Collection), Items: []jsonListItem{}}
	if r.HasEnv {
		doc.Environment = r.Environment.String()
	}
	for _, item := range r.Items {
		attrs := map[string]string{}
		for _, a := range item.Attributes {
			attrs[a.Key] = a.Value
		}
		doc.Items = append(doc.Items, jsonListItem{Ref: item.Ref, Attributes: attrs})
	}
	return encode(w, doc)
}

func (jsonRenderer) Removed(w io.Writer, r application.Removed) error {
	return encode(w, struct {
		Ref      string `json:"ref"`
		File     string `json:"file"`
		Retained string `json:"note"`
	}{Ref: r.Ref.String(), File: r.File, Retained: RemoveNote})
}
