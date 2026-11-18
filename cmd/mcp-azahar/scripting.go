// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/michvllni/mcp-azahar/internal/ops"
	"github.com/michvllni/mcp-azahar/internal/sequence"
	"github.com/michvllni/mcp-azahar/internal/symbols"
)

const targetHelp = `A target is a symbol expression from the app's ELF (launch/attach --elf, or
--elf): a name, then .field and [index] parts, e.g. test_state.touch.x or
table[2]; fields and indices need DWARF (-g). It can also be an address (hex
0x... or decimal) read with --type (default u32).
Types: u8 u16 u32 u64 s8 s16 s32 s64 f32 f64 bool string bytes.`

func scriptingCommands(a *app) []*cobra.Command {
	var elf, filter string
	syms := &cobra.Command{
		Use:     "symbols",
		Short:   "List the symbols of the session's ELF",
		Example: "  mcp-azahar symbols --filter state",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := a.open()
			if err != nil {
				return err
			}
			ctx, cancel := a.ctx(cmd, 30*time.Second)
			defer cancel()
			list, err := o.Symbols(ctx, a.session, elf, filter)
			if err != nil {
				return err
			}
			return a.print(cmd.OutOrStdout(), list, func(w io.Writer) {
				for _, s := range list {
					_, _ = fmt.Fprintf(w, "0x%08x %6d %-6s %s %s\n", s.Address, s.Size, s.Kind, s.Name, s.Type)
				}
			})
		},
	}
	syms.Flags().StringVar(&elf, "elf", "", "ELF file instead of the session's")
	syms.Flags().StringVar(&filter, "filter", "", "only names containing this text (case-insensitive)")

	var vo struct {
		elf, typ string
		n        int
	}
	read := &cobra.Command{
		Use:     "read-symbol <target>",
		Short:   "Read and decode a variable (or address) of the app",
		Long:    "Read a variable by name and decode it by its C type.\n\n" + targetHelp,
		Example: "  mcp-azahar read-symbol test_state\n  mcp-azahar read-symbol test_state.last_buttons\n  mcp-azahar read-symbol 0x00101000 --type s16",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := a.open()
			if err != nil {
				return err
			}
			ctx, cancel := a.ctx(cmd, 30*time.Second)
			defer cancel()
			v, err := o.ReadValue(ctx, a.session, args[0], ops.ValueOptions{ELF: vo.elf, Type: symbols.Scalar(vo.typ), Len: vo.n})
			if err != nil {
				return err
			}
			return a.print(cmd.OutOrStdout(), v, func(w io.Writer) {
				b, _ := json.Marshal(v.Value)
				_, _ = fmt.Fprintf(w, "%s = %s\n", v.Target, b)
			})
		},
	}
	read.Flags().StringVar(&vo.elf, "elf", "", "ELF file instead of the session's")
	read.Flags().StringVar(&vo.typ, "type", "", "decode as this type instead of the DWARF type")
	read.Flags().IntVar(&vo.n, "len", 0, "byte count for string/bytes or untyped symbols")

	var wo struct {
		elf, typ, op string
		n            int
		interval     time.Duration
	}
	wait := &cobra.Command{
		Use:   "wait-for <target> [value]",
		Short: "Wait until a variable (or address) has a value; exit 3 on timeout",
		Long: `Poll the target until it compares true with the value (--op eq by default;
ne lt le gt ge, or changed without a value). Numbers can be decimal or 0x hex;
booleans true/false; strings and enum names compare with eq/ne. Waits up to
--timeout (default 10s) and exits with code 3 if the condition is not reached.

` + targetHelp,
		Example: "  mcp-azahar wait-for test_state.frame 60 --op ge --timeout 5s\n  mcp-azahar wait-for test_state.last_buttons --op changed",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cond := ops.Condition{Op: wo.op}
			if len(args) == 2 {
				cond.Value = args[1]
			}
			o, err := a.open()
			if err != nil {
				return err
			}
			ctx, cancel := a.ctx(cmd, ops.DefaultWait)
			defer cancel()
			r, err := o.WaitFor(ctx, a.session, args[0], cond, ops.ValueOptions{ELF: wo.elf, Type: symbols.Scalar(wo.typ), Len: wo.n}, wo.interval)
			if r != nil {
				if perr := a.print(cmd.OutOrStdout(), r, func(w io.Writer) {
					b, _ := json.Marshal(r.Value.Value)
					state := "reached"
					if err != nil {
						state = "timed out"
					}
					_, _ = fmt.Fprintf(w, "%s: %s = %s after %v\n", state, r.Value.Target, b, r.Elapsed.Round(time.Millisecond))
				}); perr != nil {
					return perr
				}
			}
			return err
		},
	}
	wait.Flags().StringVar(&wo.op, "op", "eq", "eq, ne, lt, le, gt, ge or changed")
	wait.Flags().StringVar(&wo.elf, "elf", "", "ELF file instead of the session's")
	wait.Flags().StringVar(&wo.typ, "type", "", "decode as this type instead of the DWARF type")
	wait.Flags().IntVar(&wo.n, "len", 0, "byte count for string/bytes")
	wait.Flags().DurationVar(&wo.interval, "interval", 50*time.Millisecond, "poll interval")

	var seqELF string
	seq := &cobra.Command{
		Use:   "sequence <file|->",
		Short: "Run a scripted test (YAML or JSON steps); exit 3 if a check fails",
		Long: `Run the steps of a sequence file against the session, stopping at the first
failure. Exit code 0 if all steps pass, 3 if an assert/wait_for/log_contains
check fails, 1 for other errors. Relative paths in the file are relative to it.

Example file:
  name: smoke
  elf: build/app.elf
  steps:
    - press: A
    - press: [L, R]
      for: 300ms
    - hold: B
    - release: all
    - touch: {x: 160, y: 120}
    - circle_pad: {x: 154, y: 0, for: 200ms}
    - wait: 500ms
    - wait_for: {target: test_state.frame, op: ge, value: 60, timeout: 5s}
    - assert: {target: test_state.last_buttons, value: 0}
    - assert: {target: "0x00101000", type: u32, op: ne, value: 0}
    - write: {address: "0x00101000", bytes: "de ad be ef"}
    - screenshot: {screen: both, output: out/smoke.png}
    - save_state: 1
    - load_state: 1
    - log_contains: {text: "ready", timeout: 5s}

Each step has exactly one action and an optional name.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r := cmd.InOrStdin()
			dir := "."
			if args[0] != "-" {
				f, err := os.Open(args[0])
				if err != nil {
					return err
				}
				defer func() { _ = f.Close() }()
				r, dir = f, filepath.Dir(args[0])
			}
			sf, err := sequence.Parse(r)
			if err != nil {
				return err
			}
			o, err := a.open()
			if err != nil {
				return err
			}
			ctx, cancel := a.ctx(cmd, 0)
			defer cancel()
			w := cmd.OutOrStdout()
			rep, runErr := sequence.Run(ctx, o, a.session, sf, sequence.Options{Dir: dir, ELF: seqELF, OnStep: func(s sequence.StepResult) {
				if a.json {
					return
				}
				mark := "ok  "
				if !s.OK {
					mark = "FAIL"
				}
				label := s.Action
				if s.Name != "" {
					label += " " + s.Name
				}
				_, _ = fmt.Fprintf(w, "%s %2d %s (%s)\n", mark, s.Index, label, s.Duration)
				if s.Error != "" {
					_, _ = fmt.Fprintf(w, "        %s\n", s.Error)
				}
			}})
			switch {
			case a.json:
				if perr := a.print(w, rep, nil); perr != nil {
					return perr
				}
			case rep.Passed:
				_, _ = fmt.Fprintf(w, "passed: %d steps\n", len(rep.Steps))
			default:
				_, _ = fmt.Fprintf(w, "failed: %d passed, 1 failed, %d skipped\n", len(rep.Steps)-1, rep.Skipped)
			}
			return runErr
		},
	}
	seq.Flags().StringVar(&seqELF, "elf", "", "ELF for symbol steps instead of the file's or session's")
	return []*cobra.Command{syms, read, wait, seq}
}
