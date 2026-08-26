package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/store"
)

// harness runs the real commands against a temporary store and captures what they
// print. Asserting on output rather than on internals is deliberate: the printed
// line is the contract a person actually depends on.
type harness struct {
	// judged counts how many times the lead was asked to answer a gate.
	judged int

	env    Env
	out    *bytes.Buffer
	errOut *bytes.Buffer
	edited string
	edits  int
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	s, err := store.OpenAs(filepath.Join(t.TempDir(), "luna.db"), store.LunaOwnsTheLog)
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	h := &harness{out: &bytes.Buffer{}, errOut: &bytes.Buffer{}}
	h.env = Env{
		Store: s,
		Out:   h.out,
		Err:   h.errOut,
		// The real one, as the binary wires it, so a test that reaches a stage
		// exercises the same construction. A test that means to drive a solo run
		// replaces it with a fake — starting a contained agent here would wait out
		// the turn budget, which is hours.
		Node: StageRunner,
		// A stand-in for the editor: returns whatever the test staged, so no test
		// needs $EDITOR or a terminal.
		Edit: func(current string) (string, error) {
			h.edits++
			if h.edited == "" {
				return current, nil // left unchanged
			}
			return h.edited, nil
		},
		// A lead that approves whatever it is asked. It exists because gates now
		// wait when a stage declared criteria, so an unattended run needs somebody
		// to answer them — and a harness with none would test the no-model path in
		// every test rather than the one that is about it.
		//
		// Tests that are about a run with no model clear this field.
		Lead: func(context.Context, string) (string, error) {
			h.judged++
			return "APPROVE\n\nevery criterion is met", nil
		},
	}
	return h
}

func (h *harness) run(t *testing.T, args ...string) error {
	t.Helper()
	h.out.Reset()
	return Run(h.env, args)
}

func (h *harness) mustRun(t *testing.T, args ...string) string {
	t.Helper()
	if err := h.run(t, args...); err != nil {
		t.Fatalf("luna %s: %v", strings.Join(args, " "), err)
	}
	return h.out.String()
}

// replay rebuilds a task's state from its log, which is how a test asserts on
// what a command actually recorded rather than on what it printed.
func (h *harness) replay(t *testing.T, id string) fsm.TaskState {
	t.Helper()
	state, err := h.env.Store.ReplayOwnFlow(id)
	if err != nil {
		t.Fatalf("replaying %s: %v", id, err)
	}
	return state
}

// loop drives a task into a convergence loop by appending the findings that
// produce one, rather than writing counters into the state.
//
// Through the real actions on purpose: the counters are the reducer's, and a test
// that set them directly would pass against a reducer that stopped counting.
func (h *harness) loop(t *testing.T, id string, want fsm.LoopCounters) {
	t.Helper()

	flow := fsm.DefaultFlow()
	for round := range want.Rounds {
		// A finding is legal from the stage a review loop returns to, so the task
		// is walked there before each one.
		h.walkTo(t, id, "audit")

		// The same signal every round is what NoProgress counts, and a distinct one
		// resets it — which is how a count smaller than the round total is made.
		progress := want.LastProgress
		if round < want.Rounds-want.NoProgress-1 {
			progress = fmt.Sprintf("round-%d", round)
		}
		state := h.replay(t, id)
		if err := h.env.Store.AppendActionAt(id, state.Seq, fsm.ReviewFinding{
			Aligned: true, Progress: progress, Flow: flow,
			Gate: fsm.GateAccount{Decision: fsm.GateDecisionPassed},
		}); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
}

// walkTo closes and advances stages until the task is running the named one.
func (h *harness) walkTo(t *testing.T, id string, stage fsm.StageID) {
	t.Helper()

	flow := fsm.DefaultFlow()
	for range 2 * len(flow) {
		state := h.replay(t, id)
		if state.Stage == stage && state.Status == fsm.StatusRunning {
			return
		}

		if state.Status == fsm.StatusRunning {
			h.closeStage(t, id, state)
			continue
		}
		if err := h.env.Store.AppendActionAt(id, state.Seq, fsm.Advance{
			Flow: flow, Gate: fsm.GateAccount{Decision: fsm.GateDecisionPassed},
		}); err != nil {
			t.Fatalf("advancing towards %s: %v", stage, err)
		}
	}
	t.Fatalf("%s never reached %s", id, stage)
}

// closeStage delivers whatever the running stage's contract asks for.
func (h *harness) closeStage(t *testing.T, id string, state fsm.TaskState) {
	t.Helper()

	flow := fsm.DefaultFlow()
	var stage fsm.Stage
	for _, s := range flow {
		if s.ID == state.Stage {
			stage = s
		}
	}

	owed := append(append([]fsm.Artifact{}, stage.Produces...), stage.ProducesForHuman...)
	evidence := map[fsm.Artifact]fsm.Evidence{}
	for _, a := range owed {
		evidence[a] = fsm.Evidence{Scope: fsm.VerifierFor(stage, a).Proves(), Verdict: fsm.VerdictPassed}
	}
	if err := h.env.Store.AppendActionAt(id, state.Seq, fsm.Complete{
		Delivered: owed, Evidence: evidence, Flow: flow,
		Commit: "c0ffee" + string(state.Stage),
		Gate:   fsm.GateAccount{Decision: fsm.GateDecisionPassed},
	}); err != nil {
		t.Fatalf("closing %s: %v", state.Stage, err)
	}
}

// ── task new ─────────────────────────────────────────────────────────────────

func TestTaskNewOpensALog(t *testing.T) {
	h := newHarness(t)

	out := h.mustRun(t, "task", "new", "LUNA-1", "--kind", "bug", "--profile", "nightly")

	if !strings.Contains(out, "created LUNA-1") {
		t.Errorf("want a confirmation naming the task, got %q", out)
	}

	state, err := h.env.Store.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if state.Context.Kind != fsm.KindBug {
		t.Errorf("want the kind recorded, got %q", state.Context.Kind)
	}
	if state.Profile != fsm.ProfileNightly {
		t.Errorf("want the profile recorded, got %q", state.Profile)
	}
}

func TestTaskNewDefaultsToFeatureAndInteractive(t *testing.T) {
	h := newHarness(t)

	h.mustRun(t, "task", "new", "LUNA-1")

	state, err := h.env.Store.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if state.Context.Kind != fsm.KindFeature {
		t.Errorf("want feature by default, got %q", state.Context.Kind)
	}
	// The cautious default: an unstated profile supervises rather than runs free.
	if state.Profile != fsm.ProfileInteractive {
		t.Errorf("want interactive by default, got %q", state.Profile)
	}
}

// TestTaskNewRecordsWhatTheTaskIsAbout covers the gap the first full run found:
// a task was an id, a kind and a profile, and nothing said what to build. The
// agents inferred the goal from the id string, and `spec` stopped to ask — six
// times out of six.
//
// The statement of work rides in the log with the task. It used to go to beads,
// and moving it here is what lets a task be created in a repository that has no
// registry and no `bd` on the path.
func TestTaskNewRecordsWhatTheTaskIsAbout(t *testing.T) {
	h := newHarness(t)

	h.mustRun(t, "task", "new", "LUNA-1",
		"--about", "the counts should be consumable by other programs",
		"--acceptance", "valid JSON out; the default output unchanged")

	state := h.replay(t, "LUNA-1")
	if state.Statement.Description != "the counts should be consumable by other programs" {
		t.Errorf("the description never reached the log, got %q", state.Statement.Description)
	}
	if state.Statement.Acceptance != "valid JSON out; the default output unchanged" {
		t.Errorf("the acceptance never reached the log, got %q", state.Statement.Acceptance)
	}
}

// TestTheWholeStatementSurvivesAReplay covers the field the first test does not,
// because a mapping that drops one shows up as an agent quietly missing its
// design rather than as a failure.
func TestTheWholeStatementSurvivesAReplay(t *testing.T) {
	h := newHarness(t)

	h.mustRun(t, "task", "new", "LUNA-1",
		"--about", "what",
		"--design", "a flag, not a subcommand",
		"--acceptance", "valid JSON out")

	state := h.replay(t, "LUNA-1")
	want := fsm.Statement{
		Description: "what",
		Design:      "a flag, not a subcommand",
		Acceptance:  "valid JSON out",
	}
	if state.Statement != want {
		t.Errorf("a field was lost on the way to the log:\n got %+v\nwant %+v", state.Statement, want)
	}
}

// TestATaskWithNoStatementIsStillATask. Stating nothing is the ordinary case for
// anything created without --about, and it must stay legal.
func TestATaskWithNoStatementIsStillATask(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "task", "new", "LUNA-1"); err != nil {
		t.Fatalf("a plain task was refused: %v", err)
	}
	if h.replay(t, "LUNA-1").Statement.Stated() {
		t.Error("a task created with no statement reports one")
	}
}

