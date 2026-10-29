# StackPuddle CLI

Planned Go command-line interface for [StackPuddle](https://github.com/TomazVidovic533/stackpuddle). It will run the StackPuddle execution engine in-process; the engine itself lives in [StackPuddle Control Plane](https://github.com/TomazVidovic533/stackpuddle-control-plane).

Through that engine, the CLI will drive project onboarding, configuration, module resolution, planning, environment lifecycle, and diagnostics. All supported authoring SDKs will use the same execution engine.

Shared schemas, infrastructure modules, recipes, and bundles belong to the main StackPuddle repository. Docker Compose is the first planned local runtime.

## Status

Early development. No installable release is available yet.

## What is here today

The `stackpuddle` command prints its help, and `stackpuddle completion bash|zsh|fish|powershell` prints a shell completion script. It has no other commands yet.

```bash
go run ./cmd/stackpuddle --help
```

Run the checks with Go 1.27.0 or later, Task 3, and a C compiler (the race detector needs cgo). `task check` checks formatting, runs static analysis, verifies dependencies, runs the tests, and builds the binary to `bin/stackpuddle`.

```bash
GOWORK=off task check
```

## License

Licensed under the [Apache License 2.0](LICENSE).
