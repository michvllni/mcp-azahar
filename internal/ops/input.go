// SPDX-License-Identifier: GPL-3.0-or-later

package ops

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/michvllni/mcp-azahar/internal/rpc"
	"github.com/michvllni/mcp-azahar/internal/session"
)

// DefaultPress is how long press holds buttons: at least two frames and
// about 23 HID updates (234 Hz in emulated time, PROTOCOL.md §5.8; fewer if
// emulation runs slower than real time).
const DefaultPress = 100 * time.Millisecond

// restoreTimeout bounds restoring the held input after a press, which runs
// even if the caller's context has ended.
const restoreTimeout = 10 * time.Second

// ParseButtons parses button names; "home" and "power" set the flags.
func ParseButtons(names []string) (rpc.Input, error) {
	var in rpc.Input
	for _, n := range names {
		switch strings.ToUpper(strings.TrimSpace(n)) {
		case "HOME":
			in.Home = true
		case "POWER":
			in.Power = true
		default:
			b, err := rpc.ParseButton(n)
			if err != nil {
				return in, usage("%v", err)
			}
			in.Buttons |= b
		}
	}
	return in, nil
}

// InputResult reports the input state after an input operation.
type InputResult struct {
	// Held is what stays held after the operation.
	Held InputView `json:"held"`
}

// Press applies in on top of the held input for d (DefaultPress if 0),
// then sets the held input again. The held input is re-read from the
// session file at the end, so a hold made meanwhile is kept; if anything
// fails or ctx ends, the held input is still restored.
func (o *Ops) Press(ctx context.Context, name string, in rpc.Input, d time.Duration) (*InputResult, error) {
	if in.IsZero() {
		return nil, usage("nothing to press")
	}
	if err := in.Validate(); err != nil {
		return nil, usage("%v", err)
	}
	if d < 0 {
		return nil, usage("negative duration %v", d)
	}
	if d == 0 {
		d = DefaultPress
	}
	l := o.locksFor(name)
	l.input.Lock()
	defer l.input.Unlock()
	s, c, err := o.client(ctx, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()

	pressErr := c.SetInput(ctx, s.Held.Merge(in))
	if pressErr == nil {
		select {
		case <-ctx.Done():
			pressErr = ctx.Err()
		case <-time.After(d):
		}
	}
	// Restore in every case: a request whose reply was lost may still have
	// pressed the buttons in Azahar.
	held, restoreErr := o.applyHeld(ctx, name, c, s.Held)
	if err := errors.Join(pressErr, restoreErr); err != nil {
		return nil, err
	}
	return &InputResult{Held: ViewInput(held)}, nil
}

// applyHeld sends the session's current held input to Azahar, under the
// store lock, even if ctx has ended. If the session file cannot be used
// (stopped meanwhile, lock not available), it still sends fallback, or
// releases everything if the session is gone, so no press stays stuck.
func (o *Ops) applyHeld(ctx context.Context, name string, c *rpc.Client, fallback rpc.Input) (rpc.Input, error) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), restoreTimeout)
	defer cancel()
	var held rpc.Input
	sent := false
	_, err := o.M.Update(rctx, name, func(s *session.Session) error {
		held, sent = s.Held, true
		return c.SetInput(rctx, held)
	})
	if err == nil || sent {
		return held, err
	}
	held = fallback
	if errors.Is(err, session.ErrNotFound) {
		held = rpc.Input{}
	}
	return held, errors.Join(err, c.SetInput(rctx, held))
}

// Hold adds in to the held input until Release.
func (o *Ops) Hold(ctx context.Context, name string, in rpc.Input) (*InputResult, error) {
	if in.IsZero() {
		return nil, usage("nothing to hold")
	}
	if err := in.Validate(); err != nil {
		return nil, usage("%v", err)
	}
	return o.setHeld(ctx, name, func(h rpc.Input) rpc.Input { return h.Merge(in) })
}

// ReleaseWhat selects what Release lets go of; the zero value releases
// everything.
type ReleaseWhat struct {
	// Buttons (and HOME/POWER flags) to release.
	Buttons rpc.Input
	// Touch and CirclePad release the held touch or circle pad position.
	Touch, CirclePad bool
}

// Release lets go of held input: everything if what is empty.
func (o *Ops) Release(ctx context.Context, name string, what ReleaseWhat) (*InputResult, error) {
	all := what.Buttons.IsZero() && !what.Touch && !what.CirclePad
	return o.setHeld(ctx, name, func(h rpc.Input) rpc.Input {
		if all {
			return rpc.Input{}
		}
		h.Buttons &^= what.Buttons.Buttons
		h.Home = h.Home && !what.Buttons.Home
		h.Power = h.Power && !what.Buttons.Power
		if what.Touch {
			h.Touch = nil
		}
		if what.CirclePad {
			h.CirclePad = nil
		}
		return h
	})
}

// setHeld changes the held input in the session file and in Azahar.
func (o *Ops) setHeld(ctx context.Context, name string, fn func(rpc.Input) rpc.Input) (*InputResult, error) {
	l := o.locksFor(name)
	l.input.Lock()
	defer l.input.Unlock()
	_, c, err := o.client(ctx, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	var held rpc.Input
	_, err = o.M.Update(ctx, name, func(s *session.Session) error {
		held = fn(s.Held)
		if err := c.SetInput(ctx, held); err != nil {
			return err
		}
		s.Held = held
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &InputResult{Held: ViewInput(held)}, nil
}

// Touch touches the bottom screen at (x, y): briefly (d, DefaultPress if
// 0), or held until Release if hold.
func (o *Ops) Touch(ctx context.Context, name string, x, y int, hold bool, d time.Duration) (*InputResult, error) {
	if x < 0 || y < 0 || x > rpc.TouchMaxX || y > rpc.TouchMaxY {
		return nil, usage("touch (%d, %d) outside the bottom screen (0..%d, 0..%d)", x, y, rpc.TouchMaxX, rpc.TouchMaxY)
	}
	in := rpc.Input{Touch: &rpc.Point{X: uint16(x), Y: uint16(y)}}
	if hold {
		return o.Hold(ctx, name, in)
	}
	return o.Press(ctx, name, in, d)
}

// CirclePad moves the circle pad to (x, y) in -154..154 (0x9A): briefly
// or held until Release.
func (o *Ops) CirclePad(ctx context.Context, name string, x, y int, hold bool, d time.Duration) (*InputResult, error) {
	if x < -rpc.CirclePadMax || x > rpc.CirclePadMax || y < -rpc.CirclePadMax || y > rpc.CirclePadMax {
		return nil, usage("circle pad (%d, %d) outside -%d..%d", x, y, rpc.CirclePadMax, rpc.CirclePadMax)
	}
	in := rpc.Input{CirclePad: &rpc.Stick{X: int16(x), Y: int16(y)}}
	if hold {
		return o.Hold(ctx, name, in)
	}
	return o.Press(ctx, name, in, d)
}
