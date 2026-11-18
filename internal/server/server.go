// SPDX-License-Identifier: GPL-3.0-or-later

// Package server is the MCP front end (`mcp-azahar serve`): one tool per
// CLI command, each calling the same internal/ops function. Tools share
// the CLI's sessions, so a human can watch or poke the instance an agent
// drives.
package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/michvllni/mcp-azahar/internal/capture"
	"github.com/michvllni/mcp-azahar/internal/emu"
	"github.com/michvllni/mcp-azahar/internal/ops"
	"github.com/michvllni/mcp-azahar/internal/sequence"
	"github.com/michvllni/mcp-azahar/internal/session"
	"github.com/michvllni/mcp-azahar/internal/symbols"
)

// Spec describes a tool and the CLI command it mirrors.
type Spec struct {
	Name string
	// CLI is the command path of the CLI counterpart ("state save").
	CLI string
}

// Server holds the MCP server and the operations it calls.
type Server struct {
	ops     *ops.Ops
	session string // default session name
	allow   []string
	mcp     *mcp.Server
	specs   []Spec
}

// Options configure New.
type Options struct {
	// Version is reported to clients.
	Version string
	// Session is the default session for tools called without one.
	Session string
	// AllowDirs are the directories whose files tools may read (app, ELF,
	// log, sequence) or write (screenshot output). Default: the working
	// directory.
	AllowDirs []string
}

// New builds the server with all tools.
func New(o *ops.Ops, opt Options) *Server {
	if opt.Session == "" {
		opt.Session = session.DefaultName
	}
	s := &Server{ops: o, session: opt.Session}
	if len(opt.AllowDirs) == 0 {
		if wd, err := os.Getwd(); err == nil {
			opt.AllowDirs = []string{wd}
		}
	}
	for _, d := range opt.AllowDirs {
		if abs, err := filepath.Abs(d); err == nil {
			if resolved, err := filepath.EvalSymlinks(abs); err == nil {
				abs = resolved
			}
			s.allow = append(s.allow, abs)
		}
	}
	s.mcp = mcp.NewServer(&mcp.Implementation{Name: "mcp-azahar", Title: "Azahar 3DS emulator", Version: opt.Version},
		&mcp.ServerOptions{Instructions: instructions})
	s.register()
	return s
}

// Run serves MCP over stdin/stdout until the client disconnects or ctx
// ends.
func (s *Server) Run(ctx context.Context) error {
	return s.mcp.Run(ctx, &mcp.StdioTransport{})
}

// MCP returns the underlying server (for in-memory tests).
func (s *Server) MCP() *mcp.Server { return s.mcp }

// Specs lists the tools and their CLI counterparts.
func (s *Server) Specs() []Spec { return append([]Spec(nil), s.specs...) }

const instructions = `Drives the Azahar 3DS emulator to test homebrew: launch an app, send input,
take screenshots, read and write memory (by address or by variable name from
the app's ELF), wait for conditions, use save states, read the log and run
scripted test sequences.

Every tool acts on a session (optional "session" argument, default given by the
server). Start with azahar_status or azahar_sessions; if nothing runs, use
azahar_launch. Only one emulator with RPC can run per host. Sessions are shared
with the mcp-azahar CLI, and launched emulators keep running until
azahar_stop. Addresses are virtual addresses of the app process, as hex
("0x00101000") or decimal. Durations are in milliseconds. File paths (app,
elf, log, sequence file, screenshot output) must lie in the directories the
server allows (by default its working directory); relative paths are relative
to the first of them.`

// common is embedded in every tool input.
type common struct {
	Session string `json:"session,omitempty" jsonschema:"session name (default: the server's default session)"`
}

func (s *Server) name(c common) string {
	if c.Session != "" {
		return c.Session
	}
	return s.session
}

