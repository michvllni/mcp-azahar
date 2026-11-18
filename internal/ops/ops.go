// SPDX-License-Identifier: GPL-3.0-or-later

// Package ops implements every user-facing operation once. The CLI
// (cmd/mcp-azahar) and the MCP server are thin front ends over it, so a
// feature behaves the same in both (PLAN.md, D3).
package ops

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/michvllni/mcp-azahar/internal/capture"
	"github.com/michvllni/mcp-azahar/internal/emu"
	"github.com/michvllni/mcp-azahar/internal/rpc"
	"github.com/michvllni/mcp-azahar/internal/session"
)

// Errors that front ends map to exit codes or tool errors.
var (
	// ErrCheckFailed means a check did not pass (wait-for timed out, a
	// sequence assertion failed): a test failure, not a tool failure.
	ErrCheckFailed = errors.New("check failed")
	// ErrUsage means invalid arguments.
	ErrUsage = errors.New("invalid arguments")
)

// Ops runs operations against sessions.
type Ops struct{ M *emu.Manager }

// New opens the default session store.
func New() (*Ops, error) {
	dir, err := session.DefaultDir()
	if err != nil {
		return nil, err
	}
	st, err := session.Open(dir)
	if err != nil {
		return nil, err
	}
	return &Ops{M: &emu.Manager{Store: st}}, nil
}

func usage(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrUsage, fmt.Sprintf(format, a...))
}

// --- sessions -------------------------------------------------------------

// Launch starts Azahar with an app in a new session.
func (o *Ops) Launch(ctx context.Context, name string, opts emu.LaunchOptions) (*session.Session, error) {
	return o.M.Launch(ctx, name, opts)
}

// Attach registers an Azahar started by hand.
func (o *Ops) Attach(ctx context.Context, name string, opts emu.AttachOptions) (*session.Session, error) {
	return o.M.Attach(ctx, name, opts)
}

// Stop ends a session.
func (o *Ops) Stop(ctx context.Context, name string, keepFiles bool) error {
	return o.M.Stop(ctx, name, keepFiles)
}

// Status reports a session's state.
func (o *Ops) Status(ctx context.Context, name string) (*emu.Status, error) {
	return o.M.Status(ctx, name)
}

// SessionInfo is one entry of Sessions.
type SessionInfo struct {
	Name    string       `json:"name"`
	Kind    session.Kind `json:"kind"`
	Running bool         `json:"running"`
	RPC     string       `json:"rpc"`
	App     string       `json:"app,omitempty"`
	Display string       `json:"display,omitempty"`
}

// Sessions lists sessions after removing stale ones.
func (o *Ops) Sessions(ctx context.Context) ([]SessionInfo, error) {
	if _, err := o.M.Prune(ctx); err != nil {
		return nil, err
	}
	list, err := o.M.Store.List()
	if err != nil {
		return nil, err
	}
	out := make([]SessionInfo, 0, len(list))
	for _, s := range list {
		out = append(out, SessionInfo{
			Name: s.Name, Kind: s.Kind, Running: s.Running(), RPC: s.RPC.Address,
			App: s.AppPath, Display: s.Display,
		})
	}
	return out, nil
}

// client opens an RPC client for a session.
func (o *Ops) client(ctx context.Context, name string) (*session.Session, *rpc.Client, error) {
	return o.M.Client(ctx, name)
}

// --- input ----------------------------------------------------------------

// DefaultPress is how long press holds buttons: several HID updates
// (234 Hz emulated, PROTOCOL.md §5.8) and at least two frames.
const DefaultPress = 100 * time.Millisecond

// ParseButtons parses button names; "home" and "power" set the flags.
func ParseButtons(names []string) (rpc.Input, error) {
	var in rpc.Input
	for _, n := range names {
		switch strings.ToUpper(strings.TrimSpace(n)) {
		case "HOME":
			in.Home = true
		case "POWER":
			in.Power = true
		default:
			b, err := rpc.ParseButton(n)
			if err != nil {
				return in, usage("%v", err)
			}
			in.Buttons |= b
		}
	}
	return in, nil
}

// InputResult reports the input state after an input operation.
type InputResult struct {
	// Held is what stays held after the operation.
	Held rpc.Input `json:"held"`
	// HeldButtons lists Held.Buttons by name.
	HeldButtons []string `json:"held_buttons,omitempty"`
}

func inputResult(held rpc.Input) *InputResult {
	return &InputResult{Held: held, HeldButtons: held.Buttons.Names()}
}

