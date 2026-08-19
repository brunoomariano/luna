package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// blockAndAge opens a task, blocks it, and moves the store's clock forward so
// the block has an age without any test sleeping.
func blockAndAge(t *testing.T, h *harness, id, reason string, age time.Duration) {
	t.Helper()

	at := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	h.env.Store.Now = func() time.Time { return at }

	h.mustRun(t, "task", "new", id, "--kind", "feature", "--profile", "nightly")
	if err := h.env.Store.AppendAction(id, fsm.Block{Reason: reason}); err != nil {
		t.Fatalf("blocking %s: %v", id, err)
	}

	at = at.Add(age)
}

func TestStuckReportsNothingWhenNothingIsStuck(t *testing.T) {
	h := newHarness(t)
	blockAndAge(t, h, "LUNA-1", "merge conflict on runner.go", 5*time.Minute)

	out := h.mustRun(t, "stuck")

	if !strings.Contains(out, "nothing stuck") {
		t.Errorf("a five-minute-old block was reported:\n%s", out)
	}
}

// TestStuckNamesWhatIsWaitingAndForHowLong is the whole point of the command:
// "a task is stuck" sends someone looking, the file and the duration tell them
// where to look and whether it is urgent.
func TestStuckNamesWhatIsWaitingAndForHowLong(t *testing.T) {
	h := newHarness(t)
	blockAndAge(t, h, "LUNA-1", "merge conflict on runner.go", 3*time.Hour)

	out := h.mustRun(t, "stuck")

	for _, want := range []string{"LUNA-1", "3h", "runner.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestStuckTakesAPatience(t *testing.T) {
	h := newHarness(t)
	blockAndAge(t, h, "LUNA-1", "merge conflict", 30*time.Minute)

	if out := h.mustRun(t, "stuck"); !strings.Contains(out, "nothing stuck") {
		t.Errorf("half an hour beat the default patience of an hour:\n%s", out)
	}
	if out := h.mustRun(t, "stuck", "--for", "10m"); !strings.Contains(out, "LUNA-1") {
		t.Errorf("a ten-minute patience did not report a half-hour block:\n%s", out)
	}
}

func TestStuckRefusesAPatienceItCannotRead(t *testing.T) {
	h := newHarness(t)

	err := h.run(t, "stuck", "--for", "soon")
	if err == nil {
		t.Fatal("--for soon was accepted")
	}
	if !strings.Contains(err.Error(), "duration") {
		t.Errorf("the refusal does not say what a duration looks like: %v", err)
	}
}

func TestStuckRefusesACommandLineItCannotParse(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "stuck", "LUNA-1"); err == nil {
		t.Fatal("a bare argument was accepted; stuck takes flags only")
	}
	if err := h.run(t, "stuck", "--for"); err == nil {
		t.Fatal("--for with no value was accepted")
	}
}

// TestNotifyTellsSomebodyRatherThanOnlyTheCaller is the difference between a
// listing and a watchdog. Without it the report reaches whoever ran the command,
// and the failure being guarded against is that nobody is looking.
func TestNotifyTellsSomebodyRatherThanOnlyTheCaller(t *testing.T) {
	h := newHarness(t)
	blockAndAge(t, h, "LUNA-1", "merge conflict on runner.go", 3*time.Hour)

	var told []string
	h.env.Notify = func(_ context.Context, taskID, body string) error {
		told = append(told, taskID+": "+body)
		return nil
	}

	h.mustRun(t, "stuck", "--notify")

	if len(told) != 1 {
		t.Fatalf("notified %d times, want once", len(told))
	}
	if !strings.Contains(told[0], "runner.go") {
		t.Errorf("the notification does not say why: %q", told[0])
	}
}

// TestNothingIsNotifiedWhenNothingIsStuck guards the way a watchdog dies: an
// alert that fires when everything is fine is an alert people mute.
func TestNothingIsNotifiedWhenNothingIsStuck(t *testing.T) {
	h := newHarness(t)
	blockAndAge(t, h, "LUNA-1", "merge conflict", time.Minute)

	notified := 0
	h.env.Notify = func(context.Context, string, string) error {
		notified++
		return nil
	}

	h.mustRun(t, "stuck", "--notify")

	if notified != 0 {
		t.Errorf("notified %d times about a task nobody needs to look at", notified)
	}
}

// TestANotifierThatFailsDoesNotHideTheReport — the information already printed,
// and turning "something is stuck" into "the watchdog broke" would lose it.
func TestANotifierThatFailsDoesNotHideTheReport(t *testing.T) {
	h := newHarness(t)
	blockAndAge(t, h, "LUNA-1", "merge conflict on runner.go", 3*time.Hour)

	h.env.Notify = func(context.Context, string, string) error {
		return context.DeadlineExceeded
	}

	out := h.mustRun(t, "stuck", "--notify")

	if !strings.Contains(out, "LUNA-1") {
		t.Errorf("the listing was lost when the notifier failed:\n%s", out)
	}
	if !strings.Contains(h.errOut.String(), "LUNA-1") {
		t.Errorf("the notifier's failure was swallowed: %q", h.errOut.String())
	}
}

// TestWithNoNotifierTheListingSaysSo. A machine with no notifier should carry
// on, and say that the printed list is the whole report rather than implying
// somebody was told.
func TestWithNoNotifierTheListingSaysSo(t *testing.T) {
	h := newHarness(t)
	blockAndAge(t, h, "LUNA-1", "merge conflict", 3*time.Hour)
	h.env.Notify = nil

	h.mustRun(t, "stuck", "--notify")

	if !strings.Contains(h.errOut.String(), "no notifier") {
		t.Errorf("nothing said the notification went nowhere: %q", h.errOut.String())
	}
}

func TestStuckComesInBothShapes(t *testing.T) {
	h := newHarness(t)
	blockAndAge(t, h, "LUNA-1", "merge conflict on runner.go", 3*time.Hour)

	raw := h.mustRun(t, "stuck", "--json")

	var report []StuckReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("stuck --json does not parse: %v\n%s", err, raw)
	}
	if len(report) != 1 {
		t.Fatalf("got %d entries, want 1", len(report))
	}
	if report[0].SinceSeconds != int((3 * time.Hour).Seconds()) {
		t.Errorf("since = %ds, want 3 hours in seconds", report[0].SinceSeconds)
	}
	if report[0].Status != fsm.StatusBlocked {
		t.Errorf("status = %q, want blocked", report[0].Status)
	}
}

// TestAnEmptyStuckListIsAnArrayNotNull — a consumer that iterates the result
// should not have to special-case nothing being wrong.
func TestAnEmptyStuckListIsAnArrayNotNull(t *testing.T) {
	h := newHarness(t)

	if out := strings.TrimSpace(h.mustRun(t, "stuck", "--json")); out != "[]" {
		t.Errorf("stuck --json with nothing stuck returned %q, want []", out)
	}
}
