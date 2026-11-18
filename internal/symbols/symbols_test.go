// SPDX-License-Identifier: GPL-3.0-or-later

package symbols_test

import (
	"debug/elf"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/michvllni/mcp-azahar/internal/symbols"
)

const (
	testELF    = "testdata/symbols.elf"
	noDebugELF = "testdata/symbols-nodebug.elf"
)

// dataAt returns n bytes of the ELF's initialized data at addr.
func dataAt(t *testing.T, addr uint32, n int) []byte {
	t.Helper()
	f, err := elf.Open(testELF)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for _, sec := range f.Sections {
		if sec.Type != elf.SHT_PROGBITS || uint64(addr) < sec.Addr || uint64(addr)+uint64(n) > sec.Addr+sec.Size {
			continue
		}
		b, err := sec.Data()
		if err != nil {
			t.Fatal(err)
		}
		off := uint64(addr) - sec.Addr
		return b[off : off+uint64(n)]
	}
	t.Fatalf("no section holds 0x%x+%d", addr, n)
	return nil
}

func open(t *testing.T, p string) *symbols.Table {
	t.Helper()
	tb, err := symbols.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	return tb
}

func TestList(t *testing.T) {
	tb := open(t, testELF)
	if !tb.HasDWARF() {
		t.Fatal("test ELF should have DWARF")
	}
	all := tb.List("")
	names := []string{}
	for _, s := range all {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "counter,main,negative,table,test_state" {
		t.Fatalf("symbols: %v", names)
	}
	f := tb.List("STATE")
	if len(f) != 1 || f[0].Name != "test_state" || f[0].Address != 0x00101058 || f[0].Size != 40 || f[0].Kind != "object" || f[0].Type != "struct test_state" {
		t.Fatalf("filtered: %+v", f)
	}
}

func TestResolveAndDecode(t *testing.T) {
	tb := open(t, testELF)
	for expr, want := range map[string]string{
		"test_state.magic":     `1413829460`,
		"test_state.touch":     `{"x":160,"y":120}`,
		"test_state.touch.x":   `160`,
		"test_state.circle":    `[-154,12]`,
		"test_state.circle[0]": `-154`,
		"test_state.name":      `"fixture"`,
		"test_state.speed":     `1.5`,
		"test_state.ok":        `true`,
		"test_state.mode":      `"MODE_RUN"`,
		"test_state.ptr":       `"0x00000000"`,
		"table[3]":             `40`,
		"table[0x1]":           `20`,
		"counter":              `3`,
		"negative":             `-42`,
	} {
		loc, err := tb.Resolve(expr)
		if err != nil {
			t.Errorf("%s: %v", expr, err)
			continue
		}
		v, err := tb.Decode(dataAt(t, loc.Address, loc.Size), loc.Type)
		if err != nil {
			t.Errorf("%s: decode: %v", expr, err)
			continue
		}
		b, _ := json.Marshal(v)
		if string(b) != want {
			t.Errorf("%s = %s, want %s", expr, b, want)
		}
	}
	// A whole struct keeps declaration order.
	loc, _ := tb.Resolve("test_state")
	v, _ := tb.Decode(dataAt(t, loc.Address, loc.Size), loc.Type)
	b, _ := json.Marshal(v)
	if !strings.HasPrefix(string(b), `{"magic":1413829460,"frame":7,"last_buttons":0,"touch":`) {
		t.Fatalf("struct order: %s", b)
	}
}

func TestResolveErrors(t *testing.T) {
	tb := open(t, testELF)
	for expr, want := range map[string]string{
		"nope":                  "not found",
		"test_state.nope":       "has no field",
		"test_state.magic.x":    "not a struct",
		"test_state.magic[0]":   "not an array",
		"table[4]":              "out of range",
		"table[1":               "missing ]",
		"table[x]":              "bad index",
		"test_state.touch.x.y.": "not a struct",
	} {
		_, err := tb.Resolve(expr)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", expr, err, want)
		}
	}
	if _, err := tb.Resolve("nope"); !errors.Is(err, symbols.ErrNotFound) {
		t.Errorf("not found: %v", err)
	}
}

func TestWithoutDWARF(t *testing.T) {
	tb := open(t, noDebugELF)
	if tb.HasDWARF() {
		t.Fatal("stripped ELF reports DWARF")
	}
	loc, err := tb.Resolve("test_state")
	if err != nil || loc.Type != nil || loc.Size != 40 {
		t.Fatalf("plain symbol: %+v, %v", loc, err)
	}
	if _, err := tb.Resolve("test_state.frame"); err == nil || !strings.Contains(err.Error(), "DWARF") {
		t.Fatalf("field without DWARF: %v", err)
	}
	v, _ := tb.Decode([]byte{1, 2}, nil)
	if v != "0102" {
		t.Fatalf("untyped decode: %v", v)
	}
}

func TestOpenRejects(t *testing.T) {
	if _, err := symbols.Open("testdata/missing.elf"); err == nil {
		t.Fatal("missing file accepted")
	}
	self, _ := os.Executable() // the 64-bit test binary
	if _, err := symbols.Open(self); err == nil || !strings.Contains(err.Error(), "32-bit") {
		t.Fatalf("64-bit ELF: %v", err)
	}
}

func TestDecodeScalar(t *testing.T) {
	for _, tc := range []struct {
		typ  symbols.Scalar
		in   []byte
		want string
	}{
		{"u8", []byte{0xff}, "255"},
		{"s8", []byte{0xff}, "-1"},
		{"u16", []byte{0x34, 0x12}, "4660"},
		{"s32", []byte{0xd6, 0xff, 0xff, 0xff}, "-42"},
		{"u64", []byte{1, 0, 0, 0, 0, 0, 0, 0}, "1"},
		{"f32", []byte{0, 0, 0xc0, 0x3f}, "1.5"},
		{"bool", []byte{2}, "true"},
		{"string", []byte("hi\x00junk"), `"hi"`},
		{"bytes", []byte{0xde, 0xad}, `"dead"`},
	} {
		v, err := symbols.DecodeScalar(tc.in, tc.typ)
		b, _ := json.Marshal(v)
		if err != nil || string(b) != tc.want {
			t.Errorf("%s % x = %s, %v; want %s", tc.typ, tc.in, b, err, tc.want)
		}
	}
	if _, err := symbols.DecodeScalar([]byte{1}, "u32"); err == nil {
		t.Error("short input accepted")
	}
	if _, err := symbols.Scalar("u128").Size(); err == nil {
		t.Error("unknown type accepted")
	}
}
