# Azahar RPC protocol (2127)

The contract for `internal/rpc` (PLAN Phase 2). Derived from Azahar source,
not from older Citra clients, and checked against a running Azahar.

- **Source:** azahar-emu/azahar tag `2127.0-rc3`, commit
  `11eceda5e0e682a1f44e396bf954a1b9137097ca`. References below are
  `path:line` at that commit.
- **Verified (2026-11-18)** against the 2127.0-rc3 AppImage under Xvfb with
  a hand-made 3DSX (an ARM `b .` loop plus marker words). Statements marked
  *observed* come from that test, all others from the source.
- **Reference client:** `dist/scripting/AzaharRPC.py` (same commit), the
  official Python client (GPLv2-or-later, compatible with this project). If
  code is ported from it, keep its copyright notice and name the source file
  and commit (AGENTS.md).

## 1. Enabling the server

| Topic | Fact | Source |
| --- | --- | --- |
| Build flags | `ENABLE_SCRIPTING` (default ON), `ENABLE_SCRIPTING_SYNC` (default ON), `ENABLE_FREECAM` (ON with Qt). The Linux AppImage uses the defaults, so all three are on; the log prints `Starting RPC server (synchronous).` | `CMakeLists.txt:137-139`, `.ci/linux.sh:22-28` |
| Config key | `[Debugging] enable_rpc_server=true` in `qt-config.ini`, default `false` | `src/common/settings.h:699`, `src/citra_qt/configuration/config.cpp:525,536` |
| `\default` flag | If `enable_rpc_server\default=true` is present, the stored value is **ignored** and the default (`false`) is used. Seed configs must omit `\default` or set it to `false`. Applies to every setting. | `config.cpp:1489-1497` |
| UI | Checkbox under Configure → Debug (hidden in per-game config) | `configure_debug.cpp:136-139,215` |
| Lifetime | Created at the **end of `System::Load`**, i.e. only once an app is running; nothing listens on the empty main window. Destroyed first in `System::Shutdown`. | `src/core/core.cpp:702-707,802-805` |
| Load state | Loading a state shuts the system down and loads it again, which **recreates the RPC server**: requests in flight are lost and the selected process resets (*observed*: first request after a load timed out once; `GetProcess` returned `0xFFFFFFFF` afterwards). | `core.cpp:802-805`, AzaharRPC.py `load_state` docstring |

## 2. Transport

- **UDP over IPv4**, one request per datagram, one reply per request. The
  reply is sent to the source address/port of the request
  (`src/core/rpc/udp_server.cpp:64-65,78-87`).
- **Port 45987, hard-coded; bound to `0.0.0.0` (all interfaces)**
  (`udp_server.cpp:17-19`, comment: "TODO: Make configurable"). *Observed*
  with `ss -ulpn`: `0.0.0.0:45987`. Consequences:
  - Two Azahar instances in the same network namespace cannot both serve
    RPC. The second fails to bind and logs `Error starting UDP server`
    (`server.cpp:18-22`); not tested.
  - The unauthenticated server is reachable from the network unless a
    firewall or network namespace prevents it.
- No authentication, no encryption, no handshake, no version negotiation.
- Socket buffers are raised to at least 2× (send) / 4× (receive) the maximum
  packet size (`udp_server.cpp:22-23`). Clients should do the same
  (AzaharRPC.py and `internal/rpc` do 2× for both).
- While Azahar recreates the server (LoadState), the port is closed: a
  connected UDP client gets "connection refused" (ICMP) instead of silence
  and must treat it like a lost reply.

## 3. Packet format

All integers are **little-endian**. Every datagram is a 16-byte header plus
`size` bytes of data.

| Offset | Type | Field | Request | Reply |
| --- | --- | --- | --- | --- |
| 0x00 | u32 | `version` | must be `2` | copied from the request |
| 0x04 | u32 | `id` | chosen by the client | copied from the request |
| 0x08 | u32 | `type` | request type (§5) | copied from the request |
| 0x0C | u32 | `size` | length of the data | length of the reply data |
| 0x10 | u8[size] | data | request arguments | reply payload |

Source: `src/core/rpc/packet.h:58-63` (header), `packet.h:124-128`
(constants), `udp_server.cpp:56-76` (receive), `udp_server.cpp:78-84`
(reply).

