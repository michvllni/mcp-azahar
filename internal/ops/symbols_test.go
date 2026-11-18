// SPDX-License-Identifier: GPL-3.0-or-later

package ops_test

import (
	"context"
	"debug/elf"
	"encoding/binary"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michvllni/mcp-azahar/internal/ops"
	"github.com/michvllni/mcp-azahar/internal/rpc/fakeserver"
	"github.com/michvllni/mcp-azahar/internal/session"
)

var testELF, _ = filepath.Abs("../symbols/testdata/symbols.elf")

// loadELF copies the test ELF's sections into the fake's memory and makes
// it the session's ELF.
func loadELF(t *testing.T, o *ops.Ops, srv *fakeserver.Server) {
	t.Helper()
	f, err := elf.Open(testELF)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for _, s := range f.Sections {
		if s.Type == elf.SHT_PROGBITS && s.Addr != 0 {
			b, _ := s.Data()
			srv.SetMemory(uint32(s.Addr), b)
		}
	}
	if _, err := o.M.Update(context.Background(), "t", func(s *session.Session) error { s.ELFPath = testELF; return nil }); err != nil {
		t.Fatal(err)
	}
}

func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestReadValue(t *testing.T) {
	o, srv := setup(t)
	loadELF(t, o, srv)
	ctx := context.Background()
	for expr, want := range map[string]string{
		"test_state.touch":   `{"x":160,"y":120}`,
		"test_state.name":    `"fixture"`,
		"test_state.mode":    `"MODE_RUN"`,
		"table[2]":           `30`,
		"0x00101080":         `3`, // counter, read with --type u8
		"test_state.frame":   `7`,
		"test_state.circle":  `[-154,12]`,
		"test_state.speed":   `1.5`,
		"test_state.ok":      `true`,
		"test_state.touch.y": `120`,
	} {
		opts := ops.ValueOptions{}
		if strings.HasPrefix(expr, "0x") {
			opts.Type = "u8"
		}
		v, err := o.ReadValue(ctx, "t", expr, opts)
		if err != nil {
			t.Errorf("%s: %v", expr, err)
			continue
		}
		if got := asJSON(v.Value); got != want {
			t.Errorf("%s = %s, want %s", expr, got, want)
		}
	}
	v, err := o.ReadValue(ctx, "t", "test_state.frame", ops.ValueOptions{Type: "u16"})
	if err != nil || asJSON(v.Value) != "7" || v.Size != 2 || v.Type != "u16" || v.Address != "0x0010105c" {
		t.Fatalf("type override: %+v, %v", v, err)
	}
	v, err = o.ReadValue(ctx, "t", "test_state.name", ops.ValueOptions{Type: "bytes", Len: 3})
	if err != nil || asJSON(v.Value) != `"666978"` {
		t.Fatalf("bytes: %+v, %v", v, err)
	}
	for _, bad := range []struct {
		expr string
		opts ops.ValueOptions
	}{
		{"nope", ops.ValueOptions{}},
		{"test_state.nope", ops.ValueOptions{}},
		{"0x00101000", ops.ValueOptions{Type: "string"}}, // string needs --len
		{"0x00101000", ops.ValueOptions{Type: "float9"}}, // unknown type
		{"test_state", ops.ValueOptions{Len: -1}},
	} {
		if _, err := o.ReadValue(ctx, "t", bad.expr, bad.opts); !errors.Is(err, ops.ErrUsage) {
			t.Errorf("%s %+v: %v, want ErrUsage", bad.expr, bad.opts, err)
		}
	}
}

func TestSymbolsNeedELF(t *testing.T) {
	o, _ := setup(t)
	if _, err := o.Symbols(context.Background(), "t", "", ""); !errors.Is(err, ops.ErrUsage) || !strings.Contains(err.Error(), "--elf") {
		t.Fatalf("no ELF: %v", err)
	}
	list, err := o.Symbols(context.Background(), "t", testELF, "tab")
	if err != nil || len(list) != 1 || list[0].Name != "table" {
		t.Fatalf("with --elf: %+v, %v", list, err)
	}
}

func TestWaitFor(t *testing.T) {
	o, srv := setup(t)
	loadELF(t, o, srv)
	frameAddr := uint32(0x0010105c)
	ctx := context.Background()

	// The value changes after 150 ms: ge 60 is reached.
	go func() {
		time.Sleep(150 * time.Millisecond)
		srv.SetMemory(frameAddr, binary.LittleEndian.AppendUint32(nil, 61))
	}()
	wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	r, err := o.WaitFor(wctx, "t", "test_state.frame", ops.Condition{Op: "ge", Value: "60"}, ops.ValueOptions{}, 20*time.Millisecond)
	cancel()
	if err != nil || asJSON(r.Value.Value) != "61" || r.Polls < 2 {
		t.Fatalf("ge 60: %+v, %v", r, err)
	}

	// Never reached: a check failure with the last value.
	wctx, cancel = context.WithTimeout(ctx, 200*time.Millisecond)
	r, err = o.WaitFor(wctx, "t", "test_state.mode", ops.Condition{Value: "MODE_IDLE"}, ops.ValueOptions{}, 20*time.Millisecond)
	cancel()
	if !errors.Is(err, ops.ErrCheckFailed) || r == nil || asJSON(r.Value.Value) != `"MODE_RUN"` || !strings.Contains(err.Error(), "MODE_RUN") {
		t.Fatalf("timeout: %+v, %v", r, err)
	}

	// changed
	go func() {
		time.Sleep(100 * time.Millisecond)
		srv.SetMemory(0x00101080, []byte{9})
	}()
	wctx, cancel = context.WithTimeout(ctx, 3*time.Second)
	r, err = o.WaitFor(wctx, "t", "counter", ops.Condition{Op: "changed"}, ops.ValueOptions{}, 20*time.Millisecond)
	cancel()
	if err != nil || asJSON(r.Value.Value) != "9" {
		t.Fatalf("changed: %+v, %v", r, err)
	}

	for _, bad := range []ops.Condition{{Op: "eq"}, {Op: "about", Value: "1"}} {
		if _, err := o.WaitFor(ctx, "t", "counter", bad, ops.ValueOptions{}, 0); !errors.Is(err, ops.ErrUsage) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestCompare(t *testing.T) {
	for _, tc := range []struct {
		v    any
		op   string
		lit  string
		want bool
	}{
		{int64(-5), "lt", "0", true},
		{int64(-5), "eq", "-0x5", true},
		{uint64(255), "eq", "0xff", true},
		{uint64(3), "gt", "-1", true},
		{uint64(3), "le", "2.5", false},
		{1.5, "ge", "1.5", true},
		{1.5, "eq", "1", false},
		{true, "eq", "true", true},
		{false, "ne", "1", true},
		{"MODE_RUN", "eq", "MODE_RUN", true},
		{"a", "ne", "b", true},
	} {
		got, err := ops.Compare(tc.v, tc.v, ops.Condition{Op: tc.op, Value: tc.lit})
		if err != nil || got != tc.want {
			t.Errorf("%v %s %s = %v, %v; want %v", tc.v, tc.op, tc.lit, got, err, tc.want)
		}
	}
	for _, bad := range []struct {
		v  any
		op string
		l  string
	}{
		{"x", "lt", "y"}, {true, "gt", "true"}, {int64(1), "eq", "one"}, {[]any{1}, "eq", "1"},
	} {
		if _, err := ops.Compare(bad.v, bad.v, ops.Condition{Op: bad.op, Value: bad.l}); !errors.Is(err, ops.ErrUsage) {
			t.Errorf("%v %s %s accepted", bad.v, bad.op, bad.l)
		}
	}
}