// Press applies in on top of the held input for d (DefaultPress if 0),
// then returns to the held input.
func (o *Ops) Press(ctx context.Context, name string, in rpc.Input, d time.Duration) (*InputResult, error) {
	if in.IsZero() {
		return nil, usage("nothing to press")
	}
	if err := in.Validate(); err != nil {
		return nil, usage("%v", err)
	}
	if d <= 0 {
		d = DefaultPress
	}
	s, c, err := o.client(ctx, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	if err := c.SetInput(ctx, s.Held.Merge(in)); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
	// Restore the held input even if ctx ended: never leave a press stuck.
	if err := c.SetInput(context.WithoutCancel(ctx), s.Held); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return inputResult(s.Held), nil
}

// Hold adds in to the held input until Release.
func (o *Ops) Hold(ctx context.Context, name string, in rpc.Input) (*InputResult, error) {
	if in.IsZero() {
		return nil, usage("nothing to hold")
	}
	if err := in.Validate(); err != nil {
		return nil, usage("%v", err)
	}
	return o.setHeld(ctx, name, func(h rpc.Input) rpc.Input { return h.Merge(in) })
}

// ReleaseWhat selects what Release lets go of; the zero value releases
// everything.
type ReleaseWhat struct {
	// Buttons (and HOME/POWER flags) to release.
	Buttons rpc.Input
	// Touch and CirclePad release the held touch or circle pad position.
	Touch, CirclePad bool
}

// Release lets go of held input: everything if what is empty.
func (o *Ops) Release(ctx context.Context, name string, what ReleaseWhat) (*InputResult, error) {
	all := what.Buttons.IsZero() && !what.Touch && !what.CirclePad
	return o.setHeld(ctx, name, func(h rpc.Input) rpc.Input {
		if all {
			return rpc.Input{}
		}
		h.Buttons &^= what.Buttons.Buttons
		h.Home = h.Home && !what.Buttons.Home
		h.Power = h.Power && !what.Buttons.Power
		if what.Touch {
			h.Touch = nil
		}
		if what.CirclePad {
			h.CirclePad = nil
		}
		return h
	})
}

// setHeld updates the held input in the session file and in Azahar.
func (o *Ops) setHeld(ctx context.Context, name string, fn func(rpc.Input) rpc.Input) (*InputResult, error) {
	_, c, err := o.client(ctx, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	var held rpc.Input
	_, err = o.M.Update(ctx, name, func(s *session.Session) error {
		held = fn(s.Held)
		if err := c.SetInput(ctx, held); err != nil {
			return err
		}
		s.Held = held
		return nil
	})
	if err != nil {
		return nil, err
	}
	return inputResult(held), nil
}

// Touch touches the bottom screen at (x, y): briefly (d, DefaultPress if
// 0), or held until Release if hold.
func (o *Ops) Touch(ctx context.Context, name string, x, y int, hold bool, d time.Duration) (*InputResult, error) {
	if x < 0 || y < 0 || x > rpc.TouchMaxX || y > rpc.TouchMaxY {
		return nil, usage("touch (%d, %d) outside the bottom screen (0..%d, 0..%d)", x, y, rpc.TouchMaxX, rpc.TouchMaxY)
	}
	in := rpc.Input{Touch: &rpc.Point{X: uint16(x), Y: uint16(y)}}
	if hold {
		return o.Hold(ctx, name, in)
	}
	return o.Press(ctx, name, in, d)
}

// CirclePad moves the circle pad to (x, y) in -154..154 (0x9A): briefly
// or held until Release.
func (o *Ops) CirclePad(ctx context.Context, name string, x, y int, hold bool, d time.Duration) (*InputResult, error) {
	if x < -rpc.CirclePadMax || x > rpc.CirclePadMax || y < -rpc.CirclePadMax || y > rpc.CirclePadMax {
		return nil, usage("circle pad (%d, %d) outside -%d..%d", x, y, rpc.CirclePadMax, rpc.CirclePadMax)
	}
	in := rpc.Input{CirclePad: &rpc.Stick{X: int16(x), Y: int16(y)}}
	if hold {
		return o.Hold(ctx, name, in)
	}
	return o.Press(ctx, name, in, d)
}

// --- screenshots ----------------------------------------------------------

// Screenshot captures one or both screens.
func (o *Ops) Screenshot(ctx context.Context, name string, req capture.Request) ([]capture.Shot, error) {
	_, c, err := o.client(ctx, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	return capture.RPC{Client: c}.Capture(ctx, req)
}

// --- memory ---------------------------------------------------------------

// selectApp makes sure memory requests target the app: if no process is
// selected (fresh server, or after a load), it selects the newest process,
// which is the app Azahar booted (PROTOCOL.md §5.3, §5.4).
func selectApp(ctx context.Context, c *rpc.Client) error {
	pid, err := c.Process(ctx)
	if err != nil {
		return err
	}
	if pid != rpc.NoProcess {
		return nil
	}
	ps, err := c.Processes(ctx)
	if err != nil {
		return err
	}
	if len(ps) == 0 {
		return errors.New("no emulated process to read from")
	}
	app := ps[0]
	for _, p := range ps[1:] {
		if p.PID > app.PID {
			app = p
		}
	}
	return c.SelectProcess(ctx, app.PID)
}

// MaxMemoryRead bounds a single read (16 MiB).
const MaxMemoryRead = 16 << 20

// ReadMemory reads n bytes at addr from the app's address space.
func (o *Ops) ReadMemory(ctx context.Context, name string, addr uint32, n int) ([]byte, error) {
	if n <= 0 || n > MaxMemoryRead {
		return nil, usage("length %d outside 1..%d", n, MaxMemoryRead)
	}
	_, c, err := o.client(ctx, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	if err := selectApp(ctx, c); err != nil {
		return nil, err
	}
	return c.ReadMemory(ctx, addr, n)
}

// WriteMemory writes data at addr and reads it back (Azahar silently
// ignores writes outside its writable regions, PROTOCOL.md §5.2).
func (o *Ops) WriteMemory(ctx context.Context, name string, addr uint32, data []byte) error {
	if len(data) == 0 || len(data) > MaxMemoryRead {
		return usage("data length %d outside 1..%d", len(data), MaxMemoryRead)
	}
	_, c, err := o.client(ctx, name)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	if err := selectApp(ctx, c); err != nil {
		return err
	}
	return c.WriteMemoryVerified(ctx, addr, data)
}

// --- save states ----------------------------------------------------------

// SaveState saves to slot 0..10.
func (o *Ops) SaveState(ctx context.Context, name string, slot int) error {
	_, c, err := o.client(ctx, name)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	return c.SaveState(ctx, slot)
}

// LoadState loads slot 0..10. For launched sessions it first checks that
// the slot has a state: an empty slot leaves Azahar stuck behind a modal
// dialog (PROTOCOL.md §5.7).
func (o *Ops) LoadState(ctx context.Context, name string, slot int) error {
	s, c, err := o.client(ctx, name)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	if exists, known := emu.HasState(s, slot); known && !exists {
		return usage("slot %d has no save state in session %q", slot, name)
	}
	return c.LoadState(ctx, slot)
}

// --- logs -----------------------------------------------------------------

// LogQuery filters log lines.
type LogQuery struct {
	// Tail returns only the last N matching lines (0: all).
	Tail int
	// Contains keeps lines containing this text (case-sensitive).
	Contains string
}

// logPath returns the session's Azahar log.
func (o *Ops) logPath(name string) (string, error) {
	s, err := o.M.Store.Load(name)
	if err != nil {
		return "", err
	}
	if s.LogPath == "" {
		return "", fmt.Errorf("session %q has no known log file (attach with --log)", name)
	}
	return s.LogPath, nil
}

// Logs returns matching lines of the session's Azahar log.
func (o *Ops) Logs(_ context.Context, name string, q LogQuery) ([]string, error) {
	p, err := o.logPath(name)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		if q.Contains == "" || strings.Contains(sc.Text(), q.Contains) {
			lines = append(lines, sc.Text())
			if q.Tail > 0 && len(lines) > 2*q.Tail {
				lines = append(lines[:0:0], lines[len(lines)-q.Tail:]...)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if q.Tail > 0 && len(lines) > q.Tail {
		lines = lines[len(lines)-q.Tail:]
	}
	return lines, nil
}

// FollowLogs writes matching lines appended to the log after the call
// until ctx ends.
func (o *Ops) FollowLogs(ctx context.Context, name string, q LogQuery, w io.Writer) error {
	p, err := o.logPath(name)
	if err != nil {
		return err
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	r := bufio.NewReader(f)
	var partial string
	for {
		line, err := r.ReadString('\n')
		partial += line
		if err == nil {
			l := strings.TrimRight(partial, "\r\n")
			partial = ""
			if q.Contains == "" || strings.Contains(l, q.Contains) {
				if _, err := fmt.Fprintln(w, l); err != nil {
					return err
				}
			}
			continue
		}
		if !errors.Is(err, io.EOF) {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(200 * time.Millisecond):
		}
	}
}
