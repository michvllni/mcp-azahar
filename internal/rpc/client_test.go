// SPDX-License-Identifier: GPL-3.0-or-later

package rpc_test

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"sync"
	"testing"
	"time"

	"github.com/michvllni/mcp-azahar/internal/rpc"
	"github.com/michvllni/mcp-azahar/internal/rpc/fakeserver"
)

// setup starts a fake server and a client with short timeouts.
func setup(t *testing.T, cfg fakeserver.Config, opts rpc.Options) (*fakeserver.Server, *rpc.Client) {
	t.Helper()
	srv, err := fakeserver.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	if opts.Timeout == 0 {
		opts.Timeout = 100 * time.Millisecond
	}
	c, err := rpc.Dial(context.Background(), rpc.UDPDialer(srv.Addr()), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return srv, c
}

func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i/251)
	}
	return b
}

func TestReadWriteMemory(t *testing.T) {
	srv, c := setup(t, fakeserver.Config{}, rpc.Options{})
	ctx := context.Background()
	srv.SetMemory(0x00100000, []byte{0xFE, 0xFF, 0xFF, 0xEA})
	got, err := c.ReadMemory(ctx, 0x00100000, 4)
	if err != nil || !bytes.Equal(got, []byte{0xFE, 0xFF, 0xFF, 0xEA}) {
		t.Fatalf("ReadMemory = % x, %v", got, err)
	}
	if err := c.WriteMemory(ctx, 0x00101000, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if got := srv.Memory(0x00101000, 3); !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Fatalf("memory = % x", got)
	}
	// Unmapped memory reads as zeros, like Azahar.
	if got, err := c.ReadMemory(ctx, 0x08000000, 4); err != nil || !bytes.Equal(got, make([]byte, 4)) {
		t.Fatalf("unmapped read = % x, %v", got, err)
	}
	if got, err := c.ReadMemory(ctx, 0x00100000, 0); err != nil || len(got) != 0 {
		t.Fatalf("zero-length read = % x, %v", got, err)
	}
}

func TestMemoryChunking(t *testing.T) {
	srv, c := setup(t, fakeserver.Config{}, rpc.Options{})
	ctx := context.Background()
	data := pattern(2*rpc.MaxWriteSize + 100) // 3 write chunks, 2 read chunks + rest
	if err := c.WriteMemory(ctx, 0x08000000, data); err != nil {
		t.Fatal(err)
	}
	if n := srv.CountRequests(rpc.TypeWriteMemory); n != 3 {
		t.Errorf("WriteMemory requests = %d, want 3", n)
	}
	got, err := c.ReadMemory(ctx, 0x08000000, len(data))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read back %d bytes, err %v, equal %v", len(got), err, bytes.Equal(got, data))
	}
	if n := srv.CountRequests(rpc.TypeReadMemory); n != 3 {
		t.Errorf("ReadMemory requests = %d, want 3", n)
	}
	// Exactly MaxReadSize is one request.
	if _, err := c.ReadMemory(ctx, 0x08000000, rpc.MaxReadSize); err != nil {
		t.Fatal(err)
	}
	if n := srv.CountRequests(rpc.TypeReadMemory); n != 4 {
		t.Errorf("ReadMemory requests = %d, want 4", n)
	}
}

func TestAddressRangeOverflow(t *testing.T) {
	_, c := setup(t, fakeserver.Config{}, rpc.Options{})
	if _, err := c.ReadMemory(context.Background(), 0xFFFFFFF0, 0x20); err == nil {
		t.Fatal("read past 4 GiB accepted")
	}
	if err := c.WriteMemory(context.Background(), 0xFFFFFFFF, []byte{1, 2}); err == nil {
		t.Fatal("write past 4 GiB accepted")
	}
	if _, err := c.ReadMemory(context.Background(), 0xFFFFFFFC, 4); err != nil {
		t.Fatalf("read up to the last byte rejected: %v", err)
	}
}

func TestWriteMemoryVerified(t *testing.T) {
	_, c := setup(t, fakeserver.Config{}, rpc.Options{})
	ctx := context.Background()
	if err := c.WriteMemoryVerified(ctx, 0x14000000, []byte{9, 8, 7}); err != nil {
		t.Fatalf("writable region: %v", err)
	}
	// Outside the writable regions Azahar replies normally but writes nothing.
	err := c.WriteMemoryVerified(ctx, 0x1FF80000, []byte{1})
	if !errors.Is(err, rpc.ErrWriteNotApplied) {
		t.Fatalf("err = %v, want ErrWriteNotApplied", err)
	}
}

