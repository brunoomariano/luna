package cli

import (
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/store"
)

// forgetHarness builds a task that handed one artifact over and then ended.
func forgetHarness(t *testing.T, terminal fsm.Action) *harness {
	t.Helper()
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "chore")

	if err := h.env.Store.PutBlob(store.Blob{
		TaskID: "LUNA-1", Stage: "spec", Artifact: "contract", Seq: 2, Body: []byte("the contract"),
	}); err != nil {
		t.Fatalf("handing over: %v", err)
	}
	if terminal != nil {
		if err := h.env.Store.AppendAction("LUNA-1", terminal); err != nil {
			t.Fatalf("ending the task: %v", err)
		}
	}
	return h
}

// TestForgetRemovesAFinishedTasksContent is the cleanup RFC-0008 promised: the
// content goes, the log and its hashes stay.
func TestForgetRemovesAFinishedTasksContent(t *testing.T) {
	h := forgetHarness(t, fsm.Abandon{Reason: "done with it"})

	out := h.mustRun(t, "task", "forget", "LUNA-1")

	for _, want := range []string{"forgot what LUNA-1 handed over", "contract", "only the content is gone"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in the report, got %q", want, out)
		}
	}
	if _, err := h.env.Store.LatestBlob("LUNA-1", "", "contract"); err == nil {
		t.Error("the content must be gone")
	}
	// The log survives whole: the task still replays to its terminal state.
	state, err := h.env.Store.Replay("LUNA-1", fsm.DefaultFlow())
	if err != nil || !state.IsTerminal() {
		t.Errorf("the log is untouched by forgetting, got %v / %v", state.Status, err)
	}
}

// TestForgetRefusesARunningTask: a task still in flight is still handing its
// documents to the stages ahead of it.
func TestForgetRefusesARunningTask(t *testing.T) {
	h := forgetHarness(t, nil)

	err := Run(h.env, []string{"task", "forget", "LUNA-1"})
	if err == nil || !strings.Contains(err.Error(), "abandon") {
		t.Errorf("a live task must be refused with the way out, got %v", err)
	}
	if _, blobErr := h.env.Store.LatestBlob("LUNA-1", "", "contract"); blobErr != nil {
		t.Error("a refused forget must not have forgotten anything")
	}
}

// TestForgetSaysWhenThereIsNothing covers the task that handed nothing over —
// every task from before RFC-0008, and every docs task.
func TestForgetSaysWhenThereIsNothing(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "chore")
	if err := h.env.Store.AppendAction("LUNA-1", fsm.Abandon{Reason: "x"}); err != nil {
		t.Fatalf("ending: %v", err)
	}

	out := h.mustRun(t, "task", "forget", "LUNA-1")
	if !strings.Contains(out, "nothing to forget") {
		t.Errorf("an empty forget says so, got %q", out)
	}
}

// TestForgetUsageErrors pins the surface.
func TestForgetUsageErrors(t *testing.T) {
	h := newHarness(t)

	for _, args := range [][]string{
		{"task", "forget"},
		{"task", "forget", "LUNA-1", "extra"},
	} {
		if err := Run(h.env, args); err == nil || !strings.Contains(err.Error(), "exactly one id") {
			t.Errorf("%v: want the usage error, got %v", args, err)
		}
	}
	if err := Run(h.env, []string{"task", "forget", "GHOST-1"}); err == nil || !strings.Contains(err.Error(), "GHOST-1") {
		t.Errorf("an unknown task is named in the refusal, got %v", err)
	}
}

// TestForgetRelaysAReplayThatRefuses: a task from another flow cannot be read,
// and the refusal reaches the person whole instead of being reworded here.
func TestForgetRelaysAReplayThatRefuses(t *testing.T) {
	h := newHarness(t)
	if err := h.env.Store.AppendAction("LUNA-1", fsm.TaskCreated{
		Kind: fsm.KindChore, Flow: "0000000000000000",
	}); err != nil {
		t.Fatalf("opening under another flow: %v", err)
	}

	err := Run(h.env, []string{"task", "forget", "LUNA-1"})
	if err == nil || !strings.Contains(err.Error(), "flow") {
		t.Errorf("a flow-changed task's refusal travels whole, got %v", err)
	}
}
