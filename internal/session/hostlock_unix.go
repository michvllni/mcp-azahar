// SPDX-License-Identifier: GPL-3.0-or-later

//go:build unix

package session

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// hostLockDir is shared by all users (macOS's per-user $TMPDIR is not).
func hostLockDir() string { return "/tmp" }

// openSharedLockFile opens or creates a lock file that other users can
// open too. It never follows symlinks and only changes the mode of a file
// it created itself, so a planted symlink cannot redirect it.
func openSharedLockFile(path string) (*os.File, error) {
	for range 3 {
		f, err := os.OpenFile(path, os.O_RDWR|unix.O_NOFOLLOW, 0)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		f, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0o666)
		if errors.Is(err, os.ErrExist) {
			continue // created concurrently: open it
		}
		if err != nil {
			return nil, err
		}
		_ = f.Chmod(0o666) // despite the umask, so other users can lock it
		return f, nil
	}
	return nil, os.ErrExist
}
