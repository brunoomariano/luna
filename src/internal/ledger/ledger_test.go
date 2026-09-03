package ledger_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brunoomariano/luna/src/internal/ledger"
)

// at is a fixed clock, so a test can assert on ordering without racing one.
func at(minute int) time.Time {
	return time.Date(2026, 9, 2, 14, minute, 0, 0, time.UTC)
}

// newLedger puts a ledger on durable storage, or skips.
//
// Not t.TempDir(): /tmp is tmpfs on this machine and on any systemd default, and
// the durability guard refuses it — correctly. See durable_test.go.
func newLedger(t *testing.T) ledger.Ledger {
	t.Helper()
	return ledger.Ledger{
		Path: filepath.Join(diskDir(t), "ledger.jsonl"),
		Now:  func() time.Time { return at(0) },
	}
}

func phase(run, name string, status ledger.Status) ledger.Entry {
	return ledger.Entry{Run: run, Phase: name, Event: ledger.EventPhase, Status: status}
}

func TestALineIsWrittenAndReadBackWhole(t *testing.T) {
	l := newLedger(t)

	want := ledger.Entry{
		Run: "MAX-2", Project: "github.com/me/app", Phase: "forge",
		Event: ledger.EventCheck, Artifact: "ci_green", Verdict: "passed",
		Scope: "full", Command: "make ci", Round: 2,
		Worktree: "/repos/wt-app-MAX-2",
	}
	if err := l.Append(want); err != nil {
		t.Fatalf("appending: %v", err)
	}

	got, err := l.Read()
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d lines, want 1", len(got))
	}
	want.At = at(0)
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("the line came back changed:\n got %+v\nwant %+v", got[0], want)
	}
}

// The state is a tail, not a fold: INV-2.
func TestTheStateOfARunIsItsMostRecentLine(t *testing.T) {
	l := newLedger(t)
	for i, e := range []ledger.Entry{
		phase("MAX-2", "intake", ledger.StatusRunning),
		phase("MAX-2", "plan", ledger.StatusRunning),
		phase("MAX-2", "forge", ledger.StatusAwaitingGate),
	} {
		e.At = at(i)
		if err := l.Append(e); err != nil {
			t.Fatalf("appending: %v", err)
		}
	}

	got, found, err := l.State("MAX-2")
	if err != nil || !found {
		t.Fatalf("state: found=%v err=%v", found, err)
	}
	if got.Phase != "forge" || got.Status != ledger.StatusAwaitingGate {
		t.Errorf("got %s/%s, want forge/awaiting_gate", got.Phase, got.Status)
	}
}

func TestOneRunsLinesDoNotAnswerForAnother(t *testing.T) {
	l := newLedger(t)
	first := phase("MAX-2", "forge", ledger.StatusRunning)
	first.At = at(0)
	second := phase("FIX-9", "intake", ledger.StatusBlocked)
	second.At = at(1)
	for _, e := range []ledger.Entry{first, second} {
		if err := l.Append(e); err != nil {
			t.Fatalf("appending: %v", err)
		}
	}

	got, _, err := l.State("MAX-2")
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != "forge" {
		t.Errorf("a later line from another run answered for this one: %+v", got)
	}
}

func TestAnUnknownRunHasNoState(t *testing.T) {
	l := newLedger(t)
	_, found, err := l.State("NOBODY-1")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Error("a run that never ran reported a state")
	}
}

// A machine where nothing has run is not an error.
func TestAMissingLedgerReadsAsEmptyRatherThanFailing(t *testing.T) {
	l := ledger.Ledger{Path: filepath.Join(diskDir(t), "never-written.jsonl")}

	got, err := l.Read()
	if err != nil {
		t.Fatalf("a missing ledger was an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d lines from a ledger that does not exist", len(got))
	}
}

// Skipping an unreadable line is how a record quietly stops being the record.
func TestAnUnreadableLineIsReportedRatherThanSkipped(t *testing.T) {
	l := newLedger(t)
	if err := l.Append(phase("MAX-2", "forge", ledger.StatusRunning)); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(l.Path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{this is not json}\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	if _, err := l.Read(); err == nil {
		t.Fatal("a corrupt line was skipped, and the reader answered from what was left")
	}
}

// The record is append-only: INV-2. Nothing here rewrites a line, and a second
// append leaves the first exactly as it was.
func TestAppendingNeverRewritesWhatIsAlreadyThere(t *testing.T) {
	l := newLedger(t)
	first := phase("MAX-2", "intake", ledger.StatusRunning)
	first.At = at(0)
	if err := l.Append(first); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}

	second := phase("MAX-2", "plan", ledger.StatusRunning)
	second.At = at(1)
	if err := l.Append(second); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(string(after), string(before)) {
		t.Error("the second append changed what the first had written")
	}
}

