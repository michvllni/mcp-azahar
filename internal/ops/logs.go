// SPDX-License-Identifier: GPL-3.0-or-later

package ops

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// LogQuery selects log lines.
type LogQuery struct {
	// From is the byte offset to start at (0: beginning). Pass the Offset
	// of a previous result to get only newer lines. If the file is now
	// shorter (Azahar restarted), reading starts over at 0.
	From int64
	// Tail keeps only the last N matching lines (0: all).
	Tail int
	// Contains keeps lines containing this text (case-sensitive).
	Contains string
}

// LogResult holds matching complete lines. An unfinished last line is left
// for the next call.
type LogResult struct {
	Lines []string `json:"lines"`
	// Offset is where the next call should start (From).
	Offset int64 `json:"offset"`
	// Restarted means the file was shorter than From and was read from 0.
	Restarted bool `json:"restarted,omitempty"`
}

// logPath returns the session's Azahar log.
func (o *Ops) logPath(name string) (string, error) {
	s, err := o.M.Store.Load(name)
	if err != nil {
		return "", err
	}
	if s.LogPath == "" {
		return "", fmt.Errorf("session %q has no known log file (attach with --log)", name)
	}
	return s.LogPath, nil
}

// Logs returns matching lines of the session's Azahar log.
func (o *Ops) Logs(ctx context.Context, name string, q LogQuery) (*LogResult, error) {
	if q.Tail < 0 || q.From < 0 {
		return nil, usage("tail and from must not be negative")
	}
	p, err := o.logPath(name)
	if err != nil {
		return nil, err
	}
	return readLog(ctx, p, q)
}

func readLog(ctx context.Context, path string, q LogQuery) (*LogResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	res := &LogResult{Lines: []string{}, Offset: q.From}
	if fi, err := f.Stat(); err == nil && fi.Size() < q.From {
		res.Restarted, res.Offset = true, 0
	}
	if _, err := f.Seek(res.Offset, io.SeekStart); err != nil {
		return nil, err
	}
	r := bufio.NewReaderSize(f, 64*1024)
	for n := 0; ; n++ {
		if n%4096 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		line, err := r.ReadString('\n')
		if errors.Is(err, io.EOF) {
			break // an unfinished line stays for the next call
		}
		if err != nil {
			return nil, err
		}
		res.Offset += int64(len(line))
		l := strings.TrimRight(line, "\r\n")
		if q.Contains != "" && !strings.Contains(l, q.Contains) {
			continue
		}
		res.Lines = append(res.Lines, l)
		if q.Tail > 0 && len(res.Lines) > 2*q.Tail {
			// Compact now and then, so memory stays bounded on large logs.
			res.Lines = append(res.Lines[:0:0], res.Lines[len(res.Lines)-q.Tail:]...)
		}
	}
	if q.Tail > 0 && len(res.Lines) > q.Tail {
		res.Lines = res.Lines[len(res.Lines)-q.Tail:]
	}
	return res, nil
}

// FollowLogs calls emit for matching lines as they are appended, starting
// with the last q.Tail lines (none if 0), like `tail -f`. It handles
// truncation (Azahar restarted) and ends when ctx ends or the session is
// stopped or its emulator exits.
func (o *Ops) FollowLogs(ctx context.Context, name string, q LogQuery, emit func(string) error) error {
	if q.Tail < 0 {
		return usage("tail must not be negative")
	}
	p, err := o.logPath(name)
	if err != nil {
		return err
	}
	var off int64
	if q.Tail > 0 {
		res, err := readLog(ctx, p, LogQuery{Tail: q.Tail, Contains: q.Contains})
		if err != nil {
			return err
		}
		for _, l := range res.Lines {
			if err := emit(l); err != nil {
				return err
			}
		}
		off = res.Offset
	} else if off, err = completeLinesEnd(p); err != nil {
		return err
	}
	lastCheck := time.Now()
	for {
		res, err := readLog(ctx, p, LogQuery{From: off, Contains: q.Contains})
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			res = &LogResult{Offset: off} // removed: the session check below ends us
		}
		for _, l := range res.Lines {
			if err := emit(l); err != nil {
				return err
			}
		}
		off = res.Offset
		if time.Since(lastCheck) >= time.Second {
			lastCheck = time.Now()
			s, err := o.M.Store.Load(name)
			if err != nil || !s.Running() {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// completeLinesEnd returns the offset after the last complete line, so a
// follow starting there never emits the fragment of a line being written.
func completeLinesEnd(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	end := fi.Size()
	const chunk = 4096
	buf := make([]byte, chunk)
	for pos := end; pos > 0; {
		n := min(chunk, pos)
		pos -= n
		if _, err := f.ReadAt(buf[:n], pos); err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		if i := strings.LastIndexByte(string(buf[:n]), '\n'); i >= 0 {
			return pos + int64(i) + 1, nil
		}
	}
	return 0, nil
}
