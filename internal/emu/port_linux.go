// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package emu

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// udpPortOwner finds the process holding UDP port through /proc/net/udp
// (socket inode) and /proc/<pid>/fd (which process has that inode).
// Processes of other users are invisible without privileges.
func udpPortOwner(port int) (portOwner, bool) {
	inodes := map[string]bool{}
	for _, f := range []string{"/proc/net/udp", "/proc/net/udp6"} {
		for _, ino := range udpInodes(f, port) {
			inodes[ino] = true
		}
	}
	if len(inodes) == 0 {
		return portOwner{}, false
	}
	pids, _ := filepath.Glob("/proc/[0-9]*")
	for _, dir := range pids {
		fds, err := os.ReadDir(filepath.Join(dir, "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
			if err != nil {
				continue
			}
			ino, ok := strings.CutPrefix(link, "socket:[")
			if !ok || !inodes[strings.TrimSuffix(ino, "]")] {
				continue
			}
			pid, _ := strconv.Atoi(filepath.Base(dir))
			comm, _ := os.ReadFile(filepath.Join(dir, "comm"))
			return portOwner{PID: pid, Comm: strings.TrimSpace(string(comm))}, true
		}
	}
	return portOwner{}, false
}

// udpInodes returns the socket inodes bound to port in a /proc/net/udp file.
func udpInodes(file string, port int) []string {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	want := fmt.Sprintf(":%04X", port)
	var out []string
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		// sl local_address rem_address st tx:rx tr:tm retrnsmt uid timeout inode
		if len(fields) < 10 || !strings.HasSuffix(fields[1], want) {
			continue
		}
		out = append(out, fields[9])
	}
	return out
}

// parentPID returns the parent of pid, or 0.
func parentPID(pid int) int {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 2 {
		return 0
	}
	ppid, _ := strconv.Atoi(f[1])
	return ppid
}

// isSelfOrDescendant reports whether pid is root or one of its descendants.
func isSelfOrDescendant(pid, root int) bool {
	for i := 0; i < 64 && pid > 1; i++ {
		if pid == root {
			return true
		}
		pid = parentPID(pid)
	}
	return pid == root
}
