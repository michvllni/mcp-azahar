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
	// shorter (Azahar restarted), reading starts over at 0; a log that was
	// recreated and has already grown past From is not detected here
	// (FollowLogs detects it by file identity).
	From int64
	// Tail keeps only the last N matching lines (0: all).
	Tail int
	// Contains keeps lines containing this text (case-sensitive).
	Contains string
	// First stops after the first matching line; Offset is then right
	// after it, so the next call continues behind the match.
	First bool
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
		if q.First {
			break
		}
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

// LogEnd returns the offset after the last complete line of the session's
// log (0 if the file does not exist yet): pass it as LogQuery.From to see
// only lines written from now on.
func (o *Ops) LogEnd(name string) (int64, error) {
	p, err := o.logPath(name)
	if err != nil {
		return 0, err
	}
	off, err := completeLinesEnd(p)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	return off, err
}

// FollowLogs calls emit for matching lines as they are appended, starting
// with the last q.Tail lines (none if 0), like `tail -f`. A line that is
// still being written when it starts is emitted whole once it is complete;
// fragments are never emitted. It follows a log that is truncated or
// recreated (Azahar restarted) from its beginning. It ends when ctx ends,
// when the session is stopped, or, for launched sessions, when the
// emulator exits (attached sessions: ctx or stop only), after a final read.
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
	prev, _ := os.Stat(p)
	// poll reads new complete lines; final also emits an unfinished last
	// line, since nothing will complete it any more.
	poll := func(final bool) error {
		if cur, err := os.Stat(p); err == nil {
			if prev != nil && !os.SameFile(prev, cur) {
				off = 0 // recreated
			}
			prev = cur
		}
		res, err := readLog(context.WithoutCancel(ctx), p, LogQuery{From: off, Contains: q.Contains})
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, l := range res.Lines {
			if err := emit(l); err != nil {
				return err
			}
		}
		off = res.Offset
		if final {
			if rest := unfinishedLine(p, off); rest != "" && (q.Contains == "" || strings.Contains(rest, q.Contains)) {
				return emit(rest)
			}
		}
		return nil
	}
	lastCheck := time.Now()
	for {
		if err := poll(false); err != nil {
			return err
		}
		if time.Since(lastCheck) >= time.Second {
			lastCheck = time.Now()
			if s, err := o.M.Store.Load(name); err != nil || !s.Running() {
				return poll(true)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// unfinishedLine returns the text after off (a line without newline).
func unfinishedLine(path string, off int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	b := make([]byte, 64*1024)
	n, _ := f.ReadAt(b, off)
	return strings.TrimRight(string(b[:n]), "\r\n")
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
