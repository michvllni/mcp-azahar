// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFixture runs the CLI binary (this test binary, see TestMain) against
// the real Azahar with the fixture homebrew (testdata/fixture). It needs
// MCPAZ_FIXTURE_DIR (holding fixture.3dsx and fixture.elf) and an Azahar
// ($AZAHAR_BIN), and UDP port 45987 free:
//
//	MCPAZ_FIXTURE_DIR=build go test -tags integration -run TestFixture -v ./cmd/mcp-azahar/
func TestFixture(t *testing.T) {
	fix := os.Getenv("MCPAZ_FIXTURE_DIR")
	if fix == "" {
		t.Skip("MCPAZ_FIXTURE_DIR not set")
	}
	fix, _ = filepath.Abs(fix)
	app, elf := filepath.Join(fix, "fixture.3dsx"), filepath.Join(fix, "fixture.elf")
	work := t.TempDir()
	env := append(os.Environ(), "MCPAZ_TEST_MAIN=1", "MCP_AZAHAR_STATE_DIR="+filepath.Join(work, "state"), "MCP_AZAHAR_SESSION=it")

	// cli runs a command and returns stdout, failing the test unless the
	// exit code is want.
	cli := func(t *testing.T, want int, args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], args...)
		cmd.Env, cmd.Dir = env, work
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if code != want {
			t.Fatalf("%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out.String(), errb.String())
		}
		return out.String()
	}
	value := func(t *testing.T, target string) string {
		t.Helper()
		var v struct {
			Value json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal([]byte(cli(t, 0, "read-symbol", target, "--json")), &v); err != nil {
			t.Fatal(err)
		}
		return string(v.Value)
	}

	cli(t, 0, "launch", app, "--elf", elf)
	t.Cleanup(func() { cli(t, 0, "stop") })
	cli(t, 0, "wait-for", "test_state.mode", "MODE_RUN", "--timeout", "30s")
	if got := value(t, "test_state.magic"); got != "1481197140" { // 0x58494654
		t.Fatalf("magic = %s", got)
	}
	cli(t, 0, "wait-for", "test_state.frame", "--op", "changed", "--timeout", "10s")

	t.Run("press", func(t *testing.T) {
		cli(t, 0, "press", "B", "--for", "200ms") // KEY_B = 2
		cli(t, 0, "wait-for", "test_state.last_buttons", "2", "--timeout", "5s")
		cli(t, 0, "press", "A", "--for", "200ms")
		cli(t, 0, "wait-for", "test_state.last_buttons", "1", "--timeout", "5s")
	})
	t.Run("touch", func(t *testing.T) {
		cli(t, 0, "touch", "160", "120", "--for", "200ms")
		cli(t, 0, "wait-for", "test_state.touches", "1", "--op", "ge", "--timeout", "5s")
		if x, y := value(t, "test_state.touch_x"), value(t, "test_state.touch_y"); x != "160" || y != "120" {
			t.Fatalf("touch at %s, %s", x, y)
		}
	})
	t.Run("hold", func(t *testing.T) {
		cli(t, 0, "hold", "L")
		cli(t, 0, "wait-for", "test_state.held", "512", "--timeout", "5s") // KEY_L
		cli(t, 0, "release")
		cli(t, 0, "wait-for", "test_state.held", "0", "--timeout", "5s")
	})
	t.Run("screenshot", func(t *testing.T) {
		out := filepath.Join(work, "both.png")
		cli(t, 0, "screenshot", "--screen", "both", "-o", out)
		f, err := os.Open(out)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		img, err := png.Decode(f)
		if err != nil {
			t.Fatal(err)
		}
		checkFixtureImage(t, img)
	})
	t.Run("save and load state", func(t *testing.T) {
		cli(t, 0, "write", "0x"+addressOf(t, cli, "test_state.scratch"), "11 22 33 44")
		cli(t, 0, "state", "save", "1")
		cli(t, 0, "write", "0x"+addressOf(t, cli, "test_state.scratch"), "55 66 77 88")
		if got := value(t, "test_state.scratch"); got != "2289526357" { // 0x88776655
			t.Fatalf("scratch before load = %s", got)
		}
		cli(t, 0, "state", "load", "1")
		cli(t, 0, "wait-for", "test_state.scratch", "0x44332211", "--timeout", "10s")
	})
	t.Run("logs", func(t *testing.T) {
		if out := cli(t, 0, "logs", "--contains", "fixture: ready"); !strings.Contains(out, "fixture: ready") {
			t.Fatalf("log lacks the fixture's line:\n%s", cli(t, 0, "logs", "--tail", "30"))
		}
	})
	t.Run("one emulator per host", func(t *testing.T) {
		// D9: a second launch fails clearly while the first runs.
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "launch", app, "--session", "second")
		cmd.Env, cmd.Dir = env, work
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "45987") {
			t.Fatalf("second launch: %v\n%s", err, out)
		}
	})
	t.Run("sequence", func(t *testing.T) {
		src, err := os.ReadFile("../../testdata/fixture/smoke.yaml")
		if err != nil {
			t.Fatal(err)
		}
		seq := filepath.Join(work, "smoke.yaml") // its screenshot lands next to it
		if err := os.WriteFile(seq, src, 0o600); err != nil {
			t.Fatal(err)
		}
		cli(t, 0, "sequence", seq)
		if _, err := os.Stat(filepath.Join(work, "smoke.png")); err != nil {
			t.Fatal(err)
		}
		// A failing check exits 3.
		bad := filepath.Join(work, "bad.yaml")
		_ = os.WriteFile(bad, []byte("steps:\n  - assert: {target: test_state.magic, value: 0}\n"), 0o600)
		cli(t, 3, "sequence", bad)
	})
	t.Run("mcp", func(t *testing.T) { mcpSmoke(t, env, work) })
}

