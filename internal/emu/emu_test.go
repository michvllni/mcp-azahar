// SPDX-License-Identifier: GPL-3.0-or-later

//go:build unix

package emu

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/michvllni/mcp-azahar/internal/rpc"
	"github.com/michvllni/mcp-azahar/internal/rpc/fakeserver"
	"github.com/michvllni/mcp-azahar/internal/session"
)

// The test binary doubles as a fake Azahar: with MCPAZ_FAKE_AZAHAR set it
// writes a log like Azahar's portable mode, serves the fake RPC server on
// MCPAZ_FAKE_ADDR and behaves according to MCPAZ_FAKE_MODE.
func TestMain(m *testing.M) {
	if os.Getenv("MCPAZ_FAKE_AZAHAR") == "1" {
		fakeAzahar()
		return
	}
	os.Exit(m.Run())
}

func fakeAzahar() {
	mode := os.Getenv("MCPAZ_FAKE_MODE")
	switch mode {
	case "spawner":
		fakeSpawner()
		return
	case "server-only":
		fakeServerOnly()
		return
	}
	if pf := os.Getenv("MCPAZ_FAKE_PIDFILE"); pf != "" {
		_ = os.WriteFile(pf, []byte(strconv.Itoa(os.Getpid())), 0o600)
	}
	fmt.Println("fake azahar output", os.Args[1:])
	_ = os.MkdirAll(filepath.Join("user", "log"), 0o700)
	logLine := "fake azahar started\n"
	if mode != "no-rpc" && mode != "exit" {
		logLine += "RPC_Server <Info> core/rpc/rpc_server.cpp:RPCServer:34: Starting RPC server (synchronous).\n"
	}
	if mode == "bind-fail" || mode == "foreign-binderr" {
		logLine += "RPC_Server <Error> core/rpc/server.cpp: Error starting UDP server\n"
	}
	if mode == "foreign" || mode == "foreign-binderr" {
		//nolint:noctx // test helper process
		spawner := exec.Command(os.Args[0])
		spawner.Env = append(os.Environ(), "MCPAZ_FAKE_MODE=spawner")
		_ = spawner.Run()
	}
	_ = os.WriteFile(filepath.Join("user", "log", "azahar_log.txt"), []byte(logLine), 0o600)
	if _, err := os.Stat(filepath.Join("user", "config", "qt-config.ini")); err != nil {
		fmt.Println("no seed config:", err)
		os.Exit(4)
	}
	if mode == "exit" {
		os.Exit(3)
	}
	if mode == "" || mode == "ignore-hup" {
		srv, err := fakeserver.New(fakeserver.Config{Addr: os.Getenv("MCPAZ_FAKE_ADDR")})
		if err != nil {
			fmt.Println("bind:", err)
			os.Exit(5)
		}
		defer func() { _ = srv.Close() }()
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	for sig := range sigs {
		// Like Azahar: SIGTERM/SIGINT are ignored, SIGHUP ends it.
		if sig == syscall.SIGHUP && mode != "ignore-hup" {
			return
		}
	}
}

// listenUDP binds a UDP socket for a test.
func listenUDP(t *testing.T, addr string) (net.PacketConn, error) {
	var lc net.ListenConfig
	return lc.ListenPacket(t.Context(), "udp4", addr)
}

// fakeSpawner starts a foreign RPC server that is not a descendant of the
// fake Azahar (new session, parent exits), then exits.
func fakeSpawner() {
	//nolint:noctx // detached test helper process
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "MCPAZ_FAKE_MODE=server-only")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		os.Exit(6)
	}
	_ = os.WriteFile(os.Getenv("MCPAZ_FOREIGN_PIDFILE"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
}

// fakeServerOnly serves RPC until killed.
func fakeServerOnly() {
	srv, err := fakeserver.New(fakeserver.Config{Addr: os.Getenv("MCPAZ_FAKE_ADDR")})
	if err != nil {
		os.Exit(5)
	}
	defer func() { _ = srv.Close() }()
	select {}
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := listenUDP(t, "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// newManager returns a manager running the fake Azahar in the given mode.
func newManager(t *testing.T, mode string) *Manager {
	t.Helper()
	st, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	port := freeUDPPort(t)
	t.Setenv("MCPAZ_FAKE_AZAHAR", "1")
	t.Setenv("MCPAZ_FAKE_MODE", mode)
	t.Setenv("MCPAZ_FAKE_ADDR", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	m := &Manager{
		Store: st, AzaharBin: os.Args[0], RPCPort: port,
		StartTimeout: 5 * time.Second, StopTimeout: 500 * time.Millisecond,
		RPCOptions: rpc.Options{Timeout: 200 * time.Millisecond},
	}
	t.Cleanup(func() {
		sessions, _ := st.List()
		for _, s := range sessions {
			_ = m.Stop(context.Background(), s.Name, false)
		}
	})
	return m
}

func appFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "app.3dsx")
	if err := os.WriteFile(p, []byte("3DSX"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

var noXvfb = LaunchOptions{Display: ":fake"}

func launchOpts(t *testing.T) LaunchOptions {
	o := noXvfb
	o.App = appFile(t)
	return o
}

func TestLaunchStatusStop(t *testing.T) {
	m := newManager(t, "")
	ctx := context.Background()
	s, err := m.Launch(ctx, "a", launchOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if s.Kind != session.KindLaunched || s.Azahar == nil || s.Xvfb != nil || s.Display != ":fake" {
		t.Fatalf("session: %+v", s)
	}
	if s.RPC != (session.Endpoint{Network: "udp", Address: net.JoinHostPort("127.0.0.1", strconv.Itoa(m.RPCPort))}) {
		t.Fatalf("endpoint: %+v", s.RPC)
	}
	if b, err := os.ReadFile(filepath.Join(s.UserDir(), "config", "qt-config.ini")); err != nil || !strings.Contains(string(b), "enable_rpc_server=true") {
		t.Fatalf("seed config: %q, %v", b, err)
	}
	if b, _ := os.ReadFile(s.OutputPath); !strings.Contains(string(b), "fake azahar output") || !strings.Contains(string(b), s.AppPath) {
		t.Fatalf("output %q", b)
	}
	st, err := m.Status(ctx, "a")
	if err != nil || !st.Running || st.RPCError != "" || len(st.Processes) != 1 {
		t.Fatalf("status %+v, %v", st, err)
	}
	_, c, err := m.Client(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Screenshot(ctx, rpc.ScreenshotRequest{}); err != nil {
		t.Fatalf("screenshot through session: %v", err)
	}
	_ = c.Close()

	if err := m.Stop(ctx, "a", false); err != nil {
		t.Fatal(err)
	}
	if session.Alive(*s.Azahar) {
		t.Fatal("azahar still running after Stop")
	}
	if _, err := m.Store.Load("a"); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("session file left: %v", err)
	}
	if _, err := os.Stat(s.Dir); !os.IsNotExist(err) {
		t.Fatalf("session dir left: %v", err)
	}
}

func TestStopKeepFiles(t *testing.T) {
	m := newManager(t, "")
	s, err := m.Launch(context.Background(), "a", launchOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(context.Background(), "a", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.LogPath); err != nil {
		t.Fatalf("log removed despite keepFiles: %v", err)
	}
}

func TestStopEscalatesToKill(t *testing.T) {
	m := newManager(t, "ignore-hup")
	s, err := m.Launch(context.Background(), "a", launchOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := m.Stop(context.Background(), "a", false); err != nil {
		t.Fatal(err)
	}
	if session.Alive(*s.Azahar) {
		t.Fatal("still running")
	}
	if d := time.Since(start); d < m.StopTimeout || d > 4*time.Second {
		t.Fatalf("stop took %v, want grace period then kill", d)
	}
}

func TestSessionNameTaken(t *testing.T) {
	m := newManager(t, "")
	if _, err := m.Launch(context.Background(), "a", launchOpts(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Launch(context.Background(), "a", launchOpts(t)); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("err = %v, want ErrSessionExists", err)
	}
}

func TestSecondSessionPortInUse(t *testing.T) {
	m := newManager(t, "")
	if _, err := m.Launch(context.Background(), "a", launchOpts(t)); err != nil {
		t.Fatal(err)
	}
	_, err := m.Launch(context.Background(), "b", launchOpts(t))
	if !errors.Is(err, ErrPortInUse) || !strings.Contains(err.Error(), `session "a"`) {
		t.Fatalf("err = %v, want ErrPortInUse naming session a", err)
	}
	if _, err := m.Store.Load("b"); !errors.Is(err, session.ErrNotFound) {
		t.Fatal("failed launch left a session file")
	}
}

func TestPortHeldByForeignProcess(t *testing.T) {
	m := newManager(t, "")
	c, err := listenUDP(t, net.JoinHostPort("0.0.0.0", strconv.Itoa(m.RPCPort)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_, err = m.Launch(context.Background(), "a", launchOpts(t))
	if !errors.Is(err, ErrPortInUse) {
		t.Fatalf("err = %v, want ErrPortInUse", err)
	}
	if runtime.GOOS == "linux" && !strings.Contains(err.Error(), fmt.Sprintf("process %d", os.Getpid())) {
		t.Fatalf("holder not named: %v", err)
	}
}

func TestConcurrentLaunchesAreSerialized(t *testing.T) {
	m := newManager(t, "")
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = m.Launch(context.Background(), "s"+strconv.Itoa(i), launchOpts(t))
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, ErrPortInUse):
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d launches succeeded, want exactly 1: %v", ok, errs)
	}
}

func TestStartFailures(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{
		{"exit", "fake azahar output"},
		{"no-rpc", "did not answer"},
		{"bind-fail", "did not answer"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			m := newManager(t, tc.mode)
			m.StartTimeout = time.Second
			_, err := m.Launch(context.Background(), "a", launchOpts(t))
			if !errors.Is(err, ErrStartFailed) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want ErrStartFailed containing %q", err, tc.want)
			}
			if list, _ := m.Store.List(); len(list) != 0 {
				t.Fatalf("sessions left: %v", list)
			}
			if _, err := os.Stat(m.Store.SessionDir("a")); !os.IsNotExist(err) {
				t.Fatal("session dir left after failed launch")
			}
		})
	}
}

func TestVerifyOwner(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "azahar_log.txt")
	_ = os.WriteFile(logPath, []byte("Starting RPC server (synchronous).\nx RPC_Server <Error> Error starting UDP server\n"), 0o600)
	m := &Manager{RPCPort: freeUDPPort(t)}
	s := &session.Session{LogPath: logPath, Azahar: &session.Proc{PID: os.Getpid()}}
	ctx := context.Background()
	if err := m.verifyOwner(ctx, s); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("bind failure in log: %v", err)
	}
	if runtime.GOOS != "linux" {
		return
	}
	// Port held by this process, while the session's Azahar is another one.
	c, err := listenUDP(t, net.JoinHostPort("0.0.0.0", strconv.Itoa(m.RPCPort)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	other := exec.CommandContext(t.Context(), "sleep", "30")
	if err := other.Start(); err != nil {
		t.Skip(err)
	}
	defer func() { _ = other.Process.Kill(); _ = other.Wait() }()
	okLog := filepath.Join(dir, "ok.txt")
	_ = os.WriteFile(okLog, []byte("Starting RPC server (synchronous).\n"), 0o600)
	s = &session.Session{LogPath: okLog, Azahar: &session.Proc{PID: other.Process.Pid}}
	if err := m.verifyOwner(ctx, s); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("foreign owner: %v", err)
	}
	s.Azahar.PID = os.Getpid()
	if err := m.verifyOwner(ctx, s); err != nil {
		t.Fatalf("own port: %v", err)
	}
	_ = c.Close()
	if err := m.verifyOwner(ctx, s); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("nothing bound: %v", err)
	}
}

func TestStaleSessionRemoved(t *testing.T) {
	m := newManager(t, "")
	ctx := context.Background()
	s, err := m.Launch(ctx, "a", launchOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	_ = session.Signal(*s.Azahar, syscall.SIGKILL)
	if !waitDead(*s.Azahar, 3*time.Second) {
		t.Fatal("kill failed")
	}
	if _, _, err := m.Client(ctx, "a"); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("Client on stale session: %v", err)
	}
	if _, err := m.Store.Load("a"); !errors.Is(err, session.ErrNotFound) {
		t.Fatal("stale session file not removed")
	}
	// The port is free again: a new launch works.
	if _, err := m.Launch(ctx, "b", launchOpts(t)); err != nil {
		t.Fatalf("launch after stale session: %v", err)
	}
}

func TestPrune(t *testing.T) {
	m := newManager(t, "")
	ctx := context.Background()
	s, err := m.Launch(ctx, "a", launchOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	_ = session.Signal(*s.Azahar, syscall.SIGKILL)
	waitDead(*s.Azahar, 3*time.Second)
	removed, err := m.Prune(ctx)
	if err != nil || len(removed) != 1 || removed[0] != "a" {
		t.Fatalf("Prune = %v, %v", removed, err)
	}
}

func TestAttach(t *testing.T) {
	m := newManager(t, "")
	ctx := context.Background()
	srv, err := fakeserver.New(fakeserver.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	s, err := m.Attach(ctx, "manual", AttachOptions{Address: srv.Addr()})
	if err != nil {
		t.Fatal(err)
	}
	if s.Kind != session.KindAttached || s.Azahar != nil || s.RPC.Address != srv.Addr() {
		t.Fatalf("session %+v", s)
	}
	if st, err := m.Status(ctx, "manual"); err != nil || !st.Running || st.RPCError != "" {
		t.Fatalf("status %+v, %v", st, err)
	}
	if err := m.Stop(ctx, "manual", false); err != nil {
		t.Fatal(err)
	}
	// Stopping an attached session only forgets it.
	c, err := rpc.Dial(ctx, rpc.UDPDialer(srv.Addr()), rpc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Processes(ctx); err != nil {
		t.Fatalf("attached server affected by Stop: %v", err)
	}
	m.RPCOptions = rpc.Options{Timeout: 50 * time.Millisecond, Retries: -1}
	if _, err := m.Attach(ctx, "none", AttachOptions{Address: "127.0.0.1:1"}); err == nil {
		t.Fatal("attach without a server succeeded")
	}
}

func TestLaunchValidation(t *testing.T) {
	m := newManager(t, "")
	ctx := context.Background()
	cia := filepath.Join(t.TempDir(), "x.cia")
	_ = os.WriteFile(cia, []byte("x"), 0o600)
	for name, opts := range map[string]LaunchOptions{
		"missing app": {App: "/nonexistent.3dsx", Display: ":fake"},
		"no app":      {Display: ":fake"},
		"cia":         {App: cia, Display: ":fake"},
		"missing elf": {App: appFile(t), ELF: "/nonexistent.elf", Display: ":fake"},
	} {
		if _, err := m.Launch(ctx, "a", opts); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := m.Launch(ctx, "../x", launchOpts(t)); err == nil {
		t.Error("bad session name accepted")
	}
}

func TestStartXvfb(t *testing.T) {
	bin, err := exec.LookPath("Xvfb")
	if err != nil || runtime.GOOS != "linux" {
		t.Skip("Xvfb not available")
	}
	p, display, err := startXvfb(context.Background(), bin, filepath.Join(t.TempDir(), "xvfb.out"))
	if err != nil {
		t.Fatal(err)
	}
	pr := session.ProcFor(p.Pid)
	defer func() { _ = terminate(pr, syscall.SIGTERM, 2*time.Second) }()
	n, _ := strconv.Atoi(strings.TrimPrefix(display, ":"))
	if !displayInUse(n) {
		t.Fatalf("display %s has no socket", display)
	}
	if next, err := freeDisplay(firstDisplay); err != nil || next == n {
		t.Fatalf("freeDisplay returned the busy display %d (%v)", next, err)
	}
}

func TestHasState(t *testing.T) {
	s := &session.Session{Dir: t.TempDir()}
	if exists, known := HasState(s, 1); exists || !known {
		t.Fatalf("empty: %v %v", exists, known)
	}
	_ = os.MkdirAll(filepath.Join(s.UserDir(), "states"), 0o700)
	_ = os.WriteFile(filepath.Join(s.UserDir(), "states", "0000000000000000.01.cst"), []byte("x"), 0o600)
	if exists, _ := HasState(s, 1); !exists {
		t.Fatal("slot 1 not found")
	}
	if exists, _ := HasState(s, 10); exists {
		t.Fatal("slot 10 found")
	}
	if _, known := HasState(&session.Session{Kind: session.KindAttached}, 1); known {
		t.Fatal("attached session claims to know its states")
	}
}

func TestForeignRPCServerIsRejected(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("socket owners are only visible on Linux")
	}
	for _, mode := range []string{"foreign", "foreign-binderr"} {
		t.Run(mode, func(t *testing.T) {
			m := newManager(t, mode)
			pidFile := filepath.Join(t.TempDir(), "foreign.pid")
			t.Setenv("MCPAZ_FOREIGN_PIDFILE", pidFile)
			t.Cleanup(func() {
				if b, err := os.ReadFile(pidFile); err == nil {
					pid, _ := strconv.Atoi(string(b))
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			})
			_, err := m.Launch(context.Background(), "a", launchOpts(t))
			if !errors.Is(err, ErrNotOwner) {
				t.Fatalf("err = %v, want ErrNotOwner", err)
			}
			if list, _ := m.Store.List(); len(list) != 0 {
				t.Fatalf("session saved for a foreign server: %v", list)
			}
		})
	}
}

func TestWrapperChildrenAreKilled(t *testing.T) {
	m := newManager(t, "ignore-hup")
	dir := t.TempDir()
	wrapper := filepath.Join(dir, "azahar-wrapper.sh")
	// No exec: the shell stays the group leader, the "emulator" is its child.
	script := "#!/bin/sh\n\"" + os.Args[0] + "\" \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	m.AzaharBin = wrapper
	pidFile := filepath.Join(dir, "child.pid")
	t.Setenv("MCPAZ_FAKE_PIDFILE", pidFile)
	if _, err := m.Launch(context.Background(), "a", launchOpts(t)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	child, _ := strconv.Atoi(string(b))
	childProc := session.ProcFor(child)
	if err := m.Stop(context.Background(), "a", false); err != nil {
		t.Fatal(err)
	}
	if !waitDead(childProc, 3*time.Second) {
		t.Fatal("child of the wrapper survived Stop")
	}
}

func TestRelaunchKeepsOldFiles(t *testing.T) {
	m := newManager(t, "")
	ctx := context.Background()
	s, err := m.Launch(ctx, "a", launchOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(s.UserDir(), "states", "0000000000000000.01.cst")
	_ = os.MkdirAll(filepath.Dir(marker), 0o700)
	_ = os.WriteFile(marker, []byte("state"), 0o600)
	if err := m.Stop(ctx, "a", true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Launch(ctx, "a", launchOpts(t)); err != nil {
		t.Fatal(err)
	}
	old, _ := filepath.Glob(s.Dir + ".*")
	if len(old) != 1 {
		t.Fatalf("old session dir not moved aside: %v", old)
	}
	if _, err := os.Stat(filepath.Join(old[0], "user", "states", "0000000000000000.01.cst")); err != nil {
		t.Fatalf("kept save state lost: %v", err)
	}
}

func TestCorruptSessionFile(t *testing.T) {
	m := newManager(t, "")
	ctx := context.Background()
	f := filepath.Join(m.Store.Dir(), "sessions", "a.json")
	if err := os.WriteFile(f, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Launch(ctx, "a", launchOpts(t)); err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("launch over a corrupt session file: %v", err)
	}
	if err := m.Stop(ctx, "a", false); err == nil || !strings.Contains(err.Error(), "removed unreadable") {
		t.Fatalf("stop: %v", err)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Fatal("corrupt session file not removed")
	}
}

func TestXvfbStoppedAfterFailedLaunch(t *testing.T) {
	if _, err := exec.LookPath("Xvfb"); err != nil || runtime.GOOS != "linux" {
		t.Skip("Xvfb not available")
	}
	m := newManager(t, "exit")
	before := countProcs("Xvfb")
	if _, err := m.Launch(context.Background(), "a", LaunchOptions{App: appFile(t)}); !errors.Is(err, ErrStartFailed) {
		t.Fatalf("err = %v", err)
	}
	if after := countProcs("Xvfb"); after != before {
		t.Fatalf("Xvfb processes: %d before, %d after a failed launch", before, after)
	}
}

// countProcs counts running processes with the given command name (Linux).
func countProcs(comm string) int {
	dirs, _ := filepath.Glob("/proc/[0-9]*")
	n := 0
	for _, d := range dirs {
		b, err := os.ReadFile(filepath.Join(d, "comm"))
		if err != nil || strings.TrimSpace(string(b)) != comm {
			continue
		}
		pid, _ := strconv.Atoi(filepath.Base(d))
		if session.Alive(session.ProcFor(pid)) {
			n++
		}
	}
	return n
}

func TestStaleWrapperChildKilledOnPrune(t *testing.T) {
	m := newManager(t, "")
	dir := t.TempDir()
	wrapper := filepath.Join(dir, "azahar-wrapper.sh")
	script := "#!/bin/sh\n\"" + os.Args[0] + "\" \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	m.AzaharBin = wrapper
	pidFile := filepath.Join(dir, "child.pid")
	t.Setenv("MCPAZ_FAKE_PIDFILE", pidFile)
	s, err := m.Launch(context.Background(), "a", launchOpts(t))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	child, _ := strconv.Atoi(string(b))
	childProc := session.ProcFor(child)
	// The wrapper (group leader) crashes; its child keeps the RPC port.
	_ = syscall.Kill(s.Azahar.PID, syscall.SIGKILL)
	waitDead(*s.Azahar, 3*time.Second)
	if !session.Alive(childProc) {
		t.Fatal("child died with the wrapper; test setup broken")
	}
	if removed, err := m.Prune(context.Background()); err != nil || len(removed) != 1 {
		t.Fatalf("Prune = %v, %v", removed, err)
	}
	if !waitDead(childProc, 3*time.Second) {
		t.Fatal("orphaned child of a stale session survived Prune")
	}
}
