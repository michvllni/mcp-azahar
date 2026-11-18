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
cmd/mcp-azahar/      thin Cobra front end (CLI; later also `serve` for MCP)
internal/            all behavior; CLI and MCP call the same functions
  version/           build metadata, set via -ldflags
.devcontainer/       Dockerfile, devcontainer.json, pinned install scripts
docs/PLAN.md         phased plan        docs/DECISIONS.md  design decisions
NOTES.md             running log: status, blockers, installed-tools checklist
```
Planned packages (see PLAN.md): `internal/rpc`, `internal/session`,
`internal/emu`, `internal/capture`, `internal/server`.

## Build, test, lint
```sh
go build ./...
go test ./...
golangci-lint run ./...
go run ./cmd/mcp-azahar version
```
Integration tests (later phases) use the build tag `integration`.

## Conventions
- Every source file starts with `// SPDX-License-Identifier: GPL-3.0-or-later`
  (or the equivalent comment syntax). Code ported from Azahar keeps its
  copyright notice plus a comment naming the source file and commit.
- All code lives in `internal/`; never implement a feature twice for CLI
  and MCP.
- Never guess the Azahar RPC wire format: it comes from Azahar source and is
  documented in `docs/PROTOCOL.md` (Phase 1). Pre-2127 Citra examples are
  outdated.
- Shell scripts: header with synopsis/description/options/example, a
  `usage` function for `-h` and bad arguments, `set -euo pipefail`.
- Commit after each phase. Do not push, tag, release or publish without
  asking the maintainer.
- No Azure DevOps work item references in commits (GitHub repo).

## Dev container and tools
- Every tool is installed twice: live in the running container **and**
  recorded in `.devcontainer/` (Dockerfile ARG / install script). Add it to
  the checklist in `NOTES.md`.
- Pinned versions: Azahar `AZAHAR_VERSION`/`AZAHAR_SHA256`,
  `GOLANGCI_LINT_VERSION`, `GORELEASER_VERSION` in `.devcontainer/Dockerfile`.
  Bump them there (and the script defaults) together.
- `.devcontainer/install-azahar.sh` extracts the AppImage with `unsquashfs`
  into `/opt/azahar`; start Azahar via `$AZAHAR_BIN` (`/opt/azahar/AppRun`),
  not `usr/bin/azahar` directly (AppRun injects the bundled libstdc++).
- Run Azahar headless under Xvfb (`xvfb-run -a ...`), Mesa llvmpipe.

## Pitfalls
- Azahar 2127 AppImages need glibc >= 2.39; Debian bookworm (2.36) cannot
  run them. See `NOTES.md` → Blockers.
- devkitPro's servers return 403 from some cloud ranges; the test fixture
  is built in CI with the `devkitpro/devkitarm` image.
- In the Coder workspace only `/home/vscode/volume` persists; anything
  installed elsewhere is gone after a restart unless the image has it.
- The Azahar RPC server is unauthenticated: bind it to 127.0.0.1 only.