// addressOf returns the hex address of a symbol (without 0x).
func addressOf(t *testing.T, cli func(*testing.T, int, ...string) string, target string) string {
	t.Helper()
	var v struct {
		Address string `json:"address"`
	}
	if err := json.Unmarshal([]byte(cli(t, 0, "read-symbol", target, "--json")), &v); err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(v.Address, "0x")
}

// checkFixtureImage checks the composed screenshot (400x480: top above
// bottom): the top shows red, green, blue and white bars, the bottom is dark
// gray with a white square at the last touch (160, 120).
func checkFixtureImage(t *testing.T, img image.Image) {
	t.Helper()
	if b := img.Bounds(); b.Dx() != 400 || b.Dy() != 480 {
		t.Fatalf("size %v", b)
	}
	rgb := func(x, y int) [3]uint32 {
		r, g, b, _ := img.At(x, y).RGBA()
		return [3]uint32{r >> 8, g >> 8, b >> 8}
	}
	near := func(got, want [3]uint32) bool {
		for i := range got {
			d := int(got[i]) - int(want[i])
			if d < -24 || d > 24 {
				return false
			}
		}
		return true
	}
	for _, c := range []struct {
		x, y int
		want [3]uint32
	}{
		{50, 120, [3]uint32{255, 0, 0}},
		{150, 120, [3]uint32{0, 255, 0}},
		{250, 120, [3]uint32{0, 0, 255}},
		{350, 120, [3]uint32{255, 255, 255}},
		// Bottom screen: 320 wide, centered (offset 40), below the top.
		{40 + 10, 240 + 10, [3]uint32{0x20, 0x20, 0x20}},
		{40 + 160, 240 + 120, [3]uint32{255, 255, 255}},
	} {
		if got := rgb(c.x, c.y); !near(got, c.want) {
			t.Errorf("pixel (%d, %d) = %v, want about %v", c.x, c.y, got, c.want)
		}
	}
}

// mcpSmoke starts `serve` on the running session and takes a screenshot
// over stdio.
func mcpSmoke(t *testing.T, env []string, dir string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "serve")
	cmd.Env, cmd.Dir = env, dir
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Wait() }()
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	id := 0
	request := func(method string, params any) map[string]any {
		t.Helper()
		id++
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if _, err := io.WriteString(stdin, string(b)+"\n"); err != nil {
			t.Fatal(err)
		}
		for sc.Scan() {
			var msg map[string]any
			if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
				t.Fatalf("not JSON-RPC: %q", sc.Text())
			}
			if msg["id"] == float64(id) {
				if msg["error"] != nil {
					t.Fatalf("%s: %v", method, msg["error"])
				}
				return msg["result"].(map[string]any)
			}
		}
		t.Fatalf("%s: no response (%v)", method, sc.Err())
		return nil
	}
	request("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "it", "version": "0"}})
	_, _ = io.WriteString(stdin, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
	res := request("tools/call", map[string]any{"name": "azahar_screenshot", "arguments": map[string]any{"screen": "both"}})
	if res["isError"] == true {
		t.Fatalf("screenshot: %v", res["content"])
	}
	for _, c := range res["content"].([]any) {
		m := c.(map[string]any)
		if m["type"] != "image" {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(m["data"].(string))
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		checkFixtureImage(t, img)
		return
	}
	t.Fatalf("no image content: %v", res["content"])
}
