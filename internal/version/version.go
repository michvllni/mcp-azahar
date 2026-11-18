// SPDX-License-Identifier: GPL-3.0-or-later

// Package version holds build metadata, set at link time via -ldflags
// (e.g. by goreleaser).
package version

// Values overridden with -ldflags "-X github.com/michvllni/mcp-azahar/internal/version.Version=...".
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a one-line, human-readable version description.
func String() string {
	return Version + " (commit " + Commit + ", built " + Date + ")"
}