// path checks a file path the model gave against the allowed
// directories and returns it absolute. Symlinks are resolved, so a link
// cannot point out of them; for a file to be written (that may not exist
// yet) its directory is resolved.
func (s *Server) path(p string, write bool) (string, error) {
	if p == "" {
		return "", nil
	}
	if !filepath.IsAbs(p) && len(s.allow) > 0 {
		p = filepath.Join(s.allow[0], p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	switch {
	case err == nil:
	case write && errors.Is(err, os.ErrNotExist):
		if fi, lerr := os.Lstat(abs); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			// A dangling link: writing would create its target, wherever
			// that is.
			return "", fmt.Errorf("%w: %q is a symlink to a missing file", ops.ErrUsage, p)
		}
		dir, derr := filepath.EvalSymlinks(filepath.Dir(abs))
		if derr != nil {
			return "", derr
		}
		resolved = filepath.Join(dir, filepath.Base(abs))
	default:
		return "", err
	}
	for _, d := range s.allow {
		if rel, err := filepath.Rel(d, resolved); err == nil && filepath.IsLocal(rel) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("%w: path %q is outside the directories this server allows (%s); start `mcp-azahar serve --allow-dir DIR` to allow more",
		ops.ErrUsage, p, strings.Join(s.allow, ", "))
}

// paths checks several read paths in place.
func (s *Server) paths(ps ...*string) error {
	for _, p := range ps {
		v, err := s.path(*p, false)
		if err != nil {
			return err
		}
		*p = v
	}
	return nil
}

// toolError turns an ops error into a tool error message with its kind,
// so the model can tell bad arguments from failed checks and missing
// sessions.
func toolError(err error) error {
	switch {
	case errors.Is(err, ops.ErrCheckFailed):
		return err // "check failed: ..."
	case errors.Is(err, ops.ErrUsage):
		return err // "invalid arguments: ..."
	case errors.Is(err, session.ErrNotFound):
		return fmt.Errorf("no running session: %w (use azahar_sessions, azahar_launch or azahar_attach)", err)
	}
	return err
}

func ptr(b bool) *bool { return &b }

// jsonText renders v as compact JSON for text content and error messages.
func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// annotations: read-only tools are safe to call freely; others change
// the emulator but not the world outside it (except launch/stop/attach).
func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, OpenWorldHint: ptr(false)}
}

func changes(title string, destructive, idempotent bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, DestructiveHint: ptr(destructive), IdempotentHint: idempotent, OpenWorldHint: ptr(false)}
}

// add registers a tool whose result is returned as JSON.
func add[In any](s *Server, spec Spec, t *mcp.Tool, timeout time.Duration, h func(context.Context, In) (any, error)) {
	addRaw(s, spec, t, timeout, func(ctx context.Context, in In) (*mcp.CallToolResult, any, error) {
		out, err := h(ctx, in)
		return nil, out, err
	})
}

// addRaw registers a tool that may build its own result (images).
func addRaw[In any](s *Server, spec Spec, t *mcp.Tool, timeout time.Duration, h func(context.Context, In) (*mcp.CallToolResult, any, error)) {
	t.Name = spec.Name
	s.specs = append(s.specs, spec)
	mcp.AddTool(s.mcp, t, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (res *mcp.CallToolResult, out any, err error) {
		defer func() {
			// The SDK does not recover panics: one would end the server.
			if p := recover(); p != nil {
				res, out, err = nil, nil, fmt.Errorf("internal error in %s: %v", spec.Name, p)
			}
		}()
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		res, out, err = h(ctx, in)
		if err != nil {
			return nil, nil, toolError(err)
		}
		return res, out, nil
	})
}

// Limits that keep results within a model's context.
const (
	maxRead        = 4096     // bytes per azahar_read_memory
	maxValue       = 16 << 10 // bytes per azahar_read_symbol/wait_for value
	defaultLogTail = 200
	maxLogTail     = 1000
	maxSymbols     = 500
	maxScale       = 4 // 1600x960 per screen
)

func ms(v int) time.Duration { return time.Duration(v) * time.Millisecond }

const buttonHelp = "Buttons: A B X Y L R ZL ZR START SELECT UP DOWN LEFT RIGHT HOME POWER (case-insensitive)."

func (s *Server) register() {
	s.sessionTools()
	s.inputTools()
	s.screenTools()
	s.memoryTools()
	s.scriptingTools()
}

// --- sessions -------------------------------------------------------------

type launchIn struct {
	common
	App     string `json:"app" jsonschema:"path of the homebrew to boot (.3dsx .cci .3ds .cxi .app .elf)"`
	ELF     string `json:"elf,omitempty" jsonschema:"matching ELF with symbols, for azahar_read_symbol, azahar_wait_for and azahar_symbols"`
	Display string `json:"display,omitempty" jsonschema:"existing X display (e.g. :0) instead of a private Xvfb"`
}

