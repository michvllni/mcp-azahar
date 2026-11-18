// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/michvllni/mcp-azahar/internal/capture"
	"github.com/michvllni/mcp-azahar/internal/ops"
	"github.com/michvllni/mcp-azahar/internal/rpc"
)

const inputHelp = `Buttons: ` + "A B X Y L R ZL ZR START SELECT UP DOWN LEFT RIGHT HOME POWER" + `
(case-insensitive; DPAD-UP etc. also work). Input set with hold stays until
release; press and touch apply on top of it.`

func inputCommands(a *app) []*cobra.Command {
	printInput := func(cmd *cobra.Command, verb string, r *ops.InputResult) error {
		return a.print(cmd.OutOrStdout(), r, func(w io.Writer) {
			_, _ = fmt.Fprintf(w, "%s; held now: %s\n", verb, describeHeld(r.Held))
		})
	}

	var pressFor time.Duration
	press := &cobra.Command{
		Use:     "press <button>...",
		Short:   "Press buttons briefly, then let go",
		Long:    "Press buttons for --hold (default 100ms), then return to the held input.\n\n" + inputHelp,
		Example: "  mcp-azahar press A\n  mcp-azahar press L R --hold 500ms",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in, err := ops.ParseButtons(args)
			if err != nil {
				return err
			}
			o, err := a.open()
			if err != nil {
				return err
			}
			ctx, cancel := a.ctx(cmd, pressFor+30*time.Second)
			defer cancel()
			r, err := o.Press(ctx, a.session, in, pressFor)
			if err != nil {
				return err
			}
			return printInput(cmd, "pressed "+strings.Join(upper(args), "+"), r)
		},
	}
	press.Flags().DurationVar(&pressFor, "hold", ops.DefaultPress, "how long to keep the buttons pressed")

	hold := &cobra.Command{
		Use:   "hold <button>...",
		Short: "Hold buttons until release",
		Long:  "Hold buttons until `release`.\n\n" + inputHelp,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			in, err := ops.ParseButtons(args)
			if err != nil {
				return err
			}
			o, err := a.open()
			if err != nil {
				return err
			}
			ctx, cancel := a.ctx(cmd, 30*time.Second)
			defer cancel()
			r, err := o.Hold(ctx, a.session, in)
			if err != nil {
				return err
			}
			return printInput(cmd, "holding "+strings.Join(upper(args), "+"), r)
		},
	}

	var relTouch, relCircle bool
	release := &cobra.Command{
		Use:   "release [button]...",
		Short: "Release held input (everything if nothing is named)",
		Long:  "Release the named buttons, --touch or --circle-pad; with no arguments, everything.\n\n" + inputHelp,
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			in, err := ops.ParseButtons(args)
			if err != nil {
				return err
			}
			o, err := a.open()
			if err != nil {
				return err
			}
			ctx, cancel := a.ctx(cmd, 30*time.Second)
			defer cancel()
			r, err := o.Release(ctx, a.session, ops.ReleaseWhat{Buttons: in, Touch: relTouch, CirclePad: relCircle})
			if err != nil {
				return err
			}
			return printInput(cmd, "released", r)
		},
	}
	release.Flags().BoolVar(&relTouch, "touch", false, "release the held touch")
	release.Flags().BoolVar(&relCircle, "circle-pad", false, "center the held circle pad")

	var touchHold bool
	var touchFor time.Duration
	touch := &cobra.Command{
		Use:     "touch <x> <y>",
		Short:   "Touch the bottom screen at x (0-319), y (0-239)",
		Example: "  mcp-azahar touch 160 120\n  mcp-azahar touch 10 200 --hold",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			x, y, err := twoInts(args)
			if err != nil {
				return err
			}
			o, err := a.open()
			if err != nil {
				return err
			}
			ctx, cancel := a.ctx(cmd, touchFor+30*time.Second)
			defer cancel()
			r, err := o.Touch(ctx, a.session, x, y, touchHold, touchFor)
			if err != nil {
				return err
			}
			return printInput(cmd, fmt.Sprintf("touched (%d, %d)", x, y), r)
		},
	}
	touch.Flags().BoolVar(&touchHold, "hold", false, "keep touching until release --touch")
	touch.Flags().DurationVar(&touchFor, "for", ops.DefaultPress, "how long to touch without --hold")

	var padHold bool
	var padFor time.Duration
	pad := &cobra.Command{
		Use:     "circle-pad <x> <y>",
		Short:   "Move the circle pad to x, y (-154..154 each; 0 0 is centered)",
		Example: "  mcp-azahar circle-pad 154 0 --for 500ms   # right for half a second",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			x, y, err := twoInts(args)
			if err != nil {
				return err
			}
			o, err := a.open()
			if err != nil {
				return err
			}
			ctx, cancel := a.ctx(cmd, padFor+30*time.Second)
			defer cancel()
			r, err := o.CirclePad(ctx, a.session, x, y, padHold, padFor)
			if err != nil {
				return err
			}
			return printInput(cmd, fmt.Sprintf("circle pad (%d, %d)", x, y), r)
		},
	}
	pad.Flags().BoolVar(&padHold, "hold", false, "keep the position until release --circle-pad")
	pad.Flags().DurationVar(&padFor, "for", ops.DefaultPress, "how long to hold the position without --hold")
	return []*cobra.Command{press, hold, release, touch, pad}
}

