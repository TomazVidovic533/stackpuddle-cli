// Package binary_test builds the real binary and checks it against its own build information.
package binary_test

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	cliModule   = "github.com/TomazVidovic533/stackpuddle-cli"
	modelModule = "github.com/TomazVidovic533/stackpuddle"
	buildBudget = 5 * time.Minute
)

func buildBinary(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), buildBudget)
	defer cancel()
	bin := filepath.Join(t.TempDir(), "stackpuddle")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/stackpuddle")
	cmd.Dir = filepath.Join("..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// buildInfo is what `go version -m` prints for the binary.
type buildInfo struct {
	goVersion    string
	mainVersion  string
	modelVersion string
	platform     string
}

func readBuildInfo(t *testing.T, bin string) buildInfo {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "go", "version", "-m", bin).CombinedOutput()
	if err != nil {
		t.Fatalf("go version -m: %v\n%s", err, out)
	}
	var info buildInfo
	var goos, goarch string
	for i, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		switch {
		case i == 0:
			info.goVersion = strings.TrimSpace(line[strings.LastIndex(line, ":")+1:])
		case len(fields) >= 3 && fields[0] == "mod" && fields[1] == cliModule:
			info.mainVersion = fields[2]
		case len(fields) >= 3 && fields[0] == "dep" && fields[1] == modelModule:
			info.modelVersion = fields[2]
		case len(fields) >= 2 && fields[0] == "build" && strings.HasPrefix(fields[1], "GOOS="):
			goos = strings.TrimPrefix(fields[1], "GOOS=")
		case len(fields) >= 2 && fields[0] == "build" && strings.HasPrefix(fields[1], "GOARCH="):
			goarch = strings.TrimPrefix(fields[1], "GOARCH=")
		}
	}
	info.platform = goos + "/" + goarch
	return info
}

func TestVersionMatchesTheBinarysBuildInformation(t *testing.T) {
	t.Parallel()
	bin := buildBinary(t)
	want := readBuildInfo(t, bin)
	if want.mainVersion == "" || want.modelVersion == "" || want.goVersion == "" {
		t.Fatalf("go version -m gave an incomplete picture: %+v", want)
	}
	js, err := exec.CommandContext(t.Context(), bin, "--version", "--output", "json").Output()
	if err != nil {
		t.Fatalf("--version --output json: %v", err)
	}
	var doc struct {
		CLI struct {
			Version  string `json:"version"`
			Go       string `json:"go"`
			Platform string `json:"platform"`
		} `json:"cli"`
		Model struct {
			Version     string   `json:"version"`
			APIVersions []string `json:"apiVersions"`
		} `json:"model"`
	}
	if err := json.Unmarshal(js, &doc); err != nil {
		t.Fatalf("version JSON: %v\n%s", err, js)
	}
	if doc.CLI.Version != want.mainVersion || doc.CLI.Go != want.goVersion ||
		doc.CLI.Platform != want.platform || doc.Model.Version != want.modelVersion {
		t.Fatalf("version JSON %+v differs from go version -m %+v", doc, want)
	}
	if len(doc.Model.APIVersions) != 1 || doc.Model.APIVersions[0] != "stackpuddle/v1" {
		t.Fatalf("apiVersions = %v, want [stackpuddle/v1]", doc.Model.APIVersions)
	}
	text, err := exec.CommandContext(t.Context(), bin, "--version").Output()
	if err != nil {
		t.Fatalf("--version: %v", err)
	}
	for _, fact := range []string{want.mainVersion, want.goVersion, want.platform, want.modelVersion, "stackpuddle/v1"} {
		if !strings.Contains(string(text), fact) {
			t.Errorf("--version text lacks %q:\n%s", fact, text)
		}
	}
}

func TestCompletionScriptRegistersTheCommand(t *testing.T) {
	t.Parallel()
	bin := buildBinary(t)
	out, err := exec.CommandContext(t.Context(), bin, "completion", "bash").Output()
	if err != nil {
		t.Fatalf("completion bash: %v", err)
	}
	if !strings.Contains(string(out), "complete -o default -F __start_stackpuddle stackpuddle") {
		t.Fatalf("the bash completion script does not register stackpuddle:\n%.400s", out)
	}
}

func TestUnknownArgumentsExitNonZero(t *testing.T) {
	t.Parallel()
	bin := buildBinary(t)
	err := exec.CommandContext(t.Context(), bin, "frobnicate").Run()
	exit, ok := errors.AsType[*exec.ExitError](err)
	if !ok || exit.ExitCode() != 2 {
		t.Fatalf("stackpuddle frobnicate: err = %v, want exit code 2", err)
	}
}