| Constant | Value | Source |
| --- | --- | --- |
| `CURRENT_VERSION` | 2 | `packet.h:124` |
| Header size | 16 | `packet.h:125` |
| `MAX_PACKET_DATA_SIZE` | 32768 | `packet.h:126` |
| Max datagram | 32784 | `packet.h:127` |
| `MAX_READ_SIZE` | 32768 | `packet.h:128` |

## 4. Reply semantics and errors

There are no error codes in the header. A request ends in one of three ways:

1. **Dropped, no reply.** The datagram is shorter than 16 bytes, longer than
   32784 bytes, or `16 + size` differs from its length
   (`udp_server.cpp:59-62,72-73`; *observed* for a short datagram and a size
   mismatch). The client only sees a timeout.
2. **Empty reply** (`size = 0`), header otherwise echoed. This happens when
   - `version != 2` (the reply echoes the wrong version; *observed*),
   - `type` is unknown or `SetFreeCam` without `ENABLE_FREECAM`,
   - the data is shorter than the type's minimum (8 bytes for most types,
     16 for `SetInput`, 72 for `SetFreeCam`),
   - the arguments are out of range (e.g. read size 0 or > 32768; *observed*)
   (`rpc_server.cpp:349-382,465-469,472-478`).
   Some valid requests also reply empty (`WriteMemory`, `SetInput`, set
   process), so **an empty reply means failure only for request types that
   normally return data.**
3. **Normal reply** as described per type below.

Matching: replies carry the request's `id` and `type`. Clients must ignore
datagrams whose `version`/`id`/`type` don't match or whose length isn't
`16 + size`, since late replies to earlier (retried) requests arrive on the
same socket (AzaharRPC.py `_read_and_validate_header`).

Retries: UDP can drop packets. Every request **except `TakeScreenshot`,
`SaveState` and `LoadState`** is safe to resend:
- a resent `TakeScreenshot` gets `Busy` while the first is still running;
- save/load are side effects that should not run twice.

The Python client uses a 20 s timeout without resending for save/load, and
waits ≥ 30 s for a screenshot without resending.

### Processing model

- One receive thread queues requests; one handler thread processes them in
  order (`rpc_server.cpp:499-511`).
- In synchronous mode (the AppImage), every request except `ReadScreenshot`
  is handed to the **emulation thread** and executed between CPU slices
  (`rpc_server.cpp:480-485`, `core.cpp:330-334`). Memory access is then
  consistent with the emulated state.
- **Requests are not processed while emulation is stopped** (*observed*
  with Azahar's modal "Save/load Error" dialog, which stops emulation until
  dismissed): no reply at all, not even to `SetGetProcess`, and
  TakeScreenshot times out. Expected for any pause in synchronous mode,
  since the requests wait for the emulation thread; a user pause was not
  tested.

## 5. Request types

`type` values from `packet.h:15-28`. "a1, a2" are the first two u32s of the
request data (`rpc_server.cpp:398-402`). Addresses are virtual addresses of
the selected process (§5.4).

| Type | Name | Request data | Reply data |
| --- | --- | --- | --- |
| 1 | ReadMemory | a1 = address, a2 = size (1..32768) | `size` bytes, or empty |
| 2 | WriteMemory | a1 = address, a2 = size (1..32760), then `size` bytes | always empty |
| 3 | ProcessList | a1 = start index, a2 = max count | u32 count + count × ProcessInfo |
| 4 | SetGetProcess | a1 = 0 get / other set, a2 = pid | get: u32 pid; set: empty |
| 5 | TakeScreenshot | a1 = res scale (0..10), a2 = flags | ScreenshotInfo (16 bytes) |
| 6 | ReadScreenshot | a1 = offset, a2 = size (1..32768) | up to `size` bytes, or empty |
| 7 | GetPerfStats | a1 = mode (0 last, 1 get+reset), a2 unused | PerfStatsInfo (0x54 bytes), or empty |
| 8 | SetFreeCam | u32 flags, u32 reserved, 16 × f32 matrix | empty (out of scope) |
| 9 | SaveState | a1 = slot (0..10), a2 unused | u32 accepted (1/0) |
| 10 | LoadState | a1 = slot (0..10), a2 unused | u32 accepted (1/0) |
| 11 | SetInput | InputInfo (16 bytes) | empty |

### 5.1 ReadMemory (1)
`rpc_server.cpp:54-77,405-410`
- Reads `size` bytes at `address` from the selected process. With no process
  selected (`0xFFFFFFFF`), reads through the current page table and logs
  `No target process selected, memory access may be invalid.` on every
  request (*observed*).
