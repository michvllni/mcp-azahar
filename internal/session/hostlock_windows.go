// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package session

import "os"

func hostLockDir() string { return os.TempDir() }

func openSharedLockFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o666)
}
