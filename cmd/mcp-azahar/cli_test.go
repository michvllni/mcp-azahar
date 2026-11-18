// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"debug/elf"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michvllni/mcp-azahar/internal/emu"
	"github.com/michvllni/mcp-azahar/internal/ops"
	"github.com/michvllni/mcp-azahar/internal/rpc"
	"github.com/michvllni/mcp-azahar/internal/rpc/fakeserver"
	"github.com/michvllni/mcp-azahar/internal/sequence"
	"github.com/michvllni/mcp-azahar/internal/session"
)

type cli struct {
	t   *testing.T
	o   *ops.Ops
	srv *fakeserver.Server
}

func newCLI(t *testing.T) *cli {
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
	t.Setenv("MCP_AZAHAR_SESSION", "")
	return &cli{t: t, srv: srv, o: &ops.Ops{M: &emu.Manager{Store: st, RPCOptions: rpc.Options{Timeout: 200 * time.Millisecond}}}}
}

// run executes the CLI in-process and returns exit code, stdout, stderr.
func (c *cli) run(args ...string) (int, string, string) {
	c.t.Helper()
	var out, errOut bytes.Buffer
	cmd := newRootCmdWith(&app{newOps: func() (*ops.Ops, error) { return c.o, nil }})
	code := run(cmd, args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func (c *cli) ok(args ...string) string {
	c.t.Helper()
	code, out, errOut := c.run(args...)
	if code != 0 {
		c.t.Fatalf("%v: exit %d, stderr %q", args, code, errOut)
	}
	return out
}

func TestCLIWorkflow(t *testing.T) {
	c := newCLI(t)
	if out := c.ok("attach", "--address", c.srv.Addr()); !strings.Contains(out, `session "default" attached`) {
		t.Fatalf("attach: %q", out)
	}
	if out := c.ok("status"); !strings.Contains(out, "running") || !strings.Contains(out, `process 11 "fake"`) {
		t.Fatalf("status: %q", out)
	}
	c.ok("hold", "l")
	if out := c.ok("press", "a", "--for", "20ms"); !strings.Contains(out, "pressed A") || !strings.Contains(out, "held now: L") {
		t.Fatalf("press: %q", out)
	}
	c.ok("touch", "160", "120", "--hold")
	if in := c.srv.Input(); in.Buttons != rpc.ButtonL || in.Touch == nil {
		t.Fatalf("input %+v", in)
	}
	if out := c.ok("release"); !strings.Contains(out, "held now: nothing") {
		t.Fatalf("release: %q", out)
	}
	c.ok("write", "0x00101000", "de ad", "beef")
	if out := strings.TrimSpace(c.ok("read", "0x00101000", "4")); out != "deadbeef" {
		t.Fatalf("read: %q", out)
	}
	if out := c.ok("read", "0x00101000", "4", "--format", "dump"); !strings.HasPrefix(out, "00101000  de ad be ef") {
		t.Fatalf("dump: %q", out)
	}
	var jr struct {
		Address, Hex string
	}
	if err := json.Unmarshal([]byte(c.ok("read", "0x00101000", "2", "--json")), &jr); err != nil || jr.Hex != "dead" || jr.Address != "0x00101000" {
		t.Fatalf("read --json: %+v, %v", jr, err)
	}
	shot := filepath.Join(t.TempDir(), "s.png")
	if out := c.ok("screenshot", "--screen", "both", "-o", shot); !strings.Contains(out, "400x480") {
		t.Fatalf("screenshot: %q", out)
	}
	if f, err := os.Open(shot); err != nil {
		t.Fatal(err)
	} else if img, err := png.Decode(f); err != nil || img.Bounds().Dy() != 480 {
		t.Fatalf("png: %v", err)
	}
	split := filepath.Join(t.TempDir(), "x.png")
	c.ok("screenshot", "--screen", "both", "--split", "-o", split)
	for _, s := range []string{"top", "bottom"} {
		if _, err := os.Stat(strings.TrimSuffix(split, ".png") + "-" + s + ".png"); err != nil {
			t.Fatalf("split %s: %v", s, err)
		}
	}
	if out := c.ok("state", "save", "2"); out != "saved slot 2\n" {
		t.Fatalf("state save: %q", out)
	}
	if out := c.ok("state", "load", "2"); out != "loaded slot 2\n" {
		t.Fatalf("state load: %q", out)
	}
	if out := c.ok("sessions"); !strings.Contains(out, "default") || !strings.Contains(out, "attached") {
		t.Fatalf("sessions: %q", out)
	}
	c.ok("stop")
	if code, _, _ := c.run("status"); code != exitNoTarget {
		t.Fatalf("status after stop: exit %d", code)
	}
}

func TestCLIExitCodes(t *testing.T) {
	c := newCLI(t)
	c.ok("attach", "--session", "x", "--address", c.srv.Addr())
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"press", "A"}, exitNoTarget},                       // session "default" missing
		{[]string{"-s", "x", "press"}, exitUsage},                    // no button
		{[]string{"-s", "x", "press", "turbo"}, exitUsage},           // unknown button
		{[]string{"-s", "x", "touch", "400", "1"}, exitUsage},        // off screen
		{[]string{"-s", "x", "read", "zz", "4"}, exitUsage},          // bad address
		{[]string{"-s", "x", "write", "0x101000", "xyz"}, exitUsage}, // bad hex
		{[]string{"-s", "x", "state", "save", "11"}, exitUsage},      // bad slot
		{[]string{"-s", "x", "screenshot", "--screen", "left"}, exitUsage},
		{[]string{"--bogus-flag"}, exitUsage},
		{[]string{"-s", "x", "write", "0x1FF80000", "ff"}, exitError}, // not applied
		{[]string{"-s", "x", "logs"}, exitError},                      // attached without --log
		{[]string{"-s", "x", "status"}, exitOK},
		{[]string{"frobnicate"}, exitUsage},                             // unknown command
		{[]string{"-s", "x", "state"}, exitUsage},                       // no action
		{[]string{"-s", "x", "state", "foo"}, exitUsage},                // unknown action
		{[]string{"-s", "x", "screenshot", "--scale", "99"}, exitUsage}, // scale
		{[]string{"-s", "x", "screenshot", "--split"}, exitUsage},       // split needs both
		{[]string{"-s", "x", "logs", "--tail", "-1"}, exitUsage},        // negative tail
		{[]string{"-s", "x", "press", "a", "--for", "-1s"}, exitUsage},  // negative duration
		{[]string{"-s", "x", "read", "0", "4", "--format", "raw", "--json"}, exitUsage},
		{[]string{"-s", "x", "write", "0x101000", "12", "3", "4"}, exitUsage}, // odd groups
	} {
		if code, _, errOut := c.run(tc.args...); code != tc.code {
			t.Errorf("%v: exit %d, want %d (stderr %q)", tc.args, code, tc.code, errOut)
		}
	}
}

