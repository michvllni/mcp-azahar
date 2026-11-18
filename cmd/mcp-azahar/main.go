// SPDX-License-Identifier: GPL-3.0-or-later

// Command mcp-azahar is an unofficial CLI and MCP server for the Azahar 3DS
// emulator. All behavior lives in internal/ packages; this is a thin front end.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	buildinfo "github.com/michvllni/mcp-azahar/internal/version"
)

// version is the release version (SemVer, without "v"). Bump it before
// tagging: the release workflow fails unless tag v<version> matches it
// (scripts/check-version.sh).
const version = "0.1.0"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "mcp-azahar",
		Short:         "Unofficial MCP server and CLI for the Azahar 3DS emulator",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the mcp-azahar version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "mcp-azahar", buildinfo.String(version))
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