func upper(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = strings.ToUpper(v)
	}
	return out
}

func describeHeld(in rpc.Input) string {
	parts := in.Buttons.Names()
	if in.Home {
		parts = append(parts, "HOME")
	}
	if in.Power {
		parts = append(parts, "POWER")
	}
	if in.Touch != nil {
		parts = append(parts, fmt.Sprintf("touch(%d,%d)", in.Touch.X, in.Touch.Y))
	}
	if in.CirclePad != nil {
		parts = append(parts, fmt.Sprintf("circle-pad(%d,%d)", in.CirclePad.X, in.CirclePad.Y))
	}
	return joinNames(parts)
}

func twoInts(args []string) (int, int, error) {
	x, err := strconv.Atoi(args[0])
	if err != nil {
		return 0, 0, usageErr("x %q is not an integer", args[0])
	}
	y, err := strconv.Atoi(args[1])
	if err != nil {
		return 0, 0, usageErr("y %q is not an integer", args[1])
	}
	return x, y, nil
}

// --- screenshot -----------------------------------------------------------

func screenshotCommand(a *app) *cobra.Command {
	var screen, out string
	var scale int
	var split bool
	c := &cobra.Command{
		Use:   "screenshot",
		Short: "Save a PNG of the top, bottom or both screens",
		Long: `Capture the next rendered frame through Azahar's RPC. --screen both takes two
consecutive frames (top, then bottom) and writes them as one image in 3DS
arrangement, or as <name>-top.png and <name>-bottom.png with --split.`,
		Example: "  mcp-azahar screenshot -o shot.png\n  mcp-azahar screenshot --screen both --scale 2",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sc, err := capture.ParseScreens(screen)
			if err != nil {
				return usageErr("%v", err)
			}
			if out == "" {
				out = fmt.Sprintf("screenshot-%s-%s.png", a.session, time.Now().Format("20060102-150405"))
			}
			o, err := a.open()
			if err != nil {
				return err
			}
			ctx, cancel := a.ctx(cmd, 45*time.Second)
			defer cancel()
			shots, err := o.Screenshot(ctx, a.session, capture.Request{Screens: sc, Scale: scale})
			if err != nil {
				return err
			}
			type file struct {
				Path   string `json:"path"`
				Screen string `json:"screen"`
				Width  int    `json:"width"`
				Height int    `json:"height"`
			}
			var files []file
			if split && len(shots) > 1 {
				base := strings.TrimSuffix(out, filepath.Ext(out))
				for _, s := range shots {
					p := base + "-" + s.Screen + ".png"
					if err := os.WriteFile(p, s.PNG, 0o644); err != nil {
						return err
					}
					files = append(files, file{p, s.Screen, s.Width, s.Height})
				}
			} else {
				b, w, h, err := capture.Compose(shots)
				if err != nil {
					return err
				}
				if err := os.WriteFile(out, b, 0o644); err != nil {
					return err
				}
				files = append(files, file{out, sc.String(), w, h})
			}
			return a.print(cmd.OutOrStdout(), map[string]any{"files": files}, func(w io.Writer) {
				for _, f := range files {
					_, _ = fmt.Fprintf(w, "%s (%s, %dx%d)\n", f.Path, f.Screen, f.Width, f.Height)
				}
			})
		},
	}
	c.Flags().StringVar(&screen, "screen", "top", "top, bottom or both")
	c.Flags().StringVarP(&out, "output", "o", "", "PNG file (default screenshot-<session>-<time>.png)")
	c.Flags().IntVar(&scale, "scale", 1, "resolution scale 1-10 (0: Azahar's internal resolution)")
	c.Flags().BoolVar(&split, "split", false, "with --screen both, write one file per screen")
	return c
}

