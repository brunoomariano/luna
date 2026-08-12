package cli

import (
	"bufio"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/herdr"
	"github.com/brunoomariano/luna/src/internal/lead"
)

// ── parseRunOptions ──────────────────────────────────────────────────────────

// TestRunOptionsDefaultToClaudeInTheCurrentRepo covers what an unflagged run means.
//
// The defaults are the whole command line someone types when they type nothing:
// the checkout they are standing in, and a real run rather than a rehearsal. Dry
// defaulting to true would be the dangerous one — a person would watch a task
// "finish" having proven nothing.
//
// The agent defaults to empty on purpose: each stage's role decides which agent
// runs it (ADR-0040), and a default here would silently override all of them.
func TestRunOptionsDefaultToTheCurrentRepoAndTheRolesAgents(t *testing.T) {
	opts, err := parseRunOptions(nil)
	if err != nil {
		t.Fatalf("no flags is a valid command line: %v", err)
	}
	if opts.Agent != "" {
		t.Errorf("an unstated agent leaves the roles to decide, got %q", opts.Agent)
	}
	if opts.Repo != "." {
		t.Errorf("want the current checkout by default, got %q", opts.Repo)
	}
	if opts.Dry {
		t.Error("a run must be real unless --dry-run was asked for")
	}
	if opts.Socket != "" {
		t.Errorf("an empty socket resolves the usual way, got %q", opts.Socket)
	}
}

// TestEachRunFlagSetsItsField covers the flags reaching the field they name.
//
// A flag parsed into the wrong field is the kind of mistake that only shows up as
// herdr dialling the wrong socket, far from the line that caused it.
func TestEachRunFlagSetsItsField(t *testing.T) {
	opts, err := parseRunOptions([]string{
		"--agent", "codex",
		"--socket", "/tmp/herdr.sock",
		"--repo", "/srv/checkout",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Agent != "codex" {
		t.Errorf("want the agent given, got %q", opts.Agent)
	}
	if opts.Socket != "/tmp/herdr.sock" {
		t.Errorf("want the socket given, got %q", opts.Socket)
	}
	if opts.Repo != "/srv/checkout" {
		t.Errorf("want the repo given, got %q", opts.Repo)
	}
}

// TestDryRunCarriesNoValue covers the switch form.
//
// `--dry-run` is a switch, not a setting. If it demanded a value, `luna run X
// --dry-run` would swallow whatever came next as its argument — or fail on a
// command line that reads perfectly well.
func TestDryRunCarriesNoValue(t *testing.T) {
	opts, err := parseRunOptions([]string{"--dry-run"})
	if err != nil {
		t.Fatalf("--dry-run takes no value: %v", err)
	}
	if !opts.Dry {
		t.Error("want the dry run set")
	}
}

// TestRunRejectsAnUnknownFlag covers the typo path.
//
// The message names the flag because that is the only thing the person can act on:
// "usage" alone leaves them rereading a command line they already believe is right.
func TestRunRejectsAnUnknownFlag(t *testing.T) {
	_, err := parseRunOptions([]string{"--agnet", "claude"})

	if !errors.Is(err, ErrUsage) {
		t.Fatalf("want ErrUsage, got %v", err)
	}
	if !strings.Contains(err.Error(), "agnet") {
		t.Errorf("the error should name the flag that was wrong, got %v", err)
	}
}

// ── luna run, dry ────────────────────────────────────────────────────────────

// TestADryRunDrivesANightlyTaskToTheEnd is the engine end to end with no herdr.
//
// A nightly task stops at no gate, so one command takes it from an empty log to
// done — and on the way it exercises everything that is not the integration: the
// flow's ordering, every stage's contract check, the append-only log and the replay
// that rebuilds the state from it. It is what tells a broken flow apart from a
// broken herdr, which is the reason --dry-run exists at all.
func TestADryRunDrivesANightlyTaskToTheEnd(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--profile", "nightly")

	out := h.mustRun(t, "run", "LUNA-1", "--dry-run")

	if !strings.Contains(out, "LUNA-1 finished") {
		t.Errorf("want it to say the task finished, got %q", out)
	}

	// The state is asserted through `task show` rather than a replay, because the
	// printed line is what a person actually reads to find out where a task landed.
	shown := h.mustRun(t, "task", "show", "LUNA-1")
	if !strings.Contains(shown, "done") {
		t.Errorf("want task show to report it done, got %q", shown)
	}
}

// TestTheDryNodeProvesNothingAndSaysSo is the honesty of a rehearsal.
//
// The dry node hands back every artifact the contract asked for, which is what lets
// the flow run to the end. What it must not do is dress that up as verification: no
// test was run, no file was checked, and the evidence says `existence` because that
// is the truth about what was proven (ADR-0032). A dry run that recorded a full
// verdict would leave a log claiming the work was checked, and the log is the audit
// trail (INV-core-2).
func TestTheDryNodeProvesNothingAndSaysSo(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--profile", "nightly")
	h.mustRun(t, "run", "LUNA-1", "--dry-run")

	state, err := h.env.Store.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}

	// `code` is produced by build, the stage a real run would have most to prove
	// about.
	evidence, ok := state.Evidence["code"]
	if !ok {
		t.Fatal("the dry run should have delivered code")
	}
	if evidence.Scope != fsm.ScopeExistence {
		t.Errorf("a dry run proves only existence, got scope %q", evidence.Scope)
	}
}

// TestADryRunDeliversWhatTheHumanWasOwedToo covers ProducesForHuman.
//
// A stage owes two lists, and a node that honoured only Produces would leave every
// stage with a human deliverable unable to close — the flow would stop at qa and
// nobody would learn why until they read the contract.
func TestADryRunDeliversWhatTheHumanWasOwedToo(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--profile", "nightly")
	h.mustRun(t, "run", "LUNA-1", "--dry-run")

	state, err := h.env.Store.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}

	// qa_report is ProducesForHuman only; verify owes dod_checked the same way.
	for _, owed := range []fsm.Artifact{"qa_report", "dod_checked"} {
		if _, ok := state.Evidence[owed]; !ok {
			t.Errorf("want %q delivered, got evidence for %v", owed, state.Evidence)
		}
	}
}

