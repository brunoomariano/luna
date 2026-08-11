package herdr

import (
	"context"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// noWriting is what a review role denies: both capabilities, because a role that
// could still create a file has not been stopped from changing what it judges.
var noWriting = []fsm.Capability{fsm.CapEdit, fsm.CapWrite}

// TestEachHarnessSpeaksItsOwnVocabulary is the table ADR-0042 exists for.
//
// Four official harnesses, four mechanisms, no two alike. The role declares the
// capability; translating it is Luna's job, which is what lets a project move
// `reviewer` between agents without rewriting the role.
func TestEachHarnessSpeaksItsOwnVocabulary(t *testing.T) {
	cases := map[string][]string{
		// Space-separated and capitalised.
		"claude": {"--disallowed-tools", "Edit", "Write"},
		// Comma-separated and lower case.
		"pi": {"--exclude-tools", "edit,write"},
		// No tool names at all: the sandbox denies writing wholesale.
		"codex": {"-s", "read-only"},
		// The denial lives in the agent's own file, so nothing goes on argv.
		"opencode": nil,
	}

	for kind, want := range cases {
		harness, ok := HarnessFor(kind)
		if !ok {
			t.Errorf("%s is an official harness and must be in the table", kind)
			continue
		}

		got, err := harness.Deny(noWriting)
		if err != nil {
			t.Errorf("%s: denying writing must work: %v", kind, err)
			continue
		}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%s: want %v, got %v", kind, want, got)
		}
	}
}

// TestCodexCannotDenyOneCapabilityAlone covers the coarseness worth recording.
//
// `-s read-only` stops all writing and takes no tool names, which is exactly
// right for a reviewer and cannot express "no Edit, but Bash is fine". Refusing
// beats emitting a flag that denies more than was asked.
func TestCodexCannotDenyOneCapabilityAlone(t *testing.T) {
	codex, _ := HarnessFor("codex")

	_, err := codex.Deny([]fsm.Capability{fsm.CapEdit})

	if err == nil {
		t.Fatal("codex denies writing wholesale; a partial denial must be refused")
	}
	if !strings.Contains(err.Error(), "claude") && !strings.Contains(err.Error(), "pi") {
		t.Errorf("the error should name a harness that can do it, got %v", err)
	}
	if codex.Precise {
		t.Error("codex is coarse, and the table must say so")
	}
}

// TestAnUnlistedHarnessIsRefused covers the direction that matters.
//
// Guessing that an agent supports denial and being wrong fails open: a reviewer
// that can edit, with nothing in the log saying the denial did not take.
func TestAnUnlistedHarnessIsRefused(t *testing.T) {
	_, err := gateArgs(fsm.Role{Agent: "cursor", ToolsDeny: noWriting})

	if err == nil {
		t.Fatal("an agent Luna cannot gate must stop the stage")
	}
	if !strings.Contains(err.Error(), "cursor") {
		t.Errorf("the error should name the harness, got %v", err)
	}
	// This refusal will read as a bug the first time someone meets it, so it has
	// to say what would work instead.
	for _, want := range SupportedHarnesses() {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should list %q as supported, got %v", want, err)
		}
	}
}

// TestAnUngatedRoleNeedsNoHarnessSupport covers the ordinary case.
//
// Most roles deny nothing. Requiring a table entry for them would restrict the
// whole flow to four agents for no reason.
func TestAnUngatedRoleNeedsNoHarnessSupport(t *testing.T) {
	args, err := gateArgs(fsm.Role{Agent: "some-agent-nobody-listed"})
	if err != nil {
		t.Errorf("a role that denies nothing needs nothing: %v", err)
	}
	if args != nil {
		t.Errorf("want no arguments, got %v", args)
	}
}

// TestTheDenialReachesTheAgent is the end of the chain: a gated role starts with
// the tool absent rather than discouraged (ADR-0018).
func TestTheDenialReachesTheAgent(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{
		Runner: herdr,
		Prove:  herdr.proving(),
		Roles: func(fsm.RoleName) (fsm.Role, bool) {
			return fsm.Role{Agent: "claude", ToolsDeny: noWriting}, true
		},
	}

	stage := fsm.Stage{ID: "code-review", Role: "reviewer", Produces: []fsm.Artifact{"code"}}
	if _, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stage); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	args := strings.Join(herdr.startArgs, " ")
	if !strings.Contains(args, "--disallowed-tools") {
		t.Errorf("the denial must reach the agent, got %v", herdr.startArgs)
	}
	for _, capability := range noWriting {
		if !strings.Contains(args, string(capability)) {
			t.Errorf("want %s denied, got %v", capability, herdr.startArgs)
		}
	}
}

// TestAGatedRoleOnAnUngateableHarnessStopsTheStage is the refusal ADR-0041 chose
// over reported degradation.
//
// A review that ran ungated is a review whose independence rests on the prompt,
// and a log entry afterwards does not give the finding back its weight.
func TestAGatedRoleOnAnUngateableHarnessStopsTheStage(t *testing.T) {
	herdr := &fakeHerdr{settlesAt: StatusIdle}
	node := &Node{
		Runner: herdr,
		Prove:  herdr.proving(),
		Roles: func(fsm.RoleName) (fsm.Role, bool) {
			return fsm.Role{Agent: "gemini", ToolsDeny: noWriting}, true
		},
	}

	stage := fsm.Stage{ID: "code-review", Role: "reviewer", Produces: []fsm.Artifact{"code"}}
	_, err := node.Run(context.Background(), fsm.NewTaskState("LUNA-1", ""), stage)

	if err == nil {
		t.Fatal("an ungateable harness must stop the stage, not run ungated")
	}
	if !strings.Contains(err.Error(), "code-review") {
		t.Errorf("the error should name the stage, got %v", err)
	}
	if herdr.started != "" {
		t.Error("nothing should have started")
	}
}

// TestTheDeniedCapabilitiesAreOrdered covers reproducibility.
//
// A flag order that varies makes two identical runs look different in a log, and
// the log is the audit trail.
func TestTheDeniedCapabilitiesAreOrdered(t *testing.T) {
	claude, _ := HarnessFor("claude")

	forwards, _ := claude.Deny([]fsm.Capability{fsm.CapEdit, fsm.CapWrite})
	backwards, _ := claude.Deny([]fsm.Capability{fsm.CapWrite, fsm.CapEdit})

	if strings.Join(forwards, " ") != strings.Join(backwards, " ") {
		t.Errorf("the same denial must produce the same command line, got %v and %v", forwards, backwards)
	}
}
