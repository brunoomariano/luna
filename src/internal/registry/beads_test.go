package registry

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeBD is a stand-in for the binary, recording what it was asked and replying
// with what the real one replies. A named fake rather than an inline closure
// because several tests need the same behaviour and the recorded calls are what
// most of them assert on.
type fakeBD struct {
	// reply is keyed by subcommand: "create", "show", "update".
	reply map[string]string

	// exit is keyed the same way, for the paths where the code carries meaning.
	exit map[string]int

	calls [][]string
}

func (f *fakeBD) run(_ context.Context, _ string, args ...string) ([]byte, int, error) {
	f.calls = append(f.calls, args)

	key := args[0]
	if len(args) > 1 && (key == "provenance" || key == "label") {
		key = args[0] + " " + args[1]
	}
	return []byte(f.reply[key]), f.exit[key], nil
}

func (f *fakeBD) sawFlag(subcommand, flag, value string) bool {
	for _, call := range f.calls {
		if call[0] != subcommand {
			continue
		}
		for i, arg := range call {
			if arg == flag && i+1 < len(call) && call[i+1] == value {
				return true
			}
		}
	}
	return false
}

func fakeRegistry(reply map[string]string) (*Beads, *fakeBD) {
	fake := &fakeBD{reply: reply, exit: map[string]int{}}
	return &Beads{Dir: ".", Run: fake.run}, fake
}

func TestCreatingATaskTakesTheIdTheRegistryAssigns(t *testing.T) {
	b, _ := fakeRegistry(map[string]string{
		"create": `{"id":"luna-zig","title":"add auth","status":"open"}`,
	})

	id, err := b.Create(context.Background(), "add auth")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id != "luna-zig" {
		t.Errorf("id = %q, want the one beads assigned", id)
	}
}

// TestATaskWithNoIdIsRefused. A registry that answers without an id has not
// created anything, and returning an empty string would let the caller carry on
// referring to a task that does not exist.
func TestATaskWithNoIdIsRefused(t *testing.T) {
	b, _ := fakeRegistry(map[string]string{"create": `{"title":"add auth"}`})

	if _, err := b.Create(context.Background(), "add auth"); err == nil {
		t.Fatal("a response with no id was accepted")
	}
}

// TestMovingATaskGuardsOnWhereItWas is the concurrency rule. It is the same
// guarantee AppendActionAt gave, arrived at differently — a decision taken
// against a state that has since changed must not be written (ADR-0047).
func TestMovingATaskGuardsOnWhereItWas(t *testing.T) {
	b, fake := fakeRegistry(map[string]string{"update": `[{"id":"luna-zig"}]`})

	if err := b.Move(context.Background(), "luna-zig", StatusOpen, StatusInProgress); err != nil {
		t.Fatalf("Move: %v", err)
	}

	if !fake.sawFlag("update", "--if-status", "open") {
		t.Errorf("the move was written without a guard: %v", fake.calls)
	}
	if !fake.sawFlag("update", "--status", "in_progress") {
		t.Errorf("the move did not set the new status: %v", fake.calls)
	}
}

// TestLosingTheRaceIsReportedAsSuch. bd exits 13 and says nothing was written;
// treating that as an ordinary failure would send the caller into a retry that
// its own documentation calls pointless.
func TestLosingTheRaceIsReportedAsSuch(t *testing.T) {
	b, fake := fakeRegistry(map[string]string{
		"update": `{"error":"1 of 1 issues failed","failed":[{"guard_mismatch":true}]}`,
	})
	fake.exit["update"] = exitGuardMismatch

	err := b.Move(context.Background(), "luna-zig", StatusOpen, StatusClosed)

	if !errors.Is(err, ErrLostTheRace) {
		t.Fatalf("err = %v, want ErrLostTheRace", err)
	}
}

func TestAnUnknownTaskIsNotAnEmptyOne(t *testing.T) {
	b, fake := fakeRegistry(map[string]string{"show": `{"error":"not found"}`})
	fake.exit["show"] = 1

	_, err := b.Task(context.Background(), "nope-1")

	if !errors.Is(err, ErrNoSuchTask) {
		t.Fatalf("err = %v, want ErrNoSuchTask", err)
	}
}

func TestAnEmptyResultIsAlsoAnUnknownTask(t *testing.T) {
	b, _ := fakeRegistry(map[string]string{"show": `[]`})

	if _, err := b.Task(context.Background(), "nope-1"); !errors.Is(err, ErrNoSuchTask) {
		t.Fatalf("err = %v, want ErrNoSuchTask", err)
	}
}

// TestTheStageRidesAsALabel keeps the flow out of beads. beads has no concept of
// a stage and must not grow one — which stage comes next is Luna's, and it is
// the one thing this whole migration does not move (INV-core-1).
func TestTheStageRidesAsALabel(t *testing.T) {
	b, fake := fakeRegistry(map[string]string{
		"show": `[{"id":"luna-zig","status":"open"}]`,
	})

	if err := b.EnterStage(context.Background(), "luna-zig", "build"); err != nil {
		t.Fatalf("EnterStage: %v", err)
	}

	added := false
	for _, call := range fake.calls {
		if call[0] == "label" && call[1] == "add" && call[3] == stagePrefix+"build" {
			added = true
		}
	}
	if !added {
		t.Errorf("the stage was not recorded: %v", fake.calls)
	}
}