// TestRunOnATaskThatDoesNotExist covers the empty-log path.
//
// Replaying an unopened log yields a blank state rather than a failure, so without
// this check a typo'd id would start conducting a task nobody created. The message
// names the id and the command that would open it, because a mistyped id and a
// forgotten `task new` look identical from here.
func TestRunOnATaskThatDoesNotExist(t *testing.T) {
	h := newHarness(t)

	err := h.run(t, "run", "nowhere", "--dry-run")

	if err == nil {
		t.Fatal("running a task that was never created must fail")
	}
	if !strings.Contains(err.Error(), "nowhere") {
		t.Errorf("the error should name the task, got %v", err)
	}
	if !strings.Contains(err.Error(), "luna task new") {
		t.Errorf("the error should say how to create it, got %v", err)
	}
}

// TestRunNeedsATaskID covers the missing argument.
func TestRunNeedsATaskID(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "run"); !errors.Is(err, ErrUsage) {
		t.Errorf("want ErrUsage, got %v", err)
	}
}

// TestRunSurfacesAMistypedFlagBeforeTouchingTheStore covers the order of checks.
//
// Parsing before conducting is what keeps a typo a usage error. Reaching the store
// first would report a task problem for a command-line problem, and dialling herdr
// first would fail on the integration for a mistyped flag.
func TestRunSurfacesAMistypedFlagBeforeTouchingTheStore(t *testing.T) {
	h := newHarness(t)

	err := h.run(t, "run", "never-created", "--dry-runn")

	if !errors.Is(err, ErrUsage) {
		t.Fatalf("want ErrUsage, got %v", err)
	}
	if strings.Contains(err.Error(), "no task") {
		t.Errorf("a flag typo must not be reported as a missing task, got %v", err)
	}
}

// ── luna run, stopping at a gate ─────────────────────────────────────────────

// TestAnInteractiveRunStopsAtTheFirstGate covers the supervised profile end to end.
//
// The default profile stops at every gate, and the run has to end there rather than
// deciding on the person's behalf. The output carries the command that answers it
// because a suspended task released its slot: nothing is running to remind anyone it
// exists, and this line is what stands between that and a task waiting forever
// (INV-core-12).
func TestAnInteractiveRunStopsAtTheFirstGate(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1") // interactive by default

	out := h.mustRun(t, "run", "LUNA-1", "--dry-run")

	if !strings.Contains(out, "waiting at discovery") {
		t.Errorf("want the stage it stopped at, got %q", out)
	}
	if !strings.Contains(out, "confirm the repositories") {
		t.Errorf("want the reason it stopped, got %q", out)
	}
	if !strings.Contains(out, "luna gate show LUNA-1") {
		t.Errorf("want the command that answers it, got %q", out)
	}
}

