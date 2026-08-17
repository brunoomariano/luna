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

	// Only this checkout's log is asked. The registry used to be consulted here for
	// tasks blocked in *another* checkout — the one question a single repository's
	// log cannot answer — and that went with it (ADR-0067).
	//
	// What replaces it is a central store rather than a central tracker: one
	// LUNA_STORE shared between checkouts makes every task local to the same log,
	// and this listing covers them all with no second source to reconcile.

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
