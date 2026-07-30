package events

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// EventLog persists hub events to a JSONL file so history survives Paladin
// restarts (rev 17: the game writes no logs of its own, so Paladin's event
// history is the only history there is). One JSON event per line — trivially
// re-readable by Paladin and grep-able by a human.
//
// Bounded by rotation, not by a scheduler: when the file crosses maxBytes
// it becomes <path>.old (replacing any previous .old) and a fresh file
// starts. Worst case on disk: ~2×maxBytes, no cleanup jobs ever needed.
type EventLog struct {
	path     string
	maxBytes int64

	mu   sync.Mutex
	f    *os.File
	size int64
}

// OpenEventLog opens (or creates) the event log at path.
func OpenEventLog(path string, maxBytes int64) (*EventLog, error) {
	if maxBytes <= 0 {
		maxBytes = 1 << 20 // 1 MiB ≈ a few thousand events
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &EventLog{path: path, maxBytes: maxBytes, f: f, size: st.Size()}, nil
}

// Append writes one event. Errors are returned but callers may treat them
// as non-fatal: a full disk shouldn't take the live stream down.
func (l *EventLog) Append(e Event) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size+int64(len(b)) > l.maxBytes {
		if err := l.rotateLocked(); err != nil {
			return err
		}
	}
	n, err := l.f.Write(b)
	l.size += int64(n)
	return err
}

func (l *EventLog) rotateLocked() error {
	l.f.Close()
	if err := os.Rename(l.path, l.path+".old"); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	l.f, l.size = f, 0
	return nil
}

// LoadRecent reads up to n most-recent events (oldest first), spanning the
// rotation boundary: the .old generation is read first if the current file
// alone can't fill n. Corrupt lines are skipped, not fatal — a partial
// line from a crash mid-write must not poison history.
func LoadRecent(path string, n int) ([]Event, error) {
	var out []Event
	for _, p := range []string{path + ".old", path} {
		evs, err := readJSONL(p)
		if err != nil {
			continue // missing generation is normal
		}
		out = append(out, evs...)
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out, nil
}

func readJSONL(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 512*1024)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Msg != "" {
			out = append(out, e)
		}
	}
	return out, nil
}

// Close flushes and closes the underlying file.
func (l *EventLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}