// TestAnsweringAGateLetsTheRunCarryOn covers the resume.
//
// A gate is a pause, not an ending. If the second run did not pick the task up where
// the first left it, every supervised task would need restarting from scratch.
func TestAnsweringAGateLetsTheRunCarryOn(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")
	h.mustRun(t, "run", "LUNA-1", "--dry-run")

	h.mustRun(t, "gate", "approve", "LUNA-1")
	out := h.mustRun(t, "run", "LUNA-1", "--dry-run")

	// It moves on, and stops at the next gate rather than the one just answered.
	if strings.Contains(out, "waiting at discovery") {
		t.Errorf("an approved gate must not stop the task again, got %q", out)
	}
}

// ── luna run, blocked ────────────────────────────────────────────────────────

// blockedStore stages a task stopped by a stage that did not deliver.
//
// It is the cheapest honest route to a block: a stage declares what it produces, and
// the reducer refuses to close one that came back empty (ADR-0028). Reaching a block
// through a failing node would need a node that fails, and what these tests are about
// is what the CLI does once a task is blocked, not how it got there.
func blockedStore(t *testing.T, h *harness, id string) {
	t.Helper()

	for i, action := range []fsm.Action{
		fsm.TaskCreated{Kind: fsm.KindFeature, Profile: fsm.ProfileNightly},
		fsm.Advance{Flow: fsm.DefaultFlow()},  // into discovery, which owes repos
		fsm.Complete{Flow: fsm.DefaultFlow()}, // delivered nothing
	} {
		if err := h.env.Store.AppendAction(id, action); err != nil {
			t.Fatalf("seeding step %d: %v", i, err)
		}
	}

	state, err := h.env.Store.Replay(id, fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying the seed: %v", err)
	}
	if state.Status != fsm.StatusBlocked {
		t.Fatalf("the seed should block the task, got %q", state.Status)
	}
}

// TestRunReportsABlockedTaskAndHowToClearIt covers the blocked branch of reportRun.
//
// A block is waiting on a person, so the run ends rather than pushing past it. The
// reason is printed because the reason is the whole value of the block — INV-core-8
// is about not failing silently, and a status with no explanation is a quieter kind
// of silence.
func TestRunReportsABlockedTaskAndHowToClearIt(t *testing.T) {
	h := newHarness(t)
	blockedStore(t, h, "LUNA-1")

	out := h.mustRun(t, "run", "LUNA-1", "--dry-run")

	if !strings.Contains(out, "LUNA-1 is blocked") {
		t.Errorf("want it to say the task is blocked, got %q", out)
	}
	if !strings.Contains(out, "did not deliver") {
		t.Errorf("want the reason it blocked, got %q", out)
	}
	if !strings.Contains(out, "luna unblock LUNA-1") {
		t.Errorf("want the command that clears it, got %q", out)
	}
}

// ── luna unblock ─────────────────────────────────────────────────────────────

// TestUnblockClearsABlockAndSaysWhatIsNext covers the path every block ends on.
//
// A stall, a lost herdr, a stage whose verification failed — they all arrive here,
// and a person deciding the situation is dealt with is the only way out. The block is
// cleared by appending, never by editing what was written: the history is the audit
// trail (INV-core-2), and a block that was retracted from the log is a block nobody
// can learn from.
func TestUnblockClearsABlockAndSaysWhatIsNext(t *testing.T) {
	h := newHarness(t)
	blockedStore(t, h, "LUNA-1")

	before, err := h.env.Store.Events("LUNA-1")
	if err != nil {
		t.Fatalf("reading: %v", err)
	}

	out := h.mustRun(t, "unblock", "LUNA-1")

	if !strings.Contains(out, "LUNA-1 unblocked") {
		t.Errorf("want a confirmation, got %q", out)
	}
	if !strings.Contains(out, "luna run LUNA-1") {
		t.Errorf("want the command that resumes it, got %q", out)
	}

	after, err := h.env.Store.Events("LUNA-1")
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if len(after) != len(before)+1 {
		t.Errorf("unblocking appends exactly one event, went from %d to %d", len(before), len(after))
	}

	state, err := h.env.Store.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if state.Status == fsm.StatusBlocked {
		t.Error("the task must not still be blocked")
	}
}

