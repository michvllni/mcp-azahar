// SPDX-License-Identifier: GPL-3.0-or-later

// Package sequence runs scripted tests: a YAML (or JSON) file of steps such
// as press, touch, wait, screenshot, write, assert and wait_for, executed
// against a session through internal/ops. A sequence stops at the first
// failing step; failed assertions are check failures (exit code 3).
package sequence

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/michvllni/mcp-azahar/internal/capture"
	"github.com/michvllni/mcp-azahar/internal/ops"
	"github.com/michvllni/mcp-azahar/internal/symbols"
)

// File is a parsed sequence.
type File struct {
	// Name describes the sequence (optional).
	Name string `yaml:"name"`
	// ELF overrides the session's ELF for symbol steps (optional, relative
	// to the file).
	ELF   string `yaml:"elf"`
	Steps []Step `yaml:"steps"`
}

// Duration parses "100ms", "2s" etc.
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: duration %q: use e.g. 100ms or 2s", n.Line, n.Value)
	}
	if v < 0 {
		return fmt.Errorf("line %d: negative duration %q", n.Line, n.Value)
	}
	*d = Duration(v)
	return nil
}

// Buttons is a list of button names: "A", "A B" or [A, B].
type Buttons []string

// UnmarshalYAML implements yaml.Unmarshaler.
func (b *Buttons) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		*b = strings.Fields(n.Value)
		return nil
	case yaml.SequenceNode:
		var list []string
		if err := n.Decode(&list); err != nil {
			return err
		}
		*b = list
		return nil
	}
	return fmt.Errorf("line %d: buttons must be a name, names separated by spaces, or a list", n.Line)
}

// Scalar is any YAML scalar kept as its text (numbers, booleans, strings).
type Scalar string

// UnmarshalYAML implements yaml.Unmarshaler.
func (s *Scalar) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: expected a single value", n.Line)
	}
	*s = Scalar(n.Value)
	return nil
}

// XY is a touch point or circle pad position.
type XY struct {
	X    int      `yaml:"x"`
	Y    int      `yaml:"y"`
	For  Duration `yaml:"for"`
	Hold bool     `yaml:"hold"`
}

// Check compares a target (symbol expression or address) with a value.
type Check struct {
	// Target is "test_state.last_buttons" or an address like "0x00101000".
	Target string `yaml:"target"`
	// Op is eq (default), ne, lt, le, gt, ge or changed (wait_for only).
	Op    string `yaml:"op"`
	Value Scalar `yaml:"value"`
	// Type and Len as for read-symbol (needed for plain addresses).
	Type string `yaml:"type"`
	Len  int    `yaml:"len"`
	// Timeout for wait_for (default 10s); Interval between polls.
	Timeout  Duration `yaml:"timeout"`
	Interval Duration `yaml:"interval"`
}

// Step is one action; exactly one action field is set.
type Step struct {
	// Name labels the step in reports (optional).
	Name string `yaml:"name"`

	Press     *Buttons  `yaml:"press"`
	For       Duration  `yaml:"for"` // press duration
	Hold      *Buttons  `yaml:"hold"`
	Release   *Buttons  `yaml:"release"` // [] or "all" releases everything
	Touch     *XY       `yaml:"touch"`
	CirclePad *XY       `yaml:"circle_pad"`
	Wait      *Duration `yaml:"wait"`

	Screenshot *struct {
		Screen string `yaml:"screen"`
		Output string `yaml:"output"`
		Scale  int    `yaml:"scale"`
		Split  bool   `yaml:"split"`
	} `yaml:"screenshot"`

	Write *struct {
		Address string `yaml:"address"`
		Bytes   string `yaml:"bytes"`
	} `yaml:"write"`

	Assert  *Check `yaml:"assert"`
	WaitFor *Check `yaml:"wait_for"`

	Save *int `yaml:"save_state"`
	Load *int `yaml:"load_state"`

	LogContains *struct {
		Text    string   `yaml:"text"`
		Timeout Duration `yaml:"timeout"`
	} `yaml:"log_contains"`
}

