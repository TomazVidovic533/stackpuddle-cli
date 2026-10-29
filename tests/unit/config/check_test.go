package config_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/config"
	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
	"github.com/TomazVidovic533/stackpuddle-cli/tests/internal/fixture"
	"github.com/TomazVidovic533/stackpuddle/model"
)

func TestPolicyFor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		vars map[string]string
		want config.LocalPolicy
	}{
		{"unset", nil, config.LocalApplies},
		{"empty", map[string]string{"CI": ""}, config.LocalApplies},
		{"zero", map[string]string{"CI": "0"}, config.LocalApplies},
		{"false", map[string]string{"CI": "False"}, config.LocalApplies},
		{"true", map[string]string{"CI": "true"}, config.LocalExcludedInCI},
		{"one", map[string]string{"CI": "1"}, config.LocalExcludedInCI},
		{"any other text", map[string]string{"CI": "github"}, config.LocalExcludedInCI},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := config.PolicyFor(fixture.Lookup(tt.vars)); got != tt.want {
				t.Fatalf("policy = %d, want %d", got, tt.want)
			}
		})
	}
	if config.PolicyFor(nil) != config.LocalApplies {
		t.Fatal("a nil lookup must apply local values")
	}
}

func TestCheckStates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		files map[string]string
		vars  map[string]string
		want  diagnostics.State
		code  string
	}{
		{name: "pass", files: fixture.ShopFiles(nil), want: diagnostics.StatePass},
		{name: "malformed local file fails", files: fixture.ShopFiles(map[string]string{".env.local": "API_TOKEN=\"" + fixture.Canary + "\n"}),
			want: diagnostics.StateFail, code: config.CodeDotenvInvalid},
		{name: "oversized local file fails", files: fixture.ShopFiles(map[string]string{".env.local": strings.Repeat("A", 1<<20+1)}),
			want: diagnostics.StateFail, code: config.CodeLocalFileTooLarge},
		{name: "CI skips the local file entirely", files: fixture.ShopFiles(map[string]string{".env.local": "bad line"}),
			vars: map[string]string{"CI": "true"}, want: diagnostics.StatePass},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fsys := fixture.MapFS(tt.files)
			p, diags := model.Load(fsys, "stackpuddle.yaml", nil)
			if len(diags) > 0 {
				t.Fatalf("fixture does not load: %v", diags)
			}
			r, err := config.Check(context.Background(), fsys, "stackpuddle.yaml", p, envName(t, "local"), fixture.Lookup(tt.vars))
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if r.State != tt.want || r.Checker != config.CheckerName || r.Stage != diagnostics.StageConfiguration {
				t.Fatalf("result = %s/%s/%s, want %s", r.Checker, r.Stage, r.State, tt.want)
			}
			if env, ok := r.Environment(); !ok || env.String() != "local" {
				t.Fatalf("environment = %v (%v), want local", env, ok)
			}
			if tt.code != "" && (len(r.Findings) == 0 || r.Findings[0].Code != tt.code) {
				t.Fatalf("findings = %+v, want first code %s", r.Findings, tt.code)
			}
			for _, f := range r.Findings {
				if strings.Contains(f.Message+f.Location.File()+f.Location.Pointer(), fixture.Canary) {
					t.Fatalf("finding echoes the canary: %+v", f)
				}
			}
		})
	}
}

func TestCheckUnknownWhenDefinitionIsNotSupplied(t *testing.T) {
	t.Parallel()
	fsys := fixture.MapFS(fixture.Unresolved())
	p, diags := model.Load(fsys, "stackpuddle.yaml", nil)
	if len(diags) > 0 {
		t.Fatalf("fixture does not load: %v", diags)
	}
	r, err := config.Check(context.Background(), fsys, "stackpuddle.yaml", p, envName(t, "local"), nil)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if r.State != diagnostics.StateUnknown || len(r.Findings) != 1 || r.Findings[0].Code != config.CodeDefinitionUnresolved {
		t.Fatalf("result = %s %+v, want unknown with %s", r.State, r.Findings, config.CodeDefinitionUnresolved)
	}
}

func TestLoadLocalFilesIsCancellable(t *testing.T) {
	t.Parallel()
	fsys := fixture.MapFS(fixture.ShopFiles(map[string]string{".env.local": "API_TOKEN=x\n"}))
	p, _ := model.Load(fsys, "stackpuddle.yaml", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := config.LoadLocalFiles(ctx, fsys, p, envName(t, "local"), config.LocalApplies)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestLoadLocalFilesReadsNothingInCI(t *testing.T) {
	t.Parallel()
	fsys := fixture.MapFS(fixture.ShopFiles(map[string]string{".env.local": "API_TOKEN=x\n"}))
	p, _ := model.Load(fsys, "stackpuddle.yaml", nil)
	files, findings, err := config.LoadLocalFiles(context.Background(), fsys, p, envName(t, "local"), config.LocalExcludedInCI)
	if err != nil || len(files) != 0 || len(findings) != 0 {
		t.Fatalf("files=%d findings=%v err=%v, want none", len(files), findings, err)
	}
}

func TestLoadLocalFilesTreatsAbsentFileAsAbsent(t *testing.T) {
	t.Parallel()
	fsys := fixture.MapFS(fixture.ShopFiles(nil))
	p, _ := model.Load(fsys, "stackpuddle.yaml", nil)
	files, findings, err := config.LoadLocalFiles(context.Background(), fsys, p, envName(t, "local"), config.LocalApplies)
	if err != nil || len(files) != 0 || len(findings) != 0 {
		t.Fatalf("files=%d findings=%v err=%v, want none", len(files), findings, err)
	}
}
