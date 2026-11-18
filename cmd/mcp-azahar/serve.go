// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"github.com/spf13/cobra"

	"github.com/michvllni/mcp-azahar/internal/server"
	buildinfo "github.com/michvllni/mcp-azahar/internal/version"
)

func serveCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the MCP server over stdio",
		Long: `Serve the Model Context Protocol on stdin/stdout for MCP clients. Every tool
mirrors a CLI command (azahar_press = press, azahar_save_state = state save, ...)
and uses the same sessions, so you can watch or poke with the CLI while an agent
drives. --session sets the default session for tools called without one.

Example client configuration:
  {"mcpServers": {"azahar": {"command": "mcp-azahar", "args": ["serve"]}}}`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := a.open()
			if err != nil {
				return err
			}
			ctx, cancel := a.ctx(cmd, 0)
			defer cancel()
			return server.New(o, server.Options{Version: buildinfo.String(version), Session: a.session}).Run(ctx)
		},
	}
}