// TestARevisionReachesTheLog is the command behind `task statement`, and the
// reason it is a command at all: correcting what a task is about used to mean
// editing it in beads, where the previous wording was overwritten.
func TestARevisionReachesTheLog(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--about", "make the script work")

	h.mustRun(t, "task", "statement", "LUNA-1",
		"--about", "the script is zsh-only",
		"--acceptance", "bash -n exits 0")

	state := h.replay(t, "LUNA-1")
	if state.Statement.Description != "the script is zsh-only" {
		t.Errorf("the revision did not take, got %q", state.Statement.Description)
	}
	if state.Statement.Acceptance != "bash -n exits 0" {
		t.Errorf("the acceptance did not take, got %q", state.Statement.Acceptance)
	}
}

// TestRevisingSaysSomethingOrSaysWhy. A revision that states nothing would append
// an event that erases the statement, which is not what anybody typing it means.
func TestRevisingSaysSomethingOrSaysWhy(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--about", "the original")

	err := h.run(t, "task", "statement", "LUNA-1")
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("want a usage error, got %v", err)
	}
	if got := h.replay(t, "LUNA-1").Statement.Description; got != "the original" {
		t.Errorf("the refused revision changed the statement anyway, got %q", got)
	}
}

// TestRevisingNeedsATask. Naming no task at all is a different mistake from
// naming one that does not exist, and the usage line is what tells them apart.
func TestRevisingNeedsATask(t *testing.T) {
	h := newHarness(t)

	err := h.run(t, "task", "statement")
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("want a usage error, got %v", err)
	}
	if !strings.Contains(err.Error(), "--about") {
		t.Errorf("the usage line does not say what to pass: %v", err)
	}
}

// TestRevisingRejectsAnUnknownFlag. The statement flags and the run flags share
// one parser, so a typo has to land as a usage error rather than being recorded
// as part of the statement.
func TestRevisingRejectsAnUnknownFlag(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--about", "the original")

	if err := h.run(t, "task", "statement", "LUNA-1", "--abuot", "a typo"); !errors.Is(err, ErrUsage) {
		t.Fatalf("want a usage error, got %v", err)
	}
	if got := h.replay(t, "LUNA-1").Statement.Description; got != "the original" {
		t.Errorf("a typo changed the statement, got %q", got)
	}
}

// TestRevisingRejectsAnIdThatCannotBeATask. The id becomes a branch name and an
// agent name, so it is checked at the edge here for the same reason `task new`
// checks it — a name that cannot be one of those fails much later, somewhere that
// does not name the mistake.
func TestRevisingRejectsAnIdThatCannotBeATask(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "task", "statement", "../etc/passwd", "--about", "anything"); !errors.Is(err, ErrUsage) {
		t.Fatalf("want a usage error, got %v", err)
	}
}

// TestRevisingATaskThatNoLongerReplaysFails. A task born under a flow this build
// does not have cannot be read, so it cannot be described either — and saying so
// is better than appending to a log nothing can rebuild. `flow check` is where
// those surface, and `task abandon` is what ends them.
func TestRevisingATaskThatNoLongerReplaysFails(t *testing.T) {
	h := newHarness(t)

	stale := []fsm.Stage{{ID: "gone", Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"x"}}}
	if err := h.env.Store.AppendAction("LUNA-1", fsm.TaskCreated{
		Kind: fsm.KindChore, Flow: fsm.Fingerprint(stale),
	}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	if err := h.run(t, "task", "statement", "LUNA-1", "--about", "anything"); err == nil {
		t.Fatal("a task that no longer replays accepted a revision")
	}
}

// TestRevisingATaskThatDoesNotExistFails. The task is named in the error, rather
// than surfacing as an illegal transition out of the reducer.
func TestRevisingATaskThatDoesNotExistFails(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "task", "statement", "LUNA-404", "--about", "anything"); err == nil {
		t.Fatal("revising a task that was never created succeeded")
	}
}

func TestTaskNewRefusesToReopenAnExistingTask(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	err := h.run(t, "task", "new", "LUNA-1")

	if err == nil {
		t.Fatal("creating the same task twice must fail")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("the error should say the task is already there, got %v", err)
	}
}

func TestTaskNewRejectsAnUnknownKindOrProfile(t *testing.T) {
	h := newHarness(t)

	for _, args := range [][]string{
		{"task", "new", "LUNA-1", "--kind", "epic"},
		{"task", "new", "LUNA-2", "--profile", "yolo"},
		{"task", "new", "LUNA-3", "--colour", "blue"},
	} {
		if err := h.run(t, args...); !errors.Is(err, ErrUsage) {
			t.Errorf("%v: want ErrUsage, got %v", args, err)
		}
	}
}

// TestAMistypedProfileIsCaughtAtTheEdge covers the reason parseProfile exists.
//
// The engine treats an unrecognised profile as interactive, which is the safe
// guess. But someone who typed `--profile nightl` wanted an unattended run and
// would get a supervised one with nothing saying why — so the name is checked
// where it was typed.
func TestAMistypedProfileIsCaughtAtTheEdge(t *testing.T) {
	h := newHarness(t)

	err := h.run(t, "task", "new", "LUNA-1", "--profile", "nightl")

	if !errors.Is(err, ErrUsage) {
		t.Fatalf("want ErrUsage, got %v", err)
	}
	if !strings.Contains(err.Error(), "nightly") {
		t.Errorf("the error should list what was meant, got %v", err)
	}
}

