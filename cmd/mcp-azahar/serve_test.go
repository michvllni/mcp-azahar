// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestMain lets the test binary act as mcp-azahar for subprocess tests.
func TestMain(m *testing.M) {
	if os.Getenv("MCPAZ_TEST_MAIN") == "1" {
		os.Exit(run(newRootCmd(), os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// TestServeStdio runs `serve` as a real process: stdout must carry only
// JSON-RPC messages, and the server ends when stdin closes.
func TestServeStdio(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "serve", "--timeout", "1ms")
	cmd.Env = append(os.Environ(), "MCPAZ_TEST_MAIN=1", "MCP_AZAHAR_STATE_DIR="+t.TempDir())
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { // reap the child if the test ends early
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	send := func(s string) {
		if _, err := io.WriteString(stdin, s+"\n"); err != nil {
			t.Fatalf("serve exited early (stdin closed): %v", err)
		}
	}
	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	time.Sleep(50 * time.Millisecond) // past --timeout: it must not apply to serve
	send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"azahar_sessions","arguments":{}}}`)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	seen := map[float64]string{}
	for len(seen) < 3 && sc.Scan() {
		var msg map[string]any
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil || msg["jsonrpc"] != "2.0" {
			t.Fatalf("stdout line is not JSON-RPC: %q", sc.Text())
		}
		if id, ok := msg["id"].(float64); ok {
			seen[id] = sc.Text()
		}
	}
	if !strings.Contains(seen[2], "azahar_press") || !strings.Contains(seen[3], `sessions`) {
		t.Fatalf("responses: %v", seen)
	}
	_ = stdin.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve exited with %v", err)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("serve did not end after stdin closed")
	}
}
