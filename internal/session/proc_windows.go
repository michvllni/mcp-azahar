// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package session

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// ProcFor returns a Proc for pid with its creation time.
func ProcFor(pid int) Proc {
	p := Proc{PID: pid}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return p
	}
	defer func() { _ = windows.CloseHandle(h) }()
	p.Start, _ = creationTime(h)
	return p
}

// creationTime returns the process creation time in 100 ns units.
func creationTime(h windows.Handle) (uint64, bool) {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return 0, false
	}
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), true
}

// Alive reports whether p still runs and is the same process. A process
// that cannot be queried (access denied) counts as alive.
func Alive(p Proc) bool {
	if p.PID <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(p.PID))
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return true
	}
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if ev, err := windows.WaitForSingleObject(h, 0); err != nil || ev != uint32(windows.WAIT_TIMEOUT) {
		return false // signalled: exited
	}
	start, ok := creationTime(h)
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