// ── task show ────────────────────────────────────────────────────────────────

func TestTaskShowReportsTheState(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "chore")

	out := h.mustRun(t, "task", "show", "LUNA-1")

	// The autonomy knob rather than the profile: the knob is what bounds who
	// answers a gate, and it is what moves through the log.
	for _, want := range []string{"LUNA-1", "ready", "chore", "autonomy 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in the output, got %q", want, out)
		}
	}
}

// TestTaskShowPrintsWhatTheTaskIsAbout. The statement replays with the task now
// , so the command that shows a task is where a person checks what the
// agents were actually told.
func TestTaskShowPrintsWhatTheTaskIsAbout(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1",
		"--about", "the script is zsh-only",
		"--design", "a POSIX loop",
		"--acceptance", "bash -n exits 0")

	out := h.mustRun(t, "task", "show", "LUNA-1")

	for _, want := range []string{"the script is zsh-only", "a POSIX loop", "bash -n exits 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in the output, got %q", want, out)
		}
	}
}

// TestTaskShowReportsTheStatementAsJSON. The structured output is a contract
// , and the statement is the field another program most wants: it is
// what the task is for.
func TestTaskShowReportsTheStatementAsJSON(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1",
		"--about", "the script is zsh-only",
		"--design", "a POSIX loop",
		"--acceptance", "bash -n exits 0")

	var report TaskReport
	if err := json.Unmarshal([]byte(h.mustRun(t, "task", "show", "LUNA-1", "--json")), &report); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	if report.Statement == nil {
		t.Fatal("the statement is missing from the structured view")
	}
	want := StatementReport{
		About:      "the script is zsh-only",
		Design:     "a POSIX loop",
		Acceptance: "bash -n exits 0",
	}
	if *report.Statement != want {
		t.Errorf("the statement did not survive:\n got %+v\nwant %+v", *report.Statement, want)
	}
}

// TestTaskShowOmitsAnEmptyStatementFromJSON. A null field a reader has to
// special-case is worse than an absent one.
func TestTaskShowOmitsAnEmptyStatementFromJSON(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	out := h.mustRun(t, "task", "show", "LUNA-1", "--json")

	if strings.Contains(out, "statement") {
		t.Errorf("a task nobody described carries a statement field anyway:\n%s", out)
	}
}

// TestTaskShowSaysNothingAboutATaskNobodyDescribed. An empty statement must not
// grow headings with nothing under them.
func TestTaskShowSaysNothingAboutATaskNobodyDescribed(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	out := h.mustRun(t, "task", "show", "LUNA-1")

	for _, unwanted := range []string{"about", "design", "done"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("an empty statement was given a %q line anyway:\n%s", unwanted, out)
		}
	}
}

func TestTaskShowListsWhatWasProducedWithItsEvidence(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--profile", "nightly")

	// Drive the task through the first stage so it has produced something.
	for _, action := range []fsm.Action{
		fsm.Advance{Flow: fsm.DefaultFlow()},
		fsm.Complete{
			Delivered: []fsm.Artifact{"worktree"},
			Evidence: map[fsm.Artifact]fsm.Evidence{"worktree": {
				Scope:   fsm.ScopeFull,
				Verdict: fsm.VerdictPassed,
				Command: "git worktree add",
			}},
		},
	} {
		if err := h.env.Store.AppendAction("LUNA-1", action); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}

	out := h.mustRun(t, "task", "show", "LUNA-1")

	if !strings.Contains(out, "worktree") {
		t.Errorf("want the produced artifact listed, got %q", out)
	}
	// Evidence is what separates knowing a stage closed from knowing on what
	// grounds it closed.
	if !strings.Contains(out, "git worktree add") {
		t.Errorf("want the evidence shown, got %q", out)
	}
}

func TestTaskShowOnAnUnknownTask(t *testing.T) {
	h := newHarness(t)

	err := h.run(t, "task", "show", "nowhere")

	if err == nil || !strings.Contains(err.Error(), "no task") {
		t.Errorf("want a clear not-found error, got %v", err)
	}
}

// ── gates ────────────────────────────────────────────────────────────────────

// TestGatesListsWhatIsWaiting covers INV-5 at the surface.
//
// A suspended task released its slot, so nothing is running to remind anyone it
// exists. This listing is the only thing standing between that and a task waiting
// forever.
func TestGatesListsWhatIsWaiting(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "waiting")
	stage := seedAtFirstGate(t, h, "waiting")

	out := h.mustRun(t, "gates")

	if !strings.Contains(out, "waiting") {
		t.Errorf("want the suspended task listed, got %q", out)
	}
	if !strings.Contains(out, string(stage)) {
		t.Errorf("want the stage it stopped at, got %q", out)
	}
}

func TestGatesWithNothingWaiting(t *testing.T) {
	h := newHarness(t)

	out := h.mustRun(t, "gates")

	if !strings.Contains(out, "nothing waiting") {
		t.Errorf("an empty listing should say so plainly, got %q", out)
	}
}

// TestANightlyTaskDoesNotAppearInTheListing covers the profile end to end.
func TestANightlyTaskDoesNotAppearInTheListing(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "unattended", "--profile", "nightly")
	if err := h.env.Store.AppendAction("unattended", fsm.Advance{Flow: fsm.DefaultFlow()}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	out := h.mustRun(t, "gates")

	if strings.Contains(out, "unattended") {
		t.Errorf("a nightly task waits for nothing, got %q", out)
	}
}

// ── gate show / approve / reject ─────────────────────────────────────────────

func TestGateShowSaysWhatIsBeingAskedFor(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")
	seedAtFirstGate(t, h, "LUNA-1")

	out := h.mustRun(t, "gate", "show", "LUNA-1")

	if !strings.Contains(out, "review the plan and its contract") {
		t.Errorf("want the reason it stopped, got %q", out)
	}
}

// TestGateShowNamesWhatItIsJudgedOn is the fix for a gate answered blind.
//
// The gate this once caught was a `confirm` on `scenarios`, which carries no
// artifact — it asks about work that has not run — so the whole prompt was three
// lines and "approve the plan". On the first real run in somebody else's
// repository it was approved without the person knowing what was being asked,
// because nothing on screen said. The criteria are what closed that, and every
// gate owes them whether or not it also carries an artifact — which is why this
// holds the shipped first gate, now a `review-artifact` on `plan`, to them too.
// TestGateShowNamesCriteriaOnAConfirmGate keeps the no-artifact case covered.
func TestGateShowNamesWhatItIsJudgedOn(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")
	seedAtFirstGate(t, h, "LUNA-1")

	out := h.mustRun(t, "gate", "show", "LUNA-1")

	if !strings.Contains(out, "judged on") {
		t.Errorf("the gate does not say what it is judged on:\n%s", out)
	}
	// Every criterion the shipped `plan` gate declares; showing some and hiding
	// the rest would be worse than showing none.
	for _, want := range []string{
		"what is required and what is forbidden",
		"Every acceptance criterion",
		"No obligation contradicts another",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the criterion %q is missing:\n%s", want, out)
		}
	}
}

