# Decisions

Why mcp-azahar is shaped the way it is. Each entry: the decision, the
reasoning, and what would make us revisit it. Status as of 2026-11-18.

## D1 — Build on Azahar's built-in RPC server
**Decision:** Talk to Azahar exclusively through its RPC server, no fork.

**Why:** Azahar 2127 (in prerelease as of Nov 2026; 2127.0-rc2 on Nov 6)
added everything an automation tool needs except screenshots:
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
  isolated config, bound to localhost.

**Revisit if:** Azahar removes or substantially redesigns RPC.

## D2 — Separate project, not an Azahar contribution
**Decision:** mcp-azahar lives in its own repo. Small, general-purpose
emulator-side improvements (e.g. an RPC screenshot command) are proposed
upstream separately.

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
  code. A working tool makes a much stronger case for an upstream
  screenshot command than a hypothetical.

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

## D4 — Screenshots via X11 capture for now
**Decision:** Capture screenshots by grabbing the Azahar window on an Xvfb
display and cropping to the top (400x240) and bottom (320x240) screens.

**Why:** Azahar's RPC has no screenshot command (as of 2127). X11 capture
works today, in containers and CI, with no emulator changes.

**Consequence:** Capture is Linux-first. The `Capturer` interface keeps
room for an `rpc` backend.

**Revisit if:** Azahar adds an RPC screenshot/framebuffer command
(we intend to propose one; draft in `docs/upstream-proposal.md`).

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

## Known unknowns (to resolve in PLAN.md Phase 1)
- Exact 2127 RPC wire format, payload limits, and transport details
- Config key/flag that enables the RPC server
- Whether pause, frame advance, reset or app load exist over RPC
- Whether a first-run setup dialog blocks homebrew launch in a fresh config
- Whether the Azahar AppImage runs cleanly under Xvfb with Mesa
