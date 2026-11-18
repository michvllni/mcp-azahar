// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
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
	if out := c.ok("press", "a", "--hold", "20ms"); !strings.Contains(out, "pressed A") || !strings.Contains(out, "held now: L") {
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
	} {
		if code, _, errOut := c.run(tc.args...); code != tc.code {
			t.Errorf("%v: exit %d, want %d (stderr %q)", tc.args, code, tc.code, errOut)
		}
	}
}

func TestCLIJSON(t *testing.T) {
	c := newCLI(t)
	var s session.Session
	if err := json.Unmarshal([]byte(c.ok("attach", "--address", c.srv.Addr(), "--json")), &s); err != nil || s.RPC.Address != c.srv.Addr() {
		t.Fatalf("attach --json: %+v, %v", s, err)
	}
	var r ops.InputResult
	if err := json.Unmarshal([]byte(c.ok("hold", "a", "b", "--json")), &r); err != nil || strings.Join(r.HeldButtons, ",") != "A,B" {
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
