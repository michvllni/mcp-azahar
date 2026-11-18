# AGENTS.md

Guidance for coding agents (and humans) working on mcp-azahar. Read
`docs/PLAN.md` (phased plan) and `docs/DECISIONS.md` (why the project is
shaped this way) first; `NOTES.md` has the current status, blockers and
findings.

## Documentation is mandatory
Every change that adds or alters behavior, commands, flags, tools,
dependencies, the dev container, or build/test steps **must update the
affected docs in the same commit**: at least `AGENTS.md` and `README.md`,
plus `NOTES.md` (findings, deviations, installed tools) and `docs/*.md`
where relevant. A change without its doc update is incomplete and must not
be committed.

## Structure
```
cmd/mcp-azahar/      thin Cobra CLI over internal/ops plus `serve` (MCP);
                     main.go holds `const version`, root.go global flags
                     and exit codes, commands.go/scripting.go the commands,
                     parity_test.go checks every command has an MCP tool
internal/            all behavior; CLI and MCP call the same functions
  rpc/               Azahar RPC client (protocol v2, docs/PROTOCOL.md)
    fakeserver/      in-process fake Azahar RPC server for tests
  session/           session files, host-wide lock, process liveness
  emu/               launch/attach/stop/status of Azahar (+ Xvfb), D9 rules
  capture/           screenshots via RPC (D4), 3DS-style composite
  symbols/           ELF symbols + DWARF types for read-symbol/wait-for
    testdata/        symbols.c, realworld/ (C + C++, DWARF 5 and 2) and
                     their committed ARM ELFs
  sequence/          YAML/JSON scripted tests (sequence command)
  ops/               every user-facing operation, shared by CLI and MCP
  server/            MCP server (official go-sdk, stdio): one tool per
                     CLI command, `Spec.CLI` names the counterpart
  version/           version string formatting; Commit/Date set via -ldflags
scripts/             check-version.sh (tag vs. version), check-spdx.sh,
                     build-testdata.sh (rebuilds the committed test ELFs),
                     azahar-source.sh (Azahar source tarball + license),
                     npm-packages.sh (build/publish the npm packages),
                     npm-bootstrap.sh (one-time npm trust setup)
packaging/image/     release container image (Dockerfile, NOTICE.md),
                     built by goreleaser dockers_v2
packaging/npm/       npm wrapper package mcp-azahar (bin/mcp-azahar.js runs
                     the binary of mcp-azahar-<os>-<cpu>)
.devcontainer/       Dockerfile, devcontainer.json, pinned install scripts
.github/workflows/   ci.yml (PRs and main), release.yml (v* tags)
.github/             issue templates, PR template, renovate.json5
.goreleaser.yaml     release builds   .golangci.yml  linter config
docs/PLAN.md         phased plan        docs/DECISIONS.md  design decisions
docs/PROTOCOL.md     Azahar RPC wire format and emulator facts (Phase 1)
testdata/fixture/    libctru test homebrew (main.c, build.sh) and smoke.yaml
NOTES.md             running log: status, blockers, installed-tools checklist
```

## Build, test, lint
```sh
go build ./...
go test ./...
golangci-lint run ./...
go run ./cmd/mcp-azahar version
```
Integration tests use the build tag `integration` and are skipped unless
their environment is set. Live RPC client test against a running Azahar
(app loaded, RPC enabled, single-screen layout):
```sh
AZAHAR_RPC_ADDR=127.0.0.1:45987 [AZAHAR_RPC_SCRATCH=0x...] \
  go test -tags integration -run TestLiveAzahar -v ./internal/rpc/
```
`AZAHAR_RPC_SCRATCH` is the hex address of 8 writable bytes the app does
not use; it enables the write and save/load checks.

