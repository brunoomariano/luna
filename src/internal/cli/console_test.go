package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestTheConsoleNamesTheSessionOfEveryStageThatRan.
//
// The session id has been in the log all along — it is what lets a later stage
// resume the same conversation — and nothing ever showed it. A person watching a
// run had the cost of every stage and no way to see what any of them did.
func TestTheConsoleNamesTheSessionOfEveryStageThatRan(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "chore", "--flow", "chore", "--simulated")

	flow, err := fsm.FlowNamed("chore")
	if err != nil {
		t.Fatalf("reading the flow: %v", err)
	}
	// Past `setup`, which starts no agent, and onto `build`, which does.
	for _, action := range []fsm.Action{
		fsm.Advance{Flow: flow},
		fsm.Complete{
			Delivered: []fsm.Artifact{"worktree"},
			Evidence:  map[fsm.Artifact]fsm.Evidence{"worktree": fsm.Exists(0)},
		},
		fsm.Advance{Flow: flow},
	} {
		if err := h.env.Store.AppendAction("LUNA-1", action); err != nil {
			t.Fatalf("reaching build: %v", err)
		}
	}
	if err := h.env.Store.AppendAction("LUNA-1", fsm.Complete{
		Delivered: []fsm.Artifact{"code", "tests_green"},
		Evidence: map[fsm.Artifact]fsm.Evidence{
			"code": fsm.Exists(0), "tests_green": fsm.Exists(0),
		},
		Spent: fsm.Spend{
			Session: "d5888d00-5349-439d-8000-c09a8cb46af6", Turns: 9,
			InputTokens: 18, OutputTokens: 8785, CostUSD: 0.59,
		},
	}); err != nil {
		t.Fatalf("closing the stage: %v", err)
	}

	out := h.mustRun(t, "console", "LUNA-1")

	if !strings.Contains(out, "d5888d00-5349-439d-8000-c09a8cb46af6") &&
		!strings.Contains(out, "d5888d00") {
		t.Errorf("the console does not name the session that ran:\n%s", out)
	}
	if !strings.Contains(out, "tail -f") {
		t.Errorf("the console does not say how to follow it:\n%s", out)
	}
}

// TestATaskThatHasStartedNoAgentSaysSo, rather than printing an empty listing
// that reads like the transcripts are missing.
func TestATaskThatHasStartedNoAgentSaysSo(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "chore", "--simulated")

	out := h.mustRun(t, "console", "LUNA-1")

	if !strings.Contains(out, "no session to watch") {
		t.Errorf("a task with no agent yet does not say so:\n%s", out)
	}
}

// TestTheConsoleDoesNotReadTheTranscript. Printing it would mean Luna parsing one
// harness's format — a coupling it does not have and a second thing to keep in
// step with, when tail and jq already read the file better than Luna would.
func TestTheConsoleDoesNotReadTheTranscript(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "chore", "--flow", "chore", "--simulated")

	flow, err := fsm.FlowNamed("chore")
	if err != nil {
		t.Fatalf("reading the flow: %v", err)
	}
	for _, action := range []fsm.Action{
		fsm.Advance{Flow: flow},
		fsm.Complete{
			Delivered: []fsm.Artifact{"worktree"},
			Evidence:  map[fsm.Artifact]fsm.Evidence{"worktree": fsm.Exists(0)},
		},
		fsm.Advance{Flow: flow},
		fsm.Complete{
			Delivered: []fsm.Artifact{"code", "tests_green"},
			Evidence: map[fsm.Artifact]fsm.Evidence{
				"code": fsm.Exists(0),
				"tests_green": {
					Scope: fsm.ScopeTargeted, Verdict: fsm.VerdictPassed, Command: "make test",
				},
			},
			Spent: fsm.Spend{Session: "s-1", InputTokens: 10, OutputTokens: 20, CostUSD: 0.01},
		},
	} {
		if err := h.env.Store.AppendAction("LUNA-1", action); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}

	// No transcript exists for this session anywhere, and the command still
	// answers: it names where one would be rather than opening it.
	out := h.mustRun(t, "console", "LUNA-1")

	if !strings.Contains(out, "not on disk") {
		t.Errorf("a transcript that is not there should be marked, got:\n%s", out)
	}
	if !strings.Contains(out, ".jsonl") {
		t.Errorf("the console does not name where the transcript would be:\n%s", out)
	}
}