func TestRetriesAfterDroppedRequests(t *testing.T) {
	srv, c := setup(t, fakeserver.Config{}, rpc.Options{Timeout: 50 * time.Millisecond})
	srv.DropNext(2)
	if _, err := c.ReadMemory(context.Background(), 0x00100000, 4); err != nil {
		t.Fatalf("ReadMemory after 2 drops: %v", err)
	}
	if n := srv.CountRequests(rpc.TypeReadMemory); n != 3 {
		t.Errorf("requests = %d, want 3 (1 + 2 resends)", n)
	}
}

func TestTimeoutAfterAllRetries(t *testing.T) {
	srv, c := setup(t, fakeserver.Config{}, rpc.Options{Timeout: 30 * time.Millisecond, Retries: 2})
	srv.DropNext(100)
	_, err := c.ReadMemory(context.Background(), 0x00100000, 4)
	if !errors.Is(err, rpc.ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if n := srv.CountRequests(rpc.TypeReadMemory); n != 3 {
		t.Errorf("requests = %d, want 3", n)
	}
}

func TestNoRetriesOption(t *testing.T) {
	srv, c := setup(t, fakeserver.Config{}, rpc.Options{Timeout: 30 * time.Millisecond, Retries: -1})
	srv.DropNext(100)
	if _, err := c.Processes(context.Background()); !errors.Is(err, rpc.ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
	if n := srv.CountRequests(rpc.TypeProcessList); n != 1 {
		t.Errorf("requests = %d, want 1", n)
	}
}

func TestNonIdempotentNotResent(t *testing.T) {
	srv, c := setup(t, fakeserver.Config{}, rpc.Options{
		Timeout: 30 * time.Millisecond, StateTimeout: 100 * time.Millisecond, ScreenshotTimeout: 100 * time.Millisecond,
	})
	ctx := context.Background()
	srv.DropNext(1)
	if err := c.SaveState(ctx, 1); !errors.Is(err, rpc.ErrTimeout) {
		t.Fatalf("SaveState err = %v", err)
	}
	srv.DropNext(1)
	if _, err := c.Screenshot(ctx, rpc.ScreenshotRequest{}); !errors.Is(err, rpc.ErrTimeout) {
		t.Fatalf("Screenshot err = %v", err)
	}
	if n := srv.CountRequests(rpc.TypeSaveState); n != 1 {
		t.Errorf("SaveState sent %d times", n)
	}
	if n := srv.CountRequests(rpc.TypeTakeScreenshot); n != 1 {
		t.Errorf("TakeScreenshot sent %d times", n)
	}
}

func TestStaleAndMalformedRepliesIgnored(t *testing.T) {
	srv, c := setup(t, fakeserver.Config{}, rpc.Options{})
	srv.SendStaleReplies(true)
	srv.SetMemory(0x00100000, []byte{0xAA, 0xBB})
	got, err := c.ReadMemory(context.Background(), 0x00100000, 2)
	if err != nil || !bytes.Equal(got, []byte{0xAA, 0xBB}) {
		t.Fatalf("got % x, %v", got, err)
	}
}

func TestContextCancellation(t *testing.T) {
	srv, c := setup(t, fakeserver.Config{}, rpc.Options{Timeout: 5 * time.Second})
	srv.DropNext(100)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	_, err := c.ReadMemory(ctx, 0x00100000, 4)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("cancellation took %v", d)
	}
	// The client stays usable after a cancelled request.
	srv.DropNext(0)
	if _, err := c.ReadMemory(context.Background(), 0x00100000, 4); err != nil {
		t.Fatalf("after cancel: %v", err)
	}
}

func TestContextDeadlineShortensAttempt(t *testing.T) {
	srv, c := setup(t, fakeserver.Config{}, rpc.Options{Timeout: 5 * time.Second})
	srv.DropNext(100)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.ReadMemory(ctx, 0x00100000, 4); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestProcesses(t *testing.T) {
	procs := []rpc.Process{{PID: 2, Name: "fs"}, {PID: 11, TitleID: 0x0004000000ABCD00, Name: "game"}}
	srv, c := setup(t, fakeserver.Config{Processes: procs}, rpc.Options{})
	ctx := context.Background()
	got, err := c.Processes(ctx)
	if err != nil || len(got) != 2 || got[1] != procs[1] {
		t.Fatalf("Processes = %+v, %v", got, err)
	}
	if pid, err := c.Process(ctx); err != nil || pid != rpc.NoProcess {
		t.Fatalf("Process = %#x, %v", pid, err)
	}
	if err := c.SelectProcess(ctx, 11); err != nil {
		t.Fatal(err)
	}
	if pid, _ := c.Process(ctx); pid != 11 || srv.Selected() != 11 {
		t.Fatalf("selected %d / %d", pid, srv.Selected())
	}
	// A selected process that does not exist makes reads fail.
	if err := c.SelectProcess(ctx, 99); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadMemory(ctx, 0x00100000, 4); !errors.Is(err, rpc.ErrRejected) {
		t.Fatalf("err = %v, want ErrRejected", err)
	}
}

func TestScreenshot(t *testing.T) {
	_, c := setup(t, fakeserver.Config{}, rpc.Options{})
	ctx := context.Background()
	for _, tc := range []struct {
		req  rpc.ScreenshotRequest
		w, h int
	}{
		{rpc.ScreenshotRequest{Screen: rpc.ScreenTop}, 400, 240},
		{rpc.ScreenshotRequest{Screen: rpc.ScreenBottom}, 320, 240},
		{rpc.ScreenshotRequest{Screen: rpc.ScreenBottom, Scale: 2}, 640, 480},
	} {
		img, err := c.Screenshot(ctx, tc.req)
		if err != nil {
			t.Fatalf("%+v: %v", tc.req, err)
		}
		if img.Width != tc.w || img.Height != tc.h || img.Format != rpc.FormatPNG {
			t.Fatalf("%+v: got %dx%d format %v", tc.req, img.Width, img.Height, img.Format)
		}
		p, err := png.Decode(bytes.NewReader(img.Data))
		if err != nil || p.Bounds().Dx() != tc.w || p.Bounds().Dy() != tc.h {
			t.Fatalf("%+v: PNG %v, %v", tc.req, p.Bounds(), err)
		}
	}
	// Raw at scale 3 is larger than one ReadScreenshot chunk.
	img, err := c.Screenshot(ctx, rpc.ScreenshotRequest{Scale: 3, Raw: true})
	if err != nil {
		t.Fatal(err)
	}
	if img.Format != rpc.FormatRGB || len(img.Data) != 1200*720*3 || img.Data[0] != 255 || img.Data[2] != 0 {
		t.Fatalf("raw: %dx%d, %d bytes, first pixel % x", img.Width, img.Height, len(img.Data), img.Data[:3])
	}
	if _, err := c.Screenshot(ctx, rpc.ScreenshotRequest{Scale: 11}); err == nil {
		t.Fatal("scale 11 accepted")
	}
}

func TestScreenshotErrors(t *testing.T) {
	srv, c := setup(t, fakeserver.Config{}, rpc.Options{})
	srv.SetScreenshotResult(rpc.ScreenshotUnsupported)
	_, err := c.Screenshot(context.Background(), rpc.ScreenshotRequest{})
	var se *rpc.ScreenshotError
	if !errors.As(err, &se) || se.Result != rpc.ScreenshotUnsupported {
		t.Fatalf("err = %v", err)
	}
}

func TestScreenshotWaitsForCapture(t *testing.T) {
	srv, c := setup(t, fakeserver.Config{}, rpc.Options{ScreenshotTimeout: 2 * time.Second})
	srv.SetScreenshotDelay(200 * time.Millisecond)
	img, err := c.Screenshot(context.Background(), rpc.ScreenshotRequest{Screen: rpc.ScreenBottom, Raw: true})
	if err != nil || img.Width != 320 {
		t.Fatalf("got %v, %v", img, err)
	}
}

func TestPerfStats(t *testing.T) {
	_, c := setup(t, fakeserver.Config{}, rpc.Options{})
	s, err := c.PerfStats(context.Background(), true)
	if err != nil || s.SystemFPS != 60 || s.EmulationSpeed != 1 {
		t.Fatalf("PerfStats = %+v, %v", s, err)
	}
}

func TestSaveLoadState(t *testing.T) {
	srv, c := setup(t, fakeserver.Config{}, rpc.Options{PollInterval: 10 * time.Millisecond})
	ctx := context.Background()
	if err := c.SelectProcess(ctx, 11); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteMemory(ctx, 0x00101000, []byte("old!")); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveState(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteMemory(ctx, 0x00101000, []byte("new!")); err != nil {
		t.Fatal(err)
	}
	// The fake drops the first request after the load and resets the
	// selection, like Azahar recreating its RPC server.
	if err := c.LoadState(ctx, 1); err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if srv.Selected() != 11 {
		t.Fatalf("process not re-selected: %#x", srv.Selected())
	}
	if got, err := c.ReadMemory(ctx, 0x00101000, 4); err != nil || string(got) != "old!" {
		t.Fatalf("after load: %q, %v", got, err)
	}
	if err := c.LoadState(ctx, 5); !errors.Is(err, rpc.ErrNotAccepted) {
		t.Fatalf("missing slot: %v", err)
	}
	if err := c.SaveState(ctx, rpc.SaveStateSlots); err == nil {
		t.Fatal("slot 11 accepted")
	}
}

func TestLoadStateWithoutSelectionReturnsImmediately(t *testing.T) {
	srv, c := setup(t, fakeserver.Config{}, rpc.Options{})
	ctx := context.Background()
	if err := c.SaveState(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.LoadState(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if n := srv.CountRequests(rpc.TypeSetGetProcess); n != 0 {
		t.Errorf("polled %d times without a selected process", n)
	}
}

func TestLoadStateReloadTimeout(t *testing.T) {
	_, c := setup(t, fakeserver.Config{NoRestartOnLoad: true}, rpc.Options{
		Timeout: 20 * time.Millisecond, ReloadTimeout: 150 * time.Millisecond, PollInterval: 10 * time.Millisecond,
	})
	ctx := context.Background()
	if err := c.SelectProcess(ctx, 11); err != nil {
		t.Fatal(err)
	}
	if err := c.SaveState(ctx, 2); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err := c.LoadState(ctx, 2)
	if !errors.Is(err, rpc.ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout (server never restarted)", err)
	}
	if d := time.Since(start); d < 150*time.Millisecond || d > 2*time.Second {
		t.Fatalf("gave up after %v, want about ReloadTimeout", d)
	}
}

func TestNotRunning(t *testing.T) {
	_, c := setup(t, fakeserver.Config{NotRunning: true}, rpc.Options{})
	if err := c.SaveState(context.Background(), 1); !errors.Is(err, rpc.ErrNotAccepted) {
		t.Fatalf("err = %v", err)
	}
}

func TestSetInput(t *testing.T) {
	srv, c := setup(t, fakeserver.Config{}, rpc.Options{})
	ctx := context.Background()
	in := rpc.Input{Buttons: rpc.ButtonA | rpc.ButtonStart, Touch: &rpc.Point{X: 160, Y: 120}, CirclePad: &rpc.Stick{X: -0x9A}}
	if err := c.SetInput(ctx, in); err != nil {
		t.Fatal(err)
	}
	got := srv.Input()
	if got.Buttons != in.Buttons || *got.Touch != *in.Touch || *got.CirclePad != *in.CirclePad {
		t.Fatalf("server got %+v", got)
	}
	if err := c.ReleaseInput(ctx); err != nil {
		t.Fatal(err)
	}
	if got := srv.Input(); got.Buttons != 0 || got.Touch != nil || got.CirclePad != nil {
		t.Fatalf("not released: %+v", got)
	}
	if err := c.SetInput(ctx, rpc.Input{Touch: &rpc.Point{X: 400}}); err == nil {
		t.Fatal("touch outside the screen accepted")
	}
}

func TestClosedClient(t *testing.T) {
	_, c := setup(t, fakeserver.Config{}, rpc.Options{})
	_ = c.Close()
	if _, err := c.ReadMemory(context.Background(), 0x00100000, 4); !errors.Is(err, rpc.ErrClosed) {
		t.Fatalf("err = %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestConcurrentUse(t *testing.T) {
	srv, c := setup(t, fakeserver.Config{}, rpc.Options{})
	srv.SetMemory(0x00100000, pattern(256))
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := c.ReadMemory(context.Background(), 0x00100000+uint32(i), 8)
			if err == nil && !bytes.Equal(got, pattern(256)[i:i+8]) {
				err = errors.New("wrong data")
			}
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestNoServer(t *testing.T) {
	c, err := rpc.Dial(context.Background(), rpc.UDPDialer("127.0.0.1:1"), rpc.Options{Timeout: 30 * time.Millisecond, Retries: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	// Nothing listens: either a timeout or "connection refused" (ICMP).
	if _, err := c.ReadMemory(context.Background(), 0, 4); err == nil {
		t.Fatal("expected an error without a server")
	}
}
