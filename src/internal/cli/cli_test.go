package cli_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brunoomariano/luna/src/internal/cli"
	"github.com/brunoomariano/luna/src/internal/ledger"
)

// harness is a repository, a ledger and a place to capture output.
type harness struct {
	t   *testing.T
	env cli.Env
	out *bytes.Buffer
	err *bytes.Buffer
}

// durableDir answers with a directory on real storage, or skips.
//
// Not t.TempDir(): /tmp is tmpfs on this machine and on any systemd default, and
// the ledger's durability guard refuses it — correctly. See INV-4.
func durableDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	sweepStaleTestDirs(home)

	dir, err := os.MkdirTemp(home, ".luna-cli-test-")
	if err != nil {
		t.Skipf("cannot write under home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	if err := ledger.RequireDurable(filepath.Join(dir, "probe")); err != nil {
		t.Skipf("home is not durable here: %v", err)
	}
	return dir
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := durableDir(t)
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	h := &harness{
		t:   t,
		out: &bytes.Buffer{},
		err: &bytes.Buffer{},
	}
	h.env = cli.Env{Out: h.out, Err: h.err, Dir: repo, Ledger: filepath.Join(root, "ledger.jsonl")}

	h.git("init", "--quiet", "-b", "main")
	h.git("config", "user.email", "test@example.com")
	h.git("config", "user.name", "Test")
	return h
}

func (h *harness) git(args ...string) string {
	h.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = h.env.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (h *harness) commit(name, body string) string {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.env.Dir, name), []byte(body), 0o600); err != nil {
		h.t.Fatal(err)
	}
	h.git("add", "-A")
	h.git("commit", "--quiet", "-m", "add "+name)
	return h.git("rev-parse", "HEAD")
}

// contract writes a contract to a file and answers its path.
func (h *harness) contract(body string) string {
	h.t.Helper()
	path := filepath.Join(h.env.Dir, "contract.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		h.t.Fatal(err)
	}
	return path
}

func (h *harness) run(args ...string) error {
	h.t.Helper()
	h.out.Reset()
	h.err.Reset()
	return cli.Run(h.env, args)
}

func (h *harness) stdout() string { return h.out.String() }

func (h *harness) lines() []ledger.Entry {
	h.t.Helper()
	entries, err := ledger.Ledger{Path: h.env.Ledger}.Read()
	if err != nil {
		h.t.Fatalf("reading the ledger: %v", err)
	}
	return entries
}

const greenContract = `
phase    = "forge"
produces = ["ci_green"]

[verify.ci_green]
run   = "true"
scope = "full"
`

func TestCheckProvesAContractAndRecordsEachVerdict(t *testing.T) {
	h := newHarness(t)
	h.commit("a.txt", "one")

	if err := h.run("check", "--contract", h.contract(greenContract), "--run", "MAX-2"); err != nil {
		t.Fatalf("a passing contract was reported as failing: %v\n%s", err, h.stdout())
	}
	if !strings.Contains(h.stdout(), "is proven") {
		t.Errorf("the report does not say the phase is proven:\n%s", h.stdout())
	}

	lines := h.lines()
	if len(lines) != 1 {
		t.Fatalf("got %d ledger lines, want 1", len(lines))
	}
	got := lines[0]
	if got.Run != "MAX-2" || got.Phase != "forge" || got.Artifact != "ci_green" {
		t.Errorf("the line does not identify the check: %+v", got)
	}
	if got.Verdict != "passed" || got.Scope != "full" {
		t.Errorf("verdict/scope: got %s/%s", got.Verdict, got.Scope)
	}
}

// A failing check is a verdict about the work, and it has its own exit code so a
// conductor can tell it from Luna being broken.
func TestAFailedCheckIsToldApartFromABrokenLuna(t *testing.T) {
	h := newHarness(t)
	h.commit("a.txt", "one")

	err := h.run("check", "--contract", h.contract(`
phase    = "forge"
produces = ["ci_green"]

[verify.ci_green]
run   = "exit 1"
scope = "full"
`), "--run", "MAX-2")

	if err == nil {
		t.Fatal("a failing check reported success")
	}
	if !errors.Is(err, cli.ErrFailed) {
		t.Errorf("a failing check is not recognisable as a failed delivery: %v", err)
	}
	if errors.Is(err, cli.ErrUsage) {
		t.Error("a failing check reads as a usage error")
	}
}

// The help promises HEAD is the default, and a documented default that is not
// true is a bug in the code rather than in the text.
func TestCheckDefaultsToThisCheckoutsHead(t *testing.T) {
	h := newHarness(t)
	h.commit("a.txt", "one")

	if err := h.run("check", "--contract", h.contract(`
phase    = "forge"
produces = ["ci_green"]

[verify.ci_green]
run   = "test -f a.txt"
scope = "full"
`), "--run", "MAX-2"); err != nil {
		t.Fatalf("with no --commit, the check did not run over HEAD: %v\n%s", err, h.stdout())
	}
}

// A contract that cannot be read never reaches a model, and the refusal names
// every fault at once.
func TestAnUnusableContractIsRefusedBeforeAnythingRuns(t *testing.T) {
	h := newHarness(t)
	h.commit("a.txt", "one")

	err := h.run("check", "--contract", h.contract(`produces = ["ci_green"]`), "--run", "MAX-2")
	if err == nil {
		t.Fatal("an unusable contract was accepted")
	}
	if len(h.lines()) != 0 {
		t.Error("a refused contract still wrote to the ledger")
	}
}

// A simulation that reads like a result is a lie with the truth beside it.
func TestADryRunRunsNothingAndRecordsNothing(t *testing.T) {
	h := newHarness(t)
	h.commit("a.txt", "one")

	if err := h.run("check", "--contract", h.contract(`
phase    = "forge"
produces = ["ci_green"]

[verify.ci_green]
run   = "exit 1"
scope = "full"
`), "--run", "MAX-2", "--dry-run"); err != nil {
		t.Fatalf("a dry run reported a verdict: %v", err)
	}
	if !strings.Contains(h.stdout(), "nothing was run") {
		t.Errorf("the output does not say it ran nothing:\n%s", h.stdout())
	}
	if len(h.lines()) != 0 {
		t.Error("a dry run wrote to the ledger")
	}
}

func TestContractLintReportsFaultsWithoutRunningAnything(t *testing.T) {
	h := newHarness(t)

	err := h.run("contract", "lint", h.contract(`
phase    = "forge"
produces = ["ci_green"]
`))
	if err == nil {
		t.Fatal("a contract with no verifier passed lint")
	}
	if !strings.Contains(err.Error(), "no verifier") {
		t.Errorf("the refusal does not name the fault: %v", err)
	}
}

func TestContractLintAcceptsAGoodContract(t *testing.T) {
	h := newHarness(t)

	if err := h.run("contract", "lint", h.contract(greenContract)); err != nil {
		t.Fatalf("a well-formed contract was refused: %v", err)
	}
	if !strings.Contains(h.stdout(), "ci_green") {
		t.Errorf("lint does not say what is owed:\n%s", h.stdout())
	}
}

func TestRecordWritesOneLineAndStateReadsItBack(t *testing.T) {
	h := newHarness(t)

	if err := h.run("record", "--run", "MAX-2", "--event", "phase",
		"--phase", "forge", "--status", "running"); err != nil {
		t.Fatalf("recording: %v", err)
	}
	if err := h.run("state", "--run", "MAX-2"); err != nil {
		t.Fatalf("reading state: %v", err)
	}
	if !strings.Contains(h.stdout(), "forge") || !strings.Contains(h.stdout(), "running") {
		t.Errorf("the state does not say where the run is:\n%s", h.stdout())
	}
}

