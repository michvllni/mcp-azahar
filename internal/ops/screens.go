// SPDX-License-Identifier: GPL-3.0-or-later

package ops

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/michvllni/mcp-azahar/internal/capture"
	"github.com/michvllni/mcp-azahar/internal/emu"
	"github.com/michvllni/mcp-azahar/internal/rpc"
	"github.com/michvllni/mcp-azahar/internal/session"
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
// <path without extension>-<screen>.png. check, if not nil, vets every
// file name before it is written and returns the path to use.
func SaveScreenshot(shots []capture.Shot, path string, split bool, check func(string) (string, error)) ([]SavedFile, error) {
	write := func(p string, b []byte) (string, error) {
		if check != nil {
			var err error
			if p, err = check(p); err != nil {
				return "", err
			}
		}
		return p, os.WriteFile(p, b, 0o644)
	}
	if split && len(shots) > 1 {
		base := strings.TrimSuffix(path, filepath.Ext(path))
		files := make([]SavedFile, 0, len(shots))
		for _, s := range shots {
			p, err := write(base+"-"+s.Screen+".png", s.PNG)
			if err != nil {
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
	if path, err = write(path, b); err != nil {
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
	before := fingerprint(s, slot, nil)
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
	var last stateMeta
	stable := 0
	for {
		cur := fingerprint(s, slot, &before)
		// Done once the file differs from before the request and its
		// metadata stayed the same for a few polls (still being written
		// otherwise).
		if cur.path != "" && cur.size > 0 && !cur.sameAs(before) {
			if cur.stateMeta == last {
				stable++
			} else {
				stable = 0
			}
			if stable >= 2 {
				return nil
			}
		}
		last = cur.stateMeta
		if time.Now().After(deadline) {
			return fmt.Errorf("azahar accepted the save to slot %d but did not write the state file within %v", slot, wait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// stateMeta identifies a version of a save state file by its metadata.
type stateMeta struct {
	path string
	mod  time.Time
	size int64
}

// stateFingerprint adds the file identity and, to catch rewrites within
// the file system's timestamp resolution, a content hash.
type stateFingerprint struct {
	stateMeta
	info os.FileInfo
	hash [sha256.Size]byte
}

// fingerprint describes the slot's state file. The content is hashed only
// if base is given and the metadata matches it, the only case where the
// hash decides whether the file changed.
func fingerprint(s *session.Session, slot int, base *stateFingerprint) stateFingerprint {
	p, fi := emu.StateFile(s, slot)
	if fi == nil {
		return stateFingerprint{}
	}
	fp := stateFingerprint{stateMeta: stateMeta{p, fi.ModTime(), fi.Size()}, info: fi}
	if base != nil && (base.info == nil || fp.stateMeta != base.stateMeta || !os.SameFile(fi, base.info)) {
		return fp
	}
	if b, err := os.ReadFile(p); err == nil {
		fp.hash = sha256.Sum256(b)
	}
	return fp
}

// sameAs reports whether f is the very file version of before.
func (f stateFingerprint) sameAs(before stateFingerprint) bool {
	return before.info != nil && f.stateMeta == before.stateMeta && os.SameFile(f.info, before.info) && f.hash == before.hash
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
