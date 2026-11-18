// SPDX-License-Identifier: GPL-3.0-or-later

package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/michvllni/mcp-azahar/internal/symbols"
)

// ValueOptions control how a target is read.
type ValueOptions struct {
	// ELF overrides the session's ELF (from launch/attach --elf).
	ELF string
	// Type forces a scalar type (u8..u64, s8..s64, f32, f64, bool, string,
	// bytes) instead of the DWARF type. Required for plain addresses
	// (default u32 there).
	Type symbols.Scalar
	// Len is the byte count for string/bytes, or to override a symbol's
	// size when it has no type.
	Len int
}

// Value is a read and decoded target.
type Value struct {
	Target  string `json:"target"`
	Address string `json:"address"` // 0x-prefixed hex
	Size    int    `json:"size"`
	Type    string `json:"type,omitempty"`
	Value   any    `json:"value"`
	Hex     string `json:"hex"`
}

// table loads the ELF for a session (or the override).
func (o *Ops) table(name, override string) (*symbols.Table, error) {
	path := override
	if path == "" {
		s, err := o.M.Store.Load(name)
		if err != nil {
			return nil, err
		}
		path = s.ELFPath
	}
	if path == "" {
		return nil, usage("session %q has no ELF: launch or attach with --elf, or pass --elf", name)
	}
	return symbols.Open(path)
}

// Symbols lists the ELF's symbols containing filter.
func (o *Ops) Symbols(_ context.Context, name, elf, filter string) ([]symbols.Symbol, error) {
	t, err := o.table(name, elf)
	if err != nil {
		return nil, err
	}
	return t.List(filter), nil
}

// target is a resolved read: where, how many bytes, how to decode.
type target struct {
	desc   string
	addr   uint32
	size   int
	typ    string
	decode func([]byte) (any, error)
}

// resolve turns an address ("0x00101000", decimal) or a symbol expression
// ("test_state.last_buttons") into a target.
func (o *Ops) resolve(name, expr string, opts ValueOptions) (*target, error) {
	if opts.Len < 0 {
		return nil, usage("negative length")
	}
	scalarSize := 0
	if opts.Type != "" {
		n, err := opts.Type.Size()
		if err != nil {
			return nil, usage("%v", err)
		}
		scalarSize = n
		if scalarSize > 0 && opts.Len > 0 {
			return nil, usage("--len is only for string and bytes, not %s", opts.Type)
		}
	}
	if isNumberish(expr) {
		addr, err := ParseAddress(expr)
		if err != nil {
			return nil, err
		}
		typ := opts.Type
		switch {
		case typ == "" && opts.Len > 0:
			return nil, usage("--len needs --type string or bytes for an address")
		case typ == "":
			typ, scalarSize = "u32", 4
		case scalarSize == 0 && opts.Len == 0:
			return nil, usage("--type %s needs --len for an address", typ)
		}
		size := scalarSize
		if size == 0 {
			size = opts.Len
		}
		return &target{expr, addr, size, string(typ), func(b []byte) (any, error) { return symbols.DecodeScalar(b, typ) }}, nil
	}
	t, err := o.table(name, opts.ELF)
	if err != nil {
		return nil, err
	}
	loc, err := t.Resolve(expr)
	if err != nil {
		return nil, usage("%v", err)
	}
	tg := &target{expr, loc.Address, loc.Size, loc.TypeName, func(b []byte) (any, error) { return t.DecodeAt(b, loc) }}
	if loc.BitSize > 0 && (opts.Type != "" || opts.Len > 0) {
		return nil, usage("%q is a bitfield: it is decoded by its type, without --type or --len", expr)
	}
	switch {
	case opts.Type != "":
		size := scalarSize
		if size == 0 { // string/bytes: explicit length, else the symbol's size
			size = loc.Size
			if opts.Len > 0 {
				size = opts.Len
			}
		}
		tg.size, tg.typ = size, string(opts.Type)
		tg.decode = func(b []byte) (any, error) { return symbols.DecodeScalar(b, opts.Type) }
	case opts.Len > 0 && (loc.Type == nil || opts.Len != loc.Size):
		tg.size, tg.typ = opts.Len, "bytes"
		tg.decode = func(b []byte) (any, error) { return symbols.DecodeScalar(b, "bytes") }
	}
	if tg.size <= 0 {
		return nil, usage("size of %q unknown: pass --type (and --len for string/bytes)", expr)
	}
	return tg, nil
}

