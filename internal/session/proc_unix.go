// SPDX-License-Identifier: GPL-3.0-or-later

//go:build unix

package session

import (
	"errors"
	"syscall"

	"golang.org/x/sys/unix"
)

// ProcFor returns a Proc for pid, including its start time where the OS
// provides one (Linux, macOS).
func ProcFor(pid int) Proc {
	_, start, _ := procInfo(pid)
	return Proc{PID: pid, Start: start}
}

// Alive reports whether p still runs and is the same process (not a
// reused PID). Zombies count as dead.
func Alive(p Proc) bool {
	if p.PID <= 0 {
		return false
	}
	if err := unix.Kill(p.PID, 0); err != nil && !errors.Is(err, unix.EPERM) {
		return false
	}
	zombie, start, ok := procInfo(p.PID)
	if !ok {
		return true // no process information: kill(0) is all we have
	}
	if zombie {
		return false
	}
	return p.Start == 0 || start == p.Start
}

// Signal sends sig to p's process group if p leads one, else to p. It
// does nothing if p is no longer the same process.
func Signal(p Proc, sig syscall.Signal) error {
	if !Alive(p) {
		return nil
	}
	if pgid, err := unix.Getpgid(p.PID); err == nil && pgid == p.PID {
		return unix.Kill(-p.PID, sig)
	}
	return unix.Kill(p.PID, sig)
}

// KillGroupRest sends SIGKILL to what is left of the process group led by
// the (now dead) process p, e.g. children of a wrapper script. Call it
// right after p has exited, so its PID cannot have been reused yet.
func KillGroupRest(p Proc) {
	if p.PID > 0 && unix.Kill(-p.PID, 0) == nil {
		_ = unix.Kill(-p.PID, unix.SIGKILL)
	}
}
