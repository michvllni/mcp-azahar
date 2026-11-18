// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

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

// flagRenames maps CLI flags to differently named tool properties.
var flagRenames = map[string]string{"for": "for_ms", "interval": "interval_ms"}

// flagOnly are CLI flags without a tool property on purpose.
var flagOnly = map[string]string{
	"read --format":     "the tool returns hex; dump and raw are terminal formats",
	"logs --follow":     "tools do not stream; poll with from",
	"serve --allow-dir": "serve has no tool",
}

// TestToolPropertiesMirrorFlags checks that every flag of a command is a
// property of its tool (dashes become underscores), so a new flag cannot
// be forgotten in the MCP server.
func TestToolPropertiesMirrorFlags(t *testing.T) {
	ctx := context.Background()
	s := server.New(&ops.Ops{}, server.Options{})
	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.MCP().Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	props := map[string]map[string]any{}
	for _, tool := range list.Tools {
		schema, _ := tool.InputSchema.(map[string]any)
		p, _ := schema["properties"].(map[string]any)
		props[tool.Name] = p
		if _, ok := p["session"]; !ok && tool.Name != "azahar_sessions" { // lists all
			t.Errorf("%s has no session property", tool.Name)
		}
	}
	root := newRootCmd()
	for _, spec := range s.Specs() {
		cmd, _, err := root.Find(strings.Fields(spec.CLI))
		if err != nil {
			t.Fatalf("%s: %v", spec.CLI, err)
		}
		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			key := spec.CLI + " --" + f.Name
			if flagOnly[key] != "" {
				return
			}
			name := strings.ReplaceAll(f.Name, "-", "_")
			if r, ok := flagRenames[f.Name]; ok {
				name = r
			}
			if _, ok := props[spec.Name][name]; !ok {
				t.Errorf("%s has no property %q for %s (add it, or list the flag in flagOnly)", spec.Name, name, key)
			}
		})
	}
}
