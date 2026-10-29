// Package architecture_test enforces the dependency direction and the configuration-access rule by parsing
// the source, so a violation fails by file name.
package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	modulePath = "github.com/TomazVidovic533/stackpuddle-cli"
	modelPath  = "github.com/TomazVidovic533/stackpuddle"
	cobraPath  = "github.com/spf13/cobra"

	controlPlanePath = "github.com/TomazVidovic533/stackpuddle-control-plane"
	inprocessPath    = modulePath + "/internal/application/inprocess"
)

type sourceFile struct {
	path    string // slash-separated, relative to the module root
	imports []string
	file    *ast.File
}

func parseModule(t *testing.T) []sourceFile {
	t.Helper()
	root := filepath.Join("..", "..")
	var files []sourceFile
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly|parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		full, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		sf := sourceFile{path: filepath.ToSlash(rel), file: full}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			sf.imports = append(sf.imports, p)
		}
		files = append(files, sf)
		return nil
	})
	if err != nil {
		t.Fatalf("parse module: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no source files found")
	}
	return files
}

// dirOf returns the directory of a slash-separated path.
func dirOf(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[:i]
	}
	return "."
}

func importsAny(imports []string, prefixes ...string) (string, bool) {
	for _, imp := range imports {
		if slices.ContainsFunc(prefixes, func(p string) bool { return imp == p || strings.HasPrefix(imp, p+"/") }) {
			return imp, true
		}
	}
	return "", false
}

func under(path, dir string) bool { return path == dir || strings.HasPrefix(path, dir+"/") }

func isStdlib(p string) bool { return !strings.Contains(strings.SplitN(p, "/", 2)[0], ".") }

func TestCobraStaysAtTheEdge(t *testing.T) {
	t.Parallel()
	for _, f := range parseModule(t) {
		if under(f.path, "internal/cli") || under(f.path, "cmd") {
			continue
		}
		if slices.Contains(f.imports, cobraPath) {
			t.Errorf("%s imports cobra; only internal/cli and cmd may", f.path)
		}
	}
}

func TestApplicationImportsNoAdapterNorEdge(t *testing.T) {
	t.Parallel()
	banned := []string{modulePath + "/internal/project", modulePath + "/internal/cli", cobraPath}
	for _, f := range parseModule(t) {
		if !under(f.path, "internal/application") {
			continue
		}
		for _, imp := range f.imports {
			if slices.ContainsFunc(banned, func(b string) bool { return imp == b || strings.HasPrefix(imp, b+"/") }) {
				t.Errorf("%s imports %s; application declares ports and imports no adapter or edge", f.path, imp)
			}
		}
	}
}

func TestDiagnosticsImportsOnlyTheModelAndTheStandardLibrary(t *testing.T) {
	t.Parallel()
	for _, f := range parseModule(t) {
		if !under(f.path, "internal/diagnostics") {
			continue
		}
		for _, imp := range f.imports {
			if !isStdlib(imp) && imp != modelPath && !strings.HasPrefix(imp, modelPath+"/") {
				t.Errorf("%s imports %s; diagnostics imports only model and the standard library", f.path, imp)
			}
		}
	}
}

func TestConfigDoesNotReachTowardsTheEdge(t *testing.T) {
	t.Parallel()
	banned := []string{modulePath + "/internal/application", modulePath + "/internal/project", modulePath + "/internal/cli", cobraPath}
	for _, f := range parseModule(t) {
		if !under(f.path, "internal/config") {
			continue
		}
		for _, imp := range f.imports {
			if slices.ContainsFunc(banned, func(b string) bool { return imp == b || strings.HasPrefix(imp, b+"/") }) {
				t.Errorf("%s imports %s; config sits below application", f.path, imp)
			}
		}
	}
}

