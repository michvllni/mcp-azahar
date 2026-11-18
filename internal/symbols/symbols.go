// SPDX-License-Identifier: GPL-3.0-or-later

// Package symbols reads a homebrew's ELF to find variables by name instead
// of hard-coded addresses (DECISIONS.md D3, D5). With DWARF debug info it
// also resolves struct fields, bitfields and array elements
// ("test_state.touch.x", "table[2]") and decodes values by their C types.
//
// Names: file-scope variables by name; C++ namespace and class members as
// "ns::counter", "K::s" (or their mangled names); static locals as
// "func::var"; a static that shares its name with another variable as
// "file.c:name". Function locals on the stack are not addressable.
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
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ErrNotFound means the name is not in the ELF.
var ErrNotFound = errors.New("symbol not found")

// MaxDecodeSize bounds values decoded as a whole (64 KiB): bigger ones
// produce unusable output; read parts or use raw memory reads instead.
const MaxDecodeSize = 64 << 10

// Symbol is a variable or function.
type Symbol struct {
	Name    string `json:"name"`
	Address uint32 `json:"address"`
	Size    uint32 `json:"size"`
	Kind    string `json:"kind"` // "object" or "func"
	// Type is the C type from DWARF, if known.
	Type string `json:"type,omitempty"`
}

// variable is a DWARF variable with a static address.
type variable struct {
	name     string // qualified: ns::counter, fa::st_local
	cu       string // compile unit file name (base)
	addr     uint32
	typ      dwarf.Offset
	external bool
}

// Table is a loaded ELF.
type Table struct {
	order binary.ByteOrder
	syms  map[string]Symbol // ELF symbols by name
	dw    *dwarf.Data       // nil without debug info
	vars  map[string][]variable
	// funcs maps subprogram DIEs to their qualified names ("K::m"), so an
	// out-of-line definition (only DW_AT_specification) names its statics.
	funcs map[dwarf.Offset]string
	// dataBitOffset holds structs whose bitfields use DW_AT_data_bit_offset
	// (DWARF 4+) rather than DW_AT_bit_offset (DWARF 2/3).
	dataBitOffset map[dwarf.Type]bool
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
	t := &Table{order: f.ByteOrder, syms: map[string]Symbol{}, vars: map[string][]variable{},
		funcs: map[dwarf.Offset]string{}, dataBitOffset: map[dwarf.Type]bool{}}
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
		if err := t.indexVariables(); err != nil {
			return nil, fmt.Errorf("read DWARF of %s: %w", path, err)
		}
	}
	return t, nil
}

// HasDWARF reports whether type information is available.
func (t *Table) HasDWARF() bool { return t.dw != nil }

// declInfo is what a declaration contributes to a definition that refers
// to it (DW_AT_specification, DW_AT_abstract_origin).
type declInfo struct {
	name, linkage string
	typ           dwarf.Offset
	external      bool
}

// attrMIPSLinkageName is the pre-DWARF 4 attribute for mangled names.
const attrMIPSLinkageName dwarf.Attr = 0x2007

type frame struct {
	tag  dwarf.Tag
	name string
	off  dwarf.Offset
}

