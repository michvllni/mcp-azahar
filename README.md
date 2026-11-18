# mcp-azahar
Unofficial MCP server and CLI for the Azahar 3DS emulator: automate testing of 3DS homebrew, locally or in CI.

> **Unofficial.** This project is not affiliated with or endorsed by the
> [Azahar](https://github.com/azahar-emu/azahar) project.

**Status:** feature-complete for v0.1 ([docs/PLAN.md](docs/PLAN.md) phases
0–8): CLI, MCP server, scripted tests, integration tests against Azahar in CI,
and release packaging (binaries, container image, npm, MCP Registry). The first
release is pending. Current status and open questions: [NOTES.md](NOTES.md). The Azahar RPC protocol as used by this tool is
documented in [docs/PROTOCOL.md](docs/PROTOCOL.md).

## What it does
One Go binary, two front ends over the same code:
- a CLI for humans, shell scripts, CI and agents using bash
- an MCP server (`mcp-azahar serve`) for MCP clients

Both drive Azahar through its built-in RPC server: launch homebrew, send
input, take screenshots, read/write memory, use save states, read logs and
assert on program state. Design rationale: [docs/DECISIONS.md](docs/DECISIONS.md).

## Requirements
- Azahar **2127.0** or newer (RPC input injection and save states).
  The dev container pins 2127.0-rc3.
- Linux with Xvfb for headless `launch` (macOS: launch on the desktop; Windows:
  `attach` only, see [NOTES.md](NOTES.md)).
- Go 1.27 to build from source.

## Install
- **Container image** (Linux/amd64; includes Azahar, Xvfb and Mesa, nothing
  else to install):
  ```sh
  docker run -i --rm --user "$(id -u):$(id -g)" -v "$PWD:/work" ghcr.io/michvllni/mcp-azahar
  ```
  starts the MCP server over stdio with your project as `/work` (`--user`
  keeps files it writes yours). A session lives as long as its container, so
  run several CLI commands in one container:
  ```sh
  docker run --rm --user "$(id -u):$(id -g)" -v "$PWD:/work" --entrypoint sh \
    ghcr.io/michvllni/mcp-azahar -euc 'mcp-azahar launch app.3dsx --elf app.elf
      mcp-azahar sequence smoke.yaml; mcp-azahar stop'
  ```
  or keep one running (`docker run -d ... --entrypoint sleep ... infinity`) and
  use `docker exec`. Do not publish UDP port 45987 (`-p`) or use
  `--network host`: Azahar's RPC server is unauthenticated. Run one container
  per parallel session.
- **npm** (Linux, macOS, Windows; x64 and arm64; needs Node 18+):
  `npx -y mcp-azahar` starts the MCP server (no arguments means `serve`), and
  `npx -y mcp-azahar <command>` runs the CLI. MCP client configuration:
  `{"command": "npx", "args": ["-y", "mcp-azahar"]}`. You still need Azahar
  (and Xvfb for headless `launch`) on the host.
- **MCP Registry**: published as `io.github.michvllni/mcp-azahar` (npm and
  container packages) from the first release on, so registry-aware clients
  can install it by name.
- **Release binaries**: archives for Linux, macOS and Windows (amd64, arm64)
  on the [releases page](https://github.com/michvllni/mcp-azahar/releases).
- **Go**: `go install github.com/michvllni/mcp-azahar/cmd/mcp-azahar@latest`.

## CLI quickstart
```sh
go install github.com/michvllni/mcp-azahar/cmd/mcp-azahar@latest
export AZAHAR_BIN=/path/to/azahar   # or put azahar in PATH

mcp-azahar launch game.3dsx          # headless on its own Xvfb, waits for RPC
mcp-azahar press A                   # press briefly (100 ms), --for 500ms for longer
mcp-azahar hold L; mcp-azahar touch 160 120; mcp-azahar release
mcp-azahar circle-pad 154 0 --for 300ms
mcp-azahar screenshot --screen both -o shot.png
mcp-azahar read 0x00100000 16 --format dump
mcp-azahar write 0x00101000 'de ad be ef'   # verified by reading back
mcp-azahar state save 1; mcp-azahar state load 1
mcp-azahar logs --tail 20 --contains Error   # -f to follow, --from <offset> to resume
mcp-azahar stop
```
- **Sessions:** every command acts on a session: `--session NAME`, or
  `$MCP_AZAHAR_SESSION`, default `default`. `sessions` lists them, `status`
  shows one, and `attach --address host:port` registers an Azahar you started
  yourself (RPC enabled, app loaded). With Azahar 2127 only one RPC-enabled
  Azahar can run per host (DECISIONS.md D9).
- **Input:** `hold`, `touch --hold` and `circle-pad --hold` stay until
  `release`; `press`, and `touch`/`circle-pad` without `--hold`, apply on top
  of the held input and then return to it.
- **Output:** `--json` prints machine-readable output with stable field
  names; arrays are never null. It cannot be combined with `read --format raw`.
  `logs -f --json` prints one `{"line": ...}` object per line. `--timeout 30s`
  bounds a command; `logs -f` also ends when the session's emulator stops.
- **Exit codes:** 0 ok, 1 error, 2 usage error, 3 check failed, 4 no such
  session or emulator not running.
- **App output:** text your homebrew passes to `svcOutputDebugString` shows
  up in `logs` (launched sessions enable it; for attach, set
  `[Debugging] instant_debug_log=true` and
  `[Miscellaneous] log_filter=*:Info Debug.Emulated:Debug` in Azahar's config).
- **State files:** they live in `$MCP_AZAHAR_STATE_DIR`, else
  `$XDG_STATE_HOME/mcp-azahar`. A session's directory keeps Azahar's portable
  user dir (config, log, save states).

## Scripted tests and program state
Build your homebrew with debug info (`-g`) and pass its ELF to `launch --elf`
(or `attach --elf`). Then read variables by name instead of by address:

```sh
mcp-azahar symbols --filter state
mcp-azahar read-symbol test_state                 # whole struct as JSON-like text
mcp-azahar read-symbol test_state.last_buttons --json
mcp-azahar read-symbol 0x00101000 --type s16      # raw address with a type
mcp-azahar read-symbol flags.ready                 # bitfields decode too
mcp-azahar read-symbol ns::counter                # C++: ns::name, Class::member
mcp-azahar read-symbol update::calls              # static local of update() (K::m::calls in a method)
mcp-azahar read-symbol menu.c:selected            # a static whose name is used elsewhere
mcp-azahar wait-for test_state.frame 60 --op ge --timeout 5s   # exit 3 on timeout
```

Values decode by C type: integers, floats, bools, bitfields, enums (by name),
pointers (as their address), plain `char` arrays as text, structs and arrays as
JSON. Only variables with a static address (globals, statics) can be read;
locals on the stack cannot.

A **sequence** file describes a whole test run (YAML or JSON) and works without
any AI involved, e.g. in CI:

```yaml
name: smoke
elf: build/app.elf             # relative to this file; optional
steps:
  - press: A
  - press: [L, R]
    for: 300ms
  - hold: B
  - release: all
  - touch: {x: 160, y: 120}
  - circle_pad: {x: 154, y: 0, for: 200ms}
  - wait: 500ms
  - wait_for: {target: test_state.frame, op: ge, value: 60, timeout: 5s}
  - assert: {target: test_state.last_buttons, value: 0}
  - assert: {target: "0x00101000", type: u32, op: ne, value: 0}
  - write: {address: "0x00102000", bytes: "de ad be ef"}   # memory your app does not use
  - screenshot: {screen: both, output: out/smoke.png}
  - save_state: 1              # waits until Azahar has written the file
  - load_state: 1
  - log_contains: {text: "ready", timeout: 5s}
```

`mcp-azahar sequence smoke.yaml` stops at the first failing step: exit 0 if all
pass, 3 if an `assert`/`wait_for`/`log_contains` check fails, 2 for an invalid
file, 1 for other errors. The whole file is checked (buttons, ranges, addresses,
paths, and every target and value against the ELF) before the first step runs. `--json` prints a report per step with
`error_kind` (`check` or `error`) and `duration_ms`. Comparisons (`op`): eq, ne,
lt, le, gt, ge, and `changed` (no value) for `wait_for`; enums compare by name
or number. `log_contains` only matches lines logged after the run started, and
each match moves past its line.

## MCP server
`mcp-azahar serve` speaks the Model Context Protocol over stdio. Tools only read
and write files inside the server's working directory unless you allow more
with `--allow-dir` (see below), so point it at your project.

**Claude Code** (run in your project directory; `--scope project` shares it via
`.mcp.json`):

```sh
claude mcp add azahar -- npx -y mcp-azahar          # npm
claude mcp add azahar -- mcp-azahar serve           # installed binary
claude mcp add azahar -- docker run -i --rm --user "$(id -u):$(id -g)" \
  -v "$PWD:/work" ghcr.io/michvllni/mcp-azahar     # container, Azahar included
```

**Claude Desktop** (`claude_desktop_config.json`: macOS
`~/Library/Application Support/Claude/`, Windows `%APPDATA%\Claude\`). Desktop
starts servers in no particular directory, so name the project:

```json
{"mcpServers": {"azahar": {
  "command": "npx",
  "args": ["-y", "mcp-azahar", "serve", "--allow-dir", "/path/to/your/project"]
}}}
```

Other clients take the same command and arguments. Agents with a shell can also
skip MCP and call the CLI directly (`mcp-azahar press A --json`): the commands
are the same, the output is cheaper in context, and sessions are shared, so a
human can watch or poke the instance an agent drives.

Every tool mirrors a CLI command and calls the same code, so an agent and a
human with the CLI share sessions (`--session` or `$MCP_AZAHAR_SESSION` sets
the default session; each tool also takes `session`):

| Tool | CLI | Tool | CLI |
|---|---|---|---|
| `azahar_launch` | `launch` | `azahar_screenshot` (PNG image content) | `screenshot` |
| `azahar_attach` | `attach` | `azahar_save_state` | `state save` |
| `azahar_stop` | `stop` | `azahar_load_state` | `state load` |
| `azahar_status` | `status` | `azahar_read_memory` | `read` |
| `azahar_sessions` | `sessions` | `azahar_write_memory` | `write` |
| `azahar_press` | `press` | `azahar_logs` | `logs` |
| `azahar_hold` | `hold` | `azahar_symbols` | `symbols` |
| `azahar_release` | `release` | `azahar_read_symbol` | `read-symbol` |
| `azahar_touch` | `touch` | `azahar_wait_for` | `wait-for` |
| `azahar_circle_pad` | `circle-pad` | `azahar_sequence` | `sequence` |

Durations are in milliseconds (`for_ms`, `timeout_ms`). Errors come back as
tool errors whose text starts with their kind: `invalid arguments`, `check
failed` (an assert or wait that did not hold), or `no running session`.
`azahar_sequence` takes a `file` or an inline `script`; its file paths must be
relative and stay inside the sequence's directory. `azahar_screenshot` can also
save the PNG (`output`).

Files (DECISIONS.md D11): tools only read and write files (app, ELF, log,
sequence and the paths inside it, screenshot outputs) inside the server's
working directory, or the directories given with `serve --allow-dir DIR`
(repeatable); paths are checked after following symlinks, and dangling
symlinks are not written through. Relative paths are relative to the first
allowed directory. Results are capped to fit a model's context:
`azahar_read_memory` reads at most 4096 bytes, `azahar_read_symbol` and
`azahar_wait_for` values at most 16 KiB,
`azahar_logs` returns the last 200 lines by default (`tail` up to 1000),
`azahar_symbols` at most 500 entries (`filter` narrows), and screenshots use
scale 1-4 (0: Azahar's internal resolution). `serve` ignores `--timeout`.

## Development
The repository ships a dev container (`.devcontainer/`, Debian 13 trixie)
with Go, Xvfb, Mesa software rendering, Azahar extracted to `/opt/azahar`,
golangci-lint, goreleaser, actionlint and shellcheck. Open the repo in it, then:

```sh
go build ./...
go test ./...
golangci-lint run ./...
go run ./cmd/mcp-azahar version
```

Integration tests run the CLI against the real Azahar with a small test
homebrew, `testdata/fixture` (libctru; `build.sh` needs devkitPro, CI builds it
in the `devkitpro/devkitarm` image). `testdata/fixture/smoke.yaml` is a
complete example sequence. With the fixture in `build/`:

```sh
MCPAZ_FIXTURE_DIR=$PWD/build go test -tags integration -run TestFixture -v ./cmd/mcp-azahar/
```

CI (`.github/workflows/ci.yml`) runs tests, linters, a cross-platform snapshot
build and the integration tests on every pull request; [AGENTS.md](AGENTS.md#build-test-lint)
lists the same checks for running locally. Dependencies, actions and tool
versions are kept current by [Renovate](https://docs.renovatebot.com/)
(`.github/renovate.json5`).

Tool versions are pinned in `.devcontainer/Dockerfile`; the install scripts
in `.devcontainer/` can also be run by hand (`-h` for help). Contributor and
agent conventions: [AGENTS.md](AGENTS.md).

## Releases
Releases start in the GitHub UI: after the version bump in
`cmd/mcp-azahar/main.go` (`const version = "X.Y.Z"`) is merged to `main`,
create a new release with a new tag `vX.Y.Z` and publish it. The release
workflow then builds Linux, macOS and Windows binaries (amd64 and arm64)
with goreleaser and attaches them to that release, keeping its title and
notes. It fails without uploading anything if
- the tag is not exactly `v` + the version in `main.go`, or
- the "Set as a pre-release" checkbox does not match the version
  (`X.Y.Z-rc.1` must be a pre-release, `X.Y.Z` must not).

In that case delete the release and its tag, fix the cause and publish
again. `mcp-azahar version` (or `--version`) prints the version, commit and
build date. No release has been published yet.

A release also attaches Azahar's source tarball (GPL), pushes the container
image `ghcr.io/michvllni/mcp-azahar` (`X.Y.Z`; `latest` only for releases),
publishes the npm packages (pre-releases under the dist-tag `next`) with
provenance, and finally the MCP Registry entry (releases only). Maintainer
steps for the first release: run `scripts/npm-bootstrap.sh` (npm trusted
publishing setup, at most 2 days before), publish the release, make the
ghcr package public, then re-run the failed `MCP Registry` job. Details:
[AGENTS.md](AGENTS.md#ci-and-releases).

## Contributing
Bug reports and feature requests use the issue forms; emulator bugs belong
to [Azahar](https://github.com/azahar-emu/azahar/issues). Pull requests use
Conventional Commit messages and must update the docs (see
[AGENTS.md](AGENTS.md)).

## Security
Azahar's RPC server is unauthenticated and, as of Azahar 2127, always
listens on UDP port 45987 on **all interfaces** (not configurable). While
an Azahar started by mcp-azahar runs, anyone who can reach that port can
control the emulator: read and write memory, send input, save and load
states, take screenshots. Block the port with a firewall or run in an
isolated container, and never on an untrusted network. Because the port
is fixed, only one RPC-enabled Azahar can run per host
([DECISIONS.md D9](docs/DECISIONS.md)); a configurable port and bind
address is requested upstream
([azahar-emu/azahar#2829](https://github.com/azahar-emu/azahar/issues/2829)).
The container image keeps the port inside the container's network
namespace: do not publish it (`-p`) or use `--network host`.

The MCP server treats the model's arguments as untrusted: its tools only read
and write files inside the allowed directories (`serve --allow-dir`, default
the working directory; [DECISIONS.md D11](docs/DECISIONS.md)), and results are
size-capped. Report vulnerabilities privately, see [SECURITY.md](SECURITY.md).

## License
GPL-3.0-or-later. See [LICENSE](LICENSE). The container image also contains
Azahar (GPL-3.0, its source tarball is attached to each release), Qt
(LGPL-3.0) and Debian packages; see its `/usr/share/doc/mcp-azahar/NOTICE.md`.
