// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration && linux

package emu

// Live test: launches the real Azahar headless through the Manager.
// Skipped unless MCPAZ_LIVE=1. Uses AZAHAR_BIN (or the default lookup) and
// Xvfb, and needs UDP port 45987 to be free (DECISIONS.md D9).

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/michvllni/mcp-azahar/internal/rpc"
	"github.com/michvllni/mcp-azahar/internal/session"
)

// writeMinimal3DSX writes a 3DSX whose code is an endless loop ("b .")
// plus a marker word, with an 8-byte data segment at 0x00101000 used as
// scratch memory. Layout from Azahar's src/core/loader/3dsx.cpp.
func writeMinimal3DSX(t *testing.T) string {
	t.Helper()
	code := binary.LittleEndian.AppendUint32(nil, 0xEAFFFFFE)
	code = binary.LittleEndian.AppendUint32(code, 0xC0DE0001)
	data := make([]byte, 8)
	b := []byte("3DSX")
	b = binary.LittleEndian.AppendUint16(b, 0x2C) // header size
	b = binary.LittleEndian.AppendUint16(b, 8)    // relocation header size
	b = binary.LittleEndian.AppendUint32(b, 0)    // format version
	b = binary.LittleEndian.AppendUint32(b, 0)    // flags
	for _, v := range []uint32{uint32(len(code)), 0, uint32(len(data)), 0, 0, 0, 0} {
		b = binary.LittleEndian.AppendUint32(b, v) // segment sizes, bss, smdh, romfs
	}
	b = append(b, make([]byte, 3*8)...) // empty relocation headers
	b = append(append(b, code...), data...)
	p := filepath.Join(t.TempDir(), "minimal.3dsx")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLiveLaunch(t *testing.T) {
	if os.Getenv("MCPAZ_LIVE") != "1" {
		t.Skip("MCPAZ_LIVE not set")
	}
	st, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{Store: st}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	start := time.Now()
	s, err := m.Launch(ctx, "live", LaunchOptions{App: writeMinimal3DSX(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("launched in %v on %s: azahar %d, xvfb %v", time.Since(start), s.Display, s.Azahar.PID, s.Xvfb)
	defer func() {
		if err := m.Stop(context.Background(), "live", false); err != nil {
			t.Errorf("Stop: %v", err)
		}
		if session.Alive(*s.Azahar) || (s.Xvfb != nil && session.Alive(*s.Xvfb)) {
			t.Error("processes left after Stop")
		}
	}()

	// A second launch must be refused while this one runs.
	if _, err := m.Launch(ctx, "second", LaunchOptions{App: s.AppPath}); !errors.Is(err, ErrPortInUse) {
		t.Fatalf("second launch: %v, want ErrPortInUse", err)
	}

	_, c, err := m.Client(ctx, "live")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	img, err := c.Screenshot(ctx, rpc.ScreenshotRequest{Screen: rpc.ScreenBottom})
	if err != nil || img.Width != 320 || img.Height != 240 {
		t.Fatalf("bottom screenshot: %+v, %v (seeded single-screen layout?)", img, err)
	}
	if exists, _ := HasState(s, 1); exists {
		t.Fatal("fresh session has a state in slot 1")
	}
	if err := c.SaveState(ctx, 1); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for exists, _ := HasState(s, 1); !exists; exists, _ = HasState(s, 1) {
		if time.Now().After(deadline) {
			t.Fatal("state file for slot 1 not written")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := c.LoadState(ctx, 1); err != nil {
		t.Fatalf("LoadState: %v", err)
	}
}
