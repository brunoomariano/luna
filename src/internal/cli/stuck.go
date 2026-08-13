package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/brunoomariano/luna/src/internal/fsm"
	"github.com/brunoomariano/luna/src/internal/store"
)

// defaultPatience is how long something may sit stopped before it is worth
// telling someone about.
//
// An hour is long enough that an ordinary gate answered over lunch never fires,
// and short enough that nothing loses a working day. The number matters less
// than the fact that it is bounded: swarm-forge's own bugs.md records a
// multi-hour stall whose cause the dashboard never surfaced, and any finite
// patience would have caught it.
const defaultPatience = time.Hour

// stuckCommand lists what has been stopped for too long, and optionally says so
// out loud.
//
// This is the watchdog, and it is a command rather than a loop on purpose.
// Whatever polls — a cron, herdr, the lead — decides how often to ask; keeping
// the schedule outside means the clock is read in exactly one place and nothing
// in Luna has to stay running to notice a stall (ADR-0053).
func stuckCommand(env Env, args []string) error {
	flags, err := parseFlags(args)
	if err != nil {
		return err
	}

	patience := defaultPatience
	if raw := flags["for"]; raw != "" {
		patience, err = time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("%w: --for takes a duration like 30m or 2h, not %q", ErrUsage, raw)
		}
	}

	stuck, err := env.Store.Stalled(fsm.DefaultFlow(), patience)
	if err != nil {
		return err
	}

	// The registry is asked too, and what it adds is the thing the log cannot
	// answer: a task blocked in *another* checkout. The log is one repository's;
	// the registry is central, which is the reason it exists (ADR-0054).
	//
	// It is additive rather than authoritative — the local log stays the source
	// of truth for anything it knows about, and a registry that is missing or
	// unreachable degrades the listing rather than failing it. A watchdog that
	// stops watching because a tracker is down is a watchdog that stops watching
	// exactly when something is wrong.
	if elsewhere, err := env.blockedElsewhere(stuck); err != nil {
		fmt.Fprintf(env.Err, "could not ask the registry: %v\n", err)
	} else {
		stuck = append(stuck, elsewhere...)
	}

	if _, ok := flags["json"]; ok {
		return writeJSON(env.Out, stuckReport(stuck))
	}

	if len(stuck) == 0 {
		fmt.Fprintf(env.Out, "nothing stuck for more than %s\n", patience)
		return nil
	}

	for _, s := range stuck {
		fmt.Fprintln(env.Out, s.String())
	}

	// --notify is what turns a listing into a watchdog. Without it this reports
	// to whoever already ran the command, which is whoever was already looking —
	// and the failure being guarded against is that nobody is (INV-core-8).
	if _, ok := flags["notify"]; ok {
		return announce(env, stuck)
	}
	return nil
}

// announce tells a person, once per stuck task.
//
// A notifier that fails does not fail the command. The listing already printed,
// so the information is not lost, and a machine with no notifier should report
// and carry on rather than turn "something is stuck" into "the watchdog broke".
func announce(env Env, stuck []store.Stuck) error {
	if env.Notify == nil {
		fmt.Fprintln(env.Err, "no notifier configured — the list above is the only report")
		return nil
	}

	for _, s := range stuck {
		if err := env.Notify(context.Background(), s.TaskID, s.String()); err != nil {
			fmt.Fprintf(env.Err, "could not notify about %s: %v\n", s.TaskID, err)
		}
	}
	return nil
}

// blockedElsewhere asks the registry for tasks this repository's log knows
// nothing about.
//
// The dedup is by task id and it keeps the *local* entry, because the log has an
// age and a recorded reason while the registry has a status and a label. Where
// both know a task, the log knows more.
//
// A nil registry is not an error: Luna works in a project that has not adopted
// beads, and every cross-checkout query simply returns nothing there.
func (e Env) blockedElsewhere(known []store.Stuck) ([]store.Stuck, error) {
	if e.Registry == nil {
		return nil, nil
	}

	tasks, err := e.Registry.Blocked(context.Background())
	if err != nil {
		return nil, err
	}

	local := make(map[string]bool, len(known))
	for _, s := range known {
		local[s.TaskID] = true
	}

	var elsewhere []store.Stuck
	for _, task := range tasks {
		if local[task.ID] {
			continue
		}
		// No age: the registry records when a task was last touched, not how long
		// it has been stopped, and inventing a duration from `updated_at` would
		// report a number that means something else. The reason says where to
		// look instead.
		elsewhere = append(elsewhere, store.Stuck{
			TaskID: task.ID,
			Stage:  fsm.StageID(task.Stage()),
			Status: fsm.StatusBlocked,
			Reason: "blocked in another checkout (from the registry)",
		})
	}
	return elsewhere, nil
}

// StuckReport is the structured shape of `luna stuck`.
type StuckReport struct {
	TaskID string      `json:"task_id"`
	Stage  fsm.StageID `json:"stage,omitempty"`
	Status fsm.Status  `json:"status"`
	Reason string      `json:"reason"`

	// SinceSeconds is a number rather than a formatted duration: whoever parses
	// this wants to compare it, and a caller that wants "3h12m" can format it.
	SinceSeconds int `json:"since_seconds"`
}

func stuckReport(stuck []store.Stuck) []StuckReport {
	// A non-nil empty slice, so the JSON is `[]` rather than `null`. A consumer
	// that iterates the result should not have to special-case "nothing stuck".
	report := make([]StuckReport, 0, len(stuck))
	for _, s := range stuck {
		report = append(report, StuckReport{
			TaskID:       s.TaskID,
			Stage:        s.Stage,
			Status:       s.Status,
			Reason:       s.Reason,
			SinceSeconds: int(s.Since.Seconds()),
		})
	}
	return report
}
