package events

import (
	"bufio"
	"context"
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
