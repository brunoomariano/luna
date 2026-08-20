package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestProveRecordsWhatTheCommandAnswered covers the happy path end to end: a
// command that exits zero produces evidence carrying the verdict, the exit code,
// the command verbatim, the scope the verifier declared, and the log position.
//
// Each of those fields answers a question the audit will ask later, and evidence
// missing any of them says a stage closed without saying on what grounds it did.
func TestProveRecordsWhatTheCommandAnswered(t *testing.T) {
	shell := Shell{Dir: t.TempDir()}

	got, err := shell.Prove(context.Background(), fsm.Command{Run: "true", Scope: fsm.ScopeFull}, 7)
	if err != nil {
		t.Fatalf("a command that runs and succeeds is not an error: %v", err)
	}

	if got.Verdict != fsm.VerdictPassed {
		t.Errorf("want passed, got %q", got.Verdict)
	}
	if got.ExitCode != 0 {
		t.Errorf("want exit 0, got %d", got.ExitCode)
	}
	if got.Command != "true" {
		t.Errorf("the audit needs the command verbatim, got %q", got.Command)
	}
	if got.Scope != fsm.ScopeFull {
		t.Errorf("the scope is what the verifier declared, got %q", got.Scope)
	}
	if got.RecordedAt != 7 {
		t.Errorf("the log position is what staleness compares against, got %d", got.RecordedAt)
	}
}

// TestProveHonoursTheDeclaredScope covers the direction verification runs in: the
// node layer reports the scope the contract declared and never invents a wider one.
//
// A targeted run that came back as full would be exactly the laundering the
// scope exists to prevent, and this is the only place it could be introduced.
func TestProveHonoursTheDeclaredScope(t *testing.T) {
	cases := []struct {
		name    string
		command fsm.Command
		want    fsm.Scope
	}{
		{"declared full", fsm.Command{Run: "true", Scope: fsm.ScopeFull}, fsm.ScopeFull},
		{"declared targeted", fsm.Command{Run: "true", Scope: fsm.ScopeTargeted}, fsm.ScopeTargeted},
		{"unstated falls to the cautious one", fsm.Command{Run: "true"}, fsm.ScopeTargeted},
	}

	shell := Shell{Dir: t.TempDir()}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := shell.Prove(context.Background(), c.command, 1)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Scope != c.want {
				t.Errorf("want scope %q, got %q", c.want, got.Scope)
			}
		})
	}
}

// TestAFailingCommandIsAVerdictNotAnError covers the distinction the whole file
// turns on: a non-zero exit is the tool's answer, so it comes back as evidence.
//
// Returning an error here would make the caller unable to tell "the tests ran
// and lost" from "the check never happened", and the reducer needs the first to
// be a recordable fact.
func TestAFailingCommandIsAVerdictNotAnError(t *testing.T) {
	shell := Shell{Dir: t.TempDir()}

	got, err := shell.Prove(context.Background(), fsm.Command{Run: "exit 3", Scope: fsm.ScopeFull}, 2)
	if err != nil {
		t.Fatalf("a check that ran and failed is not a failure to ask: %v", err)
	}

	if got.Verdict != fsm.VerdictFailed {
		t.Errorf("want failed, got %q", got.Verdict)
	}
	if got.ExitCode != 3 {
		t.Errorf("the exit code is the tool's answer, got %d", got.ExitCode)
	}
	if !got.Delivered() {
		t.Error("a failed check still produced a record")
	}
}

// TestAVerifierThatExecutesNothingRunsNothing covers the honest floor:
// existence is a claim about delivery, not about a check.
//
// The proof is a directory that does not exist. Any attempt to run a command
// there would fail, so a successful call is evidence that nothing was executed —
// and the scope stays existence rather than being upgraded to a passing check.
func TestAVerifierThatExecutesNothingRunsNothing(t *testing.T) {
	shell := Shell{Dir: filepath.Join(t.TempDir(), "no-such-worktree")}

	got, err := shell.Prove(context.Background(), fsm.Existence{}, 5)
	if err != nil {
		t.Fatalf("existence does not touch the filesystem: %v", err)
	}

	if got.Scope != fsm.ScopeExistence {
		t.Errorf("want existence, got %q", got.Scope)
	}
	if got.Verdict != fsm.VerdictPassed {
		t.Errorf("the artifact was delivered, got %q", got.Verdict)
	}
	if got.Command != "" {
		t.Errorf("nothing ran, so there is no command to name, got %q", got.Command)
	}
	if got.RecordedAt != 5 {
		t.Errorf("the log position is recorded either way, got %d", got.RecordedAt)
	}
}

