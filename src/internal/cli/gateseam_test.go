package cli

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/registry"
)

// taskDeclaring builds a registry task carrying gate checks, through the JSON
// decoder rather than by hand — the point being that Luna can read what beads
// actually returns.
func taskDeclaring(t *testing.T, metadata string) registry.Task {
	t.Helper()

	var task registry.Task
	body := `{"id":"LUNA-1","title":"a task","status":"open","metadata":` + metadata + `}`
	if err := json.Unmarshal([]byte(body), &task); err != nil {
		t.Fatalf("building a task with metadata %s: %v", metadata, err)
	}
	return task
}

// TestTheSeamRunsWhatTheTaskDeclared is the mechanical half arriving at the gate.
func TestTheSeamRunsWhatTheTaskDeclared(t *testing.T) {
	reg := &fakeRegistry{task: taskDeclaring(t,
		`{"luna_gates":{"confirm":{"checks":["true"]}}}`)}

	check := checkGateWith(reg, t.TempDir())
	got := check(context.Background(), "LUNA-1", fsm.GateConfirm)

	if !got.Passed || got.Rejected || got.Unrunnable {
		t.Errorf("a passing check reported %+v", got)
	}
}

// TestTheSeamRejectsOnANonZeroExit covers the answer that needs no model.
func TestTheSeamRejectsOnANonZeroExit(t *testing.T) {
	reg := &fakeRegistry{task: taskDeclaring(t,
		`{"luna_gates":{"confirm":{"checks":["exit 1"]}}}`)}

	got := checkGateWith(reg, t.TempDir())(context.Background(), "LUNA-1", fsm.GateConfirm)

	if !got.Rejected || got.Passed {
		t.Errorf("a failing check reported %+v", got)
	}
}

// TestTheSeamIsSilentAboutAGateNobodyDeclared is what keeps this additive.
//
// It is every task that exists today: no `luna_gates` at all. The gate has to
// reach the judgement half with nothing decided, not be approved and not be
// treated as broken.
func TestTheSeamIsSilentAboutAGateNobodyDeclared(t *testing.T) {
	for name, metadata := range map[string]string{
		"no metadata":         `{}`,
		"another tool's only": `{"theirs":"a string"}`,
		"a different gate":    `{"luna_gates":{"review-artifact":{"checks":["true"]}}}`,
		"declared but empty":  `{"luna_gates":{"confirm":{"checks":[]}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			reg := &fakeRegistry{task: taskDeclaring(t, metadata)}

			got := checkGateWith(reg, t.TempDir())(context.Background(), "LUNA-1", fsm.GateConfirm)

			if got.Passed || got.Rejected || got.Unrunnable {
				t.Errorf("an undeclared gate reported %+v", got)
			}
			// And the decision it leads to is a person, not an approval.
			if answer := fsm.ResolveGate(nil, got, fsm.KnobAll); answer != fsm.AnswerPerson {
				t.Errorf("an undeclared gate resolved to %v", answer)
			}
		})
	}
}

// TestARegistryThatCannotBeReadIsNotAnApproval is the distinction that keeps a
// broken registry from opening gates.
//
// "The registry says nothing was declared" and "the registry could not be asked"
// are different facts, and one of them approves a gate. Collapsing them would
// mean a beads that is down answers every gate with checks as though nobody had
// declared any.
func TestARegistryThatCannotBeReadIsNotAnApproval(t *testing.T) {
	reg := &fakeRegistry{taskErr: errors.New("beads is not answering")}

	got := checkGateWith(reg, t.TempDir())(context.Background(), "LUNA-1", fsm.GateConfirm)

	if !got.Unrunnable {
		t.Errorf("an unreadable registry reported %+v, want unrunnable", got)
	}
	if answer := fsm.ResolveGate(nil, got, fsm.KnobAll); answer != fsm.AnswerPerson {
		t.Errorf("an unreadable registry resolved to %v, want a person", answer)
	}
}

// TestAMalformedDeclarationReachesAPerson covers the person's own typo.
//
// Treating it as "nothing declared" would answer the gate by asking them, which
// hides the very thing they were trying to fix — so it is unrunnable, which
// carries the results in front of somebody.
func TestAMalformedDeclarationReachesAPerson(t *testing.T) {
	reg := &fakeRegistry{task: taskDeclaring(t, `{"luna_gates":{"confirm":7}}`)}

	got := checkGateWith(reg, t.TempDir())(context.Background(), "LUNA-1", fsm.GateConfirm)

	if !got.Unrunnable {
		t.Errorf("a malformed declaration reported %+v, want unrunnable", got)
	}
}

// TestWithNoRegistryThereIsNoMechanicalHalf covers the project that has none.
func TestWithNoRegistryThereIsNoMechanicalHalf(t *testing.T) {
	if checkGateWith(nil, t.TempDir()) != nil {
		t.Error("a project with no registry got a mechanical half anyway")
	}
}
