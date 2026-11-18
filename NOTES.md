# NOTES.md — running log

Findings, open questions, and deviations from `docs/PLAN.md`. Newest phase
at the top of each section.

## Status
- Phase 0 (tooling and skeleton): done except devkitPro (see "Blockers").
  The Dockerfile now uses Debian trixie; the running workspace is still on
  bookworm and runs Azahar through a stopgap patch until it is rebuilt.
  Next: maintainer rebuilds the container (and updates the Coder template if
  it does not build from `.devcontainer/`), then Phase 1.

## Blockers

### Resolved: Azahar AppImage does not run on Debian bookworm
- Azahar 2127.0-rc3 needs `GLIBC_2.38`/`GLIBC_2.39` (bookworm: 2.36) and a
  newer HarfBuzz than bookworm's 6.0 (bundled pango:
  `undefined symbol: hb_ot_color_has_paint`). libstdc++ is no problem, the
  bundle ships one in `checkrt/`.
- Decision (maintainer, 2026-11-18): base image →
  `mcr.microsoft.com/devcontainers/base:2.2.5-trixie` (glibc 2.41,
  HarfBuzz 10.2). All apt packages were checked against the trixie index;
  only `libasound2` → `libasound2t64` changed. Not yet built: needs a
  rebuild by the maintainer.
- Stopgap for the running bookworm container:
  `.devcontainer/patch-azahar-bookworm.sh` (not used by the Dockerfile;
  delete after the rebuild). It copies trixie's `libc6` and
  `libharfbuzz0b` (pinned versions + SHA-256) and the bundle's
  libstdc++/libgcc_s into `/opt/azahar/usr/lib`, sets the interpreter of
  `/opt/azahar/usr/bin/azahar` to that dir's `ld-linux` and turns its
  RUNPATH into an RPATH. Start it as `/opt/azahar/usr/bin/azahar`.
  Rerun `install-azahar.sh` + the patch after every workspace restart (root
  FS is ephemeral).
- Why not `LD_LIBRARY_PATH` (first attempt): Azahar spawns `sh`, which
  inherited the variable and crashed (`/opt/glibc/libc.so.6: undefined
  symbol: __tunable_is_initialized, version GLIBC_PRIVATE`). The AppImage's
  `checkrt/exec.so` only hooks `exec*`, not glibc 2.41's
  `posix_spawn`-based `popen`/`system`. With the RPATH approach no variable
  is set, so children use the system libraries.

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
Live = installed in the running (bookworm) container; the Dockerfile
installs the same on trixie.

| Tool | Version | Installed live | Recorded in |
| --- | --- | --- | --- |
| apt: xvfb xauth mesa-utils libgl1-mesa-dri libegl1 libxkbcommon-x11-0 libxcb-cursor0 libxcb-icccm4 libxcb-image0 libxcb-keysyms1 libxcb-randr0 libxcb-render-util0 libxcb-shape0 libxcb-xinerama0 libxcb-xkb1 libfontconfig1 libdbus-1-3 x11-utils imagemagick file jq unzip curl python3 | distro | yes | `.devcontainer/Dockerfile` |
| apt: libopengl0 libharfbuzz0b libjack-jackd2-0 libasound2 (trixie: libasound2t64) libfribidi0 libusb-1.0-0 (found missing via `ldd`) | distro | yes | `.devcontainer/Dockerfile` |
| apt: squashfs-tools binutils (AppImage extraction) | distro | yes | `.devcontainer/Dockerfile` |
| apt: shellcheck | distro (bookworm 0.9.0) | yes | `.devcontainer/Dockerfile` |
| Azahar AppImage, extracted to `/opt/azahar` | 2127.0-rc3, sha256 `173dcedb…fc1da` | yes | `Dockerfile` ARGs + `.devcontainer/install-azahar.sh` |
| trixie libc6 2.41-12+deb13u4 + libharfbuzz0b 10.2.0-1+deb13u1 into `/opt/azahar/usr/lib`, apt: patchelf | bookworm stopgap | yes | `.devcontainer/patch-azahar-bookworm.sh` (not needed on trixie) |
| golangci-lint | 2.15.0 | yes | `.devcontainer/install-go-tools.sh` (Renovate) |
| goreleaser | 2.20.2 | yes | `.devcontainer/install-go-tools.sh` (Renovate) |
| actionlint | 1.7.12 | yes | `.devcontainer/install-go-tools.sh` (Renovate) |
| Go | 1.27.3 | preinstalled | `devcontainer.json` Go feature |
| Cobra | v1.10.2 | `go.mod` | `go.mod` |
| devkitPro (devkitARM, libctru, 3dstools) | — | **no** (HTTP 403) | not recorded; CI fallback planned |

