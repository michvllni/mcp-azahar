// SPDX-License-Identifier: GPL-3.0-or-later

// Package rpc is a client for the RPC server built into the Azahar 3DS
// emulator (protocol version 2, Azahar 2127). docs/PROTOCOL.md is the
// contract; section numbers in comments refer to it.
package rpc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Wire constants (PROTOCOL.md §3).
const (
	// ProtocolVersion is the only version Azahar 2127 accepts.
	ProtocolVersion = 2
	// HeaderSize is the size of the packet header in bytes.
	HeaderSize = 16
	// MaxDataSize is the maximum payload of a request or reply.
	MaxDataSize = 32 * 1024
	// MaxPacketSize is the maximum datagram size.
	MaxPacketSize = HeaderSize + MaxDataSize
	// MaxReadSize is the maximum size of one ReadMemory or ReadScreenshot.
	MaxReadSize = MaxDataSize
	// MaxWriteSize is the maximum payload of one WriteMemory (address and
	// size take 8 bytes of the data).
	MaxWriteSize = MaxDataSize - 8
	// DefaultPort is Azahar's hard-coded RPC port (PROTOCOL.md §2).
	DefaultPort = 45987
	// NoProcess is the "no process selected" value of SetGetProcess.
	NoProcess = 0xFFFFFFFF
	// MaxScreenshotScale is the largest accepted screenshot resolution scale.
	MaxScreenshotScale = 10
	// SaveStateSlots is the number of save state slots (0..10).
	SaveStateSlots = 11
)

// RequestType identifies a request (PROTOCOL.md §5).
type RequestType uint32

// Request types of protocol version 2.
const (
	TypeReadMemory     RequestType = 1
	TypeWriteMemory    RequestType = 2
	TypeProcessList    RequestType = 3
	TypeSetGetProcess  RequestType = 4
	TypeTakeScreenshot RequestType = 5
	TypeReadScreenshot RequestType = 6
	TypeGetPerfStats   RequestType = 7
	TypeSetFreeCam     RequestType = 8
	TypeSaveState      RequestType = 9
	TypeLoadState      RequestType = 10
	TypeSetInput       RequestType = 11
)

var requestTypeNames = map[RequestType]string{
	TypeReadMemory:     "ReadMemory",
	TypeWriteMemory:    "WriteMemory",
	TypeProcessList:    "ProcessList",
	TypeSetGetProcess:  "SetGetProcess",
	TypeTakeScreenshot: "TakeScreenshot",
	TypeReadScreenshot: "ReadScreenshot",
	TypeGetPerfStats:   "GetPerfStats",
	TypeSetFreeCam:     "SetFreeCam",
	TypeSaveState:      "SaveState",
	TypeLoadState:      "LoadState",
	TypeSetInput:       "SetInput",
}

func (t RequestType) String() string {
	if n, ok := requestTypeNames[t]; ok {
		return n
	}
	return fmt.Sprintf("RequestType(%d)", uint32(t))
}

// Idempotent reports whether a request may be resent after a timeout.
// TakeScreenshot (a resend gets Busy) and save/load state (side effects)
// must not be resent (PROTOCOL.md §4).
func (t RequestType) Idempotent() bool {
	switch t {
	case TypeTakeScreenshot, TypeSaveState, TypeLoadState:
		return false
	default:
		return true
	}
}

// Header is the 16-byte little-endian packet header (PROTOCOL.md §3).
type Header struct {
	Version uint32
	ID      uint32
	Type    RequestType
	Size    uint32
}

// EncodePacket returns header and data as one datagram. Size is taken from
// len(data).
func EncodePacket(id uint32, t RequestType, data []byte) ([]byte, error) {
	if len(data) > MaxDataSize {
		return nil, fmt.Errorf("rpc: %v data is %d bytes, max %d", t, len(data), MaxDataSize)
	}
	b := make([]byte, HeaderSize, HeaderSize+len(data))
	binary.LittleEndian.PutUint32(b[0:], ProtocolVersion)
	binary.LittleEndian.PutUint32(b[4:], id)
	binary.LittleEndian.PutUint32(b[8:], uint32(t))
	binary.LittleEndian.PutUint32(b[12:], uint32(len(data)))
	return append(b, data...), nil
}

// errMalformed reports a datagram that is not a valid packet.
var errMalformed = errors.New("rpc: malformed packet")

// DecodePacket splits a datagram into header and data. It fails, like
// Azahar does, unless the datagram is exactly HeaderSize+Size bytes and at
// most MaxPacketSize. The returned data aliases b.
func DecodePacket(b []byte) (Header, []byte, error) {
	if len(b) < HeaderSize || len(b) > MaxPacketSize {
		return Header{}, nil, errMalformed
	}
	h := Header{
		Version: binary.LittleEndian.Uint32(b[0:]),
		ID:      binary.LittleEndian.Uint32(b[4:]),
		Type:    RequestType(binary.LittleEndian.Uint32(b[8:])),
		Size:    binary.LittleEndian.Uint32(b[12:]),
	}
	if uint64(h.Size) != uint64(len(b)-HeaderSize) {
		return Header{}, nil, errMalformed
	}
	return h, b[HeaderSize:], nil
}

// args2 encodes the two u32 arguments most requests take.
func args2(a1, a2 uint32) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint32(b[0:], a1)
	binary.LittleEndian.PutUint32(b[4:], a2)
	return b
}

// Process is one entry of the process list (PROTOCOL.md §5.3).
type Process struct {
	PID     uint32
	TitleID uint64
	Name    string
}

