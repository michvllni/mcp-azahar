// SPDX-License-Identifier: GPL-3.0-or-later

package sequence_test

import (
	"context"
	"debug/elf"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michvllni/mcp-azahar/internal/emu"
	"github.com/michvllni/mcp-azahar/internal/ops"
	"github.com/michvllni/mcp-azahar/internal/rpc"
	"github.com/michvllni/mcp-azahar/internal/rpc/fakeserver"
	"github.com/michvllni/mcp-azahar/internal/sequence"
	"github.com/michvllni/mcp-azahar/internal/session"
)

var testELF, _ = filepath.Abs("../symbols/testdata/symbols.elf")

func setup(t *testing.T) (*ops.Ops, *fakeserver.Server, string) {
	t.Helper()
	srv, err := fakeserver.New(fakeserver.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	st, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	o := &ops.Ops{M: &emu.Manager{Store: st, RPCOptions: rpc.Options{Timeout: 200 * time.Millisecond}}}
	logFile := filepath.Join(t.TempDir(), "azahar_log.txt")
	_ = os.WriteFile(logFile, []byte("boot\nfixture ready\n"), 0o600)
	if _, err := o.Attach(context.Background(), "t", emu.AttachOptions{Address: srv.Addr(), ELF: testELF, LogPath: logFile}); err != nil {
		t.Fatal(err)
	}
	f, err := elf.Open(testELF)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for _, s := range f.Sections {
		if s.Type == elf.SHT_PROGBITS && s.Addr != 0 {
			b, _ := s.Data()
			srv.SetMemory(uint32(s.Addr), b)
		}
	}
	return o, srv, t.TempDir()
}

func parse(t *testing.T, src string) *sequence.File {
	t.Helper()
	f, err := sequence.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestParseErrors(t *testing.T) {
	for name, src := range map[string]string{
		"empty":         ``,
		"no steps":      `name: x`,
		"two actions":   "steps:\n  - press: A\n    hold: B\n",
		"no action":     "steps:\n  - name: nothing\n",
		"unknown key":   "steps:\n  - presss: A\n",
		"bad duration":  "steps:\n  - wait: soon\n",
		"neg duration":  "steps:\n  - wait: -1s\n",
		"bad buttons":   "steps:\n  - press: {a: 1}\n",
		"value as list": "steps:\n  - assert: {target: x, value: [1]}\n",
	} {
		if _, err := sequence.Parse(strings.NewReader(src)); !errors.Is(err, ops.ErrUsage) {
			t.Errorf("%s: %v, want ErrUsage", name, err)
		}
	}
}

func TestParseJSON(t *testing.T) {
	f := parse(t, `{"steps": [{"press": ["A", "B"], "for": "50ms"}, {"wait": "10ms"}]}`)
	if len(f.Steps) != 2 || len(*f.Steps[0].Press) != 2 || time.Duration(f.Steps[0].For) != 50*time.Millisecond {
		t.Fatalf("JSON: %+v", f.Steps)
	}
}

func TestRunPasses(t *testing.T) {
	o, srv, dir := setup(t)
	f := parse(t, `
name: smoke
steps:
  - press: A
    for: 20ms
  - hold: L R
  - name: let go of L
    release: [L]
  - touch: {x: 160, y: 120, for: 10ms}
  - circle_pad: {x: 154, y: 0, hold: true}
  - release: all
  - wait: 10ms
  - write: {address: "0x00101060", bytes: "05 00 00 00"}
  - assert: {target: test_state.last_buttons, value: 5}
  - assert: {target: test_state.touch.x, op: gt, value: 100}
  - assert: {target: "0x00101080", type: u8, value: 3}
  - wait_for: {target: test_state.mode, value: MODE_RUN, timeout: 1s}
  - screenshot: {screen: both, output: out.png}
  - save_state: 1
  - load_state: 1
  - log_contains: {text: ready, timeout: 1s}
`)
	var seen []int
	rep, err := sequence.Run(context.Background(), o, "t", f, sequence.Options{Dir: dir, OnStep: func(s sequence.StepResult) { seen = append(seen, s.Index) }})
	if err != nil {
		t.Fatalf("run: %v\n%+v", err, rep)
	}
	if !rep.Passed || len(rep.Steps) != 16 || len(seen) != 16 || rep.Steps[2].Name != "let go of L" {
		t.Fatalf("report: %+v", rep)
	}
	if !srv.Input().IsZero() {
		t.Fatalf("input not released: %+v", srv.Input())
	}
	if _, err := os.Stat(filepath.Join(dir, "out.png")); err != nil {
		t.Fatalf("screenshot not written relative to the file: %v", err)
	}
}

func TestRunStopsAtFailedAssert(t *testing.T) {
	o, _, dir := setup(t)
	f := parse(t, `
steps:
  - press: A
  - name: wrong frame
    assert: {target: test_state.frame, value: 8}
  - press: B
`)
	rep, err := sequence.Run(context.Background(), o, "t", f, sequence.Options{Dir: dir})
	if !errors.Is(err, ops.ErrCheckFailed) || !strings.Contains(err.Error(), `step 2 (assert "wrong frame")`) || !strings.Contains(err.Error(), "is 7, want eq 8") {
		t.Fatalf("err = %v", err)
	}
	if rep.Passed || len(rep.Steps) != 2 || rep.Skipped != 1 || rep.Steps[1].OK {
		t.Fatalf("report: %+v", rep)
	}
}

func TestRunErrorsAreNotCheckFailures(t *testing.T) {
	o, _, dir := setup(t)
	for name, src := range map[string]string{
		"unknown symbol":   "steps:\n  - assert: {target: nope, value: 1}\n",
		"bad button":       "steps:\n  - press: TURBO\n",
		"changed assert":   "steps:\n  - assert: {target: counter, op: changed}\n",
		"no screenshot to": "steps:\n  - screenshot: {screen: top}\n",
	} {
		_, err := sequence.Run(context.Background(), o, "t", parse(t, src), sequence.Options{Dir: dir})
		if err == nil || errors.Is(err, ops.ErrCheckFailed) || errors.Is(err, ops.ErrUsage) {
			t.Errorf("%s: %v, want a plain run-time error (exit 1)", name, err)
		}
	}
	f := parse(t, "steps:\n  - log_contains: {text: never, timeout: 300ms}\n")
	if _, err := sequence.Run(context.Background(), o, "t", f, sequence.Options{Dir: dir}); !errors.Is(err, ops.ErrCheckFailed) {
		t.Errorf("log_contains timeout: %v", err)
	}
	f = parse(t, "steps:\n  - wait_for: {target: counter, value: 99, timeout: 200ms}\n")
	if _, err := sequence.Run(context.Background(), o, "t", f, sequence.Options{Dir: dir}); !errors.Is(err, ops.ErrCheckFailed) {
		t.Errorf("wait_for timeout: %v", err)
	}
}
