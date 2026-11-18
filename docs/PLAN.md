# PLAN.md — mcp-azahar

## Goal
Build `mcp-azahar` (github.com/michvllni/mcp-azahar), an unofficial Go tool
that lets AI agents and humans test 3DS homebrew in the Azahar emulator:
launch apps, send input, take screenshots, read/write memory, use save
states, read logs, and assert on program state. One binary, two front ends:
- a CLI (Cobra) for humans, shell scripts, CI, and agents using bash
- an MCP server (`mcp-azahar serve`) for MCP clients
Not affiliated with the Azahar project — say so in the README.

Read `docs/DECISIONS.md` before starting: it explains why the project is
shaped this way. Don't revisit those decisions without asking the human.

## Repository facts
- Repo: https://github.com/michvllni/mcp-azahar
- Go module path: `github.com/michvllni/mcp-azahar`
- Binary name: `mcp-azahar`
- License: GPL-3.0-or-later (same family as Azahar)
- Description: "Unofficial MCP server and CLI for the Azahar 3DS emulator:
  automate testing of 3DS homebrew, locally or in CI."

## Ground rules for the agent
- Start by inspecting what already exists in the repo (README, LICENSE,
  .gitignore, .devcontainer/). Keep and extend existing files; don't
  overwrite them wholesale. If LICENSE is missing or not GPL-3.0, stop
  and ask.
- Work in phases; commit after each phase with a clear message. Do not push,
  create releases, or publish packages without asking the human first.
- Every source file gets `// SPDX-License-Identifier: GPL-3.0-or-later`
  (or the comment syntax of its language). When porting code from Azahar,
  keep its original copyright notice and note the source file + commit in
  a comment.
