package cli

import (
	"errors"
	"os"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/node"
	"github.com/brunoomariano/luna/src/internal/store"
)

// artifactHarness is a harness with a live artifact socket, which is how the
// command runs for real: Luna listening on one side, the CLI dialing from the
// other.
func artifactHarness(t *testing.T, stage string) (*harness, string) {
	t.Helper()
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "chore")

	worktree, err := os.MkdirTemp("/tmp", "luna-wt-")
	if err != nil {
		t.Fatalf("making a worktree: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(worktree) })

	server, err := node.ServeArtifacts(worktree, stage, NewTaskArtifacts(h.env.Store, "LUNA-1", 3))
	if err != nil {
		t.Fatalf("serving artifacts: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	t.Setenv(SocketEnv, server.Path())
	return h, server.Path()
}

// TestAnAgentHandsOverThroughTheCLI is the round trip the design exists for: put
// from stdin, get to stdout, with the store in between.
func TestAnAgentHandsOverThroughTheCLI(t *testing.T) {
	h, _ := artifactHarness(t, "spec")

	h.env.In = strings.NewReader("the contract, in full")
	if err := Run(h.env, []string{"artifact", "put", "contract"}); err != nil {
		t.Fatalf("putting: %v", err)
	}
	if !strings.Contains(h.out.String(), "handed over contract") {
		t.Errorf("put must confirm the handover, got %q", h.out.String())
	}

	h.out.Reset()
	if err := Run(h.env, []string{"artifact", "get", "contract"}); err != nil {
		t.Fatalf("getting: %v", err)
	}
	if h.out.String() != "the contract, in full" {
		t.Errorf("get must return the content verbatim, got %q", h.out.String())
	}
}

// TestGetCanNameTheStage covers the audit read: what did *that* stage hand over.
func TestGetCanNameTheStage(t *testing.T) {
	h, _ := artifactHarness(t, "spec")

	h.env.In = strings.NewReader("from spec")
	if err := Run(h.env, []string{"artifact", "put", "contract"}); err != nil {
		t.Fatalf("putting: %v", err)
	}

	h.out.Reset()
	if err := Run(h.env, []string{"artifact", "get", "contract", "--stage", "spec"}); err != nil {
		t.Fatalf("getting by stage: %v", err)
	}
	if h.out.String() != "from spec" {
		t.Errorf("get --stage must return that stage's version, got %q", h.out.String())
	}

	// A stage that wrote nothing is a miss, not a fallback to somebody else's.
	if err := Run(h.env, []string{"artifact", "get", "contract", "--stage", "qa"}); err == nil {
		t.Error("a stage that handed nothing over must not answer with another stage's work")
	}
}

// TestAnEmptyHandoverIsRefused: an artifact with no content is not a handover,
// and recording it would let a truncated redirect close a stage.
func TestAnEmptyHandoverIsRefused(t *testing.T) {
	h, _ := artifactHarness(t, "spec")

	h.env.In = strings.NewReader("")
	err := Run(h.env, []string{"artifact", "put", "contract"})
	if err == nil {
		t.Fatal("empty content must be refused")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("the refusal must say why, got %q", err)
	}
}

// TestNothingOnStdinIsReported covers the redirect that never happened.
func TestNothingOnStdinIsReported(t *testing.T) {
	h, _ := artifactHarness(t, "spec")

	h.env.In = nil
	err := Run(h.env, []string{"artifact", "put", "contract"})
	if err == nil || !strings.Contains(err.Error(), "stdin") {
		t.Errorf("a put with nothing connected must name stdin, got %v", err)
	}
}

// TestOutsideAStageTheCommandSaysSo covers the agentless terminal: no socket in
// the environment means this is not running inside a stage.
func TestOutsideAStageTheCommandSaysSo(t *testing.T) {
	h := newHarness(t)
	t.Setenv(SocketEnv, "")

	err := Run(h.env, []string{"artifact", "get", "contract"})
	if err == nil || !strings.Contains(err.Error(), SocketEnv) {
		t.Errorf("outside a stage the command must name the missing variable, got %v", err)
	}
}

// TestArtifactUsageErrors pins the argument parsing: each mistake is named.
func TestArtifactUsageErrors(t *testing.T) {
	h, _ := artifactHarness(t, "spec")

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"artifact"}, "usage"},
		{[]string{"artifact", "burn", "contract"}, "burn"},
		{[]string{"artifact", "get"}, "artifact name"},
		{[]string{"artifact", "get", "contract", "extra"}, "extra"},
		{[]string{"artifact", "get", "--wat", "contract"}, "--wat"},
	} {
		err := Run(h.env, tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: want %q in the error, got %v", tc.args, tc.want, err)
		}
	}
}

// TestTaskShowListsWhatWasHandedOver is INV-5's gap closing: an artifact a
// person is meant to read is discoverable by command.
func TestTaskShowListsWhatWasHandedOver(t *testing.T) {
	h, _ := artifactHarness(t, "qa")

	h.env.In = strings.NewReader("all scenarios pass; one flaky test quarantined")
	if err := Run(h.env, []string{"artifact", "put", "qa_report"}); err != nil {
		t.Fatalf("putting: %v", err)
	}

	out := h.mustRun(t, "task", "show", "LUNA-1")
	for _, want := range []string{"handed over", "qa_report", "qa"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in the listing, got %q", want, out)
		}
	}
}

// TestTaskShowIsQuietWithNothingHandedOver pins that every task predating the handover
// reads exactly as before.
func TestTaskShowIsQuietWithNothingHandedOver(t *testing.T) {
	h := newHarness(t)
	h.mustRun(t, "task", "new", "LUNA-1", "--kind", "chore")

	out := h.mustRun(t, "task", "show", "LUNA-1")
	if strings.Contains(out, "handed over") {
		t.Errorf("a task with no handovers must not grow a section, got %q", out)
	}
}

