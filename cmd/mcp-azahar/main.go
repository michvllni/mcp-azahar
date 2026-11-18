// SPDX-License-Identifier: GPL-3.0-or-later

// Command mcp-azahar is an unofficial CLI and MCP server for the Azahar 3DS
// emulator. All behavior lives in internal/ packages; this is a thin front end.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/michvllni/mcp-azahar/internal/version"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "mcp-azahar",
		Short:         "Unofficial MCP server and CLI for the Azahar 3DS emulator",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the mcp-azahar version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "mcp-azahar", version.String())
			return err
		},
	})
	return root
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
