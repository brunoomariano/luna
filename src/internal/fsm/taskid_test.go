package fsm

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// TestValidateTaskIDRefusesWhatBreaksDownstream is the point of the check: the id
// is not only a key, it becomes a directory name and a branch name.
func TestValidateTaskIDRefusesWhatBreaksDownstream(t *testing.T) {
	refused := []struct {
		id  string
		why string
	}{
		{"", "an empty id names nothing"},
		{"../../etc", "climbs out of the directory the worktree belongs in"},
		{"..", "the same, at its shortest"},
		{"a/b", "a separator makes the id a path of its own"},
		{"a\\b", "the same on the other separator"},
		{"LUNA 1", "a space quotes the directory and the branch"},
		{"task#1", "punctuation a branch name and a path do not want"},
		{"caf\u00e9", "outside the ASCII range the names are built from"},
		{strings.Repeat("a", MaxTaskIDLen+1), "one character past the limit"},
	}

	for _, c := range refused {
		if err := ValidateTaskID(c.id); !errors.Is(err, ErrInvalidTaskID) {
			t.Errorf("%q should be refused — %s; got %v", c.id, c.why, err)
		}
	}
}

// TestValidateTaskIDAcceptsWhatPeopleActuallyType keeps the rule from being so
// narrow that a tracker's own ids stop working.
func TestValidateTaskIDAcceptsWhatPeopleActuallyType(t *testing.T) {
	accepted := []string{
		"LUNA-1",                          // what this project uses
		"PROJ-4823",                       // what a tracker gives you
		"fix_login",                       // a slug
		"a",                               // the shortest useful id
		strings.Repeat("x", MaxTaskIDLen), // exactly at the limit
	}

	for _, id := range accepted {
		if err := ValidateTaskID(id); err != nil {
			t.Errorf("%q is an ordinary id and must be accepted: %v", id, err)
		}
	}
}

// TestTheRefusalSaysWhat keeps the error useful.
//
// An id is typed by a person, and "invalid" without naming the offending
// character or the limit is a message that makes them guess.
func TestTheRefusalSaysWhat(t *testing.T) {
	tooLong := strings.Repeat("a", MaxTaskIDLen+5)

	err := ValidateTaskID(tooLong)
	if err == nil {
		t.Fatal("setup: this id is over the limit")
	}
	// The limit itself, derived rather than typed: a message that says "too long"
	// without the number makes the person guess how much to cut.
	for _, want := range []string{strconv.Itoa(MaxTaskIDLen), "branch name"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should mention %q, got %q", want, err)
		}
	}

	err = ValidateTaskID("a b")
	if err == nil {
		t.Fatal("setup: a space is not allowed")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("the refusal should say why the rule exists, got %q", err)
	}
}

// TestTheShippedFlowLeavesRoomForATaskID ties the limit to the flow it is read
// beside.
//
// A stage id and a task id share the room the names built from them have. If a
// stage is renamed to something long enough to eat that room, the limit stops
// being true and nothing else would say so.
func TestTheShippedFlowLeavesRoomForATaskID(t *testing.T) {
	if gaps := AuditFlowNames(DefaultFlow()); len(gaps) > 0 {
		for _, gap := range gaps {
			t.Errorf("stage %q leaves only %d characters for a task id, and MaxTaskIDLen is %d",
				gap.Stage, gap.Budget, MaxTaskIDLen)
		}
	}
}

// TestAFlowWithLongStageNamesIsReported covers the custom-flow case a project
// bringing its own flow allows and the arithmetic cannot know about.
func TestAFlowWithLongStageNamesIsReported(t *testing.T) {
	// Long enough to leave less than MaxTaskIDLen behind, which is the condition
	// being tested rather than any particular number of characters.
	tooLong := StageID(strings.Repeat("a-stage-name-nobody-should-write-", 3))

	flow := []Stage{
		{ID: "short", Requires: []Artifact{TaskID}, Produces: []Artifact{"a"}},
		{ID: tooLong, Requires: []Artifact{"a"}, Produces: []Artifact{"b"}},
	}

	gaps := AuditFlowNames(flow)
	if len(gaps) != 1 {
		t.Fatalf("want the long stage reported, got %+v", gaps)
	}
	if gaps[0].Stage != tooLong {
		t.Errorf("the wrong stage was reported: %+v", gaps[0])
	}
	if gaps[0].Budget >= MaxTaskIDLen {
		t.Errorf("a stage that leaves room should not be reported, got budget %d", gaps[0].Budget)
	}
}
