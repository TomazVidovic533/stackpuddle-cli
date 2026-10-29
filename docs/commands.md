# Commands

`stackpuddle` reads a project, checks it, shows its effective configuration and state, and runs the lifecycle of an
environment through the in-process engine. This page states the behaviour of `check`, `inspect`, `up`, `stop`,
`destroy`, `remove`, `cancel`, `list` and `--version`, the JSON documents they print and the exit codes. The
semantics of PostgreSQL options and the live checks arrive in later releases.

## Selecting a project

| Flag | Meaning |
| --- | --- |
| `--file <path>` | The composition root: a Project file, a canonical Model file (the TypeScript SDK writes one), or a directory searched like the working directory. Relative paths resolve against the working directory. |
| `--env <name>` | A declared environment. `check` without it checks every declared environment. |
| `--output text\|json` | One output format. Any other value is a usage error. |
| `--non-interactive` | Never prompt. The commands here never prompt, so a missing input fails with or without it. |
| `--timeout <duration>` | How long to wait for a busy environment (`up`, `stop`, `destroy`) and how long `inspect operation/<id> --wait\|--follow` polls. Without it a busy environment fails at once. It never cancels accepted work. A negative value is a usage error. |

Without `--file` the root is `stackpuddle.yaml` in the working directory. If `stackpuddle.yaml` and
`stackpuddle.yml` both exist the command fails with `project.root_ambiguous` instead of guessing; none fails
with `project.root_not_found`. The directory of the root file is opened as a confined root: an import, a
definition path or a local file that leaves it is refused, symlinks included. The CLI never starts Node or any
other process to read a project.

## check

`stackpuddle check` loads the project once and runs the checkers in order. Each prints one result per
environment (structure prints one for the project):

- `structure`: syntax, compatibility, structure, identity, references, bindings and graph, as the model reports
  them. When it fails, later checkers report `skipped`.
- `configuration`: applies the configuration layers to each environment and reports a missing non-secret input
  (`config.input_missing`), a malformed local file (`config.dotenv_invalid`, `config.local_file_too_large`,
  `config.local_file_unreadable`, `config.local_file_invalid_path`) and, as `unknown`, module options it could not
  check because their definition is not supplied (`config.definition_unresolved`).

- `semantic`: each module's own rules over its effective options, offline (for example `postgres.unknown_member_role`).
  A module whose definition is not resolved (nothing cached yet) or that has no rules is `unknown`
  (`semantic.module_unchecked`), never `pass`. Packaged definitions come from the verified cache when it is warm.
- `live` (only with `--live`): asks the running system what an `up` would meet, through a dry-run `up`. It takes
  no lock and runs no mutating probe. A Docker daemon it cannot reach and a missing secrets key are `unknown`; a
  lost or unowned data object (`ownership.lost_object`) is `fail`, naming the object and the consumers that bind
  it; a step the plan cannot decide is `unknown`. Each live result carries `observedAt` and the state
  `generation` it is based on. Docker evidence for this checker is pending until the acceptance run passes.

`check` takes no lock, reads no state folder (`STACKPUDDLE_STATE_DIR` is never read) and needs no Docker or
control plane. It runs the same on a CI runner and a laptop.

States are `pass`, `fail`, `unknown` and `skipped`. The outcome is `fail` when any result failed, else `unknown`
when any result is `unknown` or `skipped`, else `pass`.

```json
{
  "results": [
    {"checker": "configuration", "stage": "configuration", "state": "fail", "environment": "local",
     "findings": [{"code": "config.input_missing", "file": "stackpuddle.yaml", "line": 12, "column": 7,
                   "pointer": "/spec/inputs/region", "message": "..."}]}
  ],
  "outcome": "fail"
}
```

`line`, `column` and `environment` appear only when known. Messages are for people; match on `code`.

## inspect --view config

`stackpuddle inspect workload/<key> --view config [--explain]` and `module/<key>` show the effective values of one
object in one environment. `--env` is required when the project declares more than one environment. Other
reference kinds and other views (`definition`, `state`, `graph`) fail as usage errors. `--view` defaults to `config`.

Workload values are its environment variables: the workload's literal or input reference, then the selected
environment's override. Module values are the leaves of its options, named by JSON pointer: defaults, then the
declared options, then the environment's override. A module whose definition is not supplied prints `unknown`
instead of a guessed default.

`--explain` adds, for each value, its source and the sources it shadowed. A secret prints as `[redacted]` in text
and as `"redacted": true` in JSON; the value itself is never an output. A secret no layer supplies has status
`deferred`: the environment's secret provider resolves it later.

