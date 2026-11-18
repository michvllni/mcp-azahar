// SPDX-License-Identifier: GPL-3.0-or-later

// Package fakeserver is an in-process stand-in for Azahar's RPC server,
// implementing docs/PROTOCOL.md (Azahar 2127), for tests. It follows
// Azahar's validation rules (which datagrams are dropped, when replies are
// empty) and adds fault injection: dropped requests, stale replies, delays.
package fakeserver

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net"
	"sync"
	"time"

	"github.com/michvllni/mcp-azahar/internal/rpc"
)

const pageSize = 0x1000

// Writable regions checked by WriteMemory, start address only, end
// inclusive (PROTOCOL.md §5.2).
var writable = [][2]uint32{
	{0x00100000, 0x04000000},
	{0x08000000, 0x10000000},
	{0x14000000, 0x1C000000},
	{0x1E800000, 0x1EC00000},
}

// ScreenSource renders a screen as RGB888 for a screenshot. secondary
// selects the bottom screen in the single-screen layout.
type ScreenSource func(secondary bool, scale int) (w, h int, rgb []byte)

// Config sets up a Server. Zero values give one process "fake" with PID 11,
// a running app, and solid-color 400x240 / 320x240 screens.
type Config struct {
	Processes []rpc.Process
	// NotRunning makes save/load state requests fail as with no app loaded.
	NotRunning bool
	// NoRestartOnLoad applies LoadState immediately without recreating the
	// server (selection kept, port stays open), unlike Azahar.
	NoRestartOnLoad bool
	// RestartDelay is the time between accepting LoadState and closing the
	// socket (default 20 ms); RestartGap how long the port stays closed, so
	// requests are refused (default 50 ms). Azahar does the same when it
	// recreates its RPC server (PROTOCOL.md §1).
	RestartDelay, RestartGap time.Duration
	Screens                  ScreenSource
}

// Server is a fake RPC server listening on 127.0.0.1 (random port).
type Server struct {
	connMu sync.Mutex
	conn   net.PacketConn // replaced on restart
	addr   string
	done   chan struct{}
	wg     sync.WaitGroup

	mu         sync.Mutex
	cfg        Config
	mem        map[uint32][]byte // page base -> page
	selected   uint32
	input      rpc.Input
	requests   []rpc.Header
	dropNext   int
	stale      bool
	delay      time.Duration
	shotResult rpc.ScreenshotResult
	shotDelay  time.Duration
	shotBusy   bool
	shotData   []byte
	states     map[uint32]map[uint32][]byte
	restarts   int
}

// New starts a fake server.
func New(cfg Config) (*Server, error) {
	if cfg.Processes == nil {
		cfg.Processes = []rpc.Process{{PID: 11, TitleID: 0, Name: "fake"}}
	}
	if cfg.Screens == nil {
		cfg.Screens = SolidScreens
	}
	if cfg.RestartDelay <= 0 {
		cfg.RestartDelay = 20 * time.Millisecond
	}
	if cfg.RestartGap <= 0 {
		cfg.RestartGap = 50 * time.Millisecond
	}
	var lc net.ListenConfig
	conn, err := lc.ListenPacket(context.Background(), "udp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{
		conn:     conn,
		addr:     conn.LocalAddr().String(),
		done:     make(chan struct{}),
		cfg:      cfg,
		mem:      map[uint32][]byte{},
		selected: rpc.NoProcess,
		states:   map[uint32]map[uint32][]byte{},
	}
	s.wg.Add(1)
	go s.serve(conn)
	return s, nil
}

// SolidScreens renders the top screen red and the bottom screen blue.
func SolidScreens(secondary bool, scale int) (int, int, []byte) {
	if scale == 0 {
		scale = 1
	}
	w, h, c := 400*scale, 240*scale, [3]byte{255, 0, 0}
	if secondary {
		w, c = 320*scale, [3]byte{0, 0, 255}
	}
	return w, h, bytes.Repeat(c[:], w*h)
}