// INV-5: the account of where the answer was looked for is what separates a real
// block from an unread file, so the verb refuses without it.
func TestABlockWithNoAccountOfWhatWasConsultedIsRefused(t *testing.T) {
	h := newHarness(t)

	err := h.run("record", "--run", "MAX-2", "--event", "block", "--status", "blocked",
		"--question", "is this a defect?", "--needs", "which reading holds")
	if err == nil {
		t.Fatal("a block with no account was recorded")
	}
	if len(h.lines()) != 0 {
		t.Error("the refused block was written anyway")
	}
}

func TestACompleteBlockIsPutInFrontOfWhoeverAnswersIt(t *testing.T) {
	h := newHarness(t)

	if err := h.run("record", "--run", "MAX-2", "--event", "block", "--status", "blocked",
		"--phase", "forge",
		"--question", "is --largest meant to return an argument?",
		"--looked", "the contract, clause 4 — undefined with --max",
		"--looked", "tests/ — the combination is not covered",
		"--needs", "which of the two readings holds"); err != nil {
		t.Fatalf("recording a complete block: %v", err)
	}

	if err := h.run("state", "--run", "MAX-2"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"the question", "looked in", "clause 4", "what unblocks"} {
		if !strings.Contains(h.stdout(), want) {
			t.Errorf("the block is missing %q:\n%s", want, h.stdout())
		}
	}
}

// The scale that preceded this had eleven values and three meanings.
func TestANumericAutonomyIsRefused(t *testing.T) {
	h := newHarness(t)

	err := h.run("record", "--run", "MAX-2", "--event", "autonomy", "--autonomy", "9")
	if err == nil {
		t.Fatal("a numeric autonomy was accepted")
	}
	if !errors.Is(err, cli.ErrUsage) {
		t.Errorf("a bad flag value is not a usage error: %v", err)
	}
}

func TestReportPutsWhatNeedsAPersonFirst(t *testing.T) {
	h := newHarness(t)

	if err := h.run("record", "--run", "MOVING-1", "--event", "phase",
		"--status", "running", "--phase", "forge"); err != nil {
		t.Fatal(err)
	}
	if err := h.run("record", "--run", "STOPPED-2", "--event", "block", "--status", "blocked",
		"--question", "which reading holds?", "--looked", "the contract", "--needs", "an answer"); err != nil {
		t.Fatal(err)
	}

	if err := h.run("report"); err != nil {
		t.Fatal(err)
	}
	out := h.stdout()
	needs := strings.Index(out, "needs somebody")
	flight := strings.Index(out, "in flight")
	if needs < 0 || flight < 0 || needs > flight {
		t.Errorf("a blocked run is not listed before one that is merrily running:\n%s", out)
	}
}

func TestReportOnAnEmptyLedgerSaysSo(t *testing.T) {
	h := newHarness(t)

	if err := h.run("report"); err != nil {
		t.Fatalf("reporting on an empty ledger failed: %v", err)
	}
	if !strings.Contains(h.stdout(), "nothing recorded") {
		t.Errorf("got %q", h.stdout())
	}
}

func TestAnUnknownCommandNamesWhatIsAvailable(t *testing.T) {
	h := newHarness(t)

	err := h.run("orchestrate")
	if err == nil || !errors.Is(err, cli.ErrUsage) {
		t.Fatalf("an unknown command was not a usage error: %v", err)
	}
	if !strings.Contains(err.Error(), "check") {
		t.Errorf("the refusal does not name the real commands: %v", err)
	}
}

// The branch travels with the work, so a checkout on luna/<run> knows which run
// it is without being told.
func TestTheBranchNamesTheRunWhenNobodyDoes(t *testing.T) {
	h := newHarness(t)
	h.commit("a.txt", "one")
	h.git("checkout", "--quiet", "-b", "luna/MAX-2")

	if err := h.run("record", "--event", "phase", "--status", "running", "--phase", "forge"); err != nil {
		t.Fatalf("recording without --run: %v", err)
	}
	lines := h.lines()
	if len(lines) != 1 || lines[0].Run != "MAX-2" {
		t.Fatalf("the branch did not name the run: %+v", lines)
	}

	if err := h.run("state"); err != nil {
		t.Fatalf("reading state without --run: %v", err)
	}
	if !strings.Contains(h.stdout(), "MAX-2") {
		t.Errorf("state did not find the run from the branch:\n%s", h.stdout())
	}
}

func TestAStateWithNoRunAndNoBranchAsksForOne(t *testing.T) {
	h := newHarness(t)
	h.commit("a.txt", "one")

	err := h.run("state")
	if err == nil || !errors.Is(err, cli.ErrUsage) {
		t.Fatalf("expected a usage error, got %v", err)
	}
	if !strings.Contains(err.Error(), "--run") {
		t.Errorf("the refusal does not say what to pass: %v", err)
	}
}

func TestJSONCarriesTheSameVerdictAsTheText(t *testing.T) {
	h := newHarness(t)
	h.commit("a.txt", "one")

	if err := h.run("check", "--contract", h.contract(greenContract), "--run", "MAX-2", "--json"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"passed": true`, `"artifact": "ci_green"`, `"scope": "full"`} {
		if !strings.Contains(h.stdout(), want) {
			t.Errorf("the JSON is missing %s:\n%s", want, h.stdout())
		}
	}
}

func TestReportInJSONCarriesWhatTheTextShows(t *testing.T) {
	h := newHarness(t)
	if err := h.run("record", "--run", "MAX-2", "--event", "block", "--status", "blocked",
		"--question", "which reading holds?", "--looked", "the contract", "--needs", "an answer"); err != nil {
		t.Fatal(err)
	}

	if err := h.run("report", "--json"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"run": "MAX-2"`, `"needs_somebody": true`, `"status": "blocked"`} {
		if !strings.Contains(h.stdout(), want) {
			t.Errorf("the JSON is missing %s:\n%s", want, h.stdout())
		}
	}
}