// walk visits every entry with the stack of enclosing entries.
func (t *Table) walk(fn func(e *dwarf.Entry, stack []frame)) error {
	r := t.dw.Reader()
	var stack []frame
	for {
		e, err := r.Next()
		if err != nil {
			return err
		}
		if e == nil {
			return nil
		}
		if e.Tag == 0 {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		fn(e, stack)
		if e.Children {
			name, _ := e.Val(dwarf.AttrName).(string)
			stack = append(stack, frame{e.Tag, name, e.Offset})
		}
	}
}

// qualify builds the name of an entry from its enclosing scopes:
// namespaces and classes ("ns::K::v"), or the function for static locals
// ("fa::v", "K::m::v"), and returns the compile unit's file name.
func (t *Table) qualify(name string, stack []frame) (q, cu string) {
	parts := []string{}
	for _, f := range stack {
		switch f.tag {
		case dwarf.TagCompileUnit:
			cu = filepath.Base(f.name)
		case dwarf.TagNamespace, dwarf.TagClassType, dwarf.TagStructType, dwarf.TagUnionType:
			if f.name != "" {
				parts = append(parts, f.name)
			}
		case dwarf.TagSubprogram:
			// Statics are named after their function's qualified name.
			fn := t.funcs[f.off]
			if fn == "" {
				fn = strings.Join(append(parts, f.name), "::")
			}
			parts = []string{fn}
		}
	}
	return strings.Join(append(parts, name), "::"), cu
}

// staticAddr returns the address of a DW_OP_addr location expression.
func staticAddr(e *dwarf.Entry) (uint32, bool) {
	loc, ok := e.Val(dwarf.AttrLocation).([]byte)
	if !ok || len(loc) != 5 || loc[0] != 0x03 { // DW_OP_addr, 4-byte address
		return 0, false
	}
	return binary.LittleEndian.Uint32(loc[1:]), true
}

// indexVariables maps variable names to their static addresses and types:
// file-scope variables, namespace and class statics, and static locals.
// Stack locals are left out. Declarations (in headers, classes) supply
// name and type to definitions that refer to them.
func (t *Table) indexVariables() error {
	decls := map[dwarf.Offset]declInfo{}
	dbo := map[dwarf.Offset]bool{}
	err := t.walk(func(e *dwarf.Entry, stack []frame) {
		if e.Tag == dwarf.TagSubprogram {
			// Declarations (in a class) come before their out-of-line
			// definitions, which refer to them.
			if name, _ := e.Val(dwarf.AttrName).(string); name != "" {
				t.funcs[e.Offset], _ = t.qualify(name, stack)
			}
			for _, attr := range []dwarf.Attr{dwarf.AttrSpecification, dwarf.AttrAbstractOrigin} {
				if off, ok := e.Val(attr).(dwarf.Offset); ok && t.funcs[e.Offset] == "" {
					t.funcs[e.Offset] = t.funcs[off]
				}
			}
			return
		}
		if e.Tag == dwarf.TagMember && len(stack) > 0 {
			if _, ok := e.Val(dwarf.AttrDataBitOffset).(int64); ok {
				dbo[stack[len(stack)-1].off] = true
			}
		}
		if e.Tag != dwarf.TagVariable && e.Tag != dwarf.TagMember {
			return
		}
		var d declInfo
		if name, _ := e.Val(dwarf.AttrName).(string); name != "" {
			d.name, _ = t.qualify(name, stack)
		}
		d.typ, _ = e.Val(dwarf.AttrType).(dwarf.Offset)
		d.linkage, _ = e.Val(dwarf.AttrLinkageName).(string)
		if d.linkage == "" { // DWARF 2/3: DW_AT_MIPS_linkage_name
			d.linkage, _ = e.Val(attrMIPSLinkageName).(string)
		}
		d.external, _ = e.Val(dwarf.AttrExternal).(bool)
		decls[e.Offset] = d
	})
	if err != nil {
		return err
	}
	for off := range dbo {
		// Type() caches by offset, so resolved types are these pointers.
		if typ, err := t.dw.Type(off); err == nil {
			t.dataBitOffset[typ] = true
		}
	}
	return t.walk(func(e *dwarf.Entry, stack []frame) {
		if e.Tag != dwarf.TagVariable {
			return
		}
		addr, ok := staticAddr(e)
		if !ok {
			return // stack local, register, or declaration only
		}
		own := decls[e.Offset]
		v := variable{addr: addr, typ: own.typ, external: own.external}
		name, _ := e.Val(dwarf.AttrName).(string)
		linkage := own.linkage
		for _, attr := range []dwarf.Attr{dwarf.AttrSpecification, dwarf.AttrAbstractOrigin} {
			if off, ok := e.Val(attr).(dwarf.Offset); ok {
				d := decls[off]
				if v.typ == 0 {
					v.typ = d.typ
				}
				if v.name == "" {
					v.name = d.name
				}
				if linkage == "" {
					linkage = d.linkage
				}
				v.external = v.external || d.external
			}
		}
		q, cu := t.qualify(name, stack)
		if v.name == "" && name != "" { // the declaration's scope wins
			v.name = q
		}
		v.cu = cu
		if v.name == "" {
			return
		}
		t.vars[v.name] = append(t.vars[v.name], v)
		if linkage != "" && linkage != v.name {
			t.vars[linkage] = append(t.vars[linkage], v)
		}
	})
}

// lookup finds the variable for a (qualified, maybe file-prefixed) name.
func (t *Table) lookup(cu, name string) (*variable, error) {
	var cands []variable
	for _, v := range t.vars[name] {
		if cu == "" || v.cu == cu {
			cands = append(cands, v)
		}
	}
	switch len(cands) {
	case 0:
		return nil, nil
	case 1:
		return &cands[0], nil
	}
	var ext []variable
	for _, v := range cands {
		if v.external {
			ext = append(ext, v)
		}
	}
	if len(ext) == 1 {
		return &ext[0], nil
	}
	opts := []string{}
	for _, v := range cands {
		opts = append(opts, v.cu+":"+name)
	}
	sort.Strings(opts)
	return nil, fmt.Errorf("%q is ambiguous: use one of %s", name, strings.Join(opts, ", "))
}

// List returns variables (with qualified names and types, from DWARF) and
// other object and function symbols whose name contains filter
// (case-insensitive), sorted by name.
func (t *Table) List(filter string) []Symbol {
	f := strings.ToLower(filter)
	out := []Symbol{}
	covered := map[uint32]bool{}
	for name, vs := range t.vars {
		for _, v := range vs {
			if v.name != name {
				continue // mangled alias of a qualified name: list once
			}
			covered[v.addr] = true
			n := name
			if len(vs) > 1 && !v.external {
				n = v.cu + ":" + name
			}
			if f != "" && !strings.Contains(strings.ToLower(n), f) {
				continue
			}
			s := Symbol{Name: n, Address: v.addr, Kind: "object"}
			if typ, err := t.dw.Type(v.typ); err == nil && v.typ != 0 {
				s.Type, s.Size = typ.String(), uint32(max(typ.Size(), 0))
			}
			out = append(out, s)
		}
	}
	for _, s := range t.syms {
		if covered[s.Address] && s.Kind == "object" {
			continue
		}
		if f != "" && !strings.Contains(strings.ToLower(s.Name), f) {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	// Same name from DWARF and ELF at different addresses: keep both.
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
	// BitOffset and BitSize describe a bitfield within the Size bytes at
	// Address (BitSize 0: not a bitfield).
	BitOffset int `json:"bit_offset,omitempty"`
	BitSize   int `json:"bit_size,omitempty"`
}

// parseBase splits "file.c:ns::name.field[2]" into the compile unit
// ("file.c" or ""), the qualified name and the rest (".field[2]").
func parseBase(expr string) (cu, name, rest string) {
	s := expr
	// A single ':' (not '::') before the name selects the compile unit.
	for i := 0; i < len(s); i++ {
		if s[i] == '[' {
			break
		}
		if s[i] == ':' {
			if i+1 < len(s) && s[i+1] == ':' {
				i++
				continue
			}
			cu, s = s[:i], s[i+1:]
			break
		}
	}
	end := len(s)
	for i := 0; i < len(s); i++ {
		if s[i] == '.' || s[i] == '[' {
			end = i
			break
		}
	}
	return cu, s[:end], s[end:]
}

// Resolve resolves an expression: a variable name followed by .field and
// [index] parts, e.g. "test_state.touch.x" or "table[2]". Fields and
// indices need DWARF.
func (t *Table) Resolve(expr string) (Location, error) {
	cu, name, rest := parseBase(expr)
	if name == "" {
		return Location{}, fmt.Errorf("%q: no variable name", expr)
	}
	loc := Location{Expr: expr}
	var typ dwarf.Type
	v, err := t.lookup(cu, name)
	if err != nil {
		return Location{}, err
	}
	switch {
	case v != nil && v.typ != 0:
		typ, err = t.dw.Type(v.typ)
		if err != nil {
			return Location{}, fmt.Errorf("type of %q: %w", name, err)
		}
		loc.Address = v.addr
	case v != nil:
		loc.Address = v.addr
		if s, ok := t.syms[name]; ok {
			loc.Size = int(s.Size)
		}
	case cu == "":
		s, ok := t.syms[name]
		if !ok {
			return Location{}, fmt.Errorf("%w: %q", ErrNotFound, name)
		}
		loc.Address, loc.Size = s.Address, int(s.Size)
	default:
		return Location{}, fmt.Errorf("%w: %q in %s", ErrNotFound, name, cu)
	}
	if typ == nil {
		if rest != "" {
			if t.dw == nil {
				return Location{}, fmt.Errorf("%q: fields and indices need DWARF type info (build with -g)", expr)
			}
			return Location{}, fmt.Errorf("%q: no DWARF type for %q", expr, name)
		}
		return loc, nil
	}
	for rest != "" {
		typ = strip(typ)
		switch {
		case strings.HasPrefix(rest, "."):
			var field string
			field, rest = splitField(rest[1:])
			st, ok := typ.(*dwarf.StructType)
			if !ok {
				return Location{}, fmt.Errorf("%q: %s is not a struct or union", expr, typ)
			}
			f := findField(st, field)
			if f == nil {
				return Location{}, fmt.Errorf("%q: %s has no field %q (fields: %s)", expr, typ, field, fieldNames(st))
			}
			if f.BitSize > 0 {
				if rest != "" {
					return Location{}, fmt.Errorf("%q: bitfield %q has no parts", expr, field)
				}
				pos := t.bitPos(st, f)
				loc.Address += uint32(pos / 8)
				loc.BitOffset, loc.BitSize = int(pos%8), int(f.BitSize)
				loc.Type, loc.TypeName = f.Type, fmt.Sprintf("%s : %d", f.Type, f.BitSize)
				loc.Size = (loc.BitOffset + loc.BitSize + 7) / 8
				return loc, nil
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
			// Count 0 or -1: flexible array member or unknown length.
			if at.Count > 0 && int64(idx) >= at.Count {
				return Location{}, fmt.Errorf("%q: index %d out of range (length %d)", expr, idx, at.Count)
			}
			loc.Address += uint32(idx) * uint32(max(at.Type.Size(), 0))
			typ = at.Type
		default:
			return Location{}, fmt.Errorf("%q: unexpected %q", expr, rest)
		}
	}
	loc.Type, loc.TypeName, loc.Size = typ, typ.String(), int(typ.Size())
	return loc, nil
}

// bitPos returns a bitfield's bit position from the start of its struct,
// for DWARF 4+ (data_bit_offset) and DWARF 2/3 (bit_offset from the most
// significant bit of the storage unit, little-endian target).
func (t *Table) bitPos(st *dwarf.StructType, f *dwarf.StructField) int64 {
	if t.dataBitOffset[st] || f.ByteSize == 0 || f.DataBitOffset != 0 {
		return f.ByteOffset*8 + f.DataBitOffset
	}
	return f.ByteOffset*8 + f.ByteSize*8 - f.BitOffset - f.BitSize
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

// Enum is a decoded enum: its number and, if one matches, its name. It
// marshals as the name, or the number without a matching enumerator.
type Enum struct {
	Name  string
	Value int64
}

// MarshalJSON writes the name, or the number.
func (e Enum) MarshalJSON() ([]byte, error) {
	if e.Name != "" {
		return json.Marshal(e.Name)
	}
	return json.Marshal(e.Value)
}

// Decode converts memory to Go values by DWARF type: integers (int64 or
// uint64), float32/float64, bool, plain char arrays as strings (up to the
// first NUL), other arrays as slices, structs and unions as Struct,
// pointers as uint64, enums as Enum. With a nil type it returns the bytes
// as a hex string.
func (t *Table) Decode(b []byte, typ dwarf.Type) (any, error) {
	return t.DecodeAt(b, Location{Type: typ, Size: len(b)})
}

// DecodeAt decodes memory read for loc (handles bitfields).
func (t *Table) DecodeAt(b []byte, loc Location) (any, error) {
	if len(b) > MaxDecodeSize {
		return nil, fmt.Errorf("value of %d bytes is too large to decode (max %d); read a field, an element or raw memory", len(b), MaxDecodeSize)
	}
	if loc.Type == nil {
		return fmt.Sprintf("%x", b), nil
	}
	if loc.BitSize > 0 {
		return bitfield(b, loc.BitOffset, loc.BitSize, loc.Type), nil
	}
	return t.decode(b, loc.Type)
}

// bitfield decodes a bitfield of type typ; enum bitfields become Enum.
func bitfield(b []byte, off, size int, typ dwarf.Type) any {
	switch v := strip(typ).(type) {
	case *dwarf.IntType, *dwarf.CharType:
		return decodeBits(b, off, size, true)
	case *dwarf.EnumType:
		n := decodeBits(b, off, size, enumSigned(v))
		if u, ok := n.(uint64); ok {
			return enumOf(v, int64(u))
		}
		return enumOf(v, n.(int64))
	}
	return decodeBits(b, off, size, false)
}

// enumSigned reports whether an enum has negative enumerators; otherwise
// its values are unsigned (arm-none-eabi short enums: 0..255 in a byte).
func enumSigned(e *dwarf.EnumType) bool {
	for _, v := range e.Val {
		if v.Val < 0 {
			return true
		}
	}
	return false
}

func enumOf(e *dwarf.EnumType, n int64) Enum {
	out := Enum{Value: n}
	for _, ev := range e.Val {
		if ev.Val == n {
			out.Name = ev.Name
			break
		}
	}
	return out
}

// decodeBits extracts size bits starting at bit off of the little-endian
// bytes b.
func decodeBits(b []byte, off, size int, sign bool) any {
	var u uint64
	for i := len(b) - 1; i >= 0; i-- {
		u = u<<8 | uint64(b[i])
	}
	u = (u >> uint(off)) & (1<<uint(size) - 1)
	if sign && u&(1<<uint(size-1)) != 0 {
		return int64(u) - 1<<uint(size)
	}
	if sign {
		return int64(u)
	}
	return u
}

func (t *Table) decode(b []byte, typ dwarf.Type) (any, error) {
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
			return math.Float32frombits(t.order.Uint32(b)), nil
		case 8:
			return math.Float64frombits(t.order.Uint64(b)), nil
		}
	case *dwarf.EnumType:
		var n int64
		switch x := decodeInt(b, int(v.Size()), t.order, enumSigned(v)).(type) {
		case int64:
			n = x
		case uint64:
			n = int64(x)
		}
		return enumOf(v, n), nil
	case *dwarf.PtrType:
		return uint64(t.order.Uint32(b)), nil
	case *dwarf.ArrayType:
		el := strip(v.Type)
		if isPlainChar(v.Type) {
			n := int(v.Count)
			if n <= 0 || n > len(b) {
				n = len(b)
			}
			s := b[:n]
			if i := bytes.IndexByte(s, 0); i >= 0 {
				s = s[:i]
			}
			return string(s), nil
		}
		es := int(el.Size())
		if v.Count <= 0 || es <= 0 {
			return []any{}, nil // flexible or unknown: index elements instead
		}
		out := make([]any, 0, v.Count)
		for i := 0; i < int(v.Count); i++ {
			val, err := t.decode(b[i*es:(i+1)*es], el)
			if err != nil {
				return nil, err
			}
			out = append(out, val)
		}
		return out, nil
	case *dwarf.StructType:
		out := make(Struct, 0, len(v.Field))
		for _, f := range v.Field {
			if f.BitSize > 0 {
				pos := t.bitPos(v, f)
				first, last := int(pos/8), int((pos+f.BitSize+7)/8)
				if last > len(b) {
					continue
				}
				out = append(out, Field{f.Name, bitfield(b[first:last], int(pos%8), int(f.BitSize), f.Type)})
				continue
			}
			fs := int(f.Type.Size())
			if f.ByteOffset < 0 || int(f.ByteOffset)+fs > len(b) || fs <= 0 {
				continue
			}
			val, err := t.decode(b[f.ByteOffset:int(f.ByteOffset)+fs], f.Type)
			if err != nil {
				return nil, err
			}
			out = append(out, Field{f.Name, val})
		}
		return out, nil
	}
	return fmt.Sprintf("%x", b), nil
}

// isPlainChar reports whether t is plain char (unsigned on ARM), not
// signed/unsigned char or a typedef like uint8_t: only plain char arrays
// are text.
func isPlainChar(t dwarf.Type) bool {
	for {
		q, ok := t.(*dwarf.QualType)
		if !ok {
			break
		}
		t = q.Type
	}
	switch c := t.(type) {
	case *dwarf.CharType:
		return c.Name == "char"
	case *dwarf.UcharType:
		return c.Name == "char"
	}
	return false
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
		return math.Float32frombits(le.Uint32(b)), nil
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
