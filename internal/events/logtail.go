package events

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"
	"time"
)

// TailLog follows a log file (Pal.log) and publishes new lines to the hub
// as KindLog events — but only while at least one subscriber is watching,
// so a server with nobody on the Server Admin page does no tailing work.
//
// It's a polling tail (stat for growth, read the delta) rather than
// inotify: simpler, dependency-free, and fine at a ~1s cadence for a log
// a human is reading. Handles truncation/rotation by detecting a shrink
// and reseeking to the start.
func TailLog(ctx context.Context, hub *Hub, path string) {
	var offset int64
	var lastSize int64
	poll := time.NewTicker(time.Second)
	defer poll.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
		}

		// Do nothing unless someone is watching.
		if hub.SubscriberCount() == 0 {
			// Keep the offset current so that when a viewer connects we
			// stream only *new* lines, not the whole backlog.
			if fi, err := os.Stat(path); err == nil {
				offset = fi.Size()
				lastSize = fi.Size()
			}
			continue
		}

		fi, err := os.Stat(path)
		if err != nil {
			continue // log may not exist yet
		}
		size := fi.Size()
		if size < lastSize {
			// Truncated/rotated — restart from the top.
			offset = 0
		}
		lastSize = size
		if size == offset {
			continue // no new data
		}

		f, err := os.Open(path)
		if err != nil {
			continue
		}
		if _, err := f.Seek(offset, 0); err != nil {
			f.Close()
			continue
		}
		reader := bufio.NewReader(f)
		for {
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				offset += int64(len(line))
				trimmed := strings.TrimRight(line, "\r\n")
				if trimmed != "" {
					hub.Log(trimmed)
				}
			}
			if err != nil {
				break // EOF or error — stop until next poll
			}
		}
		f.Close()
	}
}

// TailFile returns up to n trailing lines of the file at path — the
// "history on page load" read (the log file IS the log's persistence; no
// buffering needed). A missing file returns nil, not an error: servers
// launched without -log simply have no log yet.
func TailFile(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	const window = 32 * 1024 // plenty for 20 lines of log
	off := st.Size() - window
	if off < 0 {
		off = 0
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if off > 0 && len(lines) > 0 {
		lines = lines[1:] // first line is likely partial (we cut mid-line)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := lines[:0]
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out, nil
}
