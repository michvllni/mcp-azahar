// SPDX-License-Identifier: GPL-3.0-or-later

package rpc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"sync"
	"time"
)

// Errors returned by the client. Use errors.Is.
var (
	// ErrTimeout means Azahar did not answer, after all allowed attempts.
	// The RPC server only runs while an app is loaded (PROTOCOL.md §1).
	ErrTimeout = errors.New("rpc: no reply from Azahar")
	// ErrRejected means Azahar answered with an empty reply where data was
	// expected: invalid arguments, wrong protocol version, or a selected
	// process that no longer exists (PROTOCOL.md §4).
	ErrRejected = errors.New("rpc: request rejected by Azahar")
	// ErrNotAccepted means a save or load state request was refused: no app
	// running or invalid slot.
	ErrNotAccepted = errors.New("rpc: save state request not accepted")
	// ErrWriteNotApplied means a verified write read back different data,
	// e.g. because the address is outside the writable regions
	// (PROTOCOL.md §5.2).
	ErrWriteNotApplied = errors.New("rpc: memory write was not applied")
	// ErrClosed means the client was closed.
	ErrClosed = errors.New("rpc: client closed")
)

// ScreenshotError reports a TakeScreenshot result other than success.
type ScreenshotError struct{ Result ScreenshotResult }

func (e *ScreenshotError) Error() string { return "rpc: screenshot failed: " + e.Result.String() }

// Dialer opens the connection to an RPC server. Any datagram-preserving
// net.Conn works ("udp", or "unixgram" for a relay).
type Dialer func(ctx context.Context) (net.Conn, error)

// UDPDialer dials Azahar's RPC server at addr ("host:port").
func UDPDialer(addr string) Dialer {
	return func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "udp", addr)
	}
}

// LocalAddr is the address of an Azahar on this host.
var LocalAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(DefaultPort))

// Options tune timeouts and retries. Zero values select the defaults.
type Options struct {
	// Timeout per attempt for ordinary requests (default 2 s).
	Timeout time.Duration
	// Retries after a timeout, for idempotent requests only (default 3).
	// Negative disables retries.
	Retries int
	// ScreenshotTimeout for TakeScreenshot, never resent (default 30 s;
	// Azahar itself gives up after 10 s without a frame).
	ScreenshotTimeout time.Duration
	// StateTimeout for SaveState/LoadState, never resent (default 20 s).
	StateTimeout time.Duration
	// ReloadTimeout bounds the wait for the RPC server restart after a
	// LoadState when a process is selected (default 15 s).
	ReloadTimeout time.Duration
	// PollInterval between checks while waiting for that restart (default 100 ms).
	PollInterval time.Duration
}

func (o Options) withDefaults() Options {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&o.Timeout, 2*time.Second)
	def(&o.ScreenshotTimeout, 30*time.Second)
	def(&o.StateTimeout, 20*time.Second)
	def(&o.ReloadTimeout, 15*time.Second)
	def(&o.PollInterval, 100*time.Millisecond)
	switch {
	case o.Retries == 0:
		o.Retries = 3
	case o.Retries < 0:
		o.Retries = 0
	}
	return o
}

// Client talks to one Azahar RPC server. It is safe for concurrent use;
// requests are sent one at a time, as Azahar processes them in order anyway.
type Client struct {
	mu       sync.Mutex
	conn     net.Conn
	opts     Options
	nextID   uint32
	selected uint32 // last process selected through this client
	buf      []byte
	closed   bool
}

// Dial connects with dialer and returns a client. UDP "connects" without
// contacting the server, so Dial succeeds even if Azahar is not running.
func Dial(ctx context.Context, dialer Dialer, opts Options) (*Client, error) {
	conn, err := dialer(ctx)
	if err != nil {
		return nil, fmt.Errorf("rpc: dial: %w", err)
	}
	return New(conn, opts), nil
}

// New wraps an open connection. The client owns conn and closes it in Close.
func New(conn net.Conn, opts Options) *Client {
	return &Client{
		conn:     conn,
		opts:     opts.withDefaults(),
		nextID:   rand.Uint32(),
		selected: NoProcess,
		buf:      make([]byte, MaxPacketSize+1),
	}
}

// Close closes the connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.conn.Close()
}