// Addr returns the "host:port" the server listens on (stable across
// restarts).
func (s *Server) Addr() string { return s.addr }

// Restarts returns how often the server was recreated by LoadState.
func (s *Server) Restarts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restarts
}

// Close stops the server.
func (s *Server) Close() error {
	close(s.done)
	s.connMu.Lock()
	var err error
	if s.conn != nil {
		err = s.conn.Close()
		s.conn = nil
	}
	s.connMu.Unlock()
	s.wg.Wait()
	return err
}

func (s *Server) currentConn() net.PacketConn {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	return s.conn
}

// restart mimics Azahar recreating its RPC server after a LoadState: the
// socket closes, the port is closed for RestartGap, the state is restored
// and the selection reset, then the same port is bound again.
func (s *Server) restart(state map[uint32][]byte) {
	defer s.wg.Done()
	select {
	case <-time.After(s.cfg.RestartDelay):
	case <-s.done:
		return
	}
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if s.conn == nil {
		return // closed
	}
	_ = s.conn.Close()
	s.conn = nil
	select {
	case <-time.After(s.cfg.RestartGap):
	case <-s.done:
		return
	}
	s.mu.Lock()
	s.mem = state
	s.selected = rpc.NoProcess
	s.restarts++
	s.mu.Unlock()
	var lc net.ListenConfig
	conn, err := lc.ListenPacket(context.Background(), "udp", s.addr)
	if err != nil {
		return // port taken meanwhile: the server stays down, like Azahar
	}
	s.conn = conn
	s.wg.Add(1)
	go s.serve(conn)
}

// SetMemory stores data at addr, bypassing the writable-region check.
func (s *Server) SetMemory(addr uint32, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.write(addr, data)
}

// Memory returns n bytes at addr.
func (s *Server) Memory(addr uint32, n int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read(addr, n)
}

// Input returns the last input set through SetInput.
func (s *Server) Input() rpc.Input {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.input
}

// Selected returns the selected process ID.
func (s *Server) Selected() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selected
}

// Requests returns the headers of all received valid-size datagrams.
func (s *Server) Requests() []rpc.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]rpc.Header(nil), s.requests...)
}

// CountRequests returns how many requests of type t were received.
func (s *Server) CountRequests(t rpc.RequestType) int {
	n := 0
	for _, h := range s.Requests() {
		if h.Type == t {
			n++
		}
	}
	return n
}

// DropNext makes the server ignore the next n requests (no reply).
func (s *Server) DropNext(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropNext = n
}

// SendStaleReplies makes the server send a reply with a wrong ID and
// a malformed datagram before every real reply.
func (s *Server) SendStaleReplies(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stale = on
}

// SetDelay delays every reply.
func (s *Server) SetDelay(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delay = d
}

// SetScreenshotResult makes TakeScreenshot fail with r (Success restores
// normal behavior).
func (s *Server) SetScreenshotResult(r rpc.ScreenshotResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shotResult = r
}

// SetScreenshotDelay delays the TakeScreenshot reply; during the delay
// TakeScreenshot answers Busy and ReadScreenshot answers empty.
func (s *Server) SetScreenshotDelay(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shotDelay = d
}

func (s *Server) serve(conn net.PacketConn) {
	defer s.wg.Done()
	buf := make([]byte, rpc.MaxPacketSize+1)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return // closed or restarting
			}
			continue
		}
		pkt := bytes.Clone(buf[:n])
		s.handle(conn, pkt, addr)
	}
}

