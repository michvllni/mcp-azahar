// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package session

import "golang.org/x/sys/unix"

// szomb is the zombie state in macOS's sys/proc.h (SIDL 1 ... SZOMB 5).
const szomb = 5

// procInfo reads the zombie state and start time (microseconds since the
// epoch) through sysctl kern.proc.pid.
func procInfo(pid int) (zombie bool, start uint64, ok bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp.Proc.P_pid != int32(pid) {
		return false, 0, false
	}
	tv := kp.Proc.P_starttime
	return kp.Proc.P_stat == szomb, uint64(tv.Sec)*1_000_000 + uint64(tv.Usec), true
}
