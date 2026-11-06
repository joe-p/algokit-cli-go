# algokit-cli-go

A Go port of the [AlgoKit CLI](https://github.com/algorandfoundation/algokit-cli), limited to the `localnet` and
`goal` commands, following [algokit-cli-ts](https://github.com/joe-p/algokit-cli-ts). It aims for parity with the
Python implementation (v2.10.x): the same commands, options, generated LocalNet configuration, on-disk locations,
console output, help text and usage errors (exit code 2). It ships as a single static binary with no runtime
dependencies.

## Usage

```sh
go install github.com/joe-p/algokit-cli-go/cmd/algokit@latest
# or
go build -o algokit ./cmd/algokit
```

```
algokit [-v] [--color/--no-color] <command>

  goal [--console] [--interactive] [GOAL_ARGS...]
  localnet config [docker|podman] [-f]
  localnet start [-n NAME] [-P CONFIG_DIR] [-d/--no-dev] [--force] [--check]
                 [--localnet-config FILE]
  localnet stop
  localnet reset [--update/--no-update] [-P CONFIG_DIR] [--check]
  localnet status [--check]
  localnet console
  localnet logs [-f/--follow] [--tail N]
```

To embed a version, build with `-ldflags "-X github.com/joe-p/algokit-cli-go/internal/commands.Version=1.2.3"`
(`go install ...@vX.Y.Z` picks up the module version automatically).

It shares state with the Python CLI, so the two can be used interchangeably:

- config: `$XDG_CONFIG_HOME/algokit` (default `~/.config/algokit`, `%APPDATA%\algokit` on Windows). This holds
  the `sandbox*/` compose directories and `active-container-engine`.
- state: `$XDG_STATE_HOME/algokit` (`~/Library/Application Support/algokit` on macOS, `%LOCALAPPDATA%\algokit` on
  Windows). This holds `cli.log` (rotating debug log, same format) and `last-localnet-version-check`.
- `ALGOKIT_LOCALNET_CONFIG_DIR` is honoured as the default for `--config-dir`.

The LocalNet templates in `internal/sandbox/templates` are generated from the Python CLI and must match it byte for
byte, otherwise the Python CLI considers the LocalNet definition out of date (and vice versa).

## Building algod from source

Build LocalNet's algod from a remote go-algorand branch, tag, or full commit SHA using a JSON configuration file.
For example, `config.json`:

```json
{
  "algod": {
    "repo": "https://github.com/algorand/go-algorand.git",
    "ref": "feature/my-change"
  }
}
```

```sh
algokit localnet start --name foo --localnet-config config.json
```

Use `algod.repo` to select a fork. At least one of `algod.repo` or `algod.ref` must be supplied; omitted fields retain
saved values, or default to upstream go-algorand and `master` on first use. Unknown fields, blank values, and invalid
JSON are rejected before changing LocalNet. Relative configuration paths resolve from the current working directory;
`--config-dir` still separately controls where LocalNet state is stored.

Supplying `--localnet-config` builds a local image before starting LocalNet. Builds run entirely in the configured
container engine, using an embedded Dockerfile and the selected checkout's Go toolchain version and runtime scripts. No host Git or Go
installation is needed. The repository must be accessible from inside the build without host credentials;
private-repository authentication is not currently configured. Only build repositories you trust: their build
scripts run inside the container build.

The effective repository and ref are saved in `algod-source.json` in the LocalNet configuration directory. Starting
again without `--localnet-config` reuses the image and does not need the original JSON file:

```sh
algokit localnet start --name foo

# Rebuild using the file's current settings and remote branch tip:
algokit localnet start --name foo --localnet-config config.json
```

Ordinary `localnet reset` reuses the image; `localnet reset --update` rebuilds the saved ref and updates the other
service images. A reset destroys chain state. As before, `reset --update` on a named instance prompts before
replacing its Compose configuration, but keeps the saved source selection. Source-built LocalNets do not perform
published-image version checks and never pull over the locally built algod image.

Named instances are recommended, especially when sharing configuration with the Python CLI, which does not
understand these source settings. Older refs must still provide a compatible build-script and runtime layout.

## Releases

Releases are managed by [semfrag](https://github.com/joe-p/semfrag). Each pull request that changes Go code adds a
fragment to `changelog.d/` (enforced in CI), for example `changelog.d/fix-logs-tail.md`:

```md
## Fixes

- Fix `localnet logs --tail` being ignored
```

The sections and the version bump each implies are configured in `semfrag.json` (`Breaking Changes` and `Features`
bump the minor version while pre-1.0, `Fixes` the patch). On every push to `main`, `.github/workflows/release.yml`
merges the fragments into `CHANGELOG.md`, commits and tags the release, and creates a GitHub release with standalone
binaries for Linux, macOS and Windows (x64 and arm64) built by `scripts/build-binaries.sh`.

## Layout

| Path | Python equivalent |
| --- | --- |
| `internal/commands/root.go` | `algokit/cli/__init__.py` (root group, `-v`, `--color`, error handling) |
| `internal/commands/localnet.go` | `algokit/cli/localnet.py` |
| `internal/commands/goal.go` | `algokit/cli/goal.py` |
| `internal/sandbox` | `algokit/core/sandbox.py` |
| `internal/goal` | `algokit/core/goal.py` |
| `internal/proc` | `algokit/core/proc.py` |
| `internal/log` | `algokit/core/log_handlers.py` |
| `internal/cli` | click (option parsing, help formatting and usage errors) |

## Tests

```sh
go test ./...
```

The suite mocks process execution and HTTP (`internal/testsupport`), so it needs neither Docker nor network access.
`internal/commands/parity_test.go` checks the generated compose/config templates and the output of key scenarios
against the Python test-suite's approval files (copied into `internal/testsupport/testdata/python`).

## Differences from the Python CLI

- Only `localnet` and `goal` exist. The top-level `config` command isn't ported, but `localnet config` is.
  `--skip-version-check` is accepted (and ignored) for compatibility, since there's no version check.
- There is no `explore` (top-level or `localnet explore`), no `localnet codespace`, and no Gitpod/Codespaces
  endpoint handling. The post-start hint points to `algokit goal` / `algokit localnet console` rather than
  `algokit explore`.
- `localnet status` points to `algokit localnet config` instead of `algokit config container-engine` to change the
  engine, since the latter doesn't exist here.
- `localnet logs`: in Python, `-f` is declared as the *off* switch of `--follow/-f`. Here `-f` means `--follow`.
- `localnet config` without an engine shows a simple numbered prompt rather than an arrow-key picker.
- `localnet <subcommand> --help` and usage errors are handled without a container engine. Click runs the engine
  checks first.
- `goal`'s stdout and stderr are passed through unchanged as they're produced (so prompts without a trailing newline
  are shown). Python trims each line, adds a leading space and merges stderr into stdout.
- `HTTP Request: …` lines (health checks, Docker Hub version checks, status) are only shown with `-v`.
- The `goal_mount` directory is created before LocalNet starts. Otherwise Docker on Linux creates it owned by root,
  and `algokit goal` can't copy input files into it.
- An empty `XDG_CONFIG_HOME`/`XDG_STATE_HOME` is treated as unset (per the XDG spec), rather than as the current
  directory.
- Unhandled errors are reported with Go error names (e.g. `Unhandled PathError: …` rather than
  `Unhandled PermissionError: …`).