// TestAnUnblockedTaskRunsAgain is the point of unblocking.
//
// Clearing the status is worth nothing if the task cannot make progress afterwards.
// This is the criterion the message promises when it says "run it again".
func TestAnUnblockedTaskRunsAgain(t *testing.T) {
	h := newHarness(t)
	blockedStore(t, h, "LUNA-1")
	h.mustRun(t, "unblock", "LUNA-1")

	out := h.mustRun(t, "run", "LUNA-1", "--dry-run")

	if !strings.Contains(out, "finished") {
		t.Errorf("want the task to carry on to the end, got %q", out)
	}
}

// TestUnblockRefusesATaskThatIsNotBlocked covers the guard.
//
// Unblocking resets the retry budget, so doing it to a healthy task would quietly
// hand a failing stage more attempts than the escalation allows. The refusal names
// the state the task is actually in, because someone reaching for `unblock` believes
// it is blocked and needs to hear what it is instead.
func TestUnblockRefusesATaskThatIsNotBlocked(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--profile", "nightly")
	h.mustRun(t, "run", "LUNA-1", "--dry-run") // runs to done

	err := h.run(t, "unblock", "LUNA-1")

	if err == nil {
		t.Fatal("unblocking a task that is not blocked must fail")
	}
	if !strings.Contains(err.Error(), "not blocked") {
		t.Errorf("want it to say the task is not blocked, got %v", err)
	}
	if !strings.Contains(err.Error(), string(fsm.StatusDone)) {
		t.Errorf("want the state it is actually in, got %v", err)
	}
}

// TestUnblockOnATaskThatDoesNotExistSaysSo covers the trap in replaying an empty
// log.
//
// Replay of nothing yields a healthy zero state, so without a guard the command
// would answer "ready, not blocked" for a task nobody ever created — telling the
// person it exists and is fine. The other commands check the log length first,
// and this one has to as well.
func TestUnblockOnATaskThatDoesNotExistSaysSo(t *testing.T) {
	h := newHarness(t)

	err := h.run(t, "unblock", "never-created")

	if err == nil {
		t.Fatal("unblocking a task that does not exist must fail")
	}
	if !strings.Contains(err.Error(), "never-created") {
		t.Errorf("the error should name the task, got %v", err)
	}
	if strings.Contains(err.Error(), "not blocked") {
		t.Errorf("a missing task must not be reported as a healthy one, got %v", err)
	}
}

// TestUnblockNeedsATaskID covers the missing argument.
func TestUnblockNeedsATaskID(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "unblock"); !errors.Is(err, ErrUsage) {
		t.Errorf("want ErrUsage, got %v", err)
	}
}

// ── reportRun ────────────────────────────────────────────────────────────────

// TestReportRunOnAnUnexpectedStatus covers the branch no ordinary run reaches.
//
// Run returns on three endings — done, gated, blocked — so a state that is still
// running only arrives here if the loop grows a fourth way out. The branch prints the
// stage and the status rather than nothing, which is what keeps a future ending from
// finishing in silence. It is called directly because reaching it through a real run
// would mean the lead was broken.
func TestReportRunOnAnUnexpectedStatus(t *testing.T) {
	h := newHarness(t)

	state := fsm.NewTaskState("LUNA-1", fsm.KindFeature)
	state.Status = fsm.StatusRunning
	state.Stage = "build"

	if err := reportRun(h.env, "LUNA-1", state); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := h.out.String()
	if !strings.Contains(out, "build") {
		t.Errorf("want the stage it stopped at, got %q", out)
	}
	if !strings.Contains(out, string(fsm.StatusRunning)) {
		t.Errorf("want the status, got %q", out)
	}
}

// TestReportRunOnAGateWithNoDetail covers the nil-gate guard.
//
// A task awaiting a gate normally carries one, but reportRun reads the reason
// defensively: printing a line with an empty reason beats panicking on the command
// someone ran to find out what went wrong.
func TestReportRunOnAGateWithNoDetail(t *testing.T) {
	h := newHarness(t)

	state := fsm.NewTaskState("LUNA-1", fsm.KindFeature)
	state.Status = fsm.StatusAwaitingGate
	state.Stage = "spec"
	state.Gate = nil

	if err := reportRun(h.env, "LUNA-1", state); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(h.out.String(), "luna gate show LUNA-1") {
		t.Errorf("want the command that answers it, got %q", h.out.String())
	}
}