- Reply is empty if the selected pid no longer exists.
- **Unmapped addresses read as zeros with a normal reply** (*observed*
  at `0x08000000` with no heap mapped); the reply cannot tell "zero" from
  "unmapped".
- Larger reads must be split into ≤ 32768-byte chunks (*observed*: 64 KiB in
  two requests).

### 5.2 WriteMemory (2)
`rpc_server.cpp:79-106,411-418`, regions `src/core/memory.h:192-219`
- Data layout: `address u32, size u32, bytes[size]`; `size ≤ 32760` and
  `size ≤ data length − 8`.
- Writes only if the **start address** lies in one of these ranges (end
  inclusive, the end of the write is not checked):

  | Region | Range |
  | --- | --- |
  | Process image | `0x00100000`–`0x04000000` |
  | Heap | `0x08000000`–`0x10000000` |
  | Linear heap | `0x14000000`–`0x1C000000` |
  | N3DS extra RAM | `0x1E800000`–`0x1EC00000` |

- **Always replies empty, also when the address is outside these ranges
  and nothing is written.** Clients must verify writes by reading back.
- Invalidates the JIT cache for the range, so code patches take effect
  (*observed*: code segment write read back correctly).

### 5.3 ProcessList (3)
`rpc_server.cpp:108-133`, `packet.h:66-71,130`
- Reply: `count u32`, then `count` packed 0x14-byte entries:
  `pid u32, title_id u64, name u8[8]` (NUL-padded ASCII; codeset name).
- At most 1638 entries per reply; page with the start index until
  `count == 0`.
- *Observed* for the 3DSX test app: `{11: (title_id 0, "min")}`; the name
  is the file name truncated to 8 bytes (*guess*, from one sample).

### 5.4 SetGetProcess (4)
`rpc_server.cpp:135-150`, `rpc_server.h:69`
- Get (`a1 = 0`): reply `pid u32`; default `0xFFFFFFFF` = none.
- Set (`a1 ≠ 0`): selects `a2`, empty reply; no validation.
- The selection is server state: shared by all clients, and **reset when the
  server is recreated (load state)**.

### 5.5 TakeScreenshot (5) and ReadScreenshot (6)
`rpc_server.cpp:152-276`, `packet.h:74-91,111-117`
- Request: `res_scale` (0 = current internal resolution, 1..10), `flags`:
  bit 0 `SecondaryWindow`, bit 1 `RawRGB`. Other bits or scale > 10 →
  `InvalidArgument` (*observed*).
- Captures the **next rendered frame**, then replies with ScreenshotInfo:
  `result u32, width u32, height u32, data_size u32`.

  | `result` | Meaning |
  | --- | --- |
  | 0 Success | data ready |
  | 1 Unsupported | software renderer active |
  | 2 Busy | another screenshot in progress |
  | 3 Timeout | no frame within 10 s (e.g. paused) |
  | 4 EncodeFailed | PNG encoding failed |
  | 5 InvalidArgument | bad scale or flags |

- Data is PNG (RGB8), or raw RGB888 rows top to bottom with `RawRGB`. Fetch
  it with ReadScreenshot in ≤ 32768-byte chunks at increasing offsets.
  ReadScreenshot replies empty while a capture is in progress or the offset
  is past the end. The buffer holds only the last screenshot.
- The image follows the **screen layout setting**
  (`src/core/frontend/framebuffer_layout.cpp:425`). With
  `[Layout] layout_option=1` (SingleScreen) and `swap_screen=false`:
  primary = top screen 400×240, `SecondaryWindow` = bottom screen 320×240,
  times `res_scale` (*observed* for scales 0, 1, 2). With the default
  layout (0), primary is 400×480 (top above bottom, *observed*).
- Typical time locally: 6–35 ms (*observed*).

### 5.6 GetPerfStats (7)
`rpc_server.cpp:297-322`, `packet.h:93-108,119-122`
- Reply (packed, 0x54 bytes): 10 × f64 (`system_fps, game_fps,
  time_vblank_interval, time_hle_svc, time_hle_ipc, time_gpu, time_swap,
  time_remaining, emulation_speed, artic_transmitted`) + u32 `artic_events`.
- Mode 1 resets the interval (the Qt frontend then skips its own display of
  that interval). Unknown modes reply empty.