// action returns the step's action name, or an error unless exactly one
// action is set.
func (s *Step) action() (string, error) {
	set := []string{}
	for name, ok := range map[string]bool{
		"press": s.Press != nil, "hold": s.Hold != nil, "release": s.Release != nil,
		"touch": s.Touch != nil, "circle_pad": s.CirclePad != nil, "wait": s.Wait != nil,
		"screenshot": s.Screenshot != nil, "write": s.Write != nil, "assert": s.Assert != nil,
		"wait_for": s.WaitFor != nil, "save_state": s.Save != nil, "load_state": s.Load != nil,
		"log_contains": s.LogContains != nil,
	} {
		if ok {
			set = append(set, name)
		}
	}
	switch len(set) {
	case 1:
		return set[0], nil
	case 0:
		return "", errors.New("no action (press, hold, release, touch, circle_pad, wait, screenshot, write, assert, wait_for, save_state, load_state, log_contains)")
	}
	return "", fmt.Errorf("more than one action: %s", strings.Join(set, ", "))
}

// Parse reads a sequence (YAML or JSON) and validates every step.
func Parse(r io.Reader) (*File, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var f File
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: empty sequence", ops.ErrUsage)
		}
		return nil, fmt.Errorf("%w: %w", ops.ErrUsage, err)
	}
	if len(f.Steps) == 0 {
		return nil, fmt.Errorf("%w: sequence has no steps", ops.ErrUsage)
	}
	for i := range f.Steps {
		if _, err := f.Steps[i].action(); err != nil {
			return nil, fmt.Errorf("%w: step %d: %w", ops.ErrUsage, i+1, err)
		}
	}
	return &f, nil
}

// StepResult reports one executed step.
type StepResult struct {
	Index    int    `json:"index"` // 1-based
	Name     string `json:"name,omitempty"`
	Action   string `json:"action"`
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Detail   any    `json:"detail,omitempty"`
	Duration string `json:"duration"`
}

// Report is the result of a run.
type Report struct {
	Name   string       `json:"name,omitempty"`
	Passed bool         `json:"passed"`
	Steps  []StepResult `json:"steps"`
	// Skipped counts steps not run after a failure.
	Skipped int `json:"skipped"`
}

// Options for Run.
type Options struct {
	// Dir resolves relative paths (screenshots, elf); usually the
	// sequence file's directory.
	Dir string
	// ELF overrides the file's and the session's ELF.
	ELF string
	// OnStep is called after each step (for progress output).
	OnStep func(StepResult)
}

// Run executes the steps against a session. It returns the report and, if
// a step failed, an error wrapping ops.ErrCheckFailed (assertions) or the
// step's error.
func Run(ctx context.Context, o *ops.Ops, session string, f *File, opt Options) (*Report, error) {
	elf := opt.ELF
	if elf == "" && f.ELF != "" {
		elf = rel(opt.Dir, f.ELF)
	}
	rep := &Report{Name: f.Name, Passed: true, Steps: []StepResult{}}
	for i := range f.Steps {
		st := &f.Steps[i]
		action, _ := st.action()
		start := time.Now()
		detail, err := runStep(ctx, o, session, st, action, elf, opt.Dir)
		res := StepResult{Index: i + 1, Name: st.Name, Action: action, OK: err == nil, Detail: detail,
			Duration: time.Since(start).Round(time.Millisecond).String()}
		if err != nil {
			res.Error = err.Error()
		}
		rep.Steps = append(rep.Steps, res)
		if opt.OnStep != nil {
			opt.OnStep(res)
		}
		if err != nil {
			rep.Passed = false
			rep.Skipped = len(f.Steps) - i - 1
			label := action
			if st.Name != "" {
				label = fmt.Sprintf("%s %q", action, st.Name)
			}
			if errors.Is(err, ops.ErrUsage) && !errors.Is(err, ops.ErrCheckFailed) {
				// The file was valid; a step failing at run time is an
				// error (exit 1), not a usage problem (exit 2).
				return rep, fmt.Errorf("step %d (%s): %s", i+1, label, err.Error())
			}
			return rep, fmt.Errorf("step %d (%s): %w", i+1, label, err)
		}
	}
	return rep, nil
}

func rel(dir, p string) string {
	if p == "" || filepath.IsAbs(p) || dir == "" {
		return p
	}
	return filepath.Join(dir, p)
}