// ── wiring ───────────────────────────────────────────────────────────────────

// dryNode must satisfy the interface the lead declares, or --dry-run would not
// compile into a conductor at all. Asserting it here documents that the dry branch
// and the herdr branch are interchangeable at exactly one point: the Node field.
var _ lead.Node = dryNode{}

// TestConductBuildsADryConductorWithoutTouchingHerdr covers the branch that makes
// --dry-run possible.
//
// The node is chosen in conduct and nowhere else (ADR-0030). The dry branch must
// return before dialling, because a rehearsal that needed herdr running could not
// tell a broken flow from a broken integration — which is the one question it exists
// to answer.
func TestConductBuildsADryConductorWithoutTouchingHerdr(t *testing.T) {
	h := newHarness(t)

	conductor, cleanup, err := conduct(h.env, runOptions{Dry: true}, fsm.ProfileNightly)
	if err != nil {
		t.Fatalf("a dry conductor needs nothing from the outside: %v", err)
	}
	defer cleanup()

	if _, ok := conductor.Node.(dryNode); !ok {
		t.Errorf("want the dry node, got %T", conductor.Node)
	}
	if conductor.Store != h.env.Store {
		t.Error("the conductor must write to the store the command was given")
	}
	if conductor.Gates == nil {
		t.Error("the conductor must carry the gate policy the config decided")
	}
	if conductor.Gates == nil {
		t.Error("without a gate policy every profile falls back to the cautious one")
	}
}

// TestTheWatchdogBudgetComesFromTheTasksOwnProfile covers where the budget is
// read from.
//
// It has to be the profile the task was created under, taken from its log — not a
// fixed one. Reading a constant gave a nightly run the supervised timeout, which
// is backwards: the run with nobody watching is the one whose watchdog is its
// only net (ADR-0034).
func TestTheWatchdogBudgetComesFromTheTasksOwnProfile(t *testing.T) {
	h := newHarness(t)
	h.env.Config = Config{Profiles: map[fsm.Profile]Policy{
		"tight": {Gates: map[fsm.GateKind]bool{}, Budgets: fsm.Budgets{Turn: time.Minute}},
		"loose": {Gates: map[fsm.GateKind]bool{}, Budgets: fsm.Budgets{Turn: time.Hour}},
	}}

	// The two profiles differ only in their budgets, so whichever the run picks up
	// is observable.
	if got := h.env.profiles().Budgets("tight").Turn; got != time.Minute {
		t.Fatalf("the config must carry the profile's own budget, got %s", got)
	}
	if got := h.env.profiles().Budgets("loose").Turn; got != time.Hour {
		t.Fatalf("the config must carry the profile's own budget, got %s", got)
	}

	// A profile the config never defined still gets a net rather than none:
	// deleting a profile must not turn its running tasks into ones that hang.
	if got := h.env.profiles().Budgets("deleted").Resolve().Turn; got != fsm.DefaultBudgets().Turn {
		t.Errorf("an undefined profile falls back to the shipped budget, got %s", got)
	}
}

// TestConductDialsHerdrForARealRun covers the branch a dry run never reaches.
//
// It is where the two systems are wired together (ADR-0027): the node that talks
// to herdr, the verifier that does not, and the agent kind the flags chose. A
// fake socket is enough to prove the wiring without a running herdr.
func TestConductDialsHerdrForARealRun(t *testing.T) {
	h := newHarness(t)
	socket := fakeHerdrSocket(t)

	conductor, cleanup, err := conduct(h.env, runOptions{
		Agent:  "codex",
		Socket: socket,
		Repo:   "/some/repo",
	}, fsm.ProfileNightly)
	if err != nil {
		t.Fatalf("dialling a reachable herdr: %v", err)
	}
	defer cleanup()

	node, ok := conductor.Node.(*herdr.Node)
	if !ok {
		t.Fatalf("want the herdr node, got %T", conductor.Node)
	}
	// --agent overrides every role's agent, which is what makes a run reproducible
	// against one harness while the roles are still being tuned (ADR-0040).
	if node.Roles == nil {
		t.Fatal("the node needs roles to resolve")
	}
	if role, ok := node.Roles("implementer"); !ok || role.Agent != "codex" {
		t.Errorf("want the override applied to every role, got %+v (found=%v)", role, ok)
	}
	if node.Runner == nil {
		t.Error("the node needs something to drive herdr with")
	}
	// Verification does not go through herdr (ADR-0035), so the node must be
	// given a prover rather than left to invent evidence.
	if node.Prove == nil {
		t.Error("the node must carry a verifier")
	}
}

