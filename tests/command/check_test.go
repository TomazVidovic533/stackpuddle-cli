package command_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/cli"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fixture"
)

type checkDoc struct {
	Results []struct {
		Checker     string `json:"checker"`
		Stage       string `json:"stage"`
		State       string `json:"state"`
		Environment string `json:"environment"`
		Findings    []struct {
			Code    string `json:"code"`
			File    string `json:"file"`
			Pointer string `json:"pointer"`
		} `json:"findings"`
	} `json:"results"`
	Outcome string `json:"outcome"`
}

func parseCheck(t *testing.T, out string) checkDoc {
	t.Helper()
	var doc checkDoc
	if err := json.NewDecoder(strings.NewReader(out)).Decode(&doc); err != nil {
		t.Fatalf("check JSON does not parse: %v\n%s", err, out)
	}
	return doc
}

func TestCheckExitCodes(t *testing.T) {
	t.Parallel()
	missingInput := fixture.ShopFiles(nil)
	missingInput["stackpuddle.yaml"] = strings.Replace(strings.Replace(missingInput["stackpuddle.yaml"],
		"      sensitivity: public\n      value: info\n", "      sensitivity: public\n", 1),
		"        api-log-level:\n          value: debug\n", "", 1)
	ownedKey := fixture.ShopFiles(nil)
	ownedKey["stackpuddle.yaml"] = strings.Replace(ownedKey["stackpuddle.yaml"], "              MODE: verbose\n",
		"              MODE: verbose\n          web:\n            environment:\n              API_HOST: override.example\n", 1)
	tests := []struct {
		name       string
		files      map[string]string
		wantExit   int
		wantOutput string
		wantCode   string
	}{
		{"valid project passes", fixture.ShopFiles(nil), cli.ExitOK, "pass", ""},
		{"missing input fails the configuration check", missingInput, cli.ExitValidation, "fail", "config.input_missing"},
		{"overriding a binding-owned key fails at load", ownedKey, cli.ExitValidation, "fail", "binding.delivery_conflict"},
		{"unresolved definition is incomplete", fixture.Unresolved(), cli.ExitIncomplete, "unknown", "config.definition_unresolved"},
		{"malformed local file fails", fixture.ShopFiles(map[string]string{".env.local": "API_TOKEN=\"" + fixture.Canary + "\n"}),
			cli.ExitValidation, "fail", "config.dotenv_invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := execute(t, fixture.Dir(t, tt.files), nil, "check", "--output", "json")
			if got.exit != tt.wantExit {
				t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", got.exit, tt.wantExit, got.stdout, got.stderr)
			}
			doc := parseCheck(t, got.stdout)
			if doc.Outcome != tt.wantOutput {
				t.Fatalf("outcome = %s, want %s", doc.Outcome, tt.wantOutput)
			}
			if tt.wantCode != "" && !strings.Contains(got.stdout, tt.wantCode) {
				t.Errorf("output lacks code %s:\n%s", tt.wantCode, got.stdout)
			}
			if strings.Contains(got.stdout+got.stderr, fixture.Canary) {
				t.Fatal("output echoes the canary")
			}
		})
	}
}

func TestCheckSkipsLaterStagesWhenStructureFails(t *testing.T) {
	t.Parallel()
	dir := fixture.Dir(t, map[string]string{"stackpuddle.yaml": "apiVersion: stackpuddle/v1\nkind: Project\nmetadata: {name: shop}\nspec:\n  surprise: 1\n"})
	got := execute(t, dir, nil, "check", "--output", "json")
	doc := parseCheck(t, got.stdout)
	if got.exit != cli.ExitValidation || len(doc.Results) != 2 {
		t.Fatalf("exit %d with %d results, want 3 with 2\n%s", got.exit, len(doc.Results), got.stdout)
	}
	if doc.Results[0].State != "fail" || doc.Results[1].State != "skipped" || doc.Results[1].Checker != "configuration" {
		t.Fatalf("results = %+v, want structure fail then configuration skipped", doc.Results)
	}
}

func TestCheckRunsEveryEnvironmentOrTheSelectedOne(t *testing.T) {
	t.Parallel()
	two := fixture.ShopFiles(nil)
	two["stackpuddle.yaml"] += "    ci:\n      runtime:\n        type: compose\n        context: ci-runner\n      secrets:\n        provider: env\n"
	dir := fixture.Dir(t, two)
	all := parseCheck(t, execute(t, dir, nil, "check", "--output", "json").stdout)
	one := parseCheck(t, execute(t, dir, nil, "check", "--env", "ci", "--output", "json").stdout)
	envs := func(d checkDoc) []string {
		var out []string
		for _, r := range d.Results {
			if r.Checker == "configuration" {
				out = append(out, r.Environment)
			}
		}
		return out
	}
	if got := strings.Join(envs(all), ","); got != "ci,local" {
		t.Errorf("all environments = %s, want ci,local", got)
	}
	if got := strings.Join(envs(one), ","); got != "ci" {
		t.Errorf("selected environment = %s, want ci", got)
	}
	if got := execute(t, dir, nil, "check", "--env", "nowhere"); got.exit != cli.ExitNotFound {
		t.Errorf("unknown environment exit = %d, want %d", got.exit, cli.ExitNotFound)
	}
}

func TestCheckLeavesTheStateFolderEmpty(t *testing.T) {
	t.Parallel()
	state := t.TempDir()
	got := execute(t, shop(t, nil), map[string]string{"STACKPUDDLE_STATE_DIR": state}, "check")
	if got.exit != cli.ExitOK {
		t.Fatalf("exit = %d, want 0\n%s%s", got.exit, got.stdout, got.stderr)
	}
	entries, err := os.ReadDir(state)
	if err != nil || len(entries) != 0 {
		t.Fatalf("state folder holds %d entries (err %v), want it untouched and empty", len(entries), err)
	}
}

func TestCheckGoldenJSON(t *testing.T) {
	t.Parallel()
	golden(t, "check-pass", execute(t, shop(t, nil), nil, "check", "--output", "json").stdout)
	missing := fixture.ShopFiles(nil)
	missing["stackpuddle.yaml"] = strings.Replace(strings.Replace(missing["stackpuddle.yaml"],
		"      sensitivity: public\n      value: info\n", "      sensitivity: public\n", 1),
		"        api-log-level:\n          value: debug\n", "", 1)
	golden(t, "check-fail", execute(t, fixture.Dir(t, missing), nil, "check", "--output", "json").stdout)
	golden(t, "check-incomplete", execute(t, fixture.Dir(t, fixture.Unresolved()), nil, "check", "--output", "json").stdout)
}

func TestCheckTextCarriesTheSameFactsAsJSON(t *testing.T) {
	t.Parallel()
	dir := fixture.Dir(t, fixture.Unresolved())
	text := execute(t, dir, nil, "check").stdout
	doc := parseCheck(t, execute(t, dir, nil, "check", "--output", "json").stdout)
	for _, r := range doc.Results {
		contains(t, "text", text, r.Checker)
		if r.Environment != "" {
			contains(t, "text", text, "["+r.Environment+"]")
		}
		contains(t, "text", text, r.State)
		for _, f := range r.Findings {
			contains(t, "text", text, f.Code)
		}
	}
	contains(t, "text", text, "outcome: "+doc.Outcome)
}
