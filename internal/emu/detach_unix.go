// SPDX-License-Identifier: GPL-3.0-or-later

//go:build unix

package emu

import (
	"os/exec"
	"syscall"
)

// detach starts cmd in its own session, so it outlives the CLI process and
// its process group can be signalled as a whole.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
