// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/michvllni/mcp-azahar/internal/ops"
	"github.com/michvllni/mcp-azahar/internal/server"
)

// cliOnly are commands that have no MCP tool on purpose.
var cliOnly = map[string]string{
	"version": "the server reports its version in the MCP handshake",
	"serve":   "starts the MCP server itself",
}

// leaves returns the runnable leaf command paths ("state save").
func leaves(c *cobra.Command, prefix string) []string {
	var out []string
	for _, sub := range c.Commands() {
		if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
			continue
		}
		p := strings.TrimSpace(prefix + " " + sub.Name())
		if sub.HasSubCommands() {
			out = append(out, leaves(sub, p)...)
		} else {
			out = append(out, p)
		}
	}
	return out
}

// TestToolsMirrorCLI keeps CLI and MCP in step: every CLI command has a
// tool and every tool names an existing CLI command.
func TestToolsMirrorCLI(t *testing.T) {
	cmds := map[string]bool{}
	for _, p := range leaves(newRootCmd(), "") {
		cmds[p] = true
	}
	specs := server.New(&ops.Ops{}, server.Options{}).Specs()
	mapped := map[string]bool{}
	for _, s := range specs {
		if !strings.HasPrefix(s.Name, "azahar_") {
			t.Errorf("tool %s lacks the azahar_ prefix", s.Name)
		}
		if !cmds[s.CLI] {
			t.Errorf("tool %s names CLI command %q, which does not exist", s.Name, s.CLI)
		}
		if mapped[s.CLI] {
			t.Errorf("CLI command %q has two tools", s.CLI)
		}
		mapped[s.CLI] = true
	}
	var missing []string
	for c := range cmds {
		if !mapped[c] && cliOnly[c] == "" {
			missing = append(missing, c)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("CLI commands without an MCP tool (add one, or list them in cliOnly): %v", missing)
	}
}
