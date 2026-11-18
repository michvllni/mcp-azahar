// SPDX-License-Identifier: GPL-3.0-or-later

package server_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/michvllni/mcp-azahar/internal/ops"
	"github.com/michvllni/mcp-azahar/internal/server"
)

func TestPathsStayInAllowedDirs(t *testing.T) {
	cs, srv, dir := connect(t)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	_ = os.WriteFile(secret, []byte("token\n"), 0o600)
	_ = os.Symlink(outside, filepath.Join(dir, "escape"))
	for name, tc := range map[string]struct {
		tool string
		args map[string]any
	}{
		"attach log":         {"azahar_attach", map[string]any{"session": "x", "address": srv.Addr(), "log": secret}},
		"symlinked log":      {"azahar_attach", map[string]any{"session": "x", "address": srv.Addr(), "log": "escape/secret.txt"}},
		"screenshot output":  {"azahar_screenshot", map[string]any{"output": filepath.Join(outside, "shot.png")}},
		"output via symlink": {"azahar_screenshot", map[string]any{"output": "escape/shot.png"}},
		"output via dotdot":  {"azahar_screenshot", map[string]any{"output": "../shot.png"}},
		"elf":                {"azahar_read_symbol", map[string]any{"target": "counter", "elf": testELF}},
		"sequence file":      {"azahar_sequence", map[string]any{"file": secret}},
		"launch app":         {"azahar_launch", map[string]any{"session": "y", "app": secret}},
	} {
		res, text := call(t, cs, tc.tool, tc.args)
		if !res.IsError || !strings.Contains(text, "outside the directories") {
			t.Errorf("%s: %v %q", name, res.IsError, text)
		}
	}
	// Routes the re-review found: a dangling link as the file itself, the
	// split file names, and links inside a sequence's paths.
	_ = os.Symlink(filepath.Join(outside, "dangling.png"), filepath.Join(dir, "d.png"))
	_ = os.Symlink(filepath.Join(outside, "top.png"), filepath.Join(dir, "s-top.png"))
	_ = os.WriteFile(filepath.Join(outside, "app.elf"), mustRead(t, testELF), 0o600)
	for name, tc := range map[string]struct {
		tool string
		args map[string]any
	}{
		"dangling link output": {"azahar_screenshot", map[string]any{"output": "d.png"}},
		"split name link":      {"azahar_screenshot", map[string]any{"output": "s.png", "screen": "both", "split": true}},
		"sequence output link": {"azahar_sequence", map[string]any{"script": "steps:\n  - screenshot: {screen: top, output: escape/seq.png}\n"}},
		"sequence elf link":    {"azahar_sequence", map[string]any{"script": "elf: escape/app.elf\nsteps:\n  - assert: {target: counter, value: 0}\n"}},
		"sequence split link":  {"azahar_sequence", map[string]any{"script": "steps:\n  - screenshot: {screen: both, split: true, output: s.png}\n"}},
	} {
		res, text := call(t, cs, tc.tool, tc.args)
		if refused := strings.Contains(text, "outside the directories") || strings.Contains(text, "symlink to a missing file"); !res.IsError || !refused {
			t.Errorf("%s: %v %q", name, res.IsError, text)
		}
	}
	for _, f := range []string{"shot.png", "dangling.png", "top.png", "seq.png"} {
		if _, err := os.Stat(filepath.Join(outside, f)); err == nil {
			t.Fatalf("%s was written outside", f)
		}
	}

	// Inside the allowed directory, relative paths work.
	res, text := call(t, cs, "azahar_screenshot", map[string]any{"output": "shot.png"})
	if res.IsError {
		t.Fatalf("output inside: %s", text)
	}
	if _, err := os.Stat(filepath.Join(dir, "shot.png")); err != nil {
		t.Fatalf("screenshot not saved: %v", err)
	}
	elf := filepath.Join(dir, "app.elf")
	b, _ := os.ReadFile(testELF)
	_ = os.WriteFile(elf, b, 0o600)
	if res, text = call(t, cs, "azahar_read_symbol", map[string]any{"target": "counter", "elf": "app.elf"}); res.IsError {
		t.Fatalf("elf inside: %s", text)
	}
	seq := filepath.Join(dir, "smoke.yaml")
	_ = os.WriteFile(seq, []byte("steps:\n  - touch: {x: 10, y: 20}\n  - screenshot: {screen: bottom, output: seq.png}\n"), 0o600)
	if res, text = call(t, cs, "azahar_sequence", map[string]any{"file": "smoke.yaml"}); res.IsError {
		t.Fatalf("sequence file: %s", text)
	}
	if _, err := os.Stat(filepath.Join(dir, "seq.png")); err != nil {
		t.Fatalf("sequence screenshot not next to the file: %v", err)
	}
	log := filepath.Join(dir, "azahar.log")
	_ = os.WriteFile(log, []byte("hello\n"), 0o600)
	if res, text = call(t, cs, "azahar_attach", map[string]any{"session": "x", "address": srv.Addr(), "log": "azahar.log"}); res.IsError {
		t.Fatalf("attach: %s", text)
	}
	if _, text = call(t, cs, "azahar_logs", map[string]any{"session": "x"}); !strings.Contains(text, "hello") {
		t.Fatalf("logs of attached session: %s", text)
	}
	if res, text = call(t, cs, "azahar_stop", map[string]any{"session": "x"}); res.IsError {
		t.Fatalf("stop: %s", text)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestResultLimits(t *testing.T) {
	cs, _, _ := connect(t)
	for tool, extra := range map[string]map[string]any{"azahar_read_symbol": {}, "azahar_wait_for": {"value": "00"}} {
		args := map[string]any{"target": "0x00100000", "type": "bytes", "len": 1 << 20}
		for k, v := range extra {
			args[k] = v
		}
		res, text := call(t, cs, tool, args)
		if !res.IsError || !strings.Contains(text, "over 16384") {
			t.Fatalf("%s with a huge len: %.200s", tool, text)
		}
	}
	res, text := call(t, cs, "azahar_read_memory", map[string]any{"address": "0x00100000", "length": 5000})
	if !res.IsError || !strings.Contains(text, "4096") {
		t.Fatalf("big read: %s", text)
	}
	if res, text = call(t, cs, "azahar_logs", map[string]any{"tail": 5000}); !res.IsError {
		t.Fatalf("big tail: %s", text)
	}
	if res, text = call(t, cs, "azahar_screenshot", map[string]any{"scale": 10}); !res.IsError {
		t.Fatalf("scale 10: %s", text)
	}
	if res, text = call(t, cs, "azahar_screenshot", map[string]any{"scale": 0}); res.IsError {
		t.Fatalf("scale 0 (internal resolution): %s", text)
	}
}

func TestMoreTools(t *testing.T) {
	cs, srv, _ := connect(t)
	if res, text := call(t, cs, "azahar_touch", map[string]any{"x": 10, "y": 20, "hold": true}); res.IsError || srv.Input().Touch == nil {
		t.Fatalf("touch: %s", text)
	}
	if res, text := call(t, cs, "azahar_circle_pad", map[string]any{"x": -154, "y": 154, "for_ms": 10}); res.IsError {
		t.Fatalf("circle pad: %s", text)
	}
	if res, text := call(t, cs, "azahar_release", map[string]any{"touch": true}); res.IsError || srv.Input().Touch != nil {
		t.Fatalf("release touch: %s", text)
	}
	if res, text := call(t, cs, "azahar_save_state", map[string]any{"slot": 2}); res.IsError {
		t.Fatalf("save: %s", text)
	}
	if res, text := call(t, cs, "azahar_load_state", map[string]any{"slot": 2}); res.IsError {
		t.Fatalf("load: %s", text)
	}
}

func TestPanicIsAToolError(t *testing.T) {
	s := server.New(&ops.Ops{}, server.Options{}) // no manager: tools panic
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := s.MCP().Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	for i := 0; i < 2; i++ { // the server survives the first
		res, text := call(t, cs, "azahar_sessions", map[string]any{})
		if !res.IsError || !strings.Contains(text, "internal error") {
			t.Fatalf("panic: %v %s", res.IsError, text)
		}
	}
}