func (s *Server) handle(conn net.PacketConn, pkt []byte, addr net.Addr) {
	h, data, err := rpc.DecodePacket(pkt)
	if err != nil {
		return // Azahar drops these silently (PROTOCOL.md §4)
	}
	s.mu.Lock()
	s.requests = append(s.requests, h)
	if s.dropNext > 0 {
		s.dropNext--
		s.mu.Unlock()
		return
	}
	stale, delay := s.stale, s.delay
	reply, async := s.dispatch(h, data, addr)
	s.mu.Unlock()
	if async {
		return
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	if stale {
		old, _ := rpc.EncodePacket(h.ID-1, h.Type, []byte{1, 2, 3, 4})
		_, _ = conn.WriteTo(old, addr)
		_, _ = conn.WriteTo([]byte{1, 2, 3}, addr)
	}
	s.send(conn, h, reply, addr)
}

func (s *Server) send(conn net.PacketConn, h rpc.Header, reply []byte, addr net.Addr) {
	out := make([]byte, rpc.HeaderSize, rpc.HeaderSize+len(reply))
	binary.LittleEndian.PutUint32(out[0:], h.Version) // echoed, even if wrong
	binary.LittleEndian.PutUint32(out[4:], h.ID)
	binary.LittleEndian.PutUint32(out[8:], uint32(h.Type))
	binary.LittleEndian.PutUint32(out[12:], uint32(len(reply)))
	_, _ = conn.WriteTo(append(out, reply...), addr)
}

func minSize(t rpc.RequestType) (int, bool) {
	switch t {
	case rpc.TypeReadMemory, rpc.TypeWriteMemory, rpc.TypeProcessList, rpc.TypeSetGetProcess,
		rpc.TypeTakeScreenshot, rpc.TypeReadScreenshot, rpc.TypeGetPerfStats,
		rpc.TypeSaveState, rpc.TypeLoadState:
		return 8, true
	case rpc.TypeSetInput:
		return 16, true
	case rpc.TypeSetFreeCam:
		return 72, true
	}
	return 0, false
}

// dispatch handles a request with s.mu held. async means the reply is
// sent later by a goroutine (screenshots).
func (s *Server) dispatch(h rpc.Header, data []byte, addr net.Addr) (reply []byte, async bool) {
	if h.Version != rpc.ProtocolVersion {
		return nil, false
	}
	if m, ok := minSize(h.Type); !ok || len(data) < m {
		return nil, false
	}
	a1 := binary.LittleEndian.Uint32(data[0:])
	a2 := binary.LittleEndian.Uint32(data[4:])
	switch h.Type {
	case rpc.TypeReadMemory:
		if a2 == 0 || a2 > rpc.MaxReadSize || !s.pidValid() {
			return nil, false
		}
		return s.read(a1, int(a2)), false
	case rpc.TypeWriteMemory:
		if a2 == 0 || a2 > rpc.MaxWriteSize || int(a2) > len(data)-8 {
			return nil, false
		}
		if isWritable(a1) && s.pidValid() {
			s.write(a1, data[8:8+a2])
		}
		return nil, false
	case rpc.TypeProcessList:
		start := min(int(a1), len(s.cfg.Processes))
		end := min(start+int(min(a2, 1638)), len(s.cfg.Processes))
		out := binary.LittleEndian.AppendUint32(nil, uint32(end-start))
		for _, p := range s.cfg.Processes[start:end] {
			out = binary.LittleEndian.AppendUint32(out, p.PID)
			out = binary.LittleEndian.AppendUint64(out, p.TitleID)
			var name [8]byte
			copy(name[:], p.Name)
			out = append(out, name[:]...)
		}
		return out, false
	case rpc.TypeSetGetProcess:
		if a1 == 0 {
			return binary.LittleEndian.AppendUint32(nil, s.selected), false
		}
		s.selected = a2
		return nil, false
	case rpc.TypeTakeScreenshot:
		return s.takeScreenshot(h, a1, a2, addr)
	case rpc.TypeReadScreenshot:
		if a2 == 0 || a2 > rpc.MaxReadSize || s.shotBusy || int(a1) >= len(s.shotData) {
			return nil, false
		}
		end := min(int(a1)+int(a2), len(s.shotData))
		return bytes.Clone(s.shotData[a1:end]), false
	case rpc.TypeGetPerfStats:
		if a1 > 1 {
			return nil, false
		}
		return rpc.EncodePerfStats(rpc.PerfStats{SystemFPS: 60, GameFPS: 30, EmulationSpeed: 1}), false
	case rpc.TypeSaveState, rpc.TypeLoadState:
		ok := !s.cfg.NotRunning && a1 < rpc.SaveStateSlots
		if ok && h.Type == rpc.TypeSaveState {
			s.states[a1] = cloneMem(s.mem)
		}
		if ok && h.Type == rpc.TypeLoadState {
			// Like Azahar, an empty slot is accepted; the load then fails
			// later without a restart (PROTOCOL.md §5.7).
			st, found := s.states[a1]
			switch {
			case found && s.cfg.NoRestartOnLoad:
				s.mem = cloneMem(st)
			case found:
				s.wg.Add(1)
				go s.restart(cloneMem(st))
			}
		}
		var v uint32
		if ok {
			v = 1
		}
		return binary.LittleEndian.AppendUint32(nil, v), false
	case rpc.TypeSetInput:
		in, _ := rpc.DecodeInput(data)
		s.input = in
		return nil, false
	}
	return nil, false // SetFreeCam: accepted, no effect
}

func (s *Server) takeScreenshot(h rpc.Header, scale, flags uint32, addr net.Addr) ([]byte, bool) {
	info := func(r rpc.ScreenshotResult, w, hgt, size int) []byte {
		b := make([]byte, 16)
		binary.LittleEndian.PutUint32(b[0:], uint32(r))
		binary.LittleEndian.PutUint32(b[4:], uint32(w))
		binary.LittleEndian.PutUint32(b[8:], uint32(hgt))
		binary.LittleEndian.PutUint32(b[12:], uint32(size))
		return b
	}
	if flags&^3 != 0 || scale > rpc.MaxScreenshotScale {
		return info(rpc.ScreenshotInvalidArgument, 0, 0, 0), false
	}
	if s.shotBusy {
		return info(rpc.ScreenshotBusy, 0, 0, 0), false
	}
	if s.shotResult != rpc.ScreenshotSuccess {
		return info(s.shotResult, 0, 0, 0), false
	}
	w, hgt, rgb := s.cfg.Screens(flags&1 != 0, int(scale))
	data := rgb
	if flags&2 == 0 {
		data = encodePNG(w, hgt, rgb)
	}
	reply := info(rpc.ScreenshotSuccess, w, hgt, len(data))
	if s.shotDelay == 0 {
		s.shotData = data
		return reply, false
	}
	s.shotBusy, s.shotData = true, nil
	d := s.shotDelay
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		select {
		case <-time.After(d):
		case <-s.done:
			return
		}
		s.mu.Lock()
		s.shotBusy, s.shotData = false, data
		s.mu.Unlock()
		if c := s.currentConn(); c != nil {
			s.send(c, h, reply, addr)
		}
	}()
	return nil, true
}

