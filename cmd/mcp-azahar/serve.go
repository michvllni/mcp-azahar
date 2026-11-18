// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/michvllni/mcp-azahar/internal/server"
	buildinfo "github.com/michvllni/mcp-azahar/internal/version"
)

func serveCommand(a *app) *cobra.Command {
	var allow []string
	c := &cobra.Command{
		Use:   "serve",
		Short: "Run the MCP server over stdio",
		Long: `Serve the Model Context Protocol on stdin/stdout for MCP clients. Every tool
mirrors a CLI command (azahar_press = press, azahar_save_state = state save, ...)
and uses the same sessions, so you can watch or poke with the CLI while an agent
drives. --session sets the default session for tools called without one.

Tools only read and write files (app, ELF, log, sequence, screenshot output)
inside the working directory, or the --allow-dir directories if given.
--timeout does not apply: the server runs until the client disconnects.

Example client configuration:
  {"mcpServers": {"azahar": {"command": "mcp-azahar", "args": ["serve"]}}}`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := a.open()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return server.New(o, server.Options{Version: buildinfo.String(version), Session: a.session, AllowDirs: allow}).Run(ctx)
		},
	}
	c.Flags().StringSliceVar(&allow, "allow-dir", nil, "directory tools may read and write files in (repeatable; default: the working directory)")
	return c
}