// TestACheckThatNeverHappenedIsAnError covers the other side of the same line: a
// command that could not run at all produces an error, never failing evidence.
//
// A deadline is the clearest case — the check did not finish, so there is
// nothing to conclude about the work. Recording it as VerdictFailed would tell
// the audit the tests ran and lost, which is a lie the log would keep forever.
func TestACheckThatNeverHappenedIsAnError(t *testing.T) {
	shell := Shell{Dir: t.TempDir(), Timeout: 50 * time.Millisecond}

	got, err := shell.Prove(context.Background(), fsm.Command{Run: "sleep 5", Scope: fsm.ScopeFull}, 9)
	if err == nil {
		t.Fatalf("a timeout is not a verdict, got evidence %+v", got)
	}

	if !strings.Contains(err.Error(), "did not finish within") {
		t.Errorf("the error should say the deadline hit, got %q", err)
	}
	if !strings.Contains(err.Error(), "50ms") {
		t.Errorf("the error should carry the deadline that was applied, got %q", err)
	}
	if got.Delivered() {
		t.Errorf("nothing was observed, so the evidence is zero, got %+v", got)
	}
	if got.Verdict == fsm.VerdictFailed {
		t.Error("a check that never happened must not read as a check that lost")
	}
}

// TestTheCommandRunsInTheWorktree covers the promise of Shell.Dir: the check is
// run against the task's worktree, not against whatever directory the process
// happens to sit in.
//
// A verification that silently ran somewhere else would produce evidence about
// the wrong tree, which is worse than no evidence at all.
func TestTheCommandRunsInTheWorktree(t *testing.T) {
	worktree := t.TempDir()
	marker := filepath.Join(worktree, "marker")
	if err := os.WriteFile(marker, []byte("delivered\n"), 0o600); err != nil {
		t.Fatalf("preparing the worktree: %v", err)
	}

	shell := Shell{Dir: worktree}

	inside, err := shell.Prove(context.Background(), fsm.Command{Run: "test -f marker", Scope: fsm.ScopeFull}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inside.Verdict != fsm.VerdictPassed {
		t.Errorf("the marker is in the worktree, so the check passes, got %q", inside.Verdict)
	}

	// The same command in a different tree must not find it — otherwise the pass
	// above would prove nothing about Dir.
	elsewhere := Shell{Dir: t.TempDir()}
	outside, err := elsewhere.Prove(context.Background(), fsm.Command{Run: "test -f marker", Scope: fsm.ScopeFull}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outside.Verdict != fsm.VerdictFailed {
		t.Errorf("another worktree has no marker, got %q", outside.Verdict)
	}
}

// TestACommandThatCannotStartIsAnError covers the non-deadline half of "the
// check never happened": a worktree that is not there.
//
// The shell itself cannot be started, and that is a failure to ask, not an
// answer — so it must not arrive as evidence at all.
func TestACommandThatCannotStartIsAnError(t *testing.T) {
	shell := Shell{Dir: filepath.Join(t.TempDir(), "no-such-worktree")}

	got, err := shell.Prove(context.Background(), fsm.Command{Run: "true", Scope: fsm.ScopeFull}, 1)
	if err == nil {
		t.Fatalf("a missing worktree cannot produce a verdict, got %+v", got)
	}
	if !strings.Contains(err.Error(), "true") {
		t.Errorf("the error should name the command that could not run, got %q", err)
	}
	if got.Delivered() {
		t.Errorf("nothing was observed, so the evidence is zero, got %+v", got)
	}
}

// TestAPassingCheckCarriesNoDetail covers why success is quiet: the exit code
// already says everything, and copying a green suite's output into the log
// buries the records that matter.
func TestAPassingCheckCarriesNoDetail(t *testing.T) {
	repo, commit := deliveredRepo(t)
	shell := Shell{Dir: repo, Commit: commit}

	got, err := shell.Prove(context.Background(), fsm.Command{Run: "echo everything is fine", Scope: fsm.ScopeFull}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Detail != "" {
		t.Errorf("a passing check needs no detail, got %q", got.Detail)
	}
}

// TestAFailingCheckCarriesTheTail covers where a human looks after a red run.
//
// The reason a build failed is at the end of its output, not in its banner, so
// the summary keeps the last lines and drops the head.
func TestAFailingCheckCarriesTheTail(t *testing.T) {
	repo, commit := deliveredRepo(t)
	shell := Shell{Dir: repo, Commit: commit}
	command := fsm.Command{
		Run:   "echo banner; echo noise; echo third-last; echo second-last; echo the real reason; exit 1",
		Scope: fsm.ScopeFull,
	}

	got, err := shell.Prove(context.Background(), command, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(got.Detail, "the real reason") {
		t.Errorf("the last line is why it failed, got %q", got.Detail)
	}
	if strings.Contains(got.Detail, "banner") {
		t.Errorf("the head is not the reason, got %q", got.Detail)
	}
	if strings.Contains(got.Detail, "noise") {
		t.Errorf("only the tail is kept, got %q", got.Detail)
	}
}

// TestSummariseKeepsTheLastLines covers the tail rule directly, where stating
// the expected string is clearer than reading it out of a shell's output.
//
// Three lines is the budget, joined so one evidence record stays one line.
func TestSummariseKeepsTheLastLines(t *testing.T) {
	output := "first\nsecond\nthird\nfourth\nfifth\n"

	if got := summarise(output, fsm.VerdictFailed); got != "third · fourth · fifth" {
		t.Errorf("want the last three lines joined, got %q", got)
	}

	// Fewer lines than the budget are kept whole — there is nothing to drop.
	if got := summarise("only\ntwo", fsm.VerdictFailed); got != "only · two" {
		t.Errorf("want both lines, got %q", got)
	}
}

// TestSummariseIsSilentWhenThereIsNothingToSay covers the two cases that produce
// no detail at all: a verdict that does not need one, and output that is blank.
//
// An empty Detail is a positive statement — there is nothing here worth reading
// — and it stays distinguishable from whitespace a tool happened to print.
func TestSummariseIsSilentWhenThereIsNothingToSay(t *testing.T) {
	cases := []struct {
		name    string
		output  string
		verdict fsm.Verdict
	}{
		{"a passing check says it with the exit code", "lots of green output", fsm.VerdictPassed},
		{"a passing check with no output either", "", fsm.VerdictPassed},
		{"a failing check that printed nothing", "", fsm.VerdictFailed},
		{"a failing check that printed only whitespace", "  \n\t\n ", fsm.VerdictFailed},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := summarise(c.output, c.verdict); got != "" {
				t.Errorf("want no detail, got %q", got)
			}
		})
	}
}

// TestSummariseIsCapped covers the bound on one evidence record.
//
// A stack trace or a diff dumped whole would make the log unreadable and the
// store expensive; the record points at the failure, it does not reproduce it.
func TestSummariseIsCapped(t *testing.T) {
	long := strings.Repeat("x", 1000)

	got := summarise(long, fsm.VerdictFailed)
	if len(got) != 300 {
		t.Errorf("want the detail capped at 300 characters, got %d", len(got))
	}
	if got != long[:300] {
		t.Error("the cap keeps the beginning of the kept tail, not a rewritten string")
	}

	// A detail already inside the budget is left alone.
	short := strings.Repeat("y", 299)
	if summarise(short, fsm.VerdictFailed) != short {
		t.Error("a short detail is not truncated")
	}
}

// TestZeroTimeoutFallsBackToTheDefault covers the guard against a wedged path:
// every external process gets a deadline, including one nobody configured.
//
// The assertion is on the constant rather than on waiting for it — the point is
// that an unset Timeout does not mean "no deadline".
func TestZeroTimeoutFallsBackToTheDefault(t *testing.T) {
	if DefaultTimeout <= 0 {
		t.Fatalf("a default of %s would leave a hang unbounded", DefaultTimeout)
	}

	// A zero Timeout still runs, so the fallback is a real duration rather than
	// an immediately expired context.
	repo, commit := deliveredRepo(t)
	shell := Shell{Dir: repo, Commit: commit}
	if shell.Timeout != 0 {
		t.Fatalf("this test is about the unset field, got %s", shell.Timeout)
	}

	got, err := shell.Prove(context.Background(), fsm.Command{Run: "true", Scope: fsm.ScopeFull}, 1)
	if err != nil {
		t.Fatalf("an unset timeout must not expire the command: %v", err)
	}
	if got.Verdict != fsm.VerdictPassed {
		t.Errorf("want passed, got %q", got.Verdict)
	}

	// And the deadline that would be reported is the constant, not the zero the
	// caller left in the field.
	tight := Shell{Dir: repo, Commit: commit, Timeout: 50 * time.Millisecond}
	_, err = tight.Prove(context.Background(), fsm.Command{Run: "sleep 5", Scope: fsm.ScopeFull}, 1)
	if err == nil {
		t.Fatal("the tight deadline should have hit")
	}
	if strings.Contains(err.Error(), DefaultTimeout.String()) {
		t.Errorf("a stated timeout wins over the default, got %q", err)
	}
}

// TestACancelledContextIsNotAVerdict covers the caller's own deadline: when the
// run is abandoned from outside, the check did not finish either.
//
// Luna cancels work when a task is stopped, and that must not leave a
// VerdictFailed behind claiming the work was checked and found wanting.
func TestACancelledContextIsNotAVerdict(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	repo, commit := deliveredRepo(t)
	shell := Shell{Dir: repo, Commit: commit}
	got, err := shell.Prove(ctx, fsm.Command{Run: "true", Scope: fsm.ScopeFull}, 1)
	if err == nil {
		t.Fatalf("an abandoned run has no verdict, got %+v", got)
	}
	if got.Delivered() {
		t.Errorf("nothing was observed, so the evidence is zero, got %+v", got)
	}
}

// TestProveSurvivesAWorktreeThatWentAway is the regression from the swarm bench,
// where it cost a task that had already delivered.
//
// A stage stalled, the worktree was removed with it — correctly, since a stage
// that ended has no tree — and every retry then failed on
// `chdir ...: no such file` rather than on the work. `make ci` was green on the
// agent's own commit.
//
// The fix is what this asserts: the checkout is cut from the repository at the
// delivered commit, and the repository outlives every stage.
func TestProveSurvivesAWorktreeThatWentAway(t *testing.T) {
	dir := repo(t)
	delivered := strings.TrimSpace(output(t, dir, "git", "rev-parse", "HEAD"))

	// The tree the stage worked in, gone the way it is gone after a stage ends.
	gone := filepath.Join(dir, "..", "wt-gone")
	run(t, dir, "git", "worktree", "add", "--detach", "-q", gone, "HEAD")
	run(t, dir, "git", "worktree", "remove", "--force", gone)

	// Pointed at the repository and the commit, which is what the node now passes.
	evidence, err := Shell{Dir: dir, Commit: delivered}.Prove(
		context.Background(), fsm.Command{Run: "test -f delivered.txt", Scope: fsm.ScopeFull}, 1,
	)
	if err != nil {
		t.Fatalf("verifying a stage whose worktree is gone: %v", err)
	}
	if !evidence.Passing() {
		t.Errorf("the check did not pass against the delivered commit: %+v", evidence)
	}
}

// TestProveVerifiesTheNamedCommitAndNotTheLatest is what the named commit buys.
//
// Two commits, and the check has to answer about the one the stage delivered.
// Reading HEAD instead would verify whatever landed most recently, which on a
// retry is a different stage's work.
func TestProveVerifiesTheNamedCommitAndNotTheLatest(t *testing.T) {
	dir := repo(t)
	first := strings.TrimSpace(output(t, dir, "git", "rev-parse", "HEAD"))

	write(t, dir, "later.txt", "a commit that came after")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "a later stage")

	evidence, err := Shell{Dir: dir, Commit: first}.Prove(
		context.Background(), fsm.Command{Run: "test -f later.txt", Scope: fsm.ScopeFull}, 1,
	)
	if err != nil {
		t.Fatalf("Prove: %v", err)
	}
	if evidence.Passing() {
		t.Error("the check saw a file from a commit the stage did not deliver")
	}
}

// TestADeclaredPathIsCheckedAgainstTheCommit is what checking a declared path
// against the commit is for.
//
// Measured on a real run before this existed: six of six artifacts closed with
// scope `existence`, which proves nothing — an agent that writes `Delivered:
// contract` and commits no file at all closed the stage green. That is the
// self-reported completion Luna rejects for status, one level down.
func TestADeclaredPathIsCheckedAgainstTheCommit(t *testing.T) {
	dir := repo(t)
	if err := os.MkdirAll(filepath.Join(dir, "reports"), 0o755); err != nil {
		t.Fatalf("making the directory: %v", err)
	}
	write(t, dir, "reports/dod.md", "the done-when list, clause by clause")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "verify: the report")
	commit := strings.TrimSpace(output(t, dir, "git", "rev-parse", "HEAD"))

	shell := Shell{Dir: dir, Commit: commit}
	evidence, err := shell.Prove(context.Background(), fsm.Existence{Path: "reports/"}, 1)
	if err != nil {
		t.Fatalf("proving a path that is there: %v", err)
	}

	if evidence.Verdict != fsm.VerdictPassed {
		t.Errorf("a committed file under the declared path did not pass: %+v", evidence)
	}
	// What was found, so a person reading the log can see what satisfied it.
	if !strings.Contains(evidence.Detail, "reports/dod.md") {
		t.Errorf("the evidence does not name what it found: %q", evidence.Detail)
	}
}