func TestCLIJSON(t *testing.T) {
	c := newCLI(t)
	var s ops.SessionView
	if err := json.Unmarshal([]byte(c.ok("attach", "--address", c.srv.Addr(), "--json")), &s); err != nil || s.RPC.Address != c.srv.Addr() {
		t.Fatalf("attach --json: %+v, %v", s, err)
	}
	var r ops.InputResult
	if err := json.Unmarshal([]byte(c.ok("hold", "a", "b", "--json")), &r); err != nil || strings.Join(r.Held.Buttons, ",") != "A,B" {
		t.Fatalf("hold --json: %+v, %v", r, err)
	}
	var v map[string]string
	if err := json.Unmarshal([]byte(c.ok("version", "--json")), &v); err != nil || v["version"] != version {
		t.Fatalf("version --json: %v, %v", v, err)
	}
}

func TestSessionFromEnv(t *testing.T) {
	c := newCLI(t)
	t.Setenv("MCP_AZAHAR_SESSION", "fromenv")
	c.ok("attach", "--address", c.srv.Addr())
	if _, err := c.o.M.Store.Load("fromenv"); err != nil {
		t.Fatalf("session from $MCP_AZAHAR_SESSION not used: %v", err)
	}
}

func TestCLIStatusNotAnswering(t *testing.T) {
	c := newCLI(t)
	c.ok("attach", "--address", c.srv.Addr())
	_ = c.srv.Close()
	code, out, errOut := c.run("status")
	if code != exitError || !strings.Contains(out, "RPC not answering") || !strings.Contains(errOut, "not answering") {
		t.Fatalf("exit %d, out %q, err %q", code, out, errOut)
	}
}

func TestCLIJSONShapes(t *testing.T) {
	c := newCLI(t)
	c.ok("attach", "--address", c.srv.Addr())
	var st map[string]any
	if err := json.Unmarshal([]byte(c.ok("status", "--json")), &st); err != nil {
		t.Fatal(err)
	}
	if _, ok := st["processes"].([]any); !ok {
		t.Fatalf("processes is not an array: %v", st["processes"])
	}
	held := st["session"].(map[string]any)["held"].(map[string]any)
	if b, ok := held["buttons"].([]any); !ok || len(b) != 0 {
		t.Fatalf("held buttons must be an empty array: %v", held["buttons"])
	}
}