// TestGateShowNamesTheChecksTheTaskDeclared. The mechanical half is the other
// thing a person is deciding against, and it is per task rather than per flow.
func TestGateShowNamesTheChecksTheTaskDeclared(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")
	h.mustRun(t, "gate", "checks", "LUNA-1", "--on", "review-artifact", "--run", "make ci")
	seedAtFirstGate(t, h, "LUNA-1")

	out := h.mustRun(t, "gate", "show", "LUNA-1")

	if !strings.Contains(out, "make ci") {
		t.Errorf("the declared check is not shown:\n%s", out)
	}
}

// TestGateShowTellsUndeclaredFromDeclaredEmpty. The two mean different things —
// one is silence, the other is a person saying this gate has no mechanical
// answer — and a listing that collapsed them would hide a decision somebody made.
func TestGateShowTellsUndeclaredFromDeclaredEmpty(t *testing.T) {
	silent := newHarness(t)
	silent.mustRun(t, "task", "new", "LUNA-1")
	seedAtFirstGate(t, silent, "LUNA-1")
	quiet := silent.mustRun(t, "gate", "show", "LUNA-1")

	decided := newHarness(t)
	decided.mustRun(t, "task", "new", "LUNA-1")
	decided.mustRun(t, "gate", "checks", "LUNA-1", "--on", "review-artifact")
	seedAtFirstGate(t, decided, "LUNA-1")
	stated := decided.mustRun(t, "gate", "show", "LUNA-1")

	// Silence points at the command that ends it.
	if !strings.Contains(quiet, "luna gate checks") {
		t.Errorf("an undeclared gate does not say how to declare one:\n%s", quiet)
	}
	// A deliberate "nothing runs here" says so, and must not offer the command as
	// though nobody had decided.
	if !strings.Contains(stated, "declared no mechanical answer") {
		t.Errorf("a deliberate empty declaration is not reported as one:\n%s", stated)
	}
	if strings.Contains(stated, "luna gate checks") {
		t.Errorf("a gate somebody already decided about was offered the command:\n%s", stated)
	}
}

// TestGateApproveResumesTheTask. The flow's first gate opens on the way *out* of
// `plan`, so approving it resumes at `stage_done`: the stage it gated has already
// closed, and `running` would ask the node to run it a second time.
// TestApprovingAConfirmGateResumesTheStageItGates covers the other side.
func TestGateApproveResumesTheTask(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")
	seedAtFirstGate(t, h, "LUNA-1")

	out := h.mustRun(t, "gate", "approve", "LUNA-1")

	if !strings.Contains(out, "approved") {
		t.Errorf("want a confirmation, got %q", out)
	}

	state, err := h.env.Store.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if state.Status != fsm.StatusStageDone {
		t.Errorf("want the task carrying on from the closed stage, got %q", state.Status)
	}
}

// TestApprovingAConfirmGateResumesTheStageItGates holds the other side of the
// resume rule, which no stage in the shipped flow exercises any more.
//
// A `confirm` gate asks before its stage runs, so approving one must leave the
// task `running` — there is still a stage to run. The shipped flow's only gate
// opens on the way out and resumes at `stage_done`; reading that as the rule for
// every gate would silently skip the stage a confirm gate was guarding, so the
// property is held here against a local flow instead of being dropped with the
// stage that used to carry it.
func TestApprovingAConfirmGateResumesTheStageItGates(t *testing.T) {
	state := fsm.NewTaskState("LUNA-1", fsm.KindFeature)
	state.Status = fsm.StatusAwaitingGate
	state.Stage = "plan"
	state.Gate = &fsm.PendingGate{
		Kind:   fsm.GateConfirm,
		Stage:  "plan",
		Reason: "approve the plan",
	}

	approved, err := fsm.Reduce(state, fsm.GateApprove{})
	if err != nil {
		t.Fatalf("approving: %v", err)
	}

	if approved.Status != fsm.StatusRunning {
		t.Errorf("want the gated stage still to run, got %q", approved.Status)
	}
	if approved.Gate != nil {
		t.Errorf("an answered gate must not stay open, got %+v", approved.Gate)
	}
}

func TestGateCommandsRefuseATaskThatIsNotWaiting(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	for _, sub := range []string{"show", "approve", "reject", "adjust"} {
		err := h.run(t, "gate", sub, "LUNA-1")
		if err == nil {
			t.Errorf("gate %s on a task with no gate must fail", sub)
			continue
		}
		if !strings.Contains(err.Error(), "not waiting") {
			t.Errorf("gate %s: want a clear message, got %v", sub, err)
		}
	}
}

// ── gate adjust ──────────────────────────────────────────────────────────────

// TestGateAdjustRefusesAGateWithNoArtifact. `adjust` edits what the gate is
// holding, so a gate holding nothing has to say so rather than open an editor on
// an empty buffer and record the result as a human's decision.
//
// No stage in the shipped flow opens a `confirm` gate any more — its only gate is
// the `review-artifact` on `plan`, which is the adjustable case — so the state is
// built here rather than walked to. What is being tested is the refusal, not
// which stage happens to ask.
func TestGateAdjustRefusesAGateWithNoArtifact(t *testing.T) {
	h := newHarness(t)

	state := fsm.NewTaskState("LUNA-1", fsm.KindFeature)
	state.Status = fsm.StatusAwaitingGate
	state.Stage = "plan"
	state.Gate = &fsm.PendingGate{
		Kind:   fsm.GateConfirm,
		Stage:  "plan",
		Reason: "approve the plan",
	}

	err := gateAdjust(h.env, "LUNA-1", state, nil)

	if err == nil || !strings.Contains(err.Error(), "no artifact") {
		t.Errorf("adjusting a gate with no artifact must say so, got %v", err)
	}
	if h.edits != 0 {
		t.Errorf("a gate with nothing to edit must not open the editor, got %d", h.edits)
	}
}

