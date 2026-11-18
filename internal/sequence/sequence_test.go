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

// logFile is the fake session log of the last setup.
var logFile string

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
	logFile = filepath.Join(t.TempDir(), "azahar_log.txt")
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
		"bad button":    "steps:\n  - press: A\n  - press: TURBO\n",
		"no buttons":    "steps:\n  - press: []\n",
		"for on hold":   "steps:\n  - hold: A\n    for: 1s\n",
		"hold and for":  "steps:\n  - touch: {x: 1, y: 1, hold: true, for: 1s}\n",
		"touch range":   "steps:\n  - touch: {x: 320, y: 0}\n",
		"pad range":     "steps:\n  - circle_pad: {x: 0, y: -200}\n",
		"no output":     "steps:\n  - screenshot: {screen: top}\n",
		"bad screen":    "steps:\n  - screenshot: {screen: left, output: a.png}\n",
		"bad address":   "steps:\n  - write: {address: zz, bytes: \"00\"}\n",
		"bad bytes":     "steps:\n  - write: {address: \"0x100\", bytes: \"0g\"}\n",
		"no target":     "steps:\n  - assert: {value: 1}\n",
		"no value":      "steps:\n  - assert: {target: x}\n",
		"bad op":        "steps:\n  - assert: {target: x, op: approx, value: 1}\n",
		"assert change": "steps:\n  - assert: {target: x, op: changed}\n",
		"changed value": "steps:\n  - wait_for: {target: x, op: changed, value: 1}\n",
		"bad type":      "steps:\n  - assert: {target: x, type: u128, value: 1}\n",
		"bad slot":      "steps:\n  - save_state: 11\n",
		"no log text":   "steps:\n  - log_contains: {timeout: 1s}\n",
		"two docs":      "steps:\n  - press: A\n---\nsteps:\n  - press: B\n",
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
  - log_contains: {text: ready, timeout: 2s}
`)
	go func() { // only lines written during the run count
		time.Sleep(300 * time.Millisecond)
		appendLog(t, "app ready\n")
	}()
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

func appendLog(t *testing.T, s string) {
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Error(err)
		return
	}
	_, _ = f.WriteString(s)
	_ = f.Close()
}

func TestLogContainsAdvances(t *testing.T) {
	o, _, dir := setup(t)
	// "fixture ready" was logged before the run: it must not match. Each
	// match moves past its line, so the second step needs a second line.
	appendLog(t, "level 1 done\n")
	f := parse(t, `
steps:
  - log_contains: {text: done, timeout: 2s}
  - log_contains: {text: done, timeout: 2s}
`)
	go func() {
		time.Sleep(300 * time.Millisecond)
		appendLog(t, "level 2 done\nlevel 3 done\n")
	}()
	rep, err := sequence.Run(context.Background(), o, "t", f, sequence.Options{Dir: dir})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	l1 := rep.Steps[0].Detail.(map[string]string)["line"]
	l2 := rep.Steps[1].Detail.(map[string]string)["line"]
	if l1 != "level 2 done" || l2 != "level 3 done" {
		t.Fatalf("matched %q, %q", l1, l2)
	}
	f = parse(t, "steps:\n  - log_contains: {text: ready, timeout: 300ms}\n")
	if _, err := sequence.Run(context.Background(), o, "t", f, sequence.Options{Dir: dir}); !errors.Is(err, ops.ErrCheckFailed) {
		t.Fatalf("old line matched: %v", err)
	}
}

func TestRestrictPaths(t *testing.T) {
	o, _, dir := setup(t)
	for _, src := range []string{
		"steps:\n  - screenshot: {screen: top, output: ../out.png}\n",
		"steps:\n  - screenshot: {screen: top, output: /tmp/out.png}\n",
		"elf: ../x.elf\nsteps:\n  - press: A\n",
	} {
		rep, err := sequence.Run(context.Background(), o, "t", parse(t, src), sequence.Options{Dir: dir, RestrictPaths: true})
		if !errors.Is(err, ops.ErrUsage) && (rep == nil || rep.Passed) {
			t.Errorf("%q: %v", src, err)
		}
	}
	f := parse(t, "steps:\n  - screenshot: {screen: top, output: ok.png}\n")
	if _, err := sequence.Run(context.Background(), o, "t", f, sequence.Options{Dir: dir, RestrictPaths: true}); err != nil {
		t.Fatalf("local path: %v", err)
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
	if rep.Passed || len(rep.Steps) != 2 || rep.Skipped != 1 || rep.Steps[1].OK || rep.Steps[1].ErrorKind != "check" {
		t.Fatalf("report: %+v", rep)
	}
}

func TestRunErrorsAreNotCheckFailures(t *testing.T) {
	o, _, dir := setup(t)
	for name, src := range map[string]string{
		"unknown symbol": "steps:\n  - assert: {target: nope, value: 1}\n",
		"string compare": "steps:\n  - assert: {target: test_state.name, op: gt, value: a}\n",
		"unreadable elf": "elf: missing.elf\nsteps:\n  - assert: {target: counter, value: 1}\n",
	} {
		rep, err := sequence.Run(context.Background(), o, "t", parse(t, src), sequence.Options{Dir: dir})
		if err == nil || errors.Is(err, ops.ErrCheckFailed) || errors.Is(err, ops.ErrUsage) {
			t.Errorf("%s: %v, want a plain run-time error (exit 1)", name, err)
		}
		if rep != nil && len(rep.Steps) > 0 && rep.Steps[len(rep.Steps)-1].ErrorKind != "error" {
			t.Errorf("%s: kind %q", name, rep.Steps[len(rep.Steps)-1].ErrorKind)
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