Verified: `xvfb-run glxinfo -B` → llvmpipe, OpenGL 4.5 (Mesa 22.3.6).

## Findings
- Azahar 2127.0-rc3 starts under Xvfb (bookworm + stopgap): main window
  "Azahar 2127.0-rc3" (853x512), OpenGL renderer (llvmpipe), game-list
  placeholder. **No first-run setup dialog** with a fresh, empty
  `HOME`/`XDG_CONFIG_HOME`; it wrote `~/.config/azahar-emu/qt-config.ini`.
- **Azahar ignores SIGTERM** (still running 3 s after `kill -TERM`; needed
  SIGKILL). Phase 3 `stop` must escalate (TERM → wait → KILL), or find a
  clean shutdown path in Phase 1.
- Azahar spawns `sh` at startup (seen through the LD_LIBRARY_PATH crash);
  harmless, but keep it in mind for sandboxing.
- CLI flags (`azahar -h`): `-g/--gdbport`, `-i/--install` (CIA),
  `-f/--fullscreen`, `-w/--windowed`, `-d/--dump-video`, movie
  record/play, `<file path>` to boot an app. No flag for config dir or RPC
  (Phase 1).
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
- Workflows: `ci.yml` (PRs + main) and `release.yml`, triggered by
  **publishing a GitHub release** (maintainer's choice; the release creates
  the tag). The release job fails unless the tag equals `v` +
  `const version` in `cmd/mcp-azahar/main.go` and the pre-release checkbox
  matches the version's SemVer suffix (`scripts/check-version.sh -p`),
  checked before building; goreleaser repeats the tag check as a hook
  (skipped for snapshot builds).
- goreleaser uploads into the existing release (source checked:
  `internal/client/github.go` at v2.20.2): it keeps the notes
  (`mode: keep-existing`), but always sets the title (`name_template`, here
  the release's own title via `RELEASE_NAME`) and the pre-release flag
  (`prerelease: auto`, hence the checkbox check). It refuses **immutable
  releases**; if the repo enables them, switch to a draft-first flow.
- A failed check leaves the published release without assets: delete the
  release and its tag, fix, and publish again.
- Actions pinned to SHAs (checkout v7.0.1, setup-go v7.1.0,
  golangci-lint-action v9.3.0, goreleaser-action v7.3.0), versions looked up
  on GitHub 2026-11-18.
- Dependencies: **Renovate GitHub app** (maintainer's choice, replaces
  Dependabot), config `.github/renovate.json5` (Renovate 44.x schema:
  `customManagers` with `managerFilePatterns`). Tool versions are annotated
  with `# renovate:` comments; the regex was tested locally against all 5
  annotated pins. The config itself was not run through
  `renovate-config-validator` (installing Renovate was blocked in this
  session); the app reports config errors on onboarding. Azahar is not
  managed by Renovate because its SHA-256 must change with the version.
- Workflows are untested on GitHub until the branch is pushed; locally,
  every check they run passes, and `check-version.sh` was tested with
  matching/mismatching tags, pre-release flags and build metadata.
- Integration-test job deferred until the fixture exists (Phase 7).

## Deviations from PLAN.md
- Base image changed from bookworm to trixie (maintainer decision), see
  "Resolved" above.
- Tool versions (golangci-lint, goreleaser, actionlint) are pinned in
  `install-go-tools.sh` defaults instead of Dockerfile ARGs, so the image and
  CI share one source that Renovate updates. Azahar stays pinned via ARGs.
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
- Does the Coder template build from this repo's `.devcontainer/`, or must
  the template be updated separately for trixie?
- Renovate GitHub app must be installed on the repo by the maintainer.