// TestATaskCarriesExactlyOneStage. Two stage labels make Stage() return
// whichever comes first, which is a question with no right answer.
func TestATaskCarriesExactlyOneStage(t *testing.T) {
	b, fake := fakeRegistry(map[string]string{
		"show": `[{"id":"luna-zig","status":"open","labels":["luna:stage:discovery","urgent"]}]`,
	})

	if err := b.EnterStage(context.Background(), "luna-zig", "build"); err != nil {
		t.Fatalf("EnterStage: %v", err)
	}

	removed := false
	for _, call := range fake.calls {
		if call[0] == "label" && call[1] == "remove" && call[3] == stagePrefix+"discovery" {
			removed = true
		}
	}
	if !removed {
		t.Errorf("the previous stage label was left in place: %v", fake.calls)
	}
}

// TestReenteringTheSameStageWritesNothing. A stage re-entered after a retry is
// the same stage, and a label rewritten for no reason is noise in an audit
// trail.
func TestReenteringTheSameStageWritesNothing(t *testing.T) {
	b, fake := fakeRegistry(map[string]string{
		"show": `[{"id":"luna-zig","status":"open","labels":["luna:stage:build"]}]`,
	})

	if err := b.EnterStage(context.Background(), "luna-zig", "build"); err != nil {
		t.Fatalf("EnterStage: %v", err)
	}

	for _, call := range fake.calls {
		if call[0] == "label" {
			t.Errorf("re-entering the same stage rewrote a label: %v", call)
		}
	}
}

func TestTheStageIsReadBackFromTheLabels(t *testing.T) {
	task := Task{Labels: []string{"urgent", stagePrefix + "review", "team:core"}}

	if got := task.Stage(); got != "review" {
		t.Errorf("Stage() = %q, want review", got)
	}
	if got := (Task{Labels: []string{"urgent"}}).Stage(); got != "" {
		t.Errorf("a task with no stage label reported %q", got)
	}
}

// TestACommitIsRecordedAsProvenance is the audit trail in the new model: git
// holds how the task got there, the registry holds the pointer (INV-core-2).
func TestACommitIsRecordedAsProvenance(t *testing.T) {
	b, fake := fakeRegistry(map[string]string{
		"provenance record": `{"id":"4fac","inserted":true}`,
	})

	sha := strings.Repeat("a", 40)
	if err := b.RecordCommit(context.Background(), "luna-zig", sha, "build"); err != nil {
		t.Fatalf("RecordCommit: %v", err)
	}

	if !fake.sawFlag("provenance", "--ref", sha) {
		t.Errorf("the commit was not recorded: %v", fake.calls)
	}
	if !fake.sawFlag("provenance", "--ref-kind", "git-sha") {
		t.Errorf("the ref was recorded without its kind: %v", fake.calls)
	}
	// The stage travels in the payload rather than as a beads field, for the
	// same reason the stage label does.
	if !fake.sawFlag("provenance", "--payload", `{"stage":"build"}`) {
		t.Errorf("the stage did not travel with the commit: %v", fake.calls)
	}
}

func TestDeliveriesComeBackWithTheirStage(t *testing.T) {
	b, _ := fakeRegistry(map[string]string{
		"provenance log": `[
          {"kind":"commit","ref":"aaa","payload":"{\"stage\":\"build\"}","created_at":"2026-08-12T19:22:17Z"},
          {"kind":"claim","ref":"bbb","payload":"","created_at":"2026-08-12T19:22:18Z"},
          {"kind":"commit","ref":"ccc","payload":"not json","created_at":"2026-08-12T19:22:19Z"}
        ]`,
	})

	got, err := b.Commits(context.Background(), "luna-zig")
	if err != nil {
		t.Fatalf("Commits: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d deliveries, want the two commits (a claim is not a delivery)", len(got))
	}
	if got[0].Stage != "build" {
		t.Errorf("stage = %q, want build", got[0].Stage)
	}
	// An unparseable payload loses the stage and keeps the commit. The commit is
	// the fact; the stage is a convenience.
	if got[1].Commit != "ccc" || got[1].Stage != "" {
		t.Errorf("got %+v, want the commit kept and the stage dropped", got[1])
	}
}

// TestBlockedTasksAreOneQuery is most of why the registry moved. The local store
// answered this by replaying every log in every worktree (INV-core-12).
func TestBlockedTasksAreOneQuery(t *testing.T) {
	b, fake := fakeRegistry(map[string]string{
		"list": `[{"id":"luna-zig","status":"blocked","labels":["luna:stage:build"]}]`,
	})

	got, err := b.Blocked(context.Background())
	if err != nil {
		t.Fatalf("Blocked: %v", err)
	}

	if len(got) != 1 || got[0].Stage() != "build" {
		t.Errorf("got %+v, want the blocked task and its stage", got)
	}
	if !fake.sawFlag("list", "--status", "blocked") {
		t.Errorf("the query did not filter by status: %v", fake.calls)
	}
}