Live launch test: starts the real Azahar headless through `emu.Manager`
(own Xvfb, test-generated minimal 3DSX), checks the one-per-host rule,
screenshots, save/load, and that `Stop` leaves nothing running. Needs UDP
port 45987 free:
```sh
MCPAZ_LIVE=1 [AZAHAR_BIN=...] go test -tags integration -run TestLiveLaunch -v ./internal/emu/
```
Fixture integration test: runs the CLI binary (the test binary itself,
`MCPAZ_TEST_MAIN=1`) against the real Azahar with the fixture homebrew:
launch, input, touch, screenshot pixels, save/load, logs, the one-per-host
rule, `testdata/fixture/smoke.yaml` and an MCP screenshot over stdio. The
fixture needs devkitPro, which is not in the dev container (HTTP 403): take
it from CI (`gh run download <run-id> -n fixture -D build`) or build it
with `testdata/fixture/build.sh` where devkitPro works:
```sh
MCPAZ_FIXTURE_DIR=$PWD/build go test -tags integration -run TestFixture -v ./cmd/mcp-azahar/
```
Run live tests of several packages with `-p 1` (one RPC port per host).
Live capture test against a running Azahar (single-screen layout):
`AZAHAR_RPC_ADDR=127.0.0.1:45987 go test -tags integration -run TestLiveCapture -v ./internal/capture/`.
The `internal/emu` unit tests run the test binary itself as a fake Azahar
(`MCPAZ_FAKE_AZAHAR=1`, modes in `fakeAzahar()`), so launch/stop logic is
tested with real processes but without the emulator.

Everything CI checks, runnable locally (all tools are in the dev container):
```sh
go mod tidy -diff && go vet ./... && go vet -tags integration ./...
go test -race -count=1 ./...
golangci-lint run ./...
actionlint
git ls-files -z '*.sh' | xargs -0 shellcheck
scripts/check-spdx.sh
goreleaser check && goreleaser build --snapshot --clean
```

## CI and releases
- `.github/workflows/ci.yml` runs on every pull request and push to `main`:
  build + race tests, golangci-lint, actionlint, shellcheck, SPDX headers,
  `goreleaser check` and a snapshot build for all platforms.
- `.github/workflows/release.yml` runs when a **GitHub release is
  published**. It first runs `scripts/check-version.sh -p <prerelease> <tag>`
  and **fails unless the tag is exactly `v` + `const version` in
  `cmd/mcp-azahar/main.go`** and the pre-release checkbox matches the
  version (SemVer suffix like `-rc.1` ⇔ pre-release). Then it tests and runs
  goreleaser, which uploads the archives into that release, keeping its
  notes and title (`.goreleaser.yaml` `release:`).
- The release job first attaches Azahar's source tarball (GPL, D10),
  then goreleaser pushes the image `ghcr.io/michvllni/mcp-azahar`
  (`<version>`, `latest` unless a pre-release) before uploading the
  archives. One-time, after the first release: set the ghcr package to
  public (package settings) and check it is linked to the repository;
  private packages break `docker pull` and the MCP Registry's OCI check.
  If the job fails after the push, re-run it (uploads use the existing
  release) rather than deleting the release, or delete the pushed tags
  too. The SBOM attestation and the push only run at a real release (CI
  builds a snapshot without them). CI job
  `image` builds it as a snapshot and launches the fixture inside it.
- npm: the release job then publishes `mcp-azahar-<os>-<cpu>` and the
  wrapper `mcp-azahar` with trusted publishing (`id-token: write`, no
  token; `-rc` versions get the dist-tag `next`). Before the first
  release the maintainer runs `scripts/npm-bootstrap.sh` once (npm login
  with 2FA): trust can only be set for existing packages and expires
  unless a publish follows within 2 days. Re-runs skip published versions.
  The `checks` CI job builds the packages, dry-runs the publish, installs
  the wrapper and runs it. `repository.url` must stay
  `git+https://github.com/michvllni/mcp-azahar.git` (npm checks it).
- Release process: bump `const version` (SemVer, no `v`) in a PR, merge,
  then create a GitHub release on `main` with a new tag `v<version>` and
  publish it. Do not push tags by hand. Only the maintainer releases. If
  the check fails, delete the release and its tag, fix, publish again.
  goreleaser cannot upload into immutable releases.
- Dependencies are updated by the **Renovate GitHub app**
  (`.github/renovate.json5`): Go modules, GitHub Actions (pinned to commit
  SHAs with the version in a comment), devcontainer features, and tool
  versions annotated with `# renovate: datasource=… depName=…` on the line
  above the version. Annotate every new pinned version the same way.
