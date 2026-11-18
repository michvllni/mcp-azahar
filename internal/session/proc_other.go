// SPDX-License-Identifier: GPL-3.0-or-later

//go:build unix && !linux && !darwin

package session

// procInfo is not implemented on this OS: liveness falls back to kill(0)
// without PID-reuse detection.
func procInfo(int) (zombie bool, start uint64, ok bool) { return false, 0, false }
