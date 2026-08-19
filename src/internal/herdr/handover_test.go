package herdr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// stageWithHandover owes one artifact through Luna's store rather than through
// the commit.
func stageWithHandover() fsm.Stage {
	return fsm.Stage{
		ID:       "spec",
		Role:     "specifier",
		Produces: []fsm.Artifact{"contract"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{
			"contract": fsm.Existence{Handover: true},
		},
	}
}

// fakeSocket records the artifact server's lifecycle without opening one.
type fakeSocket struct {
	opened []string // "task/worktree/stage seq" per call
	closed int
	fail   error
}

func (f *fakeSocket) open(taskID, worktree, stage string, seq int) (io.Closer, string, error) {
	f.opened = append(f.opened, fmt.Sprintf("%s/%s/%s %d", taskID, worktree, stage, seq))
	if f.fail != nil {
		return nil, "", f.fail
	}
	return f, "/wt/.luna/artifact.sock", nil
}

func (f *fakeSocket) Close() error { f.closed++; return nil }

// storedHashes answers "was it handed over?" from a map, standing in for the
// store.
func storedHashes(m map[string]string) func(taskID, stage, artifact string) (string, error) {
	return func(taskID, stage, artifact string) (string, error) {
		if hash, ok := m[stage+"/"+artifact]; ok {
			return hash, nil
		}
		return "", fmt.Errorf("%s has no %s from stage %s", taskID, artifact, stage)
	}
}

// TestAHandedOverArtifactIsProvenByTheStore is the exit check for an artifact
// that is not in the commit: the store is the witness, and the evidence carries
// the hash INV-3 asks the handoff to hold.
func TestAHandedOverArtifactIsProvenByTheStore(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	socket := &fakeSocket{}
	n := &Node{
		Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving(),
		Artifacts: socket.open,
		Stored:    storedHashes(map[string]string{"spec/contract": "sha256:feedbeef"}),
	}

	result, err := n.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithHandover())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evidence := result.Evidence["contract"]
	if !evidence.Passing() {
		t.Fatalf("an artifact the store holds is delivered, got %+v", evidence)
	}
	if !strings.Contains(evidence.Detail, "sha256:feedbeef") {
		t.Errorf("the evidence must carry the hash, got %q", evidence.Detail)
	}
	if !containsArtifact(result.Delivered, "contract") {
		t.Errorf("a handed-over artifact counts as delivered, got %v", result.Delivered)
	}
}

// TestAnArtifactNobodyHandedOverFailsItsStage: the agent was told to put it and
// did not, and the stage must not close on its word.
func TestAnArtifactNobodyHandedOverFailsItsStage(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	socket := &fakeSocket{}
	n := &Node{
		Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving(),
		Artifacts: socket.open,
		Stored:    storedHashes(nil),
	}

	result, err := n.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithHandover())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	evidence := result.Evidence["contract"]
	if evidence.Passing() {
		t.Fatal("an artifact the store never saw must not pass")
	}
	if !strings.Contains(evidence.Detail, "not handed over") {
		t.Errorf("the evidence must say what is missing, got %q", evidence.Detail)
	}
	if containsArtifact(result.Delivered, "contract") {
		t.Errorf("what was not handed over was not delivered, got %v", result.Delivered)
	}
}

// TestAHandoverStageWithNoStoreFailsRatherThanTrusts covers the unwired caller:
// recording a pass with nothing to ask would be the self-report Luna refuses.
func TestAHandoverStageWithNoStoreFailsRatherThanTrusts(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	n := &Node{Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving()}

	result, err := n.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithHandover())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Evidence["contract"].Passing() {
		t.Error("with no store configured there is no witness, and no witness is not a pass")
	}
}

