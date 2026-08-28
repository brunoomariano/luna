package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// TestGlobalWatchingCommandsReadEveryProjectFromTheCentralStore covers the CLI
// consequence of one database. A person may stand in one project, but the
// commands whose subject is the whole workload must still report another one.
func TestGlobalWatchingCommandsReadEveryProjectFromTheCentralStore(t *testing.T) {
	h := newHarness(t)
	central := h.env.Store
	first := central.ForProject("github.com-one-app-a1")
	second := central.ForProject("github.com-two-app-b2")

	h.env.Store = first
	h.mustRun(t, "task", "new", "ONE-1", "--kind", "feature", "--simulated")
	h.mustRun(t, "lead", "ONE-1", "--dry-run")

	h.env.Store = second
	h.mustRun(t, "task", "new", "TWO-1", "--kind", "feature", "--simulated")
	h.mustRun(t, "lead", "TWO-1", "--dry-run")

	h.env.Store = first
	h.env.GlobalStore = central
	for _, command := range [][]string{
		{"task", "list"},
		{"gates"},
		{"stuck", "--for", "0s"},
		{"fleet", "report"},
		{"flow", "check"},
	} {
		out := h.mustRun(t, command...)
		for _, want := range []string{"github.com-one-app-a1", "ONE-1", "github.com-two-app-b2", "TWO-1"} {
			if !strings.Contains(out, want) {
				t.Errorf("luna %s omitted %q from the central view:\n%s",
					strings.Join(command, " "), want, out)
			}
		}
	}

	jsonReport := h.mustRun(t, "gates", "--json")
	if got := strings.Count(jsonReport, `"profile_defined"`); got != 1 {
		t.Errorf("global gates claimed profile knowledge for %d projects, want only the current one:\n%s",
			got, jsonReport)
	}
}

// TestTaskListContainsOnlyActiveTasks makes "active" an observable contract.
// Finished and abandoned history remains in the database and in fleet reports,
// but the short list is the work that can still move.
func TestTaskListContainsOnlyActiveTasks(t *testing.T) {
	h := newHarness(t)
	h.env.GlobalStore = h.env.Store

	h.mustRun(t, "task", "new", "OPEN-1", "--kind", "chore")
	h.mustRun(t, "task", "new", "ENDED-1", "--kind", "chore")
	h.mustRun(t, "task", "abandon", "ENDED-1", "superseded")

	out := h.mustRun(t, "task", "list")
	if !strings.Contains(out, "OPEN-1") {
		t.Errorf("the active task is missing:\n%s", out)
	}
	if strings.Contains(out, "ENDED-1") {
		t.Errorf("an ended task appears in the active list:\n%s", out)
	}
}

func TestTaskListReportsJSONEmptyAndUnreadableViews(t *testing.T) {
	empty := newHarness(t)
	empty.env.GlobalStore = empty.env.Store
	if out := empty.mustRun(t, "task", "list"); !strings.Contains(out, "no active tasks") {
		t.Fatalf("empty task list = %q", out)
	}
	if err := empty.run(t, "task", "list", "--unknown"); err == nil {
		t.Fatal("task list accepted an unknown option")
	}

	h := newHarness(t)
	h.env.GlobalStore = h.env.Store
	if err := h.env.Store.AppendAction("CHANGED-1", fsm.TaskCreated{
		Kind: fsm.KindChore, Flow: "0123456789abcdef",
	}); err != nil {
		t.Fatalf("seeding the changed-flow task: %v", err)
	}
	out := h.mustRun(t, "task", "list", "--json")
	var report []ActiveTaskReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decoding task list: %v\n%s", err, out)
	}
	if len(report) != 1 || report[0].ID != "CHANGED-1" || report[0].Status != "unreadable" {
		t.Fatalf("changed-flow task list = %+v", report)
	}
}
