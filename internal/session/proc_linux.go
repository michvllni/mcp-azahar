// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package session

import (
	"os"
	"strconv"
	"strings"
)

// procInfo reads the state and start time (field 22, clock ticks since
// boot) from /proc/<pid>/stat. zombie is true only once every thread has
// exited.
func procInfo(pid int) (zombie bool, start uint64, ok bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false, 0, false
	}
	// The command name (field 2) is in parentheses and may contain spaces.
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return false, 0, false
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return false, 0, false
	}
	start, err = strconv.ParseUint(f[19], 10, 64)
	if err != nil {
		return false, 0, false
	}
	return (f[0] == "Z" || f[0] == "X") && !otherThreads(pid), start, true
}

// otherThreads reports whether threads other than the main one still
// exist. When a multi-threaded process (like Azahar) is killed, its main
// thread can turn into a zombie while other threads are still exiting; its
// file descriptors, e.g. the RPC socket, are only released after the last
// thread is gone, so the process does not count as dead before that.
func otherThreads(pid int) bool {
	tids, err := os.ReadDir("/proc/" + strconv.Itoa(pid) + "/task")
	if err != nil {
		return false
	}
	self := strconv.Itoa(pid)
	for _, t := range tids {
		if t.Name() != self {
			return true
		}
	}
	return false
}
