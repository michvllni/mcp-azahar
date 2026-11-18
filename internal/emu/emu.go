// SPDX-License-Identifier: GPL-3.0-or-later

// Package emu starts, finds and stops Azahar instances and records them as
// sessions (PLAN.md Phase 3). With Azahar 2127 the RPC port is fixed, so at
// most one RPC-enabled Azahar runs per host; launch holds a host-wide lock
// and verifies that the RPC server it talks to is the Azahar it started
// (DECISIONS.md D9).
package emu

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/michvllni/mcp-azahar/internal/rpc"
	"github.com/michvllni/mcp-azahar/internal/session"
)

// Errors returned by the manager. Use errors.Is.
var (
	// ErrSessionExists means the session name is taken by a running session.
	ErrSessionExists = errors.New("session already exists")
	// ErrPortInUse means Azahar's RPC port is held by something else.
	ErrPortInUse = errors.New("azahar RPC port in use")
	// ErrNotOwner means the RPC server answering is not the Azahar launch
	// started.
	ErrNotOwner = errors.New("RPC server belongs to another process")
	// ErrStartFailed means Azahar exited or its RPC server did not answer.
	ErrStartFailed = errors.New("azahar did not start")
)

// Manager launches and tracks Azahar sessions. Zero fields get defaults.
type Manager struct {
	Store *session.Store
	// AzaharBin is the emulator executable. Default: $AZAHAR_BIN, else
	// "azahar" in PATH, else /opt/azahar/AppRun.
	AzaharBin string
	// XvfbBin is the X server for headless runs (default "Xvfb").
	XvfbBin string
	// RPCPort is Azahar's RPC port (default 45987; tests override it).
	RPCPort int
	// StartTimeout bounds the wait for RPC after starting Azahar (default 60 s).
	StartTimeout time.Duration
	// StopTimeout is the grace period after SIGHUP before SIGKILL (default 5 s).
	StopTimeout time.Duration
	// RPCOptions configure clients created by the manager.
	RPCOptions rpc.Options
}

func (m *Manager) port() int {
	if m.RPCPort > 0 {
		return m.RPCPort
	}
	return rpc.DefaultPort
}

func (m *Manager) startTimeout() time.Duration {
	if m.StartTimeout > 0 {
		return m.StartTimeout
	}
	return 60 * time.Second
}

func (m *Manager) stopTimeout() time.Duration {
	if m.StopTimeout > 0 {
		return m.StopTimeout
	}
	return 5 * time.Second
}

// azaharBin resolves the emulator executable.
func (m *Manager) azaharBin() (string, error) {
	for _, c := range []string{m.AzaharBin, os.Getenv("AZAHAR_BIN")} {
		if c != "" {
			return c, nil
		}
	}
	if p, err := exec.LookPath("azahar"); err == nil {
		return p, nil
	}
	if _, err := os.Stat("/opt/azahar/AppRun"); err == nil {
		return "/opt/azahar/AppRun", nil
	}
	return "", errors.New("azahar not found: set AZAHAR_BIN or put azahar in PATH")
}

// LaunchOptions select what to run.
type LaunchOptions struct {
	// App is the homebrew to boot (.3dsx, .cci, .3ds, .cxi, .app, .elf).
	App string
	// ELF is the matching ELF with symbols, for read-symbol (optional).
	ELF string
	// Display is an existing X display to use. Empty: start Xvfb on Linux,
	// use the desktop elsewhere.
	Display string
}