### 5.7 SaveState (9) and LoadState (10)
`rpc_server.cpp:324-334`, `src/core/savestate.h:25`, `savestate.cpp:41-50`
- Slots 0..10 (`SaveStateSlotCount = 11`); slot 0 is hidden in the Qt menus.
- Reply `accepted u32`: 1 if the emulator was running and the signal was
  queued; 0 for an invalid slot (*observed* for slot 11), no app, or another
  save/load still pending (`core.cpp:347-356`). **Accepted is not done:**
  the save or load runs afterwards on the emulation thread.
- **A load from an empty slot is accepted** and fails afterwards
  (`core.cpp:167-170`, `ErrorSavestate`). The Qt frontend then stops
  emulation and shows a modal "Save/load Error" dialog until someone clicks
  OK (`src/citra_qt/citra_qt.cpp:4243-4275`); meanwhile RPC answers nothing
  (*observed*, §4). A state from another Azahar build gives a similar
  dialog. Automation must check that the state file exists before loading;
  `internal/rpc` reports `ErrStateNotLoaded` when no reload follows.
- Files: `<user>/states/<title_id %016X>.<slot %02d>.cst` (with a movie
  playing: `<title_id>.movie<movie_id>.<slot>.cst`). *Observed*:
  `states/0000000000000000.01.cst`.
- After LoadState, expect one lost request and re-select the process
  (§1). *Observed*: with the minimal test app, a LoadState including the
  server restart and re-selection took 0.83 s (Phase 2 live test); larger
  apps will take longer.
- **Not verified:** whether the SetInput override survives a load.

### 5.8 SetInput (11)
`rpc_server.cpp:278-295`, `packet.h:37-56`,
`src/core/hle/service/hid/hid.cpp:220-241,303-307`, `hid.h:362,392`

InputInfo (16 bytes):

| Offset | Type | Field |
| --- | --- | --- |
| 0x00 | u32 | `buttons`: HID pad bits |
| 0x04 | u32 | `flags`: bit 0 HOME, bit 1 POWER, bit 2 touch, bit 3 circle pad |
| 0x08 | u16 | `touch_x`: 0..319 (clamped) |
| 0x0A | u16 | `touch_y`: 0..239 (clamped) |
| 0x0C | s16 | `circle_pad_x`: −0x9A..0x9A (clamped) |
| 0x0E | s16 | `circle_pad_y`: −0x9A..0x9A (clamped) |

Button bits (AzaharRPC.py `BUTTONS`): A 0, B 1, SELECT 2, START 3,
RIGHT 4, LEFT 5, UP 6, DOWN 7, R 8, L 9, X 10, Y 11, ZL 14, ZR 15.

- The input is **held** and applied at every HID update until the next
  SetInput; send all zeros to release everything.
- Buttons are OR-ed with the configured input devices.
- Touch applies only while no real touch is pressed.
- The circle pad replaces the device value and is averaged over 3 samples.
- HID updates run at 234 Hz of emulated time, so a press must be held for
  at least one update (~4.3 ms emulated) to be seen.
- HOME/POWER are consumed by the applet manager
  (`src/core/hle/service/apt/applet_manager.cpp:1752`).
- The effect on a running app was not verified (the test app reads no
  input). The Phase 7 fixture covers this.

### 5.9 SetFreeCam (8)
Free camera matrix for 3D rendering (`rpc_server.cpp:336-347`); out of
scope for mcp-azahar.

## 6. Not available over RPC

Pause/resume, frame advance, reset, loading an app, shutdown, and log access
have no RPC request. mcp-azahar has to use other means:
- **Load an app:** pass its path on the command line (§7).
- **Read logs:** read the log file (§7).
- **Stop Azahar:** send a signal (§7).
- Pause, frame advance and reset are candidates for an upstream proposal.

## 7. Process, files and configuration (Linux)

### Command line
From `azahar -h` (*observed*): `azahar [options] <file path>` boots an
app. Options:
- `-g/--gdbport`, `-i/--install` (CIA)
- `-f/--fullscreen`, `-w/--windowed`
- `-d/--dump-video`, `-p/-r/-a` (movies)
- `-c/-x` (Z3DS), `--room`, `-v`, `-h`

There is no option for the user directory or the RPC port.

### User directory
`src/common/file_util.cpp:935` (`SetUserPath`), `common_paths.h`

1. If `./user/` exists in the **current working directory**, it is the user
   directory (portable mode), containing `config/`, `log/`, `states/`,
   `sdmc/`, `nand/`, `cache/`, …
