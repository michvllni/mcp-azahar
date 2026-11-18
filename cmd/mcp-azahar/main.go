// SPDX-License-Identifier: GPL-3.0-or-later

// Command mcp-azahar is an unofficial CLI and MCP server for the Azahar 3DS
// emulator. All behavior lives in internal/ packages (internal/ops); this is
// a thin front end.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// version is the release version (SemVer, without "v"). Bump it before
// tagging: the release workflow fails unless tag v<version> matches it
// (scripts/check-version.sh).
const version = "0.1.0"

func main() {
	os.Exit(run(newRootCmd(), os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the CLI and returns the process exit code.
func run(cmd *cobra.Command, args []string, stdout, stderr io.Writer) int {
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	err := cmd.Execute()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)
	}
	return exitCode(err)
}
