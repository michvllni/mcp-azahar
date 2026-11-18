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

// freeDisplay returns the first unused display number >= from.
func freeDisplay(from int) (int, error) {
	for n := max(from, firstDisplay); n <= lastDisplay; n++ {
		if !displayInUse(n) {
			return n, nil
		}
	}
	return 0, fmt.Errorf("no free X display between :%d and :%d", firstDisplay, lastDisplay)
}

// errXvfbExited means Xvfb exited during startup, e.g. because the display
// is taken through an abstract socket the /tmp check cannot see.
var errXvfbExited = errors.New("xvfb exited during startup")

// startXvfb starts a detached Xvfb on a free display and waits until its
// socket exists, trying further displays if Xvfb exits right away. It
// returns the process and the display (":N").
func startXvfb(ctx context.Context, bin, logPath string) (*os.Process, string, error) {
	from := firstDisplay
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		var n int
		if n, err = freeDisplay(from); err != nil {
			return nil, "", err
		}
		var p *os.Process
		p, err = startXvfbOn(ctx, bin, n, logPath)
		if err == nil {
			return p, ":" + strconv.Itoa(n), nil
		}
		if !errors.Is(err, errXvfbExited) {
			return nil, "", err
		}
		from = n + 1
	}
	return nil, "", err
}

func startXvfbOn(ctx context.Context, bin string, n int, logPath string) (*os.Process, error) {
	display := ":" + strconv.Itoa(n)
	logf, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = logf.Close() }()
	//nolint:noctx // detached: Xvfb must outlive the caller and its context
	cmd := exec.Command(bin, display, "-screen", "0", "1280x1024x24", "-nolisten", "tcp")
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start Xvfb (%s): %w", bin, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	sock := "/tmp/.X11-unix/X" + strconv.Itoa(n)
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(sock); err == nil {
			return cmd.Process, nil
		}
		select {
		case err := <-exited:
			status := "status 0"
			if err != nil {
				status = err.Error()
			}
			return nil, fmt.Errorf("%w on %s (%s); log: %s", errXvfbExited, display, status, logPath)
		case <-deadline.C:
			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("xvfb on %s did not create %s within 10s (log: %s)", display, sock, logPath)
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return nil, errors.Join(ctx.Err(), fmt.Errorf("starting Xvfb on %s", display))
		case <-time.After(20 * time.Millisecond):
		}
	}
}
