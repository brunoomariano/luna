package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brunoomariano/luna/src/internal/agent"
	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/lead"
	"github.com/brunoomariano/luna/src/internal/node"
	"github.com/brunoomariano/luna/src/internal/store"
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
// runs it, and a default here would silently override all of them.
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
}

// TestEachRunFlagSetsItsField covers the flags reaching the field they name.
//
// A flag parsed into the wrong field is the kind of mistake that only shows up
// as a run against the wrong checkout, far from the line that caused it.
func TestEachRunFlagSetsItsField(t *testing.T) {
	opts, err := parseRunOptions([]string{
		"--agent", "codex",
		"--repo", "/srv/checkout",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.Agent != "codex" {
		t.Errorf("want the agent given, got %q", opts.Agent)
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

// TestADryRunDrivesAnAutonomousTaskToTheEnd is the engine end to end with no
// agent.
//
// One command takes a task from an empty log to done, exercising everything that
// is not the integration: the flow's ordering, every stage's contract check, the
// append-only log and the replay that rebuilds the state from it. It is what
// tells a broken flow apart from a broken integration, which is why --dry-run exists.
//
// The knob is what makes it unattended now rather than a profile: gates wait
// because the shipped stages declare criteria, and 10 is what lets
// the lead answer them instead of a person.
func TestADryRunDrivesAnAutonomousTaskToTheEnd(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--simulated")
	h.mustRun(t, "autonomy", "LUNA-1", "10")

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

	// And it got there by the lead answering, not by nobody being asked: a run
	// that finished without consulting anything would mean the gates stopped
	// waiting, which is the regression this whole change could cause.
	if h.judged == 0 {
		t.Error("the task finished without the lead answering a single gate")
	}
}

// TestTheDryNodeProvesNothingAndSaysSo is the honesty of a rehearsal.
//
// The dry node hands back every artifact the contract asked for, which is what lets
// the flow run to the end. What it must not do is dress that up as verification: no
// test was run, no file was checked, and the evidence says `existence` because that
// is the truth about what was proven. A dry run that recorded a full
// verdict would leave a log claiming the work was checked, and the log is the audit
// trail.
func TestTheDryNodeProvesNothingAndSaysSo(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--simulated")
	// Unattended is the knob now, not a profile.
	h.mustRun(t, "autonomy", "LUNA-1", "10")
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
// stage with a human deliverable unable to close — the flow would stop at review
// and nobody would learn why until they read the contract.
func TestADryRunDeliversWhatTheHumanWasOwedToo(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--simulated")
	// Unattended is the knob now, not a profile.
	h.mustRun(t, "autonomy", "LUNA-1", "10")
	h.mustRun(t, "run", "LUNA-1", "--dry-run")

	state, err := h.env.Store.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}

	// review_report is ProducesForHuman only; verify owes dod_checked the same way.
	for _, owed := range []fsm.Artifact{"review_report", "dod_checked"} {
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
// first would report a task problem for a command-line problem, and starting an
// agent first would fail on the integration for a mistyped flag.
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
// .
func TestAnInteractiveRunStopsAtTheFirstGate(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--simulated") // interactive by default

	out := h.mustRun(t, "run", "LUNA-1", "--dry-run")

	if !strings.Contains(out, "waiting at plan") {
		t.Errorf("want the stage it stopped at, got %q", out)
	}
	if !strings.Contains(out, "review the plan and its contract") {
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
	h.mustRun(t, "task", "new", "LUNA-1", "--simulated")
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
// the reducer refuses to close one that came back empty. Reaching a block
// through a failing node would need a node that fails, and what these tests are about
// is what the CLI does once a task is blocked, not how it got there.
func blockedStore(t *testing.T, h *harness, id string) {
	t.Helper()

	for i, action := range []fsm.Action{
		// Simulated, because the tests built on this seed drive it with --dry-run,
		// and a dry run refuses a task that is not one.
		fsm.TaskCreated{
			Kind: fsm.KindFeature, Profile: fsm.ProfileNightly,
			Flow: fsm.Fingerprint(fsm.DefaultFlow()), Simulated: true,
		},
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
// reason is printed because the reason is the whole value of the block — INV-5
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
// A stall, broken machinery, a stage whose verification failed — they all arrive here,
// and a person deciding the situation is dealt with is the only way out. The block is
// cleared by appending, never by editing what was written: the history is the audit
// trail, and a block that was retracted from the log is a block nobody
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
	// Unattended is the knob now, not a profile.
	h.mustRun(t, "autonomy", "LUNA-1", "10")

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
	h.mustRun(t, "task", "new", "LUNA-1", "--simulated")
	// Unattended is the knob now, not a profile.
	h.mustRun(t, "autonomy", "LUNA-1", "10")
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
// and the real one are interchangeable at exactly one point: the Node field.
var _ lead.Node = dryNode{}

// TestConductBuildsADryConductorWithoutTouchingTheOutside covers the branch that
// makes --dry-run possible.
//
// The node is chosen in conduct and nowhere else. The dry branch must return
// before it reaches for anything external, because a rehearsal that needed an
// agent installed could not tell a broken flow from a broken integration — which
// is the one question it exists to answer.
func TestConductBuildsADryConductorWithoutTouchingTheOutside(t *testing.T) {
	h := newHarness(t)

	conductor, cleanup, err := conduct(h.env, runOptions{Dry: true}, fsm.ProfileNightly, fsm.DefaultFlow())
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
	// The judgement half has to be carried, or a knob raised past a gate's
	// criticality reaches a lead that is not there and the gate quietly goes to a
	// person instead — a feature off on every machine, saying nothing about it.
	if conductor.Ask == nil {
		t.Error("the conductor has no model to judge a gate the knob reached")
	}

	// The mechanical half is always carried now that the declaration is replayed
	// from the task's own log rather than read from a registry: every
	// run can answer a gate the task declared checks for. What a task declared
	// nothing about still reaches judgement — TestTheSeamIsSilentAboutAGateNobody-
	// Declared is where that is covered.
	if conductor.CheckGate == nil {
		t.Error("the conductor cannot run the checks a task declared")
	}
}

// TestTheWatchdogBudgetIsProjectWide covers where the budget is read from.
//
// It used to be the profile the task was created under, and that went with
// Profiles stopped deciding anything, and the watchdog's clock never
// had to do with who answers a gate. How long a suite takes is a fact about the
// repository — one takes twenty minutes and another takes two.
func TestTheWatchdogBudgetIsProjectWide(t *testing.T) {
	h := newHarness(t)
	h.env.Config = Config{TurnBudget: time.Minute}

	if got := h.env.Config.Turn(); got != time.Minute {
		t.Errorf("the configured budget must be what the run uses, got %s", got)
	}

	// A project that set none still gets a net rather than no limit: the direction
	// that matters, because no budget is a task that hangs forever.
	if got := (Config{}).Turn(); got != fsm.DefaultBudgets().Turn {
		t.Errorf("an unset budget falls back to the shipped one, got %s", got)
	}
}

// TestConductBuildsTheStageRunnerForARealRun covers the branch a dry run never
// reaches: where the flags, the roles and the sandbox are wired into the thing
// that actually runs a stage.
func TestConductBuildsTheStageRunnerForARealRun(t *testing.T) {
	h := newHarness(t)

	conductor, cleanup, err := conduct(h.env, runOptions{
		Agent: "codex",
		Repo:  "/some/repo",
	}, fsm.ProfileNightly, fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("building the conductor: %v", err)
	}
	defer cleanup()

	runner, ok := conductor.Node.(*node.Runner)
	if !ok {
		t.Fatalf("want the stage runner, got %T", conductor.Node)
	}

	// --agent overrides every role's agent, which is what makes a run
	// reproducible against one harness while the roles are still being tuned.
	if runner.Roles == nil {
		t.Fatal("the runner needs roles to resolve")
	}
	if role, ok := runner.Roles("maker"); !ok || role.Agent != "codex" {
		t.Errorf("want the override applied to every role, got %+v (found=%v)", role, ok)
	}
	if runner.Repo != "/some/repo" {
		t.Errorf("want the runner pointed at the repository, got %q", runner.Repo)
	}
	// An artifact handed to Luna is proven by the store rather than by the tree,
	// so the runner has to be given something to ask.
	if runner.Stored == nil {
		t.Error("the runner must carry what answers a handover")
	}
	if runner.Artifacts == nil {
		t.Error("the runner must carry somewhere to receive a handover")
	}
}

// TestTheAgentIsNeverStartedOutsideTheSandbox covers the refusal that has to
// hold before anything else does.
//
// Containment is the one guarantee Luna does not implement itself, so a missing
// sandbox is a setup failure reported plainly rather than a run that quietly
// proceeds without one.
func TestTheAgentIsNeverStartedOutsideTheSandbox(t *testing.T) {
	h := newHarness(t)

	conductor, cleanup, err := conduct(h.env, runOptions{Repo: t.TempDir()}, fsm.ProfileNightly, fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("building the conductor: %v", err)
	}
	defer cleanup()

	runner, ok := conductor.Node.(*node.Runner)
	if !ok {
		t.Fatalf("want the stage runner, got %T", conductor.Node)
	}
	harness, ok := runner.Agent.(agent.Harness)
	if !ok {
		t.Fatalf("want a real harness, got %T", runner.Agent)
	}
	if harness.Sandbox != node.Sandbox {
		t.Errorf("want every agent started inside %q, got %q", node.Sandbox, harness.Sandbox)
	}
}

// TestAMissingSandboxBlocksTheTaskRatherThanFailingTheRun covers the machinery
// breaking rather than the stage failing.
//
// A sandbox that is not installed is infrastructure: the lead records a block
// and the run ends normally, so the task shows up in `luna gates` with a reason
// — rather than the command erroring and leaving nothing behind. The retry
// budget stays untouched, because retrying a missing binary only spends it.
func TestAMissingSandboxBlocksTheTaskRatherThanFailingTheRun(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")
	// Unattended is the knob now, not a profile.
	h.mustRun(t, "autonomy", "LUNA-1", "10")

	// A PATH with git but no sandbox, which is what a machine that never
	// installed one looks like. Emptying PATH outright would take git with it and
	// prove something else — the first version of this test did exactly that.
	bin := t.TempDir()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed, and this test is about the sandbox")
	}
	if err := os.Symlink(git, filepath.Join(bin, "git")); err != nil {
		t.Fatalf("linking git into the test PATH: %v", err)
	}
	t.Setenv("PATH", bin)

	out := h.mustRun(t, "run", "LUNA-1", "--repo", repoWithCommit(t))

	if !strings.Contains(out, "blocked") {
		t.Errorf("want the task reported as blocked, got %q", out)
	}

	state, err := h.env.Store.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if state.Status != fsm.StatusBlocked {
		t.Fatalf("want the task blocked, got %q", state.Status)
	}
	if state.Retry.Attempts != 0 {
		t.Errorf("infrastructure must not spend the retry budget, got %d (reason: %s)", state.Retry.Attempts, state.Blocked)
	}
}

// TestRunRefusesToStartWhenTheStoreCannotAnswer covers the guard before anything
// is driven.
//
// A store that will not answer is not a task problem, and starting a run against
// it would append events on top of a log nobody could read back.
func TestRunRefusesToStartWhenTheStoreCannotAnswer(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--simulated", "--profile", "nightly")

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

// TestAFinishedTaskLandsOnItsOwnBranch is the promise not integrating makes, asserted
// where a person would see it.
//
// The swarm bench found it unkept: a task ran every stage, recorded the right
// commit, and left `luna/<task>` on the seed. `done` has to mean ready to
// integrate, and a branch nobody moved means it does not.
func TestAFinishedTaskLandsOnItsOwnBranch(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")
	h.mustRun(t, "autonomy", "LUNA-1", "10")

	var landed struct {
		task, commit string
	}
	conductor, cleanup, err := conduct(h.env, runOptions{Dry: true}, fsm.ProfileNightly, fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("conduct: %v", err)
	}
	defer cleanup()

	// The real Land touches a repository; this asks the narrower question — is
	// the lead told to land at all, and with what.
	conductor.Land = func(_ context.Context, taskID, commit string) error {
		landed.task, landed.commit = taskID, commit
		return nil
	}

	state, err := conductor.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.Status != fsm.StatusDone {
		t.Fatalf("the task did not finish, got %q", state.Status)
	}

	if landed.task != "LUNA-1" {
		t.Errorf("a finished task did not land, got %q", landed.task)
	}
	if landed.commit != state.Base {
		t.Errorf("landed at %q, want the commit the task ended on %q", landed.commit, state.Base)
	}
}

// TestATaskThatIsNotDoneDoesNotLand covers the other side.
//
// A task waiting at a gate has not finished, and pointing its branch would tell
// a person the work is ready when it is halfway.
func TestATaskThatIsNotDoneDoesNotLand(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	landedAnyway := false
	conductor, cleanup, err := conduct(h.env, runOptions{Dry: true}, fsm.ProfileInteractive, fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("conduct: %v", err)
	}
	defer cleanup()
	conductor.Land = func(context.Context, string, string) error {
		landedAnyway = true
		return nil
	}

	state, err := conductor.Run(context.Background(), "LUNA-1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.Status == fsm.StatusDone {
		t.Fatal("this test needs a task that stops short of done")
	}
	if landedAnyway {
		t.Errorf("a task in %q landed as though it were finished", state.Status)
	}
}

// TestStatusNamesTheBranchAFinishedTaskLandedOn is the half of not integrating a person
// actually reads.
//
// `done` means ready to integrate, and integrating is a manual act — so status
// has to say *where*. Before this it printed the sha and left the branch to be
// worked out, which is how the swarm bench ended with a finished task and nobody
// able to name what to merge.
func TestStatusNamesTheBranchAFinishedTaskLandedOn(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--simulated")
	h.mustRun(t, "autonomy", "LUNA-1", "10")
	h.mustRun(t, "run", "LUNA-1", "--dry-run")

	out := h.mustRun(t, "status", "LUNA-1")

	if !strings.Contains(out, "done") {
		t.Fatalf("this test needs a finished task, got:\n%s", out)
	}
	if !strings.Contains(out, "branch luna/LUNA-1") {
		t.Errorf("status does not name the branch the work is on:\n%s", out)
	}
}

// TestStatusIsSilentAboutTheBranchUntilATaskEnds covers the other side.
//
// The branch exists from `setup` onwards, stranded where the task opened.
// Naming it before the task finishes would point a person at the wrong commit —
// which is worse than saying nothing.
func TestStatusIsSilentAboutTheBranchUntilATaskEnds(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	out := h.mustRun(t, "status", "LUNA-1")

	if strings.Contains(out, "branch") {
		t.Errorf("status named a branch for a task that has not finished:\n%s", out)
	}
}

// repoWithCommit is a real repository with one commit, because a run opens a
// worktree and a fake git would only test the fake.
func repoWithCommit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		// The machine running the tests may sign commits by default, and a test
		// repository has no key.
		{"config", "commit.gpgsign", "false"},
		{"commit", "--allow-empty", "-m", "root"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

// TestADryRunNeitherStartsAnAgentNorNeedsASandbox covers the branch that tells a
// broken flow apart from a broken integration.
//
// It exercises the engine, the log and the gates end to end with nothing
// installed — which is what makes it useful on a machine that has neither a
// harness nor a sandbox.
func TestADryRunNeitherStartsAnAgentNorNeedsASandbox(t *testing.T) {
	h := newHarness(t)

	conductor, cleanup, err := conduct(h.env, runOptions{Dry: true, Repo: t.TempDir()}, fsm.ProfileNightly, fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("building a dry conductor: %v", err)
	}
	defer cleanup()

	if _, ok := conductor.Node.(*node.Runner); ok {
		t.Error("a dry run built the real stage runner, which would start an agent")
	}
}

// TestTheConductorCarriesTheMechanicalHalfOfAGate covers the wiring that lets a
// gate be answered by a command's exit code before any model is involved.
func TestTheConductorCarriesTheMechanicalHalfOfAGate(t *testing.T) {
	h := newHarness(t)

	conductor, cleanup, err := conduct(h.env, runOptions{Repo: t.TempDir()}, fsm.ProfileInteractive, fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("building the conductor: %v", err)
	}
	defer cleanup()

	if conductor.CheckGate == nil {
		t.Error("the conductor cannot answer a gate mechanically")
	}
	// A judge is what makes the retry budget real: without one the lead blocks on
	// the first failure and the budget is never spent.
	if conductor.Judge == nil {
		t.Error("the conductor has no judge, so the retry budget would never be spent")
	}
	// Landing is what makes `done` mean "ready to integrate, on a branch a person
	// can name".
	if conductor.Land == nil {
		t.Error("the conductor cannot point the task's branch at what it delivered")
	}
}

// TestTheHandoverIsProvenByTheStoreNotTheTree covers the closure that answers
// "was it handed over?" for an artifact that never reaches a commit.
//
// A contained agent writes a document through the handover socket, not into the
// worktree, so a stage that owes one has nothing in its diff to point at. The
// store is the only witness, and the hash it returns is what the evidence
// carries — an answer of "yes" with no hash would record a handover nobody could
// later check against the content.
//
// It is asserted against the wiring `luna run` actually builds, because the
// failure being caught is the seam coming unplugged: the runner would fall back
// to whatever it does with no `Stored` and stop proving handovers at all, and
// every stage would still pass.
func TestTheHandoverIsProvenByTheStoreNotTheTree(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	if err := h.env.Store.PutBlob(store.Blob{
		TaskID: "LUNA-1", Stage: "spec", Artifact: "contract", Seq: 2, Body: []byte("the contract"),
	}); err != nil {
		t.Fatalf("handing over: %v", err)
	}

	conductor, cleanup, err := conduct(h.env, runOptions{Repo: t.TempDir()}, fsm.ProfileNightly, fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("building the conductor: %v", err)
	}
	defer cleanup()

	runner, ok := conductor.Node.(*node.Runner)
	if !ok {
		t.Fatalf("want the stage runner, got %T", conductor.Node)
	}

	hash, err := runner.Stored("LUNA-1", "spec", "contract")
	if err != nil {
		t.Fatalf("an artifact in the store must count as handed over: %v", err)
	}
	if hash == "" {
		t.Error("the handover was confirmed with no hash, so nothing can be checked against it")
	}

	// And the other direction, which is the one that matters: a stage claiming an
	// artifact it never handed over must not be able to prove it.
	if _, err := runner.Stored("LUNA-1", "spec", "never-written"); err == nil {
		t.Error("an artifact nobody handed over was proven handed over")
	}
}

// TestTheHandoverSocketIsOpenedPerTask covers the other half of the same seam.
//
// Artifacts is what the socket writes into, and it is built per task id. One
// store shared across tasks would file LUNA-2's contract under LUNA-1 — and
// since the log is append-only, that is not a mistake anything can repair.
func TestTheHandoverSocketIsOpenedPerTask(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")
	h.mustRun(t, "task", "new", "LUNA-2")

	conductor, cleanup, err := conduct(h.env, runOptions{Repo: t.TempDir()}, fsm.ProfileNightly, fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("building the conductor: %v", err)
	}
	defer cleanup()

	runner, ok := conductor.Node.(*node.Runner)
	if !ok {
		t.Fatalf("want the stage runner, got %T", conductor.Node)
	}

	if err := runner.Artifacts("LUNA-1").PutArtifact("spec", "contract", []byte("for one")); err != nil {
		t.Fatalf("handing over: %v", err)
	}

	if _, err := h.env.Store.LatestBlob("LUNA-1", "spec", "contract"); err != nil {
		t.Errorf("the handover did not land against the task that made it: %v", err)
	}
	if _, err := h.env.Store.LatestBlob("LUNA-2", "spec", "contract"); err == nil {
		t.Error("one task's handover was filed against another's log")
	}
}

// TestWhatWentWrongWithoutFailingTheStageReachesTheTerminal covers the warning
// channel the runner and the lead are both given.
//
// A worktree that would not go away does not fail a stage, and neither does a
// branch that would not move. Both are the kind of thing that costs nothing once
// and fills a disk after a hundred runs — so the one requirement is that they
// reach the person's terminal instead of being counted silently. A Warn wired to
// nothing is indistinguishable from a run where nothing went wrong.
func TestWhatWentWrongWithoutFailingTheStageReachesTheTerminal(t *testing.T) {
	h := newHarness(t)

	conductor, cleanup, err := conduct(h.env, runOptions{Repo: t.TempDir()}, fsm.ProfileNightly, fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("building the conductor: %v", err)
	}
	defer cleanup()

	runner, ok := conductor.Node.(*node.Runner)
	if !ok {
		t.Fatalf("want the stage runner, got %T", conductor.Node)
	}

	runner.Warn("the worktree for %s would not go away", "LUNA-1")
	conductor.Warn("%s finished but its branch was not moved", "LUNA-1")

	got := h.errOut.String()
	for _, want := range []string{"would not go away", "branch was not moved", "LUNA-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("a warning never reached the terminal: want %q in\n%s", want, got)
		}
	}
}

// TestWorkRunsTheStageAgentAndNothingElse is the command the lead's brief always
// assumed and Luna never had.
//
// The brief says "the agent you start does the work — you do not do it
// yourself". There was no way to start one: `luna next` reads, `luna done`
// reports, and `luna run` drives the whole flow, which is the one thing the lead
// must not do. So on TALLY-4 the lead did three stages with its own tools and
// every artifact was recorded as "reported by hand through `luna done`" — no
// spend, no blobs, no handover, and a gate whose artifact was never attached.
//
// `luna work` is the missing half: it runs the agent for the stage that is
// already open, records what the stage cost, and stops. It chooses no stage —
// there is only the running one — so it takes nothing away from the FSM.
func TestWorkRunsTheStageAgentAndNothingElse(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature", "--profile", "nightly")

	before := mustState(t, h, "LUNA-1")
	if before.Status != fsm.StatusReady {
		t.Fatalf("a new task is ready, got %q", before.Status)
	}

	// Nothing is running yet, so there is no stage to work — and the refusal has
	// to be about that rather than about the command not existing.
	err := Run(h.env, []string{"work", "LUNA-1"})
	if err == nil {
		t.Fatal("working a task with no open stage must be refused, not guessed at")
	}
	if errors.Is(err, ErrUsage) && strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("`luna work` does not exist: %v", err)
	}
	if !strings.Contains(err.Error(), "no running stage") {
		t.Errorf("the refusal must say what is missing, got %q", err)
	}

	// And with a stage open, it dispatches to the node rather than reporting for
	// it. A fake node stands in for the sandbox, which is what is under test here
	// — that `work` runs the stage — not how the sandbox runs it.
	entering := &lead.Lead{Store: h.env.Store}
	if err := entering.Enter(context.Background(), "LUNA-1"); err != nil {
		t.Fatalf("opening a stage: %v", err)
	}
	if err := Run(h.env, []string{"work", "LUNA-1"}); err != nil {
		t.Fatalf("working the open stage: %v", err)
	}

	worked := mustState(t, h, "LUNA-1")
	if worked.Stage != before.Stage && worked.Stage == "" {
		t.Error("work left the task without a stage")
	}
}

// TestWorkOpensTheNextStageOfATaskInFlight covers the gap that routed a lead
// around the part of Luna that checks.
//
// The lead's loop is `luna next` then `luna work`, and neither one opened a
// stage: `next` is a read, and `work` required one already running. So a task
// that had just closed a stage sat at `stage_done` while `next` named the stage
// that logically followed and `work` refused it — twice, byte-identically,
// because the retry could not clear a disagreement.
//
// Measured on TALLY-6, and the stall was not the cost. Given two commands that
// contradicted each other and no third, the lead reached for
// `luna run --dry-run` to understand the mechanism, and that walked the task to
// `done` with `verify` and `review` recorded as passed against code nothing had
// checked.
func TestWorkOpensTheNextStageOfATaskInFlight(t *testing.T) {
	h := newHarness(t)
	// Simulated, so the stages run no agent and cut no worktree: what is under
	// test is the transition between two `work` calls, not what a stage does.
	h.mustRun(t, "task", "new", "LUNA-1", "--simulated", "--kind", "feature", "--profile", "nightly")

	// Into the first stage and out of it again, which is where the lead's loop
	// got stuck: a closed stage, and the next one not open.
	entering := &lead.Lead{Store: h.env.Store}
	if err := entering.Enter(context.Background(), "LUNA-1"); err != nil {
		t.Fatalf("opening the first stage: %v", err)
	}
	if err := Run(h.env, []string{"work", "LUNA-1", "--dry-run"}); err != nil {
		t.Fatalf("working the first stage: %v", err)
	}

	stalled := mustState(t, h, "LUNA-1")
	if stalled.Status != fsm.StatusStageDone {
		t.Fatalf("the setup for this test wants a closed stage, got %q", stalled.Status)
	}

	// The second `work` is the one that used to be refused.
	if err := Run(h.env, []string{"work", "LUNA-1", "--dry-run"}); err != nil {
		t.Fatalf("working the stage after a closed one: %v", err)
	}

	after := mustState(t, h, "LUNA-1")
	if after.Stage == stalled.Stage {
		t.Errorf("work did not open the next stage: still at %q", after.Stage)
	}
}

// TestWorkRefusesToChooseAStage is the boundary as a test.
//
// If `luna work` ever advanced, it would be `luna run` under another name and
// the lead would have a path to flow control. It works the stage that is open
// and refuses when none is.
func TestWorkRefusesToChooseAStage(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature", "--profile", "nightly")

	_ = Run(h.env, []string{"work", "LUNA-1"})

	after := mustState(t, h, "LUNA-1")
	if after.Status != fsm.StatusReady || after.Stage != "" {
		t.Errorf("work moved the flow: %q at %q", after.Status, after.Stage)
	}
}

// TestWorkRecordsTheEvidenceItEarned is the correction to how `luna work` and
// `luna done` were first split.
//
// `luna done` records `existence` for everything, always, and deliberately: a
// stage reported by hand must not launder a verdict nobody produced. That makes
// it the wrong command to close a stage whose contract declares `make test` —
// and it is not a gap in `done`, it is the whole reason `done` is safe.
//
// `luna work` runs the real verifiers and gets real evidence. Printing a `done`
// line and discarding it meant every command-proven stage blocked with "proved
// [tests_green] with a weaker check than its contract declared" — measured on
// TALLY-4, where the agent had genuinely run the tests and the verdict was
// thrown away between the two commands.
//
// So work closes the stage it worked, carrying the evidence its verifiers
// produced. The lead's word still costs nothing: what closes the stage is what
// the tool returned, not what anybody reported.
func TestWorkRecordsTheEvidenceItEarned(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--simulated", "--kind", "feature", "--profile", "nightly")

	entering := &lead.Lead{Store: h.env.Store}
	if err := entering.Enter(context.Background(), "LUNA-1"); err != nil {
		t.Fatalf("opening a stage: %v", err)
	}

	before := mustState(t, h, "LUNA-1")
	if err := Run(h.env, []string{"work", "LUNA-1", "--dry-run"}); err != nil {
		t.Fatalf("working: %v", err)
	}

	after := mustState(t, h, "LUNA-1")
	if after.Seq == before.Seq {
		t.Fatal("work ran the stage and recorded nothing — the evidence its verifiers " +
			"produced has nowhere else to go")
	}
	for artifact, evidence := range after.Evidence {
		if evidence.Detail == "reported by hand through `luna done`" {
			t.Errorf("%s closed on a hand report from the command that ran the tool", artifact)
		}
	}
}

// TestADryRunIsRefusedOnARealTask is the guard on the flag that walked TALLY-6
// to a false `done`.
//
// `--dry-run` records every stage as passed without running an agent. Pointed at
// a task with real stages in it, that is not a rehearsal — it is four genuine
// stages followed by two invented ones, in one history, with nothing saying
// which were which. `verify` and `review` closed that way, and the log recorded
// a green pipeline against code no command had seen.
func TestADryRunIsRefusedOnARealTask(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "feature", "--profile", "nightly")

	err := Run(h.env, []string{"run", "LUNA-1", "--dry-run"})
	if err == nil {
		t.Fatal("a dry run over a real task must be refused")
	}
	if !strings.Contains(err.Error(), "real task") {
		t.Errorf("the refusal must say why, got %q", err)
	}
}

// TestARealRunIsRefusedOnASimulatedTask is the same damage read backwards.
//
// Continuing a simulation for real would leave one history where some stages ran
// and some did not — the state the refusal above exists to prevent, arriving
// through the other door.
func TestARealRunIsRefusedOnASimulatedTask(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--simulated", "--kind", "feature", "--profile", "nightly")

	err := Run(h.env, []string{"run", "LUNA-1"})
	if err == nil {
		t.Fatal("a real run over a simulated task must be refused")
	}
	if !strings.Contains(err.Error(), "simulation") {
		t.Errorf("the refusal must say why, got %q", err)
	}
}

// TestASimulatedTaskSaysSoWhereItIsRead covers the half that TALLY-6 proved
// matters most: not what was recorded, but what a person sees.
//
// The evidence a dry run records is true — the declared commands really run —
// but nothing was built for them to run against. A green pipeline here says the
// machinery works and nothing about any code, and the only thing standing
// between those two readings is this line.
func TestASimulatedTaskSaysSoWhereItIsRead(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--simulated", "--kind", "feature", "--profile", "nightly")

	for _, view := range [][]string{
		{"task", "show", "LUNA-1"},
		{"status", "LUNA-1"},
	} {
		out := h.mustRun(t, view...)
		if !strings.Contains(out, "simulation") {
			t.Errorf("`luna %s` does not say the task is a simulation:\n%s",
				strings.Join(view, " "), out)
		}
	}

	// And in the structured view, which is read by the consumer most likely to
	// treat a verdict as a measurement.
	var report TaskReport
	if err := json.Unmarshal([]byte(h.mustRun(t, "task", "show", "LUNA-1", "--json")), &report); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if !report.Simulated {
		t.Error("the JSON view does not carry the simulation flag")
	}
}
