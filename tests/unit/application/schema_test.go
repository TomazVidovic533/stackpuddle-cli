package application_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/cli/output"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fakeops"
	"github.com/TomazVidovic533/stackpuddle/schemas"
)

const operationSchemaID = "urn:stackpuddle:schema:v1:operation"

// operationSchema compiles the published Operation schema with every published schema registered by its
// $id, so the cross-file references resolve and nothing is fetched.
func operationSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	files, err := fs.Glob(schemas.Files(), "v1/*.schema.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("schemas: %v (%d files)", err, len(files))
	}
	for _, name := range files {
		data, err := fs.ReadFile(schemas.Files(), name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var header struct {
			ID string `json:"$id"`
		}
		if err := json.Unmarshal(data, &header); err != nil || header.ID == "" {
			t.Fatalf("%s has no $id: %v", name, err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if err := compiler.AddResource(header.ID, doc); err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
	}
	schema, err := compiler.Compile(operationSchemaID)
	if err != nil {
		t.Fatalf("compile the operation schema: %v", err)
	}
	return schema
}

func validate(t *testing.T, schema *jsonschema.Schema, doc []byte) error {
	t.Helper()
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		t.Fatalf("the output is not JSON: %v\n%s", err, doc)
	}
	return schema.Validate(value)
}

func renderOperation(t *testing.T, record application.OperationRecord) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := output.NewRenderer(output.FormatJSON).Operation(&buf, record); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.Bytes()
}

func TestEveryMappedOperationValidatesAgainstThePublishedSchema(t *testing.T) {
	t.Parallel()
	schema := operationSchema(t)
	var cases []application.OperationRecord
	for _, status := range application.Statuses() {
		rec := fakeops.Record(status)
		if status == application.StatusFailed {
			rec.Problem = &application.Problem{Code: "runtime.effect_failed", Category: "unexpected"}
		}
		cases = append(cases, rec)
	}
	for _, stepStatus := range application.StepStatuses() {
		rec := fakeops.Record(application.StatusRunning)
		rec.Steps[0].Status = stepStatus
		if stepStatus == application.StepFailed {
			rec.Steps[0].Problem = &application.Problem{Code: "runtime.image_missing", Category: "not_found"}
		}
		cases = append(cases, rec)
	}
	for _, command := range application.Commands() {
		rec := fakeops.Record(application.StatusSucceeded)
		rec.Command = command
		cases = append(cases, rec)
	}
	for _, actor := range application.ActorKinds() {
		rec := fakeops.Record(application.StatusSucceeded)
		rec.Actor = actor
		cases = append(cases, rec)
	}
	for _, category := range []string{"validation", "not_found", "conflict", "forbidden", "transient", "unexpected"} {
		rec := fakeops.Record(application.StatusBlocked)
		rec.Problem = &application.Problem{Code: "validation.project_invalid", Category: category}
		cases = append(cases, rec)
	}
	cases = append(cases, fakeops.RunRecord(1, 0), fakeops.RunRecord(2, 3))
	all := fakeops.Record(application.StatusSucceeded)
	all.All, all.Refs, all.ExpandedScope = true, nil, true
	continued := fakeops.Record(application.StatusInterrupted)
	continued.Continues, continued.ContinuedBy = fakeops.ID(2), fakeops.ID(3)
	cases = append(cases, all, continued)
	for i, rec := range cases {
		doc := renderOperation(t, rec)
		if err := validate(t, schema, doc); err != nil {
			t.Errorf("case %d (%s, %s) does not validate: %v\n%s", i, rec.Status, rec.Command, err, doc)
		}
	}
}

func TestADocumentWithAnUnknownEnumFailsTheSchema(t *testing.T) {
	t.Parallel()
	schema := operationSchema(t)
	rec := fakeops.Record(application.StatusRunning)
	rec.Status = "paused"
	if err := validate(t, schema, renderOperation(t, rec)); err == nil {
		t.Fatal("an unknown status validated; the schema test would not notice a new engine status")
	}
	rec = fakeops.Record(application.StatusRunning)
	rec.Steps[0].Status = "retrying"
	if err := validate(t, schema, renderOperation(t, rec)); err == nil {
		t.Fatal("an unknown step status validated")
	}
}

func TestOperationTimesAreUTCAndTextAndJSONAgree(t *testing.T) {
	t.Parallel()
	rec := fakeops.Record(application.StatusSucceeded)
	rec.CreatedAt = time.Date(2026, 10, 29, 12, 0, 0, 0, time.FixedZone("x", 2*3600))
	doc := string(renderOperation(t, rec))
	if !strings.Contains(doc, `"createdAt": "2026-10-29T10:00:00Z"`) {
		t.Fatalf("createdAt is not UTC:\n%s", doc)
	}
	var text bytes.Buffer
	if err := output.NewRenderer(output.FormatText).Operation(&text, rec); err != nil {
		t.Fatal(err)
	}
	for _, fact := range []string{rec.ID.String(), "succeeded", "up", "workload/api", "completed"} {
		if !strings.Contains(text.String(), fact) || !strings.Contains(doc, fact) {
			t.Errorf("%q must appear in both forms\ntext: %s\njson: %s", fact, text.String(), doc)
		}
	}
}