// TestAnEmptyPathIsTheAgentsWordAgainstItself is the failure that used to close
// green: the stage says it delivered and the directory is empty.
func TestAnEmptyPathIsTheAgentsWordAgainstItself(t *testing.T) {
	dir := repo(t)
	write(t, dir, "elsewhere.md", "committed, but not where the contract says")
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "verify: the wrong place")
	commit := strings.TrimSpace(output(t, dir, "git", "rev-parse", "HEAD"))

	shell := Shell{Dir: dir, Commit: commit}
	evidence, err := shell.Prove(context.Background(), fsm.Existence{Path: "reports/"}, 1)
	if err != nil {
		t.Fatalf("proving a path that is not there: %v", err)
	}

	if evidence.Verdict != fsm.VerdictFailed {
		t.Errorf("an empty directory passed: %+v", evidence)
	}
	if !strings.Contains(evidence.Detail, "reports/") {
		t.Errorf("the evidence does not say what was missing: %q", evidence.Detail)
	}
}

// TestAnUndeclaredPathChecksNothing keeps this additive. It is every artifact in
// the shipped stock today, and none of them may start failing.
func TestAnUndeclaredPathChecksNothing(t *testing.T) {
	shell := Shell{Dir: t.TempDir(), Commit: "not-a-commit"}

	evidence, err := shell.Prove(context.Background(), fsm.Existence{}, 1)
	if err != nil {
		t.Fatalf("an existence check with no path must not touch anything: %v", err)
	}
	if evidence.Verdict != fsm.VerdictPassed {
		t.Errorf("an undeclared path stopped passing: %+v", evidence)
	}
}