- Jobs `fixture` (builds `testdata/fixture` in the pinned
  `devkitpro/devkitarm` image, uploads it as artifact `fixture`) and
  `integration` (Debian trixie container, Azahar via
  `install-azahar.sh`, runs `TestFixture` and `TestLiveLaunch` with
  `-p 1`: live tests of different packages must not run in parallel,
  there is only one RPC port per host).

## Conventions
- Every source file starts with `// SPDX-License-Identifier: GPL-3.0-or-later`
  (or the equivalent comment syntax; `*.go *.sh *.yml *.yaml *.json
  Dockerfile` are enforced by `scripts/check-spdx.sh` in CI). Code ported
  from Azahar keeps its copyright notice plus a comment naming the source
  file and commit.
- All code lives in `internal/`; never implement a feature twice for CLI
  and MCP. New features go into `internal/ops` first; front ends only parse
  arguments and format output. Errors: `ops.ErrUsage` → exit 2,
  `ops.ErrCheckFailed` → exit 3, `session.ErrNotFound` → exit 4.
- Test binaries in `testdata/` are built from committed sources with
  `scripts/build-testdata.sh` (ARM toolchain in the dev container); commit
  source and binary together.
- Sequence steps map 1:1 to ops calls; a new CLI command that makes sense
  in a script also gets a sequence action, and every CLI command gets an
  MCP tool in `internal/server` (or an entry in `cliOnly` in
  `parity_test.go` with the reason). Tool descriptions state units,
  ranges and formats; MCP durations are milliseconds.
- `serve` owns stdout for JSON-RPC: nothing in ops or server may print.
- Every file an MCP tool reads or writes goes through `Server.path`
  (DECISIONS.md D11): direct arguments, sequence paths via
  `sequence.Options.CheckPath`, and derived names (split screenshots) via
  the `check` argument of `ops.SaveScreenshot`. Results stay bounded (see
  the limits in `internal/server`). A new CLI flag needs a tool property or an entry
  in `flagOnly` in `parity_test.go`.
- Input state held across commands lives in the session file (`Held`):
  Azahar replaces the whole input on every SetInput, and each CLI call is a
  separate process.
- `internal/rpc` mirrors `docs/PROTOCOL.md`: comments cite its sections.
  Behavior Azahar shows must be reproduced in `internal/rpc/fakeserver`
  (empty replies, dropped datagrams, LoadState closing and re-binding the
  port, empty save slots being accepted), so unit tests catch client
  mistakes without an emulator.
- Only idempotent RPC requests are resent; TakeScreenshot, SaveState and
  LoadState never are (`RequestType.Idempotent`). "Connection refused"
  counts as a lost attempt (the port is closed while Azahar restarts RPC).
- A failed LoadState leaves Azahar stopped behind a modal dialog that
  blocks all RPC: check the state file exists before calling LoadState.
- Never guess the Azahar RPC wire format: `docs/PROTOCOL.md` is the
  contract, derived from Azahar source with `path:line` references at a
  pinned commit. When bumping Azahar, re-check it against the new source.
  Pre-2127 Citra examples are outdated.
- Shell scripts: header with synopsis/description/options/example, a
  `usage` function for `-h` and bad arguments, `set -euo pipefail`.
- Commit after each phase. Do not push, tag, release or publish without
  asking the maintainer.
- Commit messages follow Conventional Commits (`feat:`, `fix:`, `docs:`,
  `build:`, `ci:`, `chore:`, `refactor:`, `test:` …); a local commit-msg
  hook rejects others.
- No Azure DevOps work item references in commits (GitHub repo).

## Dev container and tools
- Every tool is installed twice: live in the running container **and**
  recorded in `.devcontainer/` (Dockerfile ARG / install script). Add it to
  the checklist in `NOTES.md`.