type attachIn struct {
	common
	Address string `json:"address,omitempty" jsonschema:"RPC address host:port (default 127.0.0.1:45987)"`
	ELF     string `json:"elf,omitempty" jsonschema:"ELF with symbols"`
	Log     string `json:"log,omitempty" jsonschema:"Azahar's log file, for azahar_logs"`
}

type stopIn struct {
	common
	KeepFiles bool `json:"keep_files,omitempty" jsonschema:"keep the session directory (logs, save states)"`
}

type noArgs struct {
	common
}

func (s *Server) sessionTools() {
	add(s, Spec{"azahar_launch", "launch"}, &mcp.Tool{
		Description: "Start Azahar headless with an app in a new session and wait until its RPC server answers (up to 2 minutes). " +
			"Fails if the session exists or another emulator holds the RPC port. Linux/macOS only. The emulator keeps running until azahar_stop.",
		Annotations: changes("Launch emulator", false, false),
	}, 2*time.Minute, func(ctx context.Context, in launchIn) (any, error) {
		if err := s.paths(&in.App, &in.ELF); err != nil {
			return nil, err
		}
		return s.ops.Launch(ctx, s.name(in.common), emu.LaunchOptions{App: in.App, ELF: in.ELF, Display: in.Display})
	})
	add(s, Spec{"azahar_attach", "attach"}, &mcp.Tool{
		Description: "Register an Azahar started by hand (RPC server enabled, app loaded) as a session.",
		Annotations: &mcp.ToolAnnotations{Title: "Attach to emulator", DestructiveHint: ptr(false), OpenWorldHint: ptr(true)},
	}, 30*time.Second, func(ctx context.Context, in attachIn) (any, error) {
		if err := s.paths(&in.ELF, &in.Log); err != nil {
			return nil, err
		}
		return s.ops.Attach(ctx, s.name(in.common), emu.AttachOptions{Address: in.Address, ELF: in.ELF, LogPath: in.Log})
	})
	add(s, Spec{"azahar_stop", "stop"}, &mcp.Tool{
		Description: "Stop the session's emulator (launched sessions) or forget it (attached sessions). Deletes the session directory unless keep_files.",
		Annotations: changes("Stop emulator", true, true),
	}, 30*time.Second, func(ctx context.Context, in stopIn) (any, error) {
		name := s.name(in.common)
		if err := s.ops.Stop(ctx, name, in.KeepFiles); err != nil {
			return nil, err
		}
		return map[string]string{"stopped": name}, nil
	})
	add(s, Spec{"azahar_status", "status"}, &mcp.Tool{
		Description: "Show the session: whether the emulator runs and answers RPC, its processes, app, log file and held input. " +
			"A session that is not running is reported, not an error.",
		Annotations: readOnly("Session status"),
	}, 15*time.Second, func(ctx context.Context, in noArgs) (any, error) {
		st, err := s.ops.Status(ctx, s.name(in.common))
		if st.Session.Name != "" {
			return st, nil // running/answering/rpc_error say what is wrong
		}
		return nil, err
	})
	add(s, Spec{"azahar_sessions", "sessions"}, &mcp.Tool{
		Description: "List sessions (stale ones are removed).",
		Annotations: changes("List sessions", false, true),
	}, 15*time.Second, func(ctx context.Context, _ struct{}) (any, error) {
		list, err := s.ops.Sessions(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"sessions": list}, nil
	})
}

// --- input ----------------------------------------------------------------

type buttonsIn struct {
	common
	Buttons []string `json:"buttons" jsonschema:"button names"`
}

type pressIn struct {
	buttonsIn
	ForMS int `json:"for_ms,omitempty" jsonschema:"how long to press in milliseconds (default 100)"`
}

type releaseIn struct {
	common
	Buttons   []string `json:"buttons,omitempty" jsonschema:"buttons to release; with nothing named everything is released"`
	Touch     bool     `json:"touch,omitempty" jsonschema:"release the held touch"`
	CirclePad bool     `json:"circle_pad,omitempty" jsonschema:"center the held circle pad"`
}

type pointIn struct {
	common
	X     int  `json:"x" jsonschema:"x coordinate (range in the tool description)"`
	Y     int  `json:"y" jsonschema:"y coordinate (range in the tool description)"`
	Hold  bool `json:"hold,omitempty" jsonschema:"keep it until azahar_release"`
	ForMS int  `json:"for_ms,omitempty" jsonschema:"duration in milliseconds without hold (default 100)"`
}