func TestStateInJSONCarriesTheWholeLine(t *testing.T) {
	h := newHarness(t)
	if err := h.run("record", "--run", "MAX-2", "--event", "phase",
		"--status", "running", "--phase", "forge", "--round", "2"); err != nil {
		t.Fatal(err)
	}

	if err := h.run("state", "--run", "MAX-2", "--json"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"phase": "forge"`, `"round": 2`, `"at":`} {
		if !strings.Contains(h.stdout(), want) {
			t.Errorf("the JSON is missing %s:\n%s", want, h.stdout())
		}
	}
}

func TestAskingForTheStateOfARunNobodyHasRecorded(t *testing.T) {
	h := newHarness(t)

	if err := h.run("state", "--run", "NEVER-1"); err != nil {
		t.Fatalf("asking about an unknown run was an error: %v", err)
	}
	if !strings.Contains(h.stdout(), "nothing recorded") {
		t.Errorf("got %q", h.stdout())
	}

	if err := h.run("state", "--run", "NEVER-1", "--json"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stdout(), `"known": false`) {
		t.Errorf("the JSON does not say the run is unknown:\n%s", h.stdout())
	}
}

func TestADryRunInJSONSaysItIsASimulation(t *testing.T) {
	h := newHarness(t)
	h.commit("a.txt", "one")

	if err := h.run("check", "--contract", h.contract(greenContract),
		"--run", "MAX-2", "--dry-run", "--json"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stdout(), `"simulated": true`) {
		t.Errorf("a simulation does not announce itself:\n%s", h.stdout())
	}
	if !strings.Contains(h.stdout(), `"would_run"`) {
		t.Errorf("the simulation does not say what it would have run:\n%s", h.stdout())
	}
}

func TestAContractThatIsNotThereIsReportedAsSuch(t *testing.T) {
	h := newHarness(t)

	err := h.run("check", "--contract", filepath.Join(h.env.Dir, "absent.toml"), "--run", "MAX-2")
	if err == nil {
		t.Fatal("a missing contract was accepted")
	}
	if !strings.Contains(err.Error(), "reading the contract") {
		t.Errorf("the error does not say what failed: %v", err)
	}
}

func TestCheckWithNoContractSaysWhatIsMissing(t *testing.T) {
	h := newHarness(t)

	err := h.run("check", "--run", "MAX-2")
	if err == nil || !errors.Is(err, cli.ErrUsage) {
		t.Fatalf("expected a usage error, got %v", err)
	}
	if !strings.Contains(err.Error(), "--contract") {
		t.Errorf("the refusal does not name the missing flag: %v", err)
	}
}

func TestContractLintNeedsSomethingToLint(t *testing.T) {
	h := newHarness(t)

	if err := h.run("contract", "lint"); !errors.Is(err, cli.ErrUsage) {
		t.Errorf("lint with no argument: %v", err)
	}
	if err := h.run("contract", "check"); !errors.Is(err, cli.ErrUsage) {
		t.Errorf("an unknown contract subcommand: %v", err)
	}
}

// --no-record is for a conductor that wants the verdict without the line, and it
// has to leave the ledger untouched to be worth having.
func TestCheckCanProveWithoutRecording(t *testing.T) {
	h := newHarness(t)
	h.commit("a.txt", "one")

	if err := h.run("check", "--contract", h.contract(greenContract),
		"--run", "MAX-2", "--no-record"); err != nil {
		t.Fatal(err)
	}
	if len(h.lines()) != 0 {
		t.Error("--no-record wrote to the ledger anyway")
	}
}

func TestHelpAndVersionAnswerWithoutARepository(t *testing.T) {
	h := newHarness(t)

	if err := h.run("help"); err != nil {
		t.Fatalf("help: %v", err)
	}
	if !strings.Contains(h.stdout(), "luna check") {
		t.Errorf("help does not describe the commands:\n%s", h.stdout())
	}

	if err := h.run("version"); err != nil {
		t.Fatalf("version: %v", err)
	}
	if h.stdout() == "" {
		t.Error("version printed nothing")
	}

	if err := h.run(); !errors.Is(err, cli.ErrUsage) {
		t.Errorf("no arguments at all: %v", err)
	}
}

// The contract usually arrives on a pipe, because it is written by whoever
// conducts and Luna keeps none of it.
func TestAContractCanArriveOnStdin(t *testing.T) {
	h := newHarness(t)
	h.commit("a.txt", "one")

	restore := feedStdin(t, greenContract)
	defer restore()

	if err := h.run("check", "--contract", "-", "--run", "MAX-2"); err != nil {
		t.Fatalf("a contract on stdin was not read: %v\n%s", err, h.stdout())
	}
	if len(h.lines()) != 1 {
		t.Errorf("got %d ledger lines, want 1", len(h.lines()))
	}
}

func TestLintReadsAContractOnStdinToo(t *testing.T) {
	h := newHarness(t)

	restore := feedStdin(t, greenContract)
	defer restore()

	if err := h.run("contract", "lint", "-"); err != nil {
		t.Fatalf("lint could not read stdin: %v", err)
	}
}

// feedStdin replaces os.Stdin with a pipe carrying body, and answers how to put
// it back.
func feedStdin(t *testing.T, body string) func() {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = w.WriteString(body)
		_ = w.Close()
	}()

	saved := os.Stdin
	os.Stdin = r
	return func() {
		os.Stdin = saved
		_ = r.Close()
	}
}

// A run whose latest line is a simulation says so wherever it is shown, because
// a simulation that reads like a result is a lie with the truth beside it.
func TestASimulatedRunAnnouncesItselfInStateAndReport(t *testing.T) {
	h := newHarness(t)

	if err := h.run("record", "--run", "DRY-1", "--event", "phase",
		"--status", "running", "--phase", "forge", "--dry-run"); err != nil {
		t.Fatal(err)
	}

	if err := h.run("state", "--run", "DRY-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stdout(), "simulated") {
		t.Errorf("state does not say the line was simulated:\n%s", h.stdout())
	}

	if err := h.run("report"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stdout(), "simulated") {
		t.Errorf("the report does not say the run was simulated:\n%s", h.stdout())
	}
}

// A report exists to find the run that stopped, so a failing count belongs
// beside it rather than in a second command.
func TestAReportShowsHowManyChecksFailed(t *testing.T) {
	h := newHarness(t)
	h.commit("a.txt", "one")

	err := h.run("check", "--contract", h.contract(`
phase    = "forge"
produces = ["ci_green"]

[verify.ci_green]
run   = "exit 1"
scope = "full"
`), "--run", "MAX-2")
	if err == nil {
		t.Fatal("expected the check to fail")
	}

	if err := h.run("report"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stdout(), "1 failed") {
		t.Errorf("the report does not carry the failure count:\n%s", h.stdout())
	}
}

func TestAReportCanBeBoundedToARecentWindow(t *testing.T) {
	h := newHarness(t)

	if err := h.run("record", "--run", "MAX-2", "--event", "phase",
		"--status", "running", "--phase", "forge"); err != nil {
		t.Fatal(err)
	}

	if err := h.run("report", "--since", "1h"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stdout(), "MAX-2") {
		t.Errorf("a run from a moment ago fell outside a one-hour window:\n%s", h.stdout())
	}

	if err := h.run("report", "--since", "1ns"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.stdout(), "MAX-2") {
		t.Errorf("the window was not applied:\n%s", h.stdout())
	}
}

func TestABadFlagIsAUsageErrorRatherThanAPanic(t *testing.T) {
	h := newHarness(t)

	for _, args := range [][]string{
		{"check", "--nonsense"},
		{"record", "--nonsense"},
		{"state", "--nonsense"},
		{"report", "--nonsense"},
	} {
		if err := h.run(args...); !errors.Is(err, cli.ErrUsage) {
			t.Errorf("%v: got %v, want a usage error", args, err)
		}
	}
}

func TestRecordNeedsAnEvent(t *testing.T) {
	h := newHarness(t)

	err := h.run("record", "--run", "MAX-2")
	if err == nil || !errors.Is(err, cli.ErrUsage) {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(err.Error(), "--event") {
		t.Errorf("the refusal does not name what is missing: %v", err)
	}
}

// The report is read to triage, so an age is rough — but it has to be right at
// the boundaries a person reads it across.
func TestAReportSaysHowLongAgoEachRunMoved(t *testing.T) {
	h := newHarness(t)

	entries := []ledger.Entry{
		{Run: "NOW-1", Event: ledger.EventPhase, Status: ledger.StatusRunning, At: time.Now().Add(-10 * time.Second)},
		{Run: "MINUTES-2", Event: ledger.EventPhase, Status: ledger.StatusRunning, At: time.Now().Add(-30 * time.Minute)},
		{Run: "HOURS-3", Event: ledger.EventPhase, Status: ledger.StatusRunning, At: time.Now().Add(-5 * time.Hour)},
		{Run: "DAYS-4", Event: ledger.EventPhase, Status: ledger.StatusRunning, At: time.Now().Add(-72 * time.Hour)},
	}
	l := ledger.Ledger{Path: h.env.Ledger}
	for _, e := range entries {
		if err := l.Append(e); err != nil {
			t.Fatal(err)
		}
	}

	if err := h.run("report"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"just now", "30m ago", "5h ago", "3d ago"} {
		if !strings.Contains(h.stdout(), want) {
			t.Errorf("the report is missing %q:\n%s", want, h.stdout())
		}
	}
}

// A check that cannot record is not a check that passed. The refusal has to
// reach the caller rather than being swallowed behind a green verdict.
func TestAVerdictThatCannotBeRecordedIsReported(t *testing.T) {
	h := newHarness(t)
	h.commit("a.txt", "one")

	inMemory, err := os.MkdirTemp("/dev/shm", "luna-cli-")
	if err != nil {
		t.Skipf("no writable tmpfs to test against: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(inMemory) })
	h.env.Ledger = filepath.Join(inMemory, "ledger.jsonl")

	err = h.run("check", "--contract", h.contract(greenContract), "--run", "MAX-2")
	if err == nil {
		t.Fatal("a check whose record could not be written reported success")
	}
	if errors.Is(err, cli.ErrFailed) {
		t.Error("a machine failure was reported as a failed delivery")
	}
}

// The trail is the log of a task: what it discovered, what it walked, what each
// check observed. `state` says where a run is; this says what it did.
func TestTheTrailIsTheWholeStoryOfARunInOrder(t *testing.T) {
	h := newHarness(t)
	h.commit("a.txt", "one")

	if err := h.run("record", "--run", "WID-1", "--event", "discovery", "--phase", "setup",
		"--found", "gate: node test.js", "--where", "package.json scripts.check"); err != nil {
		t.Fatal(err)
	}
	if err := h.run("record", "--run", "WID-1", "--event", "phase",
		"--phase", "forge", "--status", "running", "--round", "1"); err != nil {
		t.Fatal(err)
	}
	if err := h.run("check", "--contract", h.contract(greenContract), "--run", "WID-1", "--round", "1"); err != nil {
		t.Fatal(err)
	}
	if err := h.run("record", "--run", "WID-1", "--event", "phase",
		"--phase", "close", "--status", "done"); err != nil {
		t.Fatal(err)
	}

	if err := h.run("trail", "WID-1"); err != nil {
		t.Fatalf("reading the trail: %v", err)
	}
	out := h.stdout()
	for _, want := range []string{
		"discovery", "gate: node test.js", "package.json scripts.check",
		"forge", "ci_green", "close", "done",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the trail is missing %q:\n%s", want, out)
		}
	}

	// Oldest first: the discovery has to precede the phase it informed.
	if strings.Index(out, "discovery") > strings.Index(out, "close") {
		t.Errorf("the trail is not in order:\n%s", out)
	}
}

// A discovery is recorded and never consulted: the command still arrives in the
// contract. What the trail buys is a reader who can see *why* a later check ran
// the command it ran.
func TestADiscoveryIsRecordedWithWhatItReadToConcludeIt(t *testing.T) {
	h := newHarness(t)

	err := h.run("record", "--run", "WID-1", "--event", "discovery",
		"--found", "gate: cargo test")
	if err == nil {
		t.Fatal("a discovery with no source was recorded; a finding nobody can check is a claim")
	}
	if !strings.Contains(err.Error(), "no source") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}

	if err := h.run("record", "--run", "WID-1", "--event", "discovery",
		"--found", "gate: cargo test", "--where", "Cargo.toml"); err != nil {
		t.Fatalf("a complete discovery was refused: %v", err)
	}
	lines := h.lines()
	if len(lines) != 1 || lines[0].Found != "gate: cargo test" || lines[0].Where != "Cargo.toml" {
		t.Errorf("the discovery came back changed: %+v", lines)
	}
}

// Go's flag package stops at the first non-flag argument, so an id before a flag
// would silently drop the flag. Both orders have to mean the same thing.
func TestTheRunIdIsAcceptedBeforeOrAfterAFlag(t *testing.T) {
	h := newHarness(t)
	if err := h.run("record", "--run", "WID-1", "--event", "phase", "--status", "done"); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"trail", "WID-1", "--json"},
		{"trail", "--json", "WID-1"},
		{"trail", "--run", "WID-1", "--json"},
	} {
		if err := h.run(args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.HasPrefix(strings.TrimSpace(h.stdout()), "[") {
			t.Errorf("%v did not produce JSON:\n%s", args, h.stdout())
		}
	}
}

func TestATrailForARunNobodyRecordedSaysSo(t *testing.T) {
	h := newHarness(t)

	if err := h.run("trail", "NEVER-1"); err != nil {
		t.Fatalf("asking for an unknown trail was an error: %v", err)
	}
	if !strings.Contains(h.stdout(), "nothing recorded") {
		t.Errorf("got %q", h.stdout())
	}
}

func TestATrailWithNoRunAndNoBranchAsksForOne(t *testing.T) {
	h := newHarness(t)
	h.commit("a.txt", "one")

	err := h.run("trail")
	if err == nil || !errors.Is(err, cli.ErrUsage) {
		t.Fatalf("expected a usage error, got %v", err)
	}
}

// A failing check's output belongs under the line it belongs to, because that is
// what somebody reading a finished run needs to see.
func TestTheTrailCarriesWhatAFailingCheckSaid(t *testing.T) {
	h := newHarness(t)
	h.commit("a.txt", "one")

	_ = h.run("check", "--contract", h.contract(`
phase    = "forge"
produces = ["ci_green"]

[verify.ci_green]
run   = "echo 'the suite is red' >&2; exit 1"
scope = "full"
`), "--run", "WID-1", "--round", "2")

	if err := h.run("trail", "WID-1"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FAIL", "the suite is red", "r2"} {
		if !strings.Contains(h.stdout(), want) {
			t.Errorf("the trail is missing %q:\n%s", want, h.stdout())
		}
	}
}

// The three parts of a block are indented under it, in the order they are read.
func TestTheTrailPutsABlocksThreeParts(t *testing.T) {
	h := newHarness(t)

	if err := h.run("record", "--run", "WID-1", "--event", "block", "--status", "blocked",
		"--phase", "forge", "--question", "does --avg round down?",
		"--looked", "test.js — not covered", "--needs", "which behaviour holds"); err != nil {
		t.Fatal(err)
	}
	if err := h.run("trail", "WID-1"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"does --avg round down?", "looked in", "test.js", "needs", "which behaviour holds"} {
		if !strings.Contains(h.stdout(), want) {
			t.Errorf("the trail is missing %q:\n%s", want, h.stdout())
		}
	}
}

// A field that was written and is not shown is worse than one that was refused:
// the writer believes they left a record. Four phases recorded with `--found`
// rendered as blank lines on the first real use, while the text sat in the
// ledger intact.
func TestTheTrailShowsFoundOnAnyEventNotJustDiscovery(t *testing.T) {
	h := newHarness(t)

	if err := h.run("record", "--run", "REAL-1", "--event", "phase",
		"--phase", "hygiene", "--status", "running",
		"--found", "the whole suite was red on the base: vitest 4 shadows jsdom"); err != nil {
		t.Fatal(err)
	}
	if err := h.run("trail", "REAL-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stdout(), "vitest 4 shadows jsdom") {
		t.Errorf("what was recorded is not in the trail:\n%s", h.stdout())
	}
}

func TestATrailLineCarriesBothFoundAndNote(t *testing.T) {
	h := newHarness(t)

	if err := h.run("record", "--run", "REAL-1", "--event", "phase", "--status", "running",
		"--found", "the finding", "--note", "the aside"); err != nil {
		t.Fatal(err)
	}
	if err := h.run("trail", "REAL-1"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"the finding", "the aside"} {
		if !strings.Contains(h.stdout(), want) {
			t.Errorf("the trail dropped %q:\n%s", want, h.stdout())
		}
	}
}

// The tool exists to prove things, and on its first two real tasks it proved
// nothing: both ran to completion and neither called `check` once. It warns
// rather than refuses — a phase with nothing mechanically provable is ordinary,
// and refusing would be Luna deciding what counts as finished.
func TestClosingARunThatProvedNothingSaysSo(t *testing.T) {
	h := newHarness(t)

	if err := h.run("record", "--run", "UNPROVEN-1", "--event", "phase",
		"--phase", "close", "--status", "done"); err != nil {
		t.Fatalf("the warning must not fail the command: %v", err)
	}
	if !strings.Contains(h.err.String(), "nothing was ever proven") {
		t.Errorf("no warning for a run that never ran a check:\n%s", h.err.String())
	}
}

func TestClosingARunThatWasProvenIsQuiet(t *testing.T) {
	h := newHarness(t)
	h.commit("a.txt", "one")

	if err := h.run("check", "--contract", h.contract(greenContract), "--run", "PROVEN-1"); err != nil {
		t.Fatal(err)
	}
	if err := h.run("record", "--run", "PROVEN-1", "--event", "phase",
		"--phase", "close", "--status", "done"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.err.String(), "nothing was ever proven") {
		t.Errorf("a proven run was warned about:\n%s", h.err.String())
	}
}

// A batch seeding several runs from one checkout stamped all of them with that
// checkout's remote, and `report` groups by project — so they filed under a repo
// none of them touched.
func TestTheProjectCanBeNamedWhenTheCommandRunsElsewhere(t *testing.T) {
	h := newHarness(t)

	if err := h.run("record", "--run", "SEEDED-1", "--event", "phase",
		"--status", "awaiting_resume", "--project", "github.com/me/other-repo"); err != nil {
		t.Fatal(err)
	}
	lines := h.lines()
	if len(lines) != 1 || lines[0].Project != "github.com/me/other-repo" {
		t.Errorf("the named project did not survive: %+v", lines)
	}
}

// `--where` beside nothing found is a line that points at a file and says
// nothing about it.
func TestASourceWithNothingFoundIsRefused(t *testing.T) {
	h := newHarness(t)

	err := h.run("record", "--run", "X-1", "--event", "phase", "--where", "Makefile")
	if err == nil {
		t.Fatal("a source with no finding was recorded")
	}
	if !strings.Contains(err.Error(), "says nothing") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// The listing exists to answer "which tasks are there, and where does each one
// stand". Before it could be filtered, four runs across three repositories
// arrived with nothing to tell them apart, and standing in one repository said
// nothing about which of them belonged to it.
func TestTheListingCanBeNarrowedToOneRepository(t *testing.T) {
	h := newHarness(t)
	seed := func(run, project, status string) {
		t.Helper()
		if err := h.run("record", "--run", run, "--event", "phase",
			"--project", project, "--status", status, "--phase", "forge"); err != nil {
			t.Fatal(err)
		}
	}
	seed("HERE-1", "github.com/me/app", "running")
	seed("ELSEWHERE-2", "github.com/me/other", "running")

	if err := h.run("report", "--project", "github.com/me/app"); err != nil {
		t.Fatal(err)
	}
	out := h.stdout()
	if !strings.Contains(out, "HERE-1") {
		t.Errorf("the run in the named repository is missing:\n%s", out)
	}
	if strings.Contains(out, "ELSEWHERE-2") {
		t.Errorf("a run from another repository was listed:\n%s", out)
	}
}

// A run whose lines name two repositories has to appear under both.
//
// This is not hypothetical: the real ledger has two such runs, left by a batch
// that seeded them from one checkout and stamped that checkout's remote on their
// first line while their later lines named the repository they really worked in.
// Filtering on the latest line alone hides such a run from the repository it was
// seeded in; filtering on the first hides it from the one it delivered to.
func TestARunThatMovedBetweenRepositoriesIsListedUnderBoth(t *testing.T) {
	h := newHarness(t)
	for _, project := range []string{"github.com/me/seeded-from", "github.com/me/delivered-to"} {
		if err := h.run("record", "--run", "STRADDLE-9", "--event", "phase",
			"--project", project, "--status", "running", "--phase", "forge"); err != nil {
			t.Fatal(err)
		}
	}

	for _, project := range []string{"github.com/me/seeded-from", "github.com/me/delivered-to"} {
		h.out.Reset()
		if err := h.run("report", "--project", project); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(h.stdout(), "STRADDLE-9") {
			t.Errorf("a run that touched %s is not listed there:\n%s", project, h.stdout())
		}
	}
}

// --here is the whole point of the filter: somebody standing in a repository
// asking what is going on in it, without having to know how the ledger spells
// the repository's name.
func TestHereMeansTheRepositoryYouAreStandingIn(t *testing.T) {
	h := newHarness(t)
	if err := h.run("record", "--run", "LOCAL-1", "--event", "phase",
		"--status", "running", "--phase", "forge"); err != nil {
		t.Fatal(err)
	}
	if err := h.run("record", "--run", "REMOTE-2", "--event", "phase",
		"--project", "github.com/me/somewhere-else", "--status", "running"); err != nil {
		t.Fatal(err)
	}

	if err := h.run("report", "--here"); err != nil {
		t.Fatal(err)
	}
	out := h.stdout()
	if !strings.Contains(out, "LOCAL-1") {
		t.Errorf("the run recorded here is not listed:\n%s", out)
	}
	if strings.Contains(out, "REMOTE-2") {
		t.Errorf("a run from another repository was listed under --here:\n%s", out)
	}
}

// Two flags naming different repositories is a question with no answer, and
// silently preferring one of them is how somebody reads the wrong listing and
// believes it.
func TestHereAndAConflictingProjectAreRefused(t *testing.T) {
	h := newHarness(t)

	err := h.run("report", "--here", "--project", "github.com/me/not-here")
	if !errors.Is(err, cli.ErrUsage) {
		t.Fatalf("two conflicting repositories were accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "github.com/me/not-here") {
		t.Errorf("the refusal does not name what was asked for: %v", err)
	}
}

// "What is still going" is a different question from "what happened lately", and
// answering it used to mean reading the whole listing.
func TestOpenLeavesOutWhatIsFinished(t *testing.T) {
	h := newHarness(t)
	for _, seeded := range []struct{ run, status string }{
		{"GOING-1", "running"},
		{"FINISHED-2", "done"},
		{"GIVEN-UP-3", "abandoned"},
	} {
		if err := h.run("record", "--run", seeded.run, "--event", "phase",
			"--status", seeded.status, "--phase", "forge"); err != nil {
			t.Fatal(err)
		}
	}

	if err := h.run("report", "--open"); err != nil {
		t.Fatal(err)
	}
	out := h.stdout()
	if !strings.Contains(out, "GOING-1") {
		t.Errorf("a running run is missing from --open:\n%s", out)
	}
	for _, gone := range []string{"FINISHED-2", "GIVEN-UP-3"} {
		if strings.Contains(out, gone) {
			t.Errorf("%s is finished and was listed under --open:\n%s", gone, out)
		}
	}
}

// An empty listing in a repository with no runs is not a broken ledger, and
// saying "nothing recorded yet" there sends somebody looking for one.
func TestAnEmptyListingSaysWhichFilterEmptiedIt(t *testing.T) {
	h := newHarness(t)
	if err := h.run("record", "--run", "SOMEWHERE-1", "--event", "phase",
		"--project", "github.com/me/elsewhere", "--status", "running"); err != nil {
		t.Fatal(err)
	}

	if err := h.run("report", "--project", "github.com/me/nothing-here"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stdout(), "github.com/me/nothing-here") {
		t.Errorf("an empty listing does not say which repository it looked in:\n%s", h.stdout())
	}
	if strings.Contains(h.stdout(), "nothing recorded yet") {
		t.Errorf("a filtered listing reported an empty ledger:\n%s", h.stdout())
	}
}

// The repository is worth a line only when the listing spans more than one.
// Repeating the same name under every run is a column of noise.
func TestTheRepositoryIsShownOnlyWhenTheListingSpansMoreThanOne(t *testing.T) {
	h := newHarness(t)
	if err := h.run("record", "--run", "ALONE-1", "--event", "phase",
		"--project", "github.com/me/only", "--status", "running"); err != nil {
		t.Fatal(err)
	}

	if err := h.run("report"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.stdout(), "github.com/me/only") {
		t.Errorf("the repository was repeated under a listing that has only one:\n%s", h.stdout())
	}

	if err := h.run("record", "--run", "SECOND-2", "--event", "phase",
		"--project", "github.com/me/another", "--status", "running"); err != nil {
		t.Fatal(err)
	}
	h.out.Reset()
	if err := h.run("report"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"github.com/me/only", "github.com/me/another"} {
		if !strings.Contains(h.stdout(), want) {
			t.Errorf("a listing spanning two repositories does not name %s:\n%s", want, h.stdout())
		}
	}
}

// A version that never changes is indistinguishable from one nobody bumped. It
// read "dev" for the tool's whole life because nothing set it.
func TestTheVersionIsNotAPlaceholder(t *testing.T) {
	h := newHarness(t)

	if err := h.run("version"); err != nil {
		t.Fatal(err)
	}
	out := strings.TrimSpace(h.stdout())
	if !strings.HasPrefix(out, "luna ") {
		t.Fatalf("version does not name the tool: %q", out)
	}
	said := strings.Fields(strings.TrimPrefix(out, "luna "))[0]
	if said == "dev" || said == "" {
		t.Errorf("the version is a placeholder: %q", said)
	}
	if !strings.ContainsAny(said, "0123456789") {
		t.Errorf("the version carries no number: %q", said)
	}
}

// Each filter that can empty the listing says so in its own words, because "no
// runs" and "no runs in the last hour" send somebody to different places.
func TestAWindowThatMatchesNothingNamesTheWindow(t *testing.T) {
	h := newHarness(t)
	if err := h.run("record", "--run", "OLD-1", "--event", "phase",
		"--status", "running", "--phase", "forge"); err != nil {
		t.Fatal(err)
	}

	if err := h.run("report", "--since", "1ns"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stdout(), "1ns") {
		t.Errorf("an empty window does not say how far back it looked:\n%s", h.stdout())
	}
}

func TestOpenThatMatchesNothingSaysNoRunIsOpen(t *testing.T) {
	h := newHarness(t)
	if err := h.run("record", "--run", "CLOSED-1", "--event", "phase",
		"--status", "done", "--phase", "close"); err != nil {
		t.Fatal(err)
	}

	if err := h.run("report", "--open"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stdout(), "unfinished") {
		t.Errorf("an empty --open listing does not say why:\n%s", h.stdout())
	}
}

// A finished run still belongs in the listing — the question "what happened to
// WID-1" is asked after it ended — but under its own heading, so it cannot be
// mistaken for something still moving.
func TestFinishedRunsAreListedApartFromMovingOnes(t *testing.T) {
	h := newHarness(t)
	if err := h.run("record", "--run", "MOVING-1", "--event", "phase",
		"--status", "running", "--phase", "forge"); err != nil {
		t.Fatal(err)
	}
	if err := h.run("record", "--run", "ENDED-2", "--event", "phase",
		"--status", "done", "--phase", "close"); err != nil {
		t.Fatal(err)
	}

	if err := h.run("report"); err != nil {
		t.Fatal(err)
	}
	out := h.stdout()
	flight := strings.Index(out, "in flight")
	finished := strings.Index(out, "finished")
	if flight < 0 || finished < 0 {
		t.Fatalf("the listing does not separate moving from finished:\n%s", out)
	}
	if flight > finished {
		t.Errorf("a finished run is listed above one still moving:\n%s", out)
	}
}

// The commit is what tells two builds of the same version apart, which is the
// whole reason the version stopped being a placeholder.
func TestTheVersionSaysWhichCommitItWasBuiltFrom(t *testing.T) {
	h := newHarness(t)
	original := cli.Commit
	cli.Commit = "abc1234"
	t.Cleanup(func() { cli.Commit = original })

	if err := h.run("version"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stdout(), "abc1234") {
		t.Errorf("the version does not name the commit it came from:\n%s", h.stdout())
	}
}

// Naming only the first active filter is how an empty listing lies: --project
// with --open said "no run has touched X" about a repository with four finished
// runs in it, sending the reader to look for a recording fault that was not
// there.
func TestAnEmptyListingNamesEveryFilterThatNarrowedIt(t *testing.T) {
	h := newHarness(t)
	if err := h.run("record", "--run", "ENDED-1", "--event", "phase",
		"--project", "github.com/me/app", "--status", "done", "--phase", "close"); err != nil {
		t.Fatal(err)
	}

	if err := h.run("report", "--project", "github.com/me/app", "--open"); err != nil {
		t.Fatal(err)
	}
	out := h.stdout()
	if !strings.Contains(out, "github.com/me/app") {
		t.Errorf("the empty listing does not name the repository:\n%s", out)
	}
	if !strings.Contains(out, "unfinished") {
		t.Errorf("the empty listing blames the repository for what --open excluded:\n%s", out)
	}
}

// launched captures what a session would have replaced this process with,
// because a process that has execve'd cannot be asserted on.
func (h *harness) launched(t *testing.T, args ...string) []string {
	t.Helper()
	var got []string
	h.env.Launch = func(command []string) error {
		got = command
		return nil
	}
	t.Cleanup(func() { h.env.Launch = nil })
	if err := h.run(append([]string{"session"}, args...)...); err != nil {
		t.Fatalf("session %v: %v", args, err)
	}
	return got
}

// The briefing is the whole point of the verb: an agent that has to ask what is
// going on here has already cost the thing this saves.
func TestABriefingCarriesTheRunsThisRepositoryHasOpen(t *testing.T) {
	h := newHarness(t)
	if err := h.run("record", "--run", "WID-7", "--event", "block", "--status", "blocked",
		"--question", "which reading of criterion 3 holds?",
		"--looked", "AGENTS.md", "--needs", "an answer"); err != nil {
		t.Fatal(err)
	}
	if err := h.run("record", "--run", "DONE-1", "--event", "phase",
		"--status", "done", "--phase", "close"); err != nil {
		t.Fatal(err)
	}

	if err := h.run("session", "--print"); err != nil {
		t.Fatal(err)
	}
	out := h.stdout()
	if !strings.Contains(out, "WID-7") {
		t.Errorf("the briefing does not name the open run:\n%s", out)
	}
	if !strings.Contains(out, "which reading of criterion 3 holds?") {
		t.Errorf("the briefing does not say what the run is blocked on:\n%s", out)
	}
	if strings.Contains(out, "DONE-1") {
		t.Errorf("the briefing carries a finished run, which is not waiting for anybody:\n%s", out)
	}
}

// An open run is not a request to continue it. A session that resumed the wrong
// task on its own costs more than the question does, which is the same reason
// autonomy starts at manual.
func TestABriefingWithOpenRunsTellsTheAgentToAskFirst(t *testing.T) {
	h := newHarness(t)
	if err := h.run("record", "--run", "WID-4", "--event", "phase",
		"--status", "running", "--phase", "forge"); err != nil {
		t.Fatal(err)
	}

	if err := h.run("session", "--print"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stdout(), "Ask which one") {
		t.Errorf("the briefing does not tell the agent to ask before resuming:\n%s", h.stdout())
	}
}

// A repository with nothing open must say so plainly. Silence reads as a broken
// ledger, and the instruction that follows still has to arrive.
func TestABriefingSaysWhenNothingIsOpen(t *testing.T) {
	h := newHarness(t)

	if err := h.run("session", "--print"); err != nil {
		t.Fatal(err)
	}
	out := h.stdout()
	if !strings.Contains(out, "No Luna run is open here") {
		t.Errorf("the briefing does not say the repository is idle:\n%s", out)
	}
	if strings.Contains(out, "Ask which one") {
		t.Errorf("the briefing asks which run to resume when there are none:\n%s", out)
	}
	if !strings.Contains(out, "lsh-luna-soul") {
		t.Errorf("an idle briefing lost the instruction to conduct:\n%s", out)
	}
}

// The skill is named because an exact name is what a model can act on, and the
// fallback is there because a machine without the skill must not be stranded.
func TestABriefingNamesTheSkillAndSaysWhatToDoWithoutIt(t *testing.T) {
	h := newHarness(t)

	if err := h.run("session", "--print"); err != nil {
		t.Fatal(err)
	}
	out := h.stdout()
	if !strings.Contains(out, "lsh-luna-soul") {
		t.Errorf("the briefing does not name the skill:\n%s", out)
	}
	if !strings.Contains(out, "not available in this session") {
		t.Errorf("the briefing has no fallback for a session without the skill:\n%s", out)
	}
	if !strings.Contains(out, "luna check --contract -") {
		t.Errorf("the fallback does not say how to prove anything:\n%s", out)
	}
}

func TestTheSkillNamedInABriefingCanBeChangedOrDropped(t *testing.T) {
	h := newHarness(t)

	if err := h.run("session", "--print", "--skill", "my-own-flow"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.stdout(), "my-own-flow") {
		t.Errorf("--skill was ignored:\n%s", h.stdout())
	}

	h.out.Reset()
	if err := h.run("session", "--print", "--skill", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.stdout(), "lsh-luna-soul") {
		t.Errorf("an empty --skill still named the default:\n%s", h.stdout())
	}
	if !strings.Contains(h.stdout(), "luna check") {
		t.Errorf("dropping the skill also dropped how to use Luna:\n%s", h.stdout())
	}
}

// Luna composes no sandbox and no memory: it hands the briefing to whatever the
// caller already uses to start an agent, and gets out of the way.
func TestASessionHandsTheBriefingToTheLauncher(t *testing.T) {
	h := newHarness(t)

	got := h.launched(t, "claude")
	if len(got) != 3 {
		t.Fatalf("expected launcher, agent and briefing; got %d: %v", len(got), got)
	}
	if got[0] != "ai-run" || got[1] != "claude" {
		t.Errorf("the composition is wrong: %v", got[:2])
	}
	if !strings.Contains(got[2], "starting work in") {
		t.Errorf("the third argument is not the briefing: %q", got[2])
	}
}

// The briefing has to survive as ONE argument. It carries newlines, quotes and a
// repository path, and a launcher that split it would hand the agent a fragment.
func TestTheBriefingIsASingleArgument(t *testing.T) {
	h := newHarness(t)
	if err := h.run("record", "--run", "WID-4", "--event", "block", "--status", "blocked",
		"--question", `does "targeted" satisfy a demand for full?`,
		"--looked", "docs/invariants.md", "--needs", "an answer"); err != nil {
		t.Fatal(err)
	}

	got := h.launched(t, "claude")
	if len(got) != 3 {
		t.Fatalf("the briefing was split across arguments: %v", got)
	}
	if !strings.Contains(got[2], "\n") {
		t.Error("the briefing lost its line breaks")
	}
	if !strings.Contains(got[2], `does "targeted" satisfy a demand for full?`) {
		t.Errorf("a quoted question did not survive:\n%s", got[2])
	}
}

func TestABareSessionStartsTheAgentWithNoLauncher(t *testing.T) {
	h := newHarness(t)

	got := h.launched(t, "codex", "--bare")
	if len(got) != 2 || got[0] != "codex" {
		t.Errorf("--bare still composed a launcher: %v", got)
	}
}

func TestTheLauncherCanBeNamed(t *testing.T) {
	h := newHarness(t)

	got := h.launched(t, "claude", "--launcher", "my-wrapper")
	if got[0] != "my-wrapper" {
		t.Errorf("--launcher was ignored: %v", got)
	}
}

// Starting nothing is a usage error rather than a silent no-op: somebody who
// typed `luna session` meant to open one.
func TestASessionWithNoAgentSaysWhatToType(t *testing.T) {
	h := newHarness(t)

	err := h.run("session")
	if !errors.Is(err, cli.ErrUsage) {
		t.Fatalf("session with no agent was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "luna session claude") {
		t.Errorf("the refusal does not show the shape of the command: %v", err)
	}
}

// A missing binary names itself. It used to also advise --bare, which is wrong
// advice for the case that reaches here: this path is only taken for a launcher
// the caller NAMED, and telling somebody who asked for a sandbox to run without
// one is the opposite of what they asked.
func TestAMissingLauncherNamesTheBinaryThatIsNotThere(t *testing.T) {
	h := newHarness(t)

	err := h.run("session", "claude", "--launcher", "a-launcher-that-is-not-installed")
	if err == nil {
		t.Fatal("a missing launcher was not reported")
	}
	if !strings.Contains(err.Error(), "a-launcher-that-is-not-installed") {
		t.Errorf("the refusal does not name the missing binary: %v", err)
	}
	if strings.Contains(err.Error(), "--bare") {
		t.Errorf("the refusal advises dropping a sandbox the caller asked for: %v", err)
	}
}

// A launcher is a thing Luna calls, never a thing Luna needs. `--bare` has to
// work on a machine that has none installed — otherwise the verb would have
// quietly given this project its first dependency, and it would be one that
// `go.mod` cannot show.
func TestStartingAnAgentNeedsNoLauncherInstalled(t *testing.T) {
	h := newHarness(t)

	got := h.launched(t, "some-agent", "--bare")
	if len(got) != 2 || got[0] != "some-agent" {
		t.Fatalf("--bare did not start the agent on its own: %v", got)
	}
	if !strings.Contains(got[1], "starting work in") {
		t.Errorf("a bare session lost the briefing: %q", got[1])
	}
}

// The distinction that carries the security: a launcher the caller NAMED is a
// request for containment. Starting an unsandboxed agent because it was missing
// would be a silent downgrade, and the person asked for the opposite.
func TestANamedLauncherThatIsMissingIsRefusedRatherThanDropped(t *testing.T) {
	h := newHarness(t)

	// The composition is what is asserted, not the error: with the guard removed
	// this still failed to start anything, so a test that only checked for an
	// error passed while the agent was being launched unsandboxed.
	var got []string
	h.env.Launch = func(command []string) error {
		got = command
		return nil
	}
	t.Cleanup(func() { h.env.Launch = nil })

	if err := h.run("session", "claude", "--launcher", "a-sandbox-that-is-not-installed"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "a-sandbox-that-is-not-installed" {
		t.Fatalf("a named launcher was dropped and the agent started without it: %v", got)
	}
	if strings.Contains(h.err.String(), "no sandbox") {
		t.Errorf("a named launcher was treated as an absent default:\n%s", h.err.String())
	}
}

// The fallback must be loud. Somebody who expected a sandbox and got none has to
// read it on the way past, or the surprise arrives later and worse.
func TestTheFallbackToNoLauncherSaysWhatWasLost(t *testing.T) {
	h := newHarness(t)
	// This machine has the default launcher installed; a machine that does not
	// is the case being tested, so PATH is emptied for the lookup.
	t.Setenv("PATH", "")

	var got []string
	h.env.Launch = func(command []string) error {
		got = command
		return nil
	}
	t.Cleanup(func() { h.env.Launch = nil })

	if err := h.run("session", "some-agent"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "some-agent" {
		t.Fatalf("the session did not fall back to starting the agent directly: %v", got)
	}
	warning := h.err.String()
	if !strings.Contains(warning, "ai-run") {
		t.Errorf("the fallback does not name the launcher that is missing:\n%s", warning)
	}
	for _, want := range []string{"no sandbox", "--bare", "--launcher"} {
		if !strings.Contains(warning, want) {
			t.Errorf("the fallback does not mention %q:\n%s", want, warning)
		}
	}
}

// An explicit --bare is a choice, not a degradation, and warning about it would
// train people to ignore the warning that matters.
func TestAnExplicitBareSessionWarnsAboutNothing(t *testing.T) {
	h := newHarness(t)

	h.launched(t, "some-agent", "--bare")
	if strings.Contains(h.err.String(), "no sandbox") {
		t.Errorf("--bare was warned about as though it were a fallback:\n%s", h.err.String())
	}
}

// sweepStaleTestDirs removes what a killed test run left behind.
//
// These live under $HOME rather than /tmp because the durability guard refuses
// tmpfs — correctly — so the usual "the OS cleans it" does not apply. t.Cleanup
// covers a test that finishes, and covers nothing when the run is killed by a
// timeout or a ctrl-c. Fifty-eight directories accumulated over two days that
// way before anybody looked.
//
// An hour is well past any real run of this suite, and old enough that a
// directory still in use by a concurrent run is not touched.
func sweepStaleTestDirs(home string) {
	entries, err := os.ReadDir(home)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".luna-cli-test-") {
			continue
		}
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) < time.Hour {
			continue
		}
		_ = os.RemoveAll(filepath.Join(home, entry.Name()))
	}
}

// The listing is `luna runs`. `report` was its name until it was found to mean
// three things in one binary — the verb, check's verdict printer, and main's
// exit-code mapper — and to be unfindable with rg among Go's "reports whether"
// idiom.
//
// The alias exists so a skill stack calling the old name does not break the
// moment the binary updates, and it is deliberately absent from the help: an
// alias somebody discovers is an alias somebody starts typing.
func TestTheListingAnswersToBothNamesButIsDocumentedUnderOne(t *testing.T) {
	h := newHarness(t)
	if err := h.run("record", "--run", "OPEN-1", "--event", "phase",
		"--status", "running", "--phase", "forge"); err != nil {
		t.Fatal(err)
	}

	if err := h.run("runs"); err != nil {
		t.Fatalf("luna runs: %v", err)
	}
	byNewName := h.stdout()
	if !strings.Contains(byNewName, "OPEN-1") {
		t.Fatalf("the listing does not carry the open run:\n%s", byNewName)
	}

	h.out.Reset()
	if err := h.run("report"); err != nil {
		t.Fatalf("the old name stopped working: %v", err)
	}
	if h.stdout() != byNewName {
		t.Errorf("the alias answers differently:\n--- runs ---\n%s\n--- report ---\n%s",
			byNewName, h.stdout())
	}

	h.out.Reset()
	if err := h.run("help"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.stdout(), "luna report") {
		t.Error("the compatibility alias is advertised in the help")
	}
	if !strings.Contains(h.stdout(), "luna runs") {
		t.Error("the help does not document the listing under its own name")
	}
}

// A worktree's HEAD is not the main checkout's HEAD, and the delivery being
// proven is the one in the worktree the caller is standing in. Resolving the
// wrong one runs every check over the base and records it as proof — the
// measured defect behind this test, seen in six tasks across two days.
func TestCheckResolvesTheHeadOfTheWorktreeItRunsIn(t *testing.T) {
	h := newHarness(t)
	base := h.commit("a.txt", "one")

	// A worktree beside the repository, carrying a commit the main checkout
	// does not have.
	tree := filepath.Join(filepath.Dir(h.env.Dir), "delivery")
	h.git("worktree", "add", "--quiet", "-b", "delivery", tree, "HEAD")

	inTree := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = tree
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.WriteFile(filepath.Join(tree, "delivered.txt"), []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	inTree("add", "-A")
	inTree("commit", "--quiet", "-m", "deliver")
	delivered := inTree("rev-parse", "HEAD")

	if delivered == base {
		t.Fatal("the worktree and the repository share a HEAD, so this proves nothing")
	}

	// An existence check for the file only the worktree's commit carries: it
	// passes over the delivery and fails over the base.
	const existenceContract = `
phase    = "forge"
produces = ["delivered"]

[verify.delivered]
kind = "existence"
path = "delivered.txt"
`
	path := filepath.Join(tree, "contract.toml")
	if err := os.WriteFile(path, []byte(existenceContract), 0o600); err != nil {
		t.Fatal(err)
	}

	h.env.Dir = tree
	if err := h.run("check", "--contract", path, "--run", "MAX-9"); err != nil {
		t.Fatalf("the delivery in the worktree was not proven: %v\n%s", err, h.stdout())
	}

	lines := h.lines()
	if len(lines) == 0 {
		t.Fatal("nothing was recorded")
	}
	if got := lines[len(lines)-1].Verdict; got != "passed" {
		t.Errorf("the recorded verdict is %q — the check ran over the base, not the delivery", got)
	}
}
