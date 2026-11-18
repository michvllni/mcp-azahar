// SPDX-License-Identifier: GPL-3.0-or-later

package version

import "testing"

func TestStringDefaults(t *testing.T) {
	want := "1.2.3 (commit none, built unknown)"
	if got := String("1.2.3"); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