// TestAPutPastTheCeilingReachesTheAgentThroughTheCLI is the ceiling seen from
// the agent's side: the refusal travels the socket and comes out as an error the
// agent can read, with both numbers in it.
func TestAPutPastTheCeilingReachesTheAgentThroughTheCLI(t *testing.T) {
	h, _ := artifactHarness(t, "qa")

	h.env.In = strings.NewReader(strings.Repeat("x", 1<<20+1))
	err := Run(h.env, []string{"artifact", "put", "qa_report"})
	if err == nil {
		t.Fatal("content past the ceiling must be refused through the CLI too")
	}
	if !strings.Contains(err.Error(), "1048576") {
		t.Errorf("the refusal must carry the ceiling, got %q", err)
	}
}

// TestASocketNobodyListensOnIsReported covers the stage that died between
// setting the variable and the agent's put.
func TestASocketNobodyListensOnIsReported(t *testing.T) {
	h := newHarness(t)
	t.Setenv(SocketEnv, "/tmp/luna-nobody-listens.sock")

	h.env.In = strings.NewReader("content")
	err := Run(h.env, []string{"artifact", "put", "contract"})
	if err == nil || !strings.Contains(err.Error(), "reaching Luna") {
		t.Errorf("a dead socket must be reported as unreachable, got %v", err)
	}

	if err := Run(h.env, []string{"artifact", "get", "contract"}); err == nil {
		t.Error("get through a dead socket must fail too")
	}
}

// TestShortHashesAreShownWhole pins the abbreviation's boundary: a digest is
// trimmed for the eye, anything already short is left alone.
func TestShortHashesAreShownWhole(t *testing.T) {
	if got := shortHash("abc"); got != "abc" {
		t.Errorf("a short value is not trimmed, got %q", got)
	}
	if got := shortHash("0123456789abcdef0123"); got != "0123456789ab" {
		t.Errorf("a digest shows its first 12, got %q", got)
	}
}

// TestAFailingStdinIsNotAHandover covers the read that dies partway: what was
// read so far must not be recorded as the artifact.
func TestAFailingStdinIsNotAHandover(t *testing.T) {
	h, _ := artifactHarness(t, "spec")

	h.env.In = iotest.ErrReader(errors.New("the pipe broke"))
	err := Run(h.env, []string{"artifact", "put", "contract"})
	if err == nil || !strings.Contains(err.Error(), "the pipe broke") {
		t.Errorf("a broken read is reported, got %v", err)
	}
}

// TestPutWithoutANameIsUsage rounds out the argument errors on the put side.
func TestPutWithoutANameIsUsage(t *testing.T) {
	h, _ := artifactHarness(t, "spec")

	h.env.In = strings.NewReader("content")
	err := Run(h.env, []string{"artifact", "put"})
	if err == nil || !strings.Contains(err.Error(), "artifact name") {
		t.Errorf("put with no name names the gap, got %v", err)
	}
}

// TestGateShowPrintsAHandedOverArtifact is the sf-51 complaint closing: a person
// asked to review the contract gets the contract, not a hash naming it.
//
// It drives the flow to the spec gate the same way a run does — a handover
// contract cannot be Complete'd without a blob in the store, so one is put
// there first, which is exactly what the agent's `luna artifact put` does.
func TestGateShowPrintsAHandedOverArtifact(t *testing.T) {
	h := newHarness(t)

	flow := []fsm.Stage{{
		ID:   "spec",
		Role: "specifier",
		Gate: &fsm.GateSpec{
			Kind: fsm.GateReviewArtifact, Artifact: "contract", Reason: "review the contract",
		},
		Produces:  []fsm.Artifact{"contract"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{"contract": fsm.Existence{Handover: true}},
	}}

	// Opened against this test's own flow, so the replay reads the log under the
	// fingerprint it was written with.
	if err := h.env.Store.AppendAction("LUNA-1", fsm.TaskCreated{
		Kind: fsm.KindFeature, Flow: fsm.Fingerprint(flow),
	}); err != nil {
		t.Fatalf("opening the task: %v", err)
	}
	if err := h.env.Store.AppendAction("LUNA-1", fsm.Advance{Flow: flow}); err != nil {
		t.Fatalf("entering the stage: %v", err)
	}
	if err := h.env.Store.PutBlob(store.Blob{
		TaskID: "LUNA-1", Stage: "spec", Artifact: "contract", Seq: 1,
		Body: []byte("the whole contract, verbatim"),
	}); err != nil {
		t.Fatalf("handing the contract over: %v", err)
	}
	if err := h.env.Store.AppendAction("LUNA-1", fsm.Complete{
		Flow: flow, Delivered: []fsm.Artifact{"contract"},
		Evidence: map[fsm.Artifact]fsm.Evidence{"contract": {
			Scope: fsm.ScopeExistence, Verdict: fsm.VerdictPassed,
			Detail: "handed over to Luna, abc123", RecordedAt: 1,
		}},
	}); err != nil {
		t.Fatalf("closing the stage: %v", err)
	}

	// The replay must use the same flow the events were written under.
	state, err := h.env.Store.Replay("LUNA-1", flow)
	if err != nil {
		t.Fatalf("replaying: %v", err)
	}
	if err := gateShow(h.env, state, flow); err != nil {
		t.Fatalf("showing the gate: %v", err)
	}

	if !strings.Contains(h.out.String(), "the whole contract, verbatim") {
		t.Errorf("the person reviews the artifact, not a hash naming it, got %q", h.out.String())
	}
}