// --- memory ---------------------------------------------------------------

// parseAddr parses an address: 0x-prefixed hex or decimal.
func parseAddr(s string) (uint32, error) {
	v, err := strconv.ParseUint(strings.ReplaceAll(s, "_", ""), 0, 32)
	if err != nil {
		return 0, usageErr("address %q: use hex like 0x00100000 or decimal", s)
	}
	return uint32(v), nil
}

func memoryCommands(a *app) []*cobra.Command {
	var format string
	read := &cobra.Command{
		Use:   "read <address> <length>",
		Short: "Read emulated memory of the app",
		Long: `Read <length> bytes at the virtual address (hex 0x... or decimal) of the app's
process. Unmapped memory reads as zeros. --format hex (default), dump (hex
dump with addresses) or raw (bytes to stdout).`,
		Example: "  mcp-azahar read 0x00100000 16 --format dump",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, err := parseAddr(args[0])
			if err != nil {
				return err
			}
			n, err := strconv.Atoi(args[1])
			if err != nil {
				return usageErr("length %q is not an integer", args[1])
			}
			if format != "hex" && format != "dump" && format != "raw" {
				return usageErr("--format must be hex, dump or raw")
			}
			o, err := a.open()
			if err != nil {
				return err
			}
			ctx, cancel := a.ctx(cmd, time.Minute)
			defer cancel()
			b, err := o.ReadMemory(ctx, a.session, addr, n)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if a.json {
				return a.print(w, map[string]any{"address": fmt.Sprintf("0x%08x", addr), "length": len(b), "hex": hex.EncodeToString(b)}, nil)
			}
			switch format {
			case "raw":
				_, err = w.Write(b)
				return err
			case "dump":
				d := hex.Dumper(&offsetWriter{w: w, base: addr})
				_, _ = d.Write(b)
				return d.Close()
			}
			_, err = fmt.Fprintln(w, hex.EncodeToString(b))
			return err
		},
	}
	read.Flags().StringVar(&format, "format", "hex", "hex, dump or raw")

	write := &cobra.Command{
		Use:   "write <address> <hex-bytes>",
		Short: "Write bytes to emulated memory and verify them",
		Long: `Write bytes (hex, spaces allowed) at the virtual address and read them back.
Azahar ignores writes outside the app's image and heaps; that is reported as an
error. A write of bytes equal to what the memory already holds cannot be told
apart from an ignored one.`,
		Example: "  mcp-azahar write 0x00101000 'de ad be ef'",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, err := parseAddr(args[0])
			if err != nil {
				return err
			}
			h := strings.NewReplacer(" ", "", ":", "", "0x", "").Replace(strings.Join(args[1:], ""))
			data, err := hex.DecodeString(h)
			if err != nil {
				return usageErr("bytes %q are not hex: %v", strings.Join(args[1:], " "), err)
			}
			o, err := a.open()
			if err != nil {
				return err
			}
			ctx, cancel := a.ctx(cmd, time.Minute)
			defer cancel()
			if err := o.WriteMemory(ctx, a.session, addr, data); err != nil {
				return err
			}
			return a.print(cmd.OutOrStdout(), map[string]any{"address": fmt.Sprintf("0x%08x", addr), "written": len(data)}, func(w io.Writer) {
				_, _ = fmt.Fprintf(w, "wrote %d bytes at 0x%08x\n", len(data), addr)
			})
		},
	}
	return []*cobra.Command{read, write}
}

