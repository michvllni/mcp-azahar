// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package session

import (
	"syscall"

	"golang.org/x/sys/windows"
)

const stillActive = 259 // STILL_ACTIVE exit code

// ProcFor returns a Proc for pid with its creation time.
func ProcFor(pid int) Proc {
	start, _ := creationTime(pid)
	return Proc{PID: pid, Start: start}
}

// creationTime returns the process creation time in 100 ns units.
func creationTime(pid int) (uint64, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return 0, false
	}
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), true
}

// Alive reports whether p still runs and is the same process.
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
	if err := windows.GetExitCodeProcess(h, &code); err != nil || code != stillActive {
		return false
	}
	start, ok := creationTime(p.PID)
	return !ok || p.Start == 0 || start == p.Start
}

// Signal terminates p if it is still the same process; Windows has no
// signals to send to other processes.
func Signal(p Proc, _ syscall.Signal) error {
	if !Alive(p) {
		return nil
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(p.PID))
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	return windows.TerminateProcess(h, 1)
}

// KillGroupRest is a no-op on Windows.
func KillGroupRest(Proc) {}
