package fixture

// Storefront is a project for the lifecycle verbs: two overlapping applications, a job that api requires, a
// binding from web to api, and two module instances that share one definition.
const Storefront = `apiVersion: stackpuddle/v1
kind: Project
metadata:
  name: storefront
spec:
  definitions:
    audit-store:
      path: definitions/audit-store
  workloads:
    # api serves the storefront and the back office.
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
      prerequisites:
        - job: migrate
    migrate:
      type: job
      image: ghcr.io/example/api:1.4.2
      command: [./api, migrate]
    web:
      type: service
      image: ghcr.io/example/web:3.0.0
      dependencies:
        api:
          capability: http.api/1
    backoffice:
      type: service
      image: ghcr.io/example/backoffice:0.9.0
  modules:
    audit:
      definition: audit-store
      options:
        retentionDays: 30
    audit-copy:
      definition: audit-store
      options:
        retentionDays: 7
  applications:
    storefront:
      workloads: [web, api, migrate]
      default: true
    admin:
      workloads: [backoffice, api]
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
`

// StorefrontFiles returns the files of Storefront with its module definition.
func StorefrontFiles() map[string]string {
	files := AuditDefinition()
	files["stackpuddle.yaml"] = Storefront
	return files
}
