// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// semver matches SemVer 2.0.0 without a leading "v" (the release tag adds it).
var semver = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

func TestVersionIsSemVer(t *testing.T) {
	if !semver.MatchString(version) {
		t.Fatalf("version %q is not SemVer without a leading v", version)
	}
}

func TestVersionCommand(t *testing.T) {
	var out bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "mcp-azahar "+version+" ") {
		t.Fatalf("unexpected output %q", out.String())
	}
}