// attachWithELF attaches to the fake with the symbols test ELF loaded into
// its memory.
func attachWithELF(t *testing.T, c *cli) string {
	t.Helper()
	elfPath, _ := filepath.Abs("../../internal/symbols/testdata/symbols.elf")
	f, err := elf.Open(elfPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for _, s := range f.Sections {
		if s.Type == elf.SHT_PROGBITS && s.Addr != 0 {
			b, _ := s.Data()
			c.srv.SetMemory(uint32(s.Addr), b)
		}
	}
	c.ok("attach", "--address", c.srv.Addr(), "--elf", elfPath)
	return elfPath
}

func TestCLISymbols(t *testing.T) {
	c := newCLI(t)
	attachWithELF(t, c)
	if out := c.ok("symbols", "--filter", "state"); !strings.Contains(out, "0x00101058") || !strings.Contains(out, "struct test_state") {
		t.Fatalf("symbols: %q", out)
	}
	if out := c.ok("read-symbol", "test_state.touch"); out != "test_state.touch = {\"x\":160,\"y\":120}\n" {
		t.Fatalf("read-symbol: %q", out)
	}
	var v ops.Value
	if err := json.Unmarshal([]byte(c.ok("read-symbol", "test_state.mode", "--json")), &v); err != nil || v.Value != "MODE_RUN" || v.Address != "0x00101079" {
		t.Fatalf("read-symbol --json: %+v, %v", v, err)
	}
	if out := c.ok("read-symbol", "0x00101080", "--type", "u8"); !strings.Contains(out, "= 3") {
		t.Fatalf("read-symbol address: %q", out)
	}
	if out := c.ok("wait-for", "test_state.frame", "7", "--timeout", "2s"); !strings.HasPrefix(out, "reached: test_state.frame = 7") {
		t.Fatalf("wait-for: %q", out)
	}
	code, out, _ := c.run("wait-for", "test_state.frame", "8", "--timeout", "300ms")
	if code != exitCheck || !strings.HasPrefix(out, "timed out: test_state.frame = 7") {
		t.Fatalf("wait-for timeout: exit %d, %q", code, out)
	}
	for _, args := range [][]string{
		{"read-symbol", "nope"},
		{"read-symbol", "0x10", "--type", "u128"},
		{"wait-for", "counter"},
		{"wait-for", "counter", "1", "--op", "about"},
	} {
		if code, _, errOut := c.run(args...); code != exitUsage {
			t.Errorf("%v: exit %d (%s), want 2", args, code, errOut)
		}
	}
}

func TestCLISequence(t *testing.T) {
	c := newCLI(t)
	attachWithELF(t, c)
	dir := t.TempDir()
	pass := filepath.Join(dir, "pass.yaml")
	_ = os.WriteFile(pass, []byte("steps:\n  - press: A\n  - assert: {target: test_state.frame, value: 7}\n  - screenshot: {output: shot.png}\n"), 0o600)
	if out := c.ok("sequence", pass); !strings.Contains(out, "ok    1 press") || !strings.HasSuffix(out, "passed: 3 steps\n") {
		t.Fatalf("passing sequence: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "shot.png")); err != nil {
		t.Fatalf("screenshot not relative to the sequence file: %v", err)
	}
	fail := filepath.Join(dir, "fail.yaml")
	_ = os.WriteFile(fail, []byte("steps:\n  - assert: {target: test_state.frame, value: 8}\n  - press: B\n"), 0o600)
	code, out, _ := c.run("sequence", fail)
	if code != exitCheck || !strings.Contains(out, "FAIL  1 assert") || !strings.Contains(out, "1 skipped") {
		t.Fatalf("failing sequence: exit %d, %q", code, out)
	}
	code, out, _ = c.run("sequence", fail, "--json")
	var rep sequence.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil || code != exitCheck || rep.Passed || rep.Skipped != 1 {
		t.Fatalf("--json: exit %d, %+v, %v", code, rep, err)
	}
	// From stdin.
	var stdout, stderr bytes.Buffer
	cmd := newRootCmdWith(&app{newOps: func() (*ops.Ops, error) { return c.o, nil }})
	cmd.SetIn(strings.NewReader(`{"steps": [{"press": "A"}]}`))
	if code := run(cmd, []string{"sequence", "-"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("stdin: exit %d, %s", code, stderr.String())
	}
	bad := filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(bad, []byte("steps:\n  - press: A\n    wait: 1s\n"), 0o600)
	if code, _, _ := c.run("sequence", bad); code != exitUsage {
		t.Fatalf("invalid file: exit %d, want 2", code)
	}
}
