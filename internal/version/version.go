// SPDX-License-Identifier: GPL-3.0-or-later

// Package version formats build metadata. The release version itself is the
// `version` constant in cmd/mcp-azahar/main.go (checked against the git tag
// on release); Commit and Date are set at link time via -ldflags (goreleaser).
package version

// Values overridden with -ldflags "-X github.com/michvllni/mcp-azahar/internal/version.Commit=...".
var (
	Commit = "none"
	Date   = "unknown"
)

// String returns a one-line, human-readable description of version v.
func String(v string) string {
	return v + " (commit " + Commit + ", built " + Date + ")"
}
