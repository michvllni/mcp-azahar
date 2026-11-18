// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package rpc

import (
	"errors"
	"syscall"
)

// isRefused reports an ICMP port-unreachable on a connected UDP socket.
func isRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET)
}
