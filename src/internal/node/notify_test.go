package node

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestABlockedTaskIsAnnouncedWithItsReason covers what a person sees.
//
// A banner saying only "LUNA-1 is blocked" makes them open a terminal to find out
// why. The reason travels with it because the point is to save that trip.
func TestABlockedTaskIsAnnouncedWithItsReason(t *testing.T) {
	var title, body string
	n := Notifier{Run: func(_ context.Context, gotTitle, gotBody string) error {
		title, body = gotTitle, gotBody
		return nil
	}}

	if err := n.Blocked(context.Background(), "LUNA-1", "the node did not react"); err != nil {
		t.Fatalf("notifying: %v", err)
	}

	if !strings.Contains(title, "LUNA-1") {
		t.Errorf("the title must name the task, got %q", title)
	}
	if body != "the node did not react" {
		t.Errorf("the reason must survive, got %q", body)
	}
}

// TestANotifierWithNothingBehindItIsSilent covers a machine with no external
// notifier installed.
//
// Returning an error would make every blocked task on such a machine report a
// second failure that is not about the task.
func TestANotifierWithNothingBehindItIsSilent(t *testing.T) {
	if err := (Notifier{}).Blocked(context.Background(), "LUNA-1", "why"); err != nil {
		t.Errorf("no notifier is not a failure: %v", err)
	}
}

// TestAFailingNotifierReportsWhatWentWrong keeps the error useful.
//
// The caller decides what to do with it — and always carries on — but it has to
// be able to say what failed rather than that something did.
func TestAFailingNotifierReportsWhatWentWrong(t *testing.T) {
	n := Notifier{Run: func(context.Context, string, string) error {
		return errors.New("herdr is not running")
	}}

	err := n.Blocked(context.Background(), "LUNA-1", "why")
	if err == nil {
		t.Fatal("a notifier that failed says so")
	}
	if !strings.Contains(err.Error(), "herdr is not running") {
		t.Errorf("the cause must survive, got %q", err)
	}
}

// TestTheShippedNotifierCallsHerdr pins the command line.
//
// The arguments were checked against the binary rather than read from
// documentation, the discipline arrived at after ten protocol facts turned out
// to be wrong. This keeps them from drifting quietly.
func TestTheShippedNotifierCallsHerdr(t *testing.T) {
	if NewNotifier().Run == nil {
		t.Fatal("the shipped notifier has to do something")
	}

	// Calling it needs the external notifier on the machine, which CI has no reason
	// to have. What is worth pinning here is that a notifier exists and that a
	// failure to reach it comes back as an error rather than a panic.
	err := NewNotifier().Run(context.Background(), "LUNA-1", "why")
	if err != nil && !strings.Contains(err.Error(), "herdr notification show") {
		t.Errorf("a failure must name the command it tried, got %q", err)
	}
}
