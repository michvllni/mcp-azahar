// SPDX-License-Identifier: GPL-3.0-or-later

package symbols_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/michvllni/mcp-azahar/internal/symbols"
)

// The realworld fixtures are built from testdata/realworld/ (C and C++
// units with bitfields, a flexible array, file statics sharing names with
// globals, a static local and namespace/class statics) with DWARF 5 and
// DWARF 2, which encode bitfields differently.
var realworldELFs = []string{"testdata/realworld.elf", "testdata/realworld-dwarf2.elf"}

func TestRealWorldResolve(t *testing.T) {
	for _, p := range realworldELFs {
		tb := open(t, p)
		for expr, want := range map[string]string{
			"bits":             `{"a":5,"b":17,"c":300,"s":-3}`,
			"bits.a":           `5`,
			"bits.b":           `17`,
			"bits.c":           `300`,
			"bits.s":           `-3`,
			"flexible.n":       `3`,
			"flexible.data[0]": `11`,
			"flexible.data[2]": `33`,
			"shared":           `{"lo":4660,"hi":22136}`,
			"x":                `{"lo":1,"hi":2}`, // the global wins over a.c's static
			"x.hi":             `2`,
			"a.c:x":            `1`,
			"b.c:x":            `{"lo":1,"hi":2}`,
			"hijack":           `7`, // b.c's global, not fa()'s local array
			"fa::st_local":     `77`,
			"ns::counter":      `42`,
			"_ZN2ns7counterE":  `42`,
			"K::s":             `9`,
			"shared_static":    `1`,
		} {
			loc, err := tb.Resolve(expr)
			if err != nil {
				t.Errorf("%s: %s: %v", p, expr, err)
				continue
			}
			v, err := tb.DecodeAt(dataIn(t, p, loc.Address, loc.Size), loc)
			if err != nil {
				t.Errorf("%s: %s: decode: %v", p, expr, err)
				continue
			}
			if b, _ := json.Marshal(v); string(b) != want {
				t.Errorf("%s: %s = %s, want %s", p, expr, b, want)
			}
		}
		// A pointer decodes as its address.
		loc, err := tb.Resolve("greeting")
		if err != nil {
			t.Fatal(err)
		}
		if v, _ := tb.DecodeAt(dataIn(t, p, loc.Address, loc.Size), loc); v == nil || v.(uint64) == 0 {
			t.Errorf("%s: greeting = %v, want a non-zero address", p, v)
		}
		for expr, msg := range map[string]string{
			"c.cpp:x":       "not found",
			"fa::hijack":    "not found", // stack local: not addressable
			"bits.a.x":      "bitfield",
			"flexible.n[0]": "not an array",
		} {
			if _, err := tb.Resolve(expr); err == nil || !strings.Contains(err.Error(), msg) {
				t.Errorf("%s: %s: %v, want %q", p, expr, err, msg)
			}
		}
	}
}

func TestRealWorldList(t *testing.T) {
	for _, p := range realworldELFs {
		got := map[string]symbols.Symbol{}
		for _, s := range open(t, p).List("") {
			got[s.Name] = s
		}
		for _, n := range []string{"a.c:x", "x", "ns::counter", "K::s", "fa::st_local", "bits", "fa"} {
			if _, ok := got[n]; !ok {
				t.Errorf("%s: List lacks %s: %v", p, n, got)
			}
		}
		if got["x"].Type != "struct pair" || got["a.c:x"].Type != "int" {
			t.Errorf("%s: x types: %+v %+v", p, got["x"], got["a.c:x"])
		}
		if got["fa"].Kind != "func" {
			t.Errorf("%s: fa: %+v", p, got["fa"])
		}
	}
}

func TestDecodeSizeCap(t *testing.T) {
	tb := open(t, testELF)
	loc, err := tb.Resolve("test_state")
	if err != nil {
		t.Fatal(err)
	}
	loc.Size = symbols.MaxDecodeSize + 1
	if _, err := tb.DecodeAt(make([]byte, loc.Size), loc); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("huge value decoded: %v", err)
	}
}
