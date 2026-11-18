// SPDX-License-Identifier: GPL-3.0-or-later

// Package symbols reads a homebrew's ELF to find variables by name instead
// of hard-coded addresses (DECISIONS.md D3, D5). With DWARF debug info it
// also resolves struct fields and array elements ("test_state.touch.x",
// "table[2]") and decodes values by their C types.
package symbols

import (
	"bytes"
	"debug/dwarf"
	"debug/elf"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// ErrNotFound means the name is not in the ELF.
var ErrNotFound = errors.New("symbol not found")

// Symbol is an ELF symbol.
type Symbol struct {
	Name    string `json:"name"`
	Address uint32 `json:"address"`
	Size    uint32 `json:"size"`
	Kind    string `json:"kind"` // "object" or "func"
	// Type is the C type from DWARF, if known.
	Type string `json:"type,omitempty"`
}

// Table is a loaded ELF.
type Table struct {
	order binary.ByteOrder
	syms  map[string]Symbol
	dw    *dwarf.Data             // nil without debug info
	vars  map[string]dwarf.Offset // variable name -> type offset
}

// Open loads symbols (and DWARF, if present) from an ELF file.
func Open(path string) (*Table, error) {
	f, err := elf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open ELF %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if f.Class != elf.ELFCLASS32 {
		return nil, fmt.Errorf("%s is not a 32-bit ELF (3DS homebrew is 32-bit ARM)", path)
	}
	t := &Table{order: f.ByteOrder, syms: map[string]Symbol{}, vars: map[string]dwarf.Offset{}}
	syms, err := f.Symbols()
	if err != nil && !errors.Is(err, elf.ErrNoSymbols) {
		return nil, fmt.Errorf("read symbols of %s: %w", path, err)
	}
	for _, s := range syms {
		var kind string
		switch elf.ST_TYPE(s.Info) {
		case elf.STT_OBJECT:
			kind = "object"
		case elf.STT_FUNC:
			kind = "func"
		default:
			continue
		}
		if s.Name == "" || s.Section == elf.SHN_UNDEF {
			continue
		}
		if _, dup := t.syms[s.Name]; dup && elf.ST_BIND(s.Info) == elf.STB_LOCAL {
			continue // prefer the global of two same-named symbols
		}
		t.syms[s.Name] = Symbol{Name: s.Name, Address: uint32(s.Value), Size: uint32(s.Size), Kind: kind}
	}
	if dw, err := f.DWARF(); err == nil {
		t.dw = dw
		t.indexVariables()
	}
	return t, nil
}

// HasDWARF reports whether type information is available.
func (t *Table) HasDWARF() bool { return t.dw != nil }

// indexVariables maps variable names to their DWARF types (definitions
// with a location only, not extern declarations).
func (t *Table) indexVariables() {
	r := t.dw.Reader()
	for {
		e, err := r.Next()
		if err != nil || e == nil {
			return
		}
		if e.Tag != dwarf.TagVariable {
			continue
		}
		name, _ := e.Val(dwarf.AttrName).(string)
		typ, ok := e.Val(dwarf.AttrType).(dwarf.Offset)
		if name == "" || !ok || e.Val(dwarf.AttrLocation) == nil {
			continue
		}
		if _, dup := t.vars[name]; !dup {
			t.vars[name] = typ
		}
	}
}

// List returns object and function symbols whose name contains filter
// (case-insensitive), sorted by name.
func (t *Table) List(filter string) []Symbol {
	f := strings.ToLower(filter)
	out := []Symbol{}
	for _, s := range t.syms {
		if f != "" && !strings.Contains(strings.ToLower(s.Name), f) {
			continue
		}
		if off, ok := t.vars[s.Name]; ok {
			if typ, err := t.dw.Type(off); err == nil {
				s.Type = typ.String()
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Location is a resolved expression: where to read and how to decode.
type Location struct {
	Expr    string     `json:"expr"`
	Address uint32     `json:"address"`
	Size    int        `json:"size"`
	Type    dwarf.Type `json:"-"` // nil without DWARF
	// TypeName is Type.String(), or "" without DWARF.
	TypeName string `json:"type,omitempty"`
}

// Resolve resolves an expression: a symbol name followed by .field and
// [index] parts, e.g. "test_state.touch.x" or "table[2]". Fields and
// indices need DWARF.
func (t *Table) Resolve(expr string) (Location, error) {
	name, rest := splitBase(expr)
	sym, ok := t.syms[name]
	if !ok {
		return Location{}, fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	loc := Location{Expr: expr, Address: sym.Address, Size: int(sym.Size)}
	off, typed := t.vars[name]
	if !typed {
		if rest != "" {
			return Location{}, fmt.Errorf("%q: fields and indices need DWARF type info for %q (build with -g)", expr, name)
		}
		return loc, nil
	}
	typ, err := t.dw.Type(off)
	if err != nil {
		return Location{}, fmt.Errorf("type of %q: %w", name, err)
	}
	for rest != "" {
		typ = strip(typ)
		switch {
		case strings.HasPrefix(rest, "."):
			var field string
			field, rest = splitField(rest[1:])
			st, ok := typ.(*dwarf.StructType)
			if !ok {
				return Location{}, fmt.Errorf("%q: %s is not a struct", expr, typ)
			}
			f := findField(st, field)
			if f == nil {
				return Location{}, fmt.Errorf("%q: %s has no field %q (fields: %s)", expr, typ, field, fieldNames(st))
			}
			loc.Address += uint32(f.ByteOffset)
			typ = f.Type
		case strings.HasPrefix(rest, "["):
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				return Location{}, fmt.Errorf("%q: missing ]", expr)
			}
			idx, err := strconv.ParseUint(strings.TrimSpace(rest[1:end]), 0, 32)
			if err != nil {
				return Location{}, fmt.Errorf("%q: bad index %q", expr, rest[1:end])
			}
			rest = rest[end+1:]
			at, ok := typ.(*dwarf.ArrayType)
			if !ok {
				return Location{}, fmt.Errorf("%q: %s is not an array", expr, typ)
			}
			if at.Count >= 0 && int64(idx) >= at.Count {
				return Location{}, fmt.Errorf("%q: index %d out of range (length %d)", expr, idx, at.Count)
			}
			loc.Address += uint32(idx) * uint32(at.Type.Size())
			typ = at.Type
		default:
			return Location{}, fmt.Errorf("%q: unexpected %q", expr, rest)
		}
	}
	loc.Type, loc.TypeName, loc.Size = typ, typ.String(), int(typ.Size())
	return loc, nil
}

func splitBase(expr string) (string, string) {
	i := strings.IndexAny(expr, ".[")
	if i < 0 {
		return expr, ""
	}
	return expr[:i], expr[i:]
}

func splitField(s string) (string, string) {
	i := strings.IndexAny(s, ".[")
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i:]
}

// strip removes typedefs and qualifiers.
func strip(t dwarf.Type) dwarf.Type {
	for {
		switch v := t.(type) {
		case *dwarf.TypedefType:
			t = v.Type
		case *dwarf.QualType:
			t = v.Type
		default:
			return t
		}
	}
}

func findField(st *dwarf.StructType, name string) *dwarf.StructField {
	for _, f := range st.Field {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func fieldNames(st *dwarf.StructType) string {
	n := make([]string, 0, len(st.Field))
	for _, f := range st.Field {
		n = append(n, f.Name)
	}
	return strings.Join(n, ", ")
}

// --- decoding -------------------------------------------------------------

// Field is a decoded struct member.
type Field struct {
	Name  string
	Value any
}

// Struct is a decoded struct; it marshals as a JSON object in declaration
// order.
type Struct []Field

// MarshalJSON keeps the declaration order.
func (s Struct) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, f := range s {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(f.Name)
		v, err := json.Marshal(f.Value)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// Decode converts memory to Go values by DWARF type: integers (int64 or
// uint64), float64, bool, char arrays as strings (up to the first NUL),
// other arrays as slices, structs as Struct, pointers as "0x…" strings,
// enums as their enumerator name if one matches, else the number. With a
// nil type it returns the bytes as a hex string.
func (t *Table) Decode(b []byte, typ dwarf.Type) (any, error) {
	if typ == nil {
		return fmt.Sprintf("%x", b), nil
	}
	typ = strip(typ)
	if sz := typ.Size(); sz > 0 && int64(len(b)) < sz {
		return nil, fmt.Errorf("%d bytes for %s of size %d", len(b), typ, sz)
	}
	switch v := typ.(type) {
	case *dwarf.BoolType:
		return b[0] != 0, nil
	case *dwarf.CharType, *dwarf.IntType:
		return decodeInt(b, int(typ.Size()), t.order, true), nil
	case *dwarf.UcharType, *dwarf.UintType:
		return decodeInt(b, int(typ.Size()), t.order, false), nil
	case *dwarf.FloatType:
		switch v.Size() {
		case 4:
			return float64(math.Float32frombits(t.order.Uint32(b))), nil
		case 8:
			return math.Float64frombits(t.order.Uint64(b)), nil
		}
	case *dwarf.EnumType:
		n := decodeInt(b, int(v.Size()), t.order, true).(int64)
		for _, e := range v.Val {
			if e.Val == n {
				return e.Name, nil
			}
		}
		return n, nil
	case *dwarf.PtrType:
		return fmt.Sprintf("0x%08x", t.order.Uint32(b)), nil
	case *dwarf.ArrayType:
		el := strip(v.Type)
		if _, isChar := el.(*dwarf.CharType); isChar || isUchar(el) {
			n := int(v.Count)
			if n < 0 || n > len(b) {
				n = len(b)
			}
			s := b[:n]
			if i := bytes.IndexByte(s, 0); i >= 0 {
				s = s[:i]
			}
			return string(s), nil
		}
		es := int(el.Size())
		if v.Count < 0 || es <= 0 {
			return fmt.Sprintf("%x", b), nil
		}
		out := make([]any, 0, v.Count)
		for i := 0; i < int(v.Count); i++ {
			val, err := t.Decode(b[i*es:(i+1)*es], el)
			if err != nil {
				return nil, err
			}
			out = append(out, val)
		}
		return out, nil
	case *dwarf.StructType:
		out := make(Struct, 0, len(v.Field))
		for _, f := range v.Field {
			fs := int(f.Type.Size())
			if f.ByteOffset < 0 || int(f.ByteOffset)+fs > len(b) || fs <= 0 {
				continue
			}
			val, err := t.Decode(b[f.ByteOffset:int(f.ByteOffset)+fs], f.Type)
			if err != nil {
				return nil, err
			}
			out = append(out, Field{f.Name, val})
		}
		return out, nil
	}
	return fmt.Sprintf("%x", b), nil
}

func isUchar(t dwarf.Type) bool {
	_, ok := t.(*dwarf.UcharType)
	return ok
}

func decodeInt(b []byte, size int, order binary.ByteOrder, signed bool) any {
	var u uint64
	switch size {
	case 1:
		u = uint64(b[0])
	case 2:
		u = uint64(order.Uint16(b))
	case 4:
		u = uint64(order.Uint32(b))
	case 8:
		u = order.Uint64(b)
	}
	if !signed {
		return u
	}
	shift := 64 - 8*size
	return int64(u<<shift) >> shift
}

// --- explicit types -------------------------------------------------------

// Scalar is a type chosen by name instead of DWARF: u8, u16, u32, u64, s8,
// s16, s32, s64, f32, f64, bool, string (NUL-terminated), bytes (hex).
type Scalar string

// Size returns the scalar's size, or 0 for string/bytes (length given
// separately).
func (s Scalar) Size() (int, error) {
	switch s {
	case "u8", "s8", "bool":
		return 1, nil
	case "u16", "s16":
		return 2, nil
	case "u32", "s32", "f32":
		return 4, nil
	case "u64", "s64", "f64":
		return 8, nil
	case "string", "bytes":
		return 0, nil
	}
	return 0, fmt.Errorf("unknown type %q (use u8..u64, s8..s64, f32, f64, bool, string, bytes)", string(s))
}

// DecodeScalar decodes b as s, little-endian (3DS homebrew is ARM LE).
func DecodeScalar(b []byte, s Scalar) (any, error) {
	n, err := s.Size()
	if err != nil {
		return nil, err
	}
	if len(b) < n {
		return nil, fmt.Errorf("%d bytes for %s", len(b), s)
	}
	le := binary.LittleEndian
	switch s {
	case "u8", "u16", "u32", "u64":
		return decodeInt(b, n, le, false), nil
	case "s8", "s16", "s32", "s64":
		return decodeInt(b, n, le, true), nil
	case "f32":
		return float64(math.Float32frombits(le.Uint32(b))), nil
	case "f64":
		return math.Float64frombits(le.Uint64(b)), nil
	case "bool":
		return b[0] != 0, nil
	case "string":
		if i := bytes.IndexByte(b, 0); i >= 0 {
			b = b[:i]
		}
		return string(b), nil
	}
	return fmt.Sprintf("%x", b), nil
}
