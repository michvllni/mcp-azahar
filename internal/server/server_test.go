// SPDX-License-Identifier: GPL-3.0-or-later

package server_test

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/michvllni/mcp-azahar/internal/emu"
	"github.com/michvllni/mcp-azahar/internal/ops"
	"github.com/michvllni/mcp-azahar/internal/rpc"
	"github.com/michvllni/mcp-azahar/internal/rpc/fakeserver"
	"github.com/michvllni/mcp-azahar/internal/server"
	"github.com/michvllni/mcp-azahar/internal/session"
)

var testELF, _ = filepath.Abs("../symbols/testdata/symbols.elf")

// connect starts the MCP server on a session "t" attached to a fake Azahar
// that holds the test ELF's data, and returns a connected client and the
// directory the server allows files in.
func connect(t *testing.T) (*mcp.ClientSession, *fakeserver.Server, string) {
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
	logFile := filepath.Join(t.TempDir(), "azahar_log.txt")
	_ = os.WriteFile(logFile, []byte("boot\nready\n"), 0o600)
	ctx := context.Background()
	if _, err := o.Attach(ctx, "t", emu.AttachOptions{Address: srv.Addr(), ELF: testELF, LogPath: logFile}); err != nil {
		t.Fatal(err)
	}
	f, err := elf.Open(testELF)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range f.Sections {
		if s.Type == elf.SHT_PROGBITS && s.Addr != 0 {
			b, _ := s.Data()
			srv.SetMemory(uint32(s.Addr), b)
		}
	}
	_ = f.Close()

	dir := t.TempDir()
	s := server.New(o, server.Options{Version: "test", Session: "t", AllowDirs: []string{dir}})
	ct, st2 := mcp.NewInMemoryTransports()
	ss, err := s.MCP().Connect(ctx, st2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, srv, dir
}

// call calls a tool and returns its result and joined text content.
func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	var text []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text = append(text, tc.Text)
		}
	}
	return res, strings.Join(text, "\n")
}

func TestListTools(t *testing.T) {
	cs, _, _ := connect(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Description == "" || tool.Annotations == nil || tool.Annotations.Title == "" {
			t.Errorf("%s: missing description or annotations", tool.Name)
		}
	}
	if len(res.Tools) < 20 {
		t.Fatalf("only %d tools", len(res.Tools))
	}
}

func TestToolsDriveTheSession(t *testing.T) {
	cs, srv, _ := connect(t)

	res, text := call(t, cs, "azahar_hold", map[string]any{"buttons": []string{"L"}})
	if res.IsError || srv.Input().Buttons&rpc.ButtonL == 0 {
		t.Fatalf("hold: %s, input %+v", text, srv.Input())
	}
	if res, text = call(t, cs, "azahar_press", map[string]any{"buttons": []string{"a"}, "for_ms": 10}); res.IsError {
		t.Fatalf("press: %s", text)
	}
	if res, text = call(t, cs, "azahar_release", map[string]any{}); res.IsError || !srv.Input().IsZero() {
		t.Fatalf("release: %s", text)
	}

	_, text = call(t, cs, "azahar_read_symbol", map[string]any{"target": "test_state.mode"})
	if !strings.Contains(text, `"MODE_RUN"`) {
		t.Fatalf("read_symbol: %s", text)
	}
	if res, text = call(t, cs, "azahar_write_memory", map[string]any{"address": "0x00101080", "bytes": "09"}); res.IsError {
		t.Fatalf("write: %s", text)
	}
	if _, text = call(t, cs, "azahar_read_memory", map[string]any{"address": "0x00101080", "length": 1}); !strings.Contains(text, `"hex":"09"`) {
		t.Fatalf("read: %s", text)
	}
	if _, text = call(t, cs, "azahar_wait_for", map[string]any{"target": "counter", "value": "9", "timeout_ms": 1000}); !strings.Contains(text, `"polls"`) {
		t.Fatalf("wait_for: %s", text)
	}
	if _, text = call(t, cs, "azahar_symbols", map[string]any{"filter": "tab"}); !strings.Contains(text, `"table"`) {
		t.Fatalf("symbols: %s", text)
	}
	if _, text = call(t, cs, "azahar_logs", map[string]any{"contains": "ready"}); !strings.Contains(text, `"ready"`) {
		t.Fatalf("logs: %s", text)
	}
	if _, text = call(t, cs, "azahar_status", map[string]any{}); !strings.Contains(text, `"answering":true`) {
		t.Fatalf("status: %s", text)
	}
	if res, text = call(t, cs, "azahar_save_state", map[string]any{"slot": 1}); res.IsError {
		t.Fatalf("save: %s", text)
	}
}

