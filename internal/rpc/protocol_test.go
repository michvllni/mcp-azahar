// SPDX-License-Identifier: GPL-3.0-or-later

package rpc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func TestEncodeDecodePacket(t *testing.T) {
	pkt, err := EncodePacket(0xDEADBEEF, TypeReadMemory, []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{2, 0, 0, 0, 0xEF, 0xBE, 0xAD, 0xDE, 1, 0, 0, 0, 3, 0, 0, 0, 1, 2, 3}
	if !bytes.Equal(pkt, want) {
		t.Fatalf("EncodePacket = % x, want % x", pkt, want)
	}
	h, data, err := DecodePacket(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if h != (Header{Version: 2, ID: 0xDEADBEEF, Type: TypeReadMemory, Size: 3}) || !bytes.Equal(data, []byte{1, 2, 3}) {
		t.Fatalf("DecodePacket = %+v % x", h, data)
	}
}

func TestEncodePacketTooLarge(t *testing.T) {
	if _, err := EncodePacket(1, TypeWriteMemory, make([]byte, MaxDataSize+1)); err == nil {
		t.Fatal("expected error for oversized data")
	}
	if _, err := EncodePacket(1, TypeWriteMemory, make([]byte, MaxDataSize)); err != nil {
		t.Fatalf("max size rejected: %v", err)
	}
}

func TestDecodePacketRejects(t *testing.T) {
	good, _ := EncodePacket(1, TypeReadMemory, []byte{1, 2, 3, 4})
	tooLong := make([]byte, MaxPacketSize+1)
	binary.LittleEndian.PutUint32(tooLong[12:], MaxDataSize+1)
	for name, b := range map[string][]byte{
		"short":          good[:15],
		"size too big":   good[:len(good)-1],
		"size too small": append(bytes.Clone(good), 0),
		"over max":       tooLong,
	} {
		if _, _, err := DecodePacket(b); !errors.Is(err, errMalformed) {
			t.Errorf("%s: err = %v, want errMalformed", name, err)
		}
	}
}

func TestIdempotent(t *testing.T) {
	for _, tt := range []RequestType{TypeTakeScreenshot, TypeSaveState, TypeLoadState} {
		if tt.Idempotent() {
			t.Errorf("%v must not be resent", tt)
		}
	}
	for _, tt := range []RequestType{TypeReadMemory, TypeWriteMemory, TypeReadScreenshot, TypeSetInput} {
		if !tt.Idempotent() {
			t.Errorf("%v should be resendable", tt)
		}
	}
	if got := RequestType(99).String(); got != "RequestType(99)" {
		t.Errorf("String() = %q", got)
	}
}

func TestDecodeProcessList(t *testing.T) {
	b := binary.LittleEndian.AppendUint32(nil, 2)
	for _, p := range []Process{{11, 0x0004000000123400, "min"}, {12, 0, "eightchr"}} {
		b = binary.LittleEndian.AppendUint32(b, p.PID)
		b = binary.LittleEndian.AppendUint64(b, p.TitleID)
		var n [8]byte
		copy(n[:], p.Name)
		b = append(b, n[:]...)
	}
	ps, err := decodeProcessList(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0] != (Process{11, 0x0004000000123400, "min"}) || ps[1].Name != "eightchr" {
		t.Fatalf("got %+v", ps)
	}
	if _, err := decodeProcessList(b[:len(b)-1]); err == nil {
		t.Fatal("truncated list accepted")
	}
	if _, err := decodeProcessList(nil); err == nil {
		t.Fatal("empty reply accepted")
	}
}

func TestPerfStatsRoundTrip(t *testing.T) {
	in := PerfStats{SystemFPS: 59.9, GameFPS: 30, EmulationSpeed: 1.25, ArticEvents: 7}
	out, err := decodePerfStats(EncodePerfStats(in))
	if err != nil || out != in {
		t.Fatalf("got %+v, %v", out, err)
	}
	if _, err := decodePerfStats(make([]byte, perfStatsSize-1)); err == nil {
		t.Fatal("short reply accepted")
	}
}

func TestInputEncoding(t *testing.T) {
	in := Input{Buttons: ButtonA | ButtonZR, Home: true, Touch: &Point{319, 239}, CirclePad: &Stick{-CirclePadMax, 5}}
	b := in.Encode()
	want := []byte{
		0x01, 0x80, 0, 0, // A | ZR
		0x0D, 0, 0, 0, // home | touch | circle pad
		0x3F, 0x01, 0xEF, 0x00, // 319, 239
		0x66, 0xFF, 0x05, 0x00, // -0x9A, 5
	}
	if !bytes.Equal(b, want) {
		t.Fatalf("Encode = % x, want % x", b, want)
	}
	out, err := DecodeInput(b)
	if err != nil {
		t.Fatal(err)
	}
	if out.Buttons != in.Buttons || !out.Home || out.Power || *out.Touch != *in.Touch || *out.CirclePad != *in.CirclePad {
		t.Fatalf("DecodeInput = %+v", out)
	}
	if !bytes.Equal((Input{}).Encode(), make([]byte, 16)) {
		t.Fatal("zero input must encode as all zeros (release)")
	}
}

func TestInputValidate(t *testing.T) {
	for _, bad := range []Input{
		{Touch: &Point{320, 0}},
		{Touch: &Point{0, 240}},
		{CirclePad: &Stick{CirclePadMax + 1, 0}},
		{CirclePad: &Stick{0, -CirclePadMax - 1}},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if err := (Input{Touch: &Point{0, 0}, CirclePad: &Stick{CirclePadMax, -CirclePadMax}}).Validate(); err != nil {
		t.Errorf("valid input rejected: %v", err)
	}
}

func TestParseButton(t *testing.T) {
	for _, tc := range []struct {
		name string
		want Button
	}{
		{"a", ButtonA}, {" Start ", ButtonStart}, {"zl", ButtonZL}, {"dpad-up", ButtonUp}, {"DPAD_LEFT", ButtonLeft},
	} {
		got, err := ParseButton(tc.name)
		if err != nil || got != tc.want {
			t.Errorf("ParseButton(%q) = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
	_, err := ParseButton("home")
	if err == nil || !strings.Contains(err.Error(), "HOME and POWER") || !strings.Contains(err.Error(), "SELECT") {
		t.Errorf("ParseButton(home) error = %v", err)
	}
	if n := len(ButtonNames()); n != 14 {
		t.Errorf("ButtonNames has %d entries, want 14", n)
	}
}
