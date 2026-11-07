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
                 [--config FILE]
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

## LocalNet JSON configuration

Load settings from a JSON file:

```sh
algokit localnet start --name foo --config config.json
```

For example, `config.json` can toggle developer mode and the experimental consensus protocol without building an image:

```json
{
  "devMode": true,
  "vFuture": true
}
```

- `devMode`: enables developer mode (`true` by default).
- `vFuture`: selects the `"future"` genesis consensus protocol when `true`; `false` uses algod's default protocol.
  Defaults to `false`. This is independent of developer mode and may enable experimental, unstable behavior.

Omitted toggles retain existing settings. Toggles are saved in `localnet-settings.json` alongside the LocalNet
configuration and survive subsequent starts and resets, including configuration regeneration. Explicit `--dev` or
`--no-dev` overrides JSON/saved `devMode`. Changing an existing network's toggles prompts before recreating it and
**clearing all chain state**; `--force` skips this confirmation. A new network needs no confirmation.

Unknown fields, invalid booleans (including `null`), and invalid JSON are rejected before changing LocalNet. Relative
configuration paths resolve from the current working directory; `--config-dir` separately controls where LocalNet
state is stored. At least one toggle or service selection must be provided.

### Using prebuilt images

Each service object accepts `image` as an alternative to building from `repo`/`ref`:

```json
{
  "algodSource": { "image": "algorand/algod:beta" },
  "indexerSource": { "image": "my-registry/indexer:dev" },
  "conduitSource": { "image": "algorandfoundation/conduit-localnet:latest" }
}
```

Use the same `--config config.json` command. An image can be a local tag, registry reference, or digest-pinned
reference. Image selection never builds from source; normal Compose cache/pull behavior applies when starting it.
`image` cannot be combined with `repo` or `ref` in the same object. Image references must be nonblank, with no
whitespace or `$` interpolation. The container engine validates the complete reference syntax.

Image selections are saved per LocalNet, survive resets/configuration regeneration, and can be mixed with source
builds for other services. `reset --update` pulls selected prebuilt images and rebuilds only source-based services.
Switching from a source to an image clears that service's saved repo/ref. Switching back to repo/ref starts from
the service's default source settings rather than restoring the previously selected source.

Custom images must satisfy LocalNet's existing runtime contracts: algod's startup/config mounts and KMD support,
Indexer's daemon/environment configuration, and Conduit's `localnet_algod` importer and configuration mount.

### Building services from source

Optionally include `algodSource`, `indexerSource`, or `conduitSource` objects to build services from remote branches,
tags, or full commit SHAs. Each service is independent and can be combined with either toggle above. For example:

```json
{
  "algodSource": {
    "repo": "https://github.com/algorand/go-algorand.git",
    "ref": "feature/my-change"
  },
  "indexerSource": {
    "repo": "https://github.com/algorand/indexer.git",
    "ref": "main"
  },
  "conduitSource": {
    "repo": "https://github.com/algorandfoundation/algokit-conduit-localnet-importer.git",
    "ref": "main"
  }
}
```

```sh
algokit localnet start --name foo --config config.json
```

For source builds, each service object accepts `repo` and `ref`, with at least one required. Omitted fields retain saved values, or use
these defaults on first use:

| Property | Default repository | Default ref |
| --- | --- | --- |
| `algodSource` | `algorand/go-algorand` | `master` |
| `indexerSource` | `algorand/indexer` | `main` |
| `conduitSource` | `algorandfoundation/algokit-conduit-localnet-importer` | `main` |

Set `repo` to select a fork. Blank values and unknown source fields are rejected. Conduit builds must register the
`localnet_algod` importer used by LocalNet's pipeline configuration; plain `algorand/conduit` does not include it.
The build validates this importer and fails with a diagnostic if it is missing.

Including a repo/ref selection in `--config` builds that service's image before starting LocalNet. Omitted services and
toggle-only configurations reuse their existing images. Builds run entirely in the configured container engine,
using embedded Dockerfiles and the selected checkouts' Go toolchain versions and runtime scripts. No host Git or Go
installation is needed. Repositories must be accessible from inside the build without host credentials;
private-repository authentication is not currently configured. Only build repositories you trust: their build
scripts run inside the container build.

Effective image/source selections are saved in `algod-source.json`, `indexer-source.json`, and `conduit-source.json` in
the LocalNet configuration directory. Starting again without `--config` reuses these images and does not
need the original JSON file:

```sh
algokit localnet start --name foo

# Rebuild using the file's current settings and remote branch tip:
algokit localnet start --name foo --config config.json
```

Ordinary `localnet reset` reuses the images; `localnet reset --update` rebuilds all saved source refs and pulls only
the other service images. A reset destroys chain state. As before, `reset --update` on a named instance prompts before
replacing its Compose configuration, but keeps the saved source selection and network toggles. Source-built LocalNets do not perform
default published-image version checks and never pull over source-built service images.

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
