// Package fixture builds the projects the tests load: real YAML on a real temporary directory, never a
// double of the loader.
package fixture

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// Canary is a value no output, finding or error may ever contain.
const Canary = "sp-canary-7f3a91"

// Shop is a project with public and secret inputs, a workload with a binding-delivered key, a project-local
// module definition and a local environment that sets every input source.
const Shop = `apiVersion: stackpuddle/v1
kind: Project
metadata:
  name: shop
spec:
  inputs:
    api-log-level:
      sensitivity: public
      value: info
    api-token:
      sensitivity: secret
  definitions:
    audit-store:
      path: definitions/audit-store
  workloads:
    api:
      type: service
      image: ghcr.io/example/api:1.4.2
      listeners:
        http:
          port: 8080
          protocol: http
      exports:
        http:
          capability: http.api/1
          listener: http
      environment:
        LOG_LEVEL:
          input: api-log-level
        API_TOKEN:
          input: api-token
        MODE: standard
    web:
      type: service
      image: ghcr.io/example/web:3.0.0
      dependencies:
        api:
          capability: http.api/1
  modules:
    audit:
      definition: audit-store
      options:
        retentionDays: 30
  bindings:
    - consumer: workload/web
      dependency: api
      source:
        object: workload/api
        export: http
      delivery:
        environment:
          API_HOST: host
  environments:
    local:
      runtime:
        type: compose
        context: desktop-linux
      secrets:
        provider: local
      inputs:
        api-log-level:
          value: debug
        api-token:
          local:
            file: .env.local
            key: API_TOKEN
          process: SHOP_API_TOKEN
      overrides:
        workloads:
          api:
            environment:
              MODE: verbose
        modules:
          audit:
            options:
              level: full
`

// AuditDefinition is the project-local module definition Shop uses.
func AuditDefinition() map[string]string {
	return map[string]string{
		"definitions/audit-store/module.yaml": `apiVersion: stackpuddle/v1
kind: Module
name: audit-store
version: 0.1.0
dataFormat: audit-log/1
requires:
  capabilities: [runtime.compose/1]
configuration:
  schema: content/options.schema.json
  defaults: content/defaults.yaml
workloads:
  store:
    type: service
    image: ghcr.io/example/audit-store:0.3.1
    listeners:
      ingest:
        port: 9400
        protocol: http
    exports:
      ingest:
        capability: audit.ingest/1
        listener: ingest
    volumes:
      log:
        path: /var/lib/audit
`,
		"definitions/audit-store/content/defaults.yaml": "level: summary\n",
		"definitions/audit-store/content/options.schema.json": `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "urn:stackpuddle:module:audit-store:configuration",
  "type": "object",
  "additionalProperties": false,
  "required": ["retentionDays"],
  "properties": {
    "retentionDays": { "type": "integer", "minimum": 1, "maximum": 3650 },
    "level": { "enum": ["summary", "full"] }
  }
}
`,
	}
}

// ShopFiles returns the files of Shop with its module definition, plus extra (which wins).
func ShopFiles(extra map[string]string) map[string]string {
	files := AuditDefinition()
	files["stackpuddle.yaml"] = Shop
	for k, v := range extra {
		files[k] = v
	}
	return files
}

// Unresolved is a project whose Manifest lists a packaged module that nothing supplies, so it loads with
// the module unresolved and every check that needs the definition is incomplete.
func Unresolved() map[string]string {
	return map[string]string{
		"stackpuddle.yaml": `apiVersion: stackpuddle/v1
kind: Project
metadata:
  name: catalog
spec:
  manifest: stackpuddle.manifest.yaml
  modules:
    db:
      definition: postgres
      options:
        users:
          app: {purpose: runtime, passwordSecretRef: app-password, memberOf: [writer]}
          migrator: {purpose: migrations, passwordSecretRef: migrator-password}
        roles:
          writer: {login: false}
        databases:
          catalog:
            owner: migrator
            connectRoles: [writer]
            schemas:
              public:
                grants:
                  writer: {tables: [SELECT]}
  environments:
    local:
      runtime:
        type: compose
        context: desktop-linux
      secrets:
        provider: local
`,
		"stackpuddle.manifest.yaml": "apiVersion: stackpuddle/v1\nkind: Manifest\nbundleVersion: 0.1.0-alpha.1\nmodules:\n  - postgres\n",
	}
}

// Dir writes files under a new temporary directory and returns it.
func Dir(t testing.TB, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("create directory for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// MapFS returns files as an in-memory file system.
func MapFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, content := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(content)}
	}
	return fsys
}

// Lookup returns a process lookup over vars.
func Lookup(vars map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}