// TestConductReportsAnAbsentHerdr covers the message someone sees first.
//
// A run that cannot reach herdr must say so plainly rather than failing somewhere
// downstream: it is the most common way this goes wrong, and the fix is to start
// herdr rather than to debug the flow (ADR-0033).
func TestConductReportsAnAbsentHerdr(t *testing.T) {
	h := newHarness(t)

	_, _, err := conduct(h.env, runOptions{
		Socket: filepath.Join(t.TempDir(), "nothing.sock"),
	}, fsm.ProfileNightly)

	if err == nil {
		t.Fatal("an unreachable herdr must stop the run")
	}
	if !strings.Contains(err.Error(), "herdr running") {
		t.Errorf("the error should say what to do about it, got %v", err)
	}
}

// fakeHerdrSocket answers a ping and nothing else, which is all conduct needs to
// prove it dialled.
func fakeHerdrSocket(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "h.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			if _, err := bufio.NewReader(conn).ReadBytes('\n'); err == nil {
				_, _ = conn.Write([]byte(`{"id":"1","result":{"type":"pong"}}` + "\n"))
			}
			_ = conn.Close()
		}
	}()

	return path
}

// TestALostHerdrBlocksTheTaskRatherThanFailingTheRun covers ADR-0033 end to end.
//
// A herdr that goes away mid-stage is infrastructure, not a task failure. The lead
// records a block and the run ends normally, so the task is visible in `luna gates`
// with a reason — rather than the command erroring and leaving nothing behind.
func TestALostHerdrBlocksTheTaskRatherThanFailingTheRun(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--profile", "nightly")

	// A socket that answers the dial and then refuses everything, which is what a
	// herdr exiting between the connection and the first stage looks like.
	out := h.mustRun(t, "run", "LUNA-1", "--socket", deadHerdrSocket(t))

	if !strings.Contains(out, "blocked") {
		t.Errorf("want the task reported as blocked, got %q", out)
	}

	// And the reason is in the log, so the block is not a mystery later.
	state, err := h.env.Store.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if state.Status != fsm.StatusBlocked {
		t.Fatalf("want the task blocked, got %q", state.Status)
	}
	if !strings.Contains(state.Blocked, "herdr") {
		t.Errorf("the reason must name what went away, got %q", state.Blocked)
	}
	// A stall is not a stage failure: the retry budget stays untouched (ADR-0011).
	if state.Retry.Attempts != 0 {
		t.Errorf("infrastructure must not spend the retry budget, got %d", state.Retry.Attempts)
	}
}

// deadHerdrSocket answers the dial's ping and then hangs up on everything after,
// imitating a herdr that exits mid-run.
func deadHerdrSocket(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dead.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		first := true
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			if first {
				if _, err := bufio.NewReader(conn).ReadBytes('\n'); err == nil {
					_, _ = conn.Write([]byte(`{"id":"1","result":{"type":"pong"}}` + "\n"))
				}
				first = false
			}
			_ = conn.Close()
		}
	}()

	return path
}

// TestRunRefusesToStartWhenTheStoreCannotAnswer covers the guard before anything
// is driven.
//
// A store that will not answer is not a task problem, and starting a run against
// it would append events on top of a log nobody could read back.
func TestRunRefusesToStartWhenTheStoreCannotAnswer(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--profile", "nightly")

	if err := h.env.Store.Close(); err != nil {
		t.Fatalf("closing the store: %v", err)
	}

	err := h.run(t, "run", "LUNA-1", "--dry-run")

	if err == nil {
		t.Fatal("a run against an unreadable store must fail")
	}
}

// TestUnblockRefusesWhenTheStoreCannotAnswer covers the same guard on the other
// command that reads before writing.
func TestUnblockRefusesWhenTheStoreCannotAnswer(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--profile", "nightly")

	if err := h.env.Store.Close(); err != nil {
		t.Fatalf("closing the store: %v", err)
	}

	if err := h.run(t, "unblock", "LUNA-1"); err == nil {
		t.Fatal("unblocking against an unreadable store must fail")
	}
}