func TestOnlyCmdReadsTheProcessEnvironment(t *testing.T) {
	t.Parallel()
	forbidden := map[string]bool{"Getenv": true, "LookupEnv": true, "Environ": true}
	for _, f := range parseModule(t) {
		if under(f.path, "cmd") || under(f.path, "tests") {
			continue
		}
		ast.Inspect(f.file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "os" && forbidden[sel.Sel.Name] {
				t.Errorf("%s uses os.%s; the environment arrives through the injected lookup", f.path, sel.Sel.Name)
			}
			return true
		})
	}
}

func TestNoPackageLevelMutableStateOrInit(t *testing.T) {
	t.Parallel()
	for _, f := range parseModule(t) {
		for _, decl := range f.file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.Name == "init" {
					t.Errorf("%s declares init()", f.path)
				}
			case *ast.GenDecl:
				if d.Tok == token.VAR {
					t.Errorf("%s declares a package-level var", f.path)
				}
			}
		}
	}
}

// The control plane is linked in one place: the in-process operations adapter, wired by cmd. Every command
// reaches the engine through the application's operations port, so the remote adapter of a later milestone
// replaces one package and no command changes.
func TestOnlyTheInProcessAdapterAndCmdImportTheControlPlane(t *testing.T) {
	t.Parallel()
	for _, f := range parseModule(t) {
		if under(f.path, "internal/application/inprocess") || under(f.path, "cmd") {
			continue
		}
		if imp, bad := importsAny(f.imports, controlPlanePath); bad {
			t.Errorf("%s imports %s; only internal/application/inprocess and cmd may import the control plane", f.path, imp)
		}
	}
}

func TestTheCommandEdgeNeverImportsTheEngineOrAnAdapter(t *testing.T) {
	t.Parallel()
	banned := []string{controlPlanePath, inprocessPath, modulePath + "/internal/project", modulePath + "/internal/distribution"}
	checked := 0
	for _, f := range parseModule(t) {
		if !under(f.path, "internal/cli") {
			continue
		}
		checked++
		if imp, bad := importsAny(f.imports, banned...); bad {
			t.Errorf("%s imports %s; commands stay thin and reach the engine through internal/application", f.path, imp)
		}
	}
	if checked == 0 {
		t.Fatal("no file under internal/cli was checked")
	}
}

func TestTheApplicationPackageNamesNoAdapterAndNoEngine(t *testing.T) {
	t.Parallel()
	banned := []string{controlPlanePath, inprocessPath, modulePath + "/internal/distribution", modulePath + "/internal/project", modulePath + "/internal/cli"}
	checked := 0
	for _, f := range parseModule(t) {
		if dirOf(f.path) != "internal/application" {
			continue
		}
		checked++
		if imp, bad := importsAny(f.imports, banned...); bad {
			t.Errorf("%s imports %s; the application declares ports and imports no adapter or engine", f.path, imp)
		}
	}
	if checked == 0 {
		t.Fatal("no file in internal/application was checked")
	}
}

func TestTheInProcessAdapterImportsOnlyTheEngineAndTheApplication(t *testing.T) {
	t.Parallel()
	for _, f := range parseModule(t) {
		if !under(f.path, "internal/application/inprocess") {
			continue
		}
		for _, imp := range f.imports {
			ok := isStdlib(imp) || imp == modelPath || strings.HasPrefix(imp, modelPath+"/") ||
				imp == modulePath+"/internal/application" || imp == controlPlanePath+"/engine"
			if !ok {
				t.Errorf("%s imports %s; the adapter may use the engine's public package only", f.path, imp)
			}
		}
	}
}

func TestNoSourceReadsTheStackpuddleSourceTree(t *testing.T) {
	t.Parallel()
	for _, f := range parseModule(t) {
		if under(f.path, "tests") {
			continue
		}
		ast.Inspect(f.file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(lit.Value)
			if err == nil && (strings.Contains(text, "../stackpuddle") || strings.Contains(text, "modules/postgres")) {
				t.Errorf("%s holds the path %q; packaged modules come from the Lock, never from a source tree", f.path, text)
			}
			return true
		})
	}
}