// isNumberish reports whether expr looks like an address, not a symbol.
func isNumberish(expr string) bool {
	e := strings.TrimSpace(expr)
	return e != "" && (e[0] >= '0' && e[0] <= '9')
}

// ReadValue reads and decodes an address or symbol expression.
func (o *Ops) ReadValue(ctx context.Context, name, expr string, opts ValueOptions) (*Value, error) {
	tg, err := o.resolve(name, expr, opts)
	if err != nil {
		return nil, err
	}
	return o.readTarget(ctx, name, tg)
}

func (o *Ops) readTarget(ctx context.Context, name string, tg *target) (*Value, error) {
	b, err := o.ReadMemory(ctx, name, tg.addr, tg.size)
	if err != nil {
		return nil, err
	}
	v, err := tg.decode(b)
	if err != nil {
		return nil, err
	}
	return &Value{Target: tg.desc, Address: fmt.Sprintf("0x%08x", tg.addr), Size: tg.size, Type: tg.typ, Value: v, Hex: fmt.Sprintf("%x", b)}, nil
}

// Condition compares a value with a literal: eq, ne, lt, le, gt, ge, or
// changed (no literal: true once the value differs from the first read).
type Condition struct {
	Op    string `json:"op"`
	Value string `json:"value,omitempty"`
}

// WaitResult reports a finished wait.
type WaitResult struct {
	// Value is the last value read (nil if none could be read in time).
	Value     *Value        `json:"value"`
	Elapsed   time.Duration `json:"-"`
	ElapsedMS int64         `json:"elapsed_ms"`
	Polls     int           `json:"polls"`
}

// DefaultWait is how long WaitFor waits without a deadline in ctx.
const DefaultWait = 10 * time.Second

// WaitFor polls a target every interval (default 50 ms) until cond holds.
// If ctx's deadline passes first (DefaultWait if it has none), it returns
// ErrCheckFailed with the last value; if ctx is canceled, ctx.Err().
func (o *Ops) WaitFor(ctx context.Context, name, expr string, cond Condition, opts ValueOptions, interval time.Duration) (*WaitResult, error) {
	if cond.Op == "" {
		cond.Op = "eq"
	}
	if _, ok := map[string]bool{"eq": true, "ne": true, "lt": true, "le": true, "gt": true, "ge": true, "changed": true}[cond.Op]; !ok {
		return nil, usage("unknown comparison %q (eq, ne, lt, le, gt, ge, changed)", cond.Op)
	}
	if cond.Op == "changed" && cond.Value != "" {
		return nil, usage("changed takes no value")
	}
	if cond.Op != "changed" && cond.Value == "" {
		return nil, usage("%s needs a value to compare with", cond.Op)
	}
	if interval <= 0 {
		interval = 50 * time.Millisecond
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultWait)
		defer cancel()
	}
	tg, err := o.resolve(name, expr, opts)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	res := func(v *Value, polls int) *WaitResult {
		el := time.Since(start)
		return &WaitResult{Value: v, Elapsed: el, ElapsedMS: el.Milliseconds(), Polls: polls}
	}
	var first, last *Value
	polls := 0
	for {
		polls++
		v, err := o.readTarget(ctx, name, tg)
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				break
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		last = v
		if first == nil {
			first = v
		}
		ok, err := Compare(v.Value, first.Value, cond)
		if err != nil {
			return nil, err
		}
		if ok {
			return res(v, polls), nil
		}
		select {
		case <-ctx.Done():
		case <-time.After(interval):
			continue
		}
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ctx.Err()
		}
		break
	}
	r := res(last, polls)
	lastShown := "none read"
	if last != nil {
		lastShown = "last value " + show(last.Value)
	}
	want := cond.Op
	if cond.Value != "" {
		want += " " + cond.Value
	}
	return r, fmt.Errorf("%w: %s %s not reached within %v (%s, %d polls)",
		ErrCheckFailed, expr, want, r.Elapsed.Round(time.Millisecond), lastShown, polls)
}