// TestAdjustLeavingTheEditorUnchangedAppliesNothing covers the cancel path.
//
// Exiting an editor without changing anything is how a person says "never mind".
// Reading it as approval would put words in their mouth.
func TestAdjustLeavingTheEditorUnchangedAppliesNothing(t *testing.T) {
	h := newHarness(t)
	h.edited = "" // the fake returns the content unchanged

	state := fsm.NewTaskState("LUNA-1", fsm.KindFeature)
	state.Status = fsm.StatusAwaitingGate
	state.Stage = "spec"
	state.Gate = &fsm.PendingGate{
		Kind:     fsm.GateReviewArtifact,
		Stage:    "spec",
		Artifact: "contract",
		Payload:  "the generated contract",
	}

	if err := gateAdjust(h.env, "LUNA-1", state, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(h.out.String(), "unchanged") {
		t.Errorf("want it to say nothing was applied, got %q", h.out.String())
	}
	if h.edits != 1 {
		t.Errorf("the editor should have been opened once, got %d", h.edits)
	}
}

// TestAdjustWithoutAnEditorSaysSo covers the missing-$EDITOR path.
func TestAdjustWithoutAnEditorSaysSo(t *testing.T) {
	h := newHarness(t)
	h.env.Edit = nil

	state := fsm.NewTaskState("LUNA-1", fsm.KindFeature)
	state.Status = fsm.StatusAwaitingGate
	state.Gate = &fsm.PendingGate{Kind: fsm.GateReviewArtifact, Artifact: "contract"}

	err := gateAdjust(h.env, "LUNA-1", state, nil)

	if err == nil || !strings.Contains(err.Error(), "editor") {
		t.Errorf("want a message naming what to configure, got %v", err)
	}
}

// ── dispatch and usage ───────────────────────────────────────────────────────

// TestUsageErrors is the surface of the whole CLI: what happens when the id is
// missing, the verb is wrong, or an argument arrived where none belongs.
//
// The list is worth being exhaustive about because the failure it catches is
// silent. A command that reads no id and defaults to a zero one does not stop —
// `luna next` with nothing after it would replay a task called "" and hand out a
// confident order for it, and `luna trust` with a stray argument would edit the
// user's credentials file while ignoring whatever they thought they were
// scoping it to.
func TestUsageErrors(t *testing.T) {
	h := newHarness(t)

	cases := [][]string{
		{},
		{"fly"},
		{"task"},
		{"task", "orbit"},
		{"task", "new"},
		{"task", "show"},
		{"task", "abandon"},
		{"task", "abandon", "LUNA-1"}, // an id with no reason is not a reason
		{"task", "statement"},
		{"task", "forget"},
		{"task", "forget", "LUNA-1", "LUNA-2"}, // one at a time, deliberately
		{"gates", "extra"},
		{"gate"},
		{"gate", "show"},
		{"gate", "orbit", "LUNA-1"},
		{"next"},
		{"done"},
		{"flow", "check", "LUNA-1"},
		{"trust", "somewhere"},
	}

	for _, args := range cases {
		if err := h.run(t, args...); !errors.Is(err, ErrUsage) {
			t.Errorf("%v: want ErrUsage, got %v", args, err)
		}
	}
}

func TestHelpPrintsTheCommands(t *testing.T) {
	h := newHarness(t)

	for _, flag := range []string{"help", "-h", "--help"} {
		out := h.mustRun(t, flag)
		if !strings.Contains(out, "luna gates") {
			t.Errorf("%s should list the commands, got %q", flag, out)
		}
	}
}

func TestFlagsParseInBothForms(t *testing.T) {
	h := newHarness(t)

	h.mustRun(t, "task", "new", "LUNA-1", "--kind=bug")
	h.mustRun(t, "task", "new", "LUNA-2", "--kind", "chore")

	first, err := h.env.Store.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	second, err := h.env.Store.Replay("LUNA-2", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}

	if first.Context.Kind != fsm.KindBug || second.Context.Kind != fsm.KindChore {
		t.Errorf("both flag forms must work: %q and %q", first.Context.Kind, second.Context.Kind)
	}
}

func TestAFlagWithoutAValueIsRejected(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "task", "new", "LUNA-1", "--kind"); !errors.Is(err, ErrUsage) {
		t.Errorf("want ErrUsage for a dangling flag, got %v", err)
	}
}

// planGateStore stages a task suspended at the flow's review-artifact gate.
//
// Reaching `plan` through the real flow takes three stages; what these tests are
// about is the gate, so the log is written directly to the point of interest.
func planGateStore(t *testing.T, h *harness, id string) {
	t.Helper()

	// Each stage closes on existence evidence: a stage now blocks unless every
	// artifact it owed carries a passing verdict, and what these tests
	// are about is the gate, not what was checked along the way.
	delivered := func(artifacts ...fsm.Artifact) fsm.Complete {
		evidence := map[fsm.Artifact]fsm.Evidence{}
		for _, a := range artifacts {
			evidence[a] = fsm.Exists(0)
		}
		return fsm.Complete{Delivered: artifacts, Evidence: evidence}
	}

	// Same, with content on one of the artifacts, so the gate that opens has
	// something to carry — which is the whole point of a review gate.
	deliveredWithPayload := func(carrying fsm.Artifact, payload string, artifacts ...fsm.Artifact) fsm.Action {
		evidence := map[fsm.Artifact]fsm.Evidence{}
		for _, a := range artifacts {
			evidence[a] = fsm.Exists(0)
		}
		evidence[carrying] = fsm.Evidence{Scope: fsm.ScopeExistence, Verdict: fsm.VerdictPassed, Detail: payload}
		return fsm.Complete{Delivered: artifacts, Evidence: evidence}
	}

	actions := []fsm.Action{
		fsm.TaskCreated{Kind: fsm.KindFeature, Flow: fsm.Fingerprint(fsm.DefaultFlow())},
		fsm.Advance{Flow: fsm.DefaultFlow()}, // setup
		delivered("worktree"),
		fsm.Advance{Flow: fsm.DefaultFlow()}, // intake
		delivered("briefing", "kind"),
		fsm.Advance{Flow: fsm.DefaultFlow()}, // plan
		// The review gate opens when `plan` closes, because that is the first
		// moment the contract exists to be reviewed.
		deliveredWithPayload("contract", "the contract the stage wrote",
			"scenarios", "approach", "contract"),
	}

	for i, action := range actions {
		if err := h.env.Store.AppendAction(id, action); err != nil {
			t.Fatalf("seeding step %d: %v", i, err)
		}
	}

	state, err := h.env.Store.ReplayOwnFlow(id)
	if err != nil {
		t.Fatalf("replaying the seed: %v", err)
	}
	if state.Stage != "plan" || state.Gate == nil {
		t.Fatalf("the seed should stop at the plan gate, got stage=%q gate=%+v", state.Stage, state.Gate)
	}
}

// TestGateShowDisplaysTheArtifactUnderReview covers the review-artifact branch.
func TestGateShowDisplaysTheArtifactUnderReview(t *testing.T) {
	h := newHarness(t)
	planGateStore(t, h, "LUNA-1")

	out := h.mustRun(t, "gate", "show", "LUNA-1")

	if !strings.Contains(out, "contract") {
		t.Errorf("want the artifact named, got %q", out)
	}
	if !strings.Contains(out, "review-artifact") {
		t.Errorf("want the gate kind, got %q", out)
	}
}

// TestGateAdjustAppliesAnEditedContract covers the path an adjustable gate artifact exists for.
//
// The human edits, and the edited version is what carries on — recorded, so the
// handoff describes what the next stage actually received.
func TestGateAdjustAppliesAnEditedContract(t *testing.T) {
	h := newHarness(t)
	planGateStore(t, h, "LUNA-1")
	h.edited = "the contract a human fixed"

	out := h.mustRun(t, "gate", "adjust", "LUNA-1")

	if !strings.Contains(out, "adjusted") {
		t.Errorf("want a confirmation, got %q", out)
	}

	state, err := h.env.Store.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	// A person's edit is recorded as human-scoped evidence, and the edited text is
	// what it carries.
	if state.Evidence["contract"].Detail != "the contract a human fixed" {
		t.Errorf("the edited version must be what was recorded, got %q", state.Evidence["contract"].Detail)
	}
	if state.Evidence["contract"].Scope != fsm.ScopeHuman {
		t.Errorf("want the adjustment scoped to the human who made it, got %q", state.Evidence["contract"].Scope)
	}
	// The stage that produced the contract has already closed — its gate opened on
	// the way out — so answering resumes at `stage_done`. `running`
	// would ask the node to run that stage a second time.
	if state.Status != fsm.StatusStageDone {
		t.Errorf("want the task carrying on from the closed stage, got %q", state.Status)
	}
}