- Base image: `devcontainers/base` trixie (Azahar 2127 needs glibc >= 2.39).
- Pinned versions: Azahar `AZAHAR_VERSION`/`AZAHAR_SHA256`/
  `AZAHAR_SOURCE_SHA256` as Dockerfile ARGs (bump by hand, all three
  together: digests of `azahar.AppImage` and
  `azahar-unified-source-<version>.tar.xz` of that release). CI, the
  release image and `scripts/azahar-source.sh` read them from there. After
  a bump, recompute the system libraries of `packaging/image/Dockerfile`
  (NOTES.md Phase 8a) and the Qt version in `packaging/image/NOTICE.md`.
- golangci-lint, goreleaser and actionlint: defaults of
  `.devcontainer/install-go-tools.sh` (used by the image and CI), plus the
  `version:` inputs in the workflows; Renovate keeps them in sync.
- `.devcontainer/install-azahar.sh` extracts the AppImage with `unsquashfs`
  into `/opt/azahar`; start Azahar via `$AZAHAR_BIN` (`/opt/azahar/AppRun`,
  which injects the bundled libstdc++).
- Run Azahar headless under Xvfb (`xvfb-run -a ...`), Mesa llvmpipe.

## Pitfalls
- Azahar 2127 AppImages need glibc >= 2.39 and a HarfBuzz newer than 6.0,
  hence the trixie base. Never run Azahar with a different glibc via
  `LD_LIBRARY_PATH`: Azahar spawns `sh`, which then crashes.
- Azahar ignores SIGINT/SIGTERM; SIGHUP stops it, SIGKILL as last resort.
- Azahar shows no first-run dialog with a fresh config dir.
- Azahar buffers its log file and flushes only on errors unless
  `instant_debug_log=true` (costs: per-entry flush, 100 MiB cap; see
  PROTOCOL.md §7); app debug output is Debug level. Both are in
  the seed config (`internal/emu/seed.go`, PROTOCOL.md §7); attached
  sessions need them set by hand to see app output live.
- RPC (see `docs/PROTOCOL.md`): binds `0.0.0.0:45987`, hard-coded, so one
  RPC server per network namespace; it only starts once an app is loaded
  and is recreated by LoadState; WriteMemory never reports failure (read
  back); the `\default=true` flag in `qt-config.ini` overrides a stored
  value.
- devkitPro's servers return 403 from some cloud ranges; the test fixture
  is built in CI with the `devkitpro/devkitarm` image.
- In the Coder workspace only `/home/vscode/volume` persists; anything
  installed elsewhere is gone after a restart unless the image has it.
- The Azahar RPC server is unauthenticated and listens on all interfaces
  (fixed port 45987); never make it more reachable than that.
- Screenshots come from Azahar's RPC (DECISIONS.md D4), not from X11.
- One RPC-enabled Azahar per host with Azahar 2127 (DECISIONS.md D9):
  never start a second one or reuse a port held by something else; keep
  the RPC endpoint in session data instead of assuming port 45987. All
  session-file changes and launches go through `emu.Manager`, which holds
  the store lock (`session.Store.Lock`).
- Azahar and Xvfb are started detached (own session/process group) and
  stopped with SIGHUP/SIGTERM, then SIGKILL, including group members that
  outlive the leader; liveness uses PID plus start time (`session.Alive`),
  so reused PIDs and zombies count as dead.
- Never trust an answering RPC server without the ownership check
  (`Manager.verifyOwner`): unknown owner = foreign.
- `launch` is Linux/macOS only (Azahar's portable mode uses the cwd there);
  on Windows only `attach` is supported.
- Session state lives in `$MCP_AZAHAR_STATE_DIR`, else
  `$XDG_STATE_HOME/mcp-azahar`, else `~/.local/state/mcp-azahar`; each
  launched session has `sessions/<name>.d/` with Azahar's portable `user/`
  dir, `azahar.out` and `xvfb.out`.
- Never file issues or comment on azahar-emu/azahar yourself, and do not
  commit drafts for upstream: hand them to the maintainer, who posts them.
- Symbols: only DWARF variables with a `DW_OP_addr` location are
  addressable; never index stack locals (they would shadow globals).
  Bitfield offsets differ between DWARF 2/3 and 4+ (NOTES.md Phase 5b);
  test symbol changes against both `realworld*.elf` fixtures.
