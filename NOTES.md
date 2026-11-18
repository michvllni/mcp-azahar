# NOTES.md — running log

Findings, open questions, and deviations from `docs/PLAN.md`. Newest phase
at the top of each section.

## Status
- Phase 0 (tooling and skeleton): done except for the two items under
  "Blockers". Waiting for a decision from the maintainer and a container
  rebuild before Phase 1.

## Blockers

### glibc: Azahar AppImage does not run on Debian bookworm
- Azahar 2127.0-rc3's AppImage (and the bundled Qt 6, SDL2, libsystemd …)
  requires `GLIBC_2.38`/`GLIBC_2.39`. The container is Debian 12 bookworm
  with glibc 2.36. `ldd /opt/azahar/usr/bin/azahar` shows no missing
  libraries any more, only these glibc symbol versions.
  `libstdc++` (`GLIBCXX_3.4.32`, `CXXABI_1.3.15`) is *not* a problem: the
  AppImage's `checkrt` hook (`/opt/azahar/AppRun`) puts its bundled
  libstdc++ first in `LD_LIBRARY_PATH`. glibc cannot be bundled that way.
- Not yet verified by actually starting Azahar: running a downloaded binary
  was blocked by the agent's permission policy in this session. The ldd
  output makes the failure certain on bookworm, though.
- Options (needs maintainer decision, `docs/PLAN.md` says keep the base image):
  1. **Switch the base to `mcr.microsoft.com/devcontainers/base:2.2.5-trixie`**
     (Debian 13, glibc 2.41). Recommended: smallest change, same image
     family and features. In the Dockerfile, `libasound2` becomes
     `libasound2t64`; other package names should be checked on rebuild.
  2. Build Azahar from source on bookworm (slow, large toolchain, diverges
     from the released binary users run).
  3. Ship a newer glibc next to Azahar and start it via that `ld-linux`
     (fragile, Mesa/GL drivers come from the host glibc).
- The Coder template, not this repo, decides which image the workspace runs
  on today (see "Dev container" below), so option 1 also needs a template
  change or the template must build from `.devcontainer/`.

### devkitPro: download server returns HTTP 403
- `curl https://apt.devkitpro.org/install-devkitpro-pacman` → 403 from this
  workspace (devkitPro is known to block some cloud/CI address ranges).
  Not retried with other user agents or mirrors.
- Per PLAN Phase 0 step 5, fallback: build the fixture (Phase 7) in CI with
  the `devkitpro/devkitarm` container image. Locally, a maintainer can build
  it on a machine where devkitPro is reachable. Not added to the Dockerfile.

## Dev container
- The repo had no `.devcontainer/`. The workspace runs on
  `devcontainers/base` 2.2.5 bookworm (from
  `/usr/local/etc/vscode-dev-containers/meta.env`) with Go 1.27.3 at
  `/usr/local/go` (`/go` GOPATH → Go feature), Node via nvm (Node feature)
  and `/usr/bin/claude` (Claude Code feature). `.devcontainer/` was
  **reconstructed** from that; the Coder template may set more (mounts,
  settings, feature versions — the Node feature major is a guess). Compare
  with the template before relying on a rebuild.
- The root filesystem of this Coder workspace is ephemeral; only
  `/home/vscode/volume` persists. Everything under `/opt` and `/usr/local/bin`
  disappears on restart unless the image provides it.

