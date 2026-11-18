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
.github/             issue templates, PR template, renovate.json5
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
- `.github/workflows/release.yml` runs when a **GitHub release is
  published**. It first runs `scripts/check-version.sh -p <prerelease> <tag>`
  and **fails unless the tag is exactly `v` + `const version` in
  `cmd/mcp-azahar/main.go`** and the pre-release checkbox matches the
  version (SemVer suffix like `-rc.1` ⇔ pre-release). Then it tests and runs
  goreleaser, which uploads the archives into that release, keeping its
  notes and title (`.goreleaser.yaml` `release:`).
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
- No integration-test job yet: it needs the fixture (Phase 7).

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
- Base image: `devcontainers/base` trixie (Azahar 2127 needs glibc >= 2.39).
- Pinned versions: Azahar `AZAHAR_VERSION`/`AZAHAR_SHA256` as Dockerfile
  ARGs (bump by hand, both together: the digest is on the GitHub release
  asset). golangci-lint, goreleaser and actionlint in the defaults of
  `.devcontainer/install-go-tools.sh` (used by the image and CI), plus the
  `version:` inputs in the workflows; Renovate keeps them in sync.
- `.devcontainer/install-azahar.sh` extracts the AppImage with `unsquashfs`
  into `/opt/azahar`; start Azahar via `$AZAHAR_BIN` (`/opt/azahar/AppRun`,
  which injects the bundled libstdc++).
- `.devcontainer/patch-azahar-bookworm.sh` is a stopgap only for a
  container still on bookworm; afterwards start `/opt/azahar/usr/bin/azahar`
  directly. Delete it once the container runs trixie.
- Run Azahar headless under Xvfb (`xvfb-run -a ...`), Mesa llvmpipe.

## Pitfalls
- Azahar 2127 AppImages need glibc >= 2.39 and a HarfBuzz newer than 6.0; Debian
  bookworm cannot run them unpatched. Never fix this with a global
  `LD_LIBRARY_PATH`: Azahar spawns `sh`, which then crashes.
- Azahar ignores SIGTERM: stopping it needs SIGKILL after a grace period.
- Azahar shows no first-run dialog with a fresh config dir.
- devkitPro's servers return 403 from some cloud ranges; the test fixture
  is built in CI with the `devkitpro/devkitarm` image.
- In the Coder workspace only `/home/vscode/volume` persists; anything
  installed elsewhere is gone after a restart unless the image has it.
- The Azahar RPC server is unauthenticated: bind it to 127.0.0.1 only.