func (s *Server) inputTools() {
	add(s, Spec{"azahar_press", "press"}, &mcp.Tool{
		Description: "Press buttons for for_ms (default 100 ms), then return to the held input. " + buttonHelp,
		Annotations: changes("Press buttons", false, false),
	}, 0, func(ctx context.Context, in pressIn) (any, error) {
		b, err := ops.ParseButtons(in.Buttons)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, max(ms(in.ForMS), 0)+30*time.Second)
		defer cancel()
		return s.ops.Press(ctx, s.name(in.common), b, ms(in.ForMS))
	})
	add(s, Spec{"azahar_hold", "hold"}, &mcp.Tool{
		Description: "Hold buttons until azahar_release; press, touch and circle pad apply on top. " + buttonHelp,
		Annotations: changes("Hold buttons", false, true),
	}, 30*time.Second, func(ctx context.Context, in buttonsIn) (any, error) {
		b, err := ops.ParseButtons(in.Buttons)
		if err != nil {
			return nil, err
		}
		return s.ops.Hold(ctx, s.name(in.common), b)
	})
	add(s, Spec{"azahar_release", "release"}, &mcp.Tool{
		Description: "Release held buttons, the held touch or circle pad; with nothing named, everything. " + buttonHelp,
		Annotations: changes("Release input", false, true),
	}, 30*time.Second, func(ctx context.Context, in releaseIn) (any, error) {
		b, err := ops.ParseButtons(in.Buttons)
		if err != nil {
			return nil, err
		}
		return s.ops.Release(ctx, s.name(in.common), ops.ReleaseWhat{Buttons: b, Touch: in.Touch, CirclePad: in.CirclePad})
	})
	add(s, Spec{"azahar_touch", "touch"}, &mcp.Tool{
		Description: "Touch the bottom screen at x 0-319, y 0-239 (pixels, origin top left): for for_ms (default 100 ms) or held until azahar_release.",
		Annotations: changes("Touch screen", false, false),
	}, 0, func(ctx context.Context, in pointIn) (any, error) {
		if in.Hold && in.ForMS != 0 {
			return nil, fmt.Errorf("%w: hold and for_ms exclude each other", ops.ErrUsage)
		}
		ctx, cancel := context.WithTimeout(ctx, max(ms(in.ForMS), 0)+30*time.Second)
		defer cancel()
		return s.ops.Touch(ctx, s.name(in.common), in.X, in.Y, in.Hold, ms(in.ForMS))
	})
	add(s, Spec{"azahar_circle_pad", "circle-pad"}, &mcp.Tool{
		Description: "Move the circle pad to x, y in -154..154 each (0, 0 is centered; x right, y up): for for_ms (default 100 ms) or held until azahar_release.",
		Annotations: changes("Move circle pad", false, false),
	}, 0, func(ctx context.Context, in pointIn) (any, error) {
		if in.Hold && in.ForMS != 0 {
			return nil, fmt.Errorf("%w: hold and for_ms exclude each other", ops.ErrUsage)
		}
		ctx, cancel := context.WithTimeout(ctx, max(ms(in.ForMS), 0)+30*time.Second)
		defer cancel()
		return s.ops.CirclePad(ctx, s.name(in.common), in.X, in.Y, in.Hold, ms(in.ForMS))
	})
}

// --- screens and states -----------------------------------------------------

type screenshotIn struct {
	common
	Screen string `json:"screen,omitempty" jsonschema:"top (default, 400x240), bottom (320x240) or both (two consecutive frames, in 3DS arrangement)"`
	Scale  *int   `json:"scale,omitempty" jsonschema:"resolution scale 1-4 (default 1); 0 is Azahar's internal resolution"`
	Split  bool   `json:"split,omitempty" jsonschema:"with screen both: one image per screen"`
	Output string `json:"output,omitempty" jsonschema:"also save the PNG to this file path"`
}

type slotIn struct {
	common
	Slot int `json:"slot" jsonschema:"save state slot 0-10"`
}