// do sends one request and waits for the matching reply. attempts > 1
// resends after a per-attempt timeout. The returned data is a copy.
func (c *Client) do(ctx context.Context, t RequestType, data []byte, timeout time.Duration, attempts int) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.nextID++
	id := c.nextID
	pkt, err := EncodePacket(id, t, data)
	if err != nil {
		return nil, err
	}

	// Unblock a pending Read when ctx ends.
	stop := context.AfterFunc(ctx, func() { _ = c.conn.SetReadDeadline(time.Now()) })
	defer stop()

	for attempt := 0; attempt < attempts; attempt++ {
		if _, err := c.conn.Write(pkt); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("rpc: send %v: %w", t, err)
		}
		deadline := time.Now().Add(timeout)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		if err := c.conn.SetReadDeadline(deadline); err != nil {
			return nil, fmt.Errorf("rpc: %w", err)
		}
		for {
			n, err := c.conn.Read(c.buf)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					break // next attempt
				}
				return nil, fmt.Errorf("rpc: receive %v: %w", t, err)
			}
			h, reply, err := DecodePacket(c.buf[:n])
			if err != nil || h.Version != ProtocolVersion || h.ID != id || h.Type != t {
				continue // malformed, or a late reply to an earlier request
			}
			return bytes.Clone(reply), nil
		}
	}
	return nil, fmt.Errorf("%w: %v after %d attempt(s) of %v", ErrTimeout, t, attempts, timeout)
}

// request runs an ordinary request with the default timeout and retries.
func (c *Client) request(ctx context.Context, t RequestType, data []byte) ([]byte, error) {
	attempts := 1
	if t.Idempotent() {
		attempts += c.opts.Retries
	}
	return c.do(ctx, t, data, c.opts.Timeout, attempts)
}

func checkRange(addr uint32, size int) error {
	if size < 0 || uint64(addr)+uint64(size) > 1<<32 {
		return fmt.Errorf("rpc: range %#08x+%d exceeds the 32-bit address space", addr, size)
	}
	return nil
}

// ReadMemory reads size bytes at the virtual address addr of the selected
// process (or the current one if none is selected), in chunks of
// MaxReadSize. Unmapped memory reads as zeros (PROTOCOL.md §5.1).
func (c *Client) ReadMemory(ctx context.Context, addr uint32, size int) ([]byte, error) {
	if err := checkRange(addr, size); err != nil {
		return nil, err
	}
	out := make([]byte, 0, size)
	for len(out) < size {
		n := min(size-len(out), MaxReadSize)
		a := addr + uint32(len(out))
		reply, err := c.request(ctx, TypeReadMemory, args2(a, uint32(n)))
		if err != nil {
			return nil, err
		}
		if len(reply) != n {
			return nil, fmt.Errorf("%w: ReadMemory %#08x+%d returned %d bytes (selected process gone?)",
				ErrRejected, a, n, len(reply))
		}
		out = append(out, reply...)
	}
	return out, nil
}

// WriteMemory writes data at addr in chunks of MaxWriteSize. Azahar
// silently ignores writes outside its writable regions and never reports
// failure (PROTOCOL.md §5.2); use WriteMemoryVerified to detect that.
func (c *Client) WriteMemory(ctx context.Context, addr uint32, data []byte) error {
	if err := checkRange(addr, len(data)); err != nil {
		return err
	}
	for off := 0; off < len(data); {
		n := min(len(data)-off, MaxWriteSize)
		a := addr + uint32(off)
		req := append(args2(a, uint32(n)), data[off:off+n]...)
		if _, err := c.request(ctx, TypeWriteMemory, req); err != nil {
			return err
		}
		off += n
	}
	return nil
}

// WriteMemoryVerified writes data and reads it back. It returns
// ErrWriteNotApplied if the memory does not hold data afterwards.
func (c *Client) WriteMemoryVerified(ctx context.Context, addr uint32, data []byte) error {
	if err := c.WriteMemory(ctx, addr, data); err != nil {
		return err
	}
	got, err := c.ReadMemory(ctx, addr, len(data))
	if err != nil {
		return err
	}
	if !bytes.Equal(got, data) {
		return fmt.Errorf("%w at %#08x+%d (outside the writable regions?)", ErrWriteNotApplied, addr, len(data))
	}
	return nil
}

