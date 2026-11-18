// SPDX-License-Identifier: GPL-3.0-or-later

// Package session persists emulator sessions as JSON files, so that every
// CLI call (a separate process) and the MCP server find the same Azahar.
// Files live in <state dir>/sessions/<name>.json; a host-wide lock file
// serializes changes (DECISIONS.md D9, PLAN.md Phase 3).
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DefaultName is the session used when none is given.
const DefaultName = "default"

// ErrNotFound means no session file exists for the name.
var ErrNotFound = errors.New("session not found")

// Kind tells how a session came to be.
type Kind string

// Session kinds.
const (
	// KindLaunched: mcp-azahar started Azahar (and maybe Xvfb) and owns them.
	KindLaunched Kind = "launched"
	// KindAttached: an Azahar started by someone else; only RPC is used.
	KindAttached Kind = "attached"
)

// Endpoint is the RPC server address in net.Dial terms, e.g.
// {"udp", "127.0.0.1:45987"} (DECISIONS.md D9).
type Endpoint struct {
	Network string `json:"network"`
	Address string `json:"address"`
}

// Proc identifies a process. Start is the process start time as reported
// by the OS (Linux: clock ticks since boot), used to detect PID reuse; 0
// if unknown.
type Proc struct {
	PID   int    `json:"pid"`
	Start uint64 `json:"start,omitempty"`
}

// Session is the persisted state of one emulator session.
type Session struct {
	Name      string    `json:"name"`
	Kind      Kind      `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
	RPC       Endpoint  `json:"rpc"`
	// Azahar is the emulator process (launched sessions only).
	Azahar *Proc `json:"azahar,omitempty"`
	// Xvfb is set only if mcp-azahar started the X server for this session.
	Xvfb *Proc `json:"xvfb,omitempty"`
	// Display is the X display Azahar runs on, if known.
	Display string `json:"display,omitempty"`
	// Dir is the session's working directory; Azahar runs in portable mode
	// with its user directory at Dir/user (PROTOCOL.md §7).
	Dir string `json:"dir,omitempty"`
	// LogPath is Azahar's log file; OutputPath its captured stdout/stderr.
	LogPath    string `json:"log_path,omitempty"`
	OutputPath string `json:"output_path,omitempty"`
	AppPath    string `json:"app_path,omitempty"`
	ELFPath    string `json:"elf_path,omitempty"`
}

// UserDir returns Azahar's user directory, or "" if unknown (attached).
func (s *Session) UserDir() string {
	if s.Dir == "" {
		return ""
	}
	return filepath.Join(s.Dir, "user")
}

// StateFile returns the save state file for a title and slot, or "" if the
// user directory is unknown. Format from PROTOCOL.md §5.7.
func (s *Session) StateFile(titleID uint64, slot int) string {
	if s.UserDir() == "" {
		return ""
	}
	return filepath.Join(s.UserDir(), "states", fmt.Sprintf("%016X.%02d.cst", titleID, slot))
}

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidateName checks a session name: letters, digits, '.', '_', '-',
// starting with a letter or digit, at most 64 characters.
func ValidateName(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid session name %q: use 1-64 letters, digits, '.', '_' or '-', starting with a letter or digit", name)
	}
	return nil
}

// Store is a directory of session files.
type Store struct{ dir string }

// DefaultDir returns the state directory: $MCP_AZAHAR_STATE_DIR, else
// $XDG_STATE_HOME/mcp-azahar, else ~/.local/state/mcp-azahar.
func DefaultDir() (string, error) {
	if d := os.Getenv("MCP_AZAHAR_STATE_DIR"); d != "" {
		return d, nil
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "mcp-azahar"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find state directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "mcp-azahar"), nil
}

// Open opens (and creates) a store in dir.
func Open(dir string) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(abs, "sessions"), 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	return &Store{dir: abs}, nil
}

// Dir returns the store's state directory.
func (st *Store) Dir() string { return st.dir }

func (st *Store) file(name string) string {
	return filepath.Join(st.dir, "sessions", name+".json")
}

// SessionDir returns the working directory reserved for a session.
func (st *Store) SessionDir(name string) string {
	return filepath.Join(st.dir, "sessions", name+".d")
}

// Lock takes the store's host-wide lock, waiting until ctx ends. Hold it
// across every read-modify-write of session files and across launch
// (DECISIONS.md D9). The returned function releases it.
func (st *Store) Lock(ctx context.Context) (func(), error) {
	f, err := os.OpenFile(filepath.Join(st.dir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	for {
		ok, err := tryLock(f)
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w", f.Name(), err)
		}
		if ok {
			return func() { _ = unlock(f); _ = f.Close() }, nil
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("waiting for the session lock %s: %w", f.Name(), ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Load reads a session file.
func (st *Store) Load(name string) (*Session, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(st.file(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("session %q: %w", name, err)
	}
	return &s, nil
}

// Save writes a session file atomically.
func (st *Store) Save(s *Session) error {
	if err := ValidateName(s.Name); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Join(st.dir, "sessions"), "."+s.Name+".*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(append(b, '\n'))
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), st.file(s.Name))
}

// Delete removes a session file; a missing file is not an error.
func (st *Store) Delete(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	err := os.Remove(st.file(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// List returns all sessions, sorted by name. Unreadable files are skipped.
func (st *Store) List() ([]*Session, error) {
	entries, err := os.ReadDir(filepath.Join(st.dir, "sessions"))
	if err != nil {
		return nil, err
	}
	var out []*Session
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() || ValidateName(name) != nil {
			continue
		}
		s, err := st.Load(name)
		if err != nil {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Running reports whether a launched session's Azahar still runs. Attached
// sessions have no process to check and always report true.
func (s *Session) Running() bool {
	if s.Kind != KindLaunched {
		return true
	}
	return s.Azahar != nil && Alive(*s.Azahar)
}
