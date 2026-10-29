package fixture

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/TomazVidovic533/stackpuddle/artifacts"
)

// ModuleName is the packaged module the published fixtures carry: the audit-store definition Shop uses
// project-locally, here as an archive.
const ModuleName = "audit-store"

// Archive is one built archive and its digest.
type Archive struct {
	Data   []byte
	Digest string
}

// BuildArchive builds a real archive of kind from files with artifacts.Build, so the bytes are exactly what a
// publisher would serve.
func BuildArchive(t testing.TB, kind artifacts.Kind, files map[string]string) Archive {
	t.Helper()
	dir := Dir(t, files)
	var buf bytes.Buffer
	digest, err := artifacts.Build(kind, dir, &buf)
	if err != nil {
		t.Fatalf("build %s archive: %v", kind, err)
	}
	return Archive{Data: buf.Bytes(), Digest: digest}
}

// ModuleFiles is the audit-store definition at the archive root, with the module named name. note changes the
// bytes, so two calls with different notes give different digests.
func ModuleFiles(name, note string) map[string]string {
	files := map[string]string{}
	for path, content := range AuditDefinition() {
		rel := strings.TrimPrefix(path, "definitions/audit-store/")
		if rel == "module.yaml" {
			content = strings.Replace(content, "name: audit-store", "name: "+name, 1)
		}
		if rel == "content/defaults.yaml" {
			content += "# " + note + "\n"
		}
		files[rel] = content
	}
	return files
}

// BundleFiles is a bundle document that tests the listed modules at 0.1.0.
func BundleFiles(modules ...string) map[string]string {
	var b strings.Builder
	b.WriteString("apiVersion: stackpuddle/v1\nkind: Bundle\nversion: 0.1.0-alpha.1\nmodules:\n")
	for _, m := range modules {
		fmt.Fprintf(&b, "  %s:\n    version: 0.1.0\n", m)
	}
	return map[string]string{"bundle.yaml": b.String()}
}

// LockedModule is one module entry of a Lock.
type LockedModule struct {
	Version, Source, Digest string
}

// LockYAML writes a Lock document for the bundle and the modules, in module-name order.
func LockYAML(bundleSource, bundleDigest string, modules map[string]LockedModule) string {
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: stackpuddle/v1\nkind: Lock\nbundle:\n  version: 0.1.0-alpha.1\n  source: %s\n  digest: %s\nmodules:", bundleSource, bundleDigest)
	if len(modules) == 0 {
		b.WriteString(" {}")
	}
	b.WriteString("\n")
	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	for i := range names {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	for _, name := range names {
		m := modules[name]
		fmt.Fprintf(&b, "  %s:\n    version: %s\n    source: %s\n    format: tar+gzip\n    digest: %s\n", name, m.Version, m.Source, m.Digest)
	}
	return b.String()
}

// PackagedProject is a project whose only module instance comes from a packaged module: it needs the Lock
// before it can run anything.
func PackagedProject(lock string) map[string]string {
	return map[string]string{
		"stackpuddle.yaml": `apiVersion: stackpuddle/v1
kind: Project
metadata:
  name: catalog
spec:
  manifest: stackpuddle.manifest.yaml
  lock: stackpuddle.lock.yaml
  modules:
    audit:
      definition: audit-store
      options:
        retentionDays: 30
  environments:
    local:
      runtime:
        type: compose
        context: desktop-linux
      secrets:
        provider: local
`,
		"stackpuddle.manifest.yaml": "apiVersion: stackpuddle/v1\nkind: Manifest\nbundleVersion: 0.1.0-alpha.1\nmodules:\n  - audit-store\n",
		"stackpuddle.lock.yaml":     lock,
	}
}
