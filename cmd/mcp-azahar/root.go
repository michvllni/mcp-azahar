// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/michvllni/mcp-azahar/internal/emu"
	"github.com/michvllni/mcp-azahar/internal/ops"
	"github.com/michvllni/mcp-azahar/internal/session"
	buildinfo "github.com/michvllni/mcp-azahar/internal/version"
)

// Exit codes (documented in README.md).
const (
	exitOK       = 0
	exitError    = 1
	exitUsage    = 2
	exitCheck    = 3 // a check failed: wait-for timeout, sequence assertion
	exitNoTarget = 4 // no such session, or its emulator is not running
)

// exitCode maps an error to the process exit code.
func exitCode(err error) int {
	var flagErr *flagError
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, ops.ErrCheckFailed):
		return exitCheck
	case errors.Is(err, ops.ErrUsage), errors.As(err, &flagErr):
		return exitUsage
	case errors.Is(err, session.ErrNotFound):
		return exitNoTarget
	}
	return exitError
}

// flagError marks cobra argument and flag errors.
type flagError struct{ err error }

func (e *flagError) Error() string { return e.err.Error() }
func (e *flagError) Unwrap() error { return e.err }

// app carries the global flags and the lazily opened ops.
type app struct {
	session string
	json    bool
	timeout time.Duration
	ops     *ops.Ops
	// newOps opens the session store; tests replace it.
	newOps func() (*ops.Ops, error)
}

func (a *app) open() (*ops.Ops, error) {
	if a.ops == nil {
		o, err := a.newOps()
		if err != nil {
			return nil, err
		}
		a.ops = o
	}
	return a.ops, nil
}

// ctx returns the command context with --timeout (or def if unset) and
// cancellation on SIGINT/SIGTERM.
func (a *app) ctx(cmd *cobra.Command, def time.Duration) (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	d := a.timeout
	if d == 0 {
		d = def
	}
	if d <= 0 {
		return ctx, stop
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	return ctx, func() { cancel(); stop() }
}

// print writes v as JSON with --json, else calls text.
func (a *app) print(w io.Writer, v any, text func(io.Writer)) error {
	if a.json {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	text(w)
	return nil
}

func newRootCmd() *cobra.Command {
	return newRootCmdWith(&app{newOps: ops.New})
}

func newRootCmdWith(a *app) *cobra.Command {
	defSession := os.Getenv("MCP_AZAHAR_SESSION")
	if defSession == "" {
		defSession = session.DefaultName
	}
	root := &cobra.Command{
		Use:   "mcp-azahar",
		Short: "Unofficial MCP server and CLI for the Azahar 3DS emulator",
		Long: `mcp-azahar drives the Azahar 3DS emulator through its RPC server to test
homebrew: launch an app, send input, take screenshots, read and write memory,
use save states and read logs. Unofficial, not affiliated with Azahar.

Commands act on a session (--session, default "default"). With Azahar 2127 only
one emulator with RPC can run per host.

Exit codes: 0 ok, 1 error, 2 usage error, 3 check failed, 4 no such session or
emulator not running.`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &flagError{err} })
	pf := root.PersistentFlags()
	pf.StringVarP(&a.session, "session", "s", defSession, "session name ($MCP_AZAHAR_SESSION)")
	pf.BoolVar(&a.json, "json", false, "print machine-readable JSON")
	pf.DurationVar(&a.timeout, "timeout", 0, "overall timeout, e.g. 30s (default depends on the command)")

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the mcp-azahar version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.print(cmd.OutOrStdout(), map[string]string{
				"version": version, "commit": buildinfo.Commit, "date": buildinfo.Date,
			}, func(w io.Writer) { _, _ = fmt.Fprintln(w, "mcp-azahar", buildinfo.String(version)) })
		},
	})
	root.AddCommand(sessionCommands(a)...)
	root.AddCommand(inputCommands(a)...)
	root.AddCommand(screenshotCommand(a), stateCommand(a), logsCommand(a))
	root.AddCommand(memoryCommands(a)...)
	for _, c := range root.Commands() {
		wrapArgs(c)
	}
	return root
}

// wrapArgs turns positional-argument errors into usage errors.
func wrapArgs(c *cobra.Command) {
	if c.Args != nil {
		check := c.Args
		c.Args = func(cmd *cobra.Command, args []string) error {
			if err := check(cmd, args); err != nil {
				return &flagError{err}
			}
			return nil
		}
	}
	for _, sub := range c.Commands() {
		wrapArgs(sub)
	}
}

// usageErr returns an ops usage error (exit code 2).
func usageErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ops.ErrUsage, fmt.Sprintf(format, args...))
}

// --- session commands -----------------------------------------------------

