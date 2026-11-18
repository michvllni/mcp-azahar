// SPDX-License-Identifier: GPL-3.0-or-later

package ops_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
		for i := 0; i < 2500; i++ {
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
	if len(r.Held.Buttons) != 1 || r.Held.Buttons[0] != "L" {
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
	if _, err := o.Press(ctx, "t", btn(t, "A"), -time.Second); !errors.Is(err, ops.ErrUsage) {
		t.Errorf("negative duration: %v", err)
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

func setLog(t *testing.T, o *ops.Ops, content string) string {
	t.Helper()
	logFile := filepath.Join(t.TempDir(), "azahar_log.txt")
	if err := os.WriteFile(logFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := o.M.Update(context.Background(), "t", func(s *session.Session) error { s.LogPath = logFile; return nil }); err != nil {
		t.Fatal(err)
	}
	return logFile
}

func appendFile(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(text)
	_ = f.Close()
}

func TestLogs(t *testing.T) {
	o, _ := setup(t)
	ctx := context.Background()
	logFile := setLog(t, o, "a one\nb two\na three\nc four\npartial")
	res, err := o.Logs(ctx, "t", ops.LogQuery{Contains: "a "})
	if err != nil || len(res.Lines) != 2 || res.Lines[1] != "a three" {
		t.Fatalf("contains: %+v, %v", res, err)
	}
	res, _ = o.Logs(ctx, "t", ops.LogQuery{Tail: 2})
	if len(res.Lines) != 2 || res.Lines[0] != "a three" || res.Lines[1] != "c four" {
		t.Fatalf("tail: %+v", res)
	}
	// The unfinished last line is left for the next call.
	if res.Offset != int64(len("a one\nb two\na three\nc four\n")) {
		t.Fatalf("offset %d", res.Offset)
	}
	appendFile(t, logFile, " end\nnext\n")
	res2, _ := o.Logs(ctx, "t", ops.LogQuery{From: res.Offset})
	if len(res2.Lines) != 2 || res2.Lines[0] != "partial end" || res2.Lines[1] != "next" {
		t.Fatalf("from offset: %+v", res2)
	}
	// Truncated (Azahar restarted): start over.
	_ = os.WriteFile(logFile, []byte("fresh\n"), 0o600)
	res3, _ := o.Logs(ctx, "t", ops.LogQuery{From: res2.Offset})
	if !res3.Restarted || len(res3.Lines) != 1 || res3.Lines[0] != "fresh" {
		t.Fatalf("after truncation: %+v", res3)
	}
	if empty, _ := o.Logs(ctx, "t", ops.LogQuery{Contains: "zzz"}); empty.Lines == nil {
		t.Fatal("no matches must be an empty slice, not nil")
	}
	if _, err := o.Logs(ctx, "t", ops.LogQuery{Tail: -1}); !errors.Is(err, ops.ErrUsage) {
		t.Fatalf("negative tail: %v", err)
	}
}

func TestLogsTailCompaction(t *testing.T) {
	o, _ := setup(t)
	var b strings.Builder
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	setLog(t, o, b.String())
	res, err := o.Logs(context.Background(), "t", ops.LogQuery{Tail: 3})
	if err != nil || strings.Join(res.Lines, ",") != "line 997,line 998,line 999" {
		t.Fatalf("tail 3 of 1000: %v, %v", res.Lines, err)
	}
}

// follower collects FollowLogs output.
type follower struct {
	mu    sync.Mutex
	lines []string
	done  chan error
}

func (f *follower) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.lines...)
}

func startFollow(ctx context.Context, o *ops.Ops, q ops.LogQuery) *follower {
	f := &follower{done: make(chan error, 1)}
	go func() {
		f.done <- o.FollowLogs(ctx, "t", q, func(l string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.lines = append(f.lines, l)
			return nil
		})
	}()
	return f
}

func TestFollowLogs(t *testing.T) {
	o, _ := setup(t)
	logFile := setLog(t, o, "old 1\nold 2\nhalf")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := startFollow(ctx, o, ops.LogQuery{Tail: 1})
	// The initial tail comes first; only then append.
	deadline := time.Now().Add(5 * time.Second)
	for len(f.got()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	appendFile(t, logFile, " done\nnew\n")
	for len(f.got()) < 3 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := f.got(); strings.Join(got, "|") != "old 2|half done|new" {
		t.Fatalf("follow with tail: %q", got)
	}
	cancel()
	if err := <-f.done; err != nil {
		t.Fatal(err)
	}
}

func TestFollowLogsSkipsPartialStartAndEndsWithSession(t *testing.T) {
	o, _ := setup(t)
	logFile := setLog(t, o, "old\nhalf-written")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := startFollow(ctx, o, ops.LogQuery{})
	deadline := time.Now().Add(5 * time.Second)
	appendFile(t, logFile, " rest\n")
	for i := 0; len(f.got()) == 0 && time.Now().Before(deadline); i++ {
		appendFile(t, logFile, fmt.Sprintf("next %d\n", i))
		time.Sleep(100 * time.Millisecond)
	}
	for _, l := range f.got() {
		// "old" is from before the start; " rest" would be a fragment. The
		// unfinished line may be emitted whole once it is complete.
		if l == "old" || strings.HasPrefix(l, " rest") || l == "half-written" {
			t.Fatalf("emitted a line from before the start or a fragment: %q", f.got())
		}
	}
	// Lines written right before the end are not lost, nor an unfinished
	// last line.
	appendFile(t, logFile, "last\nno newline")
	// Stopping the session ends the follow without cancelling ctx.
	if err := o.Stop(context.Background(), "t", false); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-f.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("follow did not end after the session was stopped")
	}
	got := f.got()
	if len(got) < 2 || got[len(got)-2] != "last" || got[len(got)-1] != "no newline" {
		t.Fatalf("final read missing: %q", got)
	}
}

func TestFollowLogsRecreatedFile(t *testing.T) {
	o, _ := setup(t)
	logFile := setLog(t, o, "a fairly long first line of the old log\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := startFollow(ctx, o, ops.LogQuery{})
	time.Sleep(300 * time.Millisecond) // let it take its start offset
	// Azahar restarts: a new file that is already longer than the old one.
	tmp := logFile + ".new"
	_ = os.WriteFile(tmp, []byte("new 1\nnew 2 with enough text to pass the old offset\n"), 0o600)
	if err := os.Rename(tmp, logFile); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(f.got()) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := f.got(); len(got) < 2 || got[0] != "new 1" {
		t.Fatalf("recreated log not followed from its start: %q", got)
	}
}

func TestSessions(t *testing.T) {
	o, _ := setup(t)
	list, err := o.Sessions(context.Background())
	if err != nil || len(list) != 1 || list[0].Name != "t" || list[0].Kind != session.KindAttached || !list[0].Running {
		t.Fatalf("Sessions = %+v, %v", list, err)
	}
}

func TestPressRestoresAfterLostReply(t *testing.T) {
	o, srv := setup(t)
	o.M.RPCOptions = rpc.Options{Timeout: 100 * time.Millisecond, Retries: -1}
	ctx := context.Background()
	if _, err := o.Hold(ctx, "t", btn(t, "L")); err != nil {
		t.Fatal(err)
	}
	// Azahar applies L+A but the reply is lost: Press fails, yet must not
	// leave A pressed.
	srv.DropRepliesNext(1)
	if _, err := o.Press(ctx, "t", btn(t, "A"), 0); !errors.Is(err, rpc.ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if got := srv.Input(); got.Buttons != rpc.ButtonL {
		t.Fatalf("after failed press Azahar holds %v, want L", got.Buttons.Names())
	}
}

func TestHoldFromAnotherProcessDuringPressIsKept(t *testing.T) {
	o, srv := setup(t)
	// A second Ops on the same store stands for another CLI process: no
	// shared in-process locks, only the session file.
	other := &ops.Ops{M: o.M}
	ctx := context.Background()
	if _, err := o.Hold(ctx, "t", btn(t, "L")); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := o.Press(ctx, "t", btn(t, "A"), 300*time.Millisecond); done <- err }()
	deadline := time.Now().Add(2 * time.Second)
	for srv.Input().Buttons&rpc.ButtonA == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if _, err := other.Hold(ctx, "t", btn(t, "B")); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	s, _ := o.M.Store.Load("t")
	if got := srv.Input(); got.Buttons != rpc.ButtonL|rpc.ButtonB || s.Held.Buttons != got.Buttons {
		t.Fatalf("Azahar %v, session %v, want L+B in both", got.Buttons.Names(), s.Held.Buttons.Names())
	}
}

func TestStopDuringPressReleases(t *testing.T) {
	o, srv := setup(t)
	other := &ops.Ops{M: o.M}
	ctx := context.Background()
	done := make(chan error, 1)
	go func() { _, err := o.Press(ctx, "t", btn(t, "A"), 300*time.Millisecond); done <- err }()
	deadline := time.Now().Add(2 * time.Second)
	for srv.Input().Buttons&rpc.ButtonA == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if err := other.Stop(ctx, "t", false); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("press after stop: %v", err)
	}
	if got := srv.Input(); !got.IsZero() {
		t.Fatalf("Azahar still holds %v after the session was stopped", got.Buttons.Names())
	}
}

func TestStaleSelectionIsReplaced(t *testing.T) {
	o, srv := setup(t)
	ctx := context.Background()
	// A failed load can leave its marker PID selected.
	c, err := rpc.Dial(ctx, rpc.UDPDialer(srv.Addr()), rpc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := c.SelectProcess(ctx, 0xFFFFFFFE); err != nil {
		t.Fatal(err)
	}
	if _, err := o.ReadMemory(ctx, "t", 0x00100000, 4); err != nil {
		t.Fatalf("read with a stale selection: %v", err)
	}
	if srv.Selected() != 11 {
		t.Fatalf("selection %#x, want the app (11)", srv.Selected())
	}
}

func TestLoadStateResendsHeldInput(t *testing.T) {
	o, srv := setup(t)
	ctx := context.Background()
	if _, err := o.Hold(ctx, "t", btn(t, "R")); err != nil {
		t.Fatal(err)
	}
	if err := o.SaveState(ctx, "t", 4); err != nil {
		t.Fatal(err)
	}
	before := srv.CountRequests(rpc.TypeSetInput)
	if err := o.LoadState(ctx, "t", 4); err != nil {
		t.Fatal(err)
	}
	if srv.CountRequests(rpc.TypeSetInput) != before+1 || srv.Input().Buttons != rpc.ButtonR {
		t.Fatalf("held input not re-sent after load: %v", srv.Input().Buttons.Names())
	}
}

func TestParsers(t *testing.T) {
	for in, want := range map[string]uint32{"0x00100000": 0x100000, "0X10": 16, "010": 10, "1_000": 1000} {
		if got, err := ops.ParseAddress(in); err != nil || got != want {
			t.Errorf("ParseAddress(%q) = %#x, %v", in, got, err)
		}
	}
	for _, bad := range []string{"zz", "-1", "0x100000000", ""} {
		if _, err := ops.ParseAddress(bad); !errors.Is(err, ops.ErrUsage) {
			t.Errorf("ParseAddress(%q) accepted", bad)
		}
	}
	if n, err := ops.ParseLength("0x10"); err != nil || n != 16 {
		t.Errorf("ParseLength(0x10) = %d, %v", n, err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"de ad", "beef"}, "deadbeef"},
		{[]string{"0xde", "0XaD"}, "dead"},
		{[]string{"de:ad"}, "dead"},
	} {
		b, err := ops.ParseBytes(tc.args...)
		if err != nil || fmt.Sprintf("%x", b) != tc.want {
			t.Errorf("ParseBytes(%q) = %x, %v", tc.args, b, err)
		}
	}
	for _, bad := range [][]string{{"12", "3", "4"}, {"xyz"}, {""}, {"0x"}} {
		if _, err := ops.ParseBytes(bad...); !errors.Is(err, ops.ErrUsage) {
			t.Errorf("ParseBytes(%q) accepted", bad)
		}
	}
	dump := ops.HexDump([]byte("ABCDEFGHIJKLMNOPQ"), 0x00101000)
	want := "00101000  41 42 43 44 45 46 47 48  49 4a 4b 4c 4d 4e 4f 50  |ABCDEFGHIJKLMNOP|\n" +
		"00101010  51                                                |Q|\n"
	if dump != want {
		t.Errorf("HexDump:\n%s\nwant:\n%s", dump, want)
	}
}

func TestSaveScreenshot(t *testing.T) {
	o, _ := setup(t)
	shots, err := o.Screenshot(context.Background(), "t", capture.Request{Screens: capture.Both})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files, err := ops.SaveScreenshot(shots, filepath.Join(dir, "s.png"), false)
	if err != nil || len(files) != 1 || files[0].Screen != "both" || files[0].Height != 480 {
		t.Fatalf("composite: %+v, %v", files, err)
	}
	files, err = ops.SaveScreenshot(shots, filepath.Join(dir, "x.png"), true)
	if err != nil || len(files) != 2 || !strings.HasSuffix(files[1].Path, "x-bottom.png") {
		t.Fatalf("split: %+v, %v", files, err)
	}
	if _, err := o.Screenshot(context.Background(), "t", capture.Request{Scale: 99}); !errors.Is(err, ops.ErrUsage) {
		t.Fatalf("scale 99: %v", err)
	}
}

func TestSaveStateWaitsForFile(t *testing.T) {
	o, _ := setup(t)
	ctx := context.Background()
	dir := t.TempDir()
	// A launched session (known user dir) whose "emulator" is this process.
	if _, err := o.M.Update(ctx, "t", func(s *session.Session) error {
		s.Kind, s.Dir = session.KindLaunched, dir
		self := session.ProcFor(os.Getpid())
		s.Azahar = &self
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = o.M.Store.Delete("t") }() // never let cleanup stop this process
	states := filepath.Join(dir, "user", "states")
	_ = os.MkdirAll(states, 0o700)
	// Azahar writes the file about a second after accepting; here 200 ms.
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = os.WriteFile(filepath.Join(states, "0000000000000000.02.cst"), []byte("state"), 0o600)
	}()
	start := time.Now()
	if err := o.SaveState(ctx, "t", 2); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 200*time.Millisecond {
		t.Fatalf("SaveState returned after %v, before the file was written", d)
	}
	o.SaveWait = 300 * time.Millisecond
	if err := o.SaveState(ctx, "t", 5); err == nil || !strings.Contains(err.Error(), "did not write") {
		t.Fatalf("no file written: %v", err)
	}
}