func runStep(ctx context.Context, o *ops.Ops, session string, st *Step, action, elf, dir string) (any, error) {
	switch action {
	case "press":
		in, err := ops.ParseButtons(*st.Press)
		if err != nil {
			return nil, err
		}
		return o.Press(ctx, session, in, time.Duration(st.For))
	case "hold":
		in, err := ops.ParseButtons(*st.Hold)
		if err != nil {
			return nil, err
		}
		return o.Hold(ctx, session, in)
	case "release":
		names := []string(*st.Release)
		if len(names) == 1 && strings.EqualFold(names[0], "all") {
			names = nil
		}
		in, err := ops.ParseButtons(names)
		if err != nil {
			return nil, err
		}
		return o.Release(ctx, session, ops.ReleaseWhat{Buttons: in})
	case "touch":
		return o.Touch(ctx, session, st.Touch.X, st.Touch.Y, st.Touch.Hold, time.Duration(st.Touch.For))
	case "circle_pad":
		return o.CirclePad(ctx, session, st.CirclePad.X, st.CirclePad.Y, st.CirclePad.Hold, time.Duration(st.CirclePad.For))
	case "wait":
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(*st.Wait)):
		}
		return nil, nil
	case "screenshot":
		s := st.Screenshot
		sc, err := capture.ParseScreens(s.Screen)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ops.ErrUsage, err)
		}
		if s.Output == "" {
			return nil, fmt.Errorf("%w: screenshot needs output", ops.ErrUsage)
		}
		shots, err := o.Screenshot(ctx, session, capture.Request{Screens: sc, Scale: s.Scale})
		if err != nil {
			return nil, err
		}
		return ops.SaveScreenshot(shots, rel(dir, s.Output), s.Split)
	case "write":
		addr, err := ops.ParseAddress(st.Write.Address)
		if err != nil {
			return nil, err
		}
		b, err := ops.ParseBytes(st.Write.Bytes)
		if err != nil {
			return nil, err
		}
		return nil, o.WriteMemory(ctx, session, addr, b)
	case "assert":
		c := st.Assert
		if c.Op == "changed" {
			return nil, fmt.Errorf("%w: assert cannot use changed; use wait_for", ops.ErrUsage)
		}
		v, err := o.ReadValue(ctx, session, c.Target, valueOpts(c, elf))
		if err != nil {
			return nil, err
		}
		cond := ops.Condition{Op: c.Op, Value: string(c.Value)}
		if cond.Op == "" {
			cond.Op = "eq"
		}
		ok, err := ops.Compare(v.Value, v.Value, cond)
		if err != nil {
			return v, err
		}
		if !ok {
			return v, fmt.Errorf("%w: %s is %s, want %s %s", ops.ErrCheckFailed, c.Target, showValue(v.Value), cond.Op, cond.Value)
		}
		return v, nil
	case "wait_for":
		c := st.WaitFor
		timeout := time.Duration(c.Timeout)
		if timeout == 0 {
			timeout = ops.DefaultWait
		}
		wctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return o.WaitFor(wctx, session, c.Target, ops.Condition{Op: c.Op, Value: string(c.Value)}, valueOpts(c, elf), time.Duration(c.Interval))
	case "save_state":
		return nil, o.SaveState(ctx, session, *st.Save)
	case "load_state":
		return nil, o.LoadState(ctx, session, *st.Load)
	case "log_contains":
		return logContains(ctx, o, session, st.LogContains.Text, time.Duration(st.LogContains.Timeout))
	}
	return nil, fmt.Errorf("%w: unknown action %q", ops.ErrUsage, action)
}

func valueOpts(c *Check, elf string) ops.ValueOptions {
	return ops.ValueOptions{ELF: elf, Type: symbols.Scalar(c.Type), Len: c.Len}
}

func showValue(v any) string {
	var b bytes.Buffer
	_, _ = fmt.Fprintf(&b, "%v", v)
	return b.String()
}

// logContains waits until the session log has a line containing text.
func logContains(ctx context.Context, o *ops.Ops, session, text string, timeout time.Duration) (any, error) {
	if text == "" {
		return nil, fmt.Errorf("%w: log_contains needs text", ops.ErrUsage)
	}
	if timeout == 0 {
		timeout = ops.DefaultWait
	}
	deadline := time.Now().Add(timeout)
	for {
		res, err := o.Logs(ctx, session, ops.LogQuery{Contains: text, Tail: 1})
		if err != nil {
			return nil, err
		}
		if len(res.Lines) > 0 {
			return map[string]string{"line": res.Lines[0]}, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w: no log line contains %q within %v", ops.ErrCheckFailed, text, timeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
