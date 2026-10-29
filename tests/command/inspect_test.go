package command_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/cli"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fixture"
)

type inspectDoc struct {
	Ref         string `json:"ref"`
	Environment string `json:"environment"`
	Unknown     bool   `json:"unknown"`
	Entries     []struct {
		Name      string  `json:"name"`
		Status    string  `json:"status"`
		Sensitive bool    `json:"sensitive"`
		Value     *string `json:"value"`
		Redacted  bool    `json:"redacted"`
		Source    *struct {
			Layer    string `json:"layer"`
			Location struct {
				File    string `json:"file"`
				Pointer string `json:"pointer"`
			} `json:"location"`
			Process string `json:"process"`
		} `json:"source"`
		Shadowed []struct {
			Layer  string `json:"layer"`
			Reason string `json:"reason"`
		} `json:"shadowed"`
	} `json:"entries"`
}

func parseInspect(t *testing.T, out string) inspectDoc {
	t.Helper()
	var doc inspectDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("inspect JSON does not parse: %v\n%s", err, out)
	}
	return doc
}

func suppliedShop(t *testing.T) (string, map[string]string) {
	t.Helper()
	dir := shop(t, map[string]string{".env.local": "API_TOKEN=" + fixture.Canary + "\n"})
	return dir, map[string]string{"SHOP_API_TOKEN": fixture.Canary + "-process"}
}

func TestInspectTextAndJSONCarryTheSameFacts(t *testing.T) {
	t.Parallel()
	dir, vars := suppliedShop(t)
	for _, ref := range []string{"workload/api", "module/audit"} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()
			text := execute(t, dir, vars, "inspect", ref, "--view", "config", "--explain")
			js := execute(t, dir, vars, "inspect", ref, "--view", "config", "--explain", "--output", "json")
			if text.exit != cli.ExitOK || js.exit != cli.ExitOK {
				t.Fatalf("exits = %d/%d, want 0\n%s%s", text.exit, js.exit, text.stderr, js.stderr)
			}
			doc := parseInspect(t, js.stdout)
			contains(t, "text", text.stdout, doc.Ref+" in environment "+doc.Environment)
			if len(doc.Entries) == 0 {
				t.Fatal("no entries")
			}
			for _, e := range doc.Entries {
				contains(t, "text", text.stdout, e.Name)
				if e.Source == nil {
					t.Fatalf("%s has no source under --explain", e.Name)
				}
				contains(t, "text", text.stdout, "source: "+e.Source.Layer)
				if e.Source.Location.File == "" && e.Source.Layer != "provider" {
					t.Errorf("%s has no source file", e.Name)
				}
				switch {
				case e.Redacted:
					contains(t, "text", text.stdout, e.Name+" = [redacted]")
				case e.Value != nil:
					contains(t, "text", text.stdout, e.Name+` = "`+*e.Value+`"`)
				}
				for _, s := range e.Shadowed {
					contains(t, "text", text.stdout, "shadowed: "+s.Layer)
					contains(t, "text", text.stdout, "("+s.Reason+")")
				}
			}
		})
	}
}

func TestInspectWorkloadShowsPrecedenceAndRedaction(t *testing.T) {
	t.Parallel()
	dir, vars := suppliedShop(t)
	got := execute(t, dir, vars, "inspect", "workload/api", "--explain", "--output", "json")
	doc := parseInspect(t, got.stdout)
	byName := map[string]int{}
	for i, e := range doc.Entries {
		byName[e.Name] = i
	}
	token := doc.Entries[byName["API_TOKEN"]]
	if !token.Sensitive || !token.Redacted || token.Value != nil || token.Source.Layer != "process" {
		t.Fatalf("API_TOKEN = %+v, want a redacted value from the process layer", token)
	}
	if len(token.Shadowed) != 1 || token.Shadowed[0].Layer != "local" {
		t.Fatalf("API_TOKEN shadowed = %+v, want the local layer", token.Shadowed)
	}
	if mode := doc.Entries[byName["MODE"]]; mode.Value == nil || *mode.Value != "verbose" || mode.Source.Layer != "environment" {
		t.Fatalf("MODE = %+v, want verbose from the environment layer", mode)
	}
	if strings.Contains(got.stdout+got.stderr, fixture.Canary) {
		t.Fatal("output echoes a supplied secret")
	}
}

func TestInspectWithoutExplainOmitsSources(t *testing.T) {
	t.Parallel()
	got := execute(t, shop(t, nil), nil, "inspect", "workload/api", "--output", "json")
	for _, e := range parseInspect(t, got.stdout).Entries {
		if e.Source != nil || len(e.Shadowed) != 0 {
			t.Fatalf("%s carries a source without --explain", e.Name)
		}
	}
}

func TestInspectUnresolvedModuleIsUnknown(t *testing.T) {
	t.Parallel()
	got := execute(t, fixture.Dir(t, fixture.Unresolved()), nil, "inspect", "module/db", "--output", "json")
	doc := parseInspect(t, got.stdout)
	if got.exit != cli.ExitOK || !doc.Unknown || len(doc.Entries) != 0 {
		t.Fatalf("exit %d unknown %v entries %d, want an unknown view with no entries", got.exit, doc.Unknown, len(doc.Entries))
	}
}

func TestInspectGolden(t *testing.T) {
	t.Parallel()
	dir := shop(t, map[string]string{".env.local": "API_TOKEN=from-local\n"})
	golden(t, "inspect-workload-explain", execute(t, dir, nil, "inspect", "workload/api", "--explain", "--output", "json").stdout)
	golden(t, "inspect-module-explain", execute(t, dir, nil, "inspect", "module/audit", "--explain", "--output", "json").stdout)
}

func TestInspectEnvironmentSelection(t *testing.T) {
	t.Parallel()
	two := fixture.ShopFiles(nil)
	two["stackpuddle.yaml"] += "    ci:\n      runtime:\n        type: compose\n        context: ci-runner\n      secrets:\n        provider: env\n"
	dir := fixture.Dir(t, two)
	if got := execute(t, dir, nil, "inspect", "workload/api"); got.exit != cli.ExitUsage {
		t.Errorf("several environments without --env: exit %d, want %d", got.exit, cli.ExitUsage)
	}
	got := execute(t, dir, nil, "inspect", "workload/api", "--env", "ci", "--output", "json")
	if got.exit != cli.ExitOK || parseInspect(t, got.stdout).Environment != "ci" {
		t.Errorf("--env ci: exit %d\n%s%s", got.exit, got.stdout, got.stderr)
	}
}

func TestInspectMalformedLocalFileNeverEchoesIt(t *testing.T) {
	t.Parallel()
	dir := shop(t, map[string]string{".env.local": "API_TOKEN=\"" + fixture.Canary + "\n"})
	for _, format := range []string{"text", "json"} {
		got := execute(t, dir, nil, "inspect", "workload/api", "--output", format)
		if got.exit != cli.ExitValidation {
			t.Fatalf("%s: exit %d, want %d", format, got.exit, cli.ExitValidation)
		}
		if strings.Contains(got.stdout+got.stderr, fixture.Canary) {
			t.Fatalf("%s: output echoes the dotenv canary:\n%s%s", format, got.stdout, got.stderr)
		}
		contains(t, format+" stderr", got.stderr, "config.dotenv_invalid")
	}
}
