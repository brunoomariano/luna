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