// The fleet: several processes share this file with no lock, and O_APPEND is
// what makes that safe. Processes rather than goroutines, because a lock this
// design does not have would still make goroutines pass.
func TestSeveralProcessesAppendWithoutCorruptingEachOther(t *testing.T) {
	l := newLedger(t)

	const writers, each = 8, 60
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			line := `{"at":"2026-09-02T14:00:00Z","run":"RUN-` + string(rune('A'+w)) + `","event":"phase","phase":"forge"}`
			script := "for i in $(seq 1 " + itoa(each) + "); do printf '%s\\n' '" + line + "' >> " + l.Path + "; done"
			if out, err := exec.Command("sh", "-c", script).CombinedOutput(); err != nil {
				t.Errorf("writer %d: %v: %s", w, err, out)
			}
		}(w)
	}
	wg.Wait()

	got, err := l.Read()
	if err != nil {
		t.Fatalf("the ledger did not survive concurrent writers: %v", err)
	}
	if len(got) != writers*each {
		t.Errorf("got %d lines, want %d", len(got), writers*each)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// A torn line is the one failure this format cannot recover from, so a line that
// would not append atomically is refused before it is written.
func TestALineTooLongToAppendAtomicallyIsRefused(t *testing.T) {
	l := newLedger(t)

	err := l.Append(ledger.Entry{
		Run:   "MAX-2",
		Event: ledger.EventCheck,
		Note:  strings.Repeat("x", 5000),
	})
	if err == nil {
		t.Fatal("an oversized line was appended, and a concurrent write could tear it")
	}
	if !errors.Is(err, ledger.ErrLineTooLong) {
		t.Errorf("the refusal is not recognisable: %v", err)
	}
	if got, _ := l.Read(); len(got) != 0 {
		t.Error("the refused line was written anyway")
	}
}

// The boundary itself, because the guard is what stands between this format and
// its one unrecoverable failure. Mutation testing found it: `>` mutated to `>=`
// survived the whole suite, and under it a line exactly at the limit — which is
// safe — would have been refused.
func TestALineExactlyAtTheLimitIsAccepted(t *testing.T) {
	l := newLedger(t)

	// Grow a note until the line is one byte under the cap, then keep it.
	fits := ""
	for size := 0; ; size++ {
		e := ledger.Entry{At: at(0), Run: "MAX-2", Event: ledger.EventCheck, Note: strings.Repeat("x", size)}
		encoded, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded)+1 > 4000 {
			break
		}
		fits = strings.Repeat("x", size)
	}

	if err := l.Append(ledger.Entry{
		At: at(0), Run: "MAX-2", Event: ledger.EventCheck, Note: fits,
	}); err != nil {
		t.Fatalf("a line at the limit was refused: %v", err)
	}
	if got := h(t, l); got != 1 {
		t.Errorf("got %d lines, want 1", got)
	}
}

func h(t *testing.T, l ledger.Ledger) int {
	t.Helper()
	got, err := l.Read()
	if err != nil {
		t.Fatal(err)
	}
	return len(got)
}

func TestALineWithNoRunIsRefused(t *testing.T) {
	l := newLedger(t)
	if err := l.Append(ledger.Entry{Event: ledger.EventPhase}); err == nil {
		t.Fatal("a line nothing can be grouped by was recorded")
	}
}

func TestAnUnknownEventIsRefused(t *testing.T) {
	l := newLedger(t)
	err := l.Append(ledger.Entry{Run: "MAX-2", Event: ledger.Event("finished-ish")})
	if err == nil || !strings.Contains(err.Error(), "unknown event") {
		t.Fatalf("an unknown event was recorded: %v", err)
	}
}

// INV-5: the account of where the answer was looked for is what separates a real
// block from an unread file.
func TestABlockMustSayWhereTheAnswerWasLookedFor(t *testing.T) {
	l := newLedger(t)

	err := l.Append(ledger.Entry{
		Run: "MAX-2", Event: ledger.EventBlock, Status: ledger.StatusBlocked,
		Question: "is --largest meant to return an argument?",
		Needs:    "which of the two readings holds",
	})
	if err == nil {
		t.Fatal("a block with no account of what was consulted was recorded")
	}
	if !strings.Contains(err.Error(), "unread file") {
		t.Errorf("the refusal does not say why the account matters: %v", err)
	}
}