func (s *Server) screenTools() {
	addRaw(s, Spec{"azahar_screenshot", "screenshot"}, &mcp.Tool{
		Description: "Capture the next rendered frame of the top, bottom or both screens and return it as PNG image content. " +
			"With output the PNG is also written to that file (overwritten).",
		Annotations: changes("Screenshot", false, true),
	}, 90*time.Second, func(ctx context.Context, in screenshotIn) (*mcp.CallToolResult, any, error) {
		sc, err := capture.ParseScreens(in.Screen)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", ops.ErrUsage, err)
		}
		if in.Split && sc != capture.Both {
			return nil, nil, fmt.Errorf("%w: split needs screen both", ops.ErrUsage)
		}
		scale := 1
		if in.Scale != nil {
			scale = *in.Scale
		}
		if scale < 0 || scale > maxScale {
			return nil, nil, fmt.Errorf("%w: scale %d outside 0..%d", ops.ErrUsage, scale, maxScale)
		}
		out, err := s.path(in.Output, true)
		if err != nil {
			return nil, nil, err
		}
		shots, err := s.ops.Screenshot(ctx, s.name(in.common), capture.Request{Screens: sc, Scale: scale})
		if err != nil {
			return nil, nil, err
		}
		res := &mcp.CallToolResult{}
		info := map[string]any{}
		if in.Split || len(shots) == 1 {
			for _, sh := range shots {
				res.Content = append(res.Content, &mcp.ImageContent{Data: sh.PNG, MIMEType: "image/png"})
			}
			info["screens"] = shots
		} else {
			png, w, h, err := capture.Compose(shots)
			if err != nil {
				return nil, nil, err
			}
			res.Content = append(res.Content, &mcp.ImageContent{Data: png, MIMEType: "image/png"})
			info["screens"] = []map[string]any{{"screen": "both", "width": w, "height": h}}
		}
		if out != "" {
			files, err := ops.SaveScreenshot(shots, out, in.Split, func(p string) (string, error) { return s.path(p, true) })
			if err != nil {
				return nil, nil, err
			}
			info["files"] = files
		}
		res.Content = append(res.Content, &mcp.TextContent{Text: jsonText(info)})
		return res, nil, nil
	})
	add(s, Spec{"azahar_save_state", "state save"}, &mcp.Tool{
		Description: "Save the emulator state to slot 0-10 (overwrites it). For launched sessions it waits until the file is written.",
		Annotations: changes("Save state", true, false),
	}, time.Minute, func(ctx context.Context, in slotIn) (any, error) {
		if err := s.ops.SaveState(ctx, s.name(in.common), in.Slot); err != nil {
			return nil, err
		}
		return map[string]any{"saved": in.Slot}, nil
	})
	add(s, Spec{"azahar_load_state", "state load"}, &mcp.Tool{
		Description: "Load the state in slot 0-10 (it must exist). Held input is applied again afterwards.",
		Annotations: changes("Load state", true, false),
	}, time.Minute, func(ctx context.Context, in slotIn) (any, error) {
		if err := s.ops.LoadState(ctx, s.name(in.common), in.Slot); err != nil {
			return nil, err
		}
		return map[string]any{"loaded": in.Slot}, nil
	})
}

// --- memory and logs --------------------------------------------------------

type readIn struct {
	common
	Address string `json:"address" jsonschema:"virtual address, hex 0x... or decimal"`
	Length  int    `json:"length" jsonschema:"number of bytes, at most 4096 (unmapped memory reads as zeros)"`
}

type writeIn struct {
	common
	Address string `json:"address" jsonschema:"virtual address, hex 0x... or decimal"`
	Bytes   string `json:"bytes" jsonschema:"hex bytes in memory order, e.g. \"de ad be ef\" (0x1234 means bytes 12 34)"`
}

type logsIn struct {
	common
	Tail     int    `json:"tail,omitempty" jsonschema:"only the last N matching lines (default 200, at most 1000)"`
	From     int64  `json:"from,omitempty" jsonschema:"byte offset to start at: the offset of a previous call, to get only newer lines"`
	Contains string `json:"contains,omitempty" jsonschema:"only lines containing this text"`
}

