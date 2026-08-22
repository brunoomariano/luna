package store

import (
	"errors"
	"fmt"
	"time"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// Stuck is a task that stopped and has been stopped for a while.
//
// It is the watchdog's subject, and the reason the watchdog can exist at all. An
// earlier design had it inspect a replayed TaskState and ask whether the task
// looked stalled — which nothing but a test fake could ever implement, because a
// state rebuilt from the log carries no clock.
//
// This asks a question the log can answer: the last event has a timestamp, and
// the difference between then and now is a fact rather than a judgement.
type Stuck struct {
	TaskID string
	Stage  fsm.StageID
	Status fsm.Status

	// Reason is why it stopped — the recorded block, or what the gate is asking.
	Reason string

	// Since is how long it has been in this state, measured from the last event
	// in its log.
	Since time.Duration
}

// String is what a notification says. It leads with the duration because that is
// the part that makes it worth reading: everything here was already visible in
// `luna gates`, and what is new is that nobody has looked.
func (s Stuck) String() string {
	return fmt.Sprintf("%s has been %s for %s at %s: %s",
		s.TaskID, s.Status, round(s.Since), s.Stage, s.Reason)
}

// Stalled lists tasks that have been stopped for longer than the given patience.
//
// It covers blocked tasks and tasks waiting at a gate, and the inclusion of
// gates is deliberate: a gate is a planned pause, but a planned pause nobody
// answers for six hours is indistinguishable from a stall to the person whose
// work is waiting behind it.
//
// A patience of zero returns everything currently stopped, which is what a
// `--all` listing wants.
//
// This is the watchdog's whole mechanism. It is a query, not a loop — whatever
// wants to poll decides how often, and a query keeps the clock in exactly one
// place.
func (s *Store) Stalled(patience time.Duration) ([]Stuck, error) {
	ids, err := s.Tasks()
	if err != nil {
		return nil, err
	}

	now := s.now()
	var stuck []Stuck

	for _, id := range ids {
		state, err := s.ReplayOwnFlow(id)
		// Same reasoning as AwaitingGate: an unreadable task must not hide every
		// other one. `luna flow check` is where those surface by name.
		if errors.Is(err, ErrFlowChanged) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !state.NeedsHuman() {
			continue
		}

		at, err := s.lastEventAt(id)
		if err != nil {
			return nil, err
		}
		// A task with no log has no age, rather than an age measured from the
		// epoch — which would report it stuck for fifty-six years, and nothing is
		// worse for a watchdog than an alert everyone has learned to ignore.
		if at.IsZero() {
			continue
		}

		since := now.Sub(at)
		if since < patience {
			continue
		}

		stuck = append(stuck, Stuck{
			TaskID: id,
			Stage:  state.Stage,
			Status: state.Status,
			Reason: whyStopped(state),
			Since:  since,
		})
	}
	return stuck, nil
}

// lastEventAt is when the task last moved. A zero time means there is no log to
// read it from — an id nobody has written to.
func (s *Store) lastEventAt(taskID string) (time.Time, error) {
	var at int64
	row := s.db.QueryRow(`SELECT COALESCE(MAX(at), 0) FROM events WHERE task_id = ?`, taskID)
	if err := row.Scan(&at); err != nil {
		return time.Time{}, fmt.Errorf("reading when %s last moved: %w", taskID, err)
	}
	if at == 0 {
		return time.Time{}, nil
	}
	return time.Unix(at, 0), nil
}

// whyStopped is what to tell a person, whichever way the task stopped.
func whyStopped(state fsm.TaskState) string {
	if state.Blocked != "" {
		return state.Blocked
	}
	if state.Gate != nil && state.Gate.Reason != "" {
		return state.Gate.Reason
	}
	return "waiting on a person"
}

// round trims a duration to something a person reads. "3h12m" rather than
// "3h12m7.4381s".
func round(d time.Duration) time.Duration {
	switch {
	case d >= time.Hour:
		return d.Round(time.Minute)
	case d >= time.Minute:
		return d.Round(time.Second)
	default:
		return d.Round(time.Second)
	}
}
