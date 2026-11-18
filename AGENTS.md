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
cmd/mcp-azahar/      thin Cobra front end (CLI; later also `serve` for MCP);
                     main.go holds `const version`, the release version
internal/            all behavior; CLI and MCP call the same functions
  version/           version string formatting; Commit/Date set via -ldflags
scripts/             check-version.sh (tag vs. version), check-spdx.sh
.devcontainer/       Dockerfile, devcontainer.json, pinned install scripts
.github/workflows/   ci.yml (PRs and main), release.yml (v* tags)
.github/             issue templates, PR template, dependabot.yml
.goreleaser.yaml     release builds   .golangci.yml  linter config
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

Everything CI checks, runnable locally (all tools are in the dev container):
```sh
go mod tidy -diff && go vet ./... && go test -race -count=1 ./...
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
- `.github/workflows/release.yml` runs on `v*` tags. It first runs
  `scripts/check-version.sh "$GITHUB_REF_NAME"` and **fails unless the tag
  is exactly `v` + `const version` in `cmd/mcp-azahar/main.go`**, then tests,
  then publishes with goreleaser (goreleaser repeats the check as a hook).
- Release process: bump `const version` (SemVer, no `v`) in a PR, merge,
  then tag that commit `v<version>` and push the tag. Only the maintainer
  tags and releases. A SemVer pre-release suffix (`-rc.1`) makes a GitHub
  pre-release.
- Actions are pinned to commit SHAs with the version in a comment;
  Dependabot updates them and Go modules weekly. Tool versions in the
  workflows (golangci-lint, goreleaser) must match the Dockerfile ARGs.
- No integration-test job yet: it needs a working Azahar runtime and the
  fixture (Phase 0 glibc blocker, Phase 7).

## Conventions
- Every source file starts with `// SPDX-License-Identifier: GPL-3.0-or-later`
  (or the equivalent comment syntax; `*.go *.sh *.yml *.yaml *.json
  Dockerfile` are enforced by `scripts/check-spdx.sh` in CI). Code ported
  from Azahar keeps its copyright notice plus a comment naming the source
  file and commit.
- All code lives in `internal/`; never implement a feature twice for CLI
  and MCP.
- Never guess the Azahar RPC wire format: it comes from Azahar source and is
  documented in `docs/PROTOCOL.md` (Phase 1). Pre-2127 Citra examples are
  outdated.
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
- Pinned versions: Azahar `AZAHAR_VERSION`/`AZAHAR_SHA256`,
  `GOLANGCI_LINT_VERSION`, `GORELEASER_VERSION`, `ACTIONLINT_VERSION` in
  `.devcontainer/Dockerfile`. Bump them there, in the script defaults and
  in the workflows together.
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
