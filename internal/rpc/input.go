// SPDX-License-Identifier: GPL-3.0-or-later

package rpc

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
)

// Button is a 3DS HID pad bit (PROTOCOL.md §5.8).
type Button uint32

// Buttons accepted by SetInput. HOME and POWER are flags, see Input.
const (
	ButtonA      Button = 1 << 0
	ButtonB      Button = 1 << 1
	ButtonSelect Button = 1 << 2
	ButtonStart  Button = 1 << 3
	ButtonRight  Button = 1 << 4
	ButtonLeft   Button = 1 << 5
	ButtonUp     Button = 1 << 6
	ButtonDown   Button = 1 << 7
	ButtonR      Button = 1 << 8
	ButtonL      Button = 1 << 9
	ButtonX      Button = 1 << 10
	ButtonY      Button = 1 << 11
	ButtonZL     Button = 1 << 14
	ButtonZR     Button = 1 << 15
)

var buttonNames = map[string]Button{
	"A": ButtonA, "B": ButtonB, "X": ButtonX, "Y": ButtonY,
	"L": ButtonL, "R": ButtonR, "ZL": ButtonZL, "ZR": ButtonZR,
	"START": ButtonStart, "SELECT": ButtonSelect,
	"UP": ButtonUp, "DOWN": ButtonDown, "LEFT": ButtonLeft, "RIGHT": ButtonRight,
}

// ButtonNames returns the accepted button names, sorted.
func ButtonNames() []string {
	names := make([]string, 0, len(buttonNames))
	for n := range buttonNames {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ParseButton parses a button name, case-insensitively. Aliases: DPAD-UP
// etc. for the directions.
func ParseButton(name string) (Button, error) {
	n := strings.ToUpper(strings.TrimSpace(name))
	n = strings.TrimPrefix(strings.TrimPrefix(n, "DPAD-"), "DPAD_")
	if b, ok := buttonNames[n]; ok {
		return b, nil
	}
	return 0, fmt.Errorf("unknown button %q (valid: %s; HOME and POWER are separate flags)",
		name, strings.Join(ButtonNames(), ", "))
}

// Touch screen and circle pad ranges (PROTOCOL.md §5.8).
const (
	TouchMaxX     = 319
	TouchMaxY     = 239
	CirclePadMax  = 0x9A
	inputInfoSize = 16
)

// Point is a bottom-screen touch position in pixels.
type Point struct{ X, Y uint16 }

// Stick is a circle pad position in raw HID units, -CirclePadMax..CirclePadMax.
type Stick struct{ X, Y int16 }

// Input is the state SetInput applies. Azahar holds it until the next
// SetInput; the zero Input releases everything.
type Input struct {
	Buttons   Button
	Home      bool
	Power     bool
	Touch     *Point // nil: not touched
	CirclePad *Stick // nil: not set (configured input devices apply)
}

// Validate checks the ranges Azahar would otherwise clamp silently.
func (in Input) Validate() error {
	if in.Touch != nil && (in.Touch.X > TouchMaxX || in.Touch.Y > TouchMaxY) {
		return fmt.Errorf("touch (%d, %d) outside the bottom screen (0..%d, 0..%d)",
			in.Touch.X, in.Touch.Y, TouchMaxX, TouchMaxY)
	}
	if in.CirclePad != nil {
		for _, v := range []int16{in.CirclePad.X, in.CirclePad.Y} {
			if v < -CirclePadMax || v > CirclePadMax {
				return fmt.Errorf("circle pad (%d, %d) outside -%d..%d",
					in.CirclePad.X, in.CirclePad.Y, CirclePadMax, CirclePadMax)
			}
		}
	}
	return nil
}

// Input flags (PROTOCOL.md §5.8).
const (
	inputFlagHome      uint32 = 1 << 0
	inputFlagPower     uint32 = 1 << 1
	inputFlagTouch     uint32 = 1 << 2
	inputFlagCirclePad uint32 = 1 << 3
)

// Encode returns the 16-byte InputInfo payload.
func (in Input) Encode() []byte {
	b := make([]byte, inputInfoSize)
	var flags uint32
	if in.Home {
		flags |= inputFlagHome
	}
	if in.Power {
		flags |= inputFlagPower
	}
	binary.LittleEndian.PutUint32(b[0:], uint32(in.Buttons))
	if in.Touch != nil {
		flags |= inputFlagTouch
		binary.LittleEndian.PutUint16(b[8:], in.Touch.X)
		binary.LittleEndian.PutUint16(b[10:], in.Touch.Y)
	}
	if in.CirclePad != nil {
		flags |= inputFlagCirclePad
		binary.LittleEndian.PutUint16(b[12:], uint16(in.CirclePad.X))
		binary.LittleEndian.PutUint16(b[14:], uint16(in.CirclePad.Y))
	}
	binary.LittleEndian.PutUint32(b[4:], flags)
	return b
}

// DecodeInput parses an InputInfo payload; used by fakes and tests.
func DecodeInput(b []byte) (Input, error) {
	if len(b) < inputInfoSize {
		return Input{}, fmt.Errorf("rpc: InputInfo is %d bytes, want %d", len(b), inputInfoSize)
	}
	flags := binary.LittleEndian.Uint32(b[4:])
	in := Input{
		Buttons: Button(binary.LittleEndian.Uint32(b[0:])),
		Home:    flags&inputFlagHome != 0,
		Power:   flags&inputFlagPower != 0,
	}
	if flags&inputFlagTouch != 0 {
		in.Touch = &Point{binary.LittleEndian.Uint16(b[8:]), binary.LittleEndian.Uint16(b[10:])}
	}
	if flags&inputFlagCirclePad != 0 {
		in.CirclePad = &Stick{int16(binary.LittleEndian.Uint16(b[12:])), int16(binary.LittleEndian.Uint16(b[14:]))}
	}
	return in, nil
}