func encodePNG(w, h int, rgb []byte) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		img.Set(i%w, i/w, color.RGBA{rgb[i*3], rgb[i*3+1], rgb[i*3+2], 255})
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func (s *Server) pidValid() bool {
	if s.selected == rpc.NoProcess {
		return true
	}
	for _, p := range s.cfg.Processes {
		if p.PID == s.selected {
			return true
		}
	}
	return false
}

func isWritable(addr uint32) bool {
	for _, r := range writable {
		if addr >= r[0] && addr <= r[1] {
			return true
		}
	}
	return false
}

func (s *Server) read(addr uint32, n int) []byte {
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		a := addr + uint32(i)
		if p, ok := s.mem[a&^(pageSize-1)]; ok {
			out[i] = p[a&(pageSize-1)]
		}
	}
	return out
}

func (s *Server) write(addr uint32, data []byte) {
	for i, b := range data {
		a := addr + uint32(i)
		base := a &^ (pageSize - 1)
		p, ok := s.mem[base]
		if !ok {
			p = make([]byte, pageSize)
			s.mem[base] = p
		}
		p[a&(pageSize-1)] = b
	}
}

func cloneMem(m map[uint32][]byte) map[uint32][]byte {
	out := make(map[uint32][]byte, len(m))
	for k, v := range m {
		out[k] = bytes.Clone(v)
	}
	return out
}