// Processes lists the emulated processes (PROTOCOL.md §5.3).
func (c *Client) Processes(ctx context.Context) ([]Process, error) {
	var all []Process
	for {
		reply, err := c.request(ctx, TypeProcessList, args2(uint32(len(all)), 0x7FFFFFFF))
		if err != nil {
			return nil, err
		}
		if len(reply) == 0 {
			return nil, fmt.Errorf("%w: ProcessList", ErrRejected)
		}
		ps, err := decodeProcessList(reply)
		if err != nil {
			return nil, err
		}
		if len(ps) == 0 {
			return all, nil
		}
		all = append(all, ps...)
	}
}

// Process returns the process ID selected on the server, or NoProcess.
func (c *Client) Process(ctx context.Context) (uint32, error) {
	reply, err := c.request(ctx, TypeSetGetProcess, args2(0, 0))
	if err != nil {
		return 0, err
	}
	if len(reply) < 4 {
		return 0, fmt.Errorf("%w: SetGetProcess get", ErrRejected)
	}
	return binary.LittleEndian.Uint32(reply), nil
}

// SelectProcess selects the process whose memory later requests access.
// The selection is server state shared by all clients, and is reset when
// a save state is loaded; LoadState restores it (PROTOCOL.md §5.4).
func (c *Client) SelectProcess(ctx context.Context, pid uint32) error {
	if _, err := c.request(ctx, TypeSetGetProcess, args2(1, pid)); err != nil {
		return err
	}
	c.mu.Lock()
	c.selected = pid
	c.mu.Unlock()
	return nil
}

// Screen selects a 3DS screen for screenshots.
type Screen int

// Screens. With the single-screen layout that sessions pin, Top is the
// primary capture and Bottom the secondary one (PROTOCOL.md §5.5).
const (
	ScreenTop Screen = iota
	ScreenBottom
)

func (s Screen) String() string {
	if s == ScreenBottom {
		return "bottom"
	}
	return "top"
}

// ImageFormat is the encoding of Image.Data.
type ImageFormat int

// Image formats.
const (
	FormatPNG ImageFormat = iota // PNG, RGB 8 bit
	FormatRGB                    // raw RGB888 rows, top to bottom
)

// ScreenshotRequest selects what to capture.
type ScreenshotRequest struct {
	Screen Screen
	// Scale is the resolution scale 1..MaxScreenshotScale; 0 uses
	// Azahar's current internal resolution.
	Scale int
	// Raw returns RGB888 instead of PNG (faster locally).
	Raw bool
}

// Image is a captured screenshot.
type Image struct {
	Width, Height int
	Format        ImageFormat
	Data          []byte
}

// Screenshot captures the next rendered frame (PROTOCOL.md §5.5). The
// image size depends on Azahar's layout setting and the scale.
func (c *Client) Screenshot(ctx context.Context, req ScreenshotRequest) (*Image, error) {
	if req.Scale < 0 || req.Scale > MaxScreenshotScale {
		return nil, fmt.Errorf("rpc: screenshot scale %d outside 0..%d", req.Scale, MaxScreenshotScale)
	}
	var flags uint32
	if req.Screen == ScreenBottom {
		flags |= screenshotFlagSecondary
	}
	if req.Raw {
		flags |= screenshotFlagRawRGB
	}
	reply, err := c.do(ctx, TypeTakeScreenshot, args2(uint32(req.Scale), flags), c.opts.ScreenshotTimeout, 1)
	if err != nil {
		return nil, err
	}
	info, err := decodeScreenshotInfo(reply)
	if err != nil {
		return nil, err
	}
	if info.Result != ScreenshotSuccess {
		return nil, &ScreenshotError{Result: info.Result}
	}
	if req.Raw && uint64(info.DataSize) != uint64(info.Width)*uint64(info.Height)*3 {
		return nil, fmt.Errorf("rpc: raw screenshot %dx%d has %d bytes", info.Width, info.Height, info.DataSize)
	}
	data := make([]byte, 0, info.DataSize)
	for uint32(len(data)) < info.DataSize {
		n := min(info.DataSize-uint32(len(data)), MaxReadSize)
		chunk, err := c.request(ctx, TypeReadScreenshot, args2(uint32(len(data)), n))
		if err != nil {
			return nil, err
		}
		if len(chunk) == 0 {
			return nil, fmt.Errorf("%w: ReadScreenshot at offset %d of %d (another screenshot replaced it?)",
				ErrRejected, len(data), info.DataSize)
		}
		data = append(data, chunk...)
	}
	if uint32(len(data)) != info.DataSize {
		return nil, fmt.Errorf("rpc: screenshot data is %d bytes, want %d", len(data), info.DataSize)
	}
	format := FormatPNG
	if req.Raw {
		format = FormatRGB
	}
	return &Image{Width: int(info.Width), Height: int(info.Height), Format: format, Data: data}, nil
}