// TestNothingDeliveredYetIsNotAMissingArtifact. The first stage of the first task
// has no commit to look in, and reporting that as absence would block a task for
// not yet having done what it is about to do.
func TestNothingDeliveredYetIsNotAMissingArtifact(t *testing.T) {
	shell := Shell{Dir: repo(t)}

	evidence, err := shell.Prove(context.Background(), fsm.Existence{Path: "reports/"}, 1)
	if err != nil {
		t.Fatalf("proving with no commit: %v", err)
	}
	if evidence.Verdict != fsm.VerdictPassed {
		t.Errorf("a stage with nothing delivered was blamed for it: %+v", evidence)
	}
}

// TestAStageThatCommittedNothingDoesNotPassOnSomebodyElsesCode is INV-1 stated
// where it was actually broken.
//
// A stage that owes a command-proven artifact and committed nothing has not
// delivered. Before this, `Prove` passed the empty commit to `CheckoutAt`, which
// turned "" into "HEAD" and resolved it against `Shell.Dir` — the main repository
// — so the command ran over whatever was already there and exited zero.
//
// Measured on TALLY-3, with no kill involved: build was billed $0.56, recorded
// `tests_green passed (targeted) make test → 0`, and its branch sat on the base
// commit with a clean worktree. The green was true about code the stage did not
// write, which is the one thing verification exists to prevent.
func TestAStageThatCommittedNothingDoesNotPassOnSomebodyElsesCode(t *testing.T) {
	repo := repoWithCommit(t)

	// The repository's own HEAD passes the check. That is the trap: whatever is
	// already committed here is not what the stage owed.
	shell := Shell{Dir: repo, Commit: ""}

	got, err := shell.Prove(context.Background(), fsm.Command{Run: "true", Scope: fsm.ScopeTargeted}, 3)
	if err != nil {
		t.Fatalf("a stage with nothing committed is a failed delivery, not a broken machine: %v", err)
	}

	if got.Verdict == fsm.VerdictPassed {
		t.Fatalf("a stage that committed nothing passed a command check — the verdict "+
			"attests to code the stage did not write (evidence: %+v)", got)
	}
	if !strings.Contains(got.Detail, "committed nothing") {
		t.Errorf("the evidence must say why it failed, got %q", got.Detail)
	}
}

