// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package rpc

import (
	"errors"
	"syscall"
)

// Winsock error codes for an ICMP port-unreachable on a UDP socket.
const (
	wsaECONNRESET   syscall.Errno = 10054
	wsaECONNREFUSED syscall.Errno = 10061
)

// isRefused reports an ICMP port-unreachable on a connected UDP socket.
func isRefused(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == wsaECONNRESET || errno == wsaECONNREFUSED
}
