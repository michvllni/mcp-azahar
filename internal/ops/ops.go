// SPDX-License-Identifier: GPL-3.0-or-later

// Package ops implements every user-facing operation once. The CLI
// (cmd/mcp-azahar) and the MCP server are thin front ends over it, so a
// feature behaves the same in both (PLAN.md, D3). Results are plain view
// types with stable JSON field names.
package ops

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

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
	// ErrNotAnswering means the session's emulator runs but its RPC server
	// does not answer (app not loaded yet, stuck behind a dialog).
	ErrNotAnswering = errors.New("emulator not answering")
)

// Ops runs operations against sessions. Within one process (e.g. the MCP
// server) operations on the same session are serialized where they would
// interfere; separate CLI processes are not coordinated beyond the session
// file lock.
type Ops struct {
	M *emu.Manager
	// SaveWait bounds how long SaveState waits for the state file
	// (DefaultSaveWait if 0).
	SaveWait time.Duration

	mu    sync.Mutex
	locks map[string]*sessionLocks
}

// sessionLocks serialize operations on one session within this process.
type sessionLocks struct {
	input sync.Mutex   // held-input read-modify-write, presses
	state sync.RWMutex // LoadState (write) vs. memory requests (read)
	shot  sync.Mutex   // TakeScreenshot + ReadScreenshot sequences
}

func (o *Ops) locksFor(name string) *sessionLocks {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.locks == nil {
		o.locks = map[string]*sessionLocks{}
	}
	l, ok := o.locks[name]
	if !ok {
		l = &sessionLocks{}
		o.locks[name] = l
	}
	return l
}

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

// client opens an RPC client for a session.
func (o *Ops) client(ctx context.Context, name string) (*session.Session, *rpc.Client, error) {
	return o.M.Client(ctx, name)
}

// --- views ----------------------------------------------------------------

// InputView is input state with button names. Arrays are never null.
type InputView struct {
	Buttons   []string   `json:"buttons"`
	Home      bool       `json:"home"`
	Power     bool       `json:"power"`
	Touch     *rpc.Point `json:"touch"`
	CirclePad *rpc.Stick `json:"circle_pad"`
}

// ViewInput converts input state for output.
func ViewInput(in rpc.Input) InputView {
	names := in.Buttons.Names()
	if names == nil {
		names = []string{}
	}
	return InputView{Buttons: names, Home: in.Home, Power: in.Power, Touch: in.Touch, CirclePad: in.CirclePad}
}

// SessionView is a session for output.
type SessionView struct {
	Name      string           `json:"name"`
	Kind      session.Kind     `json:"kind"`
	Running   bool             `json:"running"`
	RPC       session.Endpoint `json:"rpc"`
	AzaharPID int              `json:"azahar_pid,omitempty"`
	XvfbPID   int              `json:"xvfb_pid,omitempty"`
	Display   string           `json:"display,omitempty"`
	Dir       string           `json:"dir,omitempty"`
	LogPath   string           `json:"log_path,omitempty"`
	AppPath   string           `json:"app_path,omitempty"`
	ELFPath   string           `json:"elf_path,omitempty"`
	Held      InputView        `json:"held"`
}

// ViewSession converts a session for output.
func ViewSession(s *session.Session) SessionView {
	v := SessionView{
		Name: s.Name, Kind: s.Kind, Running: s.Running(), RPC: s.RPC, Display: s.Display, Dir: s.Dir,
		LogPath: s.LogPath, AppPath: s.AppPath, ELFPath: s.ELFPath, Held: ViewInput(s.Held),
	}
	if s.Azahar != nil {
		v.AzaharPID = s.Azahar.PID
	}
	if s.Xvfb != nil {
		v.XvfbPID = s.Xvfb.PID
	}
	return v
}

// ProcessView is an emulated process.
type ProcessView struct {
	PID     uint32 `json:"pid"`
	TitleID string `json:"title_id"` // 16 hex digits
	Name    string `json:"name"`
}

// StatusView reports a session's state.
type StatusView struct {
	Session   SessionView   `json:"session"`
	Running   bool          `json:"running"`
	Answering bool          `json:"answering"`
	RPCError  string        `json:"rpc_error,omitempty"`
	Processes []ProcessView `json:"processes"`
}

// --- sessions -------------------------------------------------------------

// Launch starts Azahar with an app in a new session.
func (o *Ops) Launch(ctx context.Context, name string, opts emu.LaunchOptions) (SessionView, error) {
	s, err := o.M.Launch(ctx, name, opts)
	if err != nil {
		return SessionView{}, err
	}
	return ViewSession(s), nil
}

// Attach registers an Azahar started by hand.
func (o *Ops) Attach(ctx context.Context, name string, opts emu.AttachOptions) (SessionView, error) {
	s, err := o.M.Attach(ctx, name, opts)
	if err != nil {
		return SessionView{}, err
	}
	return ViewSession(s), nil
}

// Stop ends a session.
func (o *Ops) Stop(ctx context.Context, name string, keepFiles bool) error {
	return o.M.Stop(ctx, name, keepFiles)
}

// Status reports a session's state. It returns the view together with
// session.ErrNotFound if the emulator is not running, or ErrNotAnswering if
// it runs but RPC does not answer, so front ends can print and fail.
func (o *Ops) Status(ctx context.Context, name string) (StatusView, error) {
	st, err := o.M.Status(ctx, name)
	if err != nil {
		return StatusView{}, err
	}
	v := StatusView{Session: ViewSession(st.Session), Running: st.Running, RPCError: st.RPCError, Processes: []ProcessView{}}
	for _, p := range st.Processes {
		v.Processes = append(v.Processes, ProcessView{PID: p.PID, TitleID: fmt.Sprintf("%016X", p.TitleID), Name: p.Name})
	}
	v.Answering = st.Running && st.RPCError == ""
	switch {
	case !st.Running:
		return v, fmt.Errorf("%w: session %q: its emulator is not running", session.ErrNotFound, name)
	case !v.Answering:
		return v, fmt.Errorf("%w: session %q: %s", ErrNotAnswering, name, st.RPCError)
	}
	return v, nil
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