func show(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// Compare evaluates cond for v (first is the baseline for "changed").
// Numbers compare numerically (literal in decimal, 0x hex or float), bools
// and strings only with eq/ne; enums by name (eq/ne) or by number.
func Compare(v, first any, cond Condition) (bool, error) {
	if cond.Op == "changed" {
		return show(v) != show(first), nil
	}
	lit := strings.TrimSpace(cond.Value)
	switch x := v.(type) {
	case symbols.Enum:
		if x.Name != "" && x.Name == lit {
			return eqOnly(true, cond.Op)
		}
		if _, err := strconv.ParseFloat(lit, 64); err != nil {
			if _, ierr := strconv.ParseInt(lit, 0, 64); ierr != nil {
				return eqOnly(false, cond.Op) // another enumerator's name
			}
		}
		return Compare(x.Value, first, cond)
	case float32:
		// Round the literal to float32 too, so 0.1 equals a stored 0.1f.
		f, err := strconv.ParseFloat(lit, 32)
		if err != nil {
			return Compare(float64(x), first, cond)
		}
		if math.IsNaN(float64(x)) {
			return cond.Op == "ne", nil
		}
		return cmpOrdered(float64(x), float64(float32(f)), cond.Op), nil
	case int64:
		if n, err := strconv.ParseInt(lit, 0, 64); err == nil {
			return cmpOrdered(x, n, cond.Op), nil
		}
		f, err := strconv.ParseFloat(lit, 64)
		if err != nil {
			return false, usage("%q is not a number", lit)
		}
		return cmpOrdered(float64(x), f, cond.Op), nil
	case uint64:
		if n, err := strconv.ParseUint(lit, 0, 64); err == nil {
			return cmpOrdered(x, n, cond.Op), nil
		}
		if n, err := strconv.ParseInt(lit, 0, 64); err == nil && n < 0 {
			return cmpOrdered(1.0, 0.0, cond.Op), nil // any uint > a negative literal
		}
		f, err := strconv.ParseFloat(lit, 64)
		if err != nil {
			return false, usage("%q is not a number", lit)
		}
		return cmpOrdered(float64(x), f, cond.Op), nil
	case float64:
		f, err := strconv.ParseFloat(lit, 64)
		if err != nil {
			if n, ierr := strconv.ParseInt(lit, 0, 64); ierr == nil {
				f = float64(n)
			} else {
				return false, usage("%q is not a number", lit)
			}
		}
		if math.IsNaN(x) {
			return cond.Op == "ne", nil
		}
		return cmpOrdered(x, f, cond.Op), nil
	case bool:
		b, err := strconv.ParseBool(lit)
		if err != nil {
			return false, usage("%q is not true or false", lit)
		}
		return eqOnly(x == b, cond.Op)
	case string:
		return eqOnly(x == lit, cond.Op)
	}
	return false, usage("cannot compare a %T; pick a scalar field or element", v)
}

func eqOnly(equal bool, op string) (bool, error) {
	switch op {
	case "eq":
		return equal, nil
	case "ne":
		return !equal, nil
	}
	return false, usage("%s needs a number; use eq or ne", op)
}

func cmpOrdered[T int64 | uint64 | float64](a, b T, op string) bool {
	switch op {
	case "eq":
		return a == b
	case "ne":
		return a != b
	case "lt":
		return a < b
	case "le":
		return a <= b
	case "gt":
		return a > b
	case "ge":
		return a >= b
	}
	return false
}