2. Otherwise data goes to `$XDG_DATA_HOME/azahar-emu/`, config to
   `$XDG_CONFIG_HOME/azahar-emu/`, cache to `$XDG_CACHE_HOME/azahar-emu/`.
   If old Citra/Lime3DS directories exist there, a **modal migration prompt**
   appears (`src/citra_qt/user_data_migration.cpp:27-52`).

Portable mode with a fresh per-session directory isolates sessions and
avoids the migration prompt (*observed*).

- Config: `<user>/config/qt-config.ini`
- Log: `<user>/log/azahar_log.txt` (`LOG_FILE`, `common_paths.h:74`; *observed*)

### Seed config for automation
Write this to `<user>/config/qt-config.ini` before the first start. Missing
keys get defaults, and Azahar writes the full file back.
```ini
[Debugging]
enable_rpc_server=true
[Layout]
layout_option=1
swap_screen=false
[Miscellaneous]
check_for_update_on_start=false
[UI]
confirmClose=false
```
- `enable_rpc_server`: §1.
- `layout_option=1`: single-screen layout, so screenshots are exactly one
  screen (§5.5).
- `check_for_update_on_start=false`: the update checker is compiled into
  tag builds (`.ci/linux.sh:17-19`), queries GitHub at start and shows a
  **modal dialog** if a newer release exists (`src/citra_qt/citra_qt.cpp:526-544,4581-4588`).
- `confirmClose=false`: no confirmation dialog on close
  (`src/citra_qt/uisettings.h:89`).

### Dialogs that can block an unattended start
- **No first-run dialog** with an empty user directory (*observed*).
- Update prompt: disabled by the seed config.
- Legacy-data migration prompt: avoided by portable mode.
- **`.cia` files:** "must be installed before usage" question
  (`citra_qt.cpp:1609`). Install first with `-i`, or only boot `.3dsx`/`.cci`.
- Load errors (invalid format, encrypted, …) are modal error dialogs
  (`citra_qt.cpp:1504-1560`); the process keeps running without an app and
  RPC never starts. Treat "RPC not answering after N seconds" as a failed
  launch and read the log.

### Display
- The AppImage bundles only the `xcb` and `wayland` Qt platform plugins;
  `QT_QPA_PLATFORM=offscreen` aborts (*observed*). An X server (Xvfb) is
  required.
- OpenGL via Mesa llvmpipe works; RPC screenshots need a hardware renderer
  backend (OpenGL/Vulkan), not the software renderer (§5.5).

### Signals
*observed*: SIGINT and SIGTERM do not stop Azahar; **SIGHUP terminates
it**; SIGKILL works. Azahar's own code installs handlers only for SIGSEGV
and SIGABRT (`src/common/logging/backend.cpp:437-438`). *Guess:* SDL2's
default SIGINT/SIGTERM handlers turn the signals into SDL quit events that
the Qt frontend ignores. Graceful stop for mcp-azahar: SIGHUP, wait, then
SIGKILL.

### Network namespaces (parallel sessions)
*observed* in the dev container: two instances, each started with
`unshare --user --map-root-user --net` (loopback brought up inside), ran in
parallel. Each had its own `127.0.0.1:45987` and they shared one Xvfb
display. Nothing was bound on the host. A client must run inside the same
namespace, or reach it through a relay (Phase 3 design).

*To verify:* Ubuntu 24.04 (GitHub runners) restricts unprivileged user
namespaces through AppArmor by default.

## 8. Implications for later phases

- **Phase 2 (`internal/rpc`):**
  - per-request timeouts;
  - retries for idempotent types only;
  - reply matching by id+type;
  - read-back verification for writes;
  - chunked reads and writes;
  - ScreenshotInfo result codes mapped to errors;
  - one retry and process re-selection after LoadState.
- **Phase 3 (sessions):**
  - one RPC port per network namespace; parallel sessions need namespaces
    or must be serialized (decision needed);
  - stop with SIGHUP → SIGKILL;
  - launch is done when RPC answers.
- **Phase 4 (screenshots):** Azahar 2127 **has** an RPC screenshot command,
  which contradicts DECISIONS.md D4's premise. Proposal: use the RPC
  backend as the default and keep X11 capture as a fallback, or drop it
  (decision needed).
- **Upstream proposal** (`docs/upstream-proposal.md`, later):
  - configurable RPC port and bind address (default 127.0.0.1);
  - status codes in replies;
  - pause/resume, frame advance, reset and shutdown requests.
