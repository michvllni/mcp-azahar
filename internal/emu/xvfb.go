// SPDX-License-Identifier: GPL-3.0-or-later

package emu

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"
)

// X display numbers tried for a new Xvfb.
const (
	firstDisplay = 99
	lastDisplay  = 199
)

// displayInUse reports whether X display n has a socket or lock file.
func displayInUse(n int) bool {
	for _, p := range []string{
		"/tmp/.X11-unix/X" + strconv.Itoa(n),
		"/tmp/.X" + strconv.Itoa(n) + "-lock",
	} {
		if _, err := os.Lstat(p); err == nil {
			return true
		}
	}
	return false
}

// freeDisplay returns the first unused display number.
func freeDisplay() (int, error) {
	for n := firstDisplay; n <= lastDisplay; n++ {
		if !displayInUse(n) {
			return n, nil
		}
	}
	return 0, fmt.Errorf("no free X display between :%d and :%d", firstDisplay, lastDisplay)
}

// startXvfb starts a detached Xvfb on a free display and waits until its
// socket exists. It returns the process and the display (":N").
func startXvfb(ctx context.Context, bin, logPath string) (*os.Process, string, error) {
	n, err := freeDisplay()
	if err != nil {
		return nil, "", err
	}
	display := ":" + strconv.Itoa(n)
	logf, err := os.Create(logPath)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = logf.Close() }()
	//nolint:noctx // detached: Xvfb must outlive the caller and its context
	cmd := exec.Command(bin, display, "-screen", "0", "1280x1024x24", "-nolisten", "tcp")
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return nil, "", fmt.Errorf("start Xvfb (%s): %w", bin, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	sock := "/tmp/.X11-unix/X" + strconv.Itoa(n)
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(sock); err == nil {
			return cmd.Process, display, nil
		}
		select {
		case err := <-exited:
			return nil, "", fmt.Errorf("xvfb on %s exited: %w (log: %s)", display, err, logPath)
		case <-deadline.C:
			_ = cmd.Process.Kill()
			return nil, "", fmt.Errorf("xvfb on %s did not create %s within 10s (log: %s)", display, sock, logPath)
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return nil, "", errors.Join(ctx.Err(), fmt.Errorf("starting Xvfb on %s", display))
		case <-time.After(20 * time.Millisecond):
		}
	}
}
