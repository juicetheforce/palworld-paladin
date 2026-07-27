package maintain

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Journal records a cycle's intent and progress BEFORE each mutating step
// (invariant I3). On tool startup, an unclosed journal means the tool
// crashed mid-cycle: recovery mode reports the exact step reached and the
// anchors; it never silently resumes mutations (§6.9).
type Journal interface {
	Begin(cycleID, kind string) error
	StepStarted(cycleID, step string)
	StepOK(cycleID, step string)
	StepFailed(cycleID, step, detail string)
	Close(cycleID, outcome, detail string) error
}

// journalEntry is one JSON line in the journal file.
type journalEntry struct {
	Time    time.Time `json:"time"`
	CycleID string    `json:"cycle_id"`
	Kind    string    `json:"kind,omitempty"` // on begin: "commit"/"restore"
	Event   string    `json:"event"`          // begin|step_started|step_ok|step_failed|close
	Step    string    `json:"step,omitempty"`
	Detail  string    `json:"detail,omitempty"`
	Outcome string    `json:"outcome,omitempty"` // on close
}

// FileJournal writes JSON lines to <dir>/active.journal, fsyncing each
// entry, and renames the file to <dir>/<cycleID>.journal on Close. An
// active.journal present at startup IS the crash signal.
type FileJournal struct {
	Dir string
	f   *os.File
}

// NewFileJournal ensures dir exists and returns a FileJournal.
func NewFileJournal(dir string) (*FileJournal, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("journal dir: %w", err)
	}
	return &FileJournal{Dir: dir}, nil
}

func (j *FileJournal) activePath() string { return filepath.Join(j.Dir, "active.journal") }

func (j *FileJournal) write(e journalEntry) {
	if j.f == nil {
		return
	}
	e.Time = time.Now()
	b, _ := json.Marshal(e)
	j.f.Write(append(b, '\n'))
	j.f.Sync() // the journal is only worth having if it survives a crash
}

func (j *FileJournal) Begin(cycleID, kind string) error {
	if _, err := os.Stat(j.activePath()); err == nil {
		return fmt.Errorf("journal: unclosed cycle already present at %s — recover before starting a new cycle", j.activePath())
	}
	f, err := os.OpenFile(j.activePath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("journal begin: %w", err)
	}
	j.f = f
	j.write(journalEntry{CycleID: cycleID, Kind: kind, Event: "begin"})
	return nil
}

func (j *FileJournal) StepStarted(cycleID, step string) {
	j.write(journalEntry{CycleID: cycleID, Event: "step_started", Step: step})
}
func (j *FileJournal) StepOK(cycleID, step string) {
	j.write(journalEntry{CycleID: cycleID, Event: "step_ok", Step: step})
}
func (j *FileJournal) StepFailed(cycleID, step, detail string) {
	j.write(journalEntry{CycleID: cycleID, Event: "step_failed", Step: step, Detail: detail})
}

func (j *FileJournal) Close(cycleID, outcome, detail string) error {
	j.write(journalEntry{CycleID: cycleID, Event: "close", Outcome: outcome, Detail: detail})
	if j.f != nil {
		j.f.Close()
		j.f = nil
	}
	dest := filepath.Join(j.Dir, cycleID+".journal")
	if err := os.Rename(j.activePath(), dest); err != nil {
		return fmt.Errorf("journal close rename: %w", err)
	}
	return nil
}

// UnclosedCycle describes a crash-interrupted cycle found at startup.
type UnclosedCycle struct {
	CycleID  string
	Kind     string
	LastStep string // the step that was in flight when the tool died
	Entries  []journalEntry
}

// ReadUnclosed reports the unclosed cycle in dir, if any. (nil, nil)
// means a clean start.
func ReadUnclosed(dir string) (*UnclosedCycle, error) {
	path := filepath.Join(dir, "active.journal")
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("journal read: %w", err)
	}
	defer f.Close()

	u := &UnclosedCycle{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e journalEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue // a torn final line is expected after a crash
		}
		u.Entries = append(u.Entries, e)
		switch e.Event {
		case "begin":
			u.CycleID, u.Kind = e.CycleID, e.Kind
		case "step_started":
			u.LastStep = e.Step
		case "close":
			// A close line in active.journal means the rename failed but
			// the cycle finished; still surface it for the operator.
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("journal scan: %w", err)
	}
	return u, nil
}