// offsetWriter rewrites hex.Dumper's offsets to start at base.
type offsetWriter struct {
	w    io.Writer
	base uint32
	line []byte
}

func (o *offsetWriter) Write(p []byte) (int, error) {
	for _, c := range p {
		o.line = append(o.line, c)
		if c == '\n' {
			if err := o.flush(); err != nil {
				return 0, err
			}
		}
	}
	return len(p), nil
}

func (o *offsetWriter) flush() error {
	l := string(o.line)
	o.line = o.line[:0]
	if len(l) >= 8 {
		if off, err := strconv.ParseUint(l[:8], 16, 32); err == nil {
			l = fmt.Sprintf("%08x", uint32(off)+o.base) + l[8:]
		}
	}
	_, err := io.WriteString(o.w, l)
	return err
}

// --- save states ----------------------------------------------------------

func stateCommand(a *app) *cobra.Command {
	c := &cobra.Command{
		Use:   "state",
		Short: "Save or load emulator save states (slots 0-10)",
	}
	for _, action := range []string{"save", "load"} {
		c.AddCommand(&cobra.Command{
			Use:   action + " <slot>",
			Short: map[string]string{"save": "Save a state to slot 0-10", "load": "Load the state in slot 0-10"}[action],
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				slot, err := strconv.Atoi(args[0])
				if err != nil || slot < 0 || slot >= rpc.SaveStateSlots {
					return usageErr("slot %q: use 0-%d", args[0], rpc.SaveStateSlots-1)
				}
				o, err := a.open()
				if err != nil {
					return err
				}
				ctx, cancel := a.ctx(cmd, time.Minute)
				defer cancel()
				if action == "save" {
					err = o.SaveState(ctx, a.session, slot)
				} else {
					err = o.LoadState(ctx, a.session, slot)
				}
				if err != nil {
					return err
				}
				return a.print(cmd.OutOrStdout(), map[string]any{"action": action, "slot": slot}, func(w io.Writer) {
					_, _ = fmt.Fprintf(w, "%s slot %d\n", map[string]string{"save": "saved", "load": "loaded"}[action], slot)
				})
			},
		})
	}
	return c
}

// --- logs -----------------------------------------------------------------

func logsCommand(a *app) *cobra.Command {
	var q ops.LogQuery
	var follow bool
	c := &cobra.Command{
		Use:   "logs",
		Short: "Show Azahar's log for the session",
		Long: `Print Azahar's log (filtered with --contains, last lines with --tail). --follow
keeps printing new lines until interrupted or --timeout.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := a.open()
			if err != nil {
				return err
			}
			if follow {
				ctx, cancel := a.ctx(cmd, 0)
				defer cancel()
				return o.FollowLogs(ctx, a.session, q, cmd.OutOrStdout())
			}
			ctx, cancel := a.ctx(cmd, time.Minute)
			defer cancel()
			lines, err := o.Logs(ctx, a.session, q)
			if err != nil {
				return err
			}
			return a.print(cmd.OutOrStdout(), map[string]any{"lines": lines}, func(w io.Writer) {
				for _, l := range lines {
					_, _ = fmt.Fprintln(w, l)
				}
			})
		},
	}
	c.Flags().IntVar(&q.Tail, "tail", 0, "only the last N matching lines")
	c.Flags().StringVar(&q.Contains, "contains", "", "only lines containing this text")
	c.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new lines")
	return c
}
