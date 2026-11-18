// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !linux

package emu

// udpPortOwner is only implemented on Linux.
func udpPortOwner(int) (portOwner, bool) { return portOwner{}, false }

// isSelfOrDescendant is only implemented on Linux; elsewhere ownership is
// checked through Azahar's log alone.
func isSelfOrDescendant(pid, root int) bool { return pid == root }