func TestScreenshotReturnsImages(t *testing.T) {
	cs, _, _ := connect(t)
	for screen, n := range map[string]int{"top": 1, "both": 1} {
		res, text := call(t, cs, "azahar_screenshot", map[string]any{"screen": screen})
		var imgs []*mcp.ImageContent
		for _, c := range res.Content {
			if ic, ok := c.(*mcp.ImageContent); ok {
				imgs = append(imgs, ic)
			}
		}
		if res.IsError || len(imgs) != n || imgs[0].MIMEType != "image/png" {
			t.Fatalf("%s: %s, %d images", screen, text, len(imgs))
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(imgs[0].Data))
		if err != nil {
			t.Fatalf("%s: not a PNG: %v", screen, err)
		}
		want := map[string][2]int{"top": {400, 240}, "both": {400, 480}}[screen]
		if cfg.Width != want[0] || cfg.Height != want[1] {
			t.Fatalf("%s: %dx%d", screen, cfg.Width, cfg.Height)
		}
	}
	res, _ := call(t, cs, "azahar_screenshot", map[string]any{"screen": "both", "split": true})
	if len(res.Content) != 3 { // two images and the info text
		t.Fatalf("split: %d content items", len(res.Content))
	}
}

func TestSequence(t *testing.T) {
	cs, _, _ := connect(t)
	script := "steps:\n  - press: A\n  - assert: {target: test_state.mode, value: MODE_RUN}\n"
	res, text := call(t, cs, "azahar_sequence", map[string]any{"script": script})
	var rep struct {
		Passed bool `json:"passed"`
	}
	if res.IsError || json.Unmarshal([]byte(text), &rep) != nil || !rep.Passed {
		t.Fatalf("sequence: %s", text)
	}
	res, text = call(t, cs, "azahar_sequence", map[string]any{"script": "steps:\n  - assert: {target: counter, value: 99}\n"})
	if !res.IsError || !strings.Contains(text, "check failed") || !strings.Contains(text, `"error_kind":"check"`) {
		t.Fatalf("failed check: %s", text)
	}
	// Paths may not leave the sequence's directory.
	res, text = call(t, cs, "azahar_sequence", map[string]any{"script": "steps:\n  - screenshot: {screen: top, output: /tmp/x.png}\n"})
	if !res.IsError || !strings.Contains(text, "inside the sequence's directory") {
		t.Fatalf("absolute path: %s", text)
	}
	res, _ = call(t, cs, "azahar_sequence", map[string]any{})
	if !res.IsError {
		t.Fatal("neither file nor script accepted")
	}
}

func TestErrorsAreToolErrors(t *testing.T) {
	cs, _, _ := connect(t)
	for name, tc := range map[string]struct {
		tool string
		args map[string]any
		want string
	}{
		"bad button":   {"azahar_press", map[string]any{"buttons": []string{"TURBO"}}, "invalid arguments"},
		"touch range":  {"azahar_touch", map[string]any{"x": 400, "y": 0}, "invalid arguments"},
		"hold and for": {"azahar_touch", map[string]any{"x": 1, "y": 1, "hold": true, "for_ms": 5}, "exclude"},
		"no session":   {"azahar_press", map[string]any{"session": "nope", "buttons": []string{"A"}}, "no running session"},
		"timeout":      {"azahar_wait_for", map[string]any{"target": "counter", "value": "99", "timeout_ms": 150}, "check failed"},
		"missing arg":  {"azahar_read_memory", map[string]any{"address": "0x0"}, "length"},
		"bad slot":     {"azahar_load_state", map[string]any{"slot": 11}, "invalid arguments"},
	} {
		res, text := call(t, cs, tc.tool, tc.args)
		if !res.IsError || !strings.Contains(text, tc.want) {
			t.Errorf("%s: error %v, %q; want %q", name, res.IsError, text, tc.want)
		}
	}
	// Status of a session that does not exist is an error; of one that
	// exists but does not run it is a report.
	if res, _ := call(t, cs, "azahar_status", map[string]any{"session": "nope"}); !res.IsError {
		t.Error("status of a missing session is not an error")
	}
}
