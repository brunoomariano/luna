package ledger

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// maxLine bounds one record.
//
// A write under PIPE_BUF (4096 on Linux) to a file opened with O_APPEND lands
// whole, which is what lets several processes share this file with no lock. A
// line that would exceed it is refused rather than truncated: a torn line is the
// one failure this format cannot recover from, and INV-2 says so.
const maxLine = 4000

// ErrLineTooLong is returned when a record would not append atomically.
var ErrLineTooLong = errors.New("this line is too long to append atomically")

// Ledger is the append-only record, one JSON object per line.
type Ledger struct {
	// Path is the file. Its directory is proven durable before the first write.
	Path string

	// Now is the clock, so a test can hold it still. Nil means time.Now.
	Now func() time.Time
}

// Default is where the ledger lives when nobody says otherwise: outside every
// checkout, one file for every project.
func Default() string {
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			// Nowhere to put it. The caller's durability check reports this
			// properly; answering with a relative path here would write a ledger
			// into whatever directory the process happened to start in.
			return filepath.Join(".luna-ledger", "ledger.jsonl")
		}
		data = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(data, "luna", "ledger.jsonl")
}

func (l Ledger) now() time.Time {
	if l.Now == nil {
		return time.Now()
	}
	return l.Now()
}

// Append records one line.
//
// The durability check runs on every call rather than once per process: a caller
// that checked at startup and wrote an hour later would be trusting a mount that
// could have changed, and the check is a single statfs.
//
// O_APPEND is what makes the write atomic between processes. There is no lock,
// and deliberately so — a lock file in a directory a sandbox maps read-write is
// one more thing to leak when a jailed process dies.
func (l Ledger) Append(e Entry) error {
	if err := RequireDurable(l.Path); err != nil {
		return err
	}
	if e.At.IsZero() {
		e.At = l.now()
	}
	if err := e.Validate(); err != nil {
		return err
	}

	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("writing a ledger line for %s: %w", e.Run, err)
	}
	if len(line)+1 > maxLine {
		return fmt.Errorf("%w: %d bytes, and the limit is %d — shorten the note or the detail",
			ErrLineTooLong, len(line)+1, maxLine)
	}

	f, err := os.OpenFile(l.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening the ledger at %s: %w", l.Path, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("appending to the ledger at %s: %w", l.Path, err)
	}
	return nil
}

// Read returns every line, oldest first.
//
// A line that cannot be parsed is reported rather than skipped. Skipping is how a
// record quietly stops being the record: the reader would answer confidently from
// whatever remained readable, which is the silent failure INV-5 forbids.
func (l Ledger) Read() ([]Entry, error) {
	f, err := os.Open(l.Path)
	if errors.Is(err, fs.ErrNotExist) {
		// No ledger yet is not an error. It is a machine where nothing has run.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the ledger at %s: %w", l.Path, err)
	}
	defer func() { _ = f.Close() }()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, maxLine), maxLine)

	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(text), &e); err != nil {
			return nil, fmt.Errorf("%s:%d is not a readable ledger line: %w", l.Path, line, err)
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading the ledger at %s: %w", l.Path, err)
	}
	return entries, nil
}

// State answers where one run stands: its most recent line.
//
// A tail rather than a fold. Nothing is reconstructed, because Luna decides no
// transitions and therefore has no state to rebuild — only a position to report.
func (l Ledger) State(run string) (Entry, bool, error) {
	entries, err := l.Read()
	if err != nil {
		return Entry{}, false, err
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Run == run {
			return entries[i], true, nil
		}
	}
	return Entry{}, false, nil
}

// Run is one run as a report sees it: its latest line, and what it took.
type Run struct {
	// Latest is the most recent line for this run.
	Latest Entry

	// Started is when its first line was written.
	Started time.Time

	// Lines is how many lines it has.
	Lines int

	// Failed is how many checks failed across the whole run. It is the number
	// worth seeing beside a run that is still going.
	Failed int
}

// Status is where the run stands, which is whatever its latest line said.
func (r Run) Status() Status { return r.Latest.Status }

// Report groups the ledger by run, most recently touched first.
//
// `since` bounds it; a zero duration means everything. Ordering is by last
// activity because the question a report answers is "what needs me now", and the
// run that moved most recently is the one most likely to.
func (l Ledger) Report(since time.Duration) ([]Run, error) {
	entries, err := l.Read()
	if err != nil {
		return nil, err
	}

	cutoff := time.Time{}
	if since > 0 {
		cutoff = l.now().Add(-since)
	}

	byRun := groupByRun(entries, cutoff)

	runs := make([]Run, 0, len(byRun))
	for _, run := range byRun {
		runs = append(runs, *run)
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].Latest.At.Equal(runs[j].Latest.At) {
			return runs[i].Latest.Run < runs[j].Latest.Run
		}
		return runs[i].Latest.At.After(runs[j].Latest.At)
	})
	return runs, nil
}

// groupByRun folds the lines into one Run each, dropping what falls outside the
// window.
func groupByRun(entries []Entry, cutoff time.Time) map[string]*Run {
	byRun := map[string]*Run{}
	for _, e := range entries {
		if !cutoff.IsZero() && e.At.Before(cutoff) {
			continue
		}
		run, seen := byRun[e.Run]
		if !seen {
			run = &Run{Started: e.At}
			byRun[e.Run] = run
		}
		run.Latest = e
		run.Lines++
		if e.Event == EventCheck && e.Verdict == string(VerdictFailed) {
			run.Failed++
		}
	}
	return byRun
}

// NeedsSomebody reports whether a run is waiting on a person.
//
// The two statuses are kept apart everywhere else because they are different
// questions — one has an artifact to judge, the other a question to answer — and
// joined here because a report answers only "does this need me".
func (r Run) NeedsSomebody() bool {
	return r.Status() == StatusBlocked || r.Status() == StatusAwaitingGate
}