// TestTheSocketOpensForAHandoverStageAndClosesWithIt pins the lifecycle: one
// socket, opened before the agent and closed after, attributed to the stage.
func TestTheSocketOpensForAHandoverStageAndClosesWithIt(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	socket := &fakeSocket{}
	n := &Node{
		Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving(),
		Artifacts: socket.open,
		Stored:    storedHashes(map[string]string{"spec/contract": "x"}),
	}

	state := fsm.NewTaskState("LUNA-1", "")
	if _, err := n.Run(context.Background(), state, stageWithHandover()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(socket.opened) != 1 || !strings.Contains(socket.opened[0], "LUNA-1") ||
		!strings.HasSuffix(socket.opened[0], "spec 0") {
		t.Errorf("one socket, for this task and stage at this seq, got %v", socket.opened)
	}
	if socket.closed != 1 {
		t.Errorf("the socket closes with the stage, got %d closes", socket.closed)
	}
}

// TestAStageWithNoHandoverOpensNoSocket: an agent that owes nothing through Luna
// gets no writer.
func TestAStageWithNoHandoverOpensNoSocket(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	socket := &fakeSocket{}
	n := &Node{
		Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving(),
		Artifacts: socket.open,
	}

	if _, err := n.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithTests()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(socket.opened) != 0 {
		t.Errorf("a stage with no handover opens nothing, got %v", socket.opened)
	}
}

// TestASocketThatWillNotOpenStopsTheStage: the agent would be told to hand over
// through a socket that is not there, and every put would fail after the work
// was done — stopping first loses less.
func TestASocketThatWillNotOpenStopsTheStage(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	socket := &fakeSocket{fail: errors.New("address already in use")}
	n := &Node{
		Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving(),
		Artifacts: socket.open,
	}

	_, err := n.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithHandover())
	if err == nil || !strings.Contains(err.Error(), "artifact socket") {
		t.Errorf("a socket that will not open must stop the stage, got %v", err)
	}
}

// TestTheBriefNamesWhatIsHandedOver: an agent told to commit everything will
// commit everything, so the exception has to be said.
func TestTheBriefNamesWhatIsHandedOver(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	socket := &fakeSocket{}
	n := &Node{
		Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving(),
		Artifacts: socket.open,
		Stored:    storedHashes(map[string]string{"spec/contract": "x"}),
	}

	if _, err := n.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithHandover()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(herdr.prompted) != 1 {
		t.Fatalf("one prompt, got %d", len(herdr.prompted))
	}
	brief := herdr.prompted[0]
	for _, want := range []string{"luna artifact put", "do not commit"} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief must say %q, got:\n%s", want, brief)
		}
	}
}

func containsArtifact(list []fsm.Artifact, want fsm.Artifact) bool {
	for _, a := range list {
		if a == want {
			return true
		}
	}
	return false
}

// TestAMissingHandoverDoesNotErasesItsNeighbours pins claimOnly's other side: a
// stage owing one artifact through Luna and one through the commit loses only
// the one the store never saw.
func TestAMissingHandoverDoesNotErasesItsNeighbours(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	socket := &fakeSocket{}
	n := &Node{
		Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving(),
		Artifacts: socket.open,
		Stored:    storedHashes(nil),
	}

	stage := fsm.Stage{
		ID:       "scenarios",
		Role:     "gherkin",
		Produces: []fsm.Artifact{"notes", "scenarios"},
		Verifiers: map[fsm.Artifact]fsm.Verifier{
			"notes":     fsm.Existence{},
			"scenarios": fsm.Existence{Handover: true},
		},
	}

	result, err := n.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stage)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !containsArtifact(result.Delivered, "notes") {
		t.Errorf("the committed artifact survives its neighbour's miss, got %v", result.Delivered)
	}
	if containsArtifact(result.Delivered, "scenarios") {
		t.Errorf("the artifact the store never saw is not delivered, got %v", result.Delivered)
	}
}

// TestAnUnreadableTreeStopsAHandoverStageToo pins that the socket handover kept
// the unreachable-worktree correction: the commit read fails, the stage stops,
// whatever the store holds.
func TestAnUnreadableTreeStopsAHandoverStageToo(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	socket := &fakeSocket{}
	n := &Node{
		Runner: herdr, Roles: fixedRole("claude"), Prove: herdr.proving(),
		Artifacts: socket.open,
		Stored:    storedHashes(map[string]string{"spec/contract": "x"}),
		Delivered: func(context.Context, string) (string, string, error) {
			return "", "", errors.New("reading what the worktree delivered: permission denied")
		},
	}

	_, err := n.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stageWithHandover())
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("an unreadable tree stops the stage, got %v", err)
	}
}
