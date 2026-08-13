package cli

import (
	"bytes"
	"errors"
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
		// A stand-in for the editor: returns whatever the test staged, so no test
		// needs $EDITOR or a terminal.
		Edit: func(current string) (string, error) {
			h.edits++
			if h.edited == "" {
				return current, nil // left unchanged
			}
			return h.edited, nil
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

	for _, want := range []string{"LUNA-1", "ready", "chore", "interactive"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in the output, got %q", want, out)
		}
	}
}

func TestTaskShowListsWhatWasProducedWithItsEvidence(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--profile", "nightly")

	// Drive the task through discovery so it has produced something.
	for _, action := range []fsm.Action{
		fsm.Advance{Flow: fsm.DefaultFlow()},
		fsm.Complete{
			Delivered: []fsm.Artifact{"repos"},
			Evidence: map[fsm.Artifact]fsm.Evidence{"repos": {
				Scope:   fsm.ScopeFull,
				Verdict: fsm.VerdictPassed,
				Command: "found 2 repositories",
			}},
		},
	} {
		if err := h.env.Store.AppendAction("LUNA-1", action); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}

	out := h.mustRun(t, "task", "show", "LUNA-1")

	if !strings.Contains(out, "repos") {
		t.Errorf("want the produced artifact listed, got %q", out)
	}
	// Evidence is what separates knowing a stage closed from knowing on what
	// grounds it closed (ADR-0024).
	if !strings.Contains(out, "found 2 repositories") {
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

// TestGatesListsWhatIsWaiting covers INV-core-12 at the surface.
//
// A suspended task released its slot, so nothing is running to remind anyone it
// exists. This listing is the only thing standing between that and a task waiting
// forever.
func TestGatesListsWhatIsWaiting(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "waiting")
	if err := h.env.Store.AppendAction("waiting", fsm.Advance{Flow: fsm.DefaultFlow()}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	out := h.mustRun(t, "gates")

	if !strings.Contains(out, "waiting") {
		t.Errorf("want the suspended task listed, got %q", out)
	}
	if !strings.Contains(out, "discovery") {
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
	if err := h.env.Store.AppendAction("LUNA-1", fsm.Advance{Flow: fsm.DefaultFlow()}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	out := h.mustRun(t, "gate", "show", "LUNA-1")

	if !strings.Contains(out, "confirm the repositories") {
		t.Errorf("want the reason it stopped, got %q", out)
	}
}

func TestGateApproveResumesTheTask(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")
	if err := h.env.Store.AppendAction("LUNA-1", fsm.Advance{Flow: fsm.DefaultFlow()}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	out := h.mustRun(t, "gate", "approve", "LUNA-1")

	if !strings.Contains(out, "approved") {
		t.Errorf("want a confirmation, got %q", out)
	}

	state, err := h.env.Store.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if state.Status != fsm.StatusRunning {
		t.Errorf("want the task running again, got %q", state.Status)
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

func TestGateAdjustAppliesTheEditedVersion(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1")
	if err := h.env.Store.AppendAction("LUNA-1", fsm.Advance{Flow: fsm.DefaultFlow()}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	// discovery carries a plain confirm, which has nothing to adjust.
	err := h.run(t, "gate", "adjust", "LUNA-1")

	if err == nil || !strings.Contains(err.Error(), "no artifact") {
		t.Errorf("adjusting a gate with no artifact must say so, got %v", err)
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

func TestUsageErrors(t *testing.T) {
	h := newHarness(t)

	cases := [][]string{
		{},
		{"fly"},
		{"task"},
		{"task", "orbit"},
		{"task", "new"},
		{"task", "show"},
		{"gates", "extra"},
		{"gate"},
		{"gate", "show"},
		{"gate", "orbit", "LUNA-1"},
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

// specGateStore stages a task suspended at a review-artifact gate.
//
// Reaching spec through the real flow takes six stages; what these tests are
// about is the gate, so the log is written directly to the point of interest.
func specGateStore(t *testing.T, h *harness, id string) {
	t.Helper()

	// Each stage closes on existence evidence: a stage now blocks unless every
	// artifact it owed carries a passing verdict (ADR-0028), and what these tests
	// are about is the gate, not what was checked along the way.
	delivered := func(artifacts ...fsm.Artifact) fsm.Complete {
		evidence := map[fsm.Artifact]fsm.Evidence{}
		for _, a := range artifacts {
			evidence[a] = fsm.Exists(0)
		}
		return fsm.Complete{Delivered: artifacts, Evidence: evidence}
	}

	actions := []fsm.Action{
		fsm.TaskCreated{Kind: fsm.KindFeature},
		fsm.Advance{Flow: fsm.DefaultFlow()}, // discovery, gated
		fsm.GateApprove{},                    //
		delivered("repos"),
		fsm.Advance{Flow: fsm.DefaultFlow()}, // setup
		delivered("worktree"),
		fsm.Advance{Flow: fsm.DefaultFlow()}, // intake
		delivered("briefing", "kind"),
		fsm.Advance{Flow: fsm.DefaultFlow()}, // scenarios, gated
		fsm.GateApprove{},                    //
		delivered("scenarios", "approach"),
		fsm.Advance{Flow: fsm.DefaultFlow()}, // spec, gated with the contract
	}

	for i, action := range actions {
		if err := h.env.Store.AppendAction(id, action); err != nil {
			t.Fatalf("seeding step %d: %v", i, err)
		}
	}

	state, err := h.env.Store.Replay(id, fsm.DefaultFlow())
	if err != nil {
		t.Fatalf("replaying the seed: %v", err)
	}
	if state.Stage != "spec" || state.Gate == nil {
		t.Fatalf("the seed should stop at the spec gate, got stage=%q gate=%+v", state.Stage, state.Gate)
	}
}

// TestGateShowDisplaysTheArtifactUnderReview covers the review-artifact branch.
func TestGateShowDisplaysTheArtifactUnderReview(t *testing.T) {
	h := newHarness(t)
	specGateStore(t, h, "LUNA-1")

	out := h.mustRun(t, "gate", "show", "LUNA-1")

	if !strings.Contains(out, "contract") {
		t.Errorf("want the artifact named, got %q", out)
	}
	if !strings.Contains(out, "review-artifact") {
		t.Errorf("want the gate kind, got %q", out)
	}
}

// TestGateAdjustAppliesAnEditedContract covers the path ADR-0022 exists for.
//
// The human edits, and the edited version is what carries on — recorded, so the
// handoff describes what the next stage actually received.
func TestGateAdjustAppliesAnEditedContract(t *testing.T) {
	h := newHarness(t)
	specGateStore(t, h, "LUNA-1")
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
	// what it carries (ADR-0022).
	if state.Evidence["contract"].Detail != "the contract a human fixed" {
		t.Errorf("the edited version must be what was recorded, got %q", state.Evidence["contract"].Detail)
	}
	if state.Evidence["contract"].Scope != fsm.ScopeHuman {
		t.Errorf("want the adjustment scoped to the human who made it, got %q", state.Evidence["contract"].Scope)
	}
	if state.Status != fsm.StatusRunning {
		t.Errorf("want the task running again, got %q", state.Status)
	}
}

// TestGateRejectSendsTheStageBack covers the third answer.
func TestGateRejectSendsTheStageBack(t *testing.T) {
	h := newHarness(t)
	specGateStore(t, h, "LUNA-1")

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
	specGateStore(t, h, "LUNA-1")

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
	if state.Status != fsm.StatusRunning {
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

	cases := [][]string{
		{"task", "new", "LUNA-2"},
		{"task", "show", "LUNA-1"},
		{"gates"},
		{"gate", "approve", "LUNA-1"},
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
// value of the block — INV-core-8 is about not failing silently, and a status
// with no explanation is a quieter kind of silence.
func TestTaskShowOnABlockedTask(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--profile", "nightly")

	// Advancing into a stage whose inputs are missing blocks the task.
	flow := []fsm.Stage{
		{ID: "first", Requires: []fsm.Artifact{fsm.TaskID}, Produces: []fsm.Artifact{"repos"}},
		{ID: "second", Requires: []fsm.Artifact{"never_produced"}, Produces: []fsm.Artifact{"x"}},
	}
	for _, action := range []fsm.Action{
		fsm.Advance{Flow: flow},
		// The first stage closes cleanly; the block has to come from the second
		// stage's missing input, not from an unverified delivery.
		fsm.Complete{
			Delivered: []fsm.Artifact{"repos"},
			Evidence:  map[fsm.Artifact]fsm.Evidence{"repos": fsm.Exists(0)},
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
	specGateStore(t, h, "LUNA-1")
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
	specGateStore(t, h, "LUNA-1")
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
	specGateStore(t, h, "LUNA-1")
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
	specGateStore(t, h, "LUNA-1")
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
	specGateStore(t, h, "LUNA-1")

	err := h.run(t, "gate", "adjust", "LUNA-1", "--append", "x", "--replace", "y")

	if !errors.Is(err, ErrUsage) {
		t.Errorf("want ErrUsage when two modes are given, got %v", err)
	}
}

// TestGateAdjustRejectsAnUnknownFlag covers the typo path.
func TestGateAdjustRejectsAnUnknownFlag(t *testing.T) {
	h := newHarness(t)
	specGateStore(t, h, "LUNA-1")

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
	specGateStore(t, h, "LUNA-1")
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
	specGateStore(t, h, "LUNA-1")
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
// decision it took is in its log (ADR-0026), but its remaining gates fall back to
// the cautious policy. Without a word about it someone would watch their nightly
// run start stopping at every gate and have nothing to go on.
func TestAnUndefinedProfileIsFlaggedInTheListing(t *testing.T) {
	h := newHarness(t)

	// A task started under a profile the config no longer carries.
	if err := h.env.Store.AppendAction("LUNA-1", fsm.TaskCreated{
		Kind:    fsm.KindFeature,
		Profile: fsm.Profile("paranoid"),
	}); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if err := h.env.Store.AppendAction("LUNA-1", fsm.Advance{Flow: fsm.DefaultFlow()}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	out := h.mustRun(t, "gates")
	if !strings.Contains(out, "no longer defined") {
		t.Errorf("want the listing to flag it, got %q", out)
	}

	out = h.mustRun(t, "task", "show", "LUNA-1")
	if !strings.Contains(out, "paranoid") {
		t.Errorf("want the profile as recorded, got %q", out)
	}
	if !strings.Contains(out, "no longer defined") {
		t.Errorf("want task show to flag it too, got %q", out)
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