// TestAliveTranscriptIsOfferedReadyToPaste.
//
// The point is watching a stage as it happens, and a command with `<path>` in it
// is one more step between a person and the thing they came to see.
func TestALiveTranscriptIsOfferedReadyToPaste(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "chore", "--flow", "chore", "--simulated")
	const session = "21af4935-7249-4323-938b-1897059685be"
	seedAgentStage(t, h, session)

	// The transcript the harness would have written by now.
	consoles := consolesOf(mustReplay(t, h, "LUNA-1"), mustFlow(t, "chore"))
	if len(consoles) != 1 || consoles[0].Path == "" {
		t.Fatalf("the console was not derived: %+v", consoles)
	}
	if err := os.MkdirAll(filepath.Dir(consoles[0].Path), 0o750); err != nil {
		t.Fatalf("making the transcript directory: %v", err)
	}
	if err := os.WriteFile(consoles[0].Path, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("writing the transcript: %v", err)
	}

	out := h.mustRun(t, "console", "LUNA-1")

	if strings.Contains(out, "not on disk") {
		t.Errorf("a transcript that is there was marked missing:\n%s", out)
	}
	if strings.Contains(out, "tail -f <path>") {
		t.Errorf("the follow command was not filled in with the real path:\n%s", out)
	}
	if !strings.Contains(out, "tail -f "+consoles[0].Path) {
		t.Errorf("the follow command does not name the live transcript:\n%s", out)
	}
}

// TestTheConsoleAsJSONCarriesThePathAndWhetherItIsThere, so a pane can be opened
// by something other than a person reading the listing.
func TestTheConsoleAsJSONCarriesThePathAndWhetherItIsThere(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "chore", "--flow", "chore", "--simulated")
	seedAgentStage(t, h, "s-1")

	out := h.mustRun(t, "console", "LUNA-1", "--json")

	var report []ConsoleReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("the report is not readable json: %v\n%s", err, out)
	}
	if len(report) != 1 {
		t.Fatalf("want the one stage that started an agent, got %+v", report)
	}
	if report[0].Session != "s-1" || report[0].Stage != "build" || report[0].Agent != "claude" {
		t.Errorf("the stage is not described: %+v", report[0])
	}
	if report[0].Live {
		t.Error("a transcript that was never written is reported as live")
	}
}

// TestTheCodexConsoleCarriesItsOwnCommands keeps observability from being a
// Claude-shaped path with a Codex label. The transcript formats and resume
// commands differ, and both are part of the console contract.
func TestTheCodexConsoleCarriesItsOwnCommands(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "chore", "--flow", "chore", "--simulated")
	const session = "01a059dd-3645-7d01-ba07-f5587c11480e"
	seedAgentStage(t, h, session)

	flow := mustFlow(t, "chore")
	for i := range flow {
		if flow[i].ID == "build" {
			flow[i].Agent = "codex"
		}
	}
	reports := consolesOf(mustReplay(t, h, "LUNA-1"), flow)
	if len(reports) != 1 {
		t.Fatalf("want one Codex console, got %+v", reports)
	}
	if !strings.Contains(reports[0].Resume, "codex resume "+session) {
		t.Errorf("the Codex resume command is missing: %+v", reports[0])
	}
	if !strings.Contains(reports[0].FollowFilter, "response_item") {
		t.Errorf("the Claude filter was reused for Codex: %+v", reports[0])
	}

	printConsoles(h.env, "LUNA-1", mustReplay(t, h, "LUNA-1"), reports)
	out := h.out.String()
	if !strings.Contains(out, "codex resume "+session) || !strings.Contains(out, "response_item") {
		t.Errorf("the rendered console is not usable for Codex:\n%s", out)
	}
}

func TestTheConsoleUsesTheHarnessRecordedByAnOverride(t *testing.T) {
	state := fsm.TaskState{Spent: map[fsm.StageID]fsm.Spend{
		"build": {
			Agent: "codex", Session: "01a059dd-3645-7d01-ba07-f5587c11480e",
			InputTokens: 1,
		},
	}}
	flow := []fsm.Stage{{ID: "build", Agent: "claude"}}

	reports := consolesOf(state, flow)
	if len(reports) != 1 || reports[0].Agent != "codex" ||
		!strings.Contains(reports[0].Resume, "codex resume") {
		t.Errorf("the console relabelled an overridden run: %+v", reports)
	}
}

// TestAnEndedStageOffersItsClaudeSessionToOpen.
//
// A Claude session is global rather than tied to the worktree it started in.
// Measured on AVG-1: resuming its finished forge session from the Luna checkout
// reached the right conversation after the stage worktree had been removed.
func TestAnEndedStageOffersItsClaudeSessionToOpen(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "chore", "--flow", "chore", "--simulated")
	seedAgentStage(t, h, "s-ended")

	out := h.mustRun(t, "console", "LUNA-1")

	for _, want := range []string{"claude -r s-ended", "stage has ended", "safe to open"} {
		if !strings.Contains(out, want) {
			t.Errorf("an ended stage does not say %q:\n%s", want, out)
		}
	}
}

