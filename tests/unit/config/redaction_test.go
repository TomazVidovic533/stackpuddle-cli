package config_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/config"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/config/secret"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fixture"
)

func TestSecretValueRedactsOnEveryPrintPath(t *testing.T) {
	t.Parallel()
	v := secret.New(fixture.Canary)
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("supplied", "value", v)
	asJSON, err := json.Marshal(struct{ V secret.Value }{v})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text, err := v.MarshalText()
	if err != nil {
		t.Fatalf("marshal text: %v", err)
	}
	outputs := map[string]string{
		"String": v.String(), "%s": fmt.Sprintf("%s", v), "%v": fmt.Sprintf("%v", v), "%+v": fmt.Sprintf("%+v", v),
		"%#v": fmt.Sprintf("%#v", v), "%q": fmt.Sprintf("%q", v), "%x": fmt.Sprintf("%x", v), "%d": fmt.Sprintf("%d", v),
		"pointer %v": fmt.Sprintf("%v", &v), "struct %+v": fmt.Sprintf("%+v", struct{ V secret.Value }{v}),
		"json": string(asJSON), "text": string(text), "slog": logs.String(), "Sprint": fmt.Sprint(v),
	}
	for name, got := range outputs {
		if strings.Contains(got, fixture.Canary) {
			t.Errorf("%s prints the canary: %s", name, got)
		}
		if !strings.Contains(got, secret.Redacted) {
			t.Errorf("%s = %q, want it to show %s", name, got, secret.Redacted)
		}
	}
	if v.Reveal() != fixture.Canary {
		t.Fatal("Reveal lost the value")
	}
}

func TestResolvedSecretNeverPrintsInAnyForm(t *testing.T) {
	t.Parallel()
	p := loadShop(t, nil)
	in := config.Inputs{
		LocalFiles: map[string]config.DotenvFile{".env.local": dotenv(t, "API_TOKEN="+fixture.Canary+"\n")},
		Local:      config.LocalApplies, RootFile: "stackpuddle.yaml",
	}
	res, findings := config.Resolve(p, envName(t, "local"), in)
	asJSON, err := json.Marshal(res.Inputs[inputKey(t, "api-token")])
	if err != nil {
		t.Fatalf("marshal resolution: %v", err)
	}
	dumps := []string{fmt.Sprintf("%v", res), fmt.Sprintf("%+v", res), fmt.Sprintf("%#v", res), string(asJSON), fmt.Sprint(findings)}
	for _, d := range dumps {
		if strings.Contains(d, fixture.Canary) {
			t.Fatalf("resolution prints the canary: %.200s", d)
		}
	}
	token := res.Inputs[inputKey(t, "api-token")]
	if text, plain := token.Text(); !token.Sensitive() || plain || text != "" {
		t.Fatalf("secret input holds its value as text: sensitive=%v text=%q", token.Sensitive(), text)
	}
}