// PerfStats returns the emulator's performance statistics. With reset, the
// statistics cover the time since the previous reset and a new interval
// starts (PROTOCOL.md §5.6).
func (c *Client) PerfStats(ctx context.Context, reset bool) (PerfStats, error) {
	var mode uint32
	if reset {
		mode = 1
	}
	reply, err := c.request(ctx, TypeGetPerfStats, args2(mode, 0))
	if err != nil {
		return PerfStats{}, err
	}
	if len(reply) == 0 {
		return PerfStats{}, fmt.Errorf("%w: GetPerfStats", ErrRejected)
	}
	return decodePerfStats(reply)
}

func (c *Client) state(ctx context.Context, t RequestType, slot int) error {
	if slot < 0 || slot >= SaveStateSlots {
		return fmt.Errorf("rpc: save state slot %d outside 0..%d", slot, SaveStateSlots-1)
	}
	reply, err := c.do(ctx, t, args2(uint32(slot), 0), c.opts.StateTimeout, 1)
	if err != nil {
		return err
	}
	if len(reply) < 4 {
		return fmt.Errorf("%w: %v", ErrRejected, t)
	}
	if binary.LittleEndian.Uint32(reply) == 0 {
		return fmt.Errorf("%w: %v slot %d (no app running?)", ErrNotAccepted, t, slot)
	}
	return nil
}

// SaveState requests a save state in slot 0..10. Azahar writes it shortly
// after accepting the request, on the emulation thread (PROTOCOL.md §5.7).
func (c *Client) SaveState(ctx context.Context, slot int) error {
	return c.state(ctx, TypeSaveState, slot)
}

// LoadState requests loading the save state in slot 0..10. Loading
// recreates Azahar's RPC server, which resets the selected process. If a
// process was selected through this client, LoadState waits for that
// restart (up to Options.ReloadTimeout) and selects the process again;
// otherwise it returns once the request is accepted and the load completes
// asynchronously (PROTOCOL.md §1, §5.7).
func (c *Client) LoadState(ctx context.Context, slot int) error {
	if err := c.state(ctx, TypeLoadState, slot); err != nil {
		return err
	}
	c.mu.Lock()
	pid := c.selected
	c.mu.Unlock()
	if pid == NoProcess {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, c.opts.ReloadTimeout)
	defer cancel()
	probe := min(c.opts.Timeout, 500*time.Millisecond)
	for {
		// A request may be lost while the server restarts: one short attempt.
		reply, err := c.do(ctx, TypeSetGetProcess, args2(0, 0), probe, 1)
		switch {
		case err == nil && len(reply) >= 4 && binary.LittleEndian.Uint32(reply) != pid:
			return c.SelectProcess(context.WithoutCancel(ctx), pid)
		case err != nil && !errors.Is(err, ErrTimeout):
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("%w: RPC server did not restart within %v after LoadState",
					ErrTimeout, c.opts.ReloadTimeout)
			}
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: RPC server did not restart within %v after LoadState",
				ErrTimeout, c.opts.ReloadTimeout)
		case <-time.After(c.opts.PollInterval):
		}
	}
}

// SetInput applies in until the next SetInput (PROTOCOL.md §5.8).
func (c *Client) SetInput(ctx context.Context, in Input) error {
	if err := in.Validate(); err != nil {
		return fmt.Errorf("rpc: %w", err)
	}
	_, err := c.request(ctx, TypeSetInput, in.Encode())
	return err
}

// ReleaseInput releases all buttons, touch and circle pad.
func (c *Client) ReleaseInput(ctx context.Context) error {
	return c.SetInput(ctx, Input{})
}
