// SPDX-License-Identifier: GPL-3.0-or-later

package version

import "testing"

func TestStringDefaults(t *testing.T) {
	want := "dev (commit none, built unknown)"
	if got := String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