const processInfoSize = 0x14

func decodeProcessList(data []byte) ([]Process, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("rpc: ProcessList reply is %d bytes", len(data))
	}
	n := binary.LittleEndian.Uint32(data)
	data = data[4:]
	if uint64(len(data)) != uint64(n)*processInfoSize {
		return nil, fmt.Errorf("rpc: ProcessList reply: %d entries in %d bytes", n, len(data))
	}
	ps := make([]Process, 0, n)
	for i := uint32(0); i < n; i++ {
		e := data[i*processInfoSize : (i+1)*processInfoSize]
		name := e[12:20]
		for j, c := range name {
			if c == 0 {
				name = name[:j]
				break
			}
		}
		ps = append(ps, Process{
			PID:     binary.LittleEndian.Uint32(e[0:]),
			TitleID: binary.LittleEndian.Uint64(e[4:]),
			Name:    string(name),
		})
	}
	return ps, nil
}

// ScreenshotResult is the status of a TakeScreenshot request (PROTOCOL.md §5.5).
type ScreenshotResult uint32

// Screenshot results.
const (
	ScreenshotSuccess         ScreenshotResult = 0
	ScreenshotUnsupported     ScreenshotResult = 1
	ScreenshotBusy            ScreenshotResult = 2
	ScreenshotTimeout         ScreenshotResult = 3
	ScreenshotEncodeFailed    ScreenshotResult = 4
	ScreenshotInvalidArgument ScreenshotResult = 5
)

var screenshotResultText = map[ScreenshotResult]string{
	ScreenshotSuccess:         "success",
	ScreenshotUnsupported:     "unsupported (the software renderer cannot take screenshots)",
	ScreenshotBusy:            "busy (another screenshot is in progress)",
	ScreenshotTimeout:         "timeout (no frame was rendered within 10 s, is emulation paused?)",
	ScreenshotEncodeFailed:    "PNG encoding failed",
	ScreenshotInvalidArgument: "invalid resolution scale or flags",
}

func (r ScreenshotResult) String() string {
	if s, ok := screenshotResultText[r]; ok {
		return s
	}
	return fmt.Sprintf("ScreenshotResult(%d)", uint32(r))
}

// Screenshot flags (PROTOCOL.md §5.5).
const (
	screenshotFlagSecondary uint32 = 1 << 0
	screenshotFlagRawRGB    uint32 = 1 << 1
)

type screenshotInfo struct {
	Result        ScreenshotResult
	Width, Height uint32
	DataSize      uint32
}

func decodeScreenshotInfo(data []byte) (screenshotInfo, error) {
	if len(data) < 16 {
		return screenshotInfo{}, fmt.Errorf("rpc: TakeScreenshot reply is %d bytes", len(data))
	}
	return screenshotInfo{
		Result:   ScreenshotResult(binary.LittleEndian.Uint32(data[0:])),
		Width:    binary.LittleEndian.Uint32(data[4:]),
		Height:   binary.LittleEndian.Uint32(data[8:]),
		DataSize: binary.LittleEndian.Uint32(data[12:]),
	}, nil
}

// PerfStats are the emulator performance statistics (PROTOCOL.md §5.6).
// Times are in seconds, rates per second.
type PerfStats struct {
	SystemFPS          float64 `json:"system_fps"`
	GameFPS            float64 `json:"game_fps"`
	TimeVBlankInterval float64 `json:"time_vblank_interval"`
	TimeHLESVC         float64 `json:"time_hle_svc"`
	TimeHLEIPC         float64 `json:"time_hle_ipc"`
	TimeGPU            float64 `json:"time_gpu"`
	TimeSwap           float64 `json:"time_swap"`
	TimeRemaining      float64 `json:"time_remaining"`
	EmulationSpeed     float64 `json:"emulation_speed"`
	ArticTransmitted   float64 `json:"artic_transmitted"`
	ArticEvents        uint32  `json:"artic_events"`
}

const perfStatsSize = 0x54

func decodePerfStats(data []byte) (PerfStats, error) {
	if len(data) < perfStatsSize {
		return PerfStats{}, fmt.Errorf("rpc: GetPerfStats reply is %d bytes, want %d", len(data), perfStatsSize)
	}
	f := func(i int) float64 { return math.Float64frombits(binary.LittleEndian.Uint64(data[i*8:])) }
	return PerfStats{
		SystemFPS: f(0), GameFPS: f(1), TimeVBlankInterval: f(2), TimeHLESVC: f(3),
		TimeHLEIPC: f(4), TimeGPU: f(5), TimeSwap: f(6), TimeRemaining: f(7),
		EmulationSpeed: f(8), ArticTransmitted: f(9),
		ArticEvents: binary.LittleEndian.Uint32(data[80:]),
	}, nil
}

// EncodePerfStats is the inverse of the reply decoding; used by fakes.
func EncodePerfStats(s PerfStats) []byte {
	b := make([]byte, perfStatsSize)
	for i, v := range []float64{s.SystemFPS, s.GameFPS, s.TimeVBlankInterval, s.TimeHLESVC,
		s.TimeHLEIPC, s.TimeGPU, s.TimeSwap, s.TimeRemaining, s.EmulationSpeed, s.ArticTransmitted} {
		binary.LittleEndian.PutUint64(b[i*8:], math.Float64bits(v))
	}
	binary.LittleEndian.PutUint32(b[80:], s.ArticEvents)
	return b
}