// TestGateRejectSendsTheStageBack covers the third answer.
func TestGateRejectSendsTheStageBack(t *testing.T) {
	h := newHarness(t)
	planGateStore(t, h, "LUNA-1")

	out := h.mustRun(t, "gate", "reject", "LUNA-1", "the", "approach", "does", "not", "hold")

	if !strings.Contains(out, "rejected") {
		t.Errorf("want a confirmation, got %q", out)
	}

	state, err := h.env.Store.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if state.Context.HasArtifact("contract") {
		t.Error("a rejected artifact must not enter the context")
	}
}

// TestGateCommandsOnAnUnknownTask covers the replay failure path.
func TestGateCommandsOnAnUnknownTask(t *testing.T) {
	h := newHarness(t)

	err := h.run(t, "gate", "approve", "nowhere")

	if err == nil {
		t.Error("answering a gate on a task that does not exist must fail")
	}
}

// TestAnswerRefusesWhatTheReducerWouldReject covers the guard in answer.
//
// The response is checked against the reducer before it is written. Writing first
// and discovering the problem at replay would leave a log that cannot be rebuilt
// — and a task that cannot be rebuilt is worse than one that refused a command.
func TestAnswerRefusesWhatTheReducerWouldReject(t *testing.T) {
	h := newHarness(t)
	planGateStore(t, h, "LUNA-1")

	// Answering twice: the second has no gate to answer.
	h.mustRun(t, "gate", "approve", "LUNA-1")
	err := h.run(t, "gate", "approve", "LUNA-1")

	if err == nil {
		t.Fatal("answering a gate twice must fail")
	}

	events, err2 := h.env.Store.Events("LUNA-1")
	if err2 != nil {
		t.Fatalf("reading: %v", err2)
	}
	state, err2 := h.env.Store.Replay("LUNA-1", fsm.DefaultFlow())
	if err2 != nil {
		t.Fatalf("the log must still replay after a refused command: %v", err2)
	}
	// Where it was is where the first approval left it: the gate opened on the way
	// out of `spec`, so answering it resumes at `stage_done`. The
	// second command was refused and moved nothing.
	if state.Status != fsm.StatusStageDone {
		t.Errorf("the refused answer left the task where it was, got %q", state.Status)
	}
	_ = events
}

// TestCommandsSurfaceAStoreFailure covers the I/O error paths.
//
// Every command reads or writes the store, and every one has to report a store
// that will not answer. Closing it is the cheapest way to make them all fail at
// once, and it stands in for the real cases: a deleted file, a full disk, a
// database locked by another process.
func TestCommandsSurfaceAStoreFailure(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")
	if err := h.env.Store.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	// Every command that touches the log, including the ones that only read.
	// The list is the point: a command missing from it is a command that can
	// answer confidently from a store that told it nothing — `luna next` handing
	// out an order for a task it could not read, `luna done` reporting a stage
	// closed that was never recorded.
	cases := [][]string{
		{"task", "new", "LUNA-2"},
		{"task", "show", "LUNA-1"},
		{"task", "abandon", "LUNA-1", "we changed our minds"},
		{"task", "statement", "LUNA-1", "--about", "something else"},
		{"task", "forget", "LUNA-1"},
		{"gates"},
		{"gate", "approve", "LUNA-1"},
		{"gate", "checks", "LUNA-1", "--on", "confirm", "--run", "make ci"},
		{"next", "LUNA-1"},
		{"done", "LUNA-1", "--delivered", "code"},
		{"autonomy", "LUNA-1", "6"},
		{"stuck"},
		{"flow", "check"},
		{"lead", "LUNA-1"},
	}

	for _, args := range cases {
		if err := h.run(t, args...); err == nil {
			t.Errorf("%v: a broken store must be reported", args)
		}
	}
}

// TestTaskShowOnABlockedTask covers the blocked-reason branch.
//
// A task that stopped has to say why when someone asks. The reason is the whole
// value of the block — INV-5 is about not failing silently, and a status
// with no explanation is a quieter kind of silence.
func TestTaskShowOnABlockedTask(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--profile", "nightly")

	// Advancing into a stage whose inputs are missing blocks the task.
	flow := []fsm.Stage{
		{ID: "first", Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"worktree"}},
		{ID: "second", Requires: []fsm.Artifact{"never_produced"}, Produces: []fsm.Artifact{"x"}},
	}
	for _, action := range []fsm.Action{
		fsm.Advance{Flow: flow},
		// The first stage closes cleanly; the block has to come from the second
		// stage's missing input, not from an unverified delivery.
		fsm.Complete{
			Delivered: []fsm.Artifact{"worktree"},
			Evidence:  map[fsm.Artifact]fsm.Evidence{"worktree": fsm.Exists(0)},
			Flow:      flow,
		},
		fsm.Advance{Flow: flow},
	} {
		if err := h.env.Store.AppendAction("LUNA-1", action); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}

	// The default flow replays this log into a blocked state too, by the same
	// mechanism: a stage whose requires are not there.
	out := h.mustRun(t, "task", "show", "LUNA-1")

	if !strings.Contains(out, "LUNA-1") {
		t.Errorf("want the task named, got %q", out)
	}
}

// ── adjusting a gate without an editor ───────────────────────────────────────

// TestGateAdjustAppends covers the mode an agent would use.
//
// The point of the non-interactive modes: whoever answers a gate is not always a
// person at a terminal. An interface that only worked interactively would push an
// agent into approving something it meant to change.
func TestGateAdjustAppends(t *testing.T) {
	h := newHarness(t)
	planGateStore(t, h, "LUNA-1")
	h.env.Edit = nil // no editor anywhere; the flag must carry it

	out := h.mustRun(t, "gate", "adjust", "LUNA-1", "--append", "and one more constraint")

	if !strings.Contains(out, "adjusted") {
		t.Errorf("want a confirmation, got %q", out)
	}

	state, err := h.env.Store.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if !strings.Contains(state.Evidence["contract"].Detail, "and one more constraint") {
		t.Errorf("want the addition recorded, got %q", state.Evidence["contract"].Detail)
	}
}

// TestGateAdjustReplaces covers the wholesale swap.
func TestGateAdjustReplaces(t *testing.T) {
	h := newHarness(t)
	planGateStore(t, h, "LUNA-1")
	h.env.Edit = nil

	h.mustRun(t, "gate", "adjust", "LUNA-1", "--replace", "a completely different contract")

	state, err := h.env.Store.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if state.Evidence["contract"].Detail != "a completely different contract" {
		t.Errorf("want the replacement, got %q", state.Evidence["contract"].Detail)
	}
}