func (s *Server) memoryTools() {
	add(s, Spec{"azahar_read_memory", "read"}, &mcp.Tool{
		Description: "Read bytes of the app's memory; returns them as hex. Prefer azahar_read_symbol when the app's ELF is known.",
		Annotations: readOnly("Read memory"),
	}, time.Minute, func(ctx context.Context, in readIn) (any, error) {
		addr, err := ops.ParseAddress(in.Address)
		if err != nil {
			return nil, err
		}
		if in.Length > maxRead {
			return nil, fmt.Errorf("%w: length %d over %d; read in parts", ops.ErrUsage, in.Length, maxRead)
		}
		b, err := s.ops.ReadMemory(ctx, s.name(in.common), addr, in.Length)
		if err != nil {
			return nil, err
		}
		return map[string]any{"address": fmt.Sprintf("0x%08x", addr), "length": len(b), "hex": hex.EncodeToString(b)}, nil
	})
	add(s, Spec{"azahar_write_memory", "write"}, &mcp.Tool{
		Description: "Write bytes to the app's memory and verify by reading back. Writes outside the app's image and heaps are ignored by Azahar and reported as an error.",
		Annotations: changes("Write memory", true, true),
	}, time.Minute, func(ctx context.Context, in writeIn) (any, error) {
		addr, err := ops.ParseAddress(in.Address)
		if err != nil {
			return nil, err
		}
		b, err := ops.ParseBytes(in.Bytes)
		if err != nil {
			return nil, err
		}
		if err := s.ops.WriteMemory(ctx, s.name(in.common), addr, b); err != nil {
			return nil, err
		}
		return map[string]any{"address": fmt.Sprintf("0x%08x", addr), "written": len(b)}, nil
	})
	add(s, Spec{"azahar_logs", "logs"}, &mcp.Tool{
		Description: "Return the last matching lines of Azahar's log (complete lines only) and the byte offset to pass as from next time, " +
			"to get only lines written since.",
		Annotations: readOnly("Read log"),
	}, time.Minute, func(ctx context.Context, in logsIn) (any, error) {
		switch {
		case in.Tail == 0:
			in.Tail = defaultLogTail
		case in.Tail > maxLogTail:
			return nil, fmt.Errorf("%w: tail %d over %d", ops.ErrUsage, in.Tail, maxLogTail)
		}
		return s.ops.Logs(ctx, s.name(in.common), ops.LogQuery{Tail: in.Tail, From: in.From, Contains: in.Contains})
	})
}

// --- symbols, waits and sequences -------------------------------------------

const targetHelp = `target is a variable of the app's ELF: a name, then .field and [index] parts ` +
	`("test_state.touch.x", "table[2]"); C++ "ns::name", static locals "func::name", ` +
	`a static sharing its name "file.c:name". Or an address (hex 0x... or decimal) with type ` +
	`(default u32). Types: u8 u16 u32 u64 s8 s16 s32 s64 f32 f64 bool string bytes ` +
	`(string/bytes need len for addresses).`

type valueIn struct {
	common
	Target string `json:"target" jsonschema:"variable expression or address"`
	Type   string `json:"type,omitempty" jsonschema:"decode as this type instead of the DWARF type"`
	Len    int    `json:"len,omitempty" jsonschema:"byte count for type string/bytes"`
	ELF    string `json:"elf,omitempty" jsonschema:"ELF file instead of the session's"`
}

func (v valueIn) opts() ops.ValueOptions {
	return ops.ValueOptions{ELF: v.ELF, Type: symbols.Scalar(v.Type), Len: v.Len, MaxSize: maxValue}
}

type waitIn struct {
	valueIn
	Op         string `json:"op,omitempty" jsonschema:"eq (default), ne, lt, le, gt, ge, or changed (no value)"`
	Value      string `json:"value,omitempty" jsonschema:"literal: number (decimal or 0x), true/false, string or enum name"`
	TimeoutMS  int    `json:"timeout_ms,omitempty" jsonschema:"how long to wait in milliseconds (default 10000)"`
	IntervalMS int    `json:"interval_ms,omitempty" jsonschema:"poll interval in milliseconds (default 50)"`
}

type symbolsIn struct {
	common
	Filter string `json:"filter,omitempty" jsonschema:"only names containing this text (case-insensitive)"`
	ELF    string `json:"elf,omitempty" jsonschema:"ELF file instead of the session's"`
}

type sequenceIn struct {
	common
	File   string `json:"file,omitempty" jsonschema:"sequence file (YAML or JSON); its paths are relative to it"`
	Script string `json:"script,omitempty" jsonschema:"sequence as YAML or JSON text instead of file; its paths are relative to the server's first allowed directory"`
	ELF    string `json:"elf,omitempty" jsonschema:"ELF for symbol steps instead of the sequence's or session's"`
}