```json
{
  "ref": "workload/api",
  "environment": "local",
  "entries": [
    {"name": "MODE", "status": "supplied", "value": "verbose",
     "source": {"layer": "environment", "location": {"file": "stackpuddle.yaml", "pointer": "/spec/environments/local/overrides/workloads/api/environment/MODE"}}}
  ]
}
```

### Precedence

Lowest to highest: module defaults, project declarations, the selected environment, the ignored local file the
environment names, the process variable the environment maps. Only the variable names an environment maps are
read from the process; nothing else is forwarded. A secret never takes a literal. A value is *missing* when no
layer supplies it and *empty* when a layer supplies the empty string (`KEY=`, `KEY=""` or an empty variable); the
two differ. Omission inherits; an explicit override replaces; `null` is refused by the model.

When the `CI` variable is set to anything but empty, `0`, `false`, `no` or `off`, the local layer is excluded and
each excluded source shows as shadowed with reason `ci`. Overriding an environment variable a binding delivers is
refused when the project loads (`binding.delivery_conflict`).

### Local files

A local file uses a small dotenv subset: `KEY=value` lines, `#` comments, blank lines, single- or double-quoted
values without interpolation (double quotes allow `\"` and `\\` only), no `export`, no multi-line values, no
duplicate keys. A file larger than 1 MiB is refused. A parse error names file, line and, when valid, the key,
never the text of the line. A local file that does not exist supplies nothing.

## Selecting objects

`up`, `stop` and `destroy` take references: `app/<key>` (a group), `workload/<key>`, `module/<key>` and
`env/<name>` (everything in that environment). Groups expand and deduplicate, so a workload in two groups is one
selection. A module selection does not select its consumers. A job never runs because its group was selected:
it runs when named, or when a selected workload requires it, and the output lists those prerequisites apart.
`--all` and explicit references exclude each other. `up` without references selects the default applications;
`stop` needs references or `--all`; `destroy` needs references (`env/<name>` for the whole environment).

When the engine refuses a change that reaches objects the selection does not name (a definition shared with
other module instances, or a section every consumer shares), the command fails with
`selection.expanded_scope_required` (exit 3), names the unselected consumers and prints the expanded selection
that would allow it; `--expand-scope` accepts the wider effect.

## up, stop, destroy

`stackpuddle up [refs] [--all] [--dry-run] [--expand-scope]` prints the selection and the accepted operation ID
(`op-` and 26 characters), then runs the operation and prints each step as it changes on stderr (text output
only). The result on stdout is the selection and the operation, or with `--dry-run` the typed actions, the ones
that are `unknown` (the plan cannot say whether the step still has anything to do) and the `basedOnGeneration`
the preview was computed from. A dry run takes no lock and writes no state, pin or runtime object; it may fill the
module cache.

`stop <refs>|--all [--dry-run]` records the stopped intent and keeps every volume. A later `up` without that
selection, `inspect` or `check --live` does not restart the stopped workloads.

`destroy <refs> [--dry-run] [--delete-data]` deletes only the runtime resources stackpuddle owns in the selected
environment. Persistent data (volumes) stays unless `--delete-data` names that intent. Another environment's
resources are never touched.

Two processes on one environment: the second exits `6` and names the operation that holds it and the command
`stackpuddle inspect operation/<id> --follow`. With `--timeout` it waits for the lock, then proceeds, or exits `6`
at the deadline.

SIGINT, SIGTERM and SIGHUP stop the operation at its next step boundary and leave it `interrupted` with its
journal intact (exit `8`). The next `up` continues the intent under a new operation ID that names the old one;
that is what resume means until the service milestone. A terminal's Ctrl-C reaches the CLI's process group, so
the CLI and its child processes see it together.

## inspect operation, inspect --view state

`stackpuddle inspect operation/<id>` reads an open or archived operation without the lock. With `--wait` it polls
every 500 ms until the operation is terminal and exits with its outcome (`0` succeeded, `10` failed or blocked,
`8` interrupted, `9` cancelled); `--follow` also prints each step as it changes on stderr. `--timeout` bounds the
wait and exits `6` with `conflict.wait_timeout` when it passes; the operation keeps running. The JSON document is
the stackpuddle Operation schema.

`stackpuddle inspect <ref> --view state` (`workload/<key>`, `module/<key>` or `env/<name>`) shows the accepted
revision, the generation (the freshness of what is shown), each realized object with its state and the runtime
objects that carry it (the ownership evidence), and the open operation if there is one. It shows no secret value.

## run