func TestACompleteBlockIsRecorded(t *testing.T) {
	l := newLedger(t)

	err := l.Append(ledger.Entry{
		Run: "MAX-2", Phase: "forge", Event: ledger.EventBlock, Status: ledger.StatusBlocked,
		Question: "is --largest meant to return an argument?",
		Looked:   []string{"the contract, clause 4 — says \"the largest\", undefined with --max", "tests/ — the combination is not covered"},
		Needs:    "which of the two readings holds",
	})
	if err != nil {
		t.Fatalf("a complete block was refused: %v", err)
	}

	got, _, err := l.State("MAX-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Looked) != 2 || got.Status != ledger.StatusBlocked {
		t.Errorf("the block came back changed: %+v", got)
	}
}

func TestAReportGroupsByRunMostRecentlyTouchedFirst(t *testing.T) {
	l := newLedger(t)
	for i, e := range []ledger.Entry{
		phase("OLD-1", "close", ledger.StatusDone),
		phase("MAX-2", "forge", ledger.StatusRunning),
		phase("FIX-9", "intake", ledger.StatusBlocked),
	} {
		e.At = at(i)
		if err := l.Append(e); err != nil {
			t.Fatal(err)
		}
	}

	runs, err := l.Report(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Fatalf("got %d runs, want 3", len(runs))
	}
	if runs[0].Latest.Run != "FIX-9" {
		t.Errorf("the most recently touched run is not first: %s", runs[0].Latest.Run)
	}
}

func TestAReportCountsWhatFailedAndWhoNeedsSomebody(t *testing.T) {
	l := newLedger(t)
	lines := []ledger.Entry{
		{Run: "MAX-2", Event: ledger.EventCheck, Artifact: "ci_green", Verdict: "failed"},
		{Run: "MAX-2", Event: ledger.EventCheck, Artifact: "ci_green", Verdict: "passed"},
		{Run: "MAX-2", Event: ledger.EventPhase, Phase: "forge", Status: ledger.StatusAwaitingGate},
		{Run: "OK-1", Event: ledger.EventPhase, Phase: "close", Status: ledger.StatusDone},
	}
	for i, e := range lines {
		e.At = at(i)
		if err := l.Append(e); err != nil {
			t.Fatal(err)
		}
	}

	runs, err := l.Report(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		switch run.Latest.Run {
		case "MAX-2":
			if run.Failed != 1 {
				t.Errorf("MAX-2 failed count: got %d, want 1", run.Failed)
			}
			if !run.NeedsSomebody() {
				t.Error("a run awaiting a gate does not read as needing somebody")
			}
		case "OK-1":
			if run.NeedsSomebody() {
				t.Error("a finished run reads as needing somebody")
			}
		}
	}
}