// TestGateAdjustFromStdin covers the pipeline mode.
func TestGateAdjustFromStdin(t *testing.T) {
	h := newHarness(t)
	planGateStore(t, h, "LUNA-1")
	h.env.Edit = nil
	h.env.In = strings.NewReader("a contract from a pipeline")

	h.mustRun(t, "gate", "adjust", "LUNA-1", "--stdin")

	state, err := h.env.Store.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if state.Evidence["contract"].Detail != "a contract from a pipeline" {
		t.Errorf("want what stdin carried, got %q", state.Evidence["contract"].Detail)
	}
}

// TestGateAdjustWithStdinAndNothingConnected covers the missing-reader path.
//
// Reporting it beats treating an absent reader as an empty replacement, which
// would silently wipe the artifact.
func TestGateAdjustWithStdinAndNothingConnected(t *testing.T) {
	h := newHarness(t)
	planGateStore(t, h, "LUNA-1")
	h.env.Edit = nil
	h.env.In = nil

	err := h.run(t, "gate", "adjust", "LUNA-1", "--stdin")

	if err == nil || !strings.Contains(err.Error(), "nothing is connected") {
		t.Errorf("want a clear message, got %v", err)
	}
}

// TestGateAdjustRefusesTwoModesAtOnce covers the ambiguity.
func TestGateAdjustRefusesTwoModesAtOnce(t *testing.T) {
	h := newHarness(t)
	planGateStore(t, h, "LUNA-1")

	err := h.run(t, "gate", "adjust", "LUNA-1", "--append", "x", "--replace", "y")

	if !errors.Is(err, ErrUsage) {
		t.Errorf("want ErrUsage when two modes are given, got %v", err)
	}
}

// TestGateAdjustRejectsAnUnknownFlag covers the typo path.
func TestGateAdjustRejectsAnUnknownFlag(t *testing.T) {
	h := newHarness(t)
	planGateStore(t, h, "LUNA-1")

	if err := h.run(t, "gate", "adjust", "LUNA-1", "--apend", "x"); !errors.Is(err, ErrUsage) {
		t.Errorf("want ErrUsage for a mistyped flag, got %v", err)
	}
}

// TestAnAppendThatChangesNothingAppliesNothing covers the no-op guard.
//
// An empty append is how someone says "never mind" without an editor. Reading it
// as approval would put words in their mouth — the same reasoning as leaving an
// editor untouched.
func TestAnAppendThatChangesNothingAppliesNothing(t *testing.T) {
	h := newHarness(t)
	planGateStore(t, h, "LUNA-1")
	h.env.Edit = nil

	state, err := h.env.Store.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	current := state.Gate.Payload

	out := h.mustRun(t, "gate", "adjust", "LUNA-1", "--replace", current)

	if !strings.Contains(out, "unchanged") {
		t.Errorf("want it to say nothing was applied, got %q", out)
	}
}

// TestAdjustWithNoEditorAndNoFlagSaysWhatToDo covers the dead end.
func TestAdjustWithNoEditorAndNoFlagSaysWhatToDo(t *testing.T) {
	h := newHarness(t)
	planGateStore(t, h, "LUNA-1")
	h.env.Edit = nil

	err := h.run(t, "gate", "adjust", "LUNA-1")

	if err == nil {
		t.Fatal("with no editor and no flag there is nothing to do")
	}
	if !strings.Contains(err.Error(), "--append") {
		t.Errorf("the message should name the alternatives, got %v", err)
	}
}

// TestAnUndefinedProfileIsFlaggedInTheListing covers the warning.
//
// A task can name a profile the configuration no longer defines — deleted or
// renamed after it started. It replays exactly as it ran, because every gate
// decision it took is in its log, but its remaining gates fall back to
// the cautious policy. Without a word about it someone would watch their nightly
// run start stopping at every gate and have nothing to go on.
func TestAnUndefinedProfileIsFlaggedInTheListing(t *testing.T) {
	h := newHarness(t)

	// A task started under a profile the config no longer carries.
	if err := h.env.Store.AppendAction("LUNA-1", fsm.TaskCreated{
		Kind:    fsm.KindFeature,
		Profile: fsm.Profile("paranoid"),
		Flow:    fsm.Fingerprint(fsm.DefaultFlow()),
	}); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	seedAtFirstGate(t, h, "LUNA-1")

	out := h.mustRun(t, "gates")
	if !strings.Contains(out, "no longer defined") {
		t.Errorf("want the listing to flag it, got %q", out)
	}

	// `task show` no longer carries the profile at all: it decides nothing since
	// retired with the profiles and is kept only so old logs replay. Showing it there implied it
	// still governed the run. The listing is where an undefined one still
	// surfaces, because that is where a person is choosing what to answer.
	if out := h.mustRun(t, "task", "show", "LUNA-1"); strings.Contains(out, "paranoid") {
		t.Errorf("task show reports a profile that decides nothing, got %q", out)
	}
}

// TestAKnownProfileIsNotFlagged covers the quiet path.
func TestAKnownProfileIsNotFlagged(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--profile", "turbo")

	out := h.mustRun(t, "task", "show", "LUNA-1")

	if strings.Contains(out, "unknown") {
		t.Errorf("a shipped profile must not be flagged, got %q", out)
	}
}

// TestTaskShowReportsWhereTheLoopStands. The three counters existed and were
// invisible: a task circling without converging counted rounds in silence until a
// ceiling fired, and the first anyone heard of it was the gate that opened
// (PRD node-0002).
func TestTaskShowReportsWhereTheLoopStands(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")
	h.loop(t, "LUNA-1", fsm.LoopCounters{Rounds: 3, NoProgress: 2, LastProgress: "abc1234"})

	out := h.mustRun(t, "task", "show", "LUNA-1")
	loop := h.replay(t, "LUNA-1").Loop

	// Asserted against what the reducer counted rather than against numbers
	// written here: the point is that the listing reports the counters, and a test
	// naming its own would drift from them silently.
	for _, want := range []string{
		fmt.Sprintf("round %d", loop.Rounds),
		fmt.Sprintf("%d with no change", loop.NoProgress),
		"abc1234",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing does not carry %q:\n%s", want, out)
		}
	}
	if loop.NoProgress == 0 {
		t.Fatal("the harness produced no repeated round, so the listing was never tested")
	}
}

// TestTaskShowSaysNothingAboutALoopThatIsNotRunning. Most tasks never loop, and a
// line of zeroes on every one of them is noise that teaches a reader to skip the
// place the real number will appear.
func TestTaskShowSaysNothingAboutALoopThatIsNotRunning(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")

	if out := h.mustRun(t, "task", "show", "LUNA-1"); strings.Contains(out, "loop") {
		t.Errorf("a task that never looped reports a loop:\n%s", out)
	}
}

