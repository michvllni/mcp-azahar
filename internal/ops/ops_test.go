// SPDX-License-Identifier: GPL-3.0-or-later

package ops_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/michvllni/mcp-azahar/internal/capture"
	"github.com/michvllni/mcp-azahar/internal/emu"
	"github.com/michvllni/mcp-azahar/internal/ops"
	"github.com/michvllni/mcp-azahar/internal/rpc"
	"github.com/michvllni/mcp-azahar/internal/rpc/fakeserver"
	"github.com/michvllni/mcp-azahar/internal/session"
)

// setup returns ops with an attached session "t" on a fake server.
func setup(t *testing.T) (*ops.Ops, *fakeserver.Server) {
	t.Helper()
	srv, err := fakeserver.New(fakeserver.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	st, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	o := &ops.Ops{M: &emu.Manager{Store: st, RPCOptions: rpc.Options{Timeout: 200 * time.Millisecond}}}
	if _, err := o.Attach(context.Background(), "t", emu.AttachOptions{Address: srv.Addr()}); err != nil {
		t.Fatal(err)
	}
	return o, srv
}

func btn(t *testing.T, names ...string) rpc.Input {
	t.Helper()
	in, err := ops.ParseButtons(names)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func TestParseButtons(t *testing.T) {
	in, err := ops.ParseButtons([]string{"a", "Start", "home"})
	if err != nil || in.Buttons != rpc.ButtonA|rpc.ButtonStart || !in.Home || in.Power {
		t.Fatalf("got %+v, %v", in, err)
	}
	if _, err := ops.ParseButtons([]string{"turbo"}); !errors.Is(err, ops.ErrUsage) {
		t.Fatalf("unknown button: %v", err)
	}
}

func TestPressReturnsToHeldInput(t *testing.T) {
	o, srv := setup(t)
	ctx := context.Background()
	if _, err := o.Hold(ctx, "t", btn(t, "L")); err != nil {
		t.Fatal(err)
	}
	// Watch what Azahar sees during the press.
	var mu sync.Mutex
	var during rpc.Input
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			if in := srv.Input(); in.Buttons&rpc.ButtonA != 0 {
				mu.Lock()
				during = in
				mu.Unlock()
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	r, err := o.Press(ctx, "t", btn(t, "A"), 80*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	<-done
	mu.Lock()
	defer mu.Unlock()
	if during.Buttons != rpc.ButtonA|rpc.ButtonL {
		t.Fatalf("during press Azahar saw %v, want A+L", during.Buttons.Names())
	}
	if got := srv.Input(); got.Buttons != rpc.ButtonL {
		t.Fatalf("after press: %v, want L still held", got.Buttons.Names())
	}
	if len(r.HeldButtons) != 1 || r.HeldButtons[0] != "L" {
		t.Fatalf("result %+v", r)
	}
}

func TestHoldTouchReleasePersist(t *testing.T) {
	o, srv := setup(t)
	ctx := context.Background()
	if _, err := o.Hold(ctx, "t", btn(t, "A", "B")); err != nil {
		t.Fatal(err)
	}
	// A separate call (as from another CLI process) keeps A+B while touching.
	if _, err := o.Touch(ctx, "t", 10, 20, true, 0); err != nil {
		t.Fatal(err)
	}
	got := srv.Input()
	if got.Buttons != rpc.ButtonA|rpc.ButtonB || got.Touch == nil || *got.Touch != (rpc.Point{X: 10, Y: 20}) {
		t.Fatalf("Azahar input %+v", got)
	}
	s, _ := o.M.Store.Load("t")
	if s.Held.Buttons != rpc.ButtonA|rpc.ButtonB || s.Held.Touch == nil {
		t.Fatalf("held input not persisted: %+v", s.Held)
	}
	if _, err := o.Release(ctx, "t", ops.ReleaseWhat{Buttons: btn(t, "A")}); err != nil {
		t.Fatal(err)
	}
	if got := srv.Input(); got.Buttons != rpc.ButtonB || got.Touch == nil {
		t.Fatalf("after release A: %+v", got)
	}
	if _, err := o.Release(ctx, "t", ops.ReleaseWhat{Touch: true}); err != nil {
		t.Fatal(err)
	}
	if got := srv.Input(); got.Touch != nil || got.Buttons != rpc.ButtonB {
		t.Fatalf("after release touch: %+v", got)
	}
	if _, err := o.Release(ctx, "t", ops.ReleaseWhat{}); err != nil {
		t.Fatal(err)
	}
	if got := srv.Input(); !got.IsZero() {
		t.Fatalf("after release all: %+v", got)
	}
	if s, _ := o.M.Store.Load("t"); !s.Held.IsZero() {
		t.Fatalf("held input not cleared: %+v", s.Held)
	}
}

func TestInputValidation(t *testing.T) {
	o, _ := setup(t)
	ctx := context.Background()
	for name, err := range map[string]error{
		"touch x":    func() error { _, err := o.Touch(ctx, "t", 320, 0, false, 0); return err }(),
		"touch neg":  func() error { _, err := o.Touch(ctx, "t", -1, 0, false, 0); return err }(),
		"circle":     func() error { _, err := o.CirclePad(ctx, "t", 155, 0, true, 0); return err }(),
		"empty":      func() error { _, err := o.Press(ctx, "t", rpc.Input{}, 0); return err }(),
		"empty hold": func() error { _, err := o.Hold(ctx, "t", rpc.Input{}); return err }(),
	} {
		if !errors.Is(err, ops.ErrUsage) {
			t.Errorf("%s: %v, want ErrUsage", name, err)
		}
	}
	if _, err := o.Press(ctx, "missing", btn(t, "A"), 0); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("missing session: %v", err)
	}
}

func TestMemorySelectsApp(t *testing.T) {
	o, srv := setup(t)
	ctx := context.Background()
	if err := o.WriteMemory(ctx, "t", 0x00101000, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if srv.Selected() != 11 {
		t.Fatalf("app process not selected: %d", srv.Selected())
	}
	b, err := o.ReadMemory(ctx, "t", 0x00101000, 3)
	if err != nil || !bytes.Equal(b, []byte{1, 2, 3}) {
		t.Fatalf("read % x, %v", b, err)
	}
	if err := o.WriteMemory(ctx, "t", 0x1FF80000, []byte{1}); !errors.Is(err, rpc.ErrWriteNotApplied) {
		t.Fatalf("protected write: %v", err)
	}
	if _, err := o.ReadMemory(ctx, "t", 0, 0); !errors.Is(err, ops.ErrUsage) {
		t.Fatalf("zero-length read: %v", err)
	}
}

func TestScreenshotAndStates(t *testing.T) {
	o, _ := setup(t)
	ctx := context.Background()
	shots, err := o.Screenshot(ctx, "t", capture.Request{Screens: capture.Both})
	if err != nil || len(shots) != 2 {
		t.Fatalf("screenshot: %v, %v", shots, err)
	}
	if err := o.SaveState(ctx, "t", 1); err != nil {
		t.Fatal(err)
	}
	if err := o.LoadState(ctx, "t", 1); err != nil {
		t.Fatal(err)
	}
}

func TestLoadStateChecksSlotFile(t *testing.T) {
	o, srv := setup(t)
	ctx := context.Background()
	// Turn the session into a launched one with a known user dir.
	dir := t.TempDir()
	if _, err := o.M.Update(ctx, "t", func(s *session.Session) error {
		s.Kind, s.Dir = session.KindLaunched, dir
		self := session.ProcFor(os.Getpid())
		s.Azahar = &self
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := o.LoadState(ctx, "t", 3); !errors.Is(err, ops.ErrUsage) || !strings.Contains(err.Error(), "no save state") {
		t.Fatalf("empty slot: %v", err)
	}
	if n := srv.CountRequests(rpc.TypeLoadState); n != 0 {
		t.Fatalf("LoadState sent to Azahar for an empty slot (%d)", n)
	}
	// Detach the session from this test process before cleanup stops it.
	_ = o.M.Store.Delete("t")
}

func TestLogs(t *testing.T) {
	o, _ := setup(t)
	ctx := context.Background()
	logFile := filepath.Join(t.TempDir(), "azahar_log.txt")
	if err := os.WriteFile(logFile, []byte("a one\nb two\na three\nc four\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := o.M.Update(ctx, "t", func(s *session.Session) error { s.LogPath = logFile; return nil }); err != nil {
		t.Fatal(err)
	}
	lines, err := o.Logs(ctx, "t", ops.LogQuery{Contains: "a "})
	if err != nil || len(lines) != 2 || lines[1] != "a three" {
		t.Fatalf("contains: %v, %v", lines, err)
	}
	lines, _ = o.Logs(ctx, "t", ops.LogQuery{Tail: 2})
	if len(lines) != 2 || lines[0] != "a three" || lines[1] != "c four" {
		t.Fatalf("tail: %v", lines)
	}

	fctx, cancel := context.WithCancel(ctx)
	var out bytes.Buffer
	var mu sync.Mutex
	w := writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return out.Write(p) })
	done := make(chan error, 1)
	go func() { done <- o.FollowLogs(fctx, "t", ops.LogQuery{Contains: "new"}, w) }()
	time.Sleep(100 * time.Millisecond)
	f, _ := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("old\nnew line\n")
	_ = f.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := out.String()
		mu.Unlock()
		if got == "new line\n" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if out.String() != "new line\n" {
		t.Fatalf("follow output %q", out.String())
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func TestSessions(t *testing.T) {
	o, _ := setup(t)
	list, err := o.Sessions(context.Background())
	if err != nil || len(list) != 1 || list[0].Name != "t" || list[0].Kind != session.KindAttached || !list[0].Running {
		t.Fatalf("Sessions = %+v, %v", list, err)
	}
}
