package config_test

import (
	"strings"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/config"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fixture"
	"github.com/TomazVidovic533/stackpuddle/model"
)

func loadShop(t *testing.T, mutate func(string) string) model.Project {
	t.Helper()
	files := fixture.ShopFiles(nil)
	if mutate != nil {
		files["stackpuddle.yaml"] = mutate(files["stackpuddle.yaml"])
	}
	p, diags := model.Load(fixture.MapFS(files), "stackpuddle.yaml", nil)
	if len(diags) > 0 {
		t.Fatalf("fixture does not load: %v", diags)
	}
	return p
}

func envName(t *testing.T, s string) model.EnvironmentName {
	t.Helper()
	n, err := model.ParseEnvironmentName(s)
	if err != nil {
		t.Fatalf("environment name %q: %v", s, err)
	}
	return n
}

func dotenv(t *testing.T, content string) config.DotenvFile {
	t.Helper()
	f, findings := config.ParseDotenv(".env.local", []byte(content))
	if len(findings) > 0 {
		t.Fatalf("dotenv fixture is invalid: %v", findings)
	}
	return f
}

func inputKey(t *testing.T, s string) model.InputKey {
	t.Helper()
	k, err := model.ParseInputKey(s)
	if err != nil {
		t.Fatalf("input key %q: %v", s, err)
	}
	return k
}

func TestResolveInputPrecedence(t *testing.T) {
	t.Parallel()
	// api-log-level is public with project value "info" and environment value "debug".
	// api-token is secret and takes its value only from the local file or the mapped process variable.
	tests := []struct {
		name         string
		local        string // content of .env.local; "-" means the file is absent
		process      map[string]string
		policy       config.LocalPolicy
		key          string
		wantStatus   config.Status
		wantLayer    config.Layer
		wantText     string
		wantShadowed []config.Layer
	}{
		{name: "environment value beats project value", local: "-", key: "api-log-level", policy: config.LocalApplies,
			wantStatus: config.StatusSupplied, wantLayer: config.LayerEnvironment, wantText: "debug",
			wantShadowed: []config.Layer{config.LayerProject}},
		{name: "secret with no source is deferred to the provider", local: "-", key: "api-token", policy: config.LocalApplies,
			wantStatus: config.StatusDeferred, wantLayer: config.LayerProvider},
		{name: "local value supplies the secret", local: "API_TOKEN=from-local\n", key: "api-token", policy: config.LocalApplies,
			wantStatus: config.StatusSupplied, wantLayer: config.LayerLocal, wantText: "from-local"},
		{name: "process beats local", local: "API_TOKEN=from-local\n", process: map[string]string{"SHOP_API_TOKEN": "from-process"},
			key: "api-token", policy: config.LocalApplies,
			wantStatus: config.StatusSupplied, wantLayer: config.LayerProcess, wantText: "from-process",
			wantShadowed: []config.Layer{config.LayerLocal}},
		{name: "empty process value is supplied, not missing", local: "API_TOKEN=from-local\n",
			process: map[string]string{"SHOP_API_TOKEN": ""}, key: "api-token", policy: config.LocalApplies,
			wantStatus: config.StatusSupplied, wantLayer: config.LayerProcess, wantText: "",
			wantShadowed: []config.Layer{config.LayerLocal}},
		{name: "empty dotenv value is supplied, not missing", local: "API_TOKEN=\n", key: "api-token", policy: config.LocalApplies,
			wantStatus: config.StatusSupplied, wantLayer: config.LayerLocal, wantText: ""},
		{name: "absent dotenv key falls through", local: "OTHER=x\n", key: "api-token", policy: config.LocalApplies,
			wantStatus: config.StatusDeferred, wantLayer: config.LayerProvider},
		{name: "CI excludes the local value and shows it shadowed", local: "API_TOKEN=from-local\n", key: "api-token",
			policy: config.LocalExcludedInCI, wantStatus: config.StatusDeferred, wantLayer: config.LayerProvider,
			wantShadowed: []config.Layer{config.LayerLocal}},
		{name: "CI keeps the mapped process value", local: "API_TOKEN=from-local\n",
			process: map[string]string{"SHOP_API_TOKEN": "from-process"}, key: "api-token", policy: config.LocalExcludedInCI,
			wantStatus: config.StatusSupplied, wantLayer: config.LayerProcess, wantText: "from-process",
			wantShadowed: []config.Layer{config.LayerLocal}},
	}
	p := loadShop(t, nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := config.Inputs{
				LocalFiles:    map[string]config.DotenvFile{},
				LookupProcess: fixture.Lookup(tt.process),
				Local:         tt.policy,
				RootFile:      "stackpuddle.yaml",
			}
			if tt.local != "-" {
				in.LocalFiles[".env.local"] = dotenv(t, tt.local)
			}
			res, findings := config.Resolve(p, envName(t, "local"), in)
			if len(findings) > 0 {
				t.Fatalf("unexpected findings: %v", findings)
			}
			v := res.Inputs[inputKey(t, tt.key)]
			if v.Status() != tt.wantStatus || v.Source.Layer() != tt.wantLayer {
				t.Fatalf("status/layer = %s/%s, want %s/%s", v.Status(), v.Source.Layer(), tt.wantStatus, tt.wantLayer)
			}
			if v.Status() == config.StatusSupplied {
				got, _ := v.Text()
				if sec, ok := v.Secret(); ok {
					got = sec.Reveal()
				}
				if got != tt.wantText {
					t.Fatalf("value = %q, want %q", got, tt.wantText)
				}
			}
			var shadowed []config.Layer
			for _, s := range v.Shadowed {
				shadowed = append(shadowed, s.Source.Layer())
			}
			if !equalLayers(shadowed, tt.wantShadowed) {
				t.Fatalf("shadowed = %v, want %v", shadowed, tt.wantShadowed)
			}
		})
	}
}

