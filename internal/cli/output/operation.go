package output

import (
	"time"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// jsonProblem, jsonStep and operationDoc mirror stackpuddle's published Operation schema
// (schemas/v1/operation.schema.json); a test validates every status, step status, command, actor and
// category against it. The engine's Operation carries no JSON tags on purpose: this mapping is the one place
// the document is written.
type jsonProblem struct {
	Code     string `json:"code"`
	Category string `json:"category"`
}

type jsonStep struct {
	ID         string       `json:"id"`
	Action     string       `json:"action"`
	Object     string       `json:"object"`
	Status     string       `json:"status"`
	StartedAt  string       `json:"startedAt,omitempty"`
	FinishedAt string       `json:"finishedAt,omitempty"`
	Error      *jsonProblem `json:"error,omitempty"`
	Job        *jsonJob     `json:"job,omitempty"`
}

type jsonJob struct {
	ExitCode int `json:"exitCode"`
}

type jsonOperationScope struct {
	Project     string `json:"project"`
	Environment string `json:"environment"`
	ScopeUID    string `json:"scopeUid"`
}

type jsonOperationSelection struct {
	All           *bool    `json:"all,omitempty"`
	Refs          []string `json:"refs,omitempty"`
	ExpandedScope bool     `json:"expandedScope,omitempty"`
}

type jsonActor struct {
	Kind string `json:"kind"`
}

type operationDoc struct {
	APIVersion  string                 `json:"apiVersion"`
	Kind        string                 `json:"kind"`
	ID          string                 `json:"id"`
	Continues   string                 `json:"continues,omitempty"`
	ContinuedBy string                 `json:"continuedBy,omitempty"`
	Scope       jsonOperationScope     `json:"scope"`
	Command     string                 `json:"command"`
	Selection   jsonOperationSelection `json:"selection"`
	Revision    int                    `json:"revision"`
	Status      string                 `json:"status"`
	Steps       []jsonStep             `json:"steps"`
	Error       *jsonProblem           `json:"error,omitempty"`
	Actor       jsonActor              `json:"actor"`
	CreatedAt   string                 `json:"createdAt"`
	UpdatedAt   string                 `json:"updatedAt"`
	FinishedAt  string                 `json:"finishedAt,omitempty"`
}

// utcText prints t as an RFC 3339 UTC time ending in Z, or "" for the zero time.
func utcText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05.999999999Z")
}

func problemJSON(p *application.Problem) *jsonProblem {
	if p == nil {
		return nil
	}
	return &jsonProblem{Code: p.Code, Category: p.Category}
}

// operationJSON maps a record to the published Operation document.
func operationJSON(r application.OperationRecord) operationDoc {
	doc := operationDoc{
		APIVersion: model.APIVersion, Kind: "Operation", ID: r.ID.String(),
		Scope:   jsonOperationScope{Project: r.Scope.Project.String(), Environment: r.Scope.Environment.String(), ScopeUID: r.ScopeUID},
		Command: string(r.Command), Revision: r.Revision, Status: string(r.Status), Steps: []jsonStep{},
		Error: problemJSON(r.Problem), Actor: jsonActor{Kind: string(r.Actor)},
		CreatedAt: utcText(r.CreatedAt), UpdatedAt: utcText(r.UpdatedAt), FinishedAt: utcText(r.FinishedAt),
	}
	if !r.Continues.IsZero() {
		doc.Continues = r.Continues.String()
	}
	if !r.ContinuedBy.IsZero() {
		doc.ContinuedBy = r.ContinuedBy.String()
	}
	if r.All {
		all := true
		doc.Selection = jsonOperationSelection{All: &all, ExpandedScope: r.ExpandedScope}
	} else {
		doc.Selection = jsonOperationSelection{Refs: r.Refs, ExpandedScope: r.ExpandedScope}
	}
	for _, s := range r.Steps {
		step := jsonStep{
			ID: s.ID, Action: s.Action, Object: s.Object, Status: string(s.Status),
			StartedAt: utcText(s.StartedAt), FinishedAt: utcText(s.FinishedAt), Error: problemJSON(s.Problem),
		}
		if s.Job != nil {
			step.Job = &jsonJob{ExitCode: s.Job.ExitCode}
		}
		doc.Steps = append(doc.Steps, step)
	}
	return doc
}
