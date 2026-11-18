// SPDX-License-Identifier: GPL-3.0-or-later

//go:build unix

package session

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// ProcFor returns a Proc for pid, including its start time where the OS
// provides one.
func ProcFor(pid int) Proc {
	start, _ := procStart(pid)
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
	state, start, ok := procStat(p.PID)
	if !ok {
		return true // no /proc: kill(0) is all we have
	}
	if state == "Z" || state == "X" {
		return false
	}
	return p.Start == 0 || start == p.Start
}

// Signal sends sig to p's process group if p leads one, else to p.
func Signal(p Proc, sig syscall.Signal) error {
	if pgid, err := unix.Getpgid(p.PID); err == nil && pgid == p.PID {
		return unix.Kill(-p.PID, sig)
	}
	return unix.Kill(p.PID, sig)
}

func procStart(pid int) (uint64, bool) {
	_, start, ok := procStat(pid)
	return start, ok
}

// procStat reads state and start time (field 22) from /proc/<pid>/stat.
func procStat(pid int) (state string, start uint64, ok bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", 0, false
	}
	// The command name (field 2) is in parentheses and may contain spaces.
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return "", 0, false
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return "", 0, false
	}
	start, err = strconv.ParseUint(f[19], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return f[0], start, true
}