func equalLayers(a, b []config.Layer) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestResolveShadowReasons(t *testing.T) {
	t.Parallel()
	p := loadShop(t, nil)
	in := config.Inputs{
		LocalFiles: map[string]config.DotenvFile{".env.local": dotenv(t, "API_TOKEN=x\n")},
		Local:      config.LocalExcludedInCI, RootFile: "stackpuddle.yaml",
	}
	res, _ := config.Resolve(p, envName(t, "local"), in)
	shadowed := res.Inputs[inputKey(t, "api-token")].Shadowed
	if len(shadowed) != 1 || shadowed[0].Reason != config.ShadowCI {
		t.Fatalf("shadowed = %+v, want one source with reason ci", shadowed)
	}
}

func TestResolveMissingPublicInput(t *testing.T) {
	t.Parallel()
	p := loadShop(t, func(s string) string {
		s = strings.Replace(s, "      sensitivity: public\n      value: info\n", "      sensitivity: public\n", 1)
		return strings.Replace(s, "        api-log-level:\n          value: debug\n", "", 1)
	})
	res, findings := config.Resolve(p, envName(t, "local"), config.Inputs{Local: config.LocalApplies, RootFile: "stackpuddle.yaml"})
	if len(findings) != 1 || findings[0].Code != config.CodeInputMissing {
		t.Fatalf("findings = %+v, want one %s", findings, config.CodeInputMissing)
	}
	if got := res.Inputs[inputKey(t, "api-log-level")].Status(); got != config.StatusMissing {
		t.Fatalf("status = %s, want missing", got)
	}
}

func TestResolveWorkloadViewProvenance(t *testing.T) {
	t.Parallel()
	p := loadShop(t, nil)
	in := config.Inputs{
		LocalFiles: map[string]config.DotenvFile{".env.local": dotenv(t, "API_TOKEN=from-local\n")},
		Local:      config.LocalApplies, RootFile: "stackpuddle.yaml",
	}
	res, findings := config.Resolve(p, envName(t, "local"), in)
	if len(findings) > 0 {
		t.Fatalf("findings: %v", findings)
	}
	key, _ := model.ParseObjectKey("api")
	view := res.Workloads[key]
	want := map[string]struct {
		layer config.Layer
		text  string
	}{
		"LOG_LEVEL": {config.LayerEnvironment, "debug"},
		"API_TOKEN": {config.LayerLocal, ""},
		"MODE":      {config.LayerEnvironment, "verbose"},
	}
	if len(view.Entries) != len(want) {
		t.Fatalf("entries = %d, want %d", len(view.Entries), len(want))
	}
	for _, e := range view.Entries {
		w, ok := want[e.Name]
		if !ok {
			t.Fatalf("unexpected entry %s", e.Name)
		}
		text, _ := e.Value.Text()
		if e.Value.Source.Layer() != w.layer || text != w.text {
			t.Errorf("%s = %q from %s, want %q from %s", e.Name, text, e.Value.Source.Layer(), w.text, w.layer)
		}
		if e.Value.Source.Location.File() == "" || e.Value.Source.Location.Pointer() == "" {
			t.Errorf("%s has no provenance location: %+v", e.Name, e.Value.Source.Location)
		}
	}
}

func TestResolveModuleOptionLayers(t *testing.T) {
	t.Parallel()
	p := loadShop(t, nil)
	res, _ := config.Resolve(p, envName(t, "local"), config.Inputs{Local: config.LocalApplies, RootFile: "stackpuddle.yaml"})
	key, _ := model.ParseObjectKey("audit")
	view := res.Modules[key]
	if view.Unknown {
		t.Fatal("module view is unknown, want resolved")
	}
	got := map[string]config.Layer{}
	for _, e := range view.Entries {
		got[e.Name] = e.Value.Source.Layer()
	}
	want := map[string]config.Layer{"/retentionDays": config.LayerProject, "/level": config.LayerEnvironment}
	for name, layer := range want {
		if got[name] != layer {
			t.Errorf("%s from %s, want %s", name, got[name], layer)
		}
	}
}

// TestBindingOwnedKeyIsRefusedByLoad pins the division of labour: the model owns the binding-owned key
// rule at load time, so Resolve never sees a project that breaks it.
func TestBindingOwnedKeyIsRefusedByLoad(t *testing.T) {
	t.Parallel()
	files := fixture.ShopFiles(nil)
	files["stackpuddle.yaml"] = strings.Replace(files["stackpuddle.yaml"], "              MODE: verbose\n",
		"              MODE: verbose\n          web:\n            environment:\n              API_HOST: override.example\n", 1)
	_, diags := model.Load(fixture.MapFS(files), "stackpuddle.yaml", nil)
	if len(diags) != 1 || diags[0].Code() != model.BindingDeliveryConflict {
		t.Fatalf("diagnostics = %v, want one %s", diags, model.BindingDeliveryConflict)
	}
}

func TestResolveUnknownEnvironment(t *testing.T) {
	t.Parallel()
	p := loadShop(t, nil)
	_, findings := config.Resolve(p, envName(t, "nowhere"), config.Inputs{Local: config.LocalApplies})
	if len(findings) != 1 || findings[0].Code != config.CodeUnknownEnvironment {
		t.Fatalf("findings = %+v, want one %s", findings, config.CodeUnknownEnvironment)
	}
}
