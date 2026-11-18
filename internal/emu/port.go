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

// ownerStatus is the result of looking up a port's owner.
type ownerStatus int

const (
	ownerFound       ownerStatus = iota // owner known
	ownerHidden                         // port bound, owner not readable (other user)
	ownerNotBound                       // nothing bound to the port
	ownerUnsupported                    // cannot be determined on this OS
)