func TestAFailingCommandIsReportedWithWhatItSaid(t *testing.T) {
	b, fake := fakeRegistry(map[string]string{"list": "database is locked"})
	fake.exit["list"] = 1

	_, err := b.Blocked(context.Background())

	if err == nil {
		t.Fatal("a failing bd was treated as success")
	}
	if !strings.Contains(err.Error(), "database is locked") {
		t.Errorf("the error does not carry what bd said: %v", err)
	}
}

// TestOutputThatIsNotJSONIsReported covers every reader. A registry answering
// with something unparseable is a version skew or a broken install, and the one
// wrong response is to carry on with a zero value — an empty task, no
// deliveries, nothing blocked. All three read as "everything is fine".
func TestOutputThatIsNotJSONIsReported(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		call func(*Beads) error
	}{
		{"create", "create", func(b *Beads) error {
			_, err := b.Create(context.Background(), "a task")
			return err
		}},
		{"show", "show", func(b *Beads) error {
			_, err := b.Task(context.Background(), "luna-zig")
			return err
		}},
		{"provenance log", "provenance log", func(b *Beads) error {
			_, err := b.Commits(context.Background(), "luna-zig")
			return err
		}},
		{"list", "list", func(b *Beads) error {
			_, err := b.Blocked(context.Background())
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := fakeRegistry(map[string]string{tc.key: "not json at all"})

			if err := tc.call(b); err == nil {
				t.Error("an unparseable response was treated as an answer")
			}
		})
	}
}

// TestAFailureOnEitherHalfOfEnterStageIsReported. The stage is two writes —
// remove the old label, add the new — and a failure in between must not be
// swallowed, or a task ends up with no stage at all.
func TestAFailureOnEitherHalfOfEnterStageIsReported(t *testing.T) {
	for _, failing := range []string{"label remove", "label add"} {
		t.Run(failing, func(t *testing.T) {
			b, fake := fakeRegistry(map[string]string{
				"show":  `[{"id":"luna-zig","labels":["luna:stage:discovery"]}]`,
				failing: "beads said no",
			})
			fake.exit[failing] = 1

			if err := b.EnterStage(context.Background(), "luna-zig", "build"); err == nil {
				t.Errorf("a failing `bd %s` was reported as success", failing)
			}
		})
	}
}

// TestAStageCannotBeSetOnATaskThatIsNotThere. EnterStage reads before it writes,
// and the read failing means there is nothing to label.
func TestAStageCannotBeSetOnATaskThatIsNotThere(t *testing.T) {
	b, fake := fakeRegistry(map[string]string{"show": `{"error":"not found"}`})
	fake.exit["show"] = 1

	if err := b.EnterStage(context.Background(), "nope-1", "build"); !errors.Is(err, ErrNoSuchTask) {
		t.Fatalf("err = %v, want ErrNoSuchTask", err)
	}
}

func TestAFailingRecordIsReported(t *testing.T) {
	b, fake := fakeRegistry(map[string]string{"provenance record": "ref-kind git-sha requires 40 characters"})
	fake.exit["provenance record"] = 1

	err := b.RecordCommit(context.Background(), "luna-zig", "abc", "build")
	if err == nil {
		t.Fatal("a refused provenance record was reported as success")
	}
	if !strings.Contains(err.Error(), "40 characters") {
		t.Errorf("the error does not carry what beads said: %v", err)
	}
}

func TestAFailingMoveIsReported(t *testing.T) {
	b, fake := fakeRegistry(map[string]string{"update": "database is locked"})
	fake.exit["update"] = 1

	err := b.Move(context.Background(), "luna-zig", StatusOpen, StatusClosed)
	if err == nil {
		t.Fatal("a failing move was reported as success")
	}
	if errors.Is(err, ErrLostTheRace) {
		t.Error("a genuine failure was reported as a lost race — retrying it is " +
			"pointless only when the guard is what refused")
	}
}

func TestAnAdapterWithNoRunnerSaysSo(t *testing.T) {
	if _, err := (&Beads{Dir: "."}).Blocked(context.Background()); err == nil {
		t.Fatal("a registry with no runner reported success")
	}
}

// TestOutputIsTruncatedInAnError keeps a failure readable when bd writes a wall
// of text. The message is for a person.
func TestOutputIsTruncatedInAnError(t *testing.T) {
	long := strings.Repeat("x", 1000)

	if got := truncate([]byte(long)); len(got) > 320 {
		t.Errorf("a 1000-character output produced a %d-character message", len(got))
	}
	if got := truncate([]byte(" short ")); got != "short" {
		t.Errorf("truncate trimmed or mangled a short message: %q", got)
	}
}