`stackpuddle run <job>` (the job's key or `workload/<key>`) invokes one job as its own recorded operation. Each
run has its own invocation ID, `<operation id>/<step id>`, and the job's native exit code is on the job step. The
job runs even when its spec is unchanged. A non-zero exit fails the operation with `job.exited_nonzero` (exit
`10`); an engine failure keeps its own class. `--dry-run` shows the invocation and runs nothing. A service is
refused with `run.not_a_job` (exit `2`). `--detach` arrives later. Docker evidence is pending until the
acceptance run passes.

## cancel

`stackpuddle cancel <op-id>` asks the engine to write the cancel request of a running operation and takes no
lock. The operation stops at its next step with partial outcomes kept and is never continued automatically.
Cancellation acts between steps: a long step such as an image pull or `initdb` runs to its own bound first, and
the command says so.

## remove

`stackpuddle remove <ref>` (`workload/<key>`, `module/<key>`, `client/<key>` or `app/<key>`) deletes the
declaration from the YAML composition root and nothing physical. Only the lines of that declaration change; the
edit is checked by loading the result in memory first, and a result that no longer loads (a remaining reference)
is refused with its findings and nothing is written. The file is replaced atomically with its original mode, a
symlinked file is never edited, and a declaration written in flow style is left for you to edit. A root written
by an SDK is refused with the edit to make in the SDK source, because SDK source is not round-trippable. The next
`up` shows the realization as retained until `destroy`.

## list

`stackpuddle list applications|workloads|modules|resources|bindings|environments` prints one collection as
`<ref>  key=value` rows, or `{"collection", "environment"?, "items": [{"ref", "attributes"}]}` as JSON. `resources`
reads the environment's state; the others read the project. `operations` and `sessions` are not listable yet and
fail with exit `2`: read one operation with `inspect operation/<id>`.

## Packaged modules and the cache

A project whose Manifest lists packaged modules needs the Lock. `up`, `stop` and `destroy` fetch each locked
archive (`https`, or `http` on `127.0.0.1` only; a redirect must stay on the host the Lock names; at most 64 MiB;
60 seconds), hash the bytes and compare them with the Lock digest before anything reads them, inspect the archive,
check the module is in the bundle's tested set, and load its definition from memory; nothing is extracted to
disk. A digest mismatch exits `7` and names the module. A module outside the bundle exits `7` before any module
is fetched or any Docker call is made. Verified archives are kept in
`<cache root>/stackpuddle/artifacts/sha256/<hex>.tar.gz` (directories `0700`, files `0600`), keyed by digest and
re-hashed on every use, so a mismatch never replaces the previous verified copy. The cache root is the user cache
directory, or `STACKPUDDLE_CACHE_DIR` (an absolute path) when set. `check` and `inspect --view config` never read
it.

## Operation records

Every operation record in JSON output (`operation` in the result of `up`, `stop` and `destroy`, the document of
`inspect operation/<id>`, and `error.operation` of a failure that ended one) is a document of the published
Operation schema. An accepted operation that did not succeed exits with its own code and carries its record in the
failure; a failure before any operation was recorded carries none.

## --version

`stackpuddle --version` prints the CLI build, the Go toolchain and platform, the stackpuddle model version and the
API versions it reads. `--version --output json` prints the VersionInfo document of the model package. The values
equal `go version -m` of the binary. The engine entry joins when the control plane is linked.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Success; the `check` outcome is `pass`. |
| 1 | Unexpected: a programming or infrastructure fault, including a cancelled run. |
| 2 | Usage: unknown command, argument or flag, bad `--output`, bad reference, unsupported view or reference kind. |
| 3 | Validation: `check` outcome `fail`, an ambiguous root, or a project too invalid for `inspect`. |
| 4 | Not found: no root file, unknown environment or object. |
| 5 | `check` found no failure but a result is `unknown` or `skipped`. |
| 6 | Conflict: the environment is busy (`conflict.environment_busy`), the state moved since it was read (`conflict.stale_generation`), or a `--wait` ended at its `--timeout`. |
| 7 | Compatibility: an apiVersion, capability, state or store version the build cannot accept, a digest that does not match the Lock, or a module outside the bundle. |
| 8 | Interrupted: the operation stopped at a step boundary because the process was signalled; run `up` again to continue. |
| 9 | Cancelled: the operation was cancelled with `cancel`; its partial outcomes are kept. |
| 10 | Operation failed: the operation was accepted and then failed or was blocked; the failure carries its record. |
| 11 | Forbidden: refused by scope or policy, such as a runtime object outside the project's scope or a redirect off the locked host. |
| 12 | Unavailable: the Docker target or an artifact source could not be reached. |

Failures print to stderr: `error (<code>): <message>` as text, or `{"error": {"class", "code", "message",
"findings"?, "operation"?}}` as JSON. A `check` outcome prints its report to stdout and nothing more.

## Limits

Reading a project is bounded by the model's limits (1 MiB per file) and the command checks for cancellation
between files. `model.Load` itself takes no context, so a cancellation lands after a load that already started.
