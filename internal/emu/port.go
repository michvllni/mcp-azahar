// SPDX-License-Identifier: GPL-3.0-or-later

package emu

import (
	"context"
	"net"
	"strconv"
)

// portFree reports whether UDP port can be bound on all interfaces, as
// Azahar does (PROTOCOL.md §2).
func portFree(port int) bool {
	var lc net.ListenConfig
	c, err := lc.ListenPacket(context.Background(), "udp4", net.JoinHostPort("0.0.0.0", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// portOwner is the process bound to a UDP port: PID and command name.
type portOwner struct {
	PID  int
	Comm string
}
