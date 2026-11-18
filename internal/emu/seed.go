// SPDX-License-Identifier: GPL-3.0-or-later

package emu

import (
	"os"
	"path/filepath"
)

// seedConfig is written to <user>/config/qt-config.ini before the first
// start (PROTOCOL.md §7): RPC on, single-screen layout so screenshots are
// exactly one screen (DECISIONS.md D4), no update-check or close dialogs.
// No "\default" keys: "\default=true" would make Azahar ignore the value.
const seedConfig = `[Debugging]
enable_rpc_server=true
[Layout]
layout_option=1
swap_screen=false
[Miscellaneous]
check_for_update_on_start=false
[UI]
confirmClose=false
`

// writeSeedConfig creates userDir/config/qt-config.ini.
func writeSeedConfig(userDir string) error {
	dir := filepath.Join(userDir, "config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "qt-config.ini"), []byte(seedConfig), 0o600)
}
