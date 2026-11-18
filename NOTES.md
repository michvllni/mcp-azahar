# NOTES.md — running log

Findings, open questions, and deviations from `docs/PLAN.md`. Newest phase
at the top of each section.

## Status
- Phase 0 (tooling and skeleton): done except devkitPro (see "Blockers").
  The dev container runs Debian trixie (maintainer: assume trixie from now
  on).
- Phase 1 (protocol research): done, see `docs/PROTOCOL.md`. D4 decided
  (RPC screenshots). Parallel sessions/RPC isolation decided as D9 (one
  RPC session per host, upstream request filed).
- Phase 2 (RPC client `internal/rpc`): done, reviewed (PR #7 review
  findings fixed). Unit tests against `internal/rpc/fakeserver`, and a live
  test (`-tags integration`) passed against Azahar 2127.0-rc3.
- Phase 3 (sessions and process manager, `internal/session`,
  `internal/emu`): done, reviewed (PR #9, two rounds); live launch test
  passed.
- Phase 4 (screenshots, `internal/capture`): done; live capture test
  passed (top 400x240, bottom 320x240, composite 400x480).
- Phase 5a (CLI part 1, `internal/ops` + commands launch, attach, stop,
  status, sessions, version, press, hold, release, touch, circle-pad,
  screenshot, read, write, state, logs): done; end-to-end run against Azahar
  passed. Next: Phase 5b (symbols, read-symbol, wait-for, sequence).

## Blockers

### Resolved: Azahar 2127 needs Debian trixie
- Azahar 2127.0-rc3 needs `GLIBC_2.38`/`GLIBC_2.39` and a HarfBuzz newer
  than 6.0 (bundled pango uses `hb_ot_color_has_paint`); Debian bookworm
  has glibc 2.36 and HarfBuzz 6.0. libstdc++ is no problem, the bundle
  ships one in `checkrt/`.
- Decision (maintainer, 2026-11-18): base image
  `mcr.microsoft.com/devcontainers/base:2.2.5-trixie` (glibc 2.41,
  HarfBuzz 10.2). All apt packages were checked against the trixie index;
  only `libasound2` → `libasound2t64` changed. From here on the project
  assumes the dev container runs trixie.
- Lesson from a temporary bookworm workaround (since removed): never run
  Azahar with a newer glibc via `LD_LIBRARY_PATH`. Azahar spawns `sh`,
  which inherits the variable and crashes (`undefined symbol:
  __tunable_is_initialized, version GLIBC_PRIVATE`).

### devkitPro: download server returns HTTP 403
- `curl https://apt.devkitpro.org/install-devkitpro-pacman` → 403 from this
  workspace (devkitPro is known to block some cloud/CI address ranges).
  Not retried with other user agents or mirrors.
- Per PLAN Phase 0 step 5, fallback: build the fixture (Phase 7) in CI with
  the `devkitpro/devkitarm` container image. Locally, a maintainer can build
  it on a machine where devkitPro is reachable. Not added to the Dockerfile.

## Dev container
- The repo had no `.devcontainer/`. The workspace was started on
  `devcontainers/base` 2.2.5 bookworm (from
  `/usr/local/etc/vscode-dev-containers/meta.env`) with Go 1.27.3 at
  `/usr/local/go` (`/go` GOPATH → Go feature), Node via nvm (Node feature)
  and `/usr/bin/claude` (Claude Code feature). `.devcontainer/` was
  **reconstructed** from that; the Coder template may set more (mounts,
  settings, feature versions — the Node feature major is a guess). Compare
  with the template before relying on a rebuild. The base was then switched
  to trixie (see "Resolved" above).
- The root filesystem of this Coder workspace is ephemeral; only
  `/home/vscode/volume` persists. Everything under `/opt` and `/usr/local/bin`
  disappears on restart unless the image provides it.

## Installed tools checklist
| Tool | Version | Installed live | Recorded in |
| --- | --- | --- | --- |
| apt: xvfb xauth mesa-utils libgl1-mesa-dri libegl1 libxkbcommon-x11-0 libxcb-cursor0 libxcb-icccm4 libxcb-image0 libxcb-keysyms1 libxcb-randr0 libxcb-render-util0 libxcb-shape0 libxcb-xinerama0 libxcb-xkb1 libfontconfig1 libdbus-1-3 x11-utils imagemagick file jq unzip curl python3 | distro | yes | `.devcontainer/Dockerfile` |
| apt: libopengl0 libharfbuzz0b libjack-jackd2-0 libasound2 (trixie: libasound2t64) libfribidi0 libusb-1.0-0 (found missing via `ldd`) | distro | yes | `.devcontainer/Dockerfile` |
| apt: squashfs-tools binutils (AppImage extraction) | distro | yes | `.devcontainer/Dockerfile` |
| apt: shellcheck | distro | yes | `.devcontainer/Dockerfile` |
| Azahar AppImage, extracted to `/opt/azahar` | 2127.0-rc3, sha256 `173dcedb…fc1da` | yes | `Dockerfile` ARGs + `.devcontainer/install-azahar.sh` |
| golangci-lint | 2.15.0 | yes | `.devcontainer/install-go-tools.sh` (Renovate) |
| goreleaser | 2.20.2 | yes | `.devcontainer/install-go-tools.sh` (Renovate) |
| actionlint | 1.7.12 | yes | `.devcontainer/install-go-tools.sh` (Renovate) |
| Go | 1.27.3 | preinstalled | `devcontainer.json` Go feature |
| Cobra | v1.10.2 | `go.mod` | `go.mod` |
| devkitPro (devkitARM, libctru, 3dstools) | — | **no** (HTTP 403) | not recorded; CI fallback planned |

Verified: `xvfb-run glxinfo -B` → llvmpipe, OpenGL 4.5 (Mesa 22.3.6).

## Findings
### Phase 5a
- `internal/ops` holds every operation once; the CLI only parses and
  prints. MCP (Phase 6) will call the same functions.
- Held input is stored in the session file and merged into every
  SetInput, because Azahar replaces the whole input state per request and
  CLI calls are separate processes. `press` = held + pressed for a duration
  (default 100 ms: ~23 HID updates at 234 Hz), then back to held, also
  when the command is interrupted.
- Memory commands select the app process if none is selected (newest
  PID), so they work right after launch and after a state load.
- `state load` refuses an empty slot for launched sessions (Azahar would
  hang behind a modal dialog).
- End-to-end run of the built binary against Azahar 2127.0-rc3 (minimal
  3DSX): launch, second launch refused (exit 1, holder named), read/write
  with verification and dump, hold/press/touch/circle-pad/release,
  screenshot both (400x480 PNG), save/modify/load restored the bytes,
  loading an empty slot refused (exit 2), logs, sessions --json, stop,
  status afterwards exit 4, no process left.
- Verified writes cannot detect a write of bytes equal to the current
  content (e.g. zeros to unmapped memory); documented in `write --help`.

### Phase 4
- `capture.RPC` takes one RPC screenshot per screen (PNG straight from
  Azahar, no re-encoding) and checks the PNG against the reported size and
  the screen's aspect ratio: Azahar's other layouts put several screens in
  one image, so attached instances must use the single-screen layout or
  get `capture.ErrLayout` with instructions.
- `both` = two consecutive frames (one per screen), not one frame.
  `capture.Compose` builds one PNG in 3DS arrangement (top above, bottom
  centered, opaque black margins), decoding one shot at a time (about
  77 MB output at scale 10); MCP will return the shots as separate images.
- Failures name the screen and add a hint: software renderer
  (Unsupported), concurrent capture (Busy), paused or blocked emulation
  (Timeout).

### Phase 3
- `emu.Manager.Launch`, under the store lock plus a best-effort host lock
  `/tmp/mcp-azahar-45987.lock` (shared by all users and state dirs; opened
  without `O_CREAT` so `fs.protected_regular` allows it, never through a
  symlink; skipped if it cannot be opened):
  - checks the session name and the RPC port (holder named: session from
    session files; on Linux the process via `/proc/net/udp` + `/proc/*/fd`);
  - moves an existing `sessions/<name>.d` aside to `<name>.d.<timestamp>`
    (kept save states or a crash log are never deleted);
  - seeds `sessions/<name>.d/user/config/qt-config.ini` (PROTOCOL.md §7);
  - starts Xvfb on the first free display from `:99` (Linux, unless a
    display is given), trying the next display if Xvfb exits right away;
  - starts Azahar detached with cwd = session dir (portable mode); `HOME`
    is left alone (overriding it broke X auth on a user display);
  - waits for RPC, then verifies ownership: it waits for Azahar's
    `Starting RPC server` log line, rejects `Error starting UDP server`,
    and on Linux requires every socket on the port to belong to the
    started PID or a descendant; a socket whose owner is unreadable
    (another user) or no socket at all counts as foreign. On macOS only the
    log check applies, which depends on Azahar's log timing and is not
    verified live.
- `launch` refuses on Windows: Azahar takes its portable `user/` dir from
  next to the executable there (`file_util.cpp:944-946`), so sessions
  cannot get an isolated config with RPC enabled. `attach` works.
- Process identity is PID plus start time on Linux (`/proc`), macOS
  (sysctl) and Windows (creation time). Every cleanup (stop, prune, stale
  removal, relaunch) also SIGKILLs what is left of a process group whose
  leader exited (wrapper scripts without `exec`), only while the leader's
  PID is free, so the group ID cannot have been reused.
- A killed multi-threaded process (Azahar, or the Go fake in tests) can
  show its main thread as a zombie while other threads are still exiting;
  its file descriptors (e.g. the RPC socket) are released only after the
  last thread. `session.Alive` therefore counts a zombie as dead only when
  `/proc/<pid>/task` lists no other threads (found through a CI failure of
  `TestStaleSessionRemoved`).
- Moved-aside session directories (`<name>.d.<timestamp>`) are never
  deleted automatically; remove them by hand when no longer needed.
- Unreadable session files: `launch` refuses, `stop` removes the file and
  warns that its processes may still run.
  Failures stop everything started and include the tail of Azahar's output
  and log.
- Live (2127.0-rc3, minimal 3DSX): launch 1.2 s; a second launch was
  refused; bottom screenshot 320x240 (seed config applied); save wrote
  `states/0000000000000000.01.cst`; load and stop worked, nothing left
  running.
- `emu.HasState` checks for a save state file before LoadState, because an
  empty slot leaves Azahar stuck behind a modal dialog (Phase 2 finding).
  Attached sessions cannot be checked (user dir unknown).
- This workspace is still bookworm with the earlier live patch: `AppRun`
  fails there (`checkrt` loads the bundled libstdc++, which needs glibc
  2.38), so live tests here use `AZAHAR_BIN=/opt/azahar/usr/bin/azahar`. On
  the trixie image `AppRun` works.

### Phase 2
- Client design: one `net.Conn` from a `Dialer` (UDP today; any
  datagram-preserving conn such as `unixgram` works, so a per-session relay
  can plug in later), requests serialized with a mutex, random starting
  request ID, replies matched by version+ID+type, per-attempt timeout
  (default 2 s) with 3 resends for idempotent requests only, context
  cancellation interrupts a blocking read via `context.AfterFunc` (the
  callback is awaited before the next request, so a late cancel cannot cut
  it short). "Connection refused" (ICMP) counts as a lost attempt.
- Screenshot and LoadState take a client-wide operation lock, so two
  goroutines cannot interleave TakeScreenshot/ReadScreenshot and tear an
  image. A selection read-write lock makes memory requests and
  SelectProcess wait while LoadState runs (otherwise a concurrent select
  fools the reload detection, and writes hit the marker selection and are
  silently dropped). Other clients of the same Azahar are not coordinated
  (D9).
- A refused attempt waits out its full attempt time, so retries cover a
  restart window like timeouts do; a refused error on Read keeps reading
  (it may be a stale ICMP report from an earlier datagram). On Windows Go
  suppresses these errors for UDP, so only timeouts apply there.
- `WriteMemoryVerified` reads back, because Azahar never reports refused
  writes. Live-confirmed for `0x1FF80000`.
- `LoadState` selects a marker PID (`0xFFFFFFFE`) before loading and waits
  until `GetProcess` no longer returns it (the recreated server has no
  selection), then restores the previous selection. This works with or
  without a selected process and detects loads that never happen. Live:
  0.87 s for the minimal 3DSX, through the closed-port window.
- Live, empty save slot: Azahar accepts the load, then stops emulation
  behind a modal "Save/load Error" dialog; until it is dismissed, **RPC
  answers nothing** (sync mode). The client returns `ErrStateNotLoaded`
  after `ReloadTimeout` plus one short restore attempt (live: 5.5 s with a
  5 s timeout); the marker selection may remain until SelectProcess. Phase 3 must check the state file exists before
  loading, and may need to detect such dialogs (window title via X) to
  report a stuck session.
- The live test passed against the 2127.0-rc3 AppImage with the Phase 1
  research 3DSX (scratch address `0x101000`).

### Phase 1 (details and sources in `docs/PROTOCOL.md`)
- Source: azahar-emu/azahar `2127.0-rc3` = `11eceda5`, shallow clone outside
  the repo. Every RPC request was exercised against the AppImage under Xvfb
  with a hand-made 3DSX (ARM `b .` loop + marker words; generator kept out
  of the repo, it is a research tool).
- RPC protocol v2: UDP, 16-byte LE header (version 2, id, type, size), max
  32 KiB data, 11 request types incl. memory, process list, **screenshots**,
  perf stats, save/load state, SetInput. No error codes: failures are empty
  replies or silently dropped datagrams.
- **The RPC server binds `0.0.0.0:45987`, hard-coded.** Parallel sessions
  need separate network namespaces; unprivileged user+net namespaces work in
  the dev container and two instances ran in parallel that way.
- **RPC has TakeScreenshot/ReadScreenshot** (PNG or raw RGB). With
  `layout_option=1` it returns the top screen (400x240) or, with the
  secondary flag, the bottom screen (320x240), so no cropping is needed.
  The maintainer then switched D4 to RPC screenshots (2026-11-18).
- RPC starts only after an app is loaded and is recreated by LoadState
  (in-flight request lost, selected process reset).
- WriteMemory replies empty even when it refuses the address; unmapped
  reads return zeros. Clients must read back.
- Seed config (`[Debugging] enable_rpc_server=true`, mind the
  `\default` flag), portable `./user/` dir per session, update checker
  off. No first-run dialog. CIA files trigger an install prompt.
- Azahar ignores SIGINT/SIGTERM, **exits on SIGHUP**.
- Qt `offscreen` platform is not bundled: Xvfb stays required.

### Phase 0
- Azahar 2127.0-rc3 starts under Xvfb: main window
  "Azahar 2127.0-rc3" (853x512), OpenGL renderer (llvmpipe), game-list
  placeholder. **No first-run setup dialog** with a fresh, empty
  `HOME`/`XDG_CONFIG_HOME`; it wrote `~/.config/azahar-emu/qt-config.ini`.
- **Azahar ignores SIGTERM** (still running 3 s after `kill -TERM`; needed
  SIGKILL). Phase 1 found that SIGHUP stops it.
- Azahar spawns `sh` at startup (seen through the LD_LIBRARY_PATH crash);
  harmless, but keep it in mind for sandboxing.
- CLI flags (`azahar -h`): `-g/--gdbport`, `-i/--install` (CIA),
  `-f/--fullscreen`, `-w/--windowed`, `-d/--dump-video`, movie
  record/play, `<file path>` to boot an app. No flag for config dir or RPC.
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
- `state save|load` takes slots 0-10 only, not paths: Azahar's RPC
  save/load commands only know slots (PROTOCOL.md §5.7). The state files are
  in the session's `user/states/`.
- `touch` and `circle-pad` also take `--for <duration>` (how long without
  `--hold`), and `read` has `--format hex|dump|raw`.
- Parallel sessions on one host are not supported with Azahar 2127
  (DECISIONS.md D9): one RPC-enabled Azahar per host, upstream request for a
  configurable port, namespaces in reserve. PLAN Phases 3, 7 and 8 updated.
- Screenshots use Azahar's RPC instead of X11 capture (DECISIONS.md D4,
  PLAN Phase 4 updated). Phase 2 keeps the RPC transport behind an
  interface so a per-session relay (D9 option A, in reserve) can plug in
  without changing the client.
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
- Upstream Azahar changes. Feature requests are drafted for the maintainer
  (not committed) and filed by them. Since Azahar 2127 already has an RPC
  screenshot command, the candidates are now: configurable RPC port and
  bind address (default 127.0.0.1), status codes in replies, and
  pause/resume, frame advance, reset and shutdown requests.

## Open questions
- Upstream feature request for a configurable RPC port/bind address
  (DECISIONS.md D9): filed by the maintainer on 2026-11-18 as
  https://github.com/azahar-emu/azahar/issues/2829 (open). Offers a PR if
  welcome. When it lands, switch sessions to a free port on 127.0.0.1 for
  Azahar versions that support it (D9 "Revisit if").
- Not verified yet: whether the SetInput override survives LoadState;
  whether a user pause (not just an error dialog) also blocks RPC.
- Does the Coder template build from this repo's `.devcontainer/`, or must
  the template be updated separately for trixie?
- Renovate GitHub app must be installed on the repo by the maintainer.