## Installed tools checklist
| Tool | Version | Installed live | Recorded in |
| --- | --- | --- | --- |
| apt: xvfb xauth mesa-utils libgl1-mesa-dri libegl1 libxkbcommon-x11-0 libxcb-cursor0 libxcb-icccm4 libxcb-image0 libxcb-keysyms1 libxcb-randr0 libxcb-render-util0 libxcb-shape0 libxcb-xinerama0 libxcb-xkb1 libfontconfig1 libdbus-1-3 x11-utils imagemagick file jq unzip curl python3 | bookworm | yes | `.devcontainer/Dockerfile` |
| apt: libopengl0 libharfbuzz0b libjack-jackd2-0 libasound2 libfribidi0 libusb-1.0-0 (found missing via `ldd`) | bookworm | yes | `.devcontainer/Dockerfile` |
| apt: squashfs-tools binutils (AppImage extraction) | bookworm | yes | `.devcontainer/Dockerfile` |
| Azahar AppImage, extracted to `/opt/azahar` | 2127.0-rc3, sha256 `173dcedb…fc1da` | yes | `Dockerfile` ARGs + `.devcontainer/install-azahar.sh` |
| golangci-lint | 2.15.0 | yes | `Dockerfile` ARG + `.devcontainer/install-go-tools.sh` |
| goreleaser | 2.20.2 | yes | `Dockerfile` ARG + `.devcontainer/install-go-tools.sh` |
| Go | 1.27.3 | preinstalled | `devcontainer.json` Go feature |
| Cobra | v1.10.2 | `go.mod` | `go.mod` |
| actionlint | 1.7.12 | yes | `Dockerfile` ARG + `.devcontainer/install-go-tools.sh` |
| apt: shellcheck | bookworm (0.9.0) | yes | `.devcontainer/Dockerfile` |
| devkitPro (devkitARM, libctru, 3dstools) | — | **no** (HTTP 403) | not recorded; CI fallback planned |

Verified: `xvfb-run glxinfo -B` → llvmpipe, OpenGL 4.5 (Mesa 22.3.6).

## Findings
- Azahar releases (2026-11-18): latest stable 2126.2; latest 2127 build is
  **2127.0-rc3** (2026-11-12). No stable 2127 yet, so rc3 is pinned.
  Both #2730 (savestate RPC, merged as `acf9523`) and #2731 (SetInput RPC,
  merged as `e886b5f`) are ancestors of the `2127.0-rc3` tag
  (GitHub compare API).
- The release asset is named `azahar.AppImage` with no version in it, so the
  tag in the download URL is what pins the version. GitHub's published
  digest matches the pinned SHA-256.
- The AppImage runtime is a static ELF; the squashfs starts right after its
  section header table (offset 944632 for rc3). `install-azahar.sh`
  computes it with `readelf` and extracts with `unsquashfs`, so the AppImage
  is never executed and no FUSE is needed.

## CI (added before Phase 1, on request)
- Workflows: `ci.yml` (PRs + main) and `release.yml` (`v*` tags). The
  release fails unless the tag equals `v` + `const version` in
  `cmd/mcp-azahar/main.go` (`scripts/check-version.sh`), checked in the
  workflow before building and again as a goreleaser hook (skipped for
  snapshot builds).
- Actions pinned to SHAs (checkout v7.0.1, setup-go v7.1.0,
  golangci-lint-action v9.3.0, goreleaser-action v7.3.0), versions looked up
  on GitHub 2026-11-18. Dependabot keeps them current.
- Workflows are untested on GitHub until the branch is pushed; locally,
  every check they run passes, and `check-version.sh` was tested with
  matching, mismatching, unprefixed and missing tags.
- Integration-test job deferred until Azahar runs (glibc blocker) and the
  fixture exists (Phase 7).

## Deviations from PLAN.md
- Phase 0 step 4 asked for `--appimage-extract`; `unsquashfs` at the
  computed offset is used instead (same result, does not execute the
  downloaded runtime).
- The release version is a constant in `cmd/mcp-azahar/main.go` (requested
  by the maintainer) instead of being injected only via `-ldflags`;
  `internal/version` keeps Commit/Date and the formatting.
- `CLAUDE.md` only imports `AGENTS.md`, so build/test commands and
  conventions live in one place.

## Out of scope (do not build)
- Upstream Azahar changes such as an RPC screenshot/framebuffer command.
  Draft the feature request in `docs/upstream-proposal.md` instead.

## Open questions
- Which base image should the dev container use given the glibc blocker?
- Does the Coder template build from this repo's `.devcontainer/`, or must
  the template be updated separately?