func TestAReportCanBeBounded(t *testing.T) {
	l := newLedger(t)
	l.Now = func() time.Time { return at(30) }

	old := phase("OLD-1", "close", ledger.StatusDone)
	old.At = at(0)
	recent := phase("MAX-2", "forge", ledger.StatusRunning)
	recent.At = at(29)
	for _, e := range []ledger.Entry{old, recent} {
		if err := l.Append(e); err != nil {
			t.Fatal(err)
		}
	}

	runs, err := l.Report(10 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Latest.Run != "MAX-2" {
		t.Errorf("the bound did not hold: %+v", runs)
	}
}

// A batch prepares runs and stops; the seeded line is the whole handoff, and
// there is no file beside it.
func TestAPreparedRunIsFoundByTheSessionThatPicksItUp(t *testing.T) {
	l := newLedger(t)

	seeded := ledger.Entry{
		Run: "BATCH-3", Project: "github.com/me/app", Event: ledger.EventPhase,
		Status: ledger.StatusAwaitingResume, Phase: "intake",
		Worktree: "/repos/wt-app-BATCH-3",
	}
	if err := l.Append(seeded); err != nil {
		t.Fatal(err)
	}

	got, found, err := l.State("BATCH-3")
	if err != nil || !found {
		t.Fatalf("a prepared run could not be found: found=%v err=%v", found, err)
	}
	if got.Status != ledger.StatusAwaitingResume || got.Worktree == "" {
		t.Errorf("the handoff does not carry what a session needs to resume: %+v", got)
	}
}

// A simulation that reads like a result is a lie with the truth beside it.
func TestASimulatedLineSaysSo(t *testing.T) {
	l := newLedger(t)
	e := phase("DRY-1", "forge", ledger.StatusRunning)
	e.Simulated = true
	if err := l.Append(e); err != nil {
		t.Fatal(err)
	}

	got, _, err := l.State("DRY-1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Simulated {
		t.Error("a dry run's line came back looking like a real one")
	}
}

func TestTheDefaultLedgerLivesOutsideEveryCheckout(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/somewhere/data")

	if got := ledger.Default(); got != "/somewhere/data/luna/ledger.jsonl" {
		t.Errorf("got %q", got)
	}
}

func TestTheDefaultLedgerFallsBackToTheHomeDirectory(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")

	got := ledger.Default()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	want := filepath.Join(home, ".local", "share", "luna", "ledger.jsonl")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestALineCarriesItsOwnTimeWhenTheCallerSetsOne(t *testing.T) {
	l := newLedger(t)
	l.Now = nil // the real clock, so the entry's own time has to survive it

	stamped := phase("MAX-2", "forge", ledger.StatusRunning)
	stamped.At = at(7)
	if err := l.Append(stamped); err != nil {
		t.Fatal(err)
	}

	got, _, err := l.State("MAX-2")
	if err != nil {
		t.Fatal(err)
	}
	if !got.At.Equal(at(7)) {
		t.Errorf("the caller's time was overwritten: got %s", got.At)
	}
}

func TestALineWithNoTimeIsStampedOnTheWayIn(t *testing.T) {
	l := newLedger(t)

	if err := l.Append(phase("MAX-2", "forge", ledger.StatusRunning)); err != nil {
		t.Fatal(err)
	}
	got, _, err := l.State("MAX-2")
	if err != nil {
		t.Fatal(err)
	}
	if got.At.IsZero() {
		t.Error("a line was recorded with no time, so nothing can be ordered by it")
	}
}

func TestAnUnreadableTimestampIsReported(t *testing.T) {
	l := newLedger(t)
	if err := os.WriteFile(l.Path, []byte(`{"at":"yesterday","run":"MAX-2","event":"phase"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := l.Read(); err == nil {
		t.Fatal("a line with an unreadable timestamp was accepted")
	}
}

func TestALineWithNoTimestampStillReadsBack(t *testing.T) {
	l := newLedger(t)
	if err := os.WriteFile(l.Path, []byte(`{"run":"MAX-2","event":"phase","phase":"forge"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := l.Read()
	if err != nil {
		t.Fatalf("a line with no timestamp was refused: %v", err)
	}
	if len(got) != 1 || got[0].Phase != "forge" {
		t.Errorf("got %+v", got)
	}
}

func TestAnUnknownStatusIsRefused(t *testing.T) {
	l := newLedger(t)
	err := l.Append(ledger.Entry{Run: "MAX-2", Event: ledger.EventPhase, Status: ledger.Status("nearly-done")})
	if err == nil || !strings.Contains(err.Error(), "unknown status") {
		t.Fatalf("got %v", err)
	}
}

func TestABlockWithNoQuestionOrNoWayOutIsRefused(t *testing.T) {
	l := newLedger(t)

	err := l.Append(ledger.Entry{
		Run: "MAX-2", Event: ledger.EventBlock,
		Looked: []string{"the contract"}, Needs: "an answer",
	})
	if err == nil || !strings.Contains(err.Error(), "no question") {
		t.Errorf("a block with no question: %v", err)
	}

	err = l.Append(ledger.Entry{
		Run: "MAX-2", Event: ledger.EventBlock,
		Question: "which reading?", Looked: []string{"the contract"},
	})
	if err == nil || !strings.Contains(err.Error(), "what would unblock") {
		t.Errorf("a block with no way out: %v", err)
	}
}

func TestBlankLinesInTheLedgerAreSkipped(t *testing.T) {
	l := newLedger(t)
	body := `{"at":"2026-09-02T14:00:00Z","run":"MAX-2","event":"phase"}` + "\n\n   \n" +
		`{"at":"2026-09-02T14:01:00Z","run":"MAX-2","event":"phase"}` + "\n"
	if err := os.WriteFile(l.Path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := l.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("got %d lines, want 2", len(got))
	}
}
