package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/application"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fixture"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// postgresProject declares one postgres instance whose user is a member of a role nobody declared.
const postgresProject = `apiVersion: stackpuddle/v1
kind: Project
metadata:
  name: catalog
spec:
  manifest: stackpuddle.manifest.yaml
  lock: stackpuddle.lock.yaml
  modules:
    db:
      definition: postgres
      options:
        users:
          app: {purpose: runtime, passwordSecretRef: app-password, memberOf: [ghost]}
  environments:
    local:
      runtime:
        type: compose
        context: desktop-linux
      secrets:
        provider: local
`

type fakeResolver struct{ err error }

func (f fakeResolver) Resolve(context.Context, model.Lock) ([]model.ModuleDefinition, error) {
	if f.err != nil {
		return nil, f.err
	}
	files := fixture.ModuleFiles("postgres", "")
	files["content/options.schema.json"] = `{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"urn:stackpuddle:module:postgres:configuration","type":"object"}` + "\n"
	files["content/defaults.yaml"] = "{}\n"
	def, diags := model.LoadModuleDefinition(fixture.MapFS(files))
	if len(diags) > 0 {
		return nil, errors.New("test definition invalid")
	}
	return []model.ModuleDefinition{def}, nil
}

func semanticCheck(t *testing.T, mods application.ModuleResolver) diagnostics.Result {
	t.Helper()
	locked := map[string]fixture.LockedModule{"postgres": {Version: "0.1.0", Source: "https://127.0.0.1:1/postgres.tar.gz", Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}
	files := map[string]string{
		"stackpuddle.yaml":          postgresProject,
		"stackpuddle.manifest.yaml": "apiVersion: stackpuddle/v1\nkind: Manifest\nbundleVersion: 0.1.0-alpha.1\nmodules:\n  - postgres\n",
		"stackpuddle.lock.yaml":     fixture.LockYAML("https://127.0.0.1:1/bundle.tar.gz", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", locked),
	}
	check := application.NewResolvingCheckProject(source(fixture.Dir(t, files)), mods, []application.Checker{
		application.StructureChecker(), application.SemanticChecker(),
	})
	report, err := check(context.Background(), application.CheckRequest{})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, r := range report.Results {
		if r.Checker == application.SemanticCheckerName {
			return r
		}
	}
	t.Fatalf("no semantic result in %+v", report)
	return diagnostics.Result{}
}

func TestCheckReportsAPostgresSemanticErrorWithItsStableCode(t *testing.T) {
	t.Parallel()
	result := semanticCheck(t, fakeResolver{})
	if result.State != diagnostics.StateFail || len(result.Findings) == 0 || result.Findings[0].Code != "postgres.unknown_member_role" {
		t.Fatalf("result = %+v, want fail with postgres.unknown_member_role", result)
	}
}

func TestAModuleThatCannotBeResolvedIsUnknownNeverPass(t *testing.T) {
	t.Parallel()
	result := semanticCheck(t, fakeResolver{err: errors.New("offline")})
	if result.State != diagnostics.StateUnknown {
		t.Fatalf("result = %+v, want unknown", result)
	}
}
