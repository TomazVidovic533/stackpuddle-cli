# StackPuddle CLI

Go command-line interface for [StackPuddle](https://github.com/TomazVidovic533/stackpuddle). It runs the StackPuddle execution engine in-process; the engine itself lives in [StackPuddle Control Plane](https://github.com/TomazVidovic533/stackpuddle-control-plane).

Through that engine, the CLI checks and inspects a project's configuration and runs the environment lifecycle (up, stop, destroy, cancel, list and inspect) on Docker Compose.

Shared schemas, infrastructure modules, recipes, and bundles belong to the main StackPuddle repository. Docker Compose is the local runtime.

## Status

Early development. No installable release is available yet.

## What is here today

`stackpuddle check` validates a project and its configuration inputs, and `stackpuddle inspect <ref> --view config
--explain` shows the effective values of a workload or module with the source of each. `--version` prints the build
and the contract versions it reads; `stackpuddle completion bash|zsh|fish|powershell` prints a completion script.
`check` needs no Docker and no control plane; `check --live` also asks the running system without changing it.

The lifecycle verbs run the engine in-process: `up`, `stop` and `destroy` (each with `--dry-run`), `remove`,
`run <job>`, `cancel <op-id>`, `list`, `inspect operation/<id> --wait|--follow` and `inspect <ref> --view state`, with
`--timeout` for a busy environment. The commands, flags, JSON output and exit codes are in
[docs/commands.md](docs/commands.md).

```bash
go run ./cmd/stackpuddle check --file path/to/stackpuddle.yaml
go run ./cmd/stackpuddle inspect workload/api --view config --explain --env local
```

Run the checks with Go 1.27.0 or later, Task 3, and a C compiler (the race detector needs cgo). `task check` checks
formatting with the toolchain's own gofmt, runs static analysis, verifies dependencies, runs the tests, and builds
the binary to `bin/stackpuddle`.

```bash
GOWORK=off task check
GOWORK=off task test:integration   # Docker: runs real operations in a disposable Compose project
```

Until the control plane and stackpuddle are pushed at the commits this CLI uses, both tasks run against a
workspace file kept outside the repository (`GOWORK=<file> task check`); `check:tidy` then reports `NOT RUN`.

## License

Licensed under the [Apache License 2.0](LICENSE).