// Launch starts Azahar for a new session and returns it once Azahar's RPC
// server answers.
func (m *Manager) Launch(ctx context.Context, name string, opts LaunchOptions) (*session.Session, error) {
	if runtime.GOOS == "windows" {
		// Azahar takes its portable user dir from next to the executable on
		// Windows, not from the working directory, so sessions cannot get
		// an isolated config with RPC enabled (PROTOCOL.md §7).
		return nil, errors.New("launch is not supported on Windows yet: start Azahar with RPC enabled yourself and use attach")
	}
	if err := session.ValidateName(name); err != nil {
		return nil, err
	}
	app, err := checkFile(opts.App, "app")
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(filepath.Ext(app), ".cia") {
		return nil, errors.New("CIA files must be installed first and are not supported yet; use a .3dsx or .cci")
	}
	var elf string
	if opts.ELF != "" {
		if elf, err = checkFile(opts.ELF, "ELF"); err != nil {
			return nil, err
		}
	}
	bin, err := m.azaharBin()
	if err != nil {
		return nil, err
	}

	unlock, err := m.Store.Lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	// Also serialize with other users and state directories (D9).
	release, _, err := session.HostLock(ctx, strconv.Itoa(m.port()))
	if err != nil {
		return nil, err
	}
	defer release()

	switch old, err := m.Store.Load(name); {
	case err == nil && old.Running():
		return nil, fmt.Errorf("%w: %q (stop it first)", ErrSessionExists, name)
	case err == nil:
		_ = m.cleanup(old, false)
	case !errors.Is(err, session.ErrNotFound):
		return nil, fmt.Errorf("existing session %q is unreadable (%w); run stop to remove it", name, err)
	}
	m.pruneLocked()
	if err := m.checkPort(); err != nil {
		return nil, err
	}

	dir := m.Store.SessionDir(name)
	if err := moveAside(dir); err != nil {
		return nil, err
	}
	s := &session.Session{
		Name:       name,
		Kind:       session.KindLaunched,
		CreatedAt:  time.Now().UTC(),
		RPC:        session.Endpoint{Network: "udp", Address: m.localAddr()},
		Dir:        dir,
		OutputPath: filepath.Join(dir, "azahar.out"),
		AppPath:    app,
		ELFPath:    elf,
	}
	s.LogPath = filepath.Join(s.UserDir(), "log", "azahar_log.txt")
	ok := false
	defer func() {
		if !ok {
			_ = m.cleanup(s, true)
		}
	}()
	if err := writeSeedConfig(s.UserDir()); err != nil {
		return nil, fmt.Errorf("write Azahar config: %w", err)
	}

	s.Display = opts.Display
	if s.Display == "" && runtime.GOOS == "linux" {
		xvfb := m.XvfbBin
		if xvfb == "" {
			xvfb = "Xvfb"
		}
		p, display, err := startXvfb(ctx, xvfb, filepath.Join(dir, "xvfb.out"))
		if err != nil {
			return nil, err
		}
		pr := session.ProcFor(p.Pid)
		s.Xvfb, s.Display = &pr, display
	}

	exited, err := m.startAzahar(s, bin)
	if err != nil {
		return nil, err
	}
	if err := m.waitRPC(ctx, s, exited); err != nil {
		return nil, err
	}
	if err := m.verifyOwner(ctx, s); err != nil {
		return nil, err
	}
	if err := m.Store.Save(s); err != nil {
		return nil, err
	}
	ok = true
	return s, nil
}

// moveAside renames an existing session directory (save states and logs
// kept by Stop, or a crashed session's log) to <dir>.<timestamp>, so a new
// launch of the same name never destroys them.
func moveAside(dir string) error {
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	dest := dir + "." + time.Now().UTC().Format("20060102-150405.000")
	if err := os.Rename(dir, dest); err != nil {
		return fmt.Errorf("move old session directory aside: %w", err)
	}
	return nil
}

func checkFile(p, what string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("no %s file given", what)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("%s file: %w", what, err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s file %s is not a regular file", what, abs)
	}
	return abs, nil
}

func (m *Manager) localAddr() string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(m.port()))
}

// checkPort fails with ErrPortInUse, naming the holder, if Azahar's RPC
// port cannot be bound (DECISIONS.md D9).
func (m *Manager) checkPort() error {
	if portFree(m.port()) {
		return nil
	}
	holder := "another process"
	if sessions, err := m.Store.List(); err == nil {
		for _, s := range sessions {
			if s.Kind == session.KindLaunched && s.Running() && s.RPC.Address == m.localAddr() {
				holder = fmt.Sprintf("session %q", s.Name)
			}
		}
	}
	if holder == "another process" {
		if owners, st := udpPortOwners(m.port()); st == ownerFound {
			holder = fmt.Sprintf("process %d (%s)", owners[0].PID, owners[0].Comm)
		}
	}
	return fmt.Errorf("%w: UDP port %d is held by %s; with Azahar 2127 only one RPC-enabled Azahar can run per host (DECISIONS.md D9)",
		ErrPortInUse, m.port(), holder)
}

