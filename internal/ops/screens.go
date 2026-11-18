// SPDX-License-Identifier: GPL-3.0-or-later

package ops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/michvllni/mcp-azahar/internal/capture"
	"github.com/michvllni/mcp-azahar/internal/emu"
	"github.com/michvllni/mcp-azahar/internal/rpc"
)

// Screenshot captures one or both screens (Both: two consecutive frames).
func (o *Ops) Screenshot(ctx context.Context, name string, req capture.Request) ([]capture.Shot, error) {
	if req.Scale < 0 || req.Scale > rpc.MaxScreenshotScale {
		return nil, usage("scale %d outside 0..%d", req.Scale, rpc.MaxScreenshotScale)
	}
	l := o.locksFor(name)
	l.shot.Lock()
	defer l.shot.Unlock()
	_, c, err := o.client(ctx, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	return capture.RPC{Client: c}.Capture(ctx, req)
}

// SavedFile is a written screenshot.
type SavedFile struct {
	Path   string `json:"path"`
	Screen string `json:"screen"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// SaveScreenshot writes shots to path: one image (both screens composed in
// 3DS arrangement), or with split one file per screen named
// <path without extension>-<screen>.png.
func SaveScreenshot(shots []capture.Shot, path string, split bool) ([]SavedFile, error) {
	if split && len(shots) > 1 {
		base := strings.TrimSuffix(path, filepath.Ext(path))
		files := make([]SavedFile, 0, len(shots))
		for _, s := range shots {
			p := base + "-" + s.Screen + ".png"
			if err := os.WriteFile(p, s.PNG, 0o644); err != nil {
				return nil, err
			}
			files = append(files, SavedFile{p, s.Screen, s.Width, s.Height})
		}
		return files, nil
	}
	b, w, h, err := capture.Compose(shots)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return nil, err
	}
	screen := shots[0].Screen
	if len(shots) > 1 {
		screen = "both"
	}
	return []SavedFile{{path, screen, w, h}}, nil
}

// --- save states ----------------------------------------------------------

// DefaultSaveWait bounds how long SaveState waits for the state file.
const DefaultSaveWait = 15 * time.Second

// SaveState saves to slot 0..10. Azahar accepts the request at once but
// writes the file about a second later (PROTOCOL.md §5.7), so for launched
// sessions, whose state directory is known, SaveState waits until the file
// is written and complete. For attached sessions it returns once Azahar
// has accepted the request.
func (o *Ops) SaveState(ctx context.Context, name string, slot int) error {
	if slot < 0 || slot >= rpc.SaveStateSlots {
		return usage("slot %d outside 0..%d", slot, rpc.SaveStateSlots-1)
	}
	s, c, err := o.client(ctx, name)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	before, _, _ := emu.StateInfo(s, slot)
	if err := c.SaveState(ctx, slot); err != nil {
		return err
	}
	if s.UserDir() == "" {
		return nil
	}
	wait := o.SaveWait
	if wait <= 0 {
		wait = DefaultSaveWait
	}
	deadline := time.Now().Add(wait)
	var lastSize int64 = -1
	for {
		mod, size, ok := emu.StateInfo(s, slot)
		// Written after the request, and the size stopped changing.
		if ok && mod.After(before) && size > 0 && size == lastSize {
			return nil
		}
		if ok && mod.After(before) {
			lastSize = size
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("azahar accepted the save to slot %d but did not write the state file within %v", slot, wait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// LoadState loads slot 0..10. For launched sessions it first checks that
// the slot has a state: an empty slot leaves Azahar stuck behind a modal
// dialog (PROTOCOL.md §5.7). Afterwards it sends the held input again,
// since the load may have reset Azahar's input override.
func (o *Ops) LoadState(ctx context.Context, name string, slot int) error {
	if slot < 0 || slot >= rpc.SaveStateSlots {
		return usage("slot %d outside 0..%d", slot, rpc.SaveStateSlots-1)
	}
	l := o.locksFor(name)
	l.state.Lock()
	defer l.state.Unlock()
	s, c, err := o.client(ctx, name)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	if exists, known := emu.HasState(s, slot); known && !exists {
		return usage("slot %d has no save state in session %q", slot, name)
	}
	if err := c.LoadState(ctx, slot); err != nil {
		return err
	}
	l.input.Lock()
	defer l.input.Unlock()
	_, err = o.applyHeld(ctx, name, c, s.Held)
	return err
}