// deliveredRepo is a repository and the commit standing in for what a stage
// delivered.
//
// Tests about how a command *behaves* — its output, its deadline, its
// cancellation — need a delivery to reach the command at all, because a Shell
// with no commit now fails before running anything. That refusal is the subject
// of TestAStageThatCommittedNothingDoesNotPassOnSomebodyElsesCode; here it is
// only in the way.
func deliveredRepo(t *testing.T) (repo, commit string) {
	t.Helper()
	repo = repoWithCommit(t)

	sha, err := git(context.Background(), repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("reading the delivered commit: %v", err)
	}
	return repo, strings.TrimSpace(sha)
}

// TestAStageThatDeliveredNothingNewIsNotProvenByItsBase is the hole left in the
// first version of this guard.
//
// That one refused a command check when the stage had *no* commit. It does not
// fire when the stage has one and it is the base it started from: `Handover`
// reads HEAD of the worktree, which is the base until something is committed on
// top. So the check runs over the code the stage was given, passes because that
// code was already green, and records `targeted`.
//
// Measured on TALLY-5: build was billed $0.64 over 17 turns, its worktree ended
// clean at the base with no `--avg` anywhere, and `tests_green` closed
// `targeted` and `passed`. A stage that delivered nothing was proven by what it
// was handed.
func TestAStageThatDeliveredNothingNewIsNotProvenByItsBase(t *testing.T) {
	repo := repoWithCommit(t)

	base, err := git(context.Background(), repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("reading the base: %v", err)
	}
	at := strings.TrimSpace(base)

	// The stage was given `at` and hands back `at`: nothing was added on top.
	shell := Shell{Dir: repo, Commit: at, Base: at}

	got, err := shell.Prove(context.Background(), fsm.Command{Run: "true", Scope: fsm.ScopeTargeted}, 4)
	if err != nil {
		t.Fatalf("a stage that delivered nothing is a failed delivery, not a broken machine: %v", err)
	}
	if got.Verdict == fsm.VerdictPassed {
		t.Fatalf("the stage was proven by the code it started from (evidence: %+v)", got)
	}
	if !strings.Contains(got.Detail, "nothing on top of") {
		t.Errorf("the evidence must say what was missing, got %q", got.Detail)
	}
}