- Never guess the RPC wire format. Derive it from Azahar source (Phase 1)
  and document it. Old Citra examples are outdated: Azahar 2127 made
  breaking RPC changes (#2631, #2640, #2633) and added savestate (#2730)
  and input injection (#2731) commands.
- All behavior lives in `internal/` packages. CLI and MCP are thin front
  ends over the same code; never implement a feature twice.
- Install every tool you need yourself (Phase 0). If something can't be
  installed in the container, stop and report what's missing and why.
- Keep a running `NOTES.md` with findings, open questions, and deviations
  from this plan. Create a `CLAUDE.md` with build/test commands and
  conventions once they're settled.

## Environment
You are running inside the project's dev container (Go, Debian-based) with
the repo already checked out as the working directory. The non-root user
has passwordless sudo. Azahar will run inside this container under Xvfb
with Mesa software rendering; no host display is needed.

You cannot rebuild the container yourself. So every tool you install must
be done twice:
1. installed live in the running container (so you can keep working), and
2. recorded in `.devcontainer/` (Dockerfile / devcontainer.json /
   post-create script) so the next rebuild reproduces it.
Keep a checklist in NOTES.md of everything installed and where it's
recorded. At the end of Phase 0, tell the human to rebuild the container
and verify nothing is missing before continuing.

## Phase 0 — Tooling and skeleton
1. Inspect `.devcontainer/` and keep everything already configured
   (base image, features such as Claude Code, mounts, settings). If it
   uses `image:` only, switch to a `Dockerfile` based on the same image.
2. `go mod init github.com/michvllni/mcp-azahar` (if not done), add Cobra.
3. `sudo apt-get install`: xvfb, xauth, mesa-utils, libgl1-mesa-dri,
   libegl1, libxkbcommon-x11-0, libxcb-cursor0, libxcb-icccm4,
   libxcb-image0, libxcb-keysyms1, libxcb-randr0, libxcb-render-util0,
   libxcb-shape0, libxcb-xinerama0, libxcb-xkb1, libfontconfig1,
   libdbus-1-3, x11-utils, imagemagick, file, jq, unzip, curl, python3.
4. Download the latest Azahar 2127.x Linux AppImage from
   github.com/azahar-emu/azahar/releases (must include #2730/#2731; prefer
   stable, else latest RC). Extract with `--appimage-extract` (no FUSE in
   containers) into `/opt/azahar`; pin the version in the Dockerfile via a
   build arg. Run `ldd` on the binary and install missing libs until it
   starts under `xvfb-run`.
5. Install devkitPro (devkitARM + libctru + 3dstools) via dkp-pacman for
   building the test fixture. If that fails in this container, record the
   failure in NOTES.md and plan to build the fixture in CI with the
   `devkitpro/devkitarm` image instead.
6. Install golangci-lint and goreleaser (pinned versions).
7. Commit the `.devcontainer/` changes, then stop and ask the human to
   rebuild the container before Phase 1.

## Phase 1 — Protocol research (no code yet)
1. Shallow-clone azahar-emu/azahar at the chosen tag (outside the repo,
   e.g. /tmp/azahar).
2. Read `src/core/rpc/` (packet layout, request types, max payload sizes,
   reply semantics, versioning, transport, port, bind address) and the
   Python client(s) under `dist/` (including the free-camera script).
3. Find how the RPC server is enabled (config key in qt-config.ini and/or
   CLI flag), the config/log file locations, the screenshot hotkey/path,
   and whether pause, frame advance, reset, or app load exist over RPC.
4. Check whether a first-run setup dialog blocks homebrew launch, and how
   to pre-seed config to skip it.
5. Write `docs/PROTOCOL.md` with exact byte layouts and source references
   (file + line + commit). This is the contract for Phase 2.

## Phase 2 — RPC client (`internal/rpc`)
- Typed Go client for every request found in Phase 1: read/write memory
  (auto-chunk to the max payload), set input (buttons, HOME/POWER, touch,
  circle pad), save/load state, plus anything else available.
- Request IDs, timeouts, retries for UDP loss, context cancellation.
- Unit tests against a fake RPC server implementing PROTOCOL.md, including
  chunking edge cases and timeouts.

## Phase 3 — Sessions and process manager (`internal/session`, `internal/emu`)
Each CLI call is a separate process, so emulator lifetime must not depend
on the caller.
- `launch` starts Xvfb (free display, or reuse `$DISPLAY`) and Azahar
  detached, with an isolated user dir/config per session (RPC enabled,
  single-screen layout; see `docs/PROTOCOL.md` §7 for the seed config).
  Azahar's RPC port is fixed (45987, all interfaces), so per DECISIONS.md
  D9 `launch` refuses to start while that port is in use, naming the
  session (or "another process") that holds it. `launch` waits until RPC
  answers, then writes a session file:
  `$XDG_STATE_HOME/mcp-azahar/sessions/<name>.json` (PIDs, display, RPC
  endpoint (address + transport), user dir, log path, app path, ELF path).
- Every other command attaches via the session file. Named sessions via
  `--session <name>` (default `default`); with Azahar 2127 only one of them
  can have a running Azahar at a time (D9).
- `attach --port N [--elf path]` registers an Azahar the user started by
  hand (RPC only; screenshots work too, since they use RPC).
- `stop` terminates processes and removes the session; detect and clean
  up stale sessions (dead PIDs) automatically.
- File locking around session files to avoid races.

## Phase 4 — Screenshots (`internal/capture`)
Changed after Phase 1 (DECISIONS.md D4): screenshots use Azahar's RPC.
- Interface `Capturer` with backend `rpc`: `TakeScreenshot` +
  `ReadScreenshot` from `internal/rpc`; top screen = normal capture,
  bottom screen = `SecondaryWindow` flag, `both` = two captures. Sessions
  pin `layout_option=1`; report the actual image size (attached instances
  may use another layout). Optional resolution scale. PNG out.
- Map `Unsupported`/`Busy`/`Timeout` results to clear errors (software
  renderer, concurrent capture, emulation not rendering).

## Phase 5 — CLI (`cmd/mcp-azahar`, Cobra)
Build and test the CLI before the MCP layer. Commands:
- `launch <app.3dsx|.cia> [--elf app.elf]`, `attach`, `stop`, `status`,
  `sessions` (list), `version`
- `press <buttons...> [--hold 100ms]`, `hold <buttons...>`, `release`,
  `touch <x> <y> [--hold]`, `circle-pad <x> <y> [--hold]`,
  `sequence <file|->` (YAML/JSON steps: press, touch, wait, screenshot,
  assert)
- `screenshot [--screen top|bottom|both] [-o file.png]`
- `read <addr> <len>`, `write <addr> <hex>`, `read-symbol <name> [--len|--type]`,
  `symbols [--filter]`, `wait-for <addr|symbol> <value> [--timeout]`
- `state save|load <slot|path>`
- `logs [--tail N] [--contains s] [--follow]`
- Global flags: `--session`, `--json` (machine-readable output for agents
  and scripts), `--timeout`. Meaningful exit codes (e.g. non-zero on
  `wait-for` timeout or failed `sequence` assertion), so it works in CI.
- `sequence` is the scripted-test primitive: one file describes a full
  test run, usable without any AI involved.

## Phase 6 — MCP server (`mcp-azahar serve`, `internal/server`)
Use the official Go SDK (github.com/modelcontextprotocol/go-sdk, latest
stable; check its docs for current API). stdio transport.
- Same session mechanism as the CLI, so a human can watch or poke the same
  instance via the CLI while an agent drives it via MCP.
- Tools mirror the CLI 1:1 and call the same functions:
  `azahar_launch`, `azahar_attach`, `azahar_stop`, `azahar_status`,
  `azahar_press`, `azahar_hold`, `azahar_release`, `azahar_touch`,
  `azahar_circle_pad`, `azahar_sequence`, `azahar_screenshot` (returns MCP
  image content), `azahar_read_memory`, `azahar_write_memory`,
  `azahar_read_symbol`, `azahar_symbols`, `azahar_wait_for`,
  `azahar_save_state`, `azahar_load_state`, `azahar_logs`.
- Good tool descriptions matter: units, hex address format, button names,
  screen coordinate ranges, session semantics.
- Add a test that every MCP tool has a CLI counterpart (and vice versa) so
  the two never drift.

## Phase 7 — Test fixture + integration tests
- `testdata/fixture/`: tiny libctru homebrew that (a) keeps a `test_state`
  struct (magic, frame counter, last buttons, touch x/y) as a global
  symbol, (b) draws something deterministic per screen, (c) prints to
  stderr/debug output. Build `.3dsx` + `.elf`. GPL-3.0-or-later like the
  rest of the repo.
- Integration tests (build tag `integration`), run through the CLI binary:
  launch fixture, press A, assert via `read-symbol` that `last_buttons`
  changed; touch and assert coordinates; screenshot and check
  dimensions/non-blank; save state, mutate, load state, assert restored;
  logs contain the fixture's output; a second `launch` in another named
  session fails with a clear error while the first runs (parallel
  sessions once D9 allows them).
- One `testdata/fixture/smoke.yaml` sequence that `mcp-azahar sequence`
  runs green — the reference example for users.
- A short MCP smoke test: start `serve`, call tools over stdio, verify a
  screenshot image comes back.

## Phase 8 — Docs, release, CI
- README: what it is, unofficial disclaimer, Azahar version requirement,
  install (`go install github.com/michvllni/mcp-azahar/cmd/mcp-azahar@latest`
  and release binaries), CLI quickstart, sequence file format, MCP setup
  for Claude Code (`claude mcp add ...`) and Claude Desktop (JSON), a note
  that agents can also use the CLI directly via bash, license section
  (GPL-3.0-or-later). Security note: RPC is unauthenticated and Azahar
  2127 binds it to all interfaces on a fixed port, so a running session is
  reachable from the network; recommend a firewall or isolated container
  (D9) and update the note once upstream allows binding to 127.0.0.1.
- goreleaser for linux/amd64+arm64, darwin, windows (`launch` with Xvfb
  is Linux-first; `attach` and screenshots via RPC work everywhere;
  document it). Include LICENSE in archives.
- GitHub Actions: lint + unit tests on every push; integration tests in a
  job with Xvfb + Azahar + fixture built in the devkitarm image.

## Definition of done
`go test ./...` passes, the integration suite passes in the devcontainer,
`mcp-azahar sequence testdata/fixture/smoke.yaml` exits 0, and Claude Code
connected via `serve` can launch the fixture, press buttons, see both
screens, and verify state via `read_symbol`.

## Out of scope (note in NOTES.md, don't build)
Upstream Azahar changes (e.g. a configurable RPC port and bind address) —
draft a feature request text for the maintainer instead (not committed);
the maintainer files it.