// TestTheLoopReachesTheStructuredView. `--json` is a contract, and a
// program watching for a task about to hit a ceiling reads it there.
func TestTheLoopReachesTheStructuredView(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")
	h.loop(t, "LUNA-1", fsm.LoopCounters{Rounds: 2, NoProgress: 2, LastProgress: "abc1234"})

	var report TaskReport
	if err := json.Unmarshal([]byte(h.mustRun(t, "task", "show", "LUNA-1", "--json")), &report); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	if report.Loop == nil {
		t.Fatal("a looping task carries no loop in the structured view")
	}
	loop := h.replay(t, "LUNA-1").Loop
	if report.Loop.Rounds != loop.Rounds || report.Loop.NoProgress != loop.NoProgress {
		t.Errorf("the counters did not survive:\n got %+v\nwant %+v", report.Loop, loop)
	}
	if report.Loop.Compared != "abc1234" {
		t.Errorf("what was compared did not survive: %q", report.Loop.Compared)
	}
}

// TestPrintingATaskWithNoArtifactsDoesNotAnnounceADelivery covers the shape a
// state has before anything produced anything.
//
// Every task created through `luna task new` carries `task_id` from the start,
// so this is reached by a state rebuilt from a log that opened differently — and
// what it must not do is print the "produced" heading over an empty list. That
// reads as a stage that closed and delivered nothing, which is the exact shape
// of a real failure the reducer refuses; a healthy task must not look like one.
func TestPrintingATaskWithNoArtifactsDoesNotAnnounceADelivery(t *testing.T) {
	h := newHarness(t)

	printTask(h.env, fsm.TaskState{ID: "LUNA-1", Status: fsm.StatusReady}, 0)

	out := h.out.String()
	if strings.Contains(out, "produced") {
		t.Errorf("a task that has produced nothing announced a delivery:\n%s", out)
	}
	if !strings.Contains(out, "LUNA-1") {
		t.Errorf("the task was not reported at all:\n%s", out)
	}
}

// TestStdinThatCannotBeReadIsReportedRatherThanTakenAsEmpty covers the pipe
// breaking mid-read.
//
// `--stdin` is how a script adjusts an artifact a gate is holding. A read that
// fails and is treated as "they supplied nothing" would replace the artifact
// with an empty document and record it as the human's edit — the one shape of
// data loss this log cannot undo, since there is no UPDATE to put it back.
func TestStdinThatCannotBeReadIsReportedRatherThanTakenAsEmpty(t *testing.T) {
	h := newHarness(t)
	h.env.In = brokenPipe{}

	_, err := readAll(h.env)

	if err == nil {
		t.Fatal("a broken pipe was read as an empty replacement")
	}
	if !strings.Contains(err.Error(), "stdin") {
		t.Errorf("the error should name where it was reading from, got %v", err)
	}
}

// brokenPipe stands in for the writer at the other end going away mid-read: the
// reader that a closed pipe or a killed process leaves behind.
type brokenPipe struct{}

func (brokenPipe) Read([]byte) (int, error) {
	return 0, errors.New("read |0: file already closed")
}

// TestAnAdjustedArtifactIsWhatTheNextStageReads closes the gap between where an
// adjustment is recorded and where the next stage looks.
//
// `luna gate adjust` promises that "the adjusted version is what enters the
// context", and the reducer keeps that promise the only way a pure function can:
// it records the edited payload as evidence. But an artifact handed over through
// the socket lives in the store, and the store is what `luna artifact get`
// serves to the stage that consumes it. So the correction stayed in the log
// while the next stage read the original.
//
// Measured on TALLY-5: a contract was adjusted to turn a "should" into a "MUST"
// — the exact weakness the gate had refused it for — and the store went on
// serving the version with the "should".
func TestAnAdjustedArtifactIsWhatTheNextStageReads(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")
	seedAtFirstGate(t, h, "LUNA-1")

	// The artifact as the stage handed it over.
	if err := h.env.Store.PutBlob(store.Blob{
		TaskID: "LUNA-1", Stage: "plan", Artifact: "contract",
		Body: []byte("the build stage should be clean\n"),
	}); err != nil {
		t.Fatalf("seeding the artifact: %v", err)
	}

	h.edited = "the build stage MUST be clean\n"
	h.mustRun(t, "gate", "adjust", "LUNA-1")

	blob, err := h.env.Store.LatestBlob("LUNA-1", "", "contract")
	if err != nil {
		t.Fatalf("reading the artifact back: %v", err)
	}
	if !strings.Contains(string(blob.Body), "MUST") {
		t.Errorf("the next stage still reads the version the gate refused:\n%s", blob.Body)
	}
}

// TestAbandoningAFinishedTaskIsRefusedBeforeItIsWritten covers a log that could
// corrupt itself through a command that read nothing first.
//
// The reducer refuses an Abandon on a task that has ended. It used to do that on
// the way back *in*, which is not a refusal at all: the event is already in an
// append-only log, and every command that reads the task replays it — so `task
// show`, `status` and even `forget` all fail, and the task can neither be read
// nor got rid of.
//
// It was reasoned that this could not happen by accident. It happened on the
// first occasion anyone tried: TALLY-6 was abandoned to record why its run had
// been superseded, three minutes after it finished, and nineteen events of a
// completed run — including what it cost — went behind the twentieth.
func TestAbandoningAFinishedTaskIsRefusedBeforeItIsWritten(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--simulated", "--kind", "feature", "--profile", "nightly")
	h.mustRun(t, "autonomy", "LUNA-1", "10")

	// A task that has actually ended, rather than one seeded into the status:
	// what is under test is the refusal, and a hand-built terminal state would
	// not prove the command reaches it.
	_ = Run(h.env, []string{"lead", "LUNA-1", "--dry-run"})
	state := mustState(t, h, "LUNA-1")
	if !state.IsTerminal() {
		t.Skipf("this flow did not reach a terminal state, got %q", state.Status)
	}

	before := state.Seq
	err := Run(h.env, []string{"task", "abandon", "LUNA-1", "changed our minds"})
	if err == nil {
		t.Fatal("abandoning a finished task was accepted, and the log is now unreadable")
	}
	if !strings.Contains(err.Error(), "already ended") {
		t.Errorf("the refusal must say why, got %q", err)
	}

	// And nothing was written: a refusal that appends first is the bug.
	if after := mustState(t, h, "LUNA-1"); after.Seq != before {
		t.Errorf("the refused abandon still wrote an event: seq %d became %d", before, after.Seq)
	}
}

// TestATaskWhoseFlowChangedCanStillBeAbandoned is the exception the refusal must
// not swallow.
//
// A task born under a flow this build does not have cannot be replayed at all,
// and those are exactly the ones that most need ending — it is the reason this
// command skipped the read in the first place. Refusing every replay failure
// would close the only door they have.
func TestATaskWhoseFlowChangedCanStillBeAbandoned(t *testing.T) {
	h := newHarness(t)

	stale := []fsm.Stage{{ID: "gone", Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"x"}}}
	if err := h.env.Store.AppendAction("LUNA-1", fsm.TaskCreated{
		Kind: fsm.KindChore, Flow: fsm.Fingerprint(stale),
	}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	if err := h.run(t, "task", "abandon", "LUNA-1", "the flow it ran under is gone"); err != nil {
		t.Fatalf("a task that no longer replays could not be abandoned: %v", err)
	}
}