func sessionCommands(a *app) []*cobra.Command {
	var elf, display string
	launch := &cobra.Command{
		Use:   "launch <app.3dsx|.cci|.3ds|.cxi|.elf>",
		Short: "Start Azahar headless with an app in a new session",
		Long: `Start Azahar with the app in a new session and wait until its RPC server
answers. On Linux it runs on its own Xvfb display unless --display is given.
Fails if the session exists or another emulator holds the RPC port.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := a.open()
			if err != nil {
				return err
			}
			ctx, cancel := a.ctx(cmd, 2*time.Minute)
			defer cancel()
			s, err := o.Launch(ctx, a.session, emu.LaunchOptions{App: args[0], ELF: elf, Display: display})
			if err != nil {
				return err
			}
			return a.print(cmd.OutOrStdout(), s, func(w io.Writer) {
				_, _ = fmt.Fprintf(w, "session %q running: azahar pid %d on display %s, RPC %s\n",
					s.Name, s.Azahar.PID, s.Display, s.RPC.Address)
			})
		},
	}
	launch.Flags().StringVar(&elf, "elf", "", "ELF with symbols for read-symbol")
	launch.Flags().StringVar(&display, "display", "", "use this X display instead of starting Xvfb (e.g. :0)")

	var addr, logPath, attachELF string
	attach := &cobra.Command{
		Use:   "attach",
		Short: "Register an Azahar you started yourself (RPC enabled, app loaded)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := a.open()
			if err != nil {
				return err
			}
			ctx, cancel := a.ctx(cmd, 30*time.Second)
			defer cancel()
			s, err := o.Attach(ctx, a.session, emu.AttachOptions{Address: addr, ELF: attachELF, LogPath: logPath})
			if err != nil {
				return err
			}
			return a.print(cmd.OutOrStdout(), s, func(w io.Writer) {
				_, _ = fmt.Fprintf(w, "session %q attached to %s\n", s.Name, s.RPC.Address)
			})
		},
	}
	attach.Flags().StringVar(&addr, "address", "", "RPC address host:port (default 127.0.0.1:45987)")
	attach.Flags().StringVar(&attachELF, "elf", "", "ELF with symbols for read-symbol")
	attach.Flags().StringVar(&logPath, "log", "", "Azahar's log file, for logs")

	var keep bool
	stop := &cobra.Command{
		Use:   "stop",
		Short: "Stop the session's emulator (or forget an attached one)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := a.open()
			if err != nil {
				return err
			}
			ctx, cancel := a.ctx(cmd, 30*time.Second)
			defer cancel()
			if err := o.Stop(ctx, a.session, keep); err != nil {
				return err
			}
			return a.print(cmd.OutOrStdout(), map[string]any{"stopped": a.session}, func(w io.Writer) {
				_, _ = fmt.Fprintf(w, "session %q stopped\n", a.session)
			})
		},
	}
	stop.Flags().BoolVar(&keep, "keep-files", false, "keep the session directory (logs, save states)")

	status := &cobra.Command{
		Use:   "status",
		Short: "Show whether the session's emulator runs and answers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := a.open()
			if err != nil {
				return err
			}
			ctx, cancel := a.ctx(cmd, 15*time.Second)
			defer cancel()
			st, err := o.Status(ctx, a.session)
			if err != nil {
				return err
			}
			if err := a.print(cmd.OutOrStdout(), st, func(w io.Writer) {
				s := st.Session
				state := "running"
				if !st.Running {
					state = "not running"
				}
				_, _ = fmt.Fprintf(w, "session %q (%s): %s\nRPC: %s", s.Name, s.Kind, state, s.RPC.Address)
				if st.RPCError != "" {
					_, _ = fmt.Fprintf(w, " (not answering: %s)", st.RPCError)
				}
				_, _ = fmt.Fprintln(w)
				for _, p := range st.Processes {
					_, _ = fmt.Fprintf(w, "process %d %q title %016X\n", p.PID, p.Name, p.TitleID)
				}
				if s.AppPath != "" {
					_, _ = fmt.Fprintf(w, "app: %s\n", s.AppPath)
				}
				if s.LogPath != "" {
					_, _ = fmt.Fprintf(w, "log: %s\n", s.LogPath)
				}
			}); err != nil {
				return err
			}
			if !st.Running || st.RPCError != "" {
				return fmt.Errorf("%w: session %q is not answering", session.ErrNotFound, a.session)
			}
			return nil
		},
	}

	sessions := &cobra.Command{
		Use:   "sessions",
		Short: "List sessions (stale ones are removed)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := a.open()
			if err != nil {
				return err
			}
			ctx, cancel := a.ctx(cmd, 15*time.Second)
			defer cancel()
			list, err := o.Sessions(ctx)
			if err != nil {
				return err
			}
			return a.print(cmd.OutOrStdout(), list, func(w io.Writer) {
				if len(list) == 0 {
					_, _ = fmt.Fprintln(w, "no sessions")
				}
				for _, s := range list {
					_, _ = fmt.Fprintf(w, "%-16s %-9s %-15s %s\n", s.Name, s.Kind, s.RPC, s.App)
				}
			})
		},
	}
	return []*cobra.Command{launch, attach, stop, status, sessions}
}

// joinNames formats button names for messages.
func joinNames(n []string) string {
	if len(n) == 0 {
		return "nothing"
	}
	return strings.Join(n, "+")
}
