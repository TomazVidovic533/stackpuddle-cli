package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// CheckerName is the name of the configuration checker in a report.
const CheckerName = "configuration"

// Codes of the findings the local file reads and the check report.
const (
	CodeLocalFileInvalidPath = "config.local_file_invalid_path"
	CodeLocalFileUnreadable  = "config.local_file_unreadable"
	CodeLocalFileTooLarge    = "config.local_file_too_large"
	CodeDefinitionUnresolved = "config.definition_unresolved"
)

// maxLocalFileBytes bounds one local file, the same budget the model gives every project file.
const maxLocalFileBytes = 1 << 20

// PolicyFor returns LocalExcludedInCI when the lookup reports a truthy CI variable, else LocalApplies.
// A variable that is unset, empty, "0", "false", "no" or "off" is not truthy.
func PolicyFor(lookup func(string) (string, bool)) LocalPolicy {
	if lookup == nil {
		return LocalApplies
	}
	v, ok := lookup("CI")
	if !ok {
		return LocalApplies
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return LocalApplies
	}
	return LocalExcludedInCI
}

// LoadLocalFiles reads and parses the ignored local files env names, through root, which the caller
// confines. Under LocalExcludedInCI it reads nothing. A file that does not exist is absent from the result,
// not an error: local files are optional by design. It checks ctx between files and returns ctx's error
// when cancelled; every other problem is a finding and the file is left out.
func LoadLocalFiles(ctx context.Context, root fs.FS, p model.Project, env model.EnvironmentName,
	policy LocalPolicy,
) (map[string]DotenvFile, []diagnostics.Finding, error) {
	files := map[string]DotenvFile{}
	if policy == LocalExcludedInCI {
		return files, nil, nil
	}
	environment, ok := p.Environment(env)
	if !ok {
		return files, nil, nil
	}
	var findings []diagnostics.Finding
	for _, sources := range environment.InputSources() {
		local, ok := sources.Local()
		if !ok {
			continue
		}
		if _, done := files[local.File]; done {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, fmt.Errorf("reading local files: %w", err)
		}
		data, found, finding := readBounded(root, local.File)
		if finding != nil {
			findings = append(findings, *finding)
			files[local.File] = DotenvFile{}
			continue
		}
		if !found {
			continue
		}
		parsed, parseFindings := ParseDotenv(local.File, data)
		findings = append(findings, parseFindings...)
		files[local.File] = parsed
	}
	return files, findings, nil
}

func readBounded(root fs.FS, name string) ([]byte, bool, *diagnostics.Finding) {
	at := model.NewLocation(name, "", 0, 0)
	if !fs.ValidPath(name) {
		return nil, false, &diagnostics.Finding{
			Code: CodeLocalFileInvalidPath, Location: at,
			Message: "local file path must be relative and stay inside the project directory",
		}
	}
	f, err := root.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, &diagnostics.Finding{Code: CodeLocalFileUnreadable, Location: at, Message: "local file cannot be opened"}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxLocalFileBytes+1))
	if err != nil {
		return nil, false, &diagnostics.Finding{Code: CodeLocalFileUnreadable, Location: at, Message: "local file cannot be read"}
	}
	if len(data) > maxLocalFileBytes {
		return nil, false, &diagnostics.Finding{
			Code: CodeLocalFileTooLarge, Location: at, Message: "local file is larger than 1 MiB",
		}
	}
	return data, true, nil
}

// Check runs the configuration check of env: it reads the local files, resolves the layers and reports
// missing inputs, binding-owned keys and unreadable or malformed local files. The result is fail when
// any of those are found, unknown when module options could not be checked because a definition is
// unresolved, else pass. It returns an error only when ctx is cancelled.
func Check(ctx context.Context, root fs.FS, rootFile string, p model.Project, env model.EnvironmentName,
	lookup func(string) (string, bool),
) (diagnostics.Result, error) {
	policy := PolicyFor(lookup)
	files, findings, err := LoadLocalFiles(ctx, root, p, env, policy)
	if err != nil {
		return diagnostics.Result{}, err
	}
	_, resolved := Resolve(p, env, Inputs{
		LocalFiles: files, LookupProcess: lookup, Local: policy, RootFile: rootFile,
	})
	findings = append(findings, resolved...)
	result := diagnostics.Result{
		Checker: CheckerName, Stage: diagnostics.StageConfiguration, State: diagnostics.StatePass, Findings: findings,
	}
	if len(findings) > 0 {
		result.State = diagnostics.StateFail
		return result.ForEnvironment(env), nil
	}
	for _, m := range p.ModuleInstances() {
		if _, ok := p.EnvironmentOptions(env, m.Key()); ok {
			continue
		}
		if !slices.Contains(p.Unresolved(), m.Definition()) {
			continue
		}
		result.State = diagnostics.StateUnknown
		result.Findings = append(result.Findings, diagnostics.Finding{
			Code:     CodeDefinitionUnresolved,
			Location: m.Locate(""),
			Message:  fmt.Sprintf("options of module %q were not checked: definition %q is not supplied", m.Key(), m.Definition()),
		})
	}
	return result.ForEnvironment(env), nil
}