const sequenceHelp = `Run a scripted test: steps, each with exactly one action and an optional name:
press (buttons, with for: 300ms), hold, release (buttons or "all"), touch {x, y, for|hold},
circle_pad {x, y, for|hold}, wait (duration), screenshot {screen, output, scale, split},
write {address, bytes}, assert {target, op, value, type, len}, wait_for {target, op, value,
timeout, interval}, save_state (slot), load_state (slot), log_contains {text, timeout}.
Durations like 100ms or 2s. The whole sequence is checked before the first step runs;
it stops at the first failure. log_contains only matches lines logged during the run.
File paths (elf, screenshot output) must be relative and stay inside the sequence's
directory. Example:
  steps:
    - press: A
    - wait_for: {target: test_state.frame, op: ge, value: 60, timeout: 5s}
    - assert: {target: test_state.last_buttons, value: 0}
    - screenshot: {screen: both, output: smoke.png}
The result has passed, and per step ok, error, error_kind (check or error) and duration_ms.`

func (s *Server) scriptingTools() {
	add(s, Spec{"azahar_symbols", "symbols"}, &mcp.Tool{
		Description: "List variables and functions of the app's ELF (address, size, kind, C type), sorted by name; at most 500, use filter to narrow.",
		Annotations: readOnly("List symbols"),
	}, 30*time.Second, func(ctx context.Context, in symbolsIn) (any, error) {
		if err := s.paths(&in.ELF); err != nil {
			return nil, err
		}
		list, err := s.ops.Symbols(ctx, s.name(in.common), in.ELF, in.Filter)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"symbols": list, "total": len(list)}
		if len(list) > maxSymbols {
			out["symbols"], out["truncated"] = list[:maxSymbols], true
		}
		return out, nil
	})
	add(s, Spec{"azahar_read_symbol", "read-symbol"}, &mcp.Tool{
		Description: "Read a variable (or address) and decode it by its C type: numbers, bools, enums by name, plain char arrays as text, structs and arrays as JSON. " +
			"Values over 16 KiB are refused: read a field or element. " + targetHelp,
		Annotations: readOnly("Read variable"),
	}, 30*time.Second, func(ctx context.Context, in valueIn) (any, error) {
		if err := s.paths(&in.ELF); err != nil {
			return nil, err
		}
		return s.ops.ReadValue(ctx, s.name(in.common), in.Target, in.opts())
	})
	add(s, Spec{"azahar_wait_for", "wait-for"}, &mcp.Tool{
		Description: "Poll a variable (or address) until it compares true with value, or until timeout_ms (a check failure with the last value). " +
			"Numbers compare numerically; bools, strings and enum names with eq/ne; changed waits for any change from the first read. " + targetHelp,
		Annotations: readOnly("Wait for value"),
	}, 0, func(ctx context.Context, in waitIn) (any, error) {
		if err := s.paths(&in.ELF); err != nil {
			return nil, err
		}
		timeout := ms(in.TimeoutMS)
		if timeout <= 0 {
			timeout = ops.DefaultWait
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		r, err := s.ops.WaitFor(ctx, s.name(in.common), in.Target, ops.Condition{Op: in.Op, Value: in.Value}, in.opts(), ms(in.IntervalMS))
		if err != nil && r != nil && errors.Is(err, ops.ErrCheckFailed) {
			return nil, fmt.Errorf("%w (%s)", err, jsonText(r))
		}
		return r, err
	})
	add(s, Spec{"azahar_sequence", "sequence"}, &mcp.Tool{
		Description: sequenceHelp,
		Annotations: changes("Run sequence", true, false),
	}, 0, func(ctx context.Context, in sequenceIn) (any, error) {
		if (in.File == "") == (in.Script == "") {
			return nil, fmt.Errorf("%w: give either file or script", ops.ErrUsage)
		}
		if err := s.paths(&in.File, &in.ELF); err != nil {
			return nil, err
		}
		var src *strings.Reader
		dir := "."
		if len(s.allow) > 0 {
			dir = s.allow[0]
		}
		if in.File != "" {
			b, err := os.ReadFile(in.File)
			if err != nil {
				return nil, err
			}
			src, dir = strings.NewReader(string(b)), filepath.Dir(in.File)
		} else {
			src = strings.NewReader(in.Script)
		}
		f, err := sequence.Parse(src)
		if err != nil {
			return nil, err
		}
		rep, err := sequence.Run(ctx, s.ops, s.name(in.common), f, sequence.Options{Dir: dir, ELF: in.ELF, RestrictPaths: true, CheckPath: s.path})
		if rep == nil {
			return nil, err
		}
		if err != nil {
			// The report is the useful part: return it as the error text.
			return nil, fmt.Errorf("%w\n%s", err, jsonText(rep))
		}
		return rep, nil
	})
}
