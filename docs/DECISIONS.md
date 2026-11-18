# Decisions

Why mcp-azahar is shaped the way it is. Each entry: the decision, the
reasoning, and what would make us revisit it. Status as of 2026-11-18.

## D1 — Build on Azahar's built-in RPC server
**Decision:** Talk to Azahar exclusively through its RPC server, no fork.

**Why:** Azahar 2127 (in prerelease as of Nov 2026; 2127.0-rc2 on Nov 6)
added everything an automation tool needs (Phase 1 found that it also has
screenshot commands, see D4 and `docs/PROTOCOL.md`):
- input injection (PR #2731): buttons, HOME/POWER, touch, circle pad;
  input is held until the next packet changes or clears it
- save state commands (PR #2730)
- memory read/write (inherited from Citra)
Both PRs were written and merged by Azahar co-lead PabloMK7 into the 2127.0
milestone. No public feature-request issue preceded them, so this was a
maintainer-driven addition rather than a debated decision, and RPC is
evidently treated as a scripting surface (a free-camera Python script ships
in `dist/`).

**Caveats:**
- 2127 also made breaking RPC changes (#2631, #2640, #2633). Old Citra RPC
  examples and clients do not apply; the protocol must be derived from
  current source (see PLAN.md Phase 1).
- Since 2121.2 the RPC server is disabled by default (a security fix: it
  was LAN-reachable and unauthenticated). We enable it per session in an
  isolated config.
- Correction (Phase 1, 2026-11-18): the bind address and port cannot be
  configured. Azahar 2127 always listens on `0.0.0.0:45987`
  (`docs/PROTOCOL.md` §2). How sessions deal with that: D9.

**Revisit if:** Azahar removes or substantially redesigns RPC.

## D2 — Separate project, not an Azahar contribution
**Decision:** mcp-azahar lives in its own repo. Small, general-purpose
emulator-side improvements (e.g. a configurable RPC port and bind
address) are proposed upstream separately.

**Why:**
- The tool is a client of the RPC, not part of the emulator. Comparable
  tools (mcp-mgba, mcp-retroarch, mcp-bizhawk) all live outside their
  emulators.
- Different stack and release rhythm: Azahar is a C++ app with
  alpha/RC/stable cycles; this tool should iterate quickly with its own
  versioning and packaging.
- Asking Azahar maintainers to own an AI-agent integration is a hard sell
  and an unnecessary burden on them.
- Azahar asks contributors to open a feature-request issue before writing
  code. A working tool makes a much stronger case for an upstream change
  than a hypothetical.

**Revisit if:** Azahar maintainers ask for the integration upstream.

## D3 — Go, one binary with CLI and MCP server
**Decision:** Implement in Go. One binary (`mcp-azahar`) with Cobra
subcommands for a CLI, plus `mcp-azahar serve` for MCP over stdio.

**Why:**
- Official MCP Go SDK (github.com/modelcontextprotocol/go-sdk).
- Single static binary: no Node/Python runtime for users or CI.
- The same internal packages power both front ends, so the CLI comes for
  free and nothing is implemented twice.
- The CLI is useful on its own: humans debugging, shell scripts, CI jobs,
  and agents using bash (cheaper in context than a full MCP tool list).
- Go's `debug/elf` makes symbol lookup trivial, enabling `read-symbol`:
  reading homebrew variables by name instead of hard-coded addresses.
- Fits the maintainer's existing Go/Cobra experience.

**Consequence:** CLI calls are short-lived processes, so the emulator runs
detached and is tracked via session files that both CLI and MCP share
(PLAN.md Phase 3). This also lets a human watch an instance via the CLI
while an agent drives it via MCP.

**Revisit if:** the MCP Go SDK falls behind the spec significantly.

## D4 — Screenshots via Azahar's RPC screenshot command
**Decision (maintainer, 2026-11-18, replaces "X11 capture for now"):**
Take screenshots with the RPC requests `TakeScreenshot`/`ReadScreenshot`.
Sessions pin the single-screen layout (`[Layout] layout_option=1`), so a
normal capture is the top screen (400x240) and a capture with the
`SecondaryWindow` flag the bottom screen (320x240), times the resolution
scale. No X11 capture backend.

**Why:** Phase 1 found that Azahar 2127 has these commands, contrary to the
original premise of this decision (`docs/PROTOCOL.md` §5.5, verified
headless). Compared with X11 capture they:
- return exact per-screen images with no window cropping or layout guessing;
- work on every OS and also for instances started by hand (`attach`);
- need no access to the X display, only the RPC connection;
- can render above native resolution (`res_scale` up to 10).

**Consequences:**
- Xvfb is still needed to *run* Azahar headless, but not to capture.
- Screenshots fail with the software renderer (`Unsupported`) and time out
  while emulation renders no frames (e.g. paused).
- For `attach`ed instances the user's layout setting applies; the capturer
  must report the actual size instead of assuming one.
- The `Capturer` interface stays, so another backend can be added if ever
  needed.

**Revisit if:** Azahar removes or changes the RPC screenshot commands, or
screenshots are needed in situations RPC cannot serve.

## D5 — Test via a fixture homebrew with known state
**Decision:** Ship a tiny libctru homebrew (`testdata/fixture/`) that
exposes a `test_state` struct as a global symbol, draws deterministic
output, and logs to stderr.

**Why:** Asserting on structured state via `read-symbol` is far more
reliable than inferring from pixels; screenshots become a secondary check.
The same pattern is what we recommend to users: build a test hook into the
homebrew under test.

## D6 — Name: mcp-azahar
**Decision:** Repo, binary and module use `mcp-azahar`
(module `github.com/michvllni/mcp-azahar`).

**Why:** Follows the `mcp-<emulator>` convention of the closest prior art
(mcp-mgba, mcp-retroarch, mcp-bizhawk). `azahar-mcp` was considered. Both
names were free on npm, PyPI, crates.io and GitHub as of 2026-11-18.

**Note:** Because the name leads with Azahar, the README and repo
description state clearly that the project is unofficial.

## D7 — License: GPL-3.0-or-later
**Decision:** GPL-3.0-or-later from the first commit, with SPDX headers in
every source file.

**Why:**
- Same license family as Azahar, so code can be ported from Azahar
  (keeping its copyright notices) without license concerns.
- Running a GPL tool imposes nothing on the homebrew it tests or the CI
  pipelines that call it, so users aren't restricted.
- Keeps the tool from being taken closed.

**Trade-off accepted:** A public Go package would force GPL on importers.
All code lives in `internal/` for now. If a reusable RPC client library is
ever wanted, it can be a separate, permissively licensed module written
from our own protocol docs (not ported from Azahar).

**Note:** Moving from GPL to a permissive license later would require
agreement from every contributor; this decision is effectively permanent.

## D8 — Develop inside a dev container
**Decision:** Development and agent work happen in a Go dev container with
Xvfb, Mesa software rendering, an extracted Azahar AppImage, and devkitPro.

**Why:** Reproducible environment for humans, agents and CI; Azahar runs
headless without a host display; no FUSE needed after AppImage extraction.

## D9 — One RPC session per host until Azahar's port is configurable
**Decision (maintainer, 2026-11-18):** With Azahar 2127, mcp-azahar runs at
most one RPC-enabled Azahar per host (more precisely, per network
namespace). In parallel, we ask Azahar upstream for a configurable RPC
port and bind address (https://github.com/azahar-emu/azahar/issues/2829). Per-session
network namespaces stay in reserve.

**Why:** Azahar 2127 always binds RPC to `0.0.0.0:45987`. Three options
were weighed:
- **A, network namespace per session** (unprivileged
  `unshare --user --net`, with a relay over a Unix socket): parallel
  sessions and RPC kept off the network. Phase 1 observed two instances
  isolated this way; the relay was not built or tested. It is Linux-only,
  needs unprivileged user namespaces (often restricted, e.g. by Ubuntu
  24.04's AppArmor defaults; unverified for GitHub runners and Docker's
  default seccomp profile), and adds a relay process to supervise.
- **B, one session per host:** simple, works anywhere Azahar runs; parallel
  runs use separate containers or VMs, as CI matrix jobs already do.
- **C, upstream setting:** solves both problems for everyone, on every OS,
  but only for a future Azahar release.

B ships Phase 3 quickly. C fixes the root cause. A costs the most and is
only worth it if parallel sessions on one host are needed before C lands.

**Consequences:**
- `launch` refuses to start a second RPC-enabled Azahar while port 45987 is
  in use and says why: it names the session if a session file holds the
  port, on Linux the process found through `/proc/net/udp`, otherwise
  "another process". Named sessions (`--session`) stay, so nothing in the
  CLI changes when parallel sessions become possible.
- Port checks race: Azahar binds the port only at the end of loading an
  app, and a second Azahar that cannot bind keeps running without RPC
  (`server.cpp:18-22` only logs `Error starting UDP server`). So `launch`
  holds a host-wide lock (a lock file under `$XDG_STATE_HOME/mcp-azahar/`)
  from the port check until the session file is written, and verifies that
  the RPC server that answers belongs to the Azahar it started: on Linux
  the socket's owner via `/proc`, everywhere no `Error starting UDP
  server` in its log. Otherwise `launch` stops its Azahar and fails.
- The session file stores the RPC endpoint in `net.Dial` terms, not an
  assumed port: `"rpc": {"network": "udp", "address": "127.0.0.1:45987"}`.
  Clients dial `127.0.0.1` (Azahar binds `0.0.0.0`). A configurable port
  (C) changes only the address; a relay (A) would use
  `"network": "unixgram"` with a socket path. The Phase 2 RPC client
  (`internal/rpc`) takes a pluggable `Dialer` for this.
- While a session runs, its RPC server is reachable from the network.
  README and SECURITY.md say so and recommend a firewall or isolated
  container.
- Once an Azahar release supports C, sessions use a free port on 127.0.0.1
  when the installed version allows it.

**Revisit if:** Azahar ships a configurable port/bind address (switch to
C), or parallel sessions on one host are needed before that (implement A,
first checking user-namespace support on the CI runners).

## D10 — Distribution: releases, container image, MCP Registry, npm
**Decision (maintainer, 2026-11-18):** Ship v0.1 through four channels,
all built by the release workflow (PLAN.md Phase 8):
1. **GitHub Releases** (goreleaser archives) and
   `go install github.com/michvllni/mcp-azahar/cmd/mcp-azahar@latest`.
2. **Container image `ghcr.io/michvllni/mcp-azahar`** with mcp-azahar,
   Azahar (extracted AppImage), Xvfb and Mesa: headless sessions without
   installing anything else; the natural way to use it in CI.
3. **npm package `mcp-azahar`** so MCP clients can run
   `npx -y mcp-azahar serve`: a thin wrapper with per-platform binary
   packages (`mcp-azahar-linux-x64` etc.), as esbuild and similar Go/Rust
   tools do. The names were free on npm on 2026-11-18.
4. **Official MCP Registry** entry `io.github.michvllni/mcp-azahar`,
   listing the npm package and the container image.

**Why:**
- The registry needs a package it can verify (npm, PyPI, NuGet, OCI,
  cargo or an MCPB bundle on a GitHub release, per the registry's
  package-types docs). goreleaser 2.20's `mcp` section supports npm,
  PyPI, NuGet and OCI; we use npm and OCI and skip MCPB for now.
- The image removes the hardest setup step (Azahar on a recent glibc plus
  Xvfb) and is ideal for CI.
- npx is the common way MCP clients start servers; it is convenient even
  though a host still needs Azahar and a display for `launch`.

**Consequences:**
- **Registry entries carry no arguments,** so clients installing from the
  registry run `npx -y mcp-azahar` or `docker run -i --rm <image>`: the
  npm wrapper and the image entrypoint default to `serve` (stdio), and the
  image must be run with `-i`.
- **Order and ownership checks:** the registry verifies ownership through
  `mcpName` in package.json (npm) and the
  `io.modelcontextprotocol.server.name` label (OCI). The registry entry is
  published last, after npm and the image, with GitHub OIDC
  (`id-token: write`): `mcp-publisher` as the final workflow step, or
  goreleaser's `mcp` section if its order allows it.
- **npm:** goreleaser OSS has no npm publishing, so a separate workflow
  step publishes the wrapper and the platform packages, with provenance
  (trusted publishing per package; the first publish may need a token).
- **GPL compliance:** Azahar's binary is GPL-3.0 (LICENSE.txt). Each
  image release ships Azahar's Corresponding Source by attaching the
  matching `azahar-unified-source-<version>.tar.xz` (published by Azahar,
  includes submodules) to our GitHub release. The image contains the
  license texts, names Azahar's version, and points to the sources of the
  Qt (LGPL-3.0) build in the AppImage and of the Debian base packages
  (snapshot.debian.org). Common practice, not legal advice.
- **Image platform and version:** linux/amd64 only (Azahar ships only an
  x86_64 AppImage; arm64 would need building Azahar from source). The
  image ships the Azahar version pinned in `.devcontainer/Dockerfile`
  (`AZAHAR_VERSION`, currently 2127.0-rc3: no stable 2127 yet) and says
  so. Its size is an estimate of several hundred MB (Qt, Mesa).
- **Tags:** the release version; `latest` only for non-prereleases
  (goreleaser `dockers_v2`, with an SBOM).
- **RPC isolation (D9):** the container's own network namespace keeps
  UDP 45987 off the host network. Do not publish the port (`-p 45987`) or
  use `--network host`; parallel sessions mean one container each.
- Not now: Homebrew, PyPI, MCPB bundles.

**Revisit if:** users ask for other channels, or goreleaser supports MCPB
(then a registry entry pointing at our release binaries becomes cheap).

## D11 — MCP tools only touch files in allowed directories
**Decision (maintainer, 2026-11-18):** Every file path an MCP tool takes
(app, ELF, attach log, sequence file, screenshot output, and the paths
inside a sequence, including split screenshot names) must resolve, with
symlinks followed, into the server's working directory or a directory
given with `serve --allow-dir` (repeatable). A dangling symlink as a write
target is refused. Relative paths are relative to the first allowed
directory. Tool results are bounded so they fit a model's context.

**Why:**
- The model's arguments are untrusted: text on the emulated screen, in
  the log or in a repository can steer it (prompt injection). Without a
  policy, `attach` with any file as its `log` plus `azahar_logs` read any
  text file, and `screenshot.output` overwrote any file (PR #15 review).
- MCP clients may auto-approve tools, so the server cannot rely on the
  user seeing each call.
- The CLI keeps full access: a human typing a path is trusted.

**Consequences:**
- Sessions created by the CLI may carry ELF and log paths outside the
  allowed directories; tools use them (the human chose them).
- Results are capped: memory reads 4 KiB, values 16 KiB, logs 200 lines
  by default (at most 1000), symbol lists 500 entries, screenshot scale 4.
- The container image (D10) runs `serve` in its working directory: mount
  the project there or pass `--allow-dir`.
- Another local process could swap a checked path for a link before the
  write (TOCTOU); out of scope for a local tool.

**Revisit if:** clients need to work across directories routinely (then
consider MCP roots from the client instead of a flag).

## Known unknowns (resolved in Phase 1)
All answered in `docs/PROTOCOL.md`:
- RPC wire format, payload limits and transport: §2–§5.
- Enabling the RPC server: `[Debugging] enable_rpc_server=true` (§1).
- Pause, frame advance, reset or app load over RPC: not available (§6).
- First-run dialog: none with a fresh config (§7).
- AppImage under Xvfb with Mesa: works on Debian trixie (bookworm's glibc
  is too old; NOTES.md).