// startAzahar starts the emulator detached in the session directory
// (portable mode: ./user). The channel receives its exit.
func (m *Manager) startAzahar(s *session.Session, bin string) (<-chan error, error) {
	out, err := os.Create(s.OutputPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = out.Close() }()
	//nolint:noctx // detached: Azahar must outlive the caller and its context
	cmd := exec.Command(bin, s.AppPath)
	cmd.Dir = s.Dir
	cmd.Stdout, cmd.Stderr = out, out
	cmd.Env = os.Environ()
	if s.Display != "" {
		cmd.Env = append(cmd.Env, "DISPLAY="+s.Display, "QT_QPA_PLATFORM=xcb")
	}
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", bin, err)
	}
	pr := session.ProcFor(cmd.Process.Pid)
	s.Azahar = &pr
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }() // reaps the child in long-running callers
	return exited, nil
}

// waitRPC polls until Azahar's RPC server lists a process.
func (m *Manager) waitRPC(ctx context.Context, s *session.Session, exited <-chan error) error {
	opts := m.RPCOptions
	opts.Timeout, opts.Retries = 250*time.Millisecond, -1
	c, err := rpc.Dial(ctx, rpc.UDPDialer(s.RPC.Address), opts)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	wait, cancel := context.WithTimeout(ctx, m.startTimeout())
	defer cancel()
	exitErr := func(err error) error {
		if err == nil {
			return fmt.Errorf("%w: azahar exited with status 0%s", ErrStartFailed, tails(s))
		}
		return fmt.Errorf("%w: azahar exited (%w)%s", ErrStartFailed, err, tails(s))
	}
	for {
		select {
		case err := <-exited:
			return exitErr(err)
		default:
		}
		if ps, err := c.Processes(wait); err == nil && len(ps) > 0 {
			return nil
		}
		select {
		case <-wait.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%w: RPC did not answer within %v (app failed to load, or a dialog blocks it)%s",
				ErrStartFailed, m.startTimeout(), tails(s))
		case err := <-exited:
			return exitErr(err)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// verifyOwner checks that the answering RPC server is the Azahar we
// started: Azahar logs a bind failure and keeps running without RPC, so
// another instance could be answering (DECISIONS.md D9). On Linux the
// socket owner must be the started process or a descendant; a socket whose
// owner cannot be read (another user's process) counts as foreign.
// Elsewhere only Azahar's log can tell, so its RPC start line must appear.
func (m *Manager) verifyOwner(ctx context.Context, s *session.Session) error {
	started := waitForLog(ctx, s.LogPath, "Starting RPC server", 3*time.Second)
	if started {
		// The bind happens right after that line is logged.
		time.Sleep(300 * time.Millisecond)
	}
	if b, err := os.ReadFile(s.LogPath); err == nil && bytes.Contains(b, []byte("Error starting UDP server")) {
		return fmt.Errorf("%w: Azahar could not bind UDP port %d", ErrNotOwner, m.port())
	}
	owners, st := udpPortOwners(m.port())
	switch st {
	case ownerFound:
		// Every socket on the port must be ours (SO_REUSEADDR/PORT allow
		// several).
		for _, o := range owners {
			if !isSelfOrDescendant(o.PID, s.Azahar.PID) {
				return fmt.Errorf("%w: UDP port %d is held by process %d (%s), not by the started Azahar (%d)",
					ErrNotOwner, m.port(), o.PID, o.Comm, s.Azahar.PID)
			}
		}
	case ownerHidden:
		return fmt.Errorf("%w: UDP port %d is held by a process this user cannot inspect", ErrNotOwner, m.port())
	case ownerNotBound:
		return fmt.Errorf("%w: nothing is bound to UDP port %d although RPC answered", ErrNotOwner, m.port())
	case ownerUnsupported:
		if !started {
			return fmt.Errorf("%w: cannot verify: Azahar's log %s shows no RPC start", ErrNotOwner, s.LogPath)
		}
	}
	return nil
}

// waitForLog polls a log file for text until timeout or ctx ends.
func waitForLog(ctx context.Context, path, text string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if b, err := os.ReadFile(path); err == nil && bytes.Contains(b, []byte(text)) {
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// tails returns the end of Azahar's output and log for error messages.
func tails(s *session.Session) string {
	var b strings.Builder
	for _, f := range []string{s.OutputPath, s.LogPath} {
		if t := tailFile(f, 15); t != "" {
			fmt.Fprintf(&b, "\n--- %s (last lines) ---\n%s", f, t)
		}
	}
	return b.String()
}

// tailFile returns the last n lines of a file, or "".
func tailFile(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	const window = 1 << 20
	if fi, err := f.Stat(); err == nil && fi.Size() > window {
		_, _ = f.Seek(fi.Size()-window, io.SeekStart)
	}
	b, err := io.ReadAll(io.LimitReader(f, window))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// AttachOptions describe an Azahar started by someone else.
type AttachOptions struct {
	// Address of its RPC server (default 127.0.0.1:45987).
	Address string
	// ELF with symbols (optional), LogPath of its log (optional).
	ELF, LogPath string
}

// Attach registers a running Azahar as a session after checking that its
// RPC server answers.
func (m *Manager) Attach(ctx context.Context, name string, opts AttachOptions) (*session.Session, error) {
	if err := session.ValidateName(name); err != nil {
		return nil, err
	}
	addr := opts.Address
	if addr == "" {
		addr = m.localAddr()
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return nil, fmt.Errorf("RPC address %q: %w", addr, err)
	}
	s := &session.Session{
		Name: name, Kind: session.KindAttached, CreatedAt: time.Now().UTC(),
		RPC: session.Endpoint{Network: "udp", Address: addr}, LogPath: opts.LogPath,
	}
	if opts.ELF != "" {
		elf, err := checkFile(opts.ELF, "ELF")
		if err != nil {
			return nil, err
		}
		s.ELFPath = elf
	}
	unlock, err := m.Store.Lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if old, err := m.Store.Load(name); err == nil && old.Running() {
		return nil, fmt.Errorf("%w: %q (stop it first)", ErrSessionExists, name)
	}
	c, err := m.dial(ctx, s)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Processes(ctx); err != nil {
		return nil, fmt.Errorf("no Azahar RPC server answers at %s (app loaded, RPC enabled?): %w", addr, err)
	}
	if err := m.Store.Save(s); err != nil {
		return nil, err
	}
	return s, nil
}

func (m *Manager) dial(ctx context.Context, s *session.Session) (*rpc.Client, error) {
	switch s.RPC.Network {
	case "udp", "":
		return rpc.Dial(ctx, rpc.UDPDialer(s.RPC.Address), m.RPCOptions)
	default:
		return nil, fmt.Errorf("session %q: unsupported RPC network %q", s.Name, s.RPC.Network)
	}
}

// Client returns the session and an RPC client for it. A launched session
// whose Azahar has exited is removed and reported as not found.
func (m *Manager) Client(ctx context.Context, name string) (*session.Session, *rpc.Client, error) {
	s, err := m.Store.Load(name)
	if err != nil {
		return nil, nil, err
	}
	if !s.Running() {
		m.removeStale(ctx, s)
		return nil, nil, fmt.Errorf("%w: %q (its Azahar exited; stale session removed)", session.ErrNotFound, name)
	}
	c, err := m.dial(ctx, s)
	if err != nil {
		return nil, nil, err
	}
	return s, c, nil
}

// Stop ends a session: for launched sessions it stops Azahar (SIGHUP, then
// SIGKILL after StopTimeout; Azahar ignores SIGTERM, PROTOCOL.md §7) and
// the Xvfb it started, then removes the session file. keepFiles keeps the
// session directory (logs, save states); attached sessions are only
// forgotten.
func (m *Manager) Stop(ctx context.Context, name string, keepFiles bool) error {
	unlock, err := m.Store.Lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	s, err := m.Store.Load(name)
	if errors.Is(err, session.ErrNotFound) {
		return err
	}
	if err != nil {
		// Unreadable: its processes are unknown, but the file must go.
		if derr := m.Store.Delete(name); derr != nil {
			return errors.Join(err, derr)
		}
		return fmt.Errorf("removed unreadable session %q; any Azahar it started may still run: %w", name, err)
	}
	return m.cleanup(s, !keepFiles)
}

// cleanup stops a session's processes and removes its file (and directory
// if removeDir). Called with the store lock held.
func (m *Manager) cleanup(s *session.Session, removeDir bool) error {
	var errs []error
	if s.Kind == session.KindLaunched {
		// KillGroupRest also covers a leader that died earlier (stale
		// session) while children of a wrapper script still run.
		if s.Azahar != nil {
			errs = append(errs, terminate(*s.Azahar, syscall.SIGHUP, m.stopTimeout()))
			session.KillGroupRest(*s.Azahar)
		}
		if s.Xvfb != nil {
			errs = append(errs, terminate(*s.Xvfb, syscall.SIGTERM, m.stopTimeout()))
			session.KillGroupRest(*s.Xvfb)
		}
		if removeDir && s.Dir != "" {
			errs = append(errs, os.RemoveAll(s.Dir))
		}
	}
	errs = append(errs, m.Store.Delete(s.Name))
	return errors.Join(errs...)
}

// terminate sends sig, waits up to grace, then kills.
func terminate(p session.Proc, sig syscall.Signal, grace time.Duration) error {
	if !session.Alive(p) {
		return nil
	}
	_ = session.Signal(p, sig)
	if waitDead(p, grace) {
		// Children (e.g. of a wrapper script without exec) may outlive it.
		session.KillGroupRest(p)
		return nil
	}
	_ = session.Signal(p, syscall.SIGKILL)
	if waitDead(p, 2*time.Second) {
		session.KillGroupRest(p)
		return nil
	}
	return fmt.Errorf("process %d did not exit", p.PID)
}

func waitDead(p session.Proc, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if !session.Alive(p) {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return !session.Alive(p)
}

// Prune removes launched sessions whose Azahar has exited (stopping a
// leftover Xvfb) and returns their names.
func (m *Manager) Prune(ctx context.Context) ([]string, error) {
	unlock, err := m.Store.Lock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return m.pruneLocked(), nil
}

func (m *Manager) pruneLocked() []string {
	sessions, err := m.Store.List()
	if err != nil {
		return nil
	}
	var removed []string
	for _, s := range sessions {
		if !s.Running() {
			_ = m.cleanup(s, false)
			removed = append(removed, s.Name)
		}
	}
	return removed
}

func (m *Manager) removeStale(ctx context.Context, s *session.Session) {
	unlock, err := m.Store.Lock(ctx)
	if err != nil {
		return
	}
	defer unlock()
	if cur, err := m.Store.Load(s.Name); err == nil && !cur.Running() {
		_ = m.cleanup(cur, false)
	}
}

// Status describes a session for `status`/`sessions`.
type Status struct {
	Session   *session.Session `json:"session"`
	Running   bool             `json:"running"`
	RPCError  string           `json:"rpc_error,omitempty"`
	Processes []rpc.Process    `json:"processes,omitempty"`
}

// Status reports whether a session's Azahar runs and its RPC answers.
func (m *Manager) Status(ctx context.Context, name string) (*Status, error) {
	s, err := m.Store.Load(name)
	if err != nil {
		return nil, err
	}
	st := &Status{Session: s, Running: s.Running()}
	if !st.Running {
		st.RPCError = "azahar is not running"
		return st, nil
	}
	c, err := m.dial(ctx, s)
	if err != nil {
		st.RPCError = err.Error()
		return st, nil
	}
	defer func() { _ = c.Close() }()
	ps, err := c.Processes(ctx)
	if err != nil {
		st.RPCError = err.Error()
	}
	st.Processes = ps
	return st, nil
}

// HasState reports whether a session has a save state in slot. Loading an
// empty slot leaves Azahar stuck behind a modal dialog (PROTOCOL.md §5.7),
// so callers check this first. known is false when the session's user
// directory is unknown (attached sessions).
func HasState(s *session.Session, slot int) (exists, known bool) {
	if s.UserDir() == "" {
		return false, false
	}
	matches, _ := filepath.Glob(filepath.Join(s.UserDir(), "states", fmt.Sprintf("*.%02d.cst", slot)))
	return len(matches) > 0, true
}
