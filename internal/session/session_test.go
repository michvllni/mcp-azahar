// SPDX-License-Identifier: GPL-3.0-or-later

package session

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestSaveLoadListDelete(t *testing.T) {
	st := newStore(t)
	in := &Session{
		Name: "b", Kind: KindLaunched, CreatedAt: time.Unix(1, 0).UTC(),
		RPC:    Endpoint{Network: "udp", Address: "127.0.0.1:45987"},
		Azahar: &Proc{PID: 42, Start: 7}, Display: ":99", Dir: "/x/b.d",
	}
	if err := st.Save(in); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(&Session{Name: "a", Kind: KindAttached}); err != nil {
		t.Fatal(err)
	}
	out, err := st.Load("b")
	if err != nil {
		t.Fatal(err)
	}
	if out.RPC != in.RPC || *out.Azahar != *in.Azahar || out.Display != ":99" || !out.CreatedAt.Equal(in.CreatedAt) {
		t.Fatalf("round trip: %+v", out)
	}
	list, err := st.List()
	if err != nil || len(list) != 2 || list[0].Name != "a" || list[1].Name != "b" {
		t.Fatalf("List = %v, %v", list, err)
	}
	if err := st.Delete("b"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Load("b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if err := st.Delete("b"); err != nil {
		t.Fatalf("deleting a missing session: %v", err)
	}
}

func TestListSkipsJunk(t *testing.T) {
	st := newStore(t)
	dir := filepath.Join(st.Dir(), "sessions")
	_ = os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, ".tmp.json"), []byte("{}"), 0o600)
	_ = os.Mkdir(filepath.Join(dir, "x.d"), 0o700)
	if list, err := st.List(); err != nil || len(list) != 0 {
		t.Fatalf("List = %v, %v", list, err)
	}
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"default", "a", "ci-run_1.2", "A9"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".hidden", "-x", "a/b", "../x", "a b", string(make([]byte, 65))} {
		if ValidateName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestStateFile(t *testing.T) {
	s := &Session{Dir: "/s"}
	if got := s.StateFile(0x0004000000ABCD00, 3); got != filepath.Join("/s", "user", "states", "0004000000ABCD00.03.cst") {
		t.Fatalf("StateFile = %q", got)
	}
	if (&Session{}).StateFile(1, 1) != "" {
		t.Fatal("attached session has no state file path")
	}
}

func TestDefaultDir(t *testing.T) {
	t.Setenv("MCP_AZAHAR_STATE_DIR", "")
	t.Setenv("XDG_STATE_HOME", "/xdg")
	if d, _ := DefaultDir(); d != filepath.Join("/xdg", "mcp-azahar") {
		t.Fatalf("XDG: %q", d)
	}
	t.Setenv("MCP_AZAHAR_STATE_DIR", "/override")
	if d, _ := DefaultDir(); d != "/override" {
		t.Fatalf("override: %q", d)
	}
}

func TestLockIsExclusive(t *testing.T) {
	st := newStore(t)
	unlock, err := st.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := st.Lock(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock: %v, want a timeout", err)
	}
	unlock()
	unlock2, err := st.Lock(context.Background())
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	unlock2()
}

func TestAlive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sleep(1)")
	}
	self := ProcFor(os.Getpid())
	if !Alive(self) {
		t.Fatal("own process reported dead")
	}
	if Alive(Proc{PID: 0}) {
		t.Fatal("PID 0 reported alive")
	}
	cmd := exec.CommandContext(t.Context(), "sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	p := ProcFor(cmd.Process.Pid)
	if !Alive(p) {
		t.Fatal("child reported dead")
	}
	_ = cmd.Process.Kill()
	// Not reaped yet: a zombie must count as dead (Linux, where /proc exists).
	if runtime.GOOS == "linux" {
		deadline := time.Now().Add(2 * time.Second)
		for Alive(p) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if Alive(p) {
			t.Fatal("zombie reported alive")
		}
	}
	_ = cmd.Wait()
	if Alive(p) {
		t.Fatal("reaped child reported alive")
	}
	if runtime.GOOS == "linux" && Alive(Proc{PID: os.Getpid(), Start: self.Start + 1}) {
		t.Fatal("reused PID (different start time) reported alive")
	}
}

func TestRunning(t *testing.T) {
	if !(&Session{Kind: KindAttached}).Running() {
		t.Fatal("attached sessions count as running")
	}
	if (&Session{Kind: KindLaunched}).Running() {
		t.Fatal("launched session without process counts as running")
	}
	if !(&Session{Kind: KindLaunched, Azahar: &Proc{PID: os.Getpid()}}).Running() {
		t.Fatal("launched session with a live process counts as stopped")
	}
}
