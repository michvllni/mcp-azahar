// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package session

import (
	"syscall"

	"golang.org/x/sys/windows"
)

const stillActive = 259 // STILL_ACTIVE exit code

// ProcFor returns a Proc for pid (no start time on Windows).
func ProcFor(pid int) Proc { return Proc{PID: pid} }

// Alive reports whether p still runs.
func Alive(p Proc) bool {
	if p.PID <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(p.PID))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}

// Signal terminates p; Windows has no signals to send to other processes.
func Signal(p Proc, _ syscall.Signal) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(p.PID))
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	return windows.TerminateProcess(h, 1)
}
