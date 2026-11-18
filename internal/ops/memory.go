// SPDX-License-Identifier: GPL-3.0-or-later

package ops

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/michvllni/mcp-azahar/internal/rpc"
)

// MaxMemoryRead bounds a single read or write (16 MiB).
const MaxMemoryRead = 16 << 20

// ParseAddress parses a 32-bit address: hex with 0x/0X prefix, or decimal
// (never octal). Underscores are ignored.
func ParseAddress(s string) (uint32, error) {
	v, err := parseNumber(s, 32)
	if err != nil {
		return 0, usage("address %q: use hex like 0x00100000 or decimal", s)
	}
	return uint32(v), nil
}

// ParseLength parses a byte count like ParseAddress.
func ParseLength(s string) (int, error) {
	v, err := parseNumber(s, 32)
	if err != nil {
		return 0, usage("length %q: use decimal or hex like 0x100", s)
	}
	return int(v), nil
}

func parseNumber(s string, bits int) (uint64, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), "_", "")
	if h, ok := strings.CutPrefix(strings.ToLower(s), "0x"); ok {
		return strconv.ParseUint(h, 16, bits)
	}
	return strconv.ParseUint(s, 10, bits)
}

// ParseBytes parses hex bytes in memory order, e.g. "de ad be ef",
// "deadbeef" or "0xde 0xad". Every whitespace-separated token must have an
// even number of hex digits, so "12 3 4" is rejected instead of being read
// as 0x12 0x34. A token like 0x1234 is the bytes 12 34, not a
// little-endian number.
func ParseBytes(args ...string) ([]byte, error) {
	var out []byte
	for _, tok := range strings.Fields(strings.Join(args, " ")) {
		t := strings.ReplaceAll(tok, ":", "")
		if h, ok := strings.CutPrefix(strings.ToLower(t), "0x"); ok {
			t = h
		}
		if len(t)%2 != 0 {
			return nil, usage("byte group %q has an odd number of hex digits", tok)
		}
		b, err := hex.DecodeString(t)
		if err != nil {
			return nil, usage("byte group %q is not hex", tok)
		}
		out = append(out, b...)
	}
	if len(out) == 0 {
		return nil, usage("no bytes given")
	}
	return out, nil
}

// HexDump formats b like `hexdump -C`, with offsets starting at base.
func HexDump(b []byte, base uint32) string {
	var sb strings.Builder
	for off := 0; off < len(b); off += 16 {
		line := b[off:min(off+16, len(b))]
		fmt.Fprintf(&sb, "%08x  ", base+uint32(off))
		for i := 0; i < 16; i++ {
			switch {
			case i < len(line):
				fmt.Fprintf(&sb, "%02x ", line[i])
			default:
				sb.WriteString("   ")
			}
			if i == 7 {
				sb.WriteByte(' ')
			}
		}
		sb.WriteString(" |")
		for _, c := range line {
			if c < 32 || c > 126 {
				c = '.'
			}
			sb.WriteByte(c)
		}
		sb.WriteString("|\n")
	}
	return sb.String()
}

// selectApp makes sure memory requests target the app: if no valid
// process is selected (fresh server, after a load, or a selection left
// behind by a failed load or another client), it selects the newest
// process, which is the app Azahar booted (PROTOCOL.md §5.3, §5.4).
func selectApp(ctx context.Context, c *rpc.Client) error {
	pid, err := c.Process(ctx)
	if err != nil {
		return err
	}
	ps, err := c.Processes(ctx)
	if err != nil {
		return err
	}
	if len(ps) == 0 {
		return errors.New("no emulated process to access")
	}
	app := ps[0]
	for _, p := range ps {
		if p.PID == pid {
			return nil
		}
		if p.PID > app.PID {
			app = p
		}
	}
	return c.SelectProcess(ctx, app.PID)
}

// ReadMemory reads n bytes at addr from the app's address space.
func (o *Ops) ReadMemory(ctx context.Context, name string, addr uint32, n int) ([]byte, error) {
	if n <= 0 || n > MaxMemoryRead {
		return nil, usage("length %d outside 1..%d", n, MaxMemoryRead)
	}
	l := o.locksFor(name)
	l.state.RLock()
	defer l.state.RUnlock()
	_, c, err := o.client(ctx, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	if err := selectApp(ctx, c); err != nil {
		return nil, err
	}
	return c.ReadMemory(ctx, addr, n)
}

// WriteMemory writes data at addr and reads it back (Azahar silently
// ignores writes outside its writable regions, PROTOCOL.md §5.2; a write
// of bytes equal to the current content cannot be told apart).
func (o *Ops) WriteMemory(ctx context.Context, name string, addr uint32, data []byte) error {
	if len(data) == 0 || len(data) > MaxMemoryRead {
		return usage("data length %d outside 1..%d", len(data), MaxMemoryRead)
	}
	l := o.locksFor(name)
	l.state.RLock()
	defer l.state.RUnlock()
	_, c, err := o.client(ctx, name)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	if err := selectApp(ctx, c); err != nil {
		return err
	}
	return c.WriteMemoryVerified(ctx, addr, data)
}