// TestARunningStageWarnsThatResumingWritesIntoItsConversation.
//
// Resuming a live headless call did not fork or damage it, but the injected
// prompt landed in that call's original transcript. That turn bypasses Luna's
// append-only log, so the console must name the boundary before offering it.
func TestARunningStageWarnsThatResumingWritesIntoItsConversation(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "chore", "--flow", "chore", "--simulated")
	flow := mustFlow(t, "chore")
	for _, action := range []fsm.Action{
		fsm.Advance{Flow: flow},
		fsm.Complete{
			Delivered: []fsm.Artifact{"worktree"},
			Evidence:  map[fsm.Artifact]fsm.Evidence{"worktree": fsm.Exists(0)},
		},
		fsm.Advance{Flow: flow},
		fsm.Complete{
			Delivered: []fsm.Artifact{"code"},
			Evidence:  map[fsm.Artifact]fsm.Evidence{"code": fsm.Exists(0)},
			Spent:     fsm.Spend{Session: "s-running", CostUSD: 0.1},
		},
	} {
		if err := h.env.Store.AppendAction("LUNA-1", action); err != nil {
			t.Fatalf("seeding a running agent stage: %v", err)
		}
	}

	out := h.mustRun(t, "console", "LUNA-1")

	for _, want := range []string{
		"claude -r s-running",
		"not a copy",
		"Anything you type joins it",
		"Luna does not record",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("a running stage does not say %q:\n%s", want, out)
		}
	}
}

// TestOnlyTheCurrentRunningStageGetsTheConversationWarning. A stage can remain
// named while it is closed, gated, blocked or terminal; none of those states may
// be presented as a process that is currently running.
func TestOnlyTheCurrentRunningStageGetsTheConversationWarning(t *testing.T) {
	for _, status := range []fsm.Status{
		fsm.StatusStageDone,
		fsm.StatusAwaitingGate,
		fsm.StatusBlocked,
		fsm.StatusDone,
		fsm.StatusAbandoned,
	} {
		t.Run(string(status), func(t *testing.T) {
			h := newHarness(t)
			state := fsm.TaskState{ID: "LUNA-1", Stage: "build", Status: status}
			printConsoles(h.env, state.ID, state, []ConsoleReport{{
				Stage: "build", Agent: "claude", Session: "s-stopped",
			}})

			out := h.out.String()
			if strings.Contains(out, "not a copy") || strings.Contains(out, "← running") {
				t.Errorf("a stage in %s was presented as running:\n%s", status, out)
			}
		})
	}
}

// seedAgentStage walks a chore task to `build`, the one stage of it that starts
// an agent, and closes it with a session.
func seedAgentStage(t *testing.T, h *harness, session string) {
	t.Helper()

	flow := mustFlow(t, "chore")
	for _, action := range []fsm.Action{
		fsm.Advance{Flow: flow},
		fsm.Complete{
			Delivered: []fsm.Artifact{"worktree"},
			Evidence:  map[fsm.Artifact]fsm.Evidence{"worktree": fsm.Exists(0)},
		},
		fsm.Advance{Flow: flow},
		fsm.Complete{
			Delivered: []fsm.Artifact{"code", "tests_green"},
			Evidence: map[fsm.Artifact]fsm.Evidence{
				"code": fsm.Exists(0),
				"tests_green": {
					Scope: fsm.ScopeTargeted, Verdict: fsm.VerdictPassed, Command: "make test",
				},
			},
			Spent: fsm.Spend{Session: session, InputTokens: 18, OutputTokens: 8785, CostUSD: 0.6},
		},
	} {
		if err := h.env.Store.AppendAction("LUNA-1", action); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}
}

func mustFlow(t *testing.T, name string) []fsm.Stage {
	t.Helper()

	flow, err := fsm.FlowNamed(name)
	if err != nil {
		t.Fatalf("reading the %s flow: %v", name, err)
	}
	return flow
}

func mustReplay(t *testing.T, h *harness, id string) fsm.TaskState {
	t.Helper()

	state, err := h.env.replay(id)
	if err != nil {
		t.Fatalf("replaying %s: %v", id, err)
	}
	return state
}

// TestTheConsoleRefusesWhatItCannotAnswer, each with the reason rather than an
// empty listing that reads like "this task started nothing".
func TestTheConsoleRefusesWhatItCannotAnswer(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "chore", "--simulated")

	if err := h.run(t, "console", "LUNA-1", "--nope"); !errors.Is(err, ErrUsage) {
		t.Errorf("an unknown flag answered %v, want a usage error", err)
	}
	if err := h.run(t, "console", "GHOST-1"); err == nil {
		t.Error("a task nothing opened answered with a listing")
	}
}

// TestAHarnessLunaCannotPlaceIsSaidToBeUnplaceable.
//
// A guessed path sends somebody to a file that is not there and lets them
// conclude the agent produced nothing — which is the failure the command exists
// to end, arriving by a different door.
func TestAHarnessLunaCannotPlaceIsSaidToBeUnplaceable(t *testing.T) {
	h := newHarness(t)

	printConsoles(h.env, "LUNA-1", fsm.TaskState{Stage: "build"}, []ConsoleReport{
		{Stage: "build", Agent: "codex", Session: "s-9"},
	})

	out := h.out.String()
	if !strings.Contains(out, "does not know where") || !strings.Contains(out, "s-9") {
		t.Errorf("an unplaceable harness is not reported with its session:\n%s", out)
	}
	if strings.Contains(out, ".jsonl") {
		t.Errorf("a path was invented for a harness Luna cannot place:\n%s", out)
	}
}
